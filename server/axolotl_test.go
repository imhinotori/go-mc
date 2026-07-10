package server

// axolotl_test.go -- deterministic pins for the Axolotl (net.minecraft.world.entity.animal.axolotl.Axolotl,
// 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 14 / MOVEMENT_SPEED 1.0 / ATTACK_DAMAGE
// 2 / STEP_HEIGHT 1.0) and the default (lucy) variant.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/world/levelgen"
)

func axolotlLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestAxolotlSpawnDefaults: spawnAxolotl builds an axolotl rendering as entity.Axolotl.ID with the jar
// attributes, including the STEP_HEIGHT 1.0 override and the default lucy variant.
func TestAxolotlSpawnDefaults(t *testing.T) {
	loop, floorY := axolotlLoop(t)
	a := loop.spawnAxolotl(8.5, float64(floorY+1), 8.5, false)
	if a.typ != entity.Axolotl.ID {
		t.Fatalf("axolotl typ = %d, want entity.Axolotl.ID %d", a.typ, entity.Axolotl.ID)
	}
	if !a.isAxolotl {
		t.Fatal("axolotl not marked isAxolotl")
	}
	if math.Abs(float64(a.health)-14.0) > 1e-6 {
		t.Fatalf("axolotl health = %v, want 14.0 (MAX_HEALTH)", a.health)
	}
	if got := a.getAttributeValue(attribute.MaxHealth); math.Abs(got-14.0) > 1e-9 {
		t.Fatalf("axolotl MAX_HEALTH = %v, want 14.0", got)
	}
	if got := a.getAttributeValue(attribute.MovementSpeed); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("axolotl MOVEMENT_SPEED = %v, want 1.0", got)
	}
	if got := a.getAttributeValue(attribute.AttackDamage); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("axolotl ATTACK_DAMAGE = %v, want 2.0", got)
	}
	if got := a.getAttributeValue(attribute.StepHeight); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("axolotl STEP_HEIGHT = %v, want 1.0 (override of the 0.6 base)", got)
	}
	// Axolotl.finalizeSpawn draws a COMMON-variant pick (never BLUE, the rare breeding mutation) on
	// level.getRandom(); the observable invariant is variant in {LUCY,WILD,GOLD,CYAN}.
	if a.axolotlVariant == axolotlVariantBlue {
		t.Fatalf("axolotl variant = BLUE %d, but finalizeSpawn only draws COMMON variants", axolotlVariantBlue)
	}
	if a.axolotlVariant < axolotlVariantLucy || a.axolotlVariant > axolotlVariantCyan {
		t.Fatalf("axolotl variant = %d, want a common variant in [%d,%d]", a.axolotlVariant, axolotlVariantLucy, axolotlVariantCyan)
	}
	if a.ai == nil || a.ai.rng == nil {
		t.Fatal("axolotl has no minimal AI / rng")
	}
}

// TestAxolotlSpawnVariantCommonDistribution: over many spawns every axolotl draws one of the four COMMON
// variants (LUCY/WILD/GOLD/CYAN) and BLUE never appears (getCommonSpawnVariant excludes it). Confirms the
// nextInt(4) common-array pick on level.getRandom(). Cite Axolotl$Variant.getCommonSpawnVariant.
func TestAxolotlSpawnVariantCommonDistribution(t *testing.T) {
	loop, floorY := axolotlLoop(t)
	seen := map[int]int{}
	for i := 0; i < 400; i++ {
		a := loop.spawnAxolotl(8.5, float64(floorY+1), 8.5, false)
		if a.axolotlVariant == axolotlVariantBlue {
			t.Fatalf("spawn %d drew BLUE, but common spawns never draw the rare mutation", i)
		}
		seen[a.axolotlVariant]++
	}
	for _, v := range []int{axolotlVariantLucy, axolotlVariantWild, axolotlVariantGold, axolotlVariantCyan} {
		if seen[v] == 0 {
			t.Fatalf("common variant %d never drawn over 400 spawns", v)
		}
	}
}

// TestAxolotlSoloSpawnDrawOrderAndCount: a solo (non-BUCKET, spawnGroupData==null) Axolotl.finalizeSpawn
// consumes EXACTLY THREE level.getRandom() draws in order (bytecode 47-87): types[0]=getCommonSpawnVariant
// (nextInt(4)), types[1]=getCommonSpawnVariant (nextInt(4)), then getVariant == types[nextInt(2)]. The port
// previously drew only ONE nextInt(4), desyncing all downstream level-random consumers. This pins the seed,
// replays the full 3-draw path, and asserts (1) the mob's variant equals the replay result and (2) the
// stream advanced by exactly 3 draws (the region's next draw equals the replay's 4th draw).
func TestAxolotlSoloSpawnDrawOrderAndCount(t *testing.T) {
	loop, floorY := axolotlLoop(t)
	const seed = int64(0x5A0517)
	loop.cur().levelRandom.SetSeed(seed)

	a := loop.spawnAxolotl(8.5, float64(floorY+1), 8.5, false)
	// The region stream is now 3 draws in; capture the 4th draw for the count assertion.
	afterSpawnDraw := loop.cur().levelRandom.NextIntN(1000)

	// Replay the exact 3-draw solo path from the same seed.
	replay := levelgen.NewLegacyRandomSource(seed)
	commons := []int{axolotlVariantLucy, axolotlVariantWild, axolotlVariantGold, axolotlVariantCyan}
	t0 := commons[replay.NextIntN(4)] // types[0] = getCommonSpawnVariant (draw 1)
	t1 := commons[replay.NextIntN(4)] // types[1] = getCommonSpawnVariant (draw 2)
	pick := []int{t0, t1}[replay.NextIntN(2)] // getVariant == types[nextInt(2)] (draw 3)
	replayAfter := replay.NextIntN(1000)      // the 4th draw -- must match the region's post-spawn draw

	if a.axolotlVariant != pick {
		t.Fatalf("axolotl variant = %d, want %d (the 3-draw AxolotlGroupData solo path)", a.axolotlVariant, pick)
	}
	if afterSpawnDraw != int32(replayAfter) {
		t.Fatalf("post-spawn level-random draw = %d, want %d -- finalizeSpawn must consume EXACTLY 3 draws (draw count desync)", afterSpawnDraw, replayAfter)
	}
}
