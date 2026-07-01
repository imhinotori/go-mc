package server

// poi_test.go — the POI subsystem (poi.go): the block-state -> PoiType map, the occupancy/ticket system,
// the getInRange/findClosest queries, the village-center machinery, and the bad-omen -> raid auto-trigger
// (BadOmenMobEffect -> RaidOmenMobEffect -> createOrExtendRaid). All values are pinned to the 26.2 jar
// (PoiTypes.bootstrap: HOME=(beds,1,1), MEETING=(bell,32,6); Raids.createOrExtendRaid radius 64 IS_OCCUPIED;
// MAX_VILLAGE_DISTANCE=6). A raid draws ZERO from any pig stream, so the pig oracle is untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestPoiTypeForState pins PoiTypes.forState: a bed -> HOME, a bell -> MEETING, else no POI, with the
// literal maxTickets/validRange from bootstrap.
func TestPoiTypeForState(t *testing.T) {
	bed := block.DefaultStateID["minecraft:red_bed"]
	bell := block.DefaultStateID["minecraft:bell"]
	stone := block.ToStateID[block.Stone{}]

	if got := poiTypeForState(bed); got != poiTypeHome {
		t.Fatalf("forState(red_bed) = %v, want HOME", got)
	}
	if got := poiTypeForState(bell); got != poiTypeMeeting {
		t.Fatalf("forState(bell) = %v, want MEETING", got)
	}
	if got := poiTypeForState(stone); got != nil {
		t.Fatalf("forState(stone) = %v, want nil", got)
	}
	if poiTypeHome.maxTickets != 1 || poiTypeHome.validRange != 1 {
		t.Fatalf("HOME = (%d,%d), want (1,1)", poiTypeHome.maxTickets, poiTypeHome.validRange)
	}
	if poiTypeMeeting.maxTickets != 32 || poiTypeMeeting.validRange != 6 {
		t.Fatalf("MEETING = (%d,%d), want (32,6)", poiTypeMeeting.maxTickets, poiTypeMeeting.validRange)
	}
	if !poiTypeVillage(poiTypeHome) || !poiTypeVillage(poiTypeMeeting) {
		t.Fatal("HOME and MEETING must both be #village members")
	}
}

// TestPoiOccupancyTickets pins PoiRecord ticket math: a fresh HOME record has freeTickets == maxTickets(1),
// hasSpace() true, isOccupied() FALSE; acquireTicket -> freeTickets 0, hasSpace false, isOccupied TRUE;
// releaseTicket restores it. (A bed is only "occupied" once a villager claims it.)
func TestPoiOccupancyTickets(t *testing.T) {
	rec := newPoiRecord(pk.Position{X: 1, Y: 64, Z: 1}, poiTypeHome)
	if rec.freeTickets != 1 || !rec.hasSpace() || rec.isOccupied() {
		t.Fatalf("fresh HOME: free=%d hasSpace=%v isOccupied=%v, want free=1 hasSpace=true isOccupied=false",
			rec.freeTickets, rec.hasSpace(), rec.isOccupied())
	}
	if !rec.acquireTicket() {
		t.Fatal("acquireTicket on a fresh HOME must succeed")
	}
	if rec.freeTickets != 0 || rec.hasSpace() || !rec.isOccupied() {
		t.Fatalf("claimed HOME: free=%d hasSpace=%v isOccupied=%v, want free=0 hasSpace=false isOccupied=true",
			rec.freeTickets, rec.hasSpace(), rec.isOccupied())
	}
	if rec.acquireTicket() {
		t.Fatal("second acquireTicket must fail (no free tickets)")
	}
	if !rec.releaseTicket() || rec.freeTickets != 1 || rec.isOccupied() {
		t.Fatalf("released HOME: free=%d isOccupied=%v, want free=1 isOccupied=false", rec.freeTickets, rec.isOccupied())
	}
	if rec.releaseTicket() {
		t.Fatal("release above maxTickets must fail")
	}
}

