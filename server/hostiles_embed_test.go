package server

// hostiles_embed_test.go — THE GATE (Plan 35-06): the integration gate that proves the 3 Phase-35
// hostiles (zombie/skeleton/spider) actually BOOT-LOAD into the shipped binary's ONE registry through
// the real //go:embed path (loadVanillaMobRegistry — NOT a temp-dir harness), and that the embedded
// copies (server/assets/vanilla_<hostile>/) are byte-identical to the canonical repo-root copies
// (plugins/vanilla_<hostile>/). The per-mob behavior (hunt + melee) is owned by zombie/skeleton/
// spider_test.go; here the assertions are: (1) the embed-vs-root byte-identity gate for all 3
// (T-35-12 — a swapped-on-disk plugins/ copy cannot widen caps because the EMBEDDED manifest governs),
// and (2) the LOUD boot-load path returns one registry holding all 3 hostile declarations with their
// goal/target sets + the MONSTER category. The pig oracle (TestPluginPigEqualsGoNativePig) is untouched
// — the hostiles are separate mobs added additively to vanillaMobNames.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// hostileEmbedSpecs pins, per hostile, the wire entity type + the jar goalSelector/targetSelector goal
// counts (the per-mob _test.go files own the priority indices; this gate only asserts the SETS landed
// through the real boot-load). zombie/skeleton: 4 goalSelector + 2 targetSelector; spider: 6 + 2.
var hostileEmbedSpecs = []struct {
	name        string
	mobName     string
	typ         entity.ID
	goalCount   int
	targetCount int
}{
	{"zombie", vanillaZombieMobName, entity.Zombie.ID, 4, 2},
	{"skeleton", vanillaSkeletonMobName, entity.Skeleton.ID, 4, 2},
	{"spider", vanillaSpiderMobName, entity.Spider.ID, 6, 2},
}

// embedByteIdenticalMobs is the FULL set of //go:embed-shipped vanilla mobs whose embedded copy
// (server/assets/vanilla_<mob>/) must stay byte-identical to the canonical repo-root copy
// (plugins/vanilla_<mob>/) — the T-35-12 / T-36-09 anti-drift gate. It spans the 3 Phase-35 hostiles
// PLUS the Phase-36 wolf (the LAST v5 mob), so a swapped-on-disk plugins/ copy of ANY of them cannot
// widen the shipped caps (the embedded manifest governs). The wolf is a CREATURE (not in
// hostileEmbedSpecs' MONSTER boot-load loop), so it lives in this dedicated byte-identity list.
var embedByteIdenticalMobs = []string{
	vanillaZombieMobName,
	vanillaSkeletonMobName,
	vanillaSpiderMobName,
	vanillaWolfMobName, // Phase 36 — the neutral/tameable wolf (T-36-09: the embed-vs-root anti-drift gate)
}

// TestHostileEmbedByteIdentical: for EACH //go:embed-shipped mob (the 3 hostiles + the Phase-36 wolf),
// the repo-root canonical copy (plugins/vanilla_<mob>/{plugin.toml,main.star}) and the embedded copy
// (server/assets/vanilla_<mob>/{...}) are byte-identical. T-35-12 / T-36-09: the embedded manifest is the
// source of truth for the SWAP's caps; a divergent on-disk plugins/ copy must NOT be able to widen them,
// which only holds if the pair is byte-identical (so the operator-visible copy == the shipped copy).
// Tests run with cwd=server/, so the repo-root copy sits at ../plugins/ and the embed copy at assets/.
func TestHostileEmbedByteIdentical(t *testing.T) {
	for _, mobName := range embedByteIdenticalMobs {
		for _, file := range []string{"plugin.toml", "main.star"} {
			rootPath := filepath.Join("..", "plugins", mobName, file)
			embedPath := filepath.Join("assets", mobName, file)

			rootData, err := os.ReadFile(rootPath)
			if err != nil {
				t.Fatalf("read repo-root %s: %v", rootPath, err)
			}
			embedData, err := os.ReadFile(embedPath)
			if err != nil {
				t.Fatalf("read embed %s: %v", embedPath, err)
			}
			if !bytes.Equal(rootData, embedData) {
				t.Fatalf("%s/%s: repo-root (%d bytes) and embed (%d bytes) copies DIVERGE — the embedded manifest governs the swap's caps (T-35-12/T-36-09); they MUST be byte-identical",
					mobName, file, len(rootData), len(embedData))
			}
		}
	}
}

