package server

// vanilla_pig_embed.go — PLUGIN-04 (Plan 24-02) generalized in Plan 34-04 (THE GATE): the BOOT-LOAD of
// the bundled vanilla mob plugins (pig + the Phase-34 cow/sheep/chicken). The SWAP (async.go/debug.go)
// makes the plugin mobs the ONLY mobs, so EACH vanilla_<mob> declaration MUST be present in the binary
// and live in ONE tick-owned registry BEFORE the first mob can spawn (RESEARCH Pitfall 4). We
// //go:embed the plugins (plugin.toml + main.star per mob) so they are ALWAYS in the binary (an
// operator cannot delete one from a plugins/ dir and leave the swap with no mob), materialize each to a
// temp dir, and load it through the host's LoadDirWith with the server-owned declare_mob/goal builtins
// injected (the exact plugin_mob_test.go harness). The least-privilege caps come from EACH embedded
// manifest (stamped via setLoadCaps PER MOB so the captured mobDecl carries the right grant — caps are
// per-load, plugin_mob_decl.go:95). If ANY of the 4 declarations is missing after load, the loader
// FAILS LOUDLY (not a silent spawn-time nil — Pitfall 4 / T-24-09 / T-34-10).

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/imhinotori/sulfur/plugin/host"
	"go.starlark.net/starlark"
)

// vanillaMobFS embeds the bundled vanilla mob plugins. The canonical operator-facing copies also ship
// at the repo-root plugins/vanilla_<mob>/; these embedded copies (server/assets/vanilla_<mob>/) are the
// source of truth for the SWAP (a swapped-on-disk plugins/ copy cannot widen the swap's caps — the
// embedded manifest governs, T-24-07). Keep each repo-root/embed pair byte-identical. Embedding the
// dirs covers plugin.toml + main.star for each mob.
//
//go:embed assets/vanilla_pig assets/vanilla_cow assets/vanilla_sheep assets/vanilla_chicken assets/vanilla_zombie assets/vanilla_skeleton assets/vanilla_spider assets/vanilla_wolf assets/vanilla_husk assets/vanilla_mooshroom assets/vanilla_silverfish assets/vanilla_creeper assets/vanilla_witch assets/vanilla_rabbit assets/vanilla_enderman
var vanillaMobFS embed.FS