// TestPoiGetInRangeFindsBeds registers beds via the block-state-change hook and asserts getInRange (ANY)
// finds them, the square/range filters bound correctly, and findClosest picks the nearest.
func TestPoiGetInRangeFindsBeds(t *testing.T) {
	loop, _ := newPhysicsLoop()
	bed := block.DefaultStateID["minecraft:red_bed"]
	air := block.ToStateID[block.Air{}]

	loop.withRegion(loop.only(), func() {
		center := pk.Position{X: 0, Y: 64, Z: 0}
		near := pk.Position{X: 3, Y: 64, Z: 0}
		far := pk.Position{X: 40, Y: 64, Z: 0} // > radius 32, inside a different section

		// Register via the setBlockState POI hook (air -> bed).
		loop.updatePoiOnBlockStateChange(near, air, bed)
		loop.updatePoiOnBlockStateChange(far, air, bed)

		pm := loop.only().poiManager
		if pm == nil {
			t.Fatal("poiManager should exist after registering a bed")
		}

		got := pm.getInRange(poiTypeVillage, center, 32, poiOccupancyAny)
		if len(got) != 1 || got[0].pos != near {
			t.Fatalf("getInRange r=32 = %v, want exactly the near bed %v", posesOf(got), near)
		}

		all := pm.getInRange(poiTypeVillage, center, 64, poiOccupancyAny)
		if len(all) != 2 {
			t.Fatalf("getInRange r=64 found %d beds, want 2", len(all))
		}

		// findClosest picks the near bed.
		closest, ok := pm.findClosest(poiTypeVillage, center, 64, poiOccupancyAny)
		if !ok || closest != near {
			t.Fatalf("findClosest = (%v,%v), want %v", closest, ok, near)
		}

		// IS_OCCUPIED finds none yet (fresh beds are unclaimed).
		if occ := pm.getInRange(poiTypeVillage, center, 64, poiOccupancyIsOccupied); len(occ) != 0 {
			t.Fatalf("IS_OCCUPIED found %d fresh beds, want 0", len(occ))
		}

		// Breaking the near bed (bed -> air) deregisters it.
		loop.updatePoiOnBlockStateChange(near, bed, air)
		if after := pm.getInRange(poiTypeVillage, center, 64, poiOccupancyAny); len(after) != 1 {
			t.Fatalf("after break, getInRange = %d, want 1 (the far bed)", len(after))
		}
	})
}

// TestPoiIsVillage pins ServerLevel.isVillage: with only UNCLAIMED beds a section is NOT a village (no
// IS_OCCUPIED #village POI); claiming a bed (villager acquire) makes its section a village center, and
// isVillage is true within MAX section distance 1 and false beyond it.
func TestPoiIsVillage(t *testing.T) {
	loop, _ := newPhysicsLoop()
	bed := block.DefaultStateID["minecraft:red_bed"]
	air := block.ToStateID[block.Air{}]

	loop.withRegion(loop.only(), func() {
		bedPos := pk.Position{X: 8, Y: 64, Z: 8} // section (0,4,0)
		loop.updatePoiOnBlockStateChange(bedPos, air, bed)
		pm := loop.only().poiManager

		// Unclaimed bed -> not a village anywhere.
		if pm.isVillage(bedPos) {
			t.Fatal("an unclaimed bed must NOT form a village (IS_OCCUPIED gate)")
		}

		// Simulate a villager claiming the bed (acquireTicket -> occupied).
		rec := pm.recordAt(bedPos)
		if rec == nil || !rec.acquireTicket() {
			t.Fatalf("failed to occupy the bed record: %v", rec)
		}
		// Cache was populated during the isVillage call above; invalidate as a real claim path would.
		pm.villageDist = map[int64]int{}

		if !pm.isVillage(bedPos) {
			t.Fatal("a claimed bed's own section must be a village")
		}
		// One section away (16 blocks) is within MAX_VILLAGE_DISTANCE section-distance 1 -> still village.
		if !pm.isVillage(pk.Position{X: 8 + 16, Y: 64, Z: 8}) {
			t.Fatal("adjacent section must be within village distance 1")
		}
		// Three sections away is beyond distance 1 -> not a village.
		if pm.isVillage(pk.Position{X: 8 + 48, Y: 64, Z: 8}) {
			t.Fatal("a section 3 away must NOT be a village")
		}
	})
}

