---
phase: 2
slug: net-protocol-state-machine
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-23
---

# Phase 2 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

This phase is **~80% assembly** of existing fork primitives (framing, compression, login, status gate, packet codec are already correct for 776) plus three genuinely new pieces: the rewritten Configuration sequence, the embedded 26.2 registry NBT + its send helper, and the read/write goroutine + channel seam. Validation is dominated by **Go unit/integration tests over an in-memory `net.Pipe`** (driving the server gate with the fork's own `bot/` client as the client side), a `-race` smoke test on the network/tick seam, and — for the single highest-risk requirement NET-04 — a **capture-diff against a real vanilla 26.2 server** that no published spec can replace.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` (+ `go test -race`) over an in-memory `net.Pipe` conn pair |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go build ./... && go test ./server/... ./net/... -count=1` |
| **Full suite command** | `go vet ./... && go build ./... && go test ./... -race -count=1` |
| **Estimated runtime** | ~30–90 seconds (the NET-04 capture-diff against the real vanilla jar is a separate, gated manual step — minutes) |
| **In-memory harness** | `net.Pipe()` → wrap each end as a `*net.Conn` (`threshold:-1`); drive the server gate (`Server.AcceptConn`) on one end and the fork's `bot/` client (`bot.Client.joinConfiguration`, login, handshake) on the other. No real socket needed for NET-01..05. |

---

## Sampling Rate

- **After every task commit:** `go build ./... && go test ./server/... ./net/... -count=1`
- **After the NET-05 seam task and the config-rewrite task:** add `-race` — `go test ./server/... -race -count=1`
- **After every plan wave:** `go vet ./... && go build ./... && go test ./... -race -count=1`
- **Phase gate (before `/gsd-verify-work`):** full suite green **AND** the manual vanilla-26.2 capture-diff for NET-04 passes (a real client reaches Play, no "Loading terrain…" hang) and is signed off.
- **Max feedback latency:** 90 seconds for the automated suite (the vanilla capture-diff is excluded — it is the phase gate, not a per-task check).

---

## Per-Task Verification Map

| Req ID | Plan | Wave | Behavior | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|--------|------|------|----------|------------|-----------------|-----------|-------------------|-------------|--------|
| NET-06 | 02-01 | 1 | VarInt/VarLong framing, threshold, 2²¹ cap enforced | T-2-02 (oversized frame), T-2-03 (compression bomb) | Oversized data length (`> MaxDataLength`) and decompressed-size overrun are rejected before allocation; no panic | unit | `go test ./net/packet/ -run 'TestMaxDataLength\|TestThreshold' -count=1` | ❌ W0 (extend existing `packet_test.go`) | ⬜ pending |
| NET-05 | 02-01 | 1 | One writer goroutine; inbound packets become intents on a channel; no game-state access in net goroutines | T-2-04 (slowloris/backpressure) | Bounded outbound queue; disconnect-on-full; read goroutine touches no game state | unit + `-race` | `go test ./server/ -run 'TestSingleWriter\|TestInboundSeam' -race -count=1` | ❌ W0 | ⬜ pending |
| NET-07 | 02-01 | 1 | Disconnect packet per state carries a readable reason | T-2-05 (silent kick) | Every failure path emits a state-correct Disconnect with `chat.Message` text, never a silent close | unit | `go test ./server/ -run TestDisconnectReason -count=1` | ❌ W0 | ⬜ pending |
| NET-01 | 02-02 | 2 | Handshake asserts proto 776; non-776 login gets a readable Login Disconnect; Status still answers any protocol | T-2-01 (malformed handshake) | Login intent with `protocol != 776` → readable Login Disconnect, not a silent drop; Status path ungated | unit | `go test ./server/ -run TestHandshakeProtocol -count=1` | ❌ W0 | ⬜ pending |
| NET-02 | 02-02 | 2 | Status returns version/MOTD/player count, protocol 776 | — | Status JSON well-formed; `protocol == 776` advertised | unit | `go test ./server/ -run TestStatusPing -count=1` | ❌ W0 | ⬜ pending |
| NET-03 | 02-02 | 2 | Offline login completes; compression negotiated at threshold; LoginAck read | T-2-03 (threshold mismatch) | `SetThreshold` applied before LoginSuccess (handled by `MojangLoginHandler`); LoginAcknowledged read before Config | integration (pipe + bot) | `go test ./server/ -run TestOfflineLogin -count=1` | ❌ W0 | ⬜ pending |
| NET-04 | 02-03 | 3 | Registry Data payload carries real 26.2 NBT (incl. `overworld` dim, `plains` biome, full `damage_type`) | T-2-06 (stale-schema silent kick) | Embedded 26.2 NBT, NOT the stale `registry/codec.go` struct; round-trips through `bot/` client decode | unit | `go test ./server/registrydata/ -count=1` | ❌ W0 | ⬜ pending |
| NET-04 | 02-04 | 4 | Full Configuration sequence (Known Packs → Feature Flags → Registry Data → Update Tags → Finish, read Ack) reaches Play; no silent kick | T-2-06, T-2-05 | Known Packs precedes Registry Data; Ack read before Play; unknown config IDs are no-ops; config errors → Config Disconnect, never silent | integration + **capture-diff** | `go test ./server/ -run TestConfigSequence -race -count=1` + manual vanilla 26.2 capture-diff | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

*Existing `net/packet/packet_test.go` + `types_test.go` already cover most of NET-06 framing/compression; the Wave 0 work EXTENDS them with explicit `MaxDataLength` overflow (both compressed and uncompressed paths) and threshold-boundary cases — it does not re-implement the codec.*

---

## Wave 0 Requirements

Test scaffolds and shared fixtures that MUST exist before the corresponding implementation task runs. The implementer creates the test file (RED) before/with the implementation.

- [ ] **Shared harness:** an in-memory `net.Pipe()` helper pairing a `*net.Conn` server end with a `*net.Conn` client end (both `threshold:-1` until compression negotiated), plus a goroutine runner that drives `Server.AcceptConn` on the server end. Lives in `server/pipe_test.go` (or `server/testutil_test.go`). Reused by every NET-01..04 integration test. The fork's `bot/configuration.go` is the ready-made client side for the config leg.
- [ ] `net/packet/packet_test.go` — EXTEND with `TestMaxDataLength` (oversized uncompressed + oversized decompressed reject) and `TestThreshold` boundary (NET-06).
- [ ] `server/client_test.go` — NET-05: single-writer drain + inbound-intent channel seam; run under `-race`; assert no game-state pointer is reachable from the read/write loops.
- [ ] `server/handshake_test.go` — NET-01: protocol-776 assertion + readable Login Disconnect on mismatch.
- [ ] `server/ping_test.go` — NET-02: status JSON shape, protocol 776.
- [ ] `server/login_test.go` — NET-03: offline login + compression negotiation + LoginAck (drive with `bot/` over the pipe).
- [ ] `server/registrydata/registrydata_test.go` — NET-04 payload: embedded NBT round-trips through the registry frame and contains `minecraft:overworld` (dimension_type) and `minecraft:plains` (biome).
- [ ] `server/configuration_test.go` — NET-04 sequence: full ordered config leg over the pipe with the `bot/` client reaches the Finish ack without error.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Full Configuration → Play with no silent "Loading terrain…" hang against a **real** vanilla 26.2 client | NET-04 | No published spec covers 776; the only authoritative check is byte-comparison against the actual vanilla 26.2 server output / a real client entering Play. Automated tests verify our own encode/decode is self-consistent, not that it is vanilla-correct. | Stand up the official vanilla 26.2 server jar (cached `temp/cache/26.2-server.jar`, sha1 `823e2250…`, JDK 25). Capture its Configuration packet stream for a connecting client (logging proxy / point the fork `bot/` client at vanilla and dump every config packet). Diff Ender's Configuration byte stream against vanilla's for the same client: registry set, order, entry counts, NBT shape, Update Tags set, packet IDs. Any divergence is the bug. Then connect an unmodified vanilla 26.2 client to Ender and confirm it reaches Play (not stuck at "Loading terrain…"). |

---

## Validation Sign-Off

- [ ] Every task has an `<automated>` verify command or an explicit Wave 0 dependency (Nyquist).
- [ ] Sampling continuity: no 3 consecutive tasks without an automated build/test check.
- [ ] The NET-05 seam task and the config-rewrite task run under `-race`.
- [ ] Wave 0 stands up the `net.Pipe` harness + the per-requirement test scaffolds before implementation.
- [ ] The NET-04 capture-diff against a real vanilla 26.2 server is a required phase gate (human sign-off in 02-04).
- [ ] No watch-mode flags.
- [ ] Feedback latency < 90s for the automated suite (vanilla capture-diff excluded — it is the phase gate).
- [ ] `nyquist_compliant: true` (every NET-0x behavior maps to an automated command + a Wave 0 scaffold; NET-04's vanilla-correctness layer is the one prescribed manual gate).

**Approval:** pending
