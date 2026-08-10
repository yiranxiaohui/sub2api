//go:build integration

package tlsfingerprint

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// TestHTTPProxyDialer_HTTPSProxy verifies that an https:// proxy URL is
// handled by first establishing TLS to the proxy (so the CONNECT request and
// everything after it are encrypted) and only then sending the CONNECT tunnel,
// followed by the utls handshake to the target. Previously the fingerprint
// path dropped https proxies entirely and fell back to the stdlib transport.
func TestHTTPProxyDialer_HTTPSProxy(t *testing.T) {
	proxyCert, proxyKey := mustGenIPCert(t)
	targetCert, targetKey := mustGenIPCert(t)

	// --- target upstream: a TLS http/1.1 server returning a canned body ---
	targetLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer targetLis.Close()
	targetAddr := targetLis.Addr().String()

	go func() {
		for {
			conn, err := targetLis.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				tlsC := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{targetCert.Raw}, PrivateKey: targetKey}}})
				_ = tlsC.Handshake()
				br := bufio.NewReader(tlsC)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				_ = req
				_, _ = tlsC.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"))
			}(conn)
		}
	}()

	// --- https proxy: TLS listener, then CONNECT over the TLS conn, then relay ---
	proxyLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	defer proxyLis.Close()
	proxyAddr := proxyLis.Addr().String()

	var proxySawTLS atomic.Bool
	var proxySawConnect atomic.Bool

	go func() {
		for {
			conn, err := proxyLis.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// The proxy itself must be TLS: accept the client's TLS handshake.
				tlsC := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{proxyCert.Raw}, PrivateKey: proxyKey}}})
				if err := tlsC.Handshake(); err != nil {
					return
				}
				proxySawTLS.Store(true)
				br := bufio.NewReader(tlsC)
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
				_, _ = tlsC.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				go func() { _, _ = io.Copy(up, tlsC) }()
				_, _ = io.Copy(tlsC, up)
			}(conn)
		}
	}()

	// --- dial through the https proxy with the fingerprint ---
	proxyPool := x509.NewCertPool()
	proxyPool.AddCert(proxyCert)
	targetPool := x509.NewCertPool()
	targetPool.AddCert(targetCert)

	dialer := NewHTTPProxyDialer(&Profile{Name: "test"}, mustParseURLHelper("https://"+proxyAddr))
	dialer.ProxyTLSConfig = &tls.Config{RootCAs: proxyPool}
	dialer.TLSConfig = &utls.Config{RootCAs: targetPool}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := dialer.DialTLSContext(ctx, "tcp", targetAddr)
	if err != nil {
		t.Fatalf("DialTLSContext through https proxy: %v", err)
	}
	defer conn.Close()

	// Send a plain HTTP/1.1 request over the fingerprinted connection (ALPN defaults to http/1.1).
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if got := string(buf[:n]); !contains(got, "hello") {
		t.Errorf("expected upstream response body, got %q", got)
	}
	if !proxySawTLS.Load() {
		t.Error("proxy did not observe a TLS handshake — the CONNECT was sent in plaintext")
	}
	if !proxySawConnect.Load() {
		t.Error("proxy did not observe a CONNECT request over the TLS tunnel")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// mustGenIPCert creates a self-signed cert valid for 127.0.0.1.
func mustGenIPCert(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	_ = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return cert, priv
}

// mustParseURLHelper parses a proxy URL for tests (the shared helper lives in
// the unit-tagged dialer_test.go, which is not compiled with -tags=integration).
func mustParseURLHelper(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// placate gofmt — fmt is used by the helpers above.
var _ = fmt.Sprintf
