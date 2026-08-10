//go:build integration

package h2fingerprint

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// TestClient_RoundTripOverH2_WithRealUTLSHandshake drives a request through the
// REAL utls handshake built by NewClient (unlike TestClient_RoundTripOverH2,
// which swaps in a stdlib tls handshake). It proves three things about the
// production path:
//
//   - the request does not panic when the utls conn is handed to the HTTP/2
//     layer (a type assertion to *tls.Conn / reqtls.Conn would blow up here),
//   - ALPN h2 is actually negotiated and used (resp.Proto == HTTP/2.0), which
//     requires NegotiatedProtocolIsMutual to be true in the translated state,
//   - RootCAs is honored so self-signed upstream certs (proxies, dev) work.
//
// Run with: go test -tags=integration -v ./internal/pkg/h2fingerprint/...
func TestClient_RoundTripOverH2_WithRealUTLSHandshake(t *testing.T) {
	cert, key := mustGenerateTestCert(t, "localhost")
	tlsCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(cert)

	var sawH2 atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 {
			sawH2.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"proto":%q}`, r.Proto)
	})

	srv := &http.Server{
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			NextProtos:   []string{"h2", "http/1.1"},
		},
	}
	if err := http2.ConfigureServer(srv, &http2.Server{}); err != nil {
		t.Fatalf("ConfigureServer: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer lis.Close()

	go func() { _ = srv.ServeTLS(lis, "", "") }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	// The default Options{} ALPN is ["h2","http/1.1"]; root trust comes from
	// Options.RootCAs instead of a handshake override.
	c, err := NewClient(Options{Timeout: 5 * time.Second, RootCAs: pool})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	srvURL := fmt.Sprintf("https://%s/", lis.Addr().String())
	resp, err := c.R().SetHeader("User-Agent", "claude-cli/2.1.81-utls").Get(srvURL)
	if err != nil {
		t.Fatalf("GET %s: %v", srvURL, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, resp.String())
	}
	if resp.Proto != "HTTP/2.0" {
		t.Errorf("client thinks it spoke %s, want HTTP/2.0 — h2 was not negotiated over the utls handshake", resp.Proto)
	}
	if !sawH2.Load() {
		t.Error("server did not observe an HTTP/2 request — ALPN negotiation likely fell back to h1")
	}
	t.Logf("response body: %s", resp.String())
}
