package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// stronghold_spawner_test.go — STRUCT-POLISH-02 Task 2: the stronghold silverfish SPAWNER.
//
// The PortalRoom places a SPAWNER BLOCK set to silverfish (a mob_spawner block-entity, NOT a
// live entity — Pitfall 5): no SpawnRequest reaches the store. The hasPlacedSpawner one-shot
// guard prevents a re-pass / reload double-placement (Pitfall 6).

// findPortalRoom returns the StrongholdPortalRoom in a graph (there is exactly one).
func findPortalRoom(t *testing.T, pieces []Piece) *StrongholdPortalRoom {
	t.Helper()
	for _, p := range pieces {
		if pr, ok := p.(*StrongholdPortalRoom); ok {
			return pr
		}
	}
	t.Fatal("no StrongholdPortalRoom in the graph")
	return nil
}

// TestStrongholdSpawner: the PortalRoom places a mob_spawner BE set to silverfish (a BLOCK), at
// getWorldPos(5,3,6), and records NO SpawnRequest (the silverfish is not a live entity).
func TestStrongholdSpawner(t *testing.T) {
	pieces := strongholdPieceGraph(7, 8, 8, 40)
	view := placeGraph(t, pieces, 7, 8, 8)
	pr := findPortalRoom(t, pieces)

	// The SPAWNER block landed at local (5,3,6) -> world pos.
	wx := pr.getWorldX(5, 6)
	wy := pr.getWorldY(3)
	wz := pr.getWorldZ(5, 6)
	if got := view.GetBlock(wx, wy, wz); got != stateOf(block.Spawner{}) {
		t.Fatalf("block at spawner pos (%d,%d,%d) = %v, want minecraft:spawner", wx, wy, wz, got)
	}

	// The mob_spawner BE is set to silverfish (SetSpawner captured by the mapView).
	if view.spawners == nil {
		t.Fatal("no SetSpawner recorded — the silverfish spawner BE was not placed")
	}
	id, ok := view.spawners[[3]int{wx, wy, wz}]
	if !ok {
		t.Fatalf("no spawner BE at (%d,%d,%d)", wx, wy, wz)
	}
	if id != "minecraft:silverfish" {
		t.Fatalf("spawner entity id = %q, want minecraft:silverfish", id)
	}

	// The silverfish is a BLOCK, not a live entity: NO SpawnRequest was recorded.
	if len(view.spawns) != 0 {
		t.Fatalf("PortalRoom recorded %d SpawnRequests, want 0 (silverfish is a spawner BLOCK)", len(view.spawns))
	}
}

// TestStrongholdSpawnerIdempotent: a FRESH-gen PortalRoom places the spawner identically on
// every PostProcess pass (the in-gen guard is NOT set — placement is idempotent via box.isInside,
// so per-chunk re-runs stay deterministic, Pitfall 2). Two passes yield the same single BE.
func TestStrongholdSpawnerIdempotent(t *testing.T) {
	pieces := strongholdPieceGraph(7, 8, 8, 40)
	pr := findPortalRoom(t, pieces)
	wx, wy, wz := pr.getWorldX(5, 6), pr.getWorldY(3), pr.getWorldZ(5, 6)

	run := func() *mapView {
		v := newMapView()
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(7, 8, 8)
		pr.PostProcess(v, fullBox(), level.ChunkPos{0, 0}, rng)
		return v
	}
	v1, v2 := run(), run()
	if v1.spawners[[3]int{wx, wy, wz}] != "minecraft:silverfish" {
		t.Fatal("first pass did not place the silverfish spawner BE")
	}
	if v2.spawners[[3]int{wx, wy, wz}] != "minecraft:silverfish" {
		t.Fatal("second pass did not place the spawner BE — the in-gen guard wrongly short-circuited")
	}
	if pr.hasPlacedSpawner {
		t.Fatal("in-gen PostProcess set hasPlacedSpawner — would break per-chunk re-run determinism")
	}
}

// TestStrongholdSpawnerReloadGuard: a RELOADED PortalRoom whose hasPlacedSpawner guard is set
// (restored from NBT) skips the spawner placement — the reload-no-double-spawn property
// (Pitfall 6). Belt-and-suspenders alongside the region-hit bypass (a saved chunk never
// re-runs PostProcess at all).
func TestStrongholdSpawnerReloadGuard(t *testing.T) {
	pieces := strongholdPieceGraph(7, 8, 8, 40)
	pr := findPortalRoom(t, pieces)
	pr.hasPlacedSpawner = true // simulate a reloaded piece whose guard was restored from NBT

	v := newMapView()
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(7, 8, 8)
	pr.PostProcess(v, fullBox(), level.ChunkPos{0, 0}, rng)

	if len(v.spawners) != 0 {
		t.Fatalf("reloaded (guard=true) portal room placed %d spawner BEs, want 0 (reload skip)", len(v.spawners))
	}
}