// The declared mob names the swap sites look up. Each is the directory name under assets/ AND the
// declare_mob(name=...) the plugin emits — the two must agree (the per-mob load asserts the name
// captured).
const (
	vanillaPigMobName     = "vanilla_pig"
	vanillaCowMobName     = "vanilla_cow"
	vanillaSheepMobName   = "vanilla_sheep"
	vanillaChickenMobName = "vanilla_chicken"

	// Phase 35 hostiles (declared here as the canonical mob-name home so the spawn-gating picker —
	// naturalMonsterMobNames / pickNaturalMonsterMob, async.go — and the later hostile-plugin embed
	// plans (35-03..05) share ONE definition, never a duplicate const). These names are the directory
	// names under assets/ AND the declare_mob(name=...) the hostile plugins emit; the embed directive
	// (vanillaMobFS) + vanillaMobNames load order are EXTENDED by the hostile-plugin plans to actually
	// boot-load them. Defining the constants now (Phase 35-02) is purely additive: the spawn picker
	// references them, and the spawnVanillaMob registry lookup resolves them once the declarations load.
	vanillaZombieMobName   = "vanilla_zombie"
	vanillaSkeletonMobName = "vanilla_skeleton"
	vanillaSpiderMobName   = "vanilla_spider"

	// Phase 36 wolf (declared here as the canonical mob-name home, the SAME forward-declaration pattern
	// the Phase-35 hostiles used above: the const + the /dbg arm landed in the 36-01 foundation plan).
	// Plan 36-03 (the .star plugin) NOW ships assets/vanilla_wolf/{plugin.toml,main.star} and COMPLETES
	// the boot-load by adding assets/vanilla_wolf to the //go:embed directive above + appending
	// vanillaWolfMobName to vanillaMobNames below (the embed + load entry the foundation plan deferred
	// because the directory did not yet exist — embedding a missing directory is a hard compile error).
	// With the asset present, spawnVanillaMob(vanillaWolfMobName) + /dbg wolf now resolve a real
	// boot-loaded declaration.
	vanillaWolfMobName = "vanilla_wolf"

	// MOB-VARIANT (Task #9): the zero-subsystem variants. Husk (extends Zombie, MONSTER) and Mooshroom
	// (extends AbstractCow, CREATURE) reuse the parent's goals + attributes verbatim — the plugins are the
	// zombie/cow declarations with base_type husk/mooshroom. Additive to the embed + load order below.
	vanillaHuskMobName      = "vanilla_husk"
	vanillaMooshroomMobName = "vanilla_mooshroom"

	// MOB-HOST-05 (Task #9): the Silverfish — a MONSTER whose CORE hunt+melee ports on the existing
	// float + Go-native combat seams (the stone-infest goals are cite-deferred). Additive.
	vanillaSilverfishMobName = "vanilla_silverfish"

	// MOB-HOST-06 (Task #9): the Creeper — hunt + SwellGoal fuse + ServerExplosion (entity damage +
	// knockback; block destruction cite-deferred). Additive.
	vanillaCreeperMobName = "vanilla_creeper"

	// MOB-HOST-07 (Task #9): the Witch — hunt + RangedAttackGoal throwing splash potions (the mob-effect
	// subsystem). Additive.
	vanillaWitchMobName = "vanilla_witch"

	// MOB-PASS-05 (Task #9): the Rabbit — the portable ambient goal slice (float/panic/breed/tempt/stroll/
	// look); the hop-movement + avoid/raid goals are cite-deferred. Additive.
	vanillaRabbitMobName = "vanilla_rabbit"

	// MOB-HOST-08 (Task #9): the Enderman — hunt + melee + the Go-native teleport (daylight-flee +
	// hurt-dodge); the gaze/block-carry goals are cite-deferred. Additive.
	vanillaEndermanMobName = "vanilla_enderman"
)

// vanillaMobNames is the load order: ALL EIGHT bundled mobs (the 4 passives + the 3 Phase-35 hostiles +
// the Phase-36 wolf) load into the ONE registry. The pig is first to keep the boot log + any pig-first
// diagnostics stable, but order is otherwise irrelevant (each mob loads independently under its own
// caps). A missing entry here means that mob never boot-loads — and the SWAP would have no such mob (the
// LOUD re-assertion in loadVanillaMobRegistry catches it). The hostiles + the wolf are additive: the
// loader + the re-assertion are category-agnostic (a MONSTER or a tameable CREATURE declares its
// goal/target sets exactly as a passive CREATURE declares its goals).
var vanillaMobNames = []string{
	vanillaPigMobName,
	vanillaCowMobName,
	vanillaSheepMobName,
	vanillaChickenMobName,
	vanillaZombieMobName,
	vanillaSkeletonMobName,
	vanillaSpiderMobName,
	vanillaWolfMobName,
	vanillaHuskMobName,
	vanillaMooshroomMobName,
	vanillaSilverfishMobName,
	vanillaCreeperMobName,
	vanillaWitchMobName,
	vanillaRabbitMobName,
	vanillaEndermanMobName,
}

