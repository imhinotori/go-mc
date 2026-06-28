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
type loadedPlugin struct {
	manifest Manifest
	loaded   *starlarkpkg.LoadedPlugin
}

// Manager is the single owner of the loaded plugins + the typed event bus.
type Manager struct {
	plugins []*loadedPlugin
	hooks   map[EventType][]Hook
}

// New returns an empty Manager with an initialized hook map.
func New() *Manager {
	return &Manager{hooks: make(map[EventType][]Hook)}
}

// LoadDir scans root for plugin dirs (root/*/plugin.toml), loads every
// runtime="starlark" plugin once, and lets each plugin's module body capture
// its hooks via the injected register builtin. Non-starlark runtimes are
// skipped (Phase-26 reserves "python"). A manifest or load error aborts the
// whole scan.
func (m *Manager) LoadDir(root string) error {
	return m.LoadDirWith(root, nil)
}

// LoadDirWith is LoadDir with extra host builtins injected into every plugin's
// predeclared set (on top of the sandbox allowlist + log + the per-plugin
// register). It is the reusable seam tests use to inject an observable counter
// builtin, and Plan 02's watcher uses to re-load a single plugin. extra keys
// win on a name collision with the host builtins.
func (m *Manager) LoadDirWith(root string, extra starlark.StringDict) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("host: scan %s: %w", root, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		man, err := readManifest(filepath.Join(dir, "plugin.toml"))
		if err != nil {
			return fmt.Errorf("plugin %s: %w", e.Name(), err)
		}
		if man.Runtime != "starlark" {
			continue // Phase 26 handles runtime=="python"; skip for the default build
		}
		// Predeclared = host builtins (log) + per-plugin register + caller extra.
		predeclared := hostBuiltins()
		predeclared["register"] = m.makeRegisterBuiltin(man.Name)
		for k, v := range extra {
			predeclared[k] = v
		}
		entry := filepath.Join(dir, man.Entrypoint)
		lp, err := starlarkpkg.LoadWith(entry, predeclared)
		if err != nil {
			return fmt.Errorf("plugin %s load: %w", man.Name, err)
		}
		// The module body ran ONCE; its register(...) calls already populated
		// m.hooks. Hold the handle so GC keeps the frozen module alive.
		m.plugins = append(m.plugins, &loadedPlugin{manifest: man, loaded: lp})
	}
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
		}
	}
	m.plugins = out
}

// PluginCount reports how many plugins are currently loaded. Exposed for tests
// and operator tooling.
func (m *Manager) PluginCount() int { return len(m.plugins) }

// HookCount reports the number of hooks registered for an event. Exposed for
// tests (register-once + unload assertions).
func (m *Manager) HookCount(evt EventType) int { return len(m.hooks[evt]) }
