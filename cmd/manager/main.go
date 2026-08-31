package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	log.Println("selfheal-manager stub starting...")

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "# HELP stub_up Stub manager is running")
		fmt.Fprintln(w, "# TYPE stub_up gauge")
		fmt.Fprintln(w, "stub_up 1")
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Listening on :%s\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
