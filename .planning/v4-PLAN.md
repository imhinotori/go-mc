# v4 — Plugin / Scripting System (PLANNED, not started)

> **Status:** PLAN ONLY. Execution waits for v3 (Phases 17–20) to close. STATE.md stays on v3;
> this doc is the adoptable plan for `/gsd-new-milestone v4` once v3 ships. Phases 21–27.
>
> **This inverts CLAUDE.md's "Server core only — no plugin/extension API" scope decision.** That
> reversal is intentional and user-directed for v4. When v4 starts, update CLAUDE.md's Scope line.

## The pitch

A **dual-runtime extension API** that makes Sulfur's gameplay scriptable without giving up the
two things that define the project: the **pure-Go static binary** (CGO_ENABLED=0) and the
**1:1-with-the-jar gameplay mandate**. The validation/dogfood target is to rewrite the vanilla
mobs + entity logic AS PLUGINS that remain a literal 1:1 port of the 26.2 jar — proving the API
is powerful enough for real vanilla AI — while the same API also enables fully-custom mobs.

## Runtimes (user-decided)

### Core: Starlark — `go.starlark.net` (`/google/starlark-go`)
- **Pure Go, no cgo** → CGO_ENABLED=0 static binary preserved. This is the non-negotiable reason
  Starlark is the CORE runtime, not Python.
- **Deterministic + sandboxed natively:** `starlark.Thread` carries a step budget (resource limit);
  recursion is OFF by default (a function calling itself is a dynamic error unless `-recursion` is
  set — we keep it off); no filesystem/network builtins unless we expose them. Perfect for running
  inside the tick loop without a rogue plugin hanging the server.
- **Concurrency model (verified, Context7 `/google/starlark-go`):** one `starlark.Thread` per
  goroutine (Threads are NOT shared); values are **frozen** when they cross into another thread, so
  a frozen handle is safe to read from the tick goroutine. `starlark.Call(thread, fn, args, kwargs)`
  invokes a Starlark function from Go; `starlark.ExecFile` loads + runs a module once and returns its
  globals. Python-like syntax (`def`, kwargs, `*args`, keyword-only params) — familiar to authors.
- **Caveat:** NOT Python. No arbitrary imports, no third-party libs, no dynamic eval. That is a
  feature here (sandbox + determinism), and the reason the heavy/dynamic workloads get the opt-in
  Python runtime below.

### Opt-in: Python — `qur/gopy` @ branch `python3.14` (`gopython.xyz/py/v3`)
- **Idiomatic CPython bindings via cgo + libpython** (`import "C"` / `Python.h`). Real Python —
  libs, dynamic, the whole ecosystem. The `python3.14` branch exists (confirmed via
  `git ls-remote https://github.com/qur/gopy`; branches go python2.6 → 3.14).
- **Requires cgo → behind a `python` build tag.** The DEFAULT build stays pure-Go static (CGO=0);
  only an operator who opts into Python pays the cgo + libpython dependency. The runtime-server "no
  JVM, no native deps" value prop is preserved for the default binary.
- **Off-tick ONLY.** cgo call overhead + the GIL + non-determinism keep Python OFF the per-tick hot
  path. It is for HEAVY plugins (data processing, external integrations, ML, web) that run on their
  own goroutines/pools and rejoin via the async seam (the existing `asyncIn2`/ants pattern), never
  inside `tickOnce`.
- **Same event/registration API as Starlark** so a plugin author picks the runtime per workload, not
  per API.

## Architecture (user-decided)

**Plugins DECLARE behavior loaded once; Go executes the hot path.** A plugin registers its hooks at
load time (parse/compile once). The per-tick hot path — physics, pathfinding, collision, the tick
loop itself — stays Go-native and calls into the plugin only at **declared seams** (event hooks,
behavior decisions), NOT by running a script per-entity-per-tick. This is what keeps "ultra-efficient"
true: 200 mobs × 20 TPS does NOT mean 4000 interpreter calls/sec — the interpreter runs at load +
on events + on cached decisions.

**But a plugin CAN fully override a mob.** When an author wants total control, the behavior API lets a
plugin replace a mob's whole decision logic (still through the declared-seam model, just owning all the
seams). The Go hot path still runs the mechanical work (move the AABB, sweep collision, step the
nav) — the plugin owns WHAT to do, Go owns HOW to execute it efficiently.

**1:1 carries into the plugin layer.** The vanilla-mob plugins (Phase 24) stay a literal jar port.
Re-expressing a mob's AI in Starlark is still "method-for-method copy of the 26.2 jar, verified against
bytecode" — the runtime changed, the mandate did not. Custom (non-vanilla) plugins are free of the
mandate by definition.

**Race-cleanliness (TICK-05) carries in too.** The plugin call seam runs on the tick goroutine over
tick-owned state; the Go→Starlark bridge exposes entity/world/nav as FROZEN, tick-owned-safe handles.
The Docker -race gate covers the plugin path exactly as it covers the async subsystems today.

