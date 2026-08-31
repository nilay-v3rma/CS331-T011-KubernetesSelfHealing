package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
)

func main() {
	nodeName := os.Getenv("NODE_NAME")
	podIP := os.Getenv("POD_IP")
	if nodeName == "" {
		nodeName = "localhost"
	}
	if podIP == "" {
		podIP = "127.0.0.1"
	}

	log.Printf("selfheal-agent stub starting on node=%s podIP=%s\n", nodeName, podIP)

	// Data port: TCP listener on :9376 (probe target)
	go func() {
		ln, err := net.Listen("tcp", ":9376")
		if err != nil {
			log.Fatalf("Failed to listen on data port 9376: %v", err)
		}
		log.Println("Data port listening on :9376")
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("Data port accept error: %v", err)
				continue
			}
			conn.Close() // just accept and close, enough for TCP probe
		}
	}()

	// Control port: HTTP on :8080
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","nodeName":%q,"podIP":%q}`, nodeName, podIP)
	})

	http.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"success":true,"lossRate":0,"rttMillis":0.1,"error":""}`)
	})

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "# HELP stub_up Stub agent is running")
		fmt.Fprintln(w, "# TYPE stub_up gauge")
		fmt.Fprintln(w, "stub_up 1")
	})

	log.Println("Control port listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Failed to start control server: %v", err)
	}
}
