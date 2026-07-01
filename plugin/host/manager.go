// Package host is the plugin HOST + typed event bus. It discovers/loads/unloads
// TOML-manifest plugins from a plugins/ dir, owns the map[EventType][]Hook bus,
// injects the register builtin that captures hooks ONCE at load, and dispatches
// discrete gameplay events to those hooks via Emit.
//
// Boundary: host imports plugin/starlark + stdlib + the manifest/watcher deps
// ONLY — NEVER server/world/level. Event payloads are PLAIN FROZEN SCALARS
// (ints/strings/float), not live entity/world handles (those are Phase 23). The
// server depends on host (one direction) and calls Emit at the discrete seams.
//
// Concurrency: the hooks map is WRITTEN only at load (LoadDir, single-threaded,
// before the tick loop owns the Manager) and READ during Emit (on the tick
// goroutine, single owner — TICK-05). No locks; lock-free by construction.
package host

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	starlarkpkg "github.com/imhinotori/sulfur/plugin/starlark"
	"go.starlark.net/starlark"
)

// Hook is one captured subscriber: a frozen Starlark callable + the owning
// plugin name (for unload + error attribution).
type Hook struct {
	fn     starlark.Callable
	plugin string
}

// loadedPlugin wraps a plugin's manifest + its Phase-21 LoadedPlugin handle.
// For a runtime="python" plugin loaded WITH the tag, `python` holds the loaded
// CPython handle (a plain interface — no cgo) the Wave-2 off-tick lane dispatches
// to; it is nil for starlark plugins (and on the default build).
type loadedPlugin struct {
	manifest Manifest
	loaded   *starlarkpkg.LoadedPlugin
	python   PythonPlugin
}

// Manager is the single owner of the loaded plugins + the typed event bus.
type Manager struct {
	plugins []*loadedPlugin
	hooks   map[EventType][]Hook

	// The value-returning RECIPE seam (Phase 25 — extends Emit's void dispatch
	// to query-resolution). recipeMatcher/recipeRemaining are the plugin
	// callables captured ONCE at load via set_recipe_matcher/set_recipe_remaining;
	// recipeTable is the Go-parsed recipe table the server injects (SetRecipeTable)
	// and the recipes() builtin hands back to the plugin (Starlark has no
	// json/file builtin). All three follow the SAME lock-free discipline as
	// hooks: WRITTEN only at load (the builtins capture on the load goroutine;
	// SetRecipeTable runs before LoadDir), READ on the tick (Match/Remaining).
	recipeMatcher   starlark.Callable
	recipeRemaining starlark.Callable
	recipeTable     starlark.Value
	recipeOwner     string // the plugin that registered the matcher (for Unload)

	// pythonRuntime is the OPT-IN CPython lane, registered at boot from a
	// //go:build python adapter via SetPythonRuntime (runtime.go). nil on the
	// default (no-tag) build → runtime="python" plugins are skipped gracefully.
	// The host holds it BY INTERFACE so plugin/host never imports gopy (cgo-free).
	pythonRuntime PythonRuntime

	// pythonDispatch is the OFF-TICK submit seam (Wave 2). When Emit fires a
	// discrete event, it hands each loaded python plugin + the event's plain
	// scalar args to this callback, which submits the hook to the off-tick ants
	// pool (server/async_python_python.go) — Emit NEVER calls a python hook inline
	// (PLUGIN-06 off-tick rule). It is registered by the server via
	// SetPythonDispatch; nil on a server with no python lane (Emit just skips the
	// python plugins). Plain Go signature (no cgo, no *py.Object) so plugin/host
	// stays cgo-free — the gopy marshalling happens inside the PythonPlugin.CallHook
	// the callback ultimately invokes.
	pythonDispatch func(pp PythonPlugin, event string, args []any)

	// chatSink is the OPTIONAL output sink the SERVER installs at boot via SetChatSink.
	// The chat(msg) builtin (Plan 28-02) calls it so a plugin hook's reaction lands on
	// the player-facing wire as a ClientboundSystemChat. plugin/host has NO server import
	// (the one-direction layering), so the sink is a plain `func(string)` the server wires
	// to TickLoop.broadcastSystemChat. When nil (no server, or no sink installed — e.g. a
	// unit test or a server with the chat lane off), chat() FALLS BACK to log() so the
	// reaction is still recorded and the host stays self-contained (T-28-08: the sandbox
	// surface widens by exactly one server-controlled text-output seam, no world/entity
	// handle). WRITTEN once at boot before the tick owns the Manager, READ on the tick
	// goroutine inside Emit (the sink fires from a hook → broadcastSystemChat's tick-owned
	// fan is safe — TICK-05); same lock-free discipline as recipeMatcher/hooks.
	chatSink func(string)

	// pythonBridgeFactory is the WORLD-BRIDGE factory (Plan 26-03), registered by
	// the server via SetPythonBridgeFactory. When LoadDir loads a runtime="python"
	// plugin, it calls this with the plugin's name + manifest capabilities to build
	// a per-plugin WorldBridge stamped with that plugin's capSet, then installs it
	// via pp.SetWorldBridge. The server's factory parses the capabilities (the SAME
	// parseCapabilities the Phase-23 Starlark handles use) and returns a bridge whose
	// set_block/spawn/log/block_at route through the async rejoin lane to the owner.
	// Plain-Go signature (no cgo) so plugin/host stays cgo-free; nil on a server with
	// no world lane → SetWorldBridge gets nil and the builtins no-op. An unknown
	// capability returns an error, which aborts the load (load-loudly discipline).
	pythonBridgeFactory func(plugin string, capabilities []string) (WorldBridge, error)
}

