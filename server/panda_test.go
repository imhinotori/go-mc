package server

// panda_test.go -- deterministic pins for the Panda (net.minecraft.world.entity.animal.panda.Panda, 1:1
// javap this session). Verifies the spawn attributes (MOVEMENT_SPEED 0.15 / ATTACK_DAMAGE 6 / MAX_HEALTH
// 20), the gene->variant mapping (getVariantFromGenes: recessive shows only when main==hidden), the
// per-variant setAttributes divergence (WEAK -> MAX_HEALTH 10, LAZY -> MOVEMENT_SPEED 0.07), and that the
// breeding gene roll (setGeneFromParents) is deterministic per child id.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func pandaLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestPandaSpawnDefaults: spawnPanda builds a panda rendering as entity.Panda.ID with the jar attributes.
// MAX_HEALTH is the living default 20 UNLESS the rolled variant is WEAK (then 10 via setAttributes); we
// assert MOVEMENT_SPEED/ATTACK_DAMAGE (fixed unless LAZY) and that health matches the folded MAX_HEALTH.
func TestPandaSpawnDefaults(t *testing.T) {
	loop, floorY := pandaLoop(t)
	p := loop.spawnPanda(8.5, float64(floorY+1), 8.5, false)
	if p.typ != entity.Panda.ID {
		t.Fatalf("panda typ = %d, want entity.Panda.ID %d", p.typ, entity.Panda.ID)
	}
	if !p.isPanda {
		t.Fatal("panda not marked isPanda")
	}
	if got := p.getAttributeValue(attribute.AttackDamage); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("panda ATTACK_DAMAGE = %v, want 6.0", got)
	}
	// health must equal the folded MAX_HEALTH (20 normally, 10 if the spawn rolled WEAK).
	wantHP := p.getAttributeValue(attribute.MaxHealth)
	if math.Abs(float64(p.health)-wantHP) > 1e-6 {
		t.Fatalf("panda health = %v, want folded MAX_HEALTH %v", p.health, wantHP)
	}
	// A non-LAZY panda keeps MOVEMENT_SPEED 0.15; a LAZY one diverges to 0.07 (both are valid outcomes).
	sp := p.getAttributeValue(attribute.MovementSpeed)
	if math.Abs(sp-0.15000000596046448) > 1e-12 && math.Abs(sp-0.07000000029802322) > 1e-12 {
		t.Fatalf("panda MOVEMENT_SPEED = %v, want 0.15 (default) or 0.07 (lazy)", sp)
	}
	if p.ai == nil || p.ai.rng == nil {
		t.Fatal("panda has no minimal AI / rng")
	}
}

// TestPandaGetVariantFromGenes pins the recessive-mask table: a dominant main always shows; a recessive
// main (BROWN/WEAK) shows ONLY when main==hidden, else the visible variant is NORMAL. Cite Panda.Gene.
func TestPandaGetVariantFromGenes(t *testing.T) {
	cases := []struct {
		main, hidden, want int
	}{
		{pandaGeneNormal, pandaGeneBrown, pandaGeneNormal},          // dominant NORMAL shows
		{pandaGeneLazy, pandaGeneWeak, pandaGeneLazy},               // dominant LAZY shows
		{pandaGeneAggressive, pandaGeneNormal, pandaGeneAggressive}, // dominant AGGRESSIVE shows
		{pandaGeneBrown, pandaGeneNormal, pandaGeneNormal},          // recessive BROWN masked (main!=hidden)
		{pandaGeneBrown, pandaGeneBrown, pandaGeneBrown},            // recessive BROWN shows (main==hidden)
		{pandaGeneWeak, pandaGeneLazy, pandaGeneNormal},             // recessive WEAK masked
		{pandaGeneWeak, pandaGeneWeak, pandaGeneWeak},               // recessive WEAK shows
	}
	for _, c := range cases {
		if got := pandaGetVariantFromGenes(c.main, c.hidden); got != c.want {
			t.Errorf("getVariantFromGenes(%d,%d) = %d, want %d", c.main, c.hidden, got, c.want)
		}
	}
}

