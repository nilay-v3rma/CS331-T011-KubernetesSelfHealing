package agent

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

type ProbeOptions struct {
	Target    string
	Timeout   time.Duration
	Count     int
	Probetype string
}

func ParseProbeOptions(query url.Values) (ProbeOptions, error) {
	target := query.Get("target")

	if target == "" {
		return ProbeOptions{}, fmt.Errorf("missing required parameter 'target'")
	}

	if !strings.Contains(target, ":") {
		target = net.JoinHostPort(target, "9376")
	}

	timeout := 2 * time.Second
	timeoutStr := query.Get("timeout")

	if timeoutStr != "" {
		if parsedTimeout, err := time.ParseDuration(timeoutStr); err != nil && parsedTimeout > 0 {
			timeout = parsedTimeout
		}
	}

	count := 1
	countStr := query.Get("count")

	if countStr != "" {
		if parsedCount, err := strconv.Atoi(countStr); err == nil && parsedCount > 0 {
			count = parsedCount
		}
	}

	probeType := strings.ToLower(query.Get("type"))

	if probeType == "" {
		probeType = "tcp"
	}

	return ProbeOptions{
		Target:    target,
		Timeout:   timeout,
		Count:     count,
		Probetype: probeType,
	}, nil
}

func probeTCP(target string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()

	conn, err := net.DialTimeout("tcp", target, timeout)

	if err != nil {
		return 0, err
	}

	conn.Close()

	return time.Since(start), nil
}

func ExecuteProbe(ctx context.Context, opts ProbeOptions) contract.ProbeResult {
	if opts.Count <= 0 {
		opts.Count = 1
	}

	var successfulProbes int
	var totalRTT time.Duration
	var lastErr error

	for i := 0; i < opts.Count; i++ {
		select {
		case <-ctx.Done():
			return contract.ProbeResult{
				Success:    false,
				LossRate:   1.0,
				Error:      "probe context cancelled or timed out",
				AgentError: true,
			}
		default:
		}

		var err error
		var rtt time.Duration

		if opts.Probetype == "tcp" {
			rtt, err = probeTCP(opts.Target, opts.Timeout)
		} else {
			err = fmt.Errorf("unsupported probe type: %s", opts.Probetype)
		}

		if err == nil {
			successfulProbes++
			totalRTT += rtt
		} else {
			lastErr = err
		}
	}

	failedProbes := opts.Count - successfulProbes
	lossRate := float64(failedProbes) / float64(opts.Count)
	success := successfulProbes > 0 && lossRate < 1.0
	var avgRTTMillis float64
	if successfulProbes > 0 {
		avgRTTMillis = float64(totalRTT.Microseconds()) / 1000.0
	}
	errStr := ""
	if lastErr != nil {
		errStr = lastErr.Error()
	}
	return contract.ProbeResult{
		Success:    success,
		LossRate:   lossRate,
		RTTMillis:  avgRTTMillis,
		Error:      errStr,
		AgentError: false,
	}
}
