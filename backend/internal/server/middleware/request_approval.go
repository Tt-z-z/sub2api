package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/requestapproval"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type RequestApproval struct {
	Store *requestapproval.Store
	keys  *service.APIKeyService
	keyID atomic.Int64
}

const approvalTargetKey = "request-approval:test:target"

// Explicitly test-only: production enablement requires a separately reviewed rollout.
// Disabled is deployment isolation, not an error fallback after protection is enabled.
func NewRequestApproval(client redis.UniversalClient, keys *service.APIKeyService) (*RequestApproval, error) {
	enabled := os.Getenv("REQUEST_APPROVAL_ENABLED")
	if enabled == "" || enabled == "false" {
		return nil, nil
	}
	if enabled != "true" || os.Getenv("REQUEST_APPROVAL_ENV") != "test" {
		return nil, errors.New("approval requires explicit test environment")
	}
	if keys == nil || client == nil {
		return nil, errors.New("invalid approval configuration")
	}
	rawID := strings.TrimSpace(os.Getenv("REQUEST_APPROVAL_KEY_ID"))
	var id int64
	if rawID != "" {
		var err error
		id, err = strconv.ParseInt(rawID, 10, 64)
		if err != nil || id < 0 {
			return nil, errors.New("invalid approval configuration")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if client.Ping(ctx).Err() != nil {
		return nil, errors.New("approval Redis unavailable")
	}
	approval := &RequestApproval{Store: &requestapproval.Store{Client: client, Namespace: "test"}, keys: keys}
	if id == 0 {
		saved, err := client.Get(ctx, approvalTargetKey).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, errors.New("approval target unavailable")
		}
		if saved != "" {
			id, err = strconv.ParseInt(saved, 10, 64)
			if err != nil || id <= 0 {
				return nil, errors.New("approval target unavailable")
			}
		}
	}
	if id > 0 {
		if err := approval.activate(ctx, id); err != nil {
			return nil, err
		}
	}
	return approval, nil
}

func (a *RequestApproval) UseKeyID(id int64) {
	if a != nil {
		a.keyID.Store(id)
	}
}

func (a *RequestApproval) CurrentKeyID() int64 {
	if a == nil {
		return 0
	}
	return a.keyID.Load()
}

func (a *RequestApproval) activate(ctx context.Context, id int64) error {
	key, err := a.keys.GetByID(ctx, id)
	if err != nil || key == nil || !key.IsActive() || key.IsExpired() {
		return errors.New("approval key must exist and be active")
	}
	if err := a.Store.Client.Set(ctx, approvalTargetKey, strconv.FormatInt(id, 10), 0).Err(); err != nil {
		return errors.New("approval target unavailable")
	}
	a.keyID.Store(id)
	return nil
}

// SetProtectedKey 按管理员粘贴的完整密钥切换保护对象。空值表示当前不保护任何密钥。
func (a *RequestApproval) SetProtectedKey(ctx context.Context, raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if err := a.Store.Client.Del(ctx, approvalTargetKey).Err(); err != nil {
			return 0, errors.New("approval target unavailable")
		}
		a.keyID.Store(0)
		return 0, nil
	}
	key, err := a.keys.GetByKey(ctx, raw)
	if err != nil || key == nil || !key.IsActive() || key.IsExpired() {
		return 0, errors.New("approval key must exist and be active")
	}
	if err := a.activate(ctx, key.ID); err != nil {
		return 0, err
	}
	return key.ID, nil
}

// Conservative allowlist: unknown/background/session routes fail closed for this key.
// Keep aliases on the same rules; do not approve an entire persistent session.
func approvalSupported(method, path string) bool {
	if method != "POST" {
		return false
	}
	switch path {
	case "/v1/messages", "/antigravity/v1/messages", "/v1/messages/count_tokens", "/messages/count_tokens", "/antigravity/v1/messages/count_tokens",
		"/v1/chat/completions", "/chat/completions", "/v1/responses", "/responses", "/backend-api/codex/responses",
		"/v1/embeddings", "/embeddings", "/v1/images/generations", "/images/generations", "/v1/images/edits", "/images/edits":
		return true
	// Gemini wildcard is narrowed further against the actual action below.
	case "/v1beta/models/*modelAction", "/antigravity/v1beta/models/*modelAction":
		return true
	}
	return false
}

