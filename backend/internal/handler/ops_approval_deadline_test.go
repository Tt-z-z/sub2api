package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/requestapproval"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
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

// Real wall-clock tests through the actual Gin + Ops wrapper, HTTP/1 and HTTP/2.
func TestRequestApprovalRealDeadline(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, slow := range []bool{false, true} {
			name := "http1"
			if h2 {
				name = "http2"
			}
			if slow {
				name += "_slow"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				m := miniredis.RunT(t)
				rc := redis.NewClient(&redis.Options{Addr: m.Addr()})
				defer rc.Close()
				a := &middleware.RequestApproval{Store: &requestapproval.Store{Client: rc, Namespace: "test"}}
				a.UseKeyID(21)
				var calls atomic.Int32
				r := gin.New()
				r.Use(OpsErrorLoggerMiddleware(nil))
				r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 21}) })
				r.POST("/v1/responses", a.Gate(), func(c *gin.Context) { calls.Add(1); c.Status(204) })
				srv := httptest.NewUnstartedServer(r)
				srv.EnableHTTP2 = h2
				srv.StartTLS()
				defer srv.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
				defer cancel()
				var body io.Reader = strings.NewReader(`{"input":"private"}`)
				if slow {
					reader, writer := io.Pipe()
					defer reader.Close()
					defer writer.Close()
					body = reader
					go func() { _, _ = writer.Write([]byte(`{"input":"`)) }()
				}
				req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/v1/responses", body)
				start := time.Now()
				res, err := srv.Client().Do(req)
				elapsed := time.Since(start)
				require.NoError(t, err)
				defer res.Body.Close()
				_, _ = io.Copy(io.Discard, res.Body)
				require.GreaterOrEqual(t, elapsed, 59*time.Second)
				require.Less(t, elapsed, 66*time.Second)
				require.NotEqual(t, 204, res.StatusCode)
				require.Zero(t, calls.Load())
				if h2 {
					require.Equal(t, 2, res.ProtoMajor)
				}
			})
		}
	}
}
