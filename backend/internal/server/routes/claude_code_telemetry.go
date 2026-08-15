package routes

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const claudeCodeTelemetryUpstreamBaseURL = "https://api.anthropic.com"

var claudeCodeTelemetryHTTPClient = &http.Client{Timeout: 10 * time.Second}

// newClaudeCodeTelemetryProxy forwards Claude Code's first-party telemetry to
// Anthropic. Authentication headers are deliberately omitted: clients using
// Sub2API carry a gateway credential that must never leave this service, while
// Anthropic's telemetry endpoint accepts the event envelope without it.
func newClaudeCodeTelemetryProxy(client *http.Client, upstreamBaseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "telemetry payload too large"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read telemetry payload"})
			return
		}

		target := strings.TrimRight(upstreamBaseURL, "/") + c.Request.URL.EscapedPath()
		if c.Request.URL.RawQuery != "" {
			target += "?" + c.Request.URL.RawQuery
		}
		upstreamReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			_ = c.Error(err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create telemetry request"})
			return
		}

		copyRequestHeader(upstreamReq.Header, c.Request.Header, "Content-Type")
		copyRequestHeader(upstreamReq.Header, c.Request.Header, "Content-Encoding")
		copyRequestHeader(upstreamReq.Header, c.Request.Header, "Accept")
		copyRequestHeader(upstreamReq.Header, c.Request.Header, "User-Agent")
		copyRequestHeader(upstreamReq.Header, c.Request.Header, "X-Service-Name")

		upstreamResp, err := client.Do(upstreamReq)
		if err != nil {
			_ = c.Error(err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "telemetry upstream unavailable"})
			return
		}
		defer upstreamResp.Body.Close()

		copyResponseHeader(c.Writer.Header(), upstreamResp.Header, "Content-Type")
		copyResponseHeader(c.Writer.Header(), upstreamResp.Header, "Retry-After")
		copyResponseHeader(c.Writer.Header(), upstreamResp.Header, "Request-Id")
		copyResponseHeader(c.Writer.Header(), upstreamResp.Header, "X-Request-Id")
		c.Status(upstreamResp.StatusCode)
		if _, err := io.Copy(c.Writer, upstreamResp.Body); err != nil {
			_ = c.Error(err)
		}
	}
}

func copyRequestHeader(dst, src http.Header, name string) {
	if value := src.Get(name); value != "" {
		dst.Set(name, value)
	}
}

func copyResponseHeader(dst, src http.Header, name string) {
	if value := src.Get(name); value != "" {
		dst.Set(name, value)
	}
}
