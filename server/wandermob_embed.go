package server

// wandermob_embed.go — PLUGIN-07 (Plan 28-01): the BOOT-LOAD of the bundled CUSTOM wander-mob plugin
// into the LIVE registry (CONTEXT D-2). Phase 23 deliberately left the wander decl in an isolated
// testdata root (server/testdata/mobplugins/wandermob/) — a gate fixture, never live. This plan
// PROMOTES it to a //go:embed'd asset (server/assets/wandermob/, mirroring vanilla_pig_embed.go) and
// boot-loads it alongside vanilla_pig so the custom mob is spawnable in the running server — the seam
// Plan 02's bot needs to spawn it via the SULFUR_TEST_KIT trigger.
//
// CUSTOM (free of the 1:1 mandate): the wanderer is a CUSTOM mob (base_type pig → it renders as the
// pig wire id; "custom" = the BEHAVIOR, one MOVE goal that nav-targets a nearby point so it WALKS via
// the Go nav). It is NOT a vanilla port, so it carries no behavior-identity obligation (v4-PLAN).
//
// SINGLE-OWNER (TICK-05): like loadVanillaPigRegistry, this WRITES the registry only at load
// (single-threaded, before the tick owns it). The boot path (cmd/sulfur/main.go) MERGES the captured
// "wanderer" decl into the SAME tick-owned registry vanilla_pig installs, before tick.Run — so both
// vanilla_pig AND wanderer are spawnable, with no lock (the registry is read-only thereafter).
//
// FAIL LOUDLY (Pitfall 4 / mirrors vanilla_pig): a missing "wanderer" decl after load is a hard error,
// never a silent spawn-time nil.

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/imhinotori/sulfur/plugin/host"
)

// wanderMobFS embeds the bundled custom wander-mob plugin (plugin.toml + main.star). Keep this copy
// identical to server/testdata/mobplugins/wandermob/ (the Phase-23 gate fixture); the embedded asset
// under server/assets/wandermob/ is the source of truth for the LIVE boot-load.
//
//go:embed assets/wandermob/plugin.toml assets/wandermob/main.star
var wanderMobFS embed.FS

// wanderMobName is the declared mob name the boot-load merge + the test-kit trigger look up. It MUST
// match declare_mob(name="wanderer") in the embedded main.star.
const wanderMobName = "wanderer"

// LoadWanderMobRegistry is the exported boot-load entry point cmd/sulfur/main.go calls: it
// materializes + loads the embedded wander-mob plugin and returns the populated registry holding the
// captured "wanderer" declaration. It fails loudly if the declaration is absent (Pitfall 4). Kept a
// thin export over the package-internal loader so the registry type stays unexported (the caller
// treats it as an opaque handle to merge into the installed vanilla_pig registry).
func LoadWanderMobRegistry() (*mobRegistry, error) { return loadWanderMobRegistry() }

// loadWanderMobRegistry materializes the embedded wander-mob plugin to a temp dir, parses its manifest
// capabilities, stamps them onto a fresh mobRegistry, and loads it through the host with the
// declare_mob/goal builtins injected — returning the registry holding the captured "wanderer"
// declaration. It FAILS LOUDLY if the declaration is absent after load (Pitfall 4). The temp dir is
// removed before returning (the module body already captured into the registry at load). Called once
// at boot, before tick.Run (single-threaded — TICK-05). This is the EXACT loadVanillaPigRegistry
// pattern parameterized for the wander mob's asset path + name.
func loadWanderMobRegistry() (*mobRegistry, error) {
	dir, err := materializeWanderMob()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	// Parse the embedded manifest's capabilities so the captured mobDecl carries the least-privilege
	// grant (the manifest declares entities.read/entities.write/nav — the wander goal needs nav).
	caps, err := wanderMobCaps()
	if err != nil {
		return nil, err
	}

	r := newMobRegistry()
	r.setLoadCaps(caps)

	mgr := host.New()
	// The FULL declaration vocabulary (declare_mob/goal + the SKILLS-01 builtins — builtinsDict).
	extra := r.builtinsDict()
	if err := mgr.LoadDirWith(dir, extra); err != nil {
		return nil, fmt.Errorf("wandermob boot-load: %w", err)
	}

	// LOUD failure if the declaration did not capture (Pitfall 4): a missing "wanderer" at boot is a
	// hard error, never a silent spawn-time nil.
	if _, ok := r.byName[wanderMobName]; !ok {
		return nil, fmt.Errorf("wandermob boot-load: the embedded plugin did not declare %q (the spawn trigger would have no mob)", wanderMobName)
	}
	return r, nil
}

