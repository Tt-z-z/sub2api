package requestapproval

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRequestApprovalAtomicState(t *testing.T) {
	m := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: m.Addr()})
	defer c.Close()
	s := &Store{Client: c, Namespace: "test"}
	ctx := context.Background()
	e, err := s.Reserve(ctx, Entry{ID: "one", Owner: "owner", Route: "/v1/responses"})
	require.NoError(t, err)
	require.Error(t, s.Decide(ctx, "approve", e.ID, 7))
	require.NoError(t, s.Ready(ctx, e, 123, "你好"))
	require.Error(t, s.Action(ctx, "consume", e.ID, e.Owner))
	require.NoError(t, s.Decide(ctx, "approve", e.ID, 7))
	audit, err := c.XRange(ctx, "request-approval:{test}:audit:v1", "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, audit, 1)
	require.Equal(t, map[string]interface{}{"request_id": "one", "key_id": "0", "admin_id": "7", "result": "approve"}, audit[0].Values)
	listed, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "你好", listed[0].Preview)
	require.NotContains(t, audit[0].Values, "你好")
	require.Error(t, s.Decide(ctx, "approve", e.ID, 7))
	require.Error(t, s.Action(ctx, "consume", e.ID, "wrong"))
	prod := &Store{Client: c, Namespace: "prod"}
	require.Error(t, prod.Action(ctx, "consume", e.ID, e.Owner))
	require.NoError(t, s.Action(ctx, "consume", e.ID, e.Owner))
	require.Error(t, c.HGet(ctx, "request-approval:{test}:preview:v1", "one").Err())
	require.Error(t, s.Action(ctx, "consume", e.ID, e.Owner))
	require.Error(t, s.Action(ctx, "approve", "unknown", ""))
}

func TestRequestApprovalRewriteStaysOutOfAudit(t *testing.T) {
	m := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: m.Addr()})
	defer c.Close()
	s := &Store{Client: c, Namespace: "test"}
	ctx := context.Background()
	e, err := s.Reserve(ctx, Entry{ID: "edit", Owner: "owner", KeyID: 24})
	require.NoError(t, err)
	require.NoError(t, s.Ready(ctx, e, 10, "原文"))
	require.NoError(t, s.DecideRewrite(ctx, "approve", e.ID, 7, `{"v":"晴天"}`))
	audit, err := c.XRange(ctx, "request-approval:{test}:audit:v1", "-", "+").Result()
	require.NoError(t, err)
	require.NotContains(t, audit[0].Values, "晴天")
	got, err := s.ReadRewrite(ctx, e.ID)
	require.NoError(t, err)
	require.Equal(t, `{"v":"晴天"}`, got)
	require.NoError(t, s.Action(ctx, "consume", e.ID, e.Owner))
	gone, err := s.ReadRewrite(ctx, e.ID)
	require.NoError(t, err)
	require.Empty(t, gone)
}

func TestRequestApprovalAuditFailureClosed(t *testing.T) {
	m := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: m.Addr()})
	defer c.Close()
	s := &Store{Client: c, Namespace: "test"}
	ctx := context.Background()
	e, err := s.Reserve(ctx, Entry{ID: "audit-fail", Owner: "owner", KeyID: 21})
	require.NoError(t, err)
	require.NoError(t, s.Ready(ctx, e, 1, ""))
	require.NoError(t, c.Set(ctx, "request-approval:{test}:audit:v1", "wrong-type", 0).Err())
	require.Error(t, s.Decide(ctx, "approve", e.ID, 7))
	require.Error(t, s.Action(ctx, "consume", e.ID, e.Owner))
}

func TestRequestApprovalRejectCancelLeaseDeadlineCapacity(t *testing.T) {
	for _, mode := range []string{"reject", "cancel", "lease", "deadline", "capacity", "failure"} {
		t.Run(mode, func(t *testing.T) {
			m := miniredis.RunT(t)
			m.SetTime(time.Now())
			c := redis.NewClient(&redis.Options{Addr: m.Addr(), MaxRetries: -1})
			defer c.Close()
			s := &Store{Client: c, Namespace: "test"}
			ctx := context.Background()
			e, err := s.Reserve(ctx, Entry{ID: "one", Owner: "owner"})
			require.NoError(t, err)
			require.NoError(t, s.Ready(ctx, e, 1, "正文"))
			switch mode {
			case "reject":
				require.NoError(t, s.Decide(ctx, "reject", e.ID, 7))
			case "cancel":
				require.NoError(t, s.Action(ctx, "cancel", e.ID, e.Owner))
			case "lease":
				m.SetTime(time.Now().Add(4 * time.Second))
			case "deadline":
				m.SetTime(time.Now().Add(181 * time.Second))
			case "capacity":
				for i := 0; i < 15; i++ {
					_, err = s.Reserve(ctx, Entry{ID: string(rune('a' + i)), Owner: "o"})
					require.NoError(t, err)
				}
				_, err = s.Reserve(ctx, Entry{ID: "overflow", Owner: "o"})
				require.Error(t, err)
				return
			case "failure":
				m.Close()
			}
			require.Error(t, s.Decide(ctx, "approve", e.ID, 7))
			require.Error(t, s.Action(ctx, "consume", e.ID, e.Owner))
		})
	}
}
