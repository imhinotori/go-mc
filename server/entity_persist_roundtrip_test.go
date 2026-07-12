package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// TestEntityPersistRoundTrip covers the baseline round-trip (item + a bare pig) INCLUDING the P0-01
// UUID preservation (do NOT mint a new UUID on load).
func TestEntityPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	loop := NewTickLoop(newFakeClock())
	pos := columnOf(8.5, 8.5)

	diamondID := itemNameToID("minecraft:diamond")
	item := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64, 8.5, component.SlotData{ItemID: pk.VarInt(diamondID), Count: 5})
	item.vx, item.vy, item.vz = 0.01, 0.2, -0.01
	item.age = 42
	item.pickupDelay = 7

	pig := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.2, 64, 8.4)
	initSpawnHealth(pig)
	pig.health = 8
	pig.yaw = 45

	ir, ok1 := entityToDisk(item)
	pr, ok2 := entityToDisk(pig)
	if !ok1 || !ok2 {
		t.Fatalf("entityToDisk failed: item=%v pig=%v", ok1, ok2)
	}
	if err := saveEntities(dir, pos, []save.Entities{ir, pr}); err != nil {
		t.Fatalf("saveEntities: %v", err)
	}
	restored, ok, err := loadEntities(dir, pos)
	if err != nil || !ok {
		t.Fatalf("loadEntities err=%v ok=%v", err, ok)
	}
	if len(restored) != 2 {
		t.Fatalf("recovered %d entities, want 2", len(restored))
	}

	var gotItem, gotMob *Entity
	for _, rec := range restored {
		e, ok := diskToEntity(loop, rec)
		if !ok {
			t.Fatalf("diskToEntity failed for %q", rec.ID)
		}
		if e.isItem {
			gotItem = e
		} else {
			gotMob = e
		}
	}
	if gotItem == nil || gotMob == nil {
		t.Fatalf("missing reconstructed entity: item=%v mob=%v", gotItem, gotMob)
	}
	if gotItem.itemStack.ItemID != pk.VarInt(diamondID) || gotItem.itemStack.Count != 5 {
		t.Fatalf("item stack = id %d count %d, want diamond x5", gotItem.itemStack.ItemID, gotItem.itemStack.Count)
	}
	if gotItem.age != 42 || gotItem.pickupDelay != 7 {
		t.Fatalf("item age/pickupDelay = (%d, %d), want (42, 7)", gotItem.age, gotItem.pickupDelay)
	}
	if gotItem.vy != 0.2 {
		t.Fatalf("item vy = %v, want 0.2 (motion restored, no RNG re-toss)", gotItem.vy)
	}
	if gotMob.typ != entity.Pig.ID {
		t.Fatalf("mob typ = %d, want pig(%d)", gotMob.typ, entity.Pig.ID)
	}
	if gotMob.health != 8 {
		t.Fatalf("mob health = %v, want 8", gotMob.health)
	}
	if gotMob.yaw != 45 {
		t.Fatalf("mob yaw = %v, want 45", gotMob.yaw)
	}
	if gotMob.uuid != pig.uuid {
		t.Fatalf("mob UUID = %v, want %v (must be restored, not re-minted)", gotMob.uuid, pig.uuid)
	}
	if gotItem.uuid != item.uuid {
		t.Fatalf("item UUID = %v, want %v (must be restored)", gotItem.uuid, item.uuid)
	}
}

