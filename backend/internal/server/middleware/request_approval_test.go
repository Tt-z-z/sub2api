package middleware

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/requestapproval"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestApprovalZeroUpstream(t *testing.T) {
	for _, mode := range []string{"approve", "reject", "cancel", "lease", "failure", "other-key", "background", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			m := miniredis.RunT(t)
			m.SetTime(time.Now())
			rc := redis.NewClient(&redis.Options{Addr: m.Addr(), MaxRetries: -1})
			defer rc.Close()
			store := &requestapproval.Store{Client: rc, Namespace: "test"}
			a := &RequestApproval{Store: store}
			a.UseKeyID(21)
			var upstream atomic.Int32
			r := gin.New()
			r.Use(func(c *gin.Context) {
				id := int64(21)
				if mode == "other-key" {
					id = 22
				}
				c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: id})
				c.Next()
			})
			path := "/v1/responses"
			if mode == "unsupported" {
				path = "/v1/live"
			}
			r.POST(path, a.Gate(), func(c *gin.Context) {
				upstream.Add(1)
				b, _ := io.ReadAll(c.Request.Body)
				c.Data(200, "application/json", b)
			})
			srv := httptest.NewServer(r)
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := `{"input":"private canary"}`
			if mode == "background" {
				body = `{"background":true}`
			}
			req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+path, strings.NewReader(body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				res, err := srv.Client().Do(req)
				if err == nil {
					io.Copy(io.Discard, res.Body)
					res.Body.Close()
				}
			}()
			if mode == "other-key" || mode == "background" || mode == "unsupported" {
				select {
				case <-done:
				case <-time.After(4 * time.Second):
					t.Fatal("request hung")
				}
				if mode == "other-key" {
					require.EqualValues(t, 1, upstream.Load())
				} else {
					require.Zero(t, upstream.Load())
				}
				return
			}
			var entry requestapproval.Entry
			require.Eventually(t, func() bool {
				items, err := store.List(context.Background())
				if err == nil && len(items) == 1 {
					entry = items[0]
					return entry.State == "pending"
				}
				return false
			}, 4*time.Second, 10*time.Millisecond)
			require.Zero(t, upstream.Load())
			switch mode {
			case "approve", "reject":
				require.NoError(t, store.Decide(context.Background(), mode, entry.ID, 7))
			case "cancel":
				cancel()
			case "lease":
				m.SetTime(time.Now().Add(61 * time.Second))
			case "failure":
				m.Close()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("request hung")
			}
			if mode == "approve" {
				require.EqualValues(t, 1, upstream.Load())
				require.Error(t, store.Action(context.Background(), "approve", entry.ID, ""))
			} else {
				require.Zero(t, upstream.Load())
			}
		})
	}
}

func TestRequestApprovalEditReachesUpstream(t *testing.T) {
	m := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: m.Addr(), MaxRetries: -1})
	defer rc.Close()
	store := &requestapproval.Store{Client: rc, Namespace: "test"}
	a := &RequestApproval{Store: store}
	a.UseKeyID(21)
	var forwarded atomic.Value
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 21})
		c.Next()
	})
	r.POST("/v1/chat/completions", a.Gate(), func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		forwarded.Store(string(b))
		c.Status(204)
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	body := `{"model":"ym-gpt-6-adtra","messages":[{"role":"system","content":"系统提示不要改"},{"role":"user","content":"<user_query>\n今天天气怎么样\n</user_query>"}]}`
	req, _ := http.NewRequest("POST", srv.URL+"/v1/chat/completions", strings.NewReader(body))
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := srv.Client().Do(req)
		if err == nil {
			res.Body.Close()
		}
	}()
	var entry requestapproval.Entry
	require.Eventually(t, func() bool {
		items, err := store.List(context.Background())
		if err == nil && len(items) == 1 && items[0].State == "pending" {
			entry = items[0]
			return true
		}
		return false
	}, 4*time.Second, 10*time.Millisecond)
	require.Contains(t, entry.Text, "今天天气怎么样")
	require.NoError(t, store.DecideRewrite(context.Background(), "approve", entry.ID, 7, `{"v":"<user_query>\n晴天\n</user_query>"}`))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("edited request hung")
	}
	got, _ := forwarded.Load().(string)
	require.Contains(t, got, "晴天")
	require.Contains(t, got, "系统提示不要改")
	require.NotContains(t, got, "今天天气怎么样")
	audit, err := rc.XRange(context.Background(), "request-approval:{test}:audit:v1", "-", "+").Result()
	require.NoError(t, err)
	require.NotContains(t, fmt.Sprint(audit[0].Values), "晴天")
}

func TestRequestApprovalSlowBodyCancelled(t *testing.T) {
	m := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: m.Addr(), MaxRetries: -1})
	defer rc.Close()
	a := &RequestApproval{Store: &requestapproval.Store{Client: rc, Namespace: "test"}}
	a.UseKeyID(21)
	var upstream atomic.Int32
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 21}) })
	r.POST("/v1/responses", a.Gate(), func(c *gin.Context) { upstream.Add(1) })
	srv := httptest.NewServer(r)
	defer srv.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/v1/responses", reader)
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, _ := srv.Client().Do(req)
		if res != nil {
			res.Body.Close()
		}
	}()
	_, err := writer.Write([]byte(`{"input":"`))
	require.NoError(t, err)
	cancel()
	writer.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("slow request did not cancel")
	}
	require.Zero(t, upstream.Load())
}

func TestRequestApprovalJWTOnly(t *testing.T) {
	for _, method := range []string{"", "admin_api_key", "jwt"} {
		r := gin.New()
		r.GET("/", func(c *gin.Context) { c.Set("auth_method", method) }, AdminJWTOnly(), func(c *gin.Context) { c.Status(204) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if method == "jwt" {
			require.Equal(t, 204, w.Code)
		} else {
			require.Equal(t, 403, w.Code)
		}
	}
}
func TestRequestApprovalConfig(t *testing.T) {
	t.Setenv("REQUEST_APPROVAL_ENABLED", "false")
	a, err := NewRequestApproval(nil, nil)
	require.NoError(t, err)
	require.Nil(t, a)
	t.Setenv("REQUEST_APPROVAL_ENABLED", "true")
	t.Setenv("REQUEST_APPROVAL_ENV", "prod")
	_, err = NewRequestApproval(nil, nil)
	require.Error(t, err)
	t.Setenv("REQUEST_APPROVAL_ENV", "test")
	t.Setenv("REQUEST_APPROVAL_KEY_ID", "0")
	_, err = NewRequestApproval(nil, nil)
	require.Error(t, err)
}

func TestRequestApprovalRoutes(t *testing.T) {
	for _, p := range []string{"/v1/live", "/v1/realtime", "/responses", "/backend-api/codex/:call_id", "/images/generations/async", "/v1/images/batches", "/v1/videos", "/v3/contents/generations/tasks", "/v1beta/cachedContents"} {
		require.False(t, approvalSupported("GET", p))
		if p != "/responses" {
			require.False(t, approvalSupported("POST", p))
		}
	}
	for _, p := range []string{"/v1/responses", "/responses", "/backend-api/codex/responses", "/antigravity/v1/messages", "/v1beta/models/*modelAction", "/antigravity/v1beta/models/*modelAction"} {
		require.True(t, approvalSupported("POST", p))
	}
}