// SetChatSink installs the OPTIONAL output sink the chat(msg) builtin calls (Plan 28-02).
// The server wires it to a closure over TickLoop.broadcastSystemChat so a plugin hook's
// chat() reaction fans to every player as a ClientboundSystemChat — the observable-event
// seam the gate bot decodes (checklist item #4). Called ONCE at boot before the tick loop
// owns the Manager (like SetPythonDispatch). On a server/test with no sink installed,
// chat() falls back to log() (the host stays self-contained).
func (m *Manager) SetChatSink(fn func(string)) { m.chatSink = fn }

// SetPythonBridgeFactory registers the world-bridge factory (Plan 26-03). The server
// wires it to a constructor that parses the plugin's manifest capabilities into a
// capSet and returns a per-plugin WorldBridge. Called ONCE before the tick loop owns
// the Manager (like SetPythonDispatch). On a server without the world lane it is never
// called → python plugins load with a nil bridge (their world builtins no-op).
func (m *Manager) SetPythonBridgeFactory(fn func(plugin string, capabilities []string) (WorldBridge, error)) {
	m.pythonBridgeFactory = fn
}

// SetPythonDispatch registers the off-tick python submit callback (Wave 2). The
// server wires it to TickLoop.submitPythonHook so a python plugin's hook is
// queued to the bounded plugin pool instead of run on the tick goroutine. Called
// ONCE before the tick loop owns the Manager (like SetPythonRuntime). On a server
// without the python lane it is never called → Emit skips python plugins.
func (m *Manager) SetPythonDispatch(fn func(pp PythonPlugin, event string, args []any)) {
	m.pythonDispatch = fn
}

// emitPython hands each loaded python plugin the event off-tick via the registered
// dispatch callback. It is called from Emit on the tick goroutine; the callback
// SUBMITS (drop-on-overload) and returns immediately, so the tick never blocks on
// python. A nil dispatch (no python lane) or no python plugins makes this a cheap
// no-op. The plugin's CallHook is a no-op for events it never registered, so it is
// correct to offer EVERY discrete event to EVERY python plugin — the per-event
// subscription lives inside the plugin's own captured hooks map (register_python.go),
// not in the host bus (which only tracks starlark hooks).
func (m *Manager) emitPython(evt EventType, payload Event) {
	if m.pythonDispatch == nil {
		return // no python lane wired (default build, or no SetPythonDispatch)
	}
	var args []any
	for _, p := range m.plugins {
		if p.python == nil {
			continue // a starlark plugin — handled inline by the m.hooks bus
		}
		if args == nil {
			args = payload.toArgs() // marshal once, lazily, only if a python plugin exists
		}
		m.pythonDispatch(p.python, string(evt), args)
	}
}

// New returns an empty Manager with an initialized hook map.
func New() *Manager {
	return &Manager{hooks: make(map[EventType][]Hook)}
}

// LoadDir scans root for plugin dirs, loads every runtime="starlark" plugin
// once, and lets each plugin's module body capture its hooks via the injected
// register builtin. Non-starlark runtimes are skipped (Phase-26 reserves
// "python").
//
// NESTED LAYOUT: a subdir WITHOUT a plugin.toml is treated as a CONTAINER and
// scanned recursively (bounded by maxPluginDirDepth). This lets operators group
// plugins into folders — e.g. plugins/mobs/vanilla_zombie/, plugins/mobs/vanilla_cow/
// — while every leaf plugin still loads independently under its own manifest caps.
// A subdir WITH a plugin.toml is loaded as a plugin (never recursed into).
//
// PER-PLUGIN TOLERANCE (Plan 28-02): the OPERATOR scan SKIPS a plugin that fails
// to load (logging it loudly) and CONTINUES with the rest, rather than aborting
// the whole scan on the first bad dir. The repo-root plugins/ ships operator-facing
// COPIES of the embedded boot-loaded plugins (vanilla_pig, crafting) that use
// boot-only builtins (declare_mob/goal) the operator scan does not inject — loading
// one via this path errors, and the old abort-the-scan behavior silently dropped
// every plugin AFTER the failing dir in directory order (so a real operator plugin
// like gate_events/customrecipe could vanish depending on its name). Skipping the
// failing plugin keeps every loadable operator plugin live (the embedded boot-load
// remains the authoritative copy of the duplicated ones). The strict, fail-fast
// LoadDirWith below is still used by the embedded boot-loads (each over a temp dir
// with exactly one plugin + its required builtins), where a failure IS fatal.
func (m *Manager) LoadDir(root string) error {
	return m.loadDir(root, nil, true /* tolerate per-plugin load failures */)
}

