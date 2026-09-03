package contract

// [P3] This contract is the shared API for the operator, agent, and classifier.
// The operator must build a Matrix of source->destination ProbeResults and pass it to the analysis layer.

// ProbeResult is the response from the agent's /probe endpoint. Frozen.
type ProbeResult struct {
	Success       bool    `json:"success"`
	LossRate      float64 `json:"lossRate"`
	RTTMillis     float64 `json:"rttMillis"`
	RTTMinMillis  float64 `json:"rttMinMillis,omitempty"`
	RTTMaxMillis  float64 `json:"rttMaxMillis,omitempty"`
	JitterMillis  float64 `json:"jitterMillis,omitempty"`
	BurstLossMax  int     `json:"burstLossMax,omitempty"`
	ThroughputBPS float64 `json:"throughputBps,omitempty"`
	BandwidthBPS  float64 `json:"bandwidthBps,omitempty"`
	BytesSent     int64   `json:"bytesSent,omitempty"`
	ProbeCount    int     `json:"probeCount,omitempty"`
	Successful    int     `json:"successfulProbes,omitempty"`
	DurationMillis float64 `json:"durationMillis,omitempty"`
	Error          string  `json:"error,omitempty"`
	AgentError     bool    `json:"agentError"` // true = control channel failed, NOT a data plane fault
}

// Endpoint represents a single agent pod in the mesh.
type Endpoint struct {
	NodeName   string `json:"nodeName"`
	PodIP      string `json:"podIP"`
	ControlURL string `json:"controlURL"`
}

// Matrix maps "srcNode->dstNode" to the probe result for that pair.
type Matrix map[string]ProbeResult

// Classification is the blast-radius verdict from the classifier.
type Classification string

const (
	Healthy            Classification = "Healthy"
	PairLocalFailure   Classification = "PairLocalFailure"
	NodeIngressFailure Classification = "NodeIngressFailure"
	NodeEgressFailure  Classification = "NodeEgressFailure"
	NodeIsolated       Classification = "NodeIsolated"
	ClusterPartition   Classification = "ClusterPartition"
	PolicyScoped       Classification = "PolicyScopedFailure"
	Unknown            Classification = "Unknown"
)

// Verdict is the output of the analysis library's Evaluate function.
type Verdict struct {
	Class        Classification `json:"classification"`
	Confidence   float64        `json:"confidence"`
	SuspectNodes []string       `json:"suspectNodes"`
	Summary      string         `json:"summary"`
}
