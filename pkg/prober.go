package agent

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

type ProbeOptions struct {
	Target       string
	Timeout      time.Duration
	Count        int
	PayloadBytes int
	Probetype    string
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
		if parsedTimeout, err := time.ParseDuration(timeoutStr); err == nil && parsedTimeout > 0 {
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

	payloadBytes := 1024
	payloadStr := query.Get("payloadBytes")

	if payloadStr != "" {
		if parsedPayloadBytes, err := strconv.Atoi(payloadStr); err == nil && parsedPayloadBytes > 0 {
			payloadBytes = parsedPayloadBytes
		}
	}

	probeType := strings.ToLower(query.Get("type"))

	if probeType == "" {
		probeType = "tcp"
	}

	return ProbeOptions{
		Target:       target,
		Timeout:      timeout,
		Count:        count,
		PayloadBytes: payloadBytes,
		Probetype:    probeType,
	}, nil
}

func probeTCP(target string, timeout time.Duration, payloadBytes int) (time.Duration, int, error) {
	start := time.Now()

	conn, err := net.DialTimeout("tcp", target, timeout)

	if err != nil {
		return 0, 0, err
	}

	_ = conn.SetDeadline(time.Now().Add(timeout))
	written := 0
	if payloadBytes > 0 {
		payload := make([]byte, payloadBytes)
		written, err = conn.Write(payload)
	}
	conn.Close()

	if err != nil {
		return 0, written, err
	}

	return time.Since(start), written, nil
}

func ExecuteProbe(ctx context.Context, opts ProbeOptions) contract.ProbeResult {
	if opts.Count <= 0 {
		opts.Count = 1
	}
	if opts.PayloadBytes <= 0 {
		opts.PayloadBytes = 1024
	}

	var successfulProbes int
	var totalRTT time.Duration
	var totalBytes int64
	var lastErr error
	rtts := make([]time.Duration, 0, opts.Count)
	currentLossBurst := 0
	maxLossBurst := 0
	probeStarted := time.Now()

	for i := 0; i < opts.Count; i++ {
		select {
		case <-ctx.Done():
			return contract.ProbeResult{
				Success:      false,
				LossRate:     1.0,
				BurstLossMax: opts.Count - i,
				ProbeCount:   opts.Count,
				Successful:   successfulProbes,
				Error:        "probe context cancelled or timed out",
				AgentError:   true,
			}
		default:
		}

		var err error
		var rtt time.Duration
		var bytesSent int

		if opts.Probetype == "tcp" {
			rtt, bytesSent, err = probeTCP(opts.Target, opts.Timeout, opts.PayloadBytes)
		} else {
			err = fmt.Errorf("unsupported probe type: %s", opts.Probetype)
		}

		if err == nil {
			successfulProbes++
			totalRTT += rtt
			totalBytes += int64(bytesSent)
			rtts = append(rtts, rtt)
			currentLossBurst = 0
		} else {
			lastErr = err
			currentLossBurst++
			if currentLossBurst > maxLossBurst {
				maxLossBurst = currentLossBurst
			}
		}
	}

	failedProbes := opts.Count - successfulProbes
	lossRate := float64(failedProbes) / float64(opts.Count)
	success := successfulProbes > 0 && lossRate < 1.0
	var avgRTTMillis, minRTTMillis, maxRTTMillis, jitterMillis float64
	if successfulProbes > 0 {
		avgRTT := totalRTT / time.Duration(successfulProbes)
		avgRTTMillis = float64(avgRTT.Microseconds()) / 1000.0
		minRTT := rtts[0]
		maxRTT := rtts[0]
		var variance float64
		for _, rtt := range rtts {
			if rtt < minRTT {
				minRTT = rtt
			}
			if rtt > maxRTT {
				maxRTT = rtt
			}
			deltaMillis := float64((rtt - avgRTT).Microseconds()) / 1000.0
			variance += deltaMillis * deltaMillis
		}
		minRTTMillis = float64(minRTT.Microseconds()) / 1000.0
		maxRTTMillis = float64(maxRTT.Microseconds()) / 1000.0
		jitterMillis = math.Sqrt(variance / float64(successfulProbes))
	}
	durationMillis := float64(time.Since(probeStarted).Microseconds()) / 1000.0
	throughputBPS := 0.0
	if durationMillis > 0 {
		throughputBPS = float64(totalBytes) / (durationMillis / 1000.0)
	}
	errStr := ""
	if lastErr != nil {
		errStr = lastErr.Error()
	}
	return contract.ProbeResult{
		Success:        success,
		LossRate:       lossRate,
		RTTMillis:      avgRTTMillis,
		RTTMinMillis:   minRTTMillis,
		RTTMaxMillis:   maxRTTMillis,
		JitterMillis:   jitterMillis,
		BurstLossMax:   maxLossBurst,
		ThroughputBPS:  throughputBPS,
		BandwidthBPS:   throughputBPS,
		BytesSent:      totalBytes,
		ProbeCount:     opts.Count,
		Successful:     successfulProbes,
		DurationMillis: durationMillis,
		Error:          errStr,
		AgentError:     false,
	}
}