// LoadDirWith is LoadDir with extra host builtins injected into every plugin's
// predeclared set (on top of the sandbox allowlist + log + the per-plugin
// register). It is the reusable seam tests use to inject an observable counter
// builtin, and Plan 02's watcher uses to re-load a single plugin. extra keys
// win on a name collision with the host builtins.
func (m *Manager) LoadDirWith(root string, extra starlark.StringDict) error {
	return m.loadDir(root, extra, false /* strict: a load error aborts (boot-load discipline) */)
}

// loadDir is the shared scan body for LoadDir (tolerate=true) and LoadDirWith
// (tolerate=false). When tolerate is true, a per-plugin manifest/load error is
// logged and SKIPPED so the scan continues with the remaining plugins (the
// operator path — a single bad/duplicate dir must not silently drop the rest).
// When false, the first error aborts the whole scan (the embedded boot-load path,
// where each scan is one plugin in a temp dir and a failure is genuinely fatal).
func (m *Manager) loadDir(root string, extra starlark.StringDict, tolerate bool) error {
	return m.loadDirDepth(root, extra, tolerate, 0)
}

// maxPluginDirDepth bounds recursion into container directories (dirs with no
// plugin.toml, e.g. plugins/mobs/) so a symlink cycle or a pathological tree can
// never spin the loader. A plugin nested a few levels deep is fine; anything past
// this is refused loudly (or skipped when tolerating).
const maxPluginDirDepth = 8

// loadDirDepth is the recursive scan body. For each immediate subdir: if it holds
// a plugin.toml it is loaded as a plugin (the Phase-22 path); if it does NOT it is
// treated as a CONTAINER directory (e.g. plugins/mobs/) and its children are
// scanned recursively. This lets operators organize plugins into subfolders
// (plugins/mobs/vanilla_zombie/, plugins/mobs/vanilla_cow/, …) while every leaf
// plugin still loads independently under its own manifest caps.
func (m *Manager) loadDirDepth(root string, extra starlark.StringDict, tolerate bool, depth int) error {
	if depth > maxPluginDirDepth {
		if tolerate {
			log.Printf("plugin host: skipping %q (nesting exceeds %d levels)", root, maxPluginDirDepth)
			return nil
		}
		return fmt.Errorf("plugin host: %q nested deeper than %d levels", root, maxPluginDirDepth)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("host: scan %s: %w", root, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		manifestPath := filepath.Join(dir, "plugin.toml")
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			// No plugin.toml here: this is a container directory (e.g. plugins/mobs/).
			// Recurse so nested plugins are discovered. A dir with neither a manifest
			// nor any nested plugin is simply a no-op.
			if err := m.loadDirDepth(dir, extra, tolerate, depth+1); err != nil {
				return err
			}
			continue
		}
		if err := m.loadOnePlugin(dir, manifestPath, extra, tolerate); err != nil {
			return err
		}
	}
	return nil
}