// TestHostilesBootLoad: the REAL boot-load path (loadVanillaMobRegistry, reading the //go:embed FS —
// NOT a temp-dir test harness) returns ONE registry holding all 3 hostile declarations (the LOUD
// re-assertion path). Each hostile, spawned through that boot-loaded registry, renders as its wire type
// with a non-nil AI carrying the jar goalSelector + targetSelector goal counts and the MONSTER category.
// This proves vanillaMobNames + the embed directive actually wire the 3 hostiles into the shipped binary.
func TestHostilesBootLoad(t *testing.T) {
	r, err := loadVanillaMobRegistry()
	if err != nil {
		t.Fatalf("loadVanillaMobRegistry (the real //go:embed boot-load): %v", err)
	}

	// The LOUD path: all 3 hostile names must be present in the ONE registry (alongside the 4 passives).
	for _, spec := range hostileEmbedSpecs {
		if _, ok := r.byName[spec.mobName]; !ok {
			t.Fatalf("boot-loaded registry has no %q declaration — the embed directive + vanillaMobNames did not wire it in", spec.mobName)
		}
	}
	// All 4 passives still boot-load too (the hostiles are additive, not a replacement).
	for _, name := range []string{vanillaPigMobName, vanillaCowMobName, vanillaSheepMobName, vanillaChickenMobName} {
		if _, ok := r.byName[name]; !ok {
			t.Fatalf("boot-loaded registry lost the passive %q — adding the hostiles must be additive", name)
		}
	}
	// The Phase-36 wolf (the LAST v5 mob) boot-loads from the SAME real //go:embed registry alongside the
	// hostiles + passives. The wolf is a CREATURE (its goal/target shape is asserted by TestWolfBootLoads);
	// here we only pin that the embed directive + vanillaMobNames wired it into the shipped binary.
	if _, ok := r.byName[vanillaWolfMobName]; !ok {
		t.Fatalf("boot-loaded registry has no %q declaration — the embed directive + vanillaMobNames did not wire the wolf in", vanillaWolfMobName)
	}

	// Spawn each hostile through the boot-loaded registry and assert its wire type + goal/target sets +
	// the MONSTER category (the per-mob priority indices are owned by zombie/skeleton/spider_test.go).
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(r)
	loop.start(loop.clock.Now())

	for _, spec := range hostileEmbedSpecs {
		decl := r.byName[spec.mobName]
		m := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
		if m == nil {
			t.Fatalf("%s: spawnDeclaredMob through the boot-loaded registry returned nil", spec.name)
		}
		if m.typ != spec.typ {
			t.Fatalf("%s typ = %d, want %d (custom = behavior, not a new wire type)", spec.name, m.typ, spec.typ)
		}
		if m.ai == nil {
			t.Fatalf("%s has no AI after boot-load spawn", spec.name)
		}
		if got := len(m.ai.goals.goals); got != spec.goalCount {
			t.Fatalf("%s has %d goalSelector goals, want %d", spec.name, got, spec.goalCount)
		}
		if got := len(m.ai.targetSelector.goals); got != spec.targetCount {
			t.Fatalf("%s has %d targetSelector goals, want %d", spec.name, got, spec.targetCount)
		}
		if cat := categoryOf(spec.typ); cat != categoryMonster {
			t.Fatalf("categoryOf(%s) = %v, want categoryMonster (vanilla EntityType is MONSTER)", spec.name, cat)
		}
	}
}
