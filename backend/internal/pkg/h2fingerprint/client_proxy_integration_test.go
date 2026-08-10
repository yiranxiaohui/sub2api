//go:build integration

package h2fingerprint

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// TestClient_RoundTripOverH2_ThroughConnectProxy drives the full production
// chain that the gateway h2fp path relies on: req client with a utls
// handshake, through an HTTP CONNECT proxy, negotiating HTTP/2 with the
// upstream. It proves the P0 fix end-to-end — utls + proxy + h2 all work
// together (previously EnableForceHTTP2 bypassed the proxy and panicked on the
// utls connection).
func TestClient_RoundTripOverH2_ThroughConnectProxy(t *testing.T) {
	// --- upstream h2 TLS server ---
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
		fmt.Fprintf(w, `{"proto":%q}`, r.Proto)
	})
	upstream := &http.Server{
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			NextProtos:   []string{"h2", "http/1.1"},
		},
	}
	if err := http2.ConfigureServer(upstream, &http2.Server{}); err != nil {
		t.Fatalf("ConfigureServer: %v", err)
	}
	upLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen upstream: %v", err)
	}
	defer upLis.Close()
	go func() { _ = upstream.ServeTLS(upLis, "", "") }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = upstream.Shutdown(ctx)
	}()

	// --- CONNECT proxy that relays to the upstream ---
	var proxySawConnect atomic.Bool
	proxyLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen proxy: %v", err)
	}
	defer proxyLis.Close()
	upAddr := upLis.Addr().String()
	go func() {
		for {
			conn, err := proxyLis.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				if req.Method != http.MethodConnect {
					return
				}
				proxySawConnect.Store(true)
				up, err := net.Dial("tcp", req.Host)
				if err != nil {
					return
				}
				defer up.Close()
				_, _ = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}(conn)
		}
	}()

	c, err := NewClient(Options{
		Timeout:  5 * time.Second,
		ProxyURL: "http://" + proxyLis.Addr().String(),
		RootCAs:  pool,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := c.R().SetHeader("User-Agent", "claude-cli/2.1.81-proxy").Get(fmt.Sprintf("https://%s/", upAddr))
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, resp.String())
	}
	if resp.Proto != "HTTP/2.0" {
		t.Errorf("client thinks it spoke %s, want HTTP/2.0 (through the proxy)", resp.Proto)
	}
	if !sawH2.Load() {
		t.Error("upstream did not observe an HTTP/2 request")
	}
	if !proxySawConnect.Load() {
		t.Error("proxy did not observe a CONNECT — the request bypassed the proxy")
	}
	t.Logf("response body: %s", resp.String())
}
