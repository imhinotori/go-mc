package server

// vanilla_pig_embed.go — PLUGIN-04 (Plan 24-02): the BOOT-LOAD of the bundled vanilla_pig plugin
// (RESEARCH Pitfall 4 — the Phase-23-deferred follow-up). The SWAP (async.go/debug.go) makes the
// plugin pig the ONLY pig, so the vanilla_pig declaration MUST be present in the binary and live in a
// tick-owned registry BEFORE the first pig can spawn. We //go:embed the plugin (plugin.toml +
// main.star) so it is ALWAYS in the binary (an operator cannot delete it from a plugins/ dir and
// leave the swap with no pig), materialize it to a temp dir, and load it through the host's
// LoadDirWith with the server-owned declare_mob/goal builtins injected (the exact plugin_mob_test.go
// harness). The least-privilege caps come from the embedded manifest (stamped via setLoadCaps so the
// captured mobDecl carries the right grant). If the declaration is missing after load, the loader
// FAILS LOUDLY (not a silent spawn-time nil — Pitfall 4 / T-24-09).

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// vanillaPigFS embeds the bundled vanilla_pig plugin. The canonical operator-facing copy also ships
// at the repo-root plugins/vanilla_pig/; this embedded copy (server/assets/vanilla_pig/) is the
// source of truth for the SWAP (a swapped-on-disk plugins/ copy cannot widen the swap's caps — the
// embedded manifest governs, T-24-07). Keep the two copies identical.
//
//go:embed assets/vanilla_pig/plugin.toml assets/vanilla_pig/main.star
var vanillaPigFS embed.FS

// vanillaPigMobName is the declared mob name the swap sites look up.
const vanillaPigMobName = "vanilla_pig"

// loadVanillaPigRegistry materializes the embedded vanilla_pig plugin to a temp dir, parses its
// manifest capabilities, stamps them onto a fresh mobRegistry, and loads it through the host with the
// declare_mob/goal builtins injected — returning the registry holding the captured "vanilla_pig"
// declaration. It FAILS LOUDLY if the declaration is absent after load (Pitfall 4). The temp dir is
// removed before returning (the module body already captured into the registry at load — the files
// are not needed afterward). Called once at boot, before tick.Run (single-threaded, before the tick
// owns the registry — TICK-05).
func loadVanillaPigRegistry() (*mobRegistry, error) {
	dir, err := materializeVanillaPig()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	// Parse the embedded manifest's capabilities so the captured mobDecl carries the least-privilege
	// grant (NOT capAll). The embedded manifest is the source of truth (T-24-07).
	caps, err := vanillaPigCaps()
	if err != nil {
		return nil, err
	}

	r := newMobRegistry()
	r.setLoadCaps(caps)

	mgr := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := mgr.LoadDirWith(dir, extra); err != nil {
		return nil, fmt.Errorf("vanilla_pig boot-load: %w", err)
	}

	// LOUD failure if the declaration did not capture (Pitfall 4 / T-24-09): a missing "vanilla_pig"
	// at boot is a hard error, never a silent spawn-time nil (which would leave a pigless world).
	if _, ok := r.byName[vanillaPigMobName]; !ok {
		return nil, fmt.Errorf("vanilla_pig boot-load: the embedded plugin did not declare %q (the SWAP would have no pig)", vanillaPigMobName)
	}
	return r, nil
}

// materializeVanillaPig writes the embedded plugin (plugin.toml + main.star) into a fresh temp plugin
// dir layout (root/vanilla_pig/{plugin.toml,main.star}) that LoadDirWith can scan, returning the root.
// The caller removes the dir after load.
func materializeVanillaPig() (string, error) {
	root, err := os.MkdirTemp("", "sulfur-vanilla-pig-*")
	if err != nil {
		return "", fmt.Errorf("vanilla_pig boot-load: temp dir: %w", err)
	}
	dir := filepath.Join(root, vanillaPigMobName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		os.RemoveAll(root)
		return "", fmt.Errorf("vanilla_pig boot-load: mkdir: %w", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := vanillaPigFS.ReadFile("assets/vanilla_pig/" + name)
		if err != nil {
			os.RemoveAll(root)
			return "", fmt.Errorf("vanilla_pig boot-load: read embedded %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			os.RemoveAll(root)
			return "", fmt.Errorf("vanilla_pig boot-load: write %s: %w", name, err)
		}
	}
	return root, nil
}

// vanillaPigCaps parses the embedded plugin.toml's capabilities into a capSet (the least-privilege
// grant the goal callbacks' handles enforce). An unknown capability string is a loud error (the same
// parseCapabilities rule the rest of the plugin system uses).
func vanillaPigCaps() (capSet, error) {
	data, err := vanillaPigFS.ReadFile("assets/vanilla_pig/plugin.toml")
	if err != nil {
		return 0, fmt.Errorf("vanilla_pig boot-load: read manifest: %w", err)
	}
	var man struct {
		Capabilities []string `toml:"capabilities"`
	}
	if err := toml.Unmarshal(data, &man); err != nil {
		return 0, fmt.Errorf("vanilla_pig boot-load: parse manifest: %w", err)
	}
	caps, err := parseCapabilities(man.Capabilities)
	if err != nil {
		return 0, fmt.Errorf("vanilla_pig boot-load: %w", err)
	}
	return caps, nil
}
