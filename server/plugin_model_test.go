package server

// plugin_model_test.go — MODEL-M2 (plugin_model_decl.go): the native-model declaration layer. Covers
// the load-time capture (declare_model + bone parsed into pure data), the load-time LOUD rejections
// (unknown bone parent, declare_mob(model=) with a missing model, the models.declare capability gate),
// and the OBSERVABLE spawn: a declared mob with model= spawns its bone item_display rig, mounts every
// bone on the (invisible) base as a passenger, and carries a non-nil e.model — while a modeless mob
// (and the pig oracle) spawns ZERO extra entities with e.model == nil (the pig-oracle property).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
)

// loadModelRegistry loads a one-off plugin body through the FULL declaration vocabulary (builtinsDict —
// the exact seam the embed loaders use, so declare_model + bone are present) into a fresh registry
// under the given capability grant, asserting the load SUCCEEDED. Returns the registry.
func loadModelRegistry(t *testing.T, star string, caps capSet) *mobRegistry {
	t.Helper()
	root := writeMobPlugin(t, star)
	r := newMobRegistry()
	r.setLoadCaps(caps)
	m := host.New()
	if err := m.LoadDirWith(root, r.builtinsDict()); err != nil {
		t.Fatalf("LoadDirWith: %v", err)
	}
	return r
}

// loadModelRegistryExpectErr loads a one-off plugin body under the given capability grant through the
// FULL vocabulary and asserts the load FAILED loudly. Returns the error for message assertions.
func loadModelRegistryExpectErr(t *testing.T, star string, caps capSet) error {
	t.Helper()
	root := writeMobPlugin(t, star)
	r := newMobRegistry()
	r.setLoadCaps(caps)
	m := host.New()
	err := m.LoadDirWith(root, r.builtinsDict())
	if err == nil {
		t.Fatalf("expected a load error, got nil (the bad model declaration was accepted)")
	}
	return err
}

// --- load-time capture -------------------------------------------------------------------------

// TestDeclareModelCapturesRig: declare_model captures a 2-bone rig (a base bone + a child that parents
// to it) as pure data — resolved item ids, pivots, the FIXED default context, and the parent link.
func TestDeclareModelCapturesRig(t *testing.T) {
	star := `
declare_model(name="golem_rig", bones=[
    bone(name="body", item="minecraft:stone", pivot=(0.0, 0.0, 0.0)),
    bone(name="head", item="diamond_block", pivot=(0.0, 1.5, 0.0), parent="body"),
])
`
	r := loadModelRegistry(t, star, capAll)

	md, ok := r.models.byName["golem_rig"]
	if !ok {
		t.Fatalf("no modelDecl captured under %q", "golem_rig")
	}
	if len(md.bones) != 2 {
		t.Fatalf("captured %d bones, want 2", len(md.bones))
	}
	body := md.bones[0]
	if body.name != "body" {
		t.Fatalf("bone[0].name = %q, want body", body.name)
	}
	if body.parent != "" {
		t.Fatalf("bone[0].parent = %q, want empty (root bone)", body.parent)
	}
	if body.displayContext != itemDisplayContextFixed {
		t.Fatalf("bone[0].displayContext = %d, want FIXED (%d)", body.displayContext, itemDisplayContextFixed)
	}
	if body.item.Count != 1 {
		t.Fatalf("bone[0].item.Count = %d, want 1", body.item.Count)
	}
	head := md.bones[1]
	if head.name != "head" || head.parent != "body" {
		t.Fatalf("bone[1] = {name:%q parent:%q}, want {head body}", head.name, head.parent)
	}
	if head.pivotY != 1.5 {
		t.Fatalf("bone[1].pivotY = %v, want 1.5", head.pivotY)
	}
	// The parent reference resolves within the model.
	if _, ok := md.boneOf("body"); !ok {
		t.Fatalf("boneOf(body) not found in the captured rig")
	}
}

// TestDeclareModelRejectsBadParent: a bone whose parent names no bone in the rig errors at LOAD.
func TestDeclareModelRejectsBadParent(t *testing.T) {
	star := `
declare_model(name="m", bones=[
    bone(name="body", item="minecraft:stone"),
    bone(name="head", item="minecraft:stone", parent="nonexistent"),
])
`
	err := loadModelRegistryExpectErr(t, star, capAll)
	if !contains(err.Error(), "parent") {
		t.Fatalf("error %q does not mention the bad parent", err.Error())
	}
}

// TestDeclareModelRejectsUnknownItem: a bone with an unknown item errors at LOAD (loud, at bone()).
func TestDeclareModelRejectsUnknownItem(t *testing.T) {
	star := `
declare_model(name="m", bones=[bone(name="b", item="minecraft:not_a_real_item")])
`
	err := loadModelRegistryExpectErr(t, star, capAll)
	if !contains(err.Error(), "unknown item") {
		t.Fatalf("error %q does not mention the unknown item", err.Error())
	}
}

