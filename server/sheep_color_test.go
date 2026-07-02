package server

// sheep_color_test.go — MOB-PASS-02 wool-color/shear/dye + evoker WOLOLO (1:1 jar). The pig oracle is a
// SEPARATE, untouched mob: the sheep spawn-color draw is on the LEVEL stream (never the mob stream), and
// every sheep-color path is sheep-gated (typ == entity.Sheep.ID), so the pig oracle is unperturbed.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// TestGetRandomSheepColorDeterminism pins getRandomSheepColor (SheepColorSpawnRules TEMPERATE): outer
// nextInt(100) picks a bucket (BLACK<5, GRAY<10, LIGHT_GRAY<15, BROWN<18, else commonColors), the
// commonColors bucket draws a NESTED nextInt(500) (WHITE<499 else PINK). Same seed -> same color; the
// draw COUNT is exact (1 for a single bucket, 2 for commonColors) via a lockstep mirror.
func TestGetRandomSheepColorDeterminism(t *testing.T) {
	for _, seed := range []int64{1, 42, 777, 123456789} {
		a := getRandomSheepColor(levelgen.NewLegacyRandomSource(seed))
		b := getRandomSheepColor(levelgen.NewLegacyRandomSource(seed))
		if a != b {
			t.Fatalf("seed %d: getRandomSheepColor not deterministic: %d != %d", seed, a, b)
		}
		if a > 15 {
			t.Fatalf("seed %d: color id %d out of DyeColor range 0..15", seed, a)
		}
	}

	for _, seed := range []int64{0, 1, 2, 3, 5, 8, 13, 21, 100, 500, 999, 31337} {
		mirror := levelgen.NewLegacyRandomSource(seed)
		sel := mirror.NextIntN(100)
		var wantColor byte
		switch {
		case sel < 5:
			wantColor = dyeBlack
		case sel < 10:
			wantColor = dyeGray
		case sel < 15:
			wantColor = dyeLightGray
		case sel < 18:
			wantColor = dyeBrown
		default:
			sel2 := mirror.NextIntN(500)
			if sel2 < 499 {
				wantColor = dyeWhite
			} else {
				wantColor = dyePink
			}
		}

		got := getRandomSheepColor(levelgen.NewLegacyRandomSource(seed))
		if got != wantColor {
			t.Fatalf("seed %d (sel=%d): color = %d, want %d", seed, sel, got, wantColor)
		}
		live := levelgen.NewLegacyRandomSource(seed)
		_ = getRandomSheepColor(live)
		if x, y := live.NextIntN(1<<30), mirror.NextIntN(1<<30); x != y {
			t.Fatalf("seed %d: post-call stream desynced (next %d != mirror %d)", seed, x, y)
		}
	}
}

// TestGetRandomSheepColorDistribution: over a big sweep WHITE dominates (~81.8%) and the 4 single colors
// each appear (their 5/5/5/3% weights).
func TestGetRandomSheepColorDistribution(t *testing.T) {
	lr := levelgen.NewLegacyRandomSource(0xC0FFEE)
	const n = 20000
	counts := map[byte]int{}
	for i := 0; i < n; i++ {
		counts[getRandomSheepColor(lr)]++
	}
	if counts[dyeWhite] < n*70/100 {
		t.Fatalf("WHITE appeared %d/%d, expected the dominant color", counts[dyeWhite], n)
	}
	for _, c := range []byte{dyeBlack, dyeGray, dyeLightGray, dyeBrown} {
		if counts[c] == 0 {
			t.Fatalf("color id %d never appeared over %d draws", c, n)
		}
	}
}

// TestSheepDyeSetsColor: DyeItem.interactLivingEntity Sheep branch — a dye on a live, un-sheared,
// differently-colored sheep sets the color + shrinks the dye; a same-color dye / sheared sheep / non-dye
// item is a no-op.
func TestSheepDyeSetsColor(t *testing.T) {
	loop, w := extrasLoop(t)
	sheep := spawnMobTyped(loop, entity.Sheep, 7300, 8.5, float64(w.floorY+1), 8.5)
	sheep.health = 8 // isAlive() needs health>0 (spawnMobTyped skips initSpawnHealth)
	sheep.sheepColor = dyeWhite

	p := shearPlayer(loop, sheep, int32(item.BlueDye.ID))
	if !loop.trySheepDye(p, sheep) {
		t.Fatal("trySheepDye on a WHITE un-sheared sheep with BLUE dye must return true")
	}
	if sheepGetColor(sheep) != dyeBlue {
		t.Fatalf("after dye the sheep color = %d, want BLUE %d", sheepGetColor(sheep), dyeBlue)
	}
	inv := ensureInventory(p)
	if held := inv.get(heldWindowSlot(inv.heldSlot)); !slotIsEmpty(held) {
		t.Fatalf("the single BLUE dye must shrink to empty; held=%v", held)
	}

	sheep2 := spawnMobTyped(loop, entity.Sheep, 7301, 9.5, float64(w.floorY+1), 9.5)
	sheep2.health = 8 // isAlive() needs health>0 (spawnMobTyped skips initSpawnHealth)
	sheep2.sheepColor = dyeBlue
	p2 := shearPlayer(loop, sheep2, int32(item.BlueDye.ID))
	if loop.trySheepDye(p2, sheep2) {
		t.Fatal("trySheepDye with a SAME-color dye must return false")
	}
	if inv2 := ensureInventory(p2); slotIsEmpty(inv2.get(heldWindowSlot(inv2.heldSlot))) {
		t.Fatal("a same-color dye must NOT be consumed")
	}

	sheep3 := spawnMobTyped(loop, entity.Sheep, 7302, 10.5, float64(w.floorY+1), 10.5)
	sheep3.health = 8 // isAlive() needs health>0 (spawnMobTyped skips initSpawnHealth)
	sheep3.sheepColor = dyeWhite
	sheep3.sheared = true
	p3 := shearPlayer(loop, sheep3, int32(item.RedDye.ID))
	if loop.trySheepDye(p3, sheep3) {
		t.Fatal("trySheepDye on a SHEARED sheep must return false")
	}

	sheep4 := spawnMobTyped(loop, entity.Sheep, 7303, 11.5, float64(w.floorY+1), 11.5)
	sheep4.health = 8 // isAlive() needs health>0 (spawnMobTyped skips initSpawnHealth)
	p4 := shearPlayer(loop, sheep4, int32(item.Stone.ID))
	if loop.trySheepDye(p4, sheep4) {
		t.Fatal("trySheepDye with a non-dye held item must return false")
	}
}

