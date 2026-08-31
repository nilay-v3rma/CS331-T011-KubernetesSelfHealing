package contract

// ProbeResult is the response from the agent's /probe endpoint. Frozen.
type ProbeResult struct {
	Success    bool    `json:"success"`
	LossRate   float64 `json:"lossRate"`
	RTTMillis  float64 `json:"rttMillis"`
	Error      string  `json:"error,omitempty"`
	AgentError bool    `json:"agentError"` // true = control channel failed, NOT a data plane fault
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