// loadOnePlugin loads a single plugin whose manifest lives at manifestPath inside
// dir. It routes by the manifest runtime selector and captures the loaded module
// (or the python plugin). On a per-plugin failure it logs+skips when tolerate is
// true, else returns the error (aborting the scan).
func (m *Manager) loadOnePlugin(dir, manifestPath string, extra starlark.StringDict, tolerate bool) error {
	man, err := readManifest(manifestPath)
	if err != nil {
		if tolerate {
			log.Printf("plugin host: skipping %q (manifest error): %v", filepath.Base(dir), err)
			return nil
		}
		return fmt.Errorf("plugin %s: %w", filepath.Base(dir), err)
	}
	// Route by the manifest runtime selector. "starlark" falls through to the
	// inline Phase-22 load path below; "python" routes to the opt-in CPython
	// lane via the runtime-routing interface (loaded WITH the tag, skipped
	// gracefully WITHOUT it); any other runtime errors loudly so an unknown
	// runtime is never silently loaded (threat T-26-05, the Phase-22
	// "load loudly or skip" discipline).
	switch man.Runtime {
	case "starlark":
		// fall through to the existing starlark load path below.
	case "python":
		if m.pythonAvailable() {
			entry := filepath.Join(dir, man.Entrypoint)
			pp, err := m.pythonRuntime.Load(entry)
			if err != nil {
				if tolerate {
					log.Printf("plugin host: skipping %q (python load error): %v", man.Name, err)
					return nil
				}
				return fmt.Errorf("plugin %s load (python): %w", man.Name, err)
			}
			// WORLD-BRIDGE (Plan 26-03): build a per-plugin bridge stamped with
			// this plugin's manifest capabilities (the SAME parseCapabilities the
			// Phase-23 Starlark handles use) and install it so the plugin's
			// off-tick set_block/spawn/log/block_at builtins reach the owner. An
			// unknown capability errors here (load-loudly). A nil factory (no world
			// lane) leaves a nil bridge → the world builtins no-op.
			if m.pythonBridgeFactory != nil {
				bridge, berr := m.pythonBridgeFactory(man.Name, man.Capabilities)
				if berr != nil {
					if tolerate {
						log.Printf("plugin host: skipping %q (python capabilities error): %v", man.Name, berr)
						return nil
					}
					return fmt.Errorf("plugin %s capabilities (python): %w", man.Name, berr)
				}
				pp.SetWorldBridge(bridge)
			}
			m.plugins = append(m.plugins, &loadedPlugin{manifest: man, python: pp})
		} else {
			// Default (no-tag) build, or no python runtime registered: skip
			// gracefully and keep scanning (T-26-06 accept — a missing optional
			// runtime is not a server-down condition).
			log.Printf("plugin %q: python runtime not built in this binary; skipping", man.Name)
		}
		return nil
	default:
		if tolerate {
			log.Printf("plugin host: skipping %q (unknown runtime %q)", man.Name, man.Runtime)
			return nil
		}
		return fmt.Errorf("plugin %s: unknown runtime %q (want \"starlark\" or \"python\")", man.Name, man.Runtime)
	}
	// Predeclared = host builtins (log) + per-plugin register + the recipe
	// seam builtins (set_recipe_matcher/set_recipe_remaining/recipes) +
	// caller extra.
	predeclared := hostBuiltins()
	predeclared["register"] = m.makeRegisterBuiltin(man.Name)
	predeclared["chat"] = m.makeChatBuiltin()
	predeclared["set_recipe_matcher"] = m.makeSetMatcherBuiltin(man.Name)
	predeclared["set_recipe_remaining"] = m.makeSetRemainingBuiltin(man.Name)
	predeclared["recipes"] = m.makeRecipesBuiltin()
	for k, v := range extra {
		predeclared[k] = v
	}
	entry := filepath.Join(dir, man.Entrypoint)
	lp, err := starlarkpkg.LoadWith(entry, predeclared)
	if err != nil {
		if tolerate {
			log.Printf("plugin host: skipping %q (starlark load error): %v", man.Name, err)
			return nil
		}
		return fmt.Errorf("plugin %s load: %w", man.Name, err)
	}
	// The module body ran ONCE; its register(...) calls already populated
	// m.hooks. Hold the handle so GC keeps the frozen module alive.
	m.plugins = append(m.plugins, &loadedPlugin{manifest: man, loaded: lp})
	return nil
}

// Unload drops a plugin's hooks from every event slice and forgets its handle.
// MUST run on the tick goroutine if the tick loop is live (TICK-05) — or before
// it starts. Starlark has no explicit teardown; dropping the references lets GC
// reclaim the module.
func (m *Manager) Unload(name string) {
	for evt, hooks := range m.hooks {
		kept := hooks[:0]
		for _, h := range hooks {
			if h.plugin != name {
				kept = append(kept, h)
			}
		}
		m.hooks[evt] = kept
	}
	out := m.plugins[:0]
	for _, p := range m.plugins {
		if p.manifest.Name != name {
			out = append(out, p)
			continue
		}
		// Tear down the python handle if this was a python plugin (drops the
		// captured callable refs; no-op for starlark plugins, python == nil).
		if p.python != nil {
			p.python.Close()
		}
	}
	m.plugins = out

	// Drop the recipe matcher/remaining if the unloaded plugin owned them.
	if m.recipeOwner == name {
		m.recipeMatcher = nil
		m.recipeRemaining = nil
		m.recipeOwner = ""
	}
}

// PluginCount reports how many plugins are currently loaded. Exposed for tests
// and operator tooling.
func (m *Manager) PluginCount() int { return len(m.plugins) }

// HookCount reports the number of hooks registered for an event. Exposed for
// tests (register-once + unload assertions).
func (m *Manager) HookCount(evt EventType) int { return len(m.hooks[evt]) }