// TestSheepShearDropsColoredWool: a shear drops wool of the sheep ACTUAL color (per-color loot table): a
// BLACK sheep -> black_wool + setSheared(true).
func TestSheepShearDropsColoredWool(t *testing.T) {
	loop, w := extrasLoop(t)
	sheep := spawnMobTyped(loop, entity.Sheep, 7310, 8.5, float64(w.floorY+1), 8.5)
	sheep.health = 8 // isAlive() needs health>0 (spawnMobTyped skips initSpawnHealth)
	sheep.sheepColor = dyeBlack

	before := countItemsInRegion(loop, sheep)
	p := shearPlayer(loop, sheep, int32(item.Shears.ID))
	if !loop.trySheepShear(p, sheep) {
		t.Fatal("trySheepShear on a ready adult BLACK sheep must return true")
	}
	if !sheep.sheared {
		t.Fatal("a successful shear must setSheared(true)")
	}
	blackWool := 0
	for _, ent := range loop.regionForEntity(sheep).entities.byID {
		if ent.isItem && int32(ent.itemStack.ItemID) == int32(item.BlackWool.ID) {
			blackWool += int(ent.itemStack.Count)
		}
	}
	if got := countItemsInRegion(loop, sheep) - before; got < 1 {
		t.Fatalf("shear dropped %d items, want >=1 black_wool", got)
	}
	if blackWool < 1 {
		t.Fatal("a BLACK sheep sheared must drop black_wool (the per-color loot table)")
	}
}

// TestEvokerWololoRecolorsBlueToRed: Evoker EvokerWololoSpellGoal — wololoCanUse acquires a BLUE sheep in
// inflate(16,4,16) (nextInt(size) pick), performSpellCasting setColor(RED). A non-blue sheep is ignored.
func TestEvokerWololoRecolorsBlueToRed(t *testing.T) {
	loop, w := extrasLoop(t)

	ev := spawnMobTyped(loop, entity.Evoker, 7400, 8.5, float64(w.floorY+1), 8.5)
	ev.health = 24 // isAlive() needs health>0 (spawnMobTyped skips initSpawnHealth)
	ev.currentSpell = illagerSpellNone

	blue := spawnMobTyped(loop, entity.Sheep, 7401, 11.5, float64(w.floorY+1), 8.5)
	blue.health = 8 // isAlive() needs health>0 (spawnMobTyped skips initSpawnHealth)
	blue.sheepColor = dyeBlue

	g := &evokerUseSpellGoal{kind: spellKindWololo, timing: evokerSpellTiming{warmup: 40, casting: 60, interval: 140, spellID: illagerSpellWololo}}

	if !g.wololoCanUse(loop, ev) {
		t.Fatal("wololoCanUse must acquire a BLUE sheep in range")
	}
	if ev.evokerWololoTarget != blue.id {
		t.Fatalf("wololoTarget = %d, want the BLUE sheep %d", ev.evokerWololoTarget, blue.id)
	}

	loop.evokerWololoRecolor(ev)
	if sheepGetColor(blue) != dyeRed {
		t.Fatalf("after WOLOLO the sheep color = %d, want RED %d", sheepGetColor(blue), dyeRed)
	}

	ev2 := spawnMobTyped(loop, entity.Evoker, 7410, 40.5, float64(w.floorY+1), 40.5)
	ev2.health = 24 // isAlive() needs health>0 (spawnMobTyped skips initSpawnHealth)
	ev2.currentSpell = illagerSpellNone
	whiteSheep := spawnMobTyped(loop, entity.Sheep, 7411, 42.5, float64(w.floorY+1), 40.5)
	whiteSheep.health = 8 // isAlive() needs health>0 (spawnMobTyped skips initSpawnHealth)
	whiteSheep.sheepColor = dyeWhite
	g2 := &evokerUseSpellGoal{kind: spellKindWololo, timing: evokerSpellTiming{warmup: 40, casting: 60, interval: 140, spellID: illagerSpellWololo}}
	if g2.wololoCanUse(loop, ev2) {
		t.Fatal("wololoCanUse must NOT acquire a non-BLUE (WHITE) sheep")
	}
}
