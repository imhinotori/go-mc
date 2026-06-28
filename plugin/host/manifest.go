package host

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Manifest is a plugin's plugin.toml. The 4 required fields (Name, Version,
// Entrypoint, Runtime) must be non-empty; Capabilities is OPTIONAL and is
// PARSED + STORED but NOT enforced in Phase 22 — there are no live entity/world
// handles to restrict yet (enforcement lands with the Phase-23 handle API,
// locked decision). The Runtime field is the Phase-26 selector ("starlark" now;
// "python" reserved).
type Manifest struct {
	Name         string   `toml:"name"`
	Version      string   `toml:"version"`
	Entrypoint   string   `toml:"entrypoint"`
	Runtime      string   `toml:"runtime"`
	Capabilities []string `toml:"capabilities"`
}

// readManifest decodes a plugin.toml, validates the 4 required fields, and
// guards the entrypoint against path traversal (threat T-22-03): an entrypoint
// containing ".." or an absolute path is rejected so the load path always stays
// confined to the plugin dir. The returned Entrypoint is filepath.Clean'd.
func readManifest(path string) (Manifest, error) {
	var m Manifest
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest %s: %w", path, err)
	}
	if m.Name == "" {
		return Manifest{}, fmt.Errorf("manifest %s: missing required field \"name\"", path)
	}
	if m.Version == "" {
		return Manifest{}, fmt.Errorf("manifest %s: missing required field \"version\"", path)
	}
	if m.Entrypoint == "" {
		return Manifest{}, fmt.Errorf("manifest %s: missing required field \"entrypoint\"", path)
	}
	if m.Runtime == "" {
		return Manifest{}, fmt.Errorf("manifest %s: missing required field \"runtime\"", path)
	}
	// Path-traversal guard (T-22-03): the entrypoint must be a relative path
	// inside the plugin dir. Reject absolute paths and any ".." segment.
	clean := filepath.Clean(m.Entrypoint)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(clean, ".."+string(filepath.Separator)) || clean == "." {
		return Manifest{}, fmt.Errorf("manifest %s: invalid entrypoint %q (must be a relative path within the plugin dir)", path, m.Entrypoint)
	}
	m.Entrypoint = clean
	return m, nil
}
