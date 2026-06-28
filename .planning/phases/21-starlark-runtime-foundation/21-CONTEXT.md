# Phase 21: Starlark runtime foundation — Context

**Gathered:** 2026-06-27
**Status:** Ready for planning
**Source:** Operator decisions + v4-PLAN.md (plan of record)
**Requirement:** PLUGIN-01

<domain>
## Phase Boundary

**This phase delivers ONLY the runtime foundation** — the riskiest single thing in v4, proven before any host/event/behavior layer is built on top. A `.star` plugin file loads, parses, and compiles once; runs inside a sandbox; calls a registered Go builtin; returns a value to Go — all CGO_ENABLED=0 and Docker `-race` clean.

**OUT of this phase (later v4 phases):**
- Plugin manager / discovery / manifest / auto-load + the event bus → Phase 22.
- The entity/world/nav frozen-handle API + mob behavior hooks → Phase 23.
- Vanilla-mobs-as-plugins, crafting-as-plugins, Python runtime, Folia → Phases 24–27.

This phase touches NO gameplay state. The demo builtin is server-NEUTRAL (no entity/world reads) by deliberate decision — exposing real tick state is Phase 23's frozen-handle design, not Phase 21's.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Runtime + dependency
- **Embed `go.starlark.net` as a PLAIN Go dependency** (`require`), NOT a vendored fork. go.starlark.net is stable upstream. ONLY if the sandbox needs a knob upstream doesn't expose do we `replace`-pin a minimal fork — and Phase 21 must explicitly determine (during research/first integration) whether that is needed. Default expectation: plain dep, no fork.
- **CGO_ENABLED=0 is non-negotiable.** go.starlark.net is pure-Go → this is the entire reason Starlark is the core runtime. The phase gate includes `CGO_ENABLED=0 go build ./...` clean + zero `import "C"` + go.mod/go.sum carry no cgo-requiring dep. (Python/cgo is Phase 26, build-tag-gated.)

### Sandbox (the safety contract — must HOLD, tested)
- **Per-`starlark.Thread` step-counter budget** enforced (a runaway plugin loop hits the budget and errors out, does NOT hang the server).
- **Recursion OFF** — a Starlark function calling itself is a dynamic error (do NOT set `-recursion`).
- **No filesystem/network builtins** exposed unless explicitly registered by Sulfur. The default global environment is the safe subset only.
- These three are observable, tested invariants (a probe `.star` that loops forever / recurses / tries I/O is rejected).

### Concurrency model
- **One `starlark.Thread` per goroutine** (Threads are NOT shared across goroutines).
- **FrozenValues cross the tick boundary safely** — a value frozen at load is safe to READ from the tick goroutine. The phase proves a frozen global produced at load is read race-clean from a second goroutine.
- TICK-05 single-owner carries into the plugin call seam; the Docker `-race` gate covers the plugin path exactly as it covers the async subsystems today.

### Plugin loader (minimal — full manager is Phase 22)
- **Dir-based loader:** Phase 21 defines a loader that takes a path/dir and loads a `.star` from it; the TESTS load `.star` fixtures from `testdata/`. The real discovery/manifest/auto-load lands in Phase 22. (Not in-memory-only — the path/dir seam is established now so Phase 22 extends it.)
- **Load/parse/compile lifecycle once:** `starlark.ExecFile` (or the program/compile API) loads + runs a module ONCE and returns its globals; the plugin is parsed/compiled at load, not re-parsed per call. `starlark.Call(thread, fn, args, kwargs)` invokes a Starlark function from Go later.

### Demo / gate scope
- **Server-NEUTRAL builtin:** the first registered Go builtin is trivial and gameplay-free (e.g. a log/echo or a pure value-returning builtin) — it proves the Go↔Starlark bridge + sandbox + freeze + race-safety WITHOUT touching entities/world. Real server-state exposure is Phase 23's frozen-handle API.
- **Gate:** a `.star` file loads → runs sandboxed → calls the registered Go builtin → returns a value to Go → the whole path is `-race` clean and the default build is CGO=0.

### Where the code lives
- A new top-level package (likely `plugin/` or `plugin/starlark/`) — NOT inside `server/` hot-path files. The planner decides the exact package layout, but it must be isolated so the runtime is testable standalone (the gate runs without a full server).
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Plan of record
- `.planning/v4-PLAN.md` — the full v4 plan: runtime decisions (Starlark core/Python opt-in), the DECLARE-once architecture, build-order rationale, the Phase 21 row + gate, the "open decisions to resolve at Phase 21 kickoff" (fork-or-vendor — resolved above as plain-dep-unless-needed).

### Requirement
- `.planning/REQUIREMENTS.md` — PLUGIN-01 (the full success criteria).

### Project constraints (carry into the plugin layer)
- `CLAUDE.md` — the (now v4-updated) Scope line + the CGO_ENABLED=0 / 1:1-mandate / -race constraints.

### Starlark API (fetch current docs — do NOT rely on training data)
- Context7 `/google/starlark-go` — `starlark.Thread` (step budget via `Thread.Steps`/`SetMaxExecutionSteps`, recursion flag), `starlark.Freeze`/frozen values, `starlark.ExecFile`/`starlark.SourceProgram`/`Program.Init`, `starlark.Call`, `starlark.Builtin`/`NewBuiltin`, `starlark.StringDict` predeclared globals. The exact current symbol names MUST be verified against current docs during research — the sandbox knobs are the load-bearing part.
</canonical_refs>

<specifics>
## Specific Ideas

- Verify the EXACT current go.starlark.net API for: setting the per-thread step budget, disabling/keeping-disabled recursion, freezing globals, the predeclared-globals (StringDict) injection point, and the load-once program/compile path. These symbol names changed historically — research must read current docs, not assume.
- The sandbox negative tests are the proof: an infinite-loop `.star` must hit the step budget and return an error (not hang); a self-recursive `.star` must error; an I/O attempt must fail because the builtin isn't registered.
- A `-race` test that freezes a global at load and reads it from a separate goroutine concurrently with the loader goroutine.
- Determine during the FIRST integration whether any sandbox requirement forces a `replace`-fork; if plain-dep suffices, record that the fork is NOT needed (closes the open v4 decision).
</specifics>

<deferred>
## Deferred Ideas

- Plugin discovery / manifest / auto-load from a plugins dir → Phase 22.
- The event bus + register-hooks-once API → Phase 22.
- Hot-reload + capability/permission model → decided at Phase 22.
- Exposing real entity/world/nav state to Starlark (frozen handles) → Phase 23.
- The opt-in Python runtime (cgo, build tag) → Phase 26.
</deferred>

---

*Phase: 21-starlark-runtime-foundation*
*Context gathered: 2026-06-27 via operator decisions + v4-PLAN.md*
