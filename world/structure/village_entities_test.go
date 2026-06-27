package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// village_entities_test.go — STRUCT-POLISH-02 Task 3: the village template entity spawns.
//
// A village template stores its inhabitants in the .nbt `entities` list (template.go already
// parses + keeps them as StructureTemplate.entities). PlaceEntities ports
// StructureTemplate.placeEntities: for each stored rawEntity, transform its position (the same
// pivot transform blocks use) + origin, clip to the box, read the entity id, and RECORD a
// SpawnRequest (a live mob the tick adds — NOT an off-tick entity, Pitfall 5).

// TestVillageSpawns: the cat template records a cat SpawnRequest at the transformed position.
func TestVillageSpawns(t *testing.T) {
	tmpl, err := LoadTemplate("village/common/animals/cat_black")
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.entities) == 0 {
		t.Fatal("cat_black template has no stored entities — template.go entity parse regressed")
	}

	view := newMapView()
	origin := Pos{100, 64, 200}
	box := BoundingBox{0, -64, 0, 1000, 320, 1000}
	rng := levelgen.NewWorldgenRandom(0)
	tmpl.PlaceEntities(view, origin, RotNone, MirrorNone, 0, 0, box, rng)

	var cats int
	for _, r := range view.spawns {
		if r.EntityType == "minecraft:cat" {
			cats++
			// The float pos = transform(entity.pos) + origin (RotNone -> identity). The cat sits
			// at origin + the template-local fractional pos.
			if r.X < float64(origin.X) || r.X > float64(origin.X)+2 {
				t.Fatalf("cat X = %v, want near origin.X=%d (transformed pos + origin)", r.X, origin.X)
			}
			if !r.PersistenceRequired {
				t.Fatal("village cat spawn not PersistenceRequired")
			}
		}
	}
	if cats != 1 {
		t.Fatalf("village cat spawn requests = %d, want exactly 1", cats)
	}
}

// TestVillageVillagerSpawns: a villager template records a villager SpawnRequest.
func TestVillageVillagerSpawns(t *testing.T) {
	tmpl, err := LoadTemplate("village/plains/villagers/nitwit")
	if err != nil {
		t.Fatal(err)
	}
	view := newMapView()
	rng := levelgen.NewWorldgenRandom(0)
	tmpl.PlaceEntities(view, Pos{0, 64, 0}, RotNone, MirrorNone, 0, 0, fullBox(), rng)

	var villagers int
	for _, r := range view.spawns {
		if r.EntityType == "minecraft:villager" {
			villagers++
		}
	}
	if villagers != 1 {
		t.Fatalf("village villager spawn requests = %d, want exactly 1", villagers)
	}
}

// TestVillageEntitiesEmptyNoSpawns: a template with no entities records nothing (no panic).
func TestVillageEntitiesEmptyNoSpawns(t *testing.T) {
	tmpl := &StructureTemplate{Size: [3]int{1, 1, 1}} // no entities
	view := newMapView()
	rng := levelgen.NewWorldgenRandom(0)
	tmpl.PlaceEntities(view, Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0, fullBox(), rng)
	if len(view.spawns) != 0 {
		t.Fatalf("empty template recorded %d spawns, want 0", len(view.spawns))
	}
}

// TestVillageEntitiesClipped: an entity whose transformed world pos is OUTSIDE the box is not
// recorded (the cross-chunk clip — only the chunk owning the entity records it).
func TestVillageEntitiesClipped(t *testing.T) {
	tmpl, err := LoadTemplate("village/common/animals/cat_black")
	if err != nil {
		t.Fatal(err)
	}
	view := newMapView()
	// A box that does NOT contain the spawn origin -> the cat is clipped out.
	farBox := BoundingBox{10000, -64, 10000, 10001, 320, 10001}
	rng := levelgen.NewWorldgenRandom(0)
	tmpl.PlaceEntities(view, Pos{0, 64, 0}, RotNone, MirrorNone, 0, 0, farBox, rng)
	if len(view.spawns) != 0 {
		t.Fatalf("out-of-box entity recorded %d spawns, want 0 (the cross-chunk clip)", len(view.spawns))
	}
}
