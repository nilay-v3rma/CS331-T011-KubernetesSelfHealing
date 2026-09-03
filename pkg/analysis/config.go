package analysis

// Config holds tunable parameters for the analysis engine.
type Config struct {
	FailureThreshold int     // consecutive failures before Degraded (default 3)
	SuccessThreshold int     // consecutive successes to recover (default 2)
	ConfidenceFloor  float64 // minimum confidence to emit a verdict (default 0.80)
}

// DefaultConfig returns sensible defaults for the analysis engine.
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		ConfidenceFloor:  0.80,
	}
}