// TestMobPersistReconstructsAIAndState is the P0-01 gate: a spawned-then-saved-then-loaded mob must
// reconstruct an AI-bearing runtime IDENTICAL to the spawn path -- goals attached, UUID preserved,
// equipment/owner/age/effects restored -- NOT the old type+health husk.
func TestMobPersistReconstructsAIAndState(t *testing.T) {
	dir := t.TempDir()
	loop := NewTickLoop(newFakeClock())
	installVanillaPigRegistry(loop) // the full //go:embed registry: pig + zombie + wolf declarations
	loop.start(loop.clock.Now())
	pos := columnOf(8.5, 8.5)

	pig := loop.spawnVanillaMob(vanillaPigMobName, 8.5, 64, 8.5)
	pig.breedAge = -12000
	pig.health = 6
	pigGoals := len(pig.ai.goals.goals)

	zombie := loop.spawnVanillaMob(vanillaZombieMobName, 8.6, 64, 8.6)
	ironHelmet := itemNameToID("minecraft:iron_helmet")
	zombie.equipment[eqSlotHead] = component.SlotData{ItemID: pk.VarInt(ironHelmet), Count: 1}
	zombie.equipmentDropChances[eqSlotHead] = 0.5
	if inst := zombie.attributes.GetInstance(attribute.MaxHealth.Name()); inst != nil {
		inst.SetBaseValue(30)
	}
	zombie.health = 30
	zombie.mobEffects = map[string]*activeEffect{
		effectSpeed: {id: effectSpeed, duration: 400, amplifier: 1, showIcon: true, visible: true},
	}
	zombieGoals := len(zombie.ai.goals.goals)
	zombieTargets := len(zombie.ai.targetSelector.goals)

	wolf := loop.spawnVanillaMob(vanillaWolfMobName, 8.7, 64, 8.7)
	wolf.tame = true
	wolf.ownerUUID = 4242
	wolf.inSittingPose = true
	wolf.angerEndTime = loop.gametime + 500
	wolfGoals := len(wolf.ai.goals.goals)
	wolfTargets := len(wolf.ai.targetSelector.goals)

	recs := make([]save.Entities, 0, 3)
	for _, e := range []*Entity{pig, zombie, wolf} {
		r, ok := entityToDisk(e)
		if !ok {
			t.Fatalf("entityToDisk failed for typ %d", e.typ)
		}
		recs = append(recs, r)
	}
	if err := saveEntities(dir, pos, recs); err != nil {
		t.Fatalf("saveEntities: %v", err)
	}
	restored, ok, err := loadEntities(dir, pos)
	if err != nil || !ok {
		t.Fatalf("loadEntities err=%v ok=%v", err, ok)
	}
	byType := map[entity.ID]*Entity{}
	for _, rec := range restored {
		e, ok := diskToEntity(loop, rec)
		if !ok {
			t.Fatalf("diskToEntity failed for %q", rec.ID)
		}
		byType[e.typ] = e
	}

	gp := byType[entity.Pig.ID]
	if gp == nil {
		t.Fatal("pig not reconstructed")
	}
	if gp.ai == nil {
		t.Fatal("reloaded pig has NO ai (P0-01: the goalSelector/targetSelector must be reattached)")
	}
	if got := len(gp.ai.goals.goals); got != pigGoals {
		t.Fatalf("reloaded pig has %d goals, want %d (identical to the spawn path)", got, pigGoals)
	}
	if gp.uuid != pig.uuid {
		t.Fatalf("reloaded pig UUID = %v, want %v (must be restored, not re-minted)", gp.uuid, pig.uuid)
	}
	if gp.breedAge != -12000 {
		t.Fatalf("reloaded pig breedAge = %d, want -12000 (baby age restored)", gp.breedAge)
	}
	if !gp.isBaby() {
		t.Fatal("reloaded pig is not a baby (age machine not restored)")
	}
	if gp.health != 6 {
		t.Fatalf("reloaded pig health = %v, want 6", gp.health)
	}

	gz := byType[entity.Zombie.ID]
	if gz == nil {
		t.Fatal("zombie not reconstructed")
	}
	if gz.ai == nil {
		t.Fatal("reloaded zombie has NO ai")
	}
	if got := len(gz.ai.goals.goals); got != zombieGoals {
		t.Fatalf("reloaded zombie has %d goalSelector goals, want %d", got, zombieGoals)
	}
	if got := len(gz.ai.targetSelector.goals); got != zombieTargets {
		t.Fatalf("reloaded zombie has %d targetSelector goals, want %d (combat targeting reattached)", got, zombieTargets)
	}
	if gz.equipment[eqSlotHead].ItemID != pk.VarInt(ironHelmet) || gz.equipment[eqSlotHead].Count != 1 {
		t.Fatalf("reloaded zombie head slot = %+v, want iron_helmet x1", gz.equipment[eqSlotHead])
	}
	if gz.equipmentDropChances[eqSlotHead] != 0.5 {
		t.Fatalf("reloaded zombie head drop chance = %v, want 0.5", gz.equipmentDropChances[eqSlotHead])
	}
	if inst := gz.attributes.GetInstance(attribute.MaxHealth.Name()); inst == nil || inst.BaseValue() != 30 {
		t.Fatalf("reloaded zombie max_health base not restored (want 30): %v", inst)
	}
	if ef := gz.mobEffects[effectSpeed]; ef == nil || ef.duration != 400 || ef.amplifier != 1 {
		t.Fatalf("reloaded zombie speed effect not restored: %+v", ef)
	}
	if gz.uuid != zombie.uuid {
		t.Fatalf("reloaded zombie UUID not restored")
	}

	gw := byType[entity.Wolf.ID]
	if gw == nil {
		t.Fatal("wolf not reconstructed")
	}
	if gw.ai == nil {
		t.Fatal("reloaded wolf has NO ai")
	}
	if got := len(gw.ai.goals.goals); got != wolfGoals {
		t.Fatalf("reloaded wolf has %d goalSelector goals, want %d", got, wolfGoals)
	}
	if got := len(gw.ai.targetSelector.goals); got != wolfTargets {
		t.Fatalf("reloaded wolf has %d targetSelector goals, want %d", got, wolfTargets)
	}
	if !gw.tame {
		t.Fatal("reloaded wolf is NOT tame (owner not restored)")
	}
	if gw.ownerUUID != 4242 {
		t.Fatalf("reloaded wolf ownerUUID = %d, want 4242", gw.ownerUUID)
	}
	if !gw.inSittingPose {
		t.Fatal("reloaded wolf is not sitting (Sitting not restored)")
	}
	if gw.angerEndTime != loop.gametime+500 {
		t.Fatalf("reloaded wolf angerEndTime = %d, want %d", gw.angerEndTime, loop.gametime+500)
	}
	if gw.uuid != wolf.uuid {
		t.Fatalf("reloaded wolf UUID not restored")
	}
}
