package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// swamp_spawn_test.go — STRUCT-POLISH-02 Task 2: the swamp-hut witch+cat spawn records.
//
// SwampHutPiece.postProcess tail (jar): !spawnedWitch -> WITCH at getWorldPos(2,2,5)+0.5; then
// spawnCat: !spawnedCat -> CAT at getWorldPos(2,2,5)+0.5. Both one-shot (the guard flips true so
// a second PostProcess records nothing — reload-safe via the persisted SpawnedWitch/SpawnedCat
// slots, 20-03). They are SpawnRequests (live mobs), NOT blocks.

// TestSwampHutSpawns: PostProcess records exactly one witch + one cat SpawnRequest, at the
// jar-exact world position (getWorldPos(2,2,5) block-center, +0.5 on X/Z).
func TestSwampHutSpawns(t *testing.T) {
	view, _, piece := placeWholeSwampHut(t)

	var witches, cats int
	var witchReq, catReq SpawnRequest
	for _, r := range view.spawns {
		switch r.EntityType {
		case "minecraft:witch":
			witches++
			witchReq = r
		case "minecraft:cat":
			cats++
			catReq = r
		default:
			t.Fatalf("unexpected spawn request %q", r.EntityType)
		}
	}
	if witches != 1 {
		t.Fatalf("witch spawn requests = %d, want exactly 1", witches)
	}
	if cats != 1 {
		t.Fatalf("cat spawn requests = %d, want exactly 1", cats)
	}

	// Both spawn at getWorldPos(2,2,5): block-center +0.5 on X/Z, Y at the block (snapTo args).
	wx := piece.getWorldX(2, 5)
	wy := piece.getWorldY(2)
	wz := piece.getWorldZ(2, 5)
	wantX, wantY, wantZ := float64(wx)+0.5, float64(wy), float64(wz)+0.5
	for _, c := range []struct {
		name string
		req  SpawnRequest
	}{{"witch", witchReq}, {"cat", catReq}} {
		if c.req.X != wantX || c.req.Y != wantY || c.req.Z != wantZ {
			t.Fatalf("%s spawn pos = (%v,%v,%v), want (%v,%v,%v) from getWorldPos(2,2,5)+0.5",
				c.name, c.req.X, c.req.Y, c.req.Z, wantX, wantY, wantZ)
		}
		if !c.req.PersistenceRequired {
			t.Fatalf("%s spawn not PersistenceRequired (setPersistenceRequired)", c.name)
		}
	}
}

// TestSwampHutSpawnOneShot: a second PostProcess over the SAME piece records NO additional
// spawns — the spawnedWitch/spawnedCat one-shot guards hold (reload-safe, Pitfall 6). This is
// the in-memory half; the persisted-guard half is covered by the piece-NBT round-trip test.
func TestSwampHutSpawnOneShot(t *testing.T) {
	view, _, piece := placeWholeSwampHut(t)
	firstWitch, firstCat := countSpawns(view.spawns)
	if firstWitch != 1 || firstCat != 1 {
		t.Fatalf("first pass: witch=%d cat=%d, want 1/1", firstWitch, firstCat)
	}

	// Second PostProcess over the same (already-spawned) piece: the guards are set, so it records
	// no new witch/cat. Reuse the same view so we can assert the TOTAL is still 1 each.
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(swampSeed, swampChunkX, swampChunkZ)
	piece.PostProcess(view, fullBox(), level.ChunkPos{swampChunkX, swampChunkZ}, rng)

	w, c := countSpawns(view.spawns)
	if w != 1 || c != 1 {
		t.Fatalf("after second PostProcess: witch=%d cat=%d, want still 1/1 (one-shot guard)", w, c)
	}
}

// countSpawns tallies witch/cat spawn requests in a buffer.
func countSpawns(reqs []SpawnRequest) (witches, cats int) {
	for _, r := range reqs {
		switch r.EntityType {
		case "minecraft:witch":
			witches++
		case "minecraft:cat":
			cats++
		}
	}
	return
}
