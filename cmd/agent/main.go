package main

import (
	"log"

	agent "github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg"
)

func main() {
	log.Println("Starting Self-Healing Network Agent...")
	server := agent.NewServer()

	server.Start()
}
