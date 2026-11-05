package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type Server struct {
	ControlPort  string
	DataPort     string
	NodeName     string
	PodIP        string
	ProbeToken   string      // from PROBE_TOKEN env var
	AllowedCIDRs []*net.IPNet // parsed from PROBE_ALLOWED_CIDRS
}

// defaultAllowedCIDRs returns the default set of allowed CIDRs for probing.
func defaultAllowedCIDRs() []*net.IPNet {
	defaults := []string{"192.168.0.0/16", "10.0.0.0/8", "172.16.0.0/12"}
	cidrs := make([]*net.IPNet, 0, len(defaults))
	for _, s := range defaults {
		_, ipnet, err := net.ParseCIDR(s)
		if err == nil {
			cidrs = append(cidrs, ipnet)
		}
	}
	return cidrs
}

// parseAllowedCIDRs parses a comma-separated list of CIDR strings.
// Returns defaultAllowedCIDRs() if the input is empty.
func parseAllowedCIDRs(raw string) []*net.IPNet {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultAllowedCIDRs()
	}
	parts := strings.Split(raw, ",")
	cidrs := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(part)
		if err != nil {
			log.Printf("WARN: invalid CIDR %q in PROBE_ALLOWED_CIDRS, skipping: %v", part, err)
			continue
		}
		cidrs = append(cidrs, ipnet)
	}
	if len(cidrs) == 0 {
		return defaultAllowedCIDRs()
	}
	return cidrs
}

func NewServer() *Server {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		nodeName = "localhost"
	}

	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		podIP = "127.0.0.1"
	}

	probeToken := os.Getenv("PROBE_TOKEN")
	if probeToken == "" {
		log.Println("WARN: PROBE_TOKEN is not set; /probe endpoint is unauthenticated")
	}

	allowedCIDRs := parseAllowedCIDRs(os.Getenv("PROBE_ALLOWED_CIDRS"))

	return &Server{
		ControlPort:  ":8080",
		DataPort:     ":9376",
		NodeName:     nodeName,
		PodIP:        podIP,
		ProbeToken:   probeToken,
		AllowedCIDRs: allowedCIDRs,
	}
}

// startDataListener accepts TCP connections on the data port.
// For each connection it: sets a deadline, writes a 1-byte ack, drains any
// remaining data, then closes — completing the V5 bidirectional handshake.
func (s *Server) startDataListener() {
	ln, err := net.Listen("tcp", s.DataPort)
	if err != nil {
		log.Fatalf("Fatal start TCP: %v", err)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go func(conn net.Conn) {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			// V5: write ack before draining so prober can read it.
			_, _ = conn.Write([]byte{0x01})
			_, _ = io.Copy(io.Discard, conn)
		}(conn)
	}
}

// validateTarget checks that ip (in "host:port" form) is a valid, non-SSRF
// target: it must be a pure IP literal, not loopback, link-local, multicast,
// or unspecified, and it must fall within one of the allowedCIDRs.
func validateTarget(ip string, allowedCIDRs []*net.IPNet) error {
	host, _, err := net.SplitHostPort(ip)
	if err != nil {
		return fmt.Errorf("invalid target %q: %w", ip, err)
	}

	parsed := net.ParseIP(host)
	if parsed == nil {
		return fmt.Errorf("target host %q is not a valid IP literal (hostnames disallowed)", host)
	}

	if parsed.IsLoopback() {
		// Allow loopback only if it is in allowedCIDRs (tests set 127.0.0.0/8).
		inAllowed := false
		for _, cidr := range allowedCIDRs {
			if cidr.Contains(parsed) {
				inAllowed = true
				break
			}
		}
		if !inAllowed {
			return fmt.Errorf("target %q is loopback and not in allowed CIDRs", host)
		}
		return nil
	}

	// Link-local unicast (169.254.0.0/16 for IPv4, fe80::/10 for IPv6).
	if parsed.IsLinkLocalUnicast() {
		return fmt.Errorf("target %q is link-local (SSRF risk)", host)
	}
	if parsed.IsMulticast() {
		return fmt.Errorf("target %q is multicast", host)
	}
	if parsed.IsUnspecified() {
		return fmt.Errorf("target %q is unspecified", host)
	}

	for _, cidr := range allowedCIDRs {
		if cidr.Contains(parsed) {
			return nil
		}
	}
	return fmt.Errorf("target %q is not in any allowed CIDR", host)
}

// bearerAuth checks the Authorization header when a ProbeToken is configured.
// Returns true and writes a 401 response if authentication fails.
func (s *Server) bearerAuth(w http.ResponseWriter, r *http.Request) bool {
	if s.ProbeToken == "" {
		return false // auth not required
	}
	authHeader := r.Header.Get("Authorization")
	expected := "Bearer " + s.ProbeToken
	if authHeader != expected {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    false,
			"error":      "unauthorized: missing or invalid Bearer token",
			"agentError": true,
		})
		return true
	}
	return false
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	resp := map[string]string{
		"status":   "ok",
		"nodeName": s.NodeName,
		"podIP":    s.PodIP,
	}

	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	// 1. Auth check.
	if denied := s.bearerAuth(w, r); denied {
		return
	}

	opts, err := ParseProbeOptions(r.URL.Query())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    false,
			"lossRate":   1.0,
			"error":      err.Error(),
			"agentError": true,
		})
		return
	}

	// 2. SSRF guard: validate target before executing probe.
	if err := validateTarget(opts.Target, s.AllowedCIDRs); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    false,
			"lossRate":   1.0,
			"error":      fmt.Sprintf("target validation failed: %v", err),
			"agentError": true,
		})
		return
	}

	result := ExecuteProbe(r.Context(), opts)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "# HELP netprobe_agent_up Agent is running")
	fmt.Fprintln(w, "# TYPE netprobe_agent_up gauge")
	fmt.Fprintln(w, "netprobe_agent_up 1")
}

// Handler builds and returns the HTTP mux. Exposed so tests can call it
// directly via httptest.NewServer(s.Handler()).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/probe", s.handleProbe)
	mux.HandleFunc("/metrics", s.handleMetrics)
	return mux
}

func (s *Server) Start() {
	go s.startDataListener()

	srv := &http.Server{
		Addr:              s.ControlPort,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// [P3] This existing agent is the working backend for the operator.
	// The operator should discover agent pods, call /probe, aggregate results into a matrix, and update the CRD status.
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Fatal start HTTP: %v", err)
	}
}
