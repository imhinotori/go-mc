package server

// silverfish_infest_test.go — MOB-HOST-05 (infest goals): the behavior test for the two Silverfish
// stone-infestation goals (ai_goals_silverfish.go + silverfish_infest.go), the 1:1 ports of
// Silverfish$SilverfishMergeWithStoneGoal + Silverfish$SilverfishWakeUpFriendsGoal + InfestedBlock:
//
//   - TestSilverfishMergeConvertsStone: the merge goal converts an adjacent HOST block (stone) into its
//     INFESTED variant (infested_stone) and discards the silverfish — driven on a success RNG seed.
//   - TestSilverfishWakeSummonsFriends: a hurt silverfish (silverfishNotifyHurt) arms the wake goal, and
//     its spiral tick destroys a nearby InfestedBlock and SUMMONS a new silverfish (mobGriefing branch).
//
// Both drive the Go-native goals directly with a controlled per-entity rng (e.ai.rng = newEntityRandom),
// the same determinism pattern ai_goals_target_test.go uses. The pig oracle is a separate mob — untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// silverfishInfestLoop boots a physics loop with a floor + the vanilla_silverfish registry loaded, and
// spawns one silverfish at the given feet position. Mirrors silverfishLoop / endermanLoop.
func silverfishInfestLoop(t *testing.T, x, y, z float64) (*TickLoop, *Entity) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaSilverfishRegistry(t))
	loop.start(loop.clock.(*fakeClock).Now())
	decl := loop.mobRegistry.byName["vanilla_silverfish"]
	sf := loop.spawnDeclaredMob(decl, x, y, z)
	if sf.typ != entity.Silverfish.ID {
		t.Fatalf("silverfish typ = %d, want entity.Silverfish.ID %d", sf.typ, entity.Silverfish.ID)
	}
	return loop, sf
}

// TestSilverfishMergeConvertsStone: with stone (a compatible host) on every side, the merge goal's
// canUse eventually sets doMerge=true (on a seed where nextInt(reducedTickDelay(10))==0), and start()
// converts the selected adjacent block into infested_stone AND discards the silverfish.
func TestSilverfishMergeConvertsStone(t *testing.T) {
	const floorY = 63
	loop, sf := silverfishInfestLoop(t, 8.5, float64(floorY+1), 8.5)

	// Wrap the silverfish body position in stone on all six faces so ANY Direction the goal draws lands
	// on a host block — this makes the merge condition depend ONLY on the nextInt(5)==0 gate, not on
	// which face is picked. Base = BlockPos.containing(x, y+0.5, z).
	bx, by, bz := 8, floorY+1, 8
	loop.withRegion(loop.only(), func() {
		for _, d := range [][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}} {
			loop.world().SetBlock(pk.Position{X: bx + d[0], Y: by + d[1], Z: bz + d[2]}, block.ToStateID[block.Stone{}], dimMinY)
		}
	})

	converted := false
	loop.withRegion(loop.only(), func() {
		// Probe seeds until canUse selects a merge (doMerge=true). A fresh goal per seed keeps the state
		// clean; the seed drives the mob's rng so the nextInt(5)==0 gate + the Direction pick are
		// deterministic. 256 seeds is ample (the gate hits ~1/5).
		for seed := uint64(1); seed <= 256 && !converted; seed++ {
			sf.ai.rng = newEntityRandom(seed)
			g := newSilverfishMergeStoneGoal(declaredWalkSpeed(loop.mobRegistry.byName["vanilla_silverfish"]))
			if !g.canUse(loop, sf) || !g.doMerge {
				continue
			}
			// The selected host must currently be stone; start() converts it to infested_stone + discards.
			before, _ := loop.world().GetBlock(g.mergePos, dimMinY)
			if !isCompatibleHostBlock(before) {
				t.Fatalf("merge selected a non-host block %d at %v", before, g.mergePos)
			}
			g.start(loop, sf)
			after, _ := loop.world().GetBlock(g.mergePos, dimMinY)
			want, _ := infestedStateByHost(before)
			if after != want {
				t.Fatalf("merge did NOT convert host->infested: pos %v got %d, want %d", g.mergePos, after, want)
			}
			if !sf.dead {
				t.Fatal("merge converted the block but did NOT discard the silverfish (mob.discard())")
			}
			converted = true
		}
	})
	if !converted {
		t.Fatal("the merge goal never selected a merge over 256 seeds despite stone on every side")
	}
}

// TestSilverfishWakeSummonsFriends: an infested_stone block sits within the wake spiral. A hurt (entity
// source) arms the wake goal via silverfishNotifyHurt; ticking the goal past its lookForFriends delay
// runs the spiral, which (mobGriefing branch) DESTROYS the infested block and SUMMONS a new silverfish.
func TestSilverfishWakeSummonsFriends(t *testing.T) {
	const floorY = 63
	loop, sf := silverfishInfestLoop(t, 8.5, float64(floorY+1), 8.5)

	// Place an infested_stone block 2 east of the silverfish (well within the ±10 xz / ±5 y spiral),
	// standing free above the floor so it is the only InfestedBlock the spiral can find.
	infPos := pk.Position{X: 10, Y: floorY + 1, Z: 8}
	loop.withRegion(loop.only(), func() {
		loop.world().SetBlock(infPos, block.ToStateID[block.InfestedStone{}], dimMinY)
	})
	if got, _ := func() (block.StateID, bool) {
		var s block.StateID
		var ok bool
		loop.withRegion(loop.only(), func() { s, ok = loop.world().GetBlock(infPos, dimMinY) })
		return s, ok
	}(); !isInfestedBlock(got) {
		t.Fatalf("setup: block at %v is not infested (got %d)", infPos, got)
	}

	before := loop.cur().entities.len()

	summoned := false
	loop.withRegion(loop.only(), func() {
		// nextBoolean() gates "stop after the first wake" — seed so the FIRST wake happens (the block is
		// destroyed + a friend summoned) regardless of the subsequent stop roll.
		sf.ai.rng = newEntityRandom(12345)

		// Arm the wake goal exactly as Silverfish.hurtServer does: an entity-source hit -> notifyHurt.
		loop.silverfishNotifyHurt(sf, damageSourceMobAttack(999))
		if sf.ai.silverfishLookForFriends == 0 {
			t.Fatal("silverfishNotifyHurt did NOT arm lookForFriends on an entity-source hit")
		}

		g := newSilverfishWakeFriendsGoal()
		if !g.canUse(loop, sf) {
			t.Fatal("the wake goal canUse is false immediately after notifyHurt (lookForFriends>0 expected)")
		}
		// Tick until the spiral fires (lookForFriends counts down to <=0). adjustedTickDelay(20)=20, so
		// ~21 ticks; give margin.
		for i := 0; i < 40; i++ {
			g.tick(loop, sf)
			after, _ := loop.world().GetBlock(infPos, dimMinY)
			if !isInfestedBlock(after) {
				summoned = true
				break
			}
		}
	})
	if !summoned {
		t.Fatal("the wake goal never de-infested the nearby InfestedBlock over its lookForFriends countdown")
	}
	// mobGriefing branch: destroyBlock -> spawnInfestation summons a NEW silverfish, so the live entity
	// count grows past the single spawned silverfish.
	after := loop.cur().entities.len()
	if after <= before {
		t.Fatalf("the wake goal destroyed the block but summoned NO silverfish (entity count %d <= %d)", after, before)
	}
}