## Phases (21–27)

| Phase | Req | What | Gate |
|-------|-----|------|------|
| 21 | PLUGIN-01 | **Starlark runtime foundation** — embed go.starlark.net (CGO=0 preserved); per-goroutine Thread, sandbox (step budget, no-recursion, no I/O builtins), FrozenValue sharing across the tick boundary, plugin load/parse/compile lifecycle, a pinned fork if needed. | A `.star` loads, runs sandboxed, calls a Go builtin, returns a value, -race clean. |
| 22 | PLUGIN-02 | **Plugin host + event bus** — plugin manager (discover/load/unload from a plugins dir), manifest, typed event system (tick, join/leave, break/place, spawn/death, damage), the register-hooks-once API, the Go→plugin dispatch seam kept off the per-entity hot path. | A plugin subscribes to events and its hook fires on the real tick, event-driven not per-tick-scan. |
| 23 | PLUGIN-03 | **Entity/mob behavior API** — declarative mob-behavior interface (declare attributes/goals/AI once; Go runs the hot path calling hooks) + the FULL-OVERRIDE path; the Go-side bridge exposing entity/world/nav to Starlark as frozen tick-safe handles. | A trivial custom mob declared in Starlark spawns, ticks, and moves via the Go nav, -race clean. |
| 24 | PLUGIN-04 | **Vanilla mobs AS plugins (1:1 dogfood)** — rewrite the existing Go mob/entity logic as Starlark plugins that stay a literal 1:1 jar port. Validates the API expresses real vanilla AI. | The plugin-driven vanilla mob is behavior-identical to the Go-native path it replaces; jar-verified; existing mob-AI tests green. |
| 25 | PLUGIN-05 | **Opt-in Python runtime** — qur/gopy @ python3.14 behind a `python` build tag (default binary stays CGO=0 static); a bridge for HEAVY off-tick plugins only (never the per-tick path); same event/registration API as Starlark. | A Python plugin runs off-tick, rejoins via the async seam; the default (no-tag) build is still pure-Go static. |
| 26 | REGION-01 | **Folia regionization** (folded from the v3 deferral) — independent-region tick threads so the world ticks in parallel regions; the plugin seam + entity API are region-aware (a hook runs on its region's thread). | The world ticks in parallel regions, -race clean, plugin hooks run on the correct region thread. |
| 27 | PLUGIN-06 | **Plugin system visual + perf gate** (autonomous:false) — real-client confirm: a custom mob plugin works, vanilla-mobs-as-plugins is behavior-identical, events fire, and the plugin layer adds no measurable per-tick cost vs Go-native (Folia regions scale). | Human-verified on a real 26.2 client + a perf benchmark. Closes v4. |

## Build order rationale
- 21 → 22: a runtime with no host is useless; a host with no runtime has nothing to load. Runtime first
  (the sandbox + CGO=0 proof is the riskiest single thing), then the host/event layer on top.
- 23 needs 22's event bus + registration API to hang the behavior hooks on.
- 24 is the dogfood — it can only run once 23's behavior API exists; it's also the real test that the
  API is "ultra-powerful" (if vanilla AI doesn't fit, the API is wrong, caught here before Python).
- 25 (Python) deliberately AFTER the Starlark path is proven end-to-end: Python is the riskier runtime
  (cgo, GIL, build tag), and it reuses the SAME event/registration API, so it must land after that API
  is validated by 22–24.
- 26 (Folia) after the single-thread plugin path works, because regionization changes WHICH thread a
  hook runs on — you regionize a working seam, you don't design the seam around regions first.
- 27 gates the whole thing on a real client + perf.

## Open decisions to resolve at v4 kickoff (NOT now)
- **Starlark fork-or-vendor:** go.starlark.net is stable upstream; likely a plain dep, not a fork
  (unlike go-mc). Confirm at Phase 21 — do we need any sandbox patch upstream doesn't expose?
- **Plugin distribution format:** single `.star` file vs a plugin dir with a manifest + assets. Lean
  manifest-dir (Phase 22) for versioning + the Python/Starlark runtime selector.
- **The frozen-handle API surface:** exactly which entity/world/nav operations a plugin may call, and
  which are read-only vs mutating-through-a-tick-owned-seam. The hardest design question; Phase 23.
- **Hot-reload:** unload/reload a plugin without a server restart? Nice-to-have; decide scope at 22.
- **Capability/permission model:** does a plugin declare what it can touch (entities, world, network)?
  Security-relevant if plugins are third-party. Decide at 22.

## Constraints that carry from the project (unchanged)
1. **CGO_ENABLED=0 default binary** — Starlark core respects it; Python is build-tag-gated.
2. **1:1 mandate** carries into vanilla-mob plugins (Phase 24) — jar-verified, no paraphrase.
3. **TICK-05 single-owner / -race clean** — the plugin call seam is tick-owned; Docker -race gate covers it.
4. **No Co-Authored-By / no Claude attribution** in commits.
5. **Push target `development`.**
