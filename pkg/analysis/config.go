package analysis

// defaultDegradedLossThreshold is the policy default for DegradedLossThreshold.
// 5% is high enough that a single lost probe in a small round does not mark a
// pair degraded, yet low enough to catch loss that hurts TCP throughput. It is
// a tunable policy choice, not a universal truth.
const defaultDegradedLossThreshold = 0.05

// Config holds tunable parameters for the analysis engine.
type Config struct {
	FailureThreshold      int     // consecutive failures before Degraded (default 3)
	SuccessThreshold      int     // consecutive successes to recover (default 2)
	ConfidenceFloor       float64 // minimum confidence to emit a verdict (default 0.80)
	DegradedLossThreshold float64 // loss rate on a reachable pair that counts as degraded (default 0.05)
}

// DefaultConfig returns sensible defaults for the analysis engine.
func DefaultConfig() Config {
	return Config{
		FailureThreshold:      3,
		SuccessThreshold:      2,
		ConfidenceFloor:       0.80,
		DegradedLossThreshold: defaultDegradedLossThreshold,
	}
}
