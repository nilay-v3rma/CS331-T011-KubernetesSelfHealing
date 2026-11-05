package agent

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestServer builds a Server with sensible test defaults and appends any
// extra CIDRs supplied by the caller.
func newTestServer(token string, extraCIDRs ...*net.IPNet) *Server {
	s := &Server{
		ControlPort:  ":0",
		DataPort:     ":0",
		NodeName:     "test-node",
		PodIP:        "127.0.0.1",
		ProbeToken:   token,
		AllowedCIDRs: defaultAllowedCIDRs(),
	}
	if len(extraCIDRs) > 0 {
		s.AllowedCIDRs = extraCIDRs
	}
	return s
}

// loopbackCIDR parses 127.0.0.0/8 for tests that need it.
func loopbackCIDR() *net.IPNet {
	_, ipnet, _ := net.ParseCIDR("127.0.0.0/8")
	return ipnet
}

// TestServerHandleProbe401WithoutToken asserts that a missing / wrong Bearer
// token returns 401 when a token is configured.
func TestServerHandleProbe401WithoutToken(t *testing.T) {
	s := newTestServer("secret")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/probe?target=10.0.0.1:9376")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestServerHandleProbe403ForMetadataIP asserts that the cloud-metadata
// link-local address (169.254.169.254) is rejected with 403.
func TestServerHandleProbe403ForMetadataIP(t *testing.T) {
	s := newTestServer("secret")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/probe?target=169.254.169.254:80", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

// TestServerHandleProbe200ForAllowedTarget starts a real data listener on
// 127.0.0.1 and asserts that a probe against it returns 200 when 127.0.0.0/8
// is in AllowedCIDRs.
func TestServerHandleProbe200ForAllowedTarget(t *testing.T) {
	// Start a mock data-plane listener that writes ack and drains.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = c.Write([]byte{0x01})
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	// Build server with 127.0.0.0/8 in AllowedCIDRs.
	s := newTestServer("secret", loopbackCIDR())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	target := ln.Addr().String() // 127.0.0.1:<random port>
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/probe?target="+target, nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// TestServerHealthzNoAuth asserts that /healthz never requires auth.
func TestServerHealthzNoAuth(t *testing.T) {
	s := newTestServer("secret") // token configured
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
