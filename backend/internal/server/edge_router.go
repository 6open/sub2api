package server

import (
	"context"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/edgenode"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func edgeAdminGate(c *gin.Context) {
	k, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || k.User == nil || k.User.ID != edgenode.AdminID || !k.User.IsAdmin() ||
		k.Group == nil || k.Group.Platform != service.PlatformOpenAI {
		c.AbortWithStatusJSON(403, gin.H{"error": gin.H{"type": "permission_error", "message": "Admin-only OpenAI pilot"}})
		return
	}
	c.Next()
}

func setupEdgeRouter(r *gin.Engine, h *handler.Handlers, auth middleware.APIKeyAuthMiddleware, cfg *config.Config, redisClient *redis.Client) *gin.Engine {
	slots := make(chan struct{}, 10)
	r.Use(middleware.SessionBindingContext(cfg))
	r.Use(middleware.RequestBodyLimit(edgenode.MaxBodyBytes))
	r.Use(middleware.ClientRequestID())
	r.Use(handler.InboundEndpointMiddleware())
	probe := func(ctx context.Context) bool {
		return edgenode.AccountingReady() && redisClient != nil && redisClient.Ping(ctx).Err() == nil && edgenode.CheckDatabase(ctx) == nil
	}
	r.GET("/health", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		if !probe(ctx) {
			c.JSON(503, gin.H{"status": "dependencies_unavailable"})
			return
		}
		c.JSON(200, gin.H{"status": "ok", "node": "bwg", "pilot": "admin"})
	})
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) {
		if !edgenode.MemoryAvailable() {
			c.AbortWithStatusJSON(503, gin.H{"error": "node_memory_pressure"})
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			c.AbortWithStatusJSON(503, gin.H{"error": "node_busy"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		ready := probe(ctx)
		cancel()
		if !ready {
			c.AbortWithStatusJSON(503, gin.H{"error": "dependencies_unavailable"})
			return
		}
		c.Next()
	})
	v1.Use(gin.HandlerFunc(auth), edgeAdminGate)
	v1.GET("/models", func(c *gin.Context) {
		if c.Query("client_version") != "" {
			h.OpenAIGateway.CodexModels(c)
			return
		}
		h.Gateway.Models(c)
	})
	v1.POST("/responses", h.OpenAIGateway.Responses)
	r.NoRoute(func(c *gin.Context) { c.AbortWithStatus(http.StatusNotFound) })
	return r
}
