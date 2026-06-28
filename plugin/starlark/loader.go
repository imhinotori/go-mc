package starlark

import (
	"fmt"
	"os"

	"go.starlark.net/starlark"
)

// Load reads a .star file from disk (the dir/path seam a later phase extends to
// discovery/manifest) and executes it ONCE: ExecFileOptions parses + compiles +
// runs the module body a single time and returns its globals, which are
// AUTO-FROZEN on completion. The module is not re-parsed per later Call.
//
// Load uses ONLY the sandbox allowlist (safeGlobals) as the predeclared set.
// A host that needs to inject extra builtins (e.g. the plugin manager's
// `register`) uses LoadWith — Load is the zero-extra path through it.
func Load(path string) (*LoadedPlugin, error) {
	return LoadWith(path, nil)
}

// LoadWith is Load with an injectable predeclared StringDict. The final
// predeclared set is safeGlobals() MERGED with extra: the sandbox allowlist is
// always present, and the host's extra builtins are added on top (extra keys
// win on a name collision). The merge COPIES safeGlobals() into a fresh map
// before adding extra, so the shared allowlist is never mutated.
//
// This is the seam the plugin host uses to inject `register` into the
// predeclared globals so a plugin's module body can capture its hooks at load.
// Like Load, it execs the module body exactly ONCE; the returned globals are
// auto-frozen.
func LoadWith(path string, extra starlark.StringDict) (*LoadedPlugin, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("starlark: read %s: %w", path, err)
	}
	predeclared := safeGlobals() // fresh map per call; safe to mutate
	for name, v := range extra {
		predeclared[name] = v // extra wins on collision
	}
	th := newThread(path)
	globals, err := starlark.ExecFileOptions(defaultFileOptions(), th, path, src, predeclared)
	if err != nil {
		return nil, fmt.Errorf("starlark: load %s: %w", path, err)
	}
	return &LoadedPlugin{path: path, globals: globals}, nil
}
