package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

// [P3] Probe orchestration: call the agent HTTP endpoint and decode the contract.ProbeResult.
func probeEndpoint(ctx context.Context, controlURL, targetIP string, timeout time.Duration, count, payloadBytes int) (contract.ProbeResult, error) {
	if count <= 0 {
		count = 5
	}
	if payloadBytes <= 0 {
		payloadBytes = 1024
	}
	url := fmt.Sprintf("%s/probe?target=%s&timeout=%s&count=%d&payloadBytes=%d&type=tcp", strings.TrimRight(controlURL, "/"), targetIP, timeout.String(), count, payloadBytes)

	// Allow the agent's data-plane probe to reach its timeout and return a result.
	// The control request needs a small grace period so a probe timeout is not
	// misclassified as an agent/control-channel failure.
	ctx, cancel := context.WithTimeout(ctx, timeout*time.Duration(count)+time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return contract.ProbeResult{}, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return contract.ProbeResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return contract.ProbeResult{}, fmt.Errorf("probe request failed with status %d", resp.StatusCode)
	}

	var result contract.ProbeResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return contract.ProbeResult{}, err
	}
	return result, nil
}