// materializeWanderMob writes the embedded plugin (plugin.toml + main.star) into a fresh temp plugin
// dir layout (root/wanderer/{plugin.toml,main.star}) that LoadDirWith can scan, returning the root.
// The caller removes the dir after load.
func materializeWanderMob() (string, error) {
	root, err := os.MkdirTemp("", "sulfur-wandermob-*")
	if err != nil {
		return "", fmt.Errorf("wandermob boot-load: temp dir: %w", err)
	}
	dir := filepath.Join(root, wanderMobName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		os.RemoveAll(root)
		return "", fmt.Errorf("wandermob boot-load: mkdir: %w", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := wanderMobFS.ReadFile("assets/wandermob/" + name)
		if err != nil {
			os.RemoveAll(root)
			return "", fmt.Errorf("wandermob boot-load: read embedded %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			os.RemoveAll(root)
			return "", fmt.Errorf("wandermob boot-load: write %s: %w", name, err)
		}
	}
	return root, nil
}

// wanderMobCaps parses the embedded plugin.toml's capabilities into a capSet (the least-privilege
// grant the goal callbacks' handles enforce). An unknown capability string is a loud error (the same
// parseCapabilities rule the rest of the plugin system uses).
func wanderMobCaps() (capSet, error) {
	data, err := wanderMobFS.ReadFile("assets/wandermob/plugin.toml")
	if err != nil {
		return 0, fmt.Errorf("wandermob boot-load: read manifest: %w", err)
	}
	var man struct {
		Capabilities []string `toml:"capabilities"`
	}
	if err := toml.Unmarshal(data, &man); err != nil {
		return 0, fmt.Errorf("wandermob boot-load: parse manifest: %w", err)
	}
	caps, err := parseCapabilities(man.Capabilities)
	if err != nil {
		return 0, fmt.Errorf("wandermob boot-load: %w", err)
	}
	return caps, nil
}

// registerWanderMob merges the embedded wander-mob declaration into an EXISTING tick-owned registry
// (the one SetMobRegistry installed for vanilla_pig). It boot-loads the wander plugin into a scratch
// registry and copies its captured "wanderer" decl into dst.byName, so the SAME registry holds both
// vanilla_pig AND wanderer. Called once at boot, BEFORE tick.Run — the registry is being WRITTEN under
// the single-owner-at-boot discipline (TICK-05): no tick goroutine reads it yet, so the map write is
// race-free without a lock (identical to the vanilla_pig install). A nil dst, a load failure, or a
// name collision is a loud error (a broken embedded plugin / a wiring bug must surface at boot).
func registerWanderMob(dst *mobRegistry) error {
	if dst == nil {
		return fmt.Errorf("wandermob boot-load: nil destination registry (SetMobRegistry must run first)")
	}
	src, err := loadWanderMobRegistry()
	if err != nil {
		return err
	}
	decl, ok := src.byName[wanderMobName]
	if !ok {
		// loadWanderMobRegistry already guards this; the redundant check keeps the merge self-contained.
		return fmt.Errorf("wandermob boot-load: the embedded plugin did not declare %q", wanderMobName)
	}
	if _, dup := dst.byName[wanderMobName]; dup {
		return fmt.Errorf("wandermob boot-load: %q already present in the registry (duplicate boot-load)", wanderMobName)
	}
	dst.byName[wanderMobName] = decl
	return nil
}

// RegisterWanderMob is the exported boot entry point cmd/sulfur/main.go calls AFTER SetMobRegistry:
// it merges the embedded custom wander mob into the tick's already-installed (vanilla_pig) registry so
// both mobs are spawnable from the SAME tick-owned registry. It is a no-op-safe boot step (single-
// threaded, before tick.Run — TICK-05). Returns an error the caller logs FATAL (a broken embedded
// plugin is a broken server). Kept an exported method (not a free function over *mobRegistry) so the
// registry type stays unexported — the caller never names it.
func (t *TickLoop) RegisterWanderMob() error {
	if t.mobRegistry == nil {
		return fmt.Errorf("wandermob boot-load: no mob registry installed (SetMobRegistry must run first)")
	}
	return registerWanderMob(t.mobRegistry)
}