// TestBadOmenStartsRaidInVillage is the end-to-end auto-trigger: a player carrying BAD_OMEN standing in a
// bed-village (an occupied HOME POI) converts BAD_OMEN -> RAID_OMEN, and when RAID_OMEN expires
// createOrExtendRaid fires and a raid appears at the village center. VERIFIED chain BadOmenMobEffect ->
// RaidOmenMobEffect -> Raids.createOrExtendRaid.
func TestBadOmenStartsRaidInVillage(t *testing.T) {
	loop, _ := newPhysicsLoop()
	bed := block.DefaultStateID["minecraft:red_bed"]
	air := block.ToStateID[block.Air{}]

	loop.withRegion(loop.only(), func() {
		// Build an occupied bed-village at (5,64,5) — a villager-claim simulation makes it IS_OCCUPIED.
		bedPos := pk.Position{X: 5, Y: 64, Z: 5}
		loop.updatePoiOnBlockStateChange(bedPos, air, bed)
		pm := loop.only().poiManager
		rec := pm.recordAt(bedPos)
		if rec == nil || !rec.acquireTicket() {
			t.Fatal("failed to occupy the village bed")
		}
		pm.villageDist = map[int64]int{}

		// A player standing on the bed, carrying BAD_OMEN (amplifier 0 -> raidOmenLevel +1).
		p := &tickPlayer{x: 5.5, y: 64, z: 5.5, health: 20}
		loop.addPlayerEffect(p, 0, effectBadOmen, 100, 0, 1.0)

		if !loop.only().poiManager.isVillage(playerBlockPos(p)) {
			t.Fatal("precondition: the player must be standing in a village")
		}

		// Tick effects: BAD_OMEN (shouldApply every tick) converts to RAID_OMEN on the first tick.
		loop.tickPlayerEffects(p)
		if playerHasEffect(p, effectBadOmen) {
			t.Fatal("BAD_OMEN should have been consumed (converted to RAID_OMEN)")
		}
		if !playerHasEffect(p, effectRaidOmen) {
			t.Fatal("RAID_OMEN should have been applied")
		}
		if p.raidOmenPosition == nil || *p.raidOmenPosition != playerBlockPos(p) {
			t.Fatalf("raidOmenPosition = %v, want the player's block pos", p.raidOmenPosition)
		}

		// No raid yet — RAID_OMEN only fires createOrExtendRaid on its FINAL tick (remaining == 1).
		if rm := loop.only().raidsManager; rm != nil && rm.raidCount() != 0 {
			t.Fatalf("no raid should exist before RAID_OMEN expires, got %d", rm.raidCount())
		}

		// Drive RAID_OMEN down to its last tick (duration was 600; tick it to remaining==1).
		e := p.activeEffects[effectRaidOmen]
		e.duration = 1
		loop.tickPlayerEffects(p)

		if playerHasEffect(p, effectRaidOmen) {
			t.Fatal("RAID_OMEN should have been consumed on its final tick")
		}
		rm := loop.only().raidsManager
		if rm == nil || rm.raidCount() != 1 {
			t.Fatalf("a raid should have been created by createOrExtendRaid, got %v", rm)
		}
		// The raid absorbed one omen level (amplifier 0 -> +1).
		var raid *Raid
		for _, r := range rm.raidMap {
			raid = r
		}
		if raid.getRaidOmenLevel() != 1 {
			t.Fatalf("raid omen level = %d, want 1 (absorbRaidOmen amp+1)", raid.getRaidOmenLevel())
		}
		// The center is the occupied bed (the only occupied #village POI in range).
		if raid.centerX != bedPos.X || raid.centerY != bedPos.Y || raid.centerZ != bedPos.Z {
			t.Fatalf("raid center = (%d,%d,%d), want the bed %v", raid.centerX, raid.centerY, raid.centerZ, bedPos)
		}
	})
}

func posesOf(recs []*poiRecord) []pk.Position {
	out := make([]pk.Position, len(recs))
	for i, r := range recs {
		out[i] = r.pos
	}
	return out
}
