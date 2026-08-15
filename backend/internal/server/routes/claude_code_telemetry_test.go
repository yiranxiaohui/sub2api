package routes

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type telemetryRequestSnapshot struct {
	path          string
	body          string
	readErr       error
	contentType   string
	userAgent     string
	serviceName   string
	authorization string
	apiKey        string
}

func TestClaudeCodeTelemetryProxyForwardsSupportedPathsWithoutGatewayCredentials(t *testing.T) {
	requests := make(chan telemetryRequestSnapshot, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		requests <- telemetryRequestSnapshot{
			path:          r.URL.Path,
			body:          string(body),
			readErr:       err,
			contentType:   r.Header.Get("Content-Type"),
			userAgent:     r.Header.Get("User-Agent"),
			serviceName:   r.Header.Get("X-Service-Name"),
			authorization: r.Header.Get("Authorization"),
			apiKey:        r.Header.Get("X-Api-Key"),
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "telemetry-request-id")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	t.Cleanup(upstream.Close)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	proxy := newClaudeCodeTelemetryProxy(upstream.Client(), upstream.URL)
	router.POST("/api/event_logging/batch", proxy)
	router.POST("/api/event_logging/v2/batch", proxy)

	for _, path := range []string{"/api/event_logging/batch", "/api/event_logging/v2/batch"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"events":[{"event_type":"test"}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "claude-cli/test")
		req.Header.Set("X-Service-Name", "claude-code")
		req.Header.Set("Authorization", "Bearer sub2api-secret")
		req.Header.Set("X-Api-Key", "sub2api-secret")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusAccepted, w.Code)
		require.JSONEq(t, `{"accepted":true}`, w.Body.String())
		require.Equal(t, "application/json", w.Header().Get("Content-Type"))
		require.Equal(t, "telemetry-request-id", w.Header().Get("X-Request-Id"))

		got := <-requests
		require.NoError(t, got.readErr)
		require.Equal(t, path, got.path)
		require.JSONEq(t, `{"events":[{"event_type":"test"}]}`, got.body)
		require.Equal(t, "application/json", got.contentType)
		require.Equal(t, "claude-cli/test", got.userAgent)
		require.Equal(t, "claude-code", got.serviceName)
		require.Empty(t, got.authorization)
		require.Empty(t, got.apiKey)
	}
}

func TestClaudeCodeTelemetryProxyRejectsOversizedPayload(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST(
		"/api/event_logging/batch",
		servermiddleware.RequestBodyLimit(8),
		newClaudeCodeTelemetryProxy(upstream.Client(), upstream.URL),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/event_logging/batch", strings.NewReader(`{"events":[1]}`))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	require.Zero(t, calls.Load())
}

type failingTelemetryRoundTripper struct{}

func (failingTelemetryRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("upstream unavailable")
}

func TestClaudeCodeTelemetryProxyReportsUpstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	client := &http.Client{Transport: failingTelemetryRoundTripper{}}
	router.POST(
		"/api/event_logging/v2/batch",
		newClaudeCodeTelemetryProxy(client, "https://api.anthropic.com"),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/event_logging/v2/batch", strings.NewReader(`{"events":[]}`))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.Contains(t, w.Body.String(), "telemetry upstream unavailable")
}
