package routes

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterCommonRoutes 注册通用路由（健康检查、状态等）
func RegisterCommonRoutes(r *gin.Engine) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Claude Code 遥测日志（忽略，直接返回200）
	// 覆盖历史 v1 路径（/api/event_logging/batch）与当前 CLI（2.1.226+）使用的
	// v2 路径（/api/event_logging/v2/batch）。两条均为独立静态路由，段数不同不冲突；
	// 若上游后续再出 v3，按此追加一行即可。
	swallowTelemetry := func(c *gin.Context) {
		c.Status(http.StatusOK)
	}
	r.POST("/api/event_logging/batch", swallowTelemetry)
	r.POST("/api/event_logging/v2/batch", swallowTelemetry)

	// Setup status endpoint (always returns needs_setup: false in normal mode)
	// This is used by the frontend to detect when the service has restarted after setup
	r.GET("/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"needs_setup": false,
				"step":        "completed",
			},
		})
	})
}