// TestDeclareModelRequiresCapability: declare_model under a manifest WITHOUT models.declare is a loud
// LOAD error (the fail-closed LOCKED pattern — the parse-time twin of capError).
func TestDeclareModelRequiresCapability(t *testing.T) {
	star := `
declare_model(name="m", bones=[bone(name="b", item="minecraft:stone")])
`
	// capAll minus models.declare — everything else granted, so ONLY the model gate can fire.
	caps := capAll &^ capModelsDeclare
	err := loadModelRegistryExpectErr(t, star, caps)
	if !contains(err.Error(), "models.declare") {
		t.Fatalf("error %q does not mention the missing models.declare capability", err.Error())
	}
}

// TestDeclareMobRejectsMissingModel: declare_mob(model=) naming a model that was never declared errors
// at LOAD (the reference is validated at declare_mob parse — load-order rule).
func TestDeclareMobRejectsMissingModel(t *testing.T) {
	star := `
declare_mob(name="x", base_type="pig", model="ghost_rig")
`
	err := loadModelRegistryExpectErr(t, star, capAll)
	if !contains(err.Error(), "unknown model") {
		t.Fatalf("error %q does not mention the unknown model", err.Error())
	}
}

// TestDeclareMobAcceptsDeclaredModel: declare_model THEN declare_mob(model=) captures the reference on
// the mobDecl (load-order honored).
func TestDeclareMobAcceptsDeclaredModel(t *testing.T) {
	star := `
declare_model(name="golem_rig", bones=[bone(name="body", item="minecraft:stone")])
declare_mob(name="golem", base_type="zombie", model="golem_rig")
`
	r := loadModelRegistry(t, star, capAll)
	decl, ok := r.byName["golem"]
	if !ok {
		t.Fatalf("no mobDecl captured under %q", "golem")
	}
	if decl.model != "golem_rig" {
		t.Fatalf("mobDecl.model = %q, want golem_rig", decl.model)
	}
}

// --- the observable spawn ----------------------------------------------------------------------

// modeledLoop builds a physics loop whose mobRegistry loads a declared model + a mob wearing it, so
// spawnDeclaredMob materializes the rig. Returns the loop + the modeled mobDecl.
func modeledLoop(t *testing.T) (*TickLoop, *mobDecl, *modelDecl) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	star := `
declare_model(name="golem_rig", bones=[
    bone(name="body", item="minecraft:stone", pivot=(0.0, 0.0, 0.0)),
    bone(name="head", item="diamond_block", pivot=(0.0, 1.5, 0.0), parent="body"),
])
declare_mob(name="golem", base_type="zombie", model="golem_rig")
`
	r := loadModelRegistry(t, star, capAll)
	loop.SetMobRegistry(r)
	return loop, r.byName["golem"], r.models.byName["golem_rig"]
}

// TestSpawnDeclaredMobWithModelSpawnsRig: spawnDeclaredMob for a modeled mob spawns N bone
// item_display entities into the store, mounts every bone as a passenger of the base, sets the base
// invisible, and attaches a non-nil e.model whose bone count == the decl bone count.
func TestSpawnDeclaredMobWithModelSpawnsRig(t *testing.T) {
	loop, decl, md := modeledLoop(t)
	const floorY = 64

	preItemDisplays := 0
	for _, x := range loop.only().entities.all() {
		if x.isItemDisplay {
			preItemDisplays++
		}
	}

	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)

	if e.model == nil {
		t.Fatal("modeled mob has a nil e.model — the rig was not attached")
	}
	if len(e.model.bones) != len(md.bones) {
		t.Fatalf("e.model has %d bones, want %d (== decl bone count)", len(e.model.bones), len(md.bones))
	}
	// N bone item_displays were added to the store.
	postItemDisplays := 0
	boneIDs := map[int32]bool{}
	for _, x := range loop.only().entities.all() {
		if x.isItemDisplay {
			postItemDisplays++
			boneIDs[x.id] = true
			if x.typ != entity.ItemDisplay.ID {
				t.Fatalf("bone entity typ = %d, want ItemDisplay (%d)", x.typ, entity.ItemDisplay.ID)
			}
		}
	}
	if postItemDisplays-preItemDisplays != len(md.bones) {
		t.Fatalf("spawned %d bone item_displays, want %d", postItemDisplays-preItemDisplays, len(md.bones))
	}
	// Every bone is a passenger of the base.
	if len(e.passengers) != len(md.bones) {
		t.Fatalf("base has %d passengers, want %d (all bones mounted)", len(e.passengers), len(md.bones))
	}
	for _, pid := range e.passengers {
		if !boneIDs[pid] {
			t.Fatalf("passenger id %d is not a spawned bone item_display", pid)
		}
	}
	// The e.model bone ids match the passengers + carry the shared decl bones.
	for i := range e.model.bones {
		br := e.model.bones[i]
		if !boneIDs[br.id] {
			t.Fatalf("e.model.bones[%d].id %d is not in the store", i, br.id)
		}
		if br.decl == nil || br.decl.name != md.bones[i].name {
			t.Fatalf("e.model.bones[%d].decl mismatch (want %q)", i, md.bones[i].name)
		}
	}
	// The base is invisible (SHARED_FLAGS bit 0x20 spliced onto the spawn metadata).
	if !metadataHasInvisibleFlag(e.metadata) {
		t.Fatalf("base metadata does not carry the invisible SHARED_FLAGS bit 0x20: % x", e.metadata)
	}
}