// loadVanillaMobRegistry materializes EACH bundled vanilla mob plugin to a temp dir, parses its
// manifest capabilities, stamps them onto the SHARED mobRegistry (per-mob, before that mob's load —
// caps are per-load), and loads it through the host with the declare_mob/goal builtins injected —
// returning the ONE registry holding ALL FOUR captured declarations. It FAILS LOUDLY if ANY declaration
// is absent after its load (Pitfall 4 / T-34-10). Each temp dir is removed before the next mob loads
// (the module body already captured into the registry at load — the files are not needed afterward).
// Called once at boot, before tick.Run (single-threaded, before the tick owns the registry — TICK-05).
//
// IMPORTANT: ONE registry r is created before the loop; all 4 mobs load into it (the byName map holds
// all 4 — plugin_mob_decl.go:80-83). setLoadCaps is called PER MOB right before its LoadDirWith so each
// captured mobDecl carries ITS OWN manifest's least-privilege grant (not the previous mob's, not capAll).
func loadVanillaMobRegistry() (*mobRegistry, error) {
	r := newMobRegistry()
	mgr := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}

	for _, name := range vanillaMobNames {
		if err := loadOneVanillaMob(r, mgr, extra, name); err != nil {
			return nil, err
		}
	}

	// LOUD failure if ANY declaration did not capture (Pitfall 4 / T-24-09 / T-34-10): a missing
	// vanilla_<mob> at boot is a hard error, never a silent spawn-time nil (which would leave that mob
	// un-spawnable). Re-assert EACH name after the whole load so a name collision (two plugins claiming
	// one name) also surfaces here.
	for _, name := range vanillaMobNames {
		if _, ok := r.byName[name]; !ok {
			return nil, fmt.Errorf("vanilla mob boot-load: the embedded plugin did not declare %q (the SWAP would have no %s)", name, name)
		}
	}
	return r, nil
}

// loadOneVanillaMob materializes the embedded plugin for one mob, stamps its manifest caps onto the
// shared registry (per-load), and loads it through the host into that registry. The temp dir is removed
// before returning. The per-mob LOUD assertion lives in the caller (after all loads) so a name that
// failed to capture is reported uniformly.
func loadOneVanillaMob(r *mobRegistry, mgr *host.Manager, extra starlark.StringDict, name string) error {
	dir, err := materializeVanillaMob(name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// Parse THIS mob's embedded manifest capabilities so the captured mobDecl carries the
	// least-privilege grant (NOT capAll, NOT the previous mob's). The embedded manifest is the source
	// of truth (T-24-07). setLoadCaps is per-load (plugin_mob_decl.go:95) — set it right before the
	// LoadDirWith for this mob.
	caps, err := vanillaMobCaps(name)
	if err != nil {
		return err
	}
	r.setLoadCaps(caps)

	if err := mgr.LoadDirWith(dir, extra); err != nil {
		return fmt.Errorf("%s boot-load: %w", name, err)
	}
	return nil
}

// materializeVanillaMob writes the embedded plugin (plugin.toml + main.star) for the named mob into a
// fresh temp plugin dir layout (root/<name>/{plugin.toml,main.star}) that LoadDirWith can scan,
// returning the root. The caller removes the dir after load.
func materializeVanillaMob(name string) (string, error) {
	root, err := os.MkdirTemp("", "sulfur-"+name+"-*")
	if err != nil {
		return "", fmt.Errorf("%s boot-load: temp dir: %w", name, err)
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		os.RemoveAll(root)
		return "", fmt.Errorf("%s boot-load: mkdir: %w", name, err)
	}
	for _, file := range []string{"plugin.toml", "main.star"} {
		data, err := vanillaMobFS.ReadFile("assets/" + name + "/" + file)
		if err != nil {
			os.RemoveAll(root)
			return "", fmt.Errorf("%s boot-load: read embedded %s: %w", name, file, err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			os.RemoveAll(root)
			return "", fmt.Errorf("%s boot-load: write %s: %w", name, file, err)
		}
	}
	return root, nil
}

// vanillaMobCaps parses the named mob's embedded plugin.toml capabilities into a capSet (the
// least-privilege grant the goal callbacks' handles enforce). An unknown capability string is a loud
// error (the same parseCapabilities rule the rest of the plugin system uses).
func vanillaMobCaps(name string) (capSet, error) {
	data, err := vanillaMobFS.ReadFile("assets/" + name + "/plugin.toml")
	if err != nil {
		return 0, fmt.Errorf("%s boot-load: read manifest: %w", name, err)
	}
	var man struct {
		Capabilities []string `toml:"capabilities"`
	}
	if err := toml.Unmarshal(data, &man); err != nil {
		return 0, fmt.Errorf("%s boot-load: parse manifest: %w", name, err)
	}
	caps, err := parseCapabilities(man.Capabilities)
	if err != nil {
		return 0, fmt.Errorf("%s boot-load: %w", name, err)
	}
	return caps, nil
}
