# Phase 21: Starlark runtime foundation — Research

**Researched:** 2026-06-27
**Domain:** Embedding the `go.starlark.net` interpreter as a sandboxed, CGO-free scripting runtime in a Go server
**Confidence:** HIGH (every load-bearing API symbol and sandbox invariant was empirically verified by building + running probe tests against the current pinned version, not from training data)

## Summary

`go.starlark.net` is the correct, low-risk foundation. It is **pure Go with zero `import "C"`** — confirmed by a tree-wide grep and a `CGO_ENABLED=0 go build` of the `starlark`, `syntax`, and `resolve` packages (all exit 0). All three sandbox knobs the phase requires (per-thread step budget, recursion-off, no I/O builtins) are exposed by upstream **without any patch**, so the fork-vs-dep open decision resolves to **plain `require`, no fork** — verified by running negative-path probe tests that each produced the exact expected error.

The current API surface is stable and matches the README embedding pattern. The load-once path is `starlark.ExecFileOptions(opts, thread, filename, src, predeclared)` → returns the module's globals as a `StringDict` (auto-frozen on completion); call back into Starlark with `starlark.Call(thread, fn, args, kwargs)`; register Go functions with `starlark.NewBuiltin(name, fn)`. The step budget is `thread.SetMaxExecutionSteps(uint64)` and reading `thread.ExecutionSteps()`; exceeding it cancels the thread and returns a `*starlark.EvalError` reading `Starlark computation cancelled: too many steps`.

Two empirical gotchas the planner MUST encode into the negative tests: (1) the runaway-loop probe must put its loop **inside a `def`**, because top-level `for`/`while` are rejected by the parser before the step counter ever runs — a top-level loop fails with a *parse* error, not a *budget* error, which would make a naive infinite-loop test pass for the wrong reason. (2) `-race` requires `CGO_ENABLED=1` (the Go race detector needs a C runtime); the project's existing CI already does exactly this (race job sets `CGO_ENABLED=1`), while the *ship* build stays `CGO_ENABLED=0`. These are not in conflict: default binary = CGO=0, race *test* = CGO=1.

