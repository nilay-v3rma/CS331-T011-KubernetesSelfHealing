package controller

import (
	"context"
	"fmt"
	"sync"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

// [P3] Probe orchestration: run one bounded, directed probe round and collect its matrix.
func (r *Reconciler) runProbeRound(ctx context.Context, spec NetworkHealthCheckSpec, endpoints []contract.Endpoint) (contract.Matrix, error) {
	if len(endpoints) == 0 {
		return contract.Matrix{}, nil
	}

	maxConcurrent := spec.MaxConcurrentProbes
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}

	matrix := contract.Matrix{}
	semaphore := make(chan struct{}, maxConcurrent)
	var matrixMu sync.Mutex
	var probes sync.WaitGroup

	for i, src := range endpoints {
		for j, dst := range endpoints {
			if i == j {
				continue
			}

			probes.Add(1)
			go func(src, dst contract.Endpoint) {
				defer probes.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				key := fmt.Sprintf("%s->%s", src.NodeName, dst.NodeName)
				result, err := probeEndpoint(ctx, src.ControlURL, dst.PodIP, spec.ProbeTimeout, spec.ProbeCount, spec.ProbePayloadBytes)
				if err != nil {
					result = contract.ProbeResult{
						Success:    false,
						LossRate:   1.0,
						Error:      err.Error(),
						AgentError: true,
					}
				}

				matrixMu.Lock()
				matrix[key] = result
				matrixMu.Unlock()
			}(src, dst)
		}
	}
	probes.Wait()

	return matrix, nil
}
