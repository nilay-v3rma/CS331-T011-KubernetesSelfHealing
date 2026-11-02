package controller

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

// WriteMetrics writes all Prometheus-formatted metrics to w.
// Metrics emitted:
//   - netprobe_manager_up
//   - netprobe_pair_reachable{src_node,dst_node}
//   - netprobe_pair_rtt_seconds{src_node,dst_node}
//   - netprobe_pair_jitter_seconds{src_node,dst_node}
//   - netprobe_pair_loss_rate{src_node,dst_node}
//   - netprobe_pair_loss_burst{src_node,dst_node}
//   - netprobe_pair_throughput_bytes_per_second{src_node,dst_node}
//   - netprobe_pair_bytes_sent{src_node,dst_node}
//   - netprobe_pair_probes_sent{src_node,dst_node}
//   - netprobe_round_duration_seconds
//   - netprobe_mesh_health{verdict}
//   - netprobe_classification{classification}  (info gauge, value 1)
//   - netprobe_classification_confidence{classification}
//   - netprobe_reachability_pairs{state}  (states: healthy, degraded, failed, nodata)
func (r *Reconciler) WriteMetrics(w io.Writer) {
	r.metricsMu.RLock()
	defer r.metricsMu.RUnlock()

	fmt.Fprintln(w, "# HELP netprobe_manager_up Manager is running")
	fmt.Fprintln(w, "# TYPE netprobe_manager_up gauge")
	fmt.Fprintln(w, "netprobe_manager_up 1")

	fmt.Fprintln(w, "# HELP netprobe_pair_reachable Directed pair reachability, 1 for reachable and 0 for failed")
	fmt.Fprintln(w, "# TYPE netprobe_pair_reachable gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_rtt_seconds Directed pair average probe RTT")
	fmt.Fprintln(w, "# TYPE netprobe_pair_rtt_seconds gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_jitter_seconds Directed pair RTT jitter")
	fmt.Fprintln(w, "# TYPE netprobe_pair_jitter_seconds gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_loss_rate Directed pair probe loss rate")
	fmt.Fprintln(w, "# TYPE netprobe_pair_loss_rate gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_loss_burst Directed pair maximum consecutive probe failures")
	fmt.Fprintln(w, "# TYPE netprobe_pair_loss_burst gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_throughput_bytes_per_second Directed pair synthetic payload throughput")
	fmt.Fprintln(w, "# TYPE netprobe_pair_throughput_bytes_per_second gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_bytes_sent Directed pair bytes sent during the latest round")
	fmt.Fprintln(w, "# TYPE netprobe_pair_bytes_sent gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_probes_sent Directed pair probes attempted during the latest round")
	fmt.Fprintln(w, "# TYPE netprobe_pair_probes_sent gauge")

	keys := make([]string, 0, len(r.lastMatrix))
	for key := range r.lastMatrix {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Counters for reachability_pairs.
	var healthy, degraded, failed, nodata int

	for _, key := range keys {
		src, dst := splitPairKey(key)
		labels := fmt.Sprintf(`src_node="%s",dst_node="%s"`, promLabel(src), promLabel(dst))
		result := r.lastMatrix[key]
		reachable := 0
		if result.Success {
			reachable = 1
		}
		fmt.Fprintf(w, "netprobe_pair_reachable{%s} %d\n", labels, reachable)
		fmt.Fprintf(w, "netprobe_pair_rtt_seconds{%s} %.9f\n", labels, result.RTTMillis/1000.0)
		fmt.Fprintf(w, "netprobe_pair_jitter_seconds{%s} %.9f\n", labels, result.JitterMillis/1000.0)
		fmt.Fprintf(w, "netprobe_pair_loss_rate{%s} %.6f\n", labels, result.LossRate)
		fmt.Fprintf(w, "netprobe_pair_loss_burst{%s} %d\n", labels, result.BurstLossMax)
		fmt.Fprintf(w, "netprobe_pair_throughput_bytes_per_second{%s} %.6f\n", labels, result.ThroughputBPS)
		fmt.Fprintf(w, "netprobe_pair_bytes_sent{%s} %d\n", labels, result.BytesSent)
		fmt.Fprintf(w, "netprobe_pair_probes_sent{%s} %d\n", labels, result.ProbeCount)

		// Classify pair state for reachability_pairs metric.
		switch {
		case result.Success && result.LossRate < 0.05:
			healthy++
		case result.Success && result.LossRate >= 0.05:
			degraded++
		case !result.Success && !result.AgentError:
			failed++
		default:
			nodata++
		}
	}

	fmt.Fprintln(w, "# HELP netprobe_round_duration_seconds Latest probe round wall-clock duration")
	fmt.Fprintln(w, "# TYPE netprobe_round_duration_seconds gauge")
	fmt.Fprintf(w, "netprobe_round_duration_seconds %.9f\n", r.lastRound.Seconds())

	fmt.Fprintln(w, "# HELP netprobe_mesh_health Mesh health, 1 when latest verdict is Healthy")
	fmt.Fprintln(w, "# TYPE netprobe_mesh_health gauge")
	meshHealthy := 0
	if r.lastVerdict.Class == contract.Healthy {
		meshHealthy = 1
	}
	fmt.Fprintf(w, "netprobe_mesh_health{verdict=\"%s\"} %d\n", promLabel(string(r.lastVerdict.Class)), meshHealthy)

	// netprobe_classification: info gauge — value 1, keyed by classification label.
	fmt.Fprintln(w, "# HELP netprobe_classification Latest classification (info gauge, value 1)")
	fmt.Fprintln(w, "# TYPE netprobe_classification gauge")
	fmt.Fprintf(w, "netprobe_classification{classification=\"%s\"} 1\n", promLabel(string(r.lastVerdict.Class)))

	// netprobe_classification_confidence: the numeric confidence value.
	fmt.Fprintln(w, "# HELP netprobe_classification_confidence Latest classification confidence score")
	fmt.Fprintln(w, "# TYPE netprobe_classification_confidence gauge")
	fmt.Fprintf(w, "netprobe_classification_confidence{classification=\"%s\"} %.6f\n", promLabel(string(r.lastVerdict.Class)), r.lastVerdict.Confidence)

	// netprobe_reachability_pairs: per-state pair counts.
	fmt.Fprintln(w, "# HELP netprobe_reachability_pairs Number of directed pairs in each reachability state")
	fmt.Fprintln(w, "# TYPE netprobe_reachability_pairs gauge")
	fmt.Fprintf(w, "netprobe_reachability_pairs{state=\"healthy\"} %d\n", healthy)
	fmt.Fprintf(w, "netprobe_reachability_pairs{state=\"degraded\"} %d\n", degraded)
	fmt.Fprintf(w, "netprobe_reachability_pairs{state=\"failed\"} %d\n", failed)
	fmt.Fprintf(w, "netprobe_reachability_pairs{state=\"nodata\"} %d\n", nodata)
}

// promLabel sanitizes a string value for use inside a Prometheus label value.
func promLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