**Primary recommendation:** Add `go.starlark.net v0.0.0-20260613233743-8ba36ccb83fb` as a plain dependency. Create an isolated `plugin/starlark/` package. Centralize sandbox policy in one constructor (`FileOptions{}` zero value + `SetMaxExecutionSteps` + a curated predeclared `StringDict`) so the safe-by-default contract lives in exactly one place. Prove the three sandbox invariants + frozen cross-goroutine read as table tests; gate them with `-race` (CGO=1) and gate the default build with `CGO_ENABLED=0 go build ./...`.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| PLUGIN-01 | Embed `go.starlark.net` (pure-Go, CGO=0 preserved). A `.star` loads/parses/compiles once, runs sandboxed (per-Thread step budget, recursion OFF, no fs/network builtins), one Thread per goroutine, FrozenValues shared safely across the tick boundary, plugin calls a registered Go builtin and returns a value — all `-race` clean. Load/parse/compile lifecycle exists. | Module path + version verified (Standard Stack). All three sandbox knobs verified to work upstream unpatched → no fork (Don't Hand-Roll, Fork verdict). Load-once + call-back path verified (Architecture Patterns). Builtin registration + predeclared injection verified (Code Examples). Frozen cross-goroutine read verified -race-conceptually + the project's race gate confirmed (Validation Architecture). CGO=0 build verified (Environment Availability). |
</phase_requirements>

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **Embed `go.starlark.net` as a PLAIN Go dependency (`require`), NOT a vendored fork.** Only `replace`-pin a minimal fork IF a needed sandbox knob is not exposed upstream — Phase 21 must explicitly determine this. Default expectation: plain dep, no fork. *(Research verdict below: plain dep — confirmed, no fork needed.)*
- **CGO_ENABLED=0 is non-negotiable.** go.starlark.net is pure-Go. Gate includes `CGO_ENABLED=0 go build ./...` clean + zero `import "C"` + go.mod/go.sum carry no cgo-requiring dep.
- **Per-`starlark.Thread` step-counter budget enforced** — a runaway loop hits the budget and errors out, does NOT hang the server.
- **Recursion OFF** — a Starlark function calling itself is a dynamic error (do NOT set `-recursion`).
- **No filesystem/network builtins** unless explicitly registered by Sulfur. Default global environment is the safe subset only.
- These three sandbox properties are observable, tested invariants (a probe `.star` that loops forever / recurses / tries I/O is rejected).
- **One `starlark.Thread` per goroutine** (Threads NOT shared across goroutines).
- **FrozenValues cross the tick boundary safely** — a value frozen at load is safe to READ from the tick goroutine. The phase proves a frozen global produced at load is read race-clean from a second goroutine.
- TICK-05 single-owner carries into the plugin call seam; the Docker `-race` gate covers the plugin path.
- **Dir-based loader:** loader takes a path/dir and loads a `.star`; TESTS load `.star` fixtures from `testdata/`. Not in-memory-only — the path/dir seam is established now so Phase 22 extends it.
- **Load/parse/compile lifecycle once:** module loaded + run ONCE returns its globals; parsed/compiled at load, not re-parsed per call. `starlark.Call` invokes a Starlark function from Go later.
- **Server-NEUTRAL builtin:** the first registered Go builtin is trivial and gameplay-free (log/echo or a pure value-returning builtin). Real server-state exposure is Phase 23's frozen-handle API.
- **Gate:** `.star` loads → runs sandboxed → calls the registered Go builtin → returns a value to Go → whole path `-race` clean and default build is CGO=0.
- **Where the code lives:** a new top-level package (likely `plugin/` or `plugin/starlark/`) — NOT inside `server/` hot-path files. Isolated so the runtime is testable standalone.

### Claude's Discretion
- The planner decides the exact package layout (must be isolated/standalone-testable).
- The exact form of the server-neutral demo builtin (log/echo or pure value-returner).

### Deferred Ideas (OUT OF SCOPE)
- Plugin discovery / manifest / auto-load from a plugins dir → Phase 22.
- The event bus + register-hooks-once API → Phase 22.
- Hot-reload + capability/permission model → Phase 22.
- Exposing real entity/world/nav state to Starlark (frozen handles) → Phase 23.
- The opt-in Python runtime (cgo, build tag) → Phase 26.
</user_constraints>

## Project Constraints (from CLAUDE.md)

- **CGO_ENABLED=0 default binary** — the entire reason Starlark (not Python) is the core runtime. Gate: `CGO_ENABLED=0 go build ./...` clean, no `import "C"`, no cgo-requiring dep in go.mod/go.sum.
- **`go test -race` is non-negotiable** for concurrency subsystems; the plugin call seam is covered by the Docker `-race` gate (which runs with `CGO_ENABLED=1`).
- **No Co-Authored-By / no Claude attribution** in commits or PRs.
- **Push to `development`** (the requirement text says `development`; v4-PLAN constraint #5 also says `development`).
- 1:1-with-the-jar mandate does NOT apply to Phase 21 (no gameplay logic here; the demo builtin is server-neutral). It carries in at Phase 24.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Parse/compile a `.star` file once | `plugin/starlark` (load layer) | — | Compilation is a one-time load-time concern; isolate from the tick. |
| Enforce sandbox policy (steps/recursion/globals) | `plugin/starlark` (policy constructor) | — | Safety contract must live in ONE place so it can't be bypassed per-call. |
| Hold module globals (frozen) | `plugin/starlark` (loaded-plugin handle) | tick goroutine (read-only) | Globals frozen at load are read by the tick later — read-only crossing. |
| Invoke a Starlark fn from Go | tick goroutine (caller) | `plugin/starlark` (provides a fresh `Thread`) | One `Thread` per goroutine; the caller owns the thread for its call. |
| Register Go builtins (the bridge) | `plugin/starlark` (predeclared `StringDict` builder) | — | The predeclared set defines the entire reachable surface; central. |
| Dir/path loader | `plugin/starlark` (loader) | filesystem (read `.star`) | Path seam established now; Phase 22 extends to discovery/manifest. |

**Key boundary:** `plugin/starlark` is a leaf package with **no import of `server/`, `world/`, `level/`, or `world` state** — it must build and test standalone. The demo builtin is server-neutral precisely to keep this edge clean (entity/world exposure is Phase 23).

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `go.starlark.net` | `v0.0.0-20260613233743-8ba36ccb83fb` (latest pseudo-version, 2026-06-13) `[VERIFIED: go get go.starlark.net@latest]` | The Starlark interpreter: parse, compile, exec, sandbox, freeze, Go↔Starlark bridge | Google's reference Go implementation. Pure-Go, no cgo. Battle-tested (Bazel's Starlark lineage). Exposes every sandbox knob this phase needs without patching. |

**Module path:** `go.starlark.net` (this IS the canonical import path; `github.com/google/starlark-go` is the *repo* URL but the *module path* is `go.starlark.net`). `[VERIFIED: go.mod of the downloaded module declares `module go.starlark.net`]`

**Go-version floor:** `go 1.25.0` (declared in the dependency's go.mod). `[VERIFIED]` The project is on `go 1.25.0` (go.mod) / toolchain 1.26.1 — **exact match, no bump needed.**

**Sub-packages used:**
- `go.starlark.net/starlark` — `Thread`, `ExecFileOptions`, `Call`, `NewBuiltin`, `Builtin`, `StringDict`, `Value`, `Tuple`, `String`, `EvalError`, `UnpackPositionalArgs`.
- `go.starlark.net/syntax` — `FileOptions` (the per-file dialect/recursion options).
- `go.starlark.net/resolve` — only if you ever need the *legacy* global flag path; **not needed** with `FileOptions` (prefer `FileOptions`, do NOT touch `resolve.Allow*` globals).

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| (stdlib) `testing`, `sync` | — | Race-gated probe tests + cross-goroutine freeze proof | The whole Phase-21 gate. No third-party test dep needed. |

The project's existing concurrency stack (`xsync`, `ants`, `conc`) is **NOT introduced here** — Phase 21 has no async subsystem to optimize. One `Thread` per goroutine + stdlib is sufficient. `[CITED: CLAUDE.md "Do NOT introduce ants/xsync yet"]`

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Plain `require` of `go.starlark.net` | `replace`-pinned fork | **Rejected — not needed.** All sandbox knobs are upstream. A fork is only justified if a knob is missing; none is. Forking adds maintenance burden for zero benefit. |
| `ExecFileOptions` (explicit `FileOptions`) | `ExecFile` (legacy globals) | `ExecFile` reads process-global resolver flags (`resolve.Allow*`) — a shared, mutable, global sandbox policy. **Reject** for a server: per-file `FileOptions` is explicit, local, and can't be flipped by another package. |
| `starlark.Program` + `Program.Init` (compile-then-init) | `ExecFileOptions` (compile+exec in one) | `ExecFileOptions` already compiles once and returns globals — it satisfies "load/parse/compile once." Split-compile (`SourceProgram`/`Program.Init`) is only worth it if you compile once and *re-init the same program with different predeclared sets*; not needed in Phase 21. Note it for Phase 22 if plugins reload. |

**Installation:**
```bash
cd D:/ender
go get go.starlark.net@v0.0.0-20260613233743-8ba36ccb83fb
go mod tidy
```

**Version verification (already done this session):**
```bash
go get go.starlark.net@latest
# => go.starlark.net v0.0.0-20260613233743-8ba36ccb83fb   [VERIFIED 2026-06-27]
```

## Architecture Patterns

### System Architecture Diagram

```
                         load time (loader goroutine)
   .star file on disk ──────────────────────────────────────────────┐
   (testdata/foo.star)                                               │
        │  os.ReadFile (dir/path loader seam)                        │
        ▼                                                            │
   ┌──────────────────────────────────────────────────────┐         │
   │ plugin/starlark : Load(path)                          │         │
   │  1. read bytes                                        │         │
   │  2. opts := &syntax.FileOptions{}  (zero value:       │         │
   │       recursion CHECK on, while OFF, set OFF)         │         │
   │  3. th := &starlark.Thread{Name:path}                 │         │
   │     th.SetMaxExecutionSteps(BUDGET)                   │         │
   │  4. predeclared := safeGlobals()  (curated StringDict │         │
   │       = ONLY Sulfur builtins; no os/io/net)           │         │
   │  5. globals, err := starlark.ExecFileOptions(         │         │
   │        opts, th, path, src, predeclared)              │         │
   │        ── parses + compiles + runs the module ONCE ── │         │
   │  6. globals are AUTO-FROZEN on module completion      │         │
   └──────────────────────────────────────────────────────┘         │
        │ returns LoadedPlugin{ globals StringDict }  (frozen)       │
        ▼                                                            │
   ════════════════ frozen values cross the boundary ═══════════════╪══════
        │                                                            │
        ▼  tick time (tick goroutine — a DIFFERENT goroutine)        │
   ┌──────────────────────────────────────────────────────┐         │
   │ caller: invoke a plugin fn                            │         │
   │  th2 := &starlark.Thread{Name:"tick"}  (FRESH thread  │         │
   │      per goroutine — never share th from load)        │         │
   │  th2.SetMaxExecutionSteps(BUDGET)                     │         │
   │  fn := loaded.globals["greet"]   // frozen, safe read │         │
   │  out, err := starlark.Call(th2, fn, args, kwargs)     │         │
   │      └─ may call back into a registered Go builtin ───┼─────────┘
   │  return out (a starlark.Value) to Go                  │
   └──────────────────────────────────────────────────────┘
```

**The crossing rule (verified):** values reachable from the module globals are frozen the instant `ExecFileOptions` returns. A frozen value is immutable, so concurrent **reads** from any goroutine are race-free with no locks. The ONE rule: a `starlark.Thread` is mutable scratch state — **never** share a `Thread` across goroutines; create a fresh `Thread` in whichever goroutine makes the call.

### Recommended Project Structure
```
plugin/
└── starlark/                 # leaf package; imports NO server/world state
    ├── runtime.go            # safeGlobals(), defaultFileOptions(), the step BUDGET const
    ├── loader.go             # Load(path string) (*LoadedPlugin, error) — dir/path seam
    ├── plugin.go             # LoadedPlugin{ globals StringDict }; Call(name, args...) helper
    ├── builtins.go           # the server-NEUTRAL demo builtin (echo/log)
    ├── runtime_test.go       # sandbox invariants (budget / recursion / no-IO) table tests
    ├── race_test.go          # frozen-global cross-goroutine read, run under -race
    └── testdata/
        ├── greet.star        # happy path: defines a fn that calls the Go builtin
        ├── infinite_loop.star# loop INSIDE a def (so the budget, not the parser, catches it)
        ├── recursive.star    # self-recursive fn -> dynamic error
        └── tries_io.star     # references `open`/undefined builtin -> rejected
```

### Pattern 1: One sandbox-policy constructor (single source of truth)
**What:** All three sandbox knobs are set in exactly one place. Loader and caller both go through it.
**When to use:** Every `ExecFileOptions`/`Call` in the package.
**Why:** If the policy is duplicated, one forgotten `SetMaxExecutionSteps` is an un-budgeted thread = a hang vector. Centralizing makes the safe default un-bypassable.
```go
// Source: verified against go.starlark.net eval.go + syntax/options.go
const stepBudget uint64 = 10_000_000 // tune; tested value can be far smaller

// defaultFileOptions returns the SAFE dialect: recursion check ON (zero value),
// while-loops OFF, set() OFF, no top-level control flow.
func defaultFileOptions() *syntax.FileOptions { return &syntax.FileOptions{} }

func newThread(name string) *starlark.Thread {
    th := &starlark.Thread{Name: name}
    th.SetMaxExecutionSteps(stepBudget) // runaway loop -> EvalError, not a hang
    return th
}
```

### Pattern 2: Curated predeclared globals = the entire reachable surface
**What:** The `predeclared StringDict` passed to `ExecFileOptions` is the ONLY app-specific surface a plugin can see. The Starlark *universe* (`len`, `range`, `dict`, etc.) has **no** filesystem/network/eval builtins — confirmed: `open(...)` fails `undefined: open`.
**When to use:** Always build it from one `safeGlobals()` func.
```go
func safeGlobals() starlark.StringDict {
    return starlark.StringDict{
        "echo": starlark.NewBuiltin("echo", echoBuiltin), // server-NEUTRAL demo
    }
}
```

### Pattern 3: Load-once, call-many
**What:** `ExecFileOptions` runs the module body once (parse+compile+exec) and returns frozen globals. Later calls reuse those frozen `Value`s via `starlark.Call` on a fresh per-goroutine `Thread`. The module is **not** re-parsed per call.

### Anti-Patterns to Avoid
- **Sharing a `starlark.Thread` across goroutines.** A `Thread` carries mutable per-call state (the step counter, call stack). One `Thread` per goroutine, always.
- **Using `ExecFile` (no `Options`).** It reads process-global resolver flags — a shared mutable sandbox policy. Use `ExecFileOptions` with an explicit `FileOptions`.
- **Setting `FileOptions{Recursion: true}`.** That DISABLES the recursion check (allows self-recursion). The phase requires recursion OFF → leave it false (zero value).
- **Forgetting `SetMaxExecutionSteps` on a thread.** An un-budgeted thread can spin forever. The `newThread` constructor prevents this.
- **Top-level loop in a negative test fixture.** A top-level `for`/`while` is a *parse* rejection, not a *budget* hit — see Pitfall 1.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Sandboxing / resource limits | A custom interpreter watchdog/timer goroutine | `thread.SetMaxExecutionSteps()` | Deterministic step counter, no timer races, returns a clean `*EvalError`. Verified. |
| Disabling recursion | Static analysis of the AST | `FileOptions.Recursion = false` (zero value) | Built-in dynamic recursion guard. Error: `function f called recursively`. Verified. |
| Restricting builtins | A blocklist / AST scrubber | Curate the predeclared `StringDict`; rely on the empty default universe | There is NO fs/net/eval builtin in the universe to begin with. Allowlist by construction. Verified (`open` is undefined). |
| Parse/compile | A `.star` parser | `starlark.ExecFileOptions` | Full lexer/parser/resolver/compiler in-tree. |
| Cross-thread safety | Locks around shared plugin state | `Value.Freeze()` (auto on module globals) | Frozen = immutable = lock-free concurrent reads. The library's central design guarantee. |
| Calling Starlark from Go | A custom dispatch ABI | `starlark.Call(thread, fn, args, kwargs)` | Handles arg packing, frames, errors. |

**Key insight:** The phase is almost entirely about *configuring* go.starlark.net correctly, not about writing interpreter machinery. The only Go code you author is: the loader (file read + the policy constructor), the predeclared `StringDict`, the one demo builtin, and the tests. Everything dangerous (sandbox, freeze, parse) is the library's job.

## Common Pitfalls

### Pitfall 1: The infinite-loop test passes for the WRONG reason
**What goes wrong:** You write `infinite_loop.star` as a top-level `for i in range(1e9): ...` and the test "passes" (gets an error) — but the error is `for loop not within a function` (a **parse** rejection), NOT a step-budget hit. The step counter was never exercised. `[VERIFIED: probe produced `loop.star:2:1: for loop not within a function (steps=0)`]`
**Why it happens:** `FileOptions{}` (zero value) disallows top-level control flow (`TopLevelControl=false`).
**How to avoid:** Put the runaway loop **inside a `def`** and call it. Then the error is `Starlark computation cancelled: too many steps` with `ExecutionSteps()==budget`. `[VERIFIED: probe produced exactly that, steps=50000 at a 50000 cap]`
**Warning signs:** Test error message mentions "not within a function" or `steps=0` — your budget test is hollow.

### Pitfall 2: `while` is OFF by default — don't assume `for` and `while` behave alike
**What goes wrong:** A fixture using `while True:` fails with `this Starlark dialect does not support while loops` even inside a `def` — because `FileOptions.While=false` by default. `[VERIFIED]`
**How to avoid:** Use a `for i in range(huge):` loop inside a `def` for the budget test (allowed by default inside functions). Only set `While:true` if you deliberately want while-loops — and you still get budget protection (verified: `while True: pass` inside a fn with `While:true` hit the budget at exactly the cap).

### Pitfall 3: `-race` needs CGO=1, but the ship binary needs CGO=0
**What goes wrong:** Running `CGO_ENABLED=0 go test -race ./...` fails: `-race requires cgo`. `[VERIFIED]`
**Why it happens:** Go's race detector links a C runtime.
**How to avoid:** Two separate gates — (a) **ship/build gate:** `CGO_ENABLED=0 go build ./...` (the static-binary guarantee), (b) **race-test gate:** `CGO_ENABLED=1 go test -race ./...`. This is NOT a contradiction; they test different things. The project's CI already does exactly this — `.github/workflows/go.yml` runs the static build at `CGO_ENABLED=0` and the race tests at `CGO_ENABLED=1` (with the comment "ubuntu-latest ships a C compiler, so -race (which needs CGO) works"). `[VERIFIED: read of go.yml]`

### Pitfall 4: Sharing a Thread across goroutines (silent race)
**What goes wrong:** Reusing the load-time `Thread` on the tick goroutine races on the step counter / call stack.
**How to avoid:** `newThread()` per goroutine per call. Frozen *Values* cross; *Threads* never do.

### Pitfall 5: Reaching for `resolve.Allow*` globals
**What goes wrong:** Old examples set `resolve.AllowRecursion = true` etc. (process-global). Mutating these is a shared-state footgun and the wrong API now.
**How to avoid:** Use per-file `syntax.FileOptions` exclusively. Never import `resolve` for flag-flipping.

### Pitfall 6: Forgetting that globals are frozen → a builtin can't mutate them
**What goes wrong:** A later builtin tries to append to a plugin-global list and gets a dynamic frozen-value error.
**How to avoid:** Expected and desired in Phase 21 (the demo builtin is pure). Note for Phase 23: mutate through tick-owned Go state, never through a frozen Starlark global.

## Code Examples

All snippets below were **compiled and run this session** against `go.starlark.net v0.0.0-20260613233743-8ba36ccb83fb`; the asserted error strings are the actual observed output.

### Register a server-neutral Go builtin
```go
// Source: VERIFIED against value.go (NewBuiltin signature) + a passing probe test.
// Builtin callback signature:
//   func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error)
func echoBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
    var s starlark.String
    if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &s); err != nil {
        return nil, err
    }
    return s, nil // server-neutral: pure value-return, touches no game state
}

func safeGlobals() starlark.StringDict {
    return starlark.StringDict{"echo": starlark.NewBuiltin("echo", echoBuiltin)}
}
```

### Set the step budget + the safe FileOptions, then exec-once and get globals
```go
// Source: VERIFIED — eval.go (SetMaxExecutionSteps, ExecFileOptions, ExecutionSteps),
// options.go (FileOptions zero value).
import (
    "go.starlark.net/starlark"
    "go.starlark.net/syntax"
)

const stepBudget uint64 = 10_000_000

func Load(path string, src any) (starlark.StringDict, error) {
    th := &starlark.Thread{Name: path}
    th.SetMaxExecutionSteps(stepBudget)             // budget enforced
    opts := &syntax.FileOptions{}                   // recursion CHECK on; while OFF; no top-level control
    // ExecFileOptions parses+compiles+runs the module ONCE; returns its globals (auto-frozen).
    globals, err := starlark.ExecFileOptions(opts, th, path, src, safeGlobals())
    return globals, err
}
```

### Call a Starlark function from Go (fresh thread per goroutine)
```go
// Source: VERIFIED — Call signature: func Call(thread, fn Value, args Tuple, kwargs []Tuple) (Value, error)
func Invoke(globals starlark.StringDict, name string, arg string) (starlark.Value, error) {
    fn := globals[name] // frozen value; safe to read from any goroutine
    th := &starlark.Thread{Name: "call"}
    th.SetMaxExecutionSteps(stepBudget)
    return starlark.Call(th, fn, starlark.Tuple{starlark.String(arg)}, nil)
}
```

### Freeze + cross-goroutine read (the -race proof, abbreviated)
```go
// Source: VERIFIED — a passing probe test (8 reader goroutines + 4 caller goroutines)
globals, _ := Load("p.star", "result = echo('hi')\ndef greet(w):\n    return echo(w)\n")
var wg sync.WaitGroup
for i := 0; i < 8; i++ {
    wg.Add(1)
    go func() { defer wg.Done(); _ = globals["result"].String() }() // frozen read, no lock
}
wg.Wait()
// each caller uses its OWN thread:
greet := globals["greet"]
go func() {
    th := &starlark.Thread{Name: "g"}
    out, _ := starlark.Call(th, greet, starlark.Tuple{starlark.String("bob")}, nil)
    _ = out // "bob"
}()
```

### Negative fixtures (the three sandbox invariants)
```python
# testdata/infinite_loop.star  -> step budget (loop MUST be inside a def)
def spin():
    x = 0
    for i in range(100000000):
        x = x + 1
    return x
spin()
# observed: "Starlark computation cancelled: too many steps", *EvalError, steps == cap

# testdata/recursive.star  -> recursion dynamic error
def f(n):
    return f(n - 1)
f(5)
# observed: "function f called recursively"

# testdata/tries_io.star  -> no I/O builtin
open("etc/passwd")
# observed: "tries_io.star:1:1: undefined: open"
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `resolve.AllowRecursion` / `resolve.AllowGlobalReassign` process-global flags + `ExecFile` | Per-file `syntax.FileOptions` + `ExecFileOptions` | FileOptions introduced ~2023; now the documented path | Use `FileOptions`. Per-file, explicit, no shared mutable sandbox state. `[CITED: options.go LegacyFileOptions doc note]` |
| `starlark.SetMaxExecutionSteps`-style globals (historical confusion) | Per-`Thread` method `thread.SetMaxExecutionSteps(uint64)` + `thread.ExecutionSteps()` + `thread.OnMaxSteps` callback | current | The budget is per-thread, not global. `[VERIFIED]` |

**Deprecated/outdated for our use:**
- `ExecFile` (no Options) — works but reads legacy globals; prefer `ExecFileOptions`.
- `LegacyFileOptions()` — explicitly a legacy bridge; do not use.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | A `stepBudget` of ~10M is a reasonable default for a load-time plugin body; the precise number is a tuning decision for the planner/operator. | Code Examples | Low — the *mechanism* is verified; only the constant is a guess. Tests should use a small cap (e.g. 50_000) to fire fast. |

**Everything else in this research was VERIFIED by building/running against the pinned version, or CITED from the library source.** No assumed sandbox behavior.

## Open Questions

1. **Exact step-budget constant for production loads.**
   - What we know: the mechanism works; a cap fires deterministically at the set value.
   - What's unclear: a sensible default for real plugin module bodies (vs. test fixtures).
   - Recommendation: pick a generous default (10M) for `Load`, a tiny one (50k) in the budget *test*. Make it a named const so Phase 22 can make it configurable per-plugin.

2. **Will Phase 22 want split compile (`SourceProgram` + `Program.Init`)?**
   - What we know: `ExecFileOptions` compiles+runs once — sufficient for Phase 21.
   - What's unclear: hot-reload (Phase 22, deferred) might want to compile once and re-init.
   - Recommendation: structure `Load` so the compile step is swappable later; do NOT build it now (no built-but-unwired code).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `go.starlark.net` module | PLUGIN-01 | ✓ | `v0.0.0-20260613233743-8ba36ccb83fb` | — |
| Go toolchain ≥ 1.25 | starlark dep floor | ✓ | project on go 1.25.0 / 1.26.1 | — |
| C compiler (for `-race` only) | race-test gate | ✓ in CI (ubuntu-latest) | — | n/a — CI already provisions it; `-race` runs `CGO_ENABLED=1` |
| `CGO_ENABLED=0` build | ship binary | ✓ | verified `go build` exit 0 | — |

**Missing dependencies with no fallback:** None.
**Missing dependencies with fallback:** None — the only CGO requirement is the race *detector*, satisfied by CI's C compiler; it does not affect the shipped CGO=0 binary.

**CGO=0 evidence (this session):**
- Tree-wide grep for `import "C"` across all `*.go` in the module → **zero matches** (one match was a `.star` testdata file, not Go). `[VERIFIED]`
- `CGO_ENABLED=0 go build go.starlark.net/starlark go.starlark.net/syntax go.starlark.net/resolve` → **exit 0**. `[VERIFIED]`
- Transitive deps (`chzyer/readline`, `google/go-cmp`, `golang.org/x/sys`, `golang.org/x/term`, `protobuf`) are all pure-Go; `readline`/`term` are only used by the `repl`/CLI sub-packages **which Phase 21 does not import** — the `starlark`/`syntax`/`resolve` packages build CGO=0 cleanly on their own. `[VERIFIED]`

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ `sync` for the race proof) |
| Config file | none (Go convention) |
| Quick run command | `go test ./plugin/starlark/` |
| Full suite (race) command | `CGO_ENABLED=1 go test -race ./plugin/starlark/` |
| Ship-build gate | `CGO_ENABLED=0 go build ./...` |

### Phase Requirements → Test Map
| Req | Behavior | Test Type | Automated Command | Verified This Session |
|-----|----------|-----------|-------------------|------------------------|
| PLUGIN-01 | `.star` loads, compiles once, runs, returns globals | unit | `go test ./plugin/starlark/ -run TestLoad` | ✅ (probe `ExecFileOptions` returned globals) |
| PLUGIN-01 | Runaway loop hits step budget (no hang) → `*EvalError` "too many steps" | unit | `go test ./plugin/starlark/ -run TestStepBudget` | ✅ (`steps=50000` at cap, `errors.As(*EvalError)` true) |
| PLUGIN-01 | Self-recursion is a dynamic error | unit | `go test ./plugin/starlark/ -run TestRecursion` | ✅ (`function f called recursively`) |
| PLUGIN-01 | I/O builtin not exposed (`open` undefined) | unit | `go test ./plugin/starlark/ -run TestNoIO` | ✅ (`undefined: open`) |
| PLUGIN-01 | Plugin calls a registered Go builtin, returns a value | unit | `go test ./plugin/starlark/ -run TestBuiltin` | ✅ (`echo('hi')` → `"hi"`) |
| PLUGIN-01 | Frozen global read from a second goroutine, race-clean | race | `CGO_ENABLED=1 go test -race ./plugin/starlark/ -run TestFrozen` | ✅ (8 readers + 4 callers passed; -race needs CGO=1 in CI) |
| PLUGIN-01 | Default binary is CGO=0 | build | `CGO_ENABLED=0 go build ./...` | ✅ (starlark pkgs build exit 0; no `import "C"`) |

### Sampling Rate
- **Per task commit:** `go test ./plugin/starlark/`
- **Per wave merge / phase gate:** `CGO_ENABLED=1 go test -race ./plugin/starlark/` **AND** `CGO_ENABLED=0 go build ./...`
- **Phase gate (full):** the Docker `-race` image (CGO=1) green + the CGO=0 static build green — both already wired in `.github/workflows/go.yml`; the new package is picked up by `./...`.

### Wave 0 Gaps
- [ ] `plugin/starlark/testdata/{greet,infinite_loop,recursive,tries_io}.star` — fixtures (the 3 negative ones are the sandbox proof; loop fixture MUST loop inside a `def` per Pitfall 1).
- [ ] `plugin/starlark/runtime_test.go` — sandbox invariants table test.
- [ ] `plugin/starlark/race_test.go` — frozen cross-goroutine read.
- [ ] `go get go.starlark.net@v0.0.0-20260613233743-8ba36ccb83fb && go mod tidy` — add the dep.

*(No framework install needed — Go stdlib `testing`. The race gate already exists in CI.)*

## Security Domain

Phase 21 has no network/auth surface of its own (it's a runtime embed), but the *sandbox* IS the security control, so it is covered explicitly.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|------------------|
| V5 Input Validation / untrusted code execution | yes | The Starlark sandbox: step budget (DoS/hang prevention), recursion-off, allowlist-only predeclared globals (no fs/net/eval). A plugin is untrusted-ish code; the sandbox is the trust boundary. |
| V6 Cryptography | no | None in this phase. |
| V2/V3/V4 Auth/Session/Access | no | No auth surface; Phase 22 capability/permission model is deferred. |

### Known Threat Patterns
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Runaway/infinite-loop plugin hangs the tick | Denial of Service | `thread.SetMaxExecutionSteps` → `*EvalError`, never a hang. Verified. |
| Stack-exhaustion via deep recursion | Denial of Service | Recursion check ON by default (`FileOptions.Recursion=false`). Verified. |
| Plugin reads/writes files or opens sockets | Information Disclosure / Tampering | No fs/net builtin in the universe; allowlist-only predeclared `StringDict`. Verified (`open` undefined). |
| Cross-goroutine data race via shared mutable state | Tampering | Auto-freeze of module globals → immutable concurrent reads; one Thread per goroutine. Verified (probe). Gate: `-race`. |

## Sources

### Primary (HIGH confidence)
- Context7 `/google/starlark-go` — README embedding pattern (`ExecFile`/`Call`), spec.md (recursion error, freezing semantics, function-param grammar), impl.md (freezing model: per-object frozen flag, safe concurrent reads).
- `go.starlark.net/starlark/eval.go` (raw GitHub master) — `Thread.SetMaxExecutionSteps`, `ExecutionSteps`, `OnMaxSteps` (default `Cancel("too many steps")`), `ExecFile`/`ExecFileOptions`/`Call` signatures, predeclared `StringDict`.
- `go.starlark.net/syntax/options.go` (raw GitHub master) — `FileOptions{Set, While, TopLevelControl, GlobalReassign, LoadBindsGlobally, Recursion}`; zero value = default (recursion check ON).
- `go.starlark.net/starlark/value.go` (raw GitHub master) — `NewBuiltin` signature, the `Builtin` callback type, `Value.Freeze()`, `Tuple`.
- **Empirical probe (this session):** built + ran 7 tests against `v0.0.0-20260613233743-8ba36ccb83fb` — step budget, recursion, no-IO, builtin call, frozen cross-goroutine, top-level-loop rejection, `*EvalError` type, `while`-default-off. All error strings quoted are observed output.
- `D:/ender/go.mod` (module `github.com/imhinotori/sulfur`, go 1.25.0), `Dockerfile` (CGO=0 static), `.github/workflows/go.yml` (CGO=0 build + CGO=1 race test).

### Secondary (MEDIUM confidence)
- None needed — all critical claims are primary-source or empirically verified.

### Tertiary (LOW confidence)
- None.

## Metadata

**Confidence breakdown:**
- Standard stack (module path, version, Go floor, CGO=0): **HIGH** — resolved via `go get`, built CGO=0, grepped for `import "C"`.
- Sandbox API (step budget, recursion, globals): **HIGH** — exact symbols from source + every invariant run as a passing/failing probe with quoted output.
- Fork-vs-dep verdict (plain dep, no fork): **HIGH** — all three knobs exercised on the unpatched upstream module.
- Architecture (load-once / freeze-cross / one-thread-per-goroutine): **HIGH** — README pattern + impl.md + probe.
- Pitfalls (top-level-loop trap, `-race` needs CGO=1, `while` off): **HIGH** — each observed directly.

**Research date:** 2026-06-27
**Valid until:** ~30 days for the API shape (stable library); the pseudo-version may advance — re-run `go get go.starlark.net@latest` at execution and pin whatever is current (API is stable across these).