func (a *RequestApproval) Gate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a == nil {
			c.Next()
			return
		}
		key, ok := GetAPIKeyFromContext(c)
		if !ok || key == nil {
			c.AbortWithStatus(401)
			return
		}
		if key.ID != a.CurrentKeyID() {
			c.Next()
			return
		}
		deny := func(status int) {
			c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": "approval_required", "message": "Request not admitted by test approval gate"}})
		}
		if !approvalSupported(c.Request.Method, c.FullPath()) || c.GetHeader("Upgrade") != "" {
			deny(403)
			return
		}
		if strings.Contains(c.FullPath(), "*modelAction") {
			action := c.Param("modelAction")
			if !strings.HasSuffix(action, ":generateContent") && !strings.HasSuffix(action, ":streamGenerateContent") && !strings.HasSuffix(action, ":countTokens") {
				deny(403)
				return
			}
		}
		// Background Responses must never outlive the original request.
		original := c.Request.Context()
		ctx, cancel := context.WithCancel(original)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		deadline := time.Now().Add(180 * time.Second)
		controller := http.NewResponseController(c.Writer)
		hasBody := c.Request.Body != nil && c.Request.Body != http.NoBody
		if hasBody && controller.SetReadDeadline(deadline) != nil {
			deny(503)
			return
		}
		if hasBody {
			defer controller.SetReadDeadline(time.Time{})
		}
		callctx, stop := context.WithTimeout(ctx, time.Second)
		entry, err := a.Store.Reserve(callctx, requestapproval.Entry{ID: uuid.NewString(), Owner: uuid.NewString(), Method: c.Request.Method, Route: c.FullPath(), KeyID: key.ID})
		stop()
		if err != nil {
			deny(503)
			return
		}
		defer func() {
			cleanup, done := context.WithTimeout(context.Background(), time.Second)
			defer done()
			_ = a.Store.Action(cleanup, "cancel", entry.ID, entry.Owner)
		}()
		// Heartbeat while reading/waiting. Any uncertainty cancels the request and
		// interrupts the real network read; never create a detached ReadAll goroutine.
		watchctx, watchstop := context.WithCancel(ctx)
		watchdone := make(chan struct{})
		go func() {
			defer close(watchdone)
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-watchctx.Done():
					return
				case <-ticker.C:
					if time.Now().After(deadline) {
						cancel()
						if hasBody {
							_ = controller.SetReadDeadline(time.Now())
						}
						return
					}
					hc, hstop := context.WithTimeout(watchctx, time.Second)
					_, e := a.Store.Renew(hc, entry)
					hstop()
					if e != nil {
						if watchctx.Err() != nil {
							return
						}
						cancel()
						if hasBody {
							_ = controller.SetReadDeadline(time.Now())
						}
						return
					}
				}
			}
		}()
		defer func() { watchstop(); <-watchdone }()
		// Cancellation of an HTTP/2 stream and HTTP/1 disconnect propagates here too.
		interrupt := context.AfterFunc(ctx, func() {
			if hasBody {
				_ = controller.SetReadDeadline(time.Now())
			}
		})
		defer interrupt()
		var body []byte
		if hasBody {
			body, err = io.ReadAll(io.LimitReader(c.Request.Body, 2*1024*1024+1))
		}
		if err != nil || ctx.Err() != nil {
			deny(408)
			return
		}
		if len(body) > 2*1024*1024 {
			deny(413)
			return
		}
		// JSON validation rejects background=true without exposing any input fields.
		if strings.HasSuffix(c.FullPath(), "/responses") && responseBackground(body) {
			deny(403)
			return
		}
		callctx, stop = context.WithTimeout(ctx, time.Second)
		err = a.Store.Ready(callctx, entry, len(body), approvalStoredPreview(body))
		stop()
		if err != nil {
			deny(503)
			return
		}
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				deny(408)
				return
			case <-ticker.C:
				if !time.Now().Before(deadline) {
					deny(408)
					return
				}
				callctx, stop = context.WithTimeout(ctx, time.Second)
				state, e := a.Store.Renew(callctx, entry)
				stop()
				if e != nil {
					deny(503)
					return
				}
				if state.State == "rejected" {
					deny(403)
					return
				}
				if state.State != "approved" {
					continue
				}
				// Stop renewal before consume; a consumed record disappearing must not cancel generation.
				watchstop()
				<-watchdone
				if ctx.Err() != nil || !time.Now().Before(deadline) {
					deny(408)
					return
				}
				callctx, stop = context.WithTimeout(ctx, time.Second)
				rewrite, e := a.Store.ReadRewrite(callctx, entry.ID)
				stop()
				if e != nil {
					deny(503)
					return
				}
				if rewrite != "" {
					var edited struct {
						V *string `json:"v"`
					}
					if json.Unmarshal([]byte(rewrite), &edited) != nil || edited.V == nil {
						deny(409)
						return
					}
					body, e = replaceLastInput(body, *edited.V)
					if e != nil {
						deny(409)
						return
					}
				}
				callctx, stop = context.WithTimeout(ctx, time.Second)
				e = a.Store.Action(callctx, "consume", entry.ID, entry.Owner)
				stop()
				if e != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
					deny(503)
					return
				}
				if !interrupt() && ctx.Err() != nil {
					deny(408)
					return
				}
				if hasBody && controller.SetReadDeadline(time.Time{}) != nil {
					deny(503)
					return
				}
				c.Request.Body = io.NopCloser(bytes.NewReader(body))
				// Keep the original cancellation context, but do not cap generation at 180s.
				c.Next()
				return
			}
		}
	}
}

func AdminJWTOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("auth_method") != "jwt" {
			c.AbortWithStatus(403)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}
