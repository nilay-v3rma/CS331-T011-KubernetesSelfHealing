# Anurag — classifier, NoData, Degraded, hysteresis

```text
You are the analysis owner on a 4-day viva delivery for a Kubernetes network self-healing operator. Work in the repository containing pkg/analysis/, pkg/contract/types.go, and contracts/fixtures/. Read this entire prompt before editing.

CONTEXT YOU MUST UNDERSTAND
The operator probes every agent-to-agent TCP path and classifies the resulting matrix. The classifier currently has two viva-killing bugs:
- An empty matrix returns Healthy with confidence 1.0 ("no probes to evaluate").
- Partial packet loss is only mentioned in a summary; the class stays Healthy. There is no Degraded class.
There is a hysteresis tracker, but it is not used by the live reconcile path. The evaluation fixture degraded-partial-loss.json currently expects Healthy.

The viva is in a few days. We are fixing correctness first, not adding ML or new probe types.

YOUR SCOPE — do only this
1. V1 NoData:
   - Add NoData Classification = "NoData" to pkg/contract/types.go.
   - In classify(), len(m)==0 returns NoData, Confidence 0.0, Summary "insufficient evidence: empty matrix".
   - Keep Unknown for agent/control-channel errors and ambiguous evidence.
2. V2 Degraded:
   - Add Degraded Classification = "Degraded".
   - Add DegradedLossThreshold float64 to analysis.Config, default 0.05.
   - In classify(), track degraded pairs when result.Success && result.LossRate >= threshold. If there are no hard failures and at least one degraded pair, return Degraded with the offending pairs in the summary and a sensible confidence.
   - If there are hard failures, keep the existing failure classification (hard failure dominates).
   - Update contracts/fixtures/degraded-partial-loss.json expectation to Degraded.
3. V3 hysteresis library:
   - Implement Tracker.Stabilize(m contract.Matrix) contract.Matrix:
     * PairHealthy + current failure -> suppress this single failure (Success=true, LossRate=0, Error="hysteresis: transient failure suppressed").
     * PairDegraded + current success -> keep failure until SuccessThreshold successes (Success=false, LossRate=1, Error="hysteresis: recovery pending").
     * Otherwise pass the real result through.
   - Keep Tracker.Update as the state machine. Add tests for: 1 failure suppressed; 3 failures surface; 1 success still degraded; 2 successes recover.
   - Do not change the public Evaluate signature.
4. Fixtures/tests:
   - Add contracts/fixtures/empty.json ({}).
   - Add contracts/fixtures/loss-40.json with multiple pairs at 40% loss and no hard failures.
   - Update classifier_test.go and hysteresis_test.go.
   - Keep all existing fixtures passing.

FILES YOU OWN
pkg/analysis/classifier.go
pkg/analysis/config.go
pkg/analysis/hysteresis.go
pkg/analysis/evaluate.go
pkg/contract/types.go
pkg/analysis/*_test.go
contracts/fixtures/*

DO NOT
- Do not edit internal/controller/* (Ridham owns the wiring), pkg/prober.go or pkg/server.go (Eeshan), hack/* (Siddhesh), or config/*.
- Do not implement NetworkIntent, eBPF, or a new ML model.
- Do not remove existing classifications or weaken tests.
- Do not make the classifier depend on wall-clock time; hysteresis state is external.

IMPLEMENTATION NOTES
- Success currently means "at least one probe succeeded"; do not change that contract. LossRate is the quality signal.
- For Degraded confidence, be defensible: a deterministic observation can use 1.0, but explain in the summary that the class is based on measured loss above threshold. Do not invent a probabilistic model.
- Stabilize must copy the matrix, not mutate the caller's map.
- Add a helper for threshold comparison with a clear comment; avoid magic numbers scattered around.
- Keep the existing structural rules (pair-local, node ingress/egress/isolated, policy-scoped, partition) intact.

TESTS AND ACCEPTANCE
- go test ./pkg/... -count=1 passes.
- go test ./pkg/analysis -run 'Empty|Degraded|Hysteresis' -v shows the new behavior.
- Empty matrix -> NoData, confidence 0.
- degraded-partial-loss.json -> Degraded.
- A single transient failure does not change the stabilized matrix to failed; three do.
- Two successes are required to recover a degraded pair.
- A reviewer can ask "why 5%?" and you answer: it is a configurable policy default, chosen to catch meaningful loss without treating one lost probe as degraded; it is not a universal truth.

FINAL RESPONSE
Report:
1. Files changed and why.
2. Tests run and exact results.
3. The new classification table (class, meaning, when emitted).
4. A 10-line viva explanation of classifier math, confidence, and hysteresis.

TEACH ME FOR THE VIVA
Explain in plain language:
- Why "no failures" is not the same as "healthy".
- The difference between NoData, Degraded, Unknown, and a hard failure.
- How consecutive failures/successes debounce flapping.
- Why partial loss must not be collapsed into a binary up/down verdict.
- What confidence does and does not mean in this system.
```
