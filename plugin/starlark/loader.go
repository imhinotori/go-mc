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
func Load(path string) (*LoadedPlugin, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("starlark: read %s: %w", path, err)
	}
	th := newThread(path)
	globals, err := starlark.ExecFileOptions(defaultFileOptions(), th, path, src, safeGlobals())
	if err != nil {
		return nil, fmt.Errorf("starlark: load %s: %w", path, err)
	}
	return &LoadedPlugin{path: path, globals: globals}, nil
}
