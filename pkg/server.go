package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

type Server struct {
	ControlPort string
	DataPort    string
	NodeName    string
	PodIP       string
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

	return &Server{
		ControlPort: ":8080",
		DataPort:    ":9376",
		NodeName:    nodeName,
		PodIP:       podIP,
	}
}

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
			_, _ = io.Copy(io.Discard, conn)
		}(conn)
	}
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

func (s *Server) Start() {
	go s.startDataListener()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/probe", s.handleProbe)
	mux.HandleFunc("/metrics", s.handleMetrics)

	// [P3] This existing agent is the working backend for the operator.
	// The operator should discover agent pods, call /probe, aggregate results into a matrix, and update the CRD status.
	if err := http.ListenAndServe(s.ControlPort, mux); err != nil {
		log.Fatalf("Fatal start HTTP: %v", err)
	}
}
