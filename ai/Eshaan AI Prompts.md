# Eshaan — probe protocol and agent security

```text
You are the agent/probe owner on a 4-day viva delivery for a Kubernetes network self-healing operator. Work in the repository containing pkg/prober.go, pkg/server.go, cmd/agent/main.go, internal/controller/probe.go, and config/agent/. Read this entire prompt before editing.

CONTEXT YOU MUST UNDERSTAND
The agent exposes /probe?target=<ip:port> and the manager calls it for every source->destination pair. The current probe only dials TCP, writes 1 KB, and closes without reading anything. A listener that accepts and immediately closes is reported as success. The data listener discards bytes, so there is no acknowledgement. /probe is unauthenticated and accepts any target, which is an SSRF/scanner primitive. The agent runs with NET_ADMIN and drops NET_RAW, even though ICMP is advertised but not implemented.

The viva is in a few days. We need a probe that proves a bidirectional exchange and an agent API that cannot be used as an open scanner.

YOUR SCOPE — do only this
1. V5 bidirectional probe:
   - In startDataListener, on accept: set a deadline, write a 1-byte acknowledgement, then drain until close.
   - In probeTCP: dial with timeout, set a deadline covering write+read, write the payload, read exactly 1 byte acknowledgement with io.ReadFull, then close.
   - Return success only if the acknowledgement was read. A connect-then-close peer must fail.
   - Keep Success = successfulProbes > 0; keep LossRate as the quality signal. Do not turn partial loss into a hard failure.
2. V6 auth and SSRF guard:
   - Add PROBE_TOKEN bearer-token auth. If set, require Authorization: Bearer <token>; return 401 otherwise.
   - Add PROBE_ALLOWED_CIDRS (comma-separated, default 192.168.0.0/16,10.0.0.0/8,172.16.0.0/12).
   - Validate target: must be an IP literal with a valid port; reject loopback, link-local (169.254.0.0/16), multicast, unspecified, and any IP outside allowed CIDRs; return 403.
   - Add bounded server timeouts (ReadHeaderTimeout, ReadTimeout, WriteTimeout, IdleTimeout).
   - Refactor Server.Start so the mux is available via Handler() for httptest. Keep Start using http.Server.
3. Manager client:
   - In internal/controller/probe.go, add Authorization: Bearer $AGENT_PROBE_TOKEN when the env var is set.
   - Replace http.DefaultClient with an http.Client with a timeout; keep the per-request context timeout.
   - Keep the existing error semantics: an HTTP/control failure becomes AgentError=true, not a data-plane failure.
4. Agent manifests:
   - Add config/agent/secret.yaml with a demo token (stringData).
   - Wire PROBE_TOKEN from the Secret and PROBE_ALLOWED_CIDRS into config/agent/daemonset.yaml.
   - Remove NET_RAW. Keep NET_ADMIN only if you can justify a current need; if not, remove it and say so.
   - Do not add a new probe type.

FILES YOU OWN
pkg/prober.go
pkg/server.go
cmd/agent/main.go
internal/controller/probe.go
config/agent/daemonset.yaml
config/agent/service.yaml
config/agent/secret.yaml (new)
pkg/prober_test.go (new)
pkg/server_test.go (new)

DO NOT
- Do not edit pkg/analysis/* or pkg/contract/types.go (Anurag).
- Do not edit internal/controller/controller.go or remediation.go (Ridham).
- Do not edit config/kustomization.yaml or config/manager/deployment.yaml (Ridham; coordinate for the token).
- Do not implement HTTP/ICMP/DNS probes this week.
- Do not remove the data port or the separate control port; they are defensible design choices.

IMPLEMENTATION NOTES
- The data listener's acknowledgement must not block forever. Set a deadline and always close.
- The prober must not treat a partial write as success; read the ack.
- For tests, allow 127.0.0.0/8 explicitly via PROBE_ALLOWED_CIDRS so httptest targets work.
- The auth check belongs in a middleware; keep /healthz and /metrics accessible without the probe token (or document the choice).
- A hostname target is not allowed because it enables DNS-based bypass; the manager already passes pod IPs.
- Keep the URL query parsing and existing option names for compatibility.

TESTS AND ACCEPTANCE
- go test ./pkg/... -count=1 passes.
- New prober test: ack server succeeds; accept-then-close server fails; deadline is honored.
- New server test: 401 without token; 403 for 169.254.169.254; 200 for an allowed target with token.
- curl without token returns 401; with token and metadata IP returns 403.
- The manager still builds and its controller tests pass.
- A reviewer can ask "why read one byte?" and you answer: it proves the return path, not just the SYN/ACK.

FINAL RESPONSE
Report:
1. Files changed and why.
2. Tests run and exact results.
3. The exact curl commands and expected outputs.
4. A 10-line viva explanation of TCP probing, RTT/loss, auth, and SSRF.

TEACH ME FOR THE VIVA
Explain in plain language:
- What a TCP connect proves and what it does not.
- Why a bidirectional acknowledgement matters.
- How RTT, jitter, loss, and throughput are computed and what they mean.
- Why an unauthenticated target parameter is dangerous.
- Why NET_ADMIN/NET_RAW are privilege smells, and what the agent actually needs.
```