// TestPandaSetAttributesVariant pins the per-variant live-instance divergence: a WEAK panda (main==hidden
// ==WEAK) gets MAX_HEALTH 10 and a LAZY panda gets MOVEMENT_SPEED 0.07, while a NORMAL panda keeps the
// supplier defaults (MAX_HEALTH 20, MOVEMENT_SPEED 0.15). Cite Panda.setAttributes.
func TestPandaSetAttributesVariant(t *testing.T) {
	loop, floorY := pandaLoop(t)
	// Force each variant via the gene fields, re-run setAttributes, assert the diverged bases.
	weak := loop.spawnPanda(8.5, float64(floorY+1), 8.5, false)
	weak.pandaMainGene, weak.pandaHiddenGene = pandaGeneWeak, pandaGeneWeak
	pandaSetAttributes(weak)
	if got := weak.getAttributeValue(attribute.MaxHealth); math.Abs(got-10.0) > 1e-9 {
		t.Errorf("WEAK panda MAX_HEALTH = %v, want 10.0", got)
	}
	lazy := loop.spawnPanda(9.5, float64(floorY+1), 9.5, false)
	lazy.pandaMainGene, lazy.pandaHiddenGene = pandaGeneLazy, pandaGeneNormal
	pandaSetAttributes(lazy)
	if got := lazy.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.07000000029802322) > 1e-12 {
		t.Errorf("LAZY panda MOVEMENT_SPEED = %v, want 0.07000000029802322", got)
	}
	// A NORMAL panda: setAttributes is a no-op, supplier defaults hold.
	norm := loop.spawnPanda(10.5, float64(floorY+1), 10.5, false)
	norm.pandaMainGene, norm.pandaHiddenGene = pandaGeneNormal, pandaGeneBrown
	pandaSetAttributes(norm)
	if got := norm.getAttributeValue(attribute.MaxHealth); math.Abs(got-20.0) > 1e-9 {
		t.Errorf("NORMAL panda MAX_HEALTH = %v, want 20.0 (living default)", got)
	}
	if got := norm.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.15000000596046448) > 1e-12 {
		t.Errorf("NORMAL panda MOVEMENT_SPEED = %v, want 0.15 (supplier default)", got)
	}
}

// TestPandaBreedGeneRoll pins that the breeding gene roll (setGeneFromParents) is deterministic per child
// id and produces valid genes (0..6). Two children spawned from the same parents at the same id-seed
// yield identical genes; the genes are always in-range. Cite Panda.setGeneFromParents.
func TestPandaBreedGeneRoll(t *testing.T) {
	loop, floorY := pandaLoop(t)
	a := loop.spawnPanda(8.5, float64(floorY+1), 8.5, false)
	b := loop.spawnPanda(9.5, float64(floorY+1), 9.5, false)
	a.pandaMainGene, a.pandaHiddenGene = pandaGeneLazy, pandaGeneBrown
	b.pandaMainGene, b.pandaHiddenGene = pandaGeneAggressive, pandaGeneWeak

	child := loop.spawnPandaChild(a, b, 8.5, float64(floorY+1), 8.5)
	if child.typ != entity.Panda.ID || !child.isPanda {
		t.Fatal("panda child not a panda")
	}
	if !child.isBaby() {
		t.Fatal("panda child is not a baby")
	}
	inRange := func(g int) bool { return g >= pandaGeneNormal && g <= pandaGeneMax }
	if !inRange(child.pandaMainGene) || !inRange(child.pandaHiddenGene) {
		t.Fatalf("child genes out of range: main=%d hidden=%d", child.pandaMainGene, child.pandaHiddenGene)
	}
	// Determinism: replay setGeneFromParents on a fresh stream reseeded to the child's id (exactly what
	// spawnPandaChild does: reseedMobAI(child.ai, child.id) then draw). Same id -> same genes.
	var replay Entity
	replay.id = child.id
	replay.ai = &mobAI{}
	reseedMobAI(replay.ai, replay.id)
	pandaSetGeneFromParents(&replay, a, b, mobRandom(&replay))
	if replay.pandaMainGene != child.pandaMainGene || replay.pandaHiddenGene != child.pandaHiddenGene {
		t.Fatalf("breed gene roll not deterministic: got (%d,%d) replay (%d,%d)",
			child.pandaMainGene, child.pandaHiddenGene, replay.pandaMainGene, replay.pandaHiddenGene)
	}
}

// TestPandaAiStepNoop: pandaAiStep is a bounded no-op today (the temperament state machine is deferred);
// it must not mutate the genes or health.
func TestPandaAiStepNoop(t *testing.T) {
	loop, floorY := pandaLoop(t)
	p := loop.spawnPanda(8.5, float64(floorY+1), 8.5, false)
	m, h, hp := p.pandaMainGene, p.pandaHiddenGene, p.health
	for i := 0; i < 100; i++ {
		loop.pandaAiStep(p)
	}
	if p.pandaMainGene != m || p.pandaHiddenGene != h || p.health != hp {
		t.Fatal("pandaAiStep mutated panda state (must be a bounded no-op)")
	}
}
