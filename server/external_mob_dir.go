package server

// external_mob_dir.go -- Bug #11 (plugin-loader): make the operator-facing mob plugins under
// plugins/mobs/ ACTUALLY LOAD into the tick-owned mob registry, so an operator CAN drop a new mob
// plugin on disk and it works, instead of the general hook manager (host.New()/LoadDir in
// cmd/sulfur/main.go) trying to load them WITHOUT the declare_mob/goal/declare_model builtins and
// spamming the boot log with "undefined: declare_mob" for every dir.
//
// TWO-LOADER SPLIT (the root cause): the bundled 28 vanilla mobs load from the EMBEDDED registry
// (loadVanillaMobRegistry, vanilla_pig_embed.go), which injects the FULL declaration vocabulary
// (mobRegistry.builtinsDict: declare_mob/goal/skill/mechanic/targeter/condition/declare_model/...).
// The general pluginMgr.LoadDir("plugins") does NOT inject that vocabulary -- it is the hook bus
// (register/chat/recipe builtins only) -- so a mob .star that calls declare_mob errored there. The
// repo ships plugins/mobs/vanilla_<mob>/ copies BYTE-IDENTICAL to the embedded
// server/assets/vanilla_<mob>/ copies, so they were pure dead + noisy under the general manager.
//
// THE FIX (Option A): the MOB registry -- the domain owner of declare_mob -- ALSO scans plugins/mobs/
// from disk with the declare builtins injected, so on-disk mob plugins are live. The general manager
// is told to SKIP the mobs/ container (LoadDirExcept, cmd/sulfur/main.go), so it no longer tries (and
// fails) to load them.
//
// EMBED-AUTHORITATIVE DEDUP: the embedded copy of a vanilla mob is the source of truth for the SWAP
// (its manifest governs the caps a swapped-on-disk copy cannot widen -- T-24-07, vanilla_pig_embed.go).
// So a disk plugin whose declare_mob(name=...) collides with an ALREADY-loaded name (every one of the
// 28 shipped duplicates, and critically vanilla_pig) is SKIPPED -- the embedded declaration stays
// authoritative and the pig oracle is byte-identically untouched (no double-load, no re-declaration,
// no RNG change). A genuinely NEW operator mob (a name not in the embedded set) LOADS. This mirrors
// registerWanderMob merge-a-captured-decl-into-the-existing-registry discipline (wandermob_embed.go).
//
// SINGLE-OWNER (TICK-05): called once at boot, AFTER SetMobRegistry and BEFORE tick.Run, so the
// registry is being WRITTEN under the single-owner-at-boot rule (no tick goroutine reads it yet) --
// race-free without a lock, exactly like the embedded boot-load + the wandermob merge.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/imhinotori/sulfur/plugin/host"
)

// ExternalMobsContainer is the immediate-child container directory under the plugins/ root that the
// tick-owned mob registry OWNS (declare_mob/goal/declare_model live here). cmd/sulfur/main.go passes
// this name to pluginMgr.LoadDirExcept so the general hook manager does NOT recurse into it (it lacks
// the declare builtins), and LoadExternalMobs scans plugins/<this>/ into the mob registry instead.
const ExternalMobsContainer = "mobs"

// LoadExternalMobs scans an on-disk mob-plugin directory (e.g. plugins/mobs/) and merges every
// loadable leaf mob plugin into the tick already-installed mob registry, injecting the full
// declare_mob/goal/declare_model vocabulary the registry owns. Each disk plugin is loaded under its
// OWN manifest capabilities (least-privilege, the same per-load stamping the embedded boot-load uses).
//
// EMBED-AUTHORITATIVE DEDUP: a disk plugin whose declared mob name is ALREADY present in the registry
// (an embedded vanilla mob -- every one of the shipped plugins/mobs/vanilla_* copies) is skipped so the
// embedded declaration stays authoritative (the pig oracle is untouched). Only a NEW name loads.
//
// It returns (loaded, error): loaded is the count of NEW disk mobs merged in (0 when the dir holds only
// duplicates of the embedded set, as the shipped repo does). A missing dir is a clean no-op (0, nil) --
// an operator with no plugins/mobs/ is fine. A genuine load error on a NEW mob is returned so the boot
// logs it. Called once at boot, before tick.Run (TICK-05, single-owner-at-boot).
func (t *TickLoop) LoadExternalMobs(dir string) (int, error) {
	if t.mobRegistry == nil {
		return 0, fmt.Errorf("external mob load: no mob registry installed (SetMobRegistry must run first)")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return 0, nil // no plugins/mobs/ on disk -- a clean no-op (operator ships none)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("external mob load: scan %s: %w", dir, err)
	}
	loaded := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		leaf := filepath.Join(dir, e.Name())
		manifestPath := filepath.Join(leaf, "plugin.toml")
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			continue // not a plugin dir (no manifest) -- skip (matches host container rule)
		}
		n, lerr := t.loadOneExternalMob(leaf, manifestPath)
		if lerr != nil {
			return loaded, lerr
		}
		loaded += n
	}
	return loaded, nil
}

