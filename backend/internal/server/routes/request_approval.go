package routes

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

func RegisterRequestApprovalRoutes(v1 *gin.RouterGroup, auth middleware.AdminAuthMiddleware, a *middleware.RequestApproval) {
	if a == nil {
		return
	}
	g := v1.Group("/admin/request-approvals", gin.HandlerFunc(auth), middleware.AdminJWTOnly())
	g.GET("", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		items, err := a.Store.List(ctx)
		if err != nil {
			c.AbortWithStatus(503)
			return
		}
		c.JSON(200, gin.H{"items": items, "key_id": a.CurrentKeyID(), "environment": "test", "server_time": time.Now().UnixMilli()})
	})
	g.PUT("/target", func(c *gin.Context) {
		var body struct {
			Key string `json:"key"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.AbortWithStatus(400)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		id, err := a.SetProtectedKey(ctx, body.Key)
		if err != nil {
			c.AbortWithStatus(400)
			return
		}
		c.JSON(200, gin.H{"key_id": id})
	})
	for _, action := range []string{"approve", "reject"} {
		action := action
		g.POST("/:id/"+action, func(c *gin.Context) {
			id := c.Param("id")
			if _, err := uuid.Parse(id); err != nil {
				c.AbortWithStatus(404)
				return
			}
			rewrite := ""
			if action == "approve" {
				var edited struct {
					Text *string `json:"text"`
				}
				raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 48_000))
				if err != nil {
					c.AbortWithStatus(400)
					return
				}
				if len(strings.TrimSpace(string(raw))) > 0 {
					if json.Unmarshal(raw, &edited) != nil {
						c.AbortWithStatus(400)
						return
					}
					if edited.Text != nil {
						if utf8.RuneCountInString(*edited.Text) > 8000 || len(*edited.Text) > 32_000 {
							c.AbortWithStatus(400)
							return
						}
						payload, err := json.Marshal(struct {
							V string `json:"v"`
						}{V: *edited.Text})
						if err != nil {
							c.AbortWithStatus(400)
							return
						}
						rewrite = string(payload)
					}
				}
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
			defer cancel()
			subject, ok := middleware.GetAuthSubjectFromContext(c)
			if !ok || subject.UserID <= 0 {
				c.AbortWithStatus(403)
				return
			}
			if a.Store.DecideRewrite(ctx, action, id, subject.UserID, rewrite) != nil {
				c.AbortWithStatus(409)
				return
			}
			c.JSON(200, gin.H{"ok": true})
		})
	}
}