// TestModelBonesMirrorBase: after the base moves, tickModelRig copies the base x/y/z onto every bone
// display so the server-side positions track (the M2 static coordinate mirror, H.1.5).
func TestModelBonesMirrorBase(t *testing.T) {
	loop, decl, _ := modeledLoop(t)
	const floorY = 64
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)

	// Move the base, then run the rig tick.
	e.x, e.y, e.z = 20.5, float64(floorY+1), 24.5
	loop.tickModelRig(e)

	for i := range e.model.bones {
		bone := loop.entityByIDAnyRegion(e.model.bones[i].id)
		if bone == nil {
			t.Fatalf("bone %d not found", i)
		}
		if bone.x != e.x || bone.y != e.y || bone.z != e.z {
			t.Fatalf("bone %d at (%v,%v,%v), want base (%v,%v,%v)", i, bone.x, bone.y, bone.z, e.x, e.y, e.z)
		}
	}
}

// --- the pig-oracle property -------------------------------------------------------------------

// TestModelessMobHasNilModel: a declared mob WITHOUT model= spawns ZERO extra entities, sets e.model
// == nil, and does not touch the passenger list — the modeless code path is byte-identical to a plain
// mob spawn (the pig-oracle guarantee that a nil model is zero new code path).
func TestModelessMobHasNilModel(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	star := `
declare_mob(name="plain", base_type="pig")
`
	r := loadModelRegistry(t, star, capAll)
	loop.SetMobRegistry(r)
	decl := r.byName["plain"]

	preTotal := len(loop.only().entities.all())
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)

	if e.model != nil {
		t.Fatal("a modeless mob must have e.model == nil (no rig, no new code path)")
	}
	if len(e.passengers) != 0 {
		t.Fatalf("a modeless mob must have no passengers, got %d", len(e.passengers))
	}
	if metadataHasInvisibleFlag(e.metadata) {
		t.Fatal("a modeless mob must NOT be invisible")
	}
	// Exactly ONE new entity (the mob itself) — no bone item_displays.
	if got := len(loop.only().entities.all()); got != preTotal+1 {
		t.Fatalf("modeless spawn added %d entities, want exactly 1 (the mob, no bones)", got-preTotal)
	}
	for _, x := range loop.only().entities.all() {
		if x.isItemDisplay {
			t.Fatalf("a modeless mob spawned a bone item_display (id %d) — it must spawn none", x.id)
		}
	}
}

// TestVanillaPigHasNilModel: a plain vanilla pig (spawnVanillaPig) carries e.model == nil and spawns
// zero item_display bones — the model system adds nothing to the byte-identical oracle pig.
func TestVanillaPigHasNilModel(t *testing.T) {
	loop, mgr := newPhysicsLoop() // installs the vanilla_pig registry
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	pre := len(loop.only().entities.all())
	pig := loop.spawnVanillaPig(8.5, float64(floorY+1), 8.5)

	if pig.model != nil {
		t.Fatal("the vanilla pig must have e.model == nil (the pig oracle stays byte-identical)")
	}
	if len(pig.passengers) != 0 {
		t.Fatalf("the vanilla pig must have no passengers, got %d", len(pig.passengers))
	}
	if got := len(loop.only().entities.all()); got != pre+1 {
		t.Fatalf("the vanilla pig spawn added %d entities, want exactly 1 (no bones)", got-pre)
	}
}

// metadataHasInvisibleFlag reports whether the entity metadata byte slice contains a SHARED_FLAGS
// (index 0, BYTE serializer 0) entry with the invisible bit 0x20 set. The entry framing is
// UnsignedByte(index=0) + VarInt(serializerID=0) + Byte(flags), so scan for the 0x00 0x00 <flags>
// triple with the 0x20 bit set.
func metadataHasInvisibleFlag(md []byte) bool {
	for i := 0; i+2 < len(md); i++ {
		if md[i] == 0x00 && md[i+1] == 0x00 && md[i+2]&0x20 != 0 {
			return true
		}
	}
	return false
}