// loadOneExternalMob loads a single on-disk mob plugin at leaf (manifest at manifestPath) into the
// tick mob registry. It parses the manifest name + capabilities, SKIPS the plugin if its name is
// already declared (embed-authoritative dedup), else stamps the per-plugin caps and loads it through
// the host with the declare builtins injected. Returns (1, nil) for a NEW mob merged, (0, nil) for a
// dedup-skip, and (0, err) for a genuine load failure on a new mob. Mirrors loadOneVanillaMob
// (vanilla_pig_embed.go) but over a REAL operator dir with the embed-dedup guard.
func (t *TickLoop) loadOneExternalMob(leaf, manifestPath string) (int, error) {
	base := filepath.Base(leaf)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return 0, fmt.Errorf("external mob load %s: read manifest: %w", base, err)
	}
	var man struct {
		Name         string   `toml:"name"`
		Capabilities []string `toml:"capabilities"`
	}
	if err := toml.Unmarshal(data, &man); err != nil {
		return 0, fmt.Errorf("external mob load %s: parse manifest: %w", base, err)
	}

	// EMBED-AUTHORITATIVE DEDUP: the declared mob name equals the plugin dir name for the vanilla
	// copies (vanilla_pig, vanilla_zombie, ...), and declare_mob(name=...) inside main.star matches it.
	// If the manifest name is ALREADY registered (an embedded vanilla mob), skip the disk copy entirely
	// -- the embedded declaration governs (the pig oracle stays byte-identical, T-24-07). A plugin whose
	// declared mob name differs from its manifest name is still caught by declare_mob own duplicate
	// guard (plugin_mob_decl.go), which loudly rejects re-declaring an already-loaded name.
	if man.Name != "" {
		if _, dup := t.mobRegistry.byName[man.Name]; dup {
			return 0, nil // embedded copy is authoritative -- skip the on-disk duplicate
		}
	}

	// Least-privilege caps from THIS plugin manifest (per-load stamp, plugin_mob_decl.go setLoadCaps),
	// the same rule the embedded boot-load applies before each mob LoadDirWith.
	caps, err := parseCapabilities(man.Capabilities)
	if err != nil {
		return 0, fmt.Errorf("external mob load %s: %w", base, err)
	}

	// LoadDirWith scans a ROOT for child plugin dirs (root/<plugin>/{plugin.toml,main.star}). The leaf
	// IS a plugin dir, so materialize a one-child temp root holding a copy of it (the exact
	// materializeVanillaMob pattern, over disk bytes instead of embed bytes) and load that root. A temp
	// root (not the leaf real parent) keeps the scan to exactly this one plugin -- a sibling in
	// plugins/mobs/ is loaded by its OWN loadOneExternalMob call, never twice.
	root, err := os.MkdirTemp("", "sulfur-extmob-*")
	if err != nil {
		return 0, fmt.Errorf("external mob load %s: temp dir: %w", base, err)
	}
	defer os.RemoveAll(root)
	child := filepath.Join(root, base)
	if err := os.MkdirAll(child, 0o755); err != nil {
		return 0, fmt.Errorf("external mob load %s: mkdir: %w", base, err)
	}
	for _, f := range []string{"plugin.toml", "main.star"} {
		b, rerr := os.ReadFile(filepath.Join(leaf, f))
		if rerr != nil {
			return 0, fmt.Errorf("external mob load %s: read %s: %w", base, f, rerr)
		}
		if werr := os.WriteFile(filepath.Join(child, f), b, 0o644); werr != nil {
			return 0, fmt.Errorf("external mob load %s: write %s: %w", base, f, werr)
		}
	}

	before := t.mobRegistry.Count()
	t.mobRegistry.setLoadCaps(caps)
	mgr := host.New()
	extra := t.mobRegistry.builtinsDict()
	if err := mgr.LoadDirWith(root, extra); err != nil {
		return 0, fmt.Errorf("external mob load %s: %w", base, err)
	}
	if t.mobRegistry.Count() > before {
		return 1, nil // a NEW declaration captured
	}
	return 0, nil // the plugin declared nothing new
}
