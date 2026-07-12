package server

// entity_persist.go -- SUB-PERSIST (Part C): in-chunk (per-column) entity persistence, wiring the
// previously-DEAD saveEntities/loadEntities region path (persistence.go) to a live snapshot/restore of
// the tick-owned entityStore. Vanilla writes entities to a SEPARATE region (EntityStorage ->
// entities/r.x.z.mca, ENTITIES_TAG "Entities"), NOT the legacy in-chunk Entities list -- this is that
// modern path (the codebase already had the region IO; it just had no caller building a save.Entities
// from a live *Entity). v1 persists the entity kinds the codebase round-trips: dropped ItemEntity
// (Item/Age/PickupDelay) and generic mobs (id/Pos/Motion/Rotation/UUID/Health).
//
// CITED JAR (26.2-inner.jar):
//   - net.minecraft.world.level.chunk.storage.EntityStorage.storeEntities / loadEntities: the per-
//     column ChunkEntities <-> entities-region mapping (ENTITIES_TAG "Entities").
//   - net.minecraft.world.entity.Entity.save / saveWithoutId: id (EntityType.CODEC), Pos, Motion,
//     Rotation, OnGround, UUID, PortalCooldown, ...
//   - net.minecraft.world.entity.LivingEntity.addAdditionalSaveData: Health.
//   - net.minecraft.world.entity.item.ItemEntity.addAdditionalSaveData: Item, Age, PickupDelay.
//
// RNG SAFETY (pig oracle): the LOAD path (diskToEntity) reconstructs from saved data ONLY -- it never
// calls attribute.FinalizeSpawn (a fresh-spawn draw off the shared levelRandom) and never draws the
// ItemEntity toss velocities (it restores the saved Motion). So respawning saved entities on chunk
// load perturbs NEITHER the shared levelRandom stream NOR the per-mob AI stream (seeded from the
// entity id). initSpawnHealth is called only for a legacy record with no saved Health (a bare field
// write, RNG-free). The dogfood pig oracle uses no chunk streaming, so this path is off its trace.
//
// DOUBLE-SPAWN GUARD: the load side is driven by a one-shot SavedEntities list on the ChunkResult,
// consumed exactly once when the column becomes Ready (drainSavedEntities), mirroring how
// drainStructureSpawns consumes res.Spawns. A column loaded twice carries the saved list only on the
// first (generation/region) load; a re-Insert of an already-live column carries an empty list.

import (
	"bytes"
	"sync"

	"github.com/google/uuid"
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

var (
	entityByNameOnce sync.Once
	entityByName     map[string]entity.Entity
)

func resolveEntityByName(name string) (entity.Entity, bool) {
	entityByNameOnce.Do(func() {
		entityByName = make(map[string]entity.Entity, len(entity.ByID))
		for _, e := range entity.ByID {
			entityByName[e.Name] = *e
		}
	})
	n := name
	if i := indexByte(n, ':'); i >= 0 {
		n = n[i+1:]
	}
	e, ok := entityByName[n]
	return e, ok
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func entityToDisk(e *Entity) (save.Entities, bool) {
	if e == nil || e.dead {
		return save.Entities{}, false
	}
	if e.typ == entity.Player.ID {
		return save.Entities{}, false
	}
	rec := save.Entities{
		ID:             "minecraft:" + entityTypeName(e.typ),
		Pos:            [3]float64{e.x, e.y, e.z},
		Motion:         [3]float64{e.vx, e.vy, e.vz},
		Rotation:       [2]float32{e.yaw, e.pitch},
		UUID:           uuidToInts(e.uuid),
		OnGround:       e.onGround,
		PortalCooldown: 0,
		Health:         e.health,
	}
	if e.isItem {
		rec.Age = int32(e.age)
		rec.PickupDelay = int16(e.pickupDelay)
		item := save.ItemStackDisk{
			ID:    itemName(int32(e.itemStack.ItemID)),
			Count: int32(e.itemStack.Count),
		}
		rec.Item = &item
		return rec, true
	}
	// MOB save contract (P0-01): write the full LivingEntity/Mob/AgeableMob/TamableAnimal/NeutralMob
	// extra state a mob carries, so diskToEntity can rebuild an AI-bearing runtime identical to the
	// spawn path (RNG-free). A player has already returned above; a non-living entity (a projectile,
	// a falling block) simply carries zero-valued mob fields, so every mob tag stays omitempty-dropped
	// and its on-disk shape is byte-identical to the pre-P0-01 record. CITE Entity.saveWithoutId ->
	// LivingEntity/Mob/AgeableMob/TamableAnimal/NeutralMob.addAdditionalSaveData.
	writeMobDisk(&rec, e)
	return rec, true
}

// writeMobDisk fills rec's MOB save contract from the live Entity — the entityToDisk mob tail
// (LivingEntity.addAdditionalSaveData: AbsorptionAmount/equipment/attributes/active_effects; Mob:
// drop_chances/PersistenceRequired/CanPickUpLoot/LeftHanded; AgeableMob: Age/ForcedAge/AgeLocked;
// Animal: InLove; TamableAnimal: Owner/Sitting; NeutralMob: anger_end_time; per-type variant). Every
// write is an unconditional field copy (no RNG). CITE the respective addAdditionalSaveData.
func writeMobDisk(rec *save.Entities, e *Entity) {
	rec.AbsorptionAmount = e.absorptionAmount
	// equipment: EntityEquipment.CODEC unbounded map, keyed by LOWERCASE slot name, skip EMPTY.
	for slot := 0; slot < equipmentSlotCount; slot++ {
		st := e.equipment[slot]
		if st.Count <= 0 {
			continue // EMPTY: EntityEquipment.CODEC omits it
		}
		if rec.Equipment == nil {
			rec.Equipment = make(map[string]save.ItemStackDisk)
		}
		rec.Equipment[equipmentSlotName(slot)] = save.ItemStackDisk{
			ID:    itemName(int32(st.ItemID)),
			Count: int32(st.Count),
		}
		// drop_chances: write only a slot whose chance differs from the 0.085f default (Mob.save
		// writes the full map; the omitempty-per-slot reduction round-trips the non-default set).
		if e.equipmentDropChances[slot] != 0 && e.equipmentDropChances[slot] != defaultEquipmentDropChance {
			if rec.DropChances == nil {
				rec.DropChances = make(map[string]float32)
			}
			rec.DropChances[equipmentSlotName(slot)] = e.equipmentDropChances[slot]
		}
	}
	// attributes: AttributeMap.save writes every instance whose base differs from its default (the
	// permanent finalizeSpawn bonus + any setBaseValue override). Keyed by the short registry name
	// (the runtime Map's key space); the "minecraft:" namespace prefix is a cited reduction (the
	// runtime is not yet keyed by full resource id). CITE AttributeInstance.save (id + base).
	if e.attributes != nil {
		for name, inst := range e.attributes.LocalInstances() {
			if inst == nil {
				continue
			}
			if inst.BaseValue() == inst.Attribute().DefaultValue() {
				continue // unchanged from the supplier default: AttributeMap.save skips it
			}
			rec.Attributes = append(rec.Attributes, save.AttributeDisk{ID: name, Base: inst.BaseValue()})
		}
	}
	// active_effects: LivingEntity.activeEffects. The runtime activeEffect carries a subset of the
	// MobEffectInstance fields (mob_effect.go); round-trip that subset. CITE MobEffectInstance.save.
	for _, ef := range e.mobEffects {
		if ef == nil {
			continue
		}
		rec.ActiveEffects = append(rec.ActiveEffects, save.MobEffectDisk{
			ID:            ef.id,
			Amplifier:     byte(ef.amplifier),
			Duration:      int32(ef.duration),
			Ambient:       ef.ambient,
			ShowParticles: ef.visible,
			ShowIcon:      ef.showIcon,
		})
	}
	// PersistenceRequired: the runtime has no dedicated field yet (a cited reduction); it is written
	// only when a future field lands. CanPickUpLoot/LeftHanded round-trip 1:1.
	rec.CanPickUpLoot = e.canPickUpLoot
	rec.LeftHanded = e.leftHanded
	// AgeableMob: Age (the breeding age machine, shared "Age" tag) + ForcedAge/AgeLocked (v1 const-0
	// stubs). CITE AgeableMob.addAdditionalSaveData.
	rec.Age = int32(e.breedAge)
	// Animal.InLove.
	rec.InLove = int32(e.inLove)
	// TamableAnimal.Owner/Sitting — Owner is the owner UUID; the runtime carries a THIN 32-bit owner
	// ref (ownerUUID, a cited v1 reduction of the full owner UUID). Store it in the low int of the
	// Owner [4]int32 so tame state round-trips; a future full-UUID owner slots into the other ints.
	if e.tame {
		rec.Owner = [4]int32{0, 0, 0, e.ownerUUID}
	}
	rec.Sitting = e.inSittingPose
	// NeutralMob anger endpoint (a gametime): 1:1 with the runtime angerEndTime. CITE
	// NeutralMob.addPersistentAngerSaveData (putLong "anger_end_time" == getPersistentAngerEndTime()).
	rec.AngerEndTime = e.angerEndTime
	// Per-type variant/state tags (the finalizeSpawn results load must NOT re-roll).
	rec.SheepColor = e.sheepColor
	rec.Sheared = e.sheared
	switch e.typ {
	case entity.Cat.ID:
		rec.Variant = e.catVariant
		rec.CollarColor = int32(e.catCollarColor)
	case entity.Fox.ID:
		rec.Variant = e.foxVariant
	case entity.Rabbit.ID:
		rec.Variant = e.rabbitVariant
	}
}

// equipmentSlotName maps an EquipmentSlot ordinal to its LOWERCASE StringRepresentable name (the
// EntityEquipment.CODEC / drop_chances map key). CITE EquipmentSlot enum (mainhand/offhand/feet/
// legs/chest/head/body/saddle).
func equipmentSlotName(slot int) string {
	switch slot {
	case eqSlotMainHand:
		return "mainhand"
	case eqSlotOffHand:
		return "offhand"
	case eqSlotFeet:
		return "feet"
	case eqSlotLegs:
		return "legs"
	case eqSlotChest:
		return "chest"
	case eqSlotHead:
		return "head"
	case eqSlotBody:
		return "body"
	case eqSlotSaddle:
		return "saddle"
	}
	return ""
}

// equipmentSlotByName is the inverse of equipmentSlotName (diskToEntity equipment restore). Returns
// (ordinal, true) for a known slot name, (0, false) otherwise.
func equipmentSlotByName(name string) (int, bool) {
	switch name {
	case "mainhand":
		return eqSlotMainHand, true
	case "offhand":
		return eqSlotOffHand, true
	case "feet":
		return eqSlotFeet, true
	case "legs":
		return eqSlotLegs, true
	case "chest":
		return eqSlotChest, true
	case "head":
		return eqSlotHead, true
	case "body":
		return eqSlotBody, true
	case "saddle":
		return eqSlotSaddle, true
	}
	return 0, false
}

func diskToEntity(t *TickLoop, rec save.Entities) (*Entity, bool) {
	typeRec, ok := resolveEntityByName(rec.ID)
	if !ok {
		return nil, false
	}
	id := t.idAlloc.AllocID()
	if typeRec.ID == entity.Item.ID {
		stack := component.SlotData{}
		if rec.Item != nil {
			stack.ItemID = pk.VarInt(itemNameToID(rec.Item.ID))
			stack.Count = pk.VarInt(rec.Item.Count)
		}
		ie := NewEntity(id, entity.Item, rec.Pos[0], rec.Pos[1], rec.Pos[2])
		ie.uuid = intsToUUID(rec.UUID) // restore the persisted identity (Entity.load readUUID)
		ie.isItem = true
		ie.itemStack = stack
		ie.vx, ie.vy, ie.vz = rec.Motion[0], rec.Motion[1], rec.Motion[2]
		ie.yaw, ie.pitch = rec.Rotation[0], rec.Rotation[1]
		ie.onGround = rec.OnGround
		ie.age = int(rec.Age)
		ie.pickupDelay = int(rec.PickupDelay)
		ie.metadata = encodeItemMetadata(stack)
		return ie, true
	}
	e := NewEntity(id, typeRec, rec.Pos[0], rec.Pos[1], rec.Pos[2])
	// UUID: restore the PERSISTED identity (do NOT mint a new one). NewEntity seeded a fresh
	// uuid.New(); overwrite it so cross-references (owner, leashes, scoreboard) survive a reload.
	// CITE Entity.load (readUUID "UUID").
	e.uuid = intsToUUID(rec.UUID)
	e.vx, e.vy, e.vz = rec.Motion[0], rec.Motion[1], rec.Motion[2]
	e.yaw, e.pitch = rec.Rotation[0], rec.Rotation[1]
	e.headYaw = rec.Rotation[0]
	e.onGround = rec.OnGround
	if rec.Health > 0 {
		e.health = rec.Health
	} else {
		initSpawnHealth(e)
	}
	// MOB reconstruction (P0-01): rebuild the SAME AI-bearing runtime the spawn path builds
	// (goalSelector/targetSelector/brain via the declaration registry) and restore every persisted
	// state field. RNG-FREE: buildAIFromDecl + reseedMobAI are deterministic (no draw), and no
	// finalizeSpawn runs, so the load never perturbs the shared levelRandom or the pig oracle stream.
	reconstructMobRuntime(t, e, rec)
	return e, true
}

func (t *TickLoop) snapshotColumnEntities(pos level.ChunkPos) []save.Entities {
	r := t.regionForColumn(pos)
	if r == nil || r.entities == nil {
		return nil
	}
	bucket := r.entities.buckets[pos]
	if len(bucket) == 0 {
		return nil
	}
	out := make([]save.Entities, 0, len(bucket))
	for _, e := range bucket {
		if rec, ok := entityToDisk(e); ok {
			out = append(out, rec)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func uuidToInts(u [16]byte) [4]int32 {
	var out [4]int32
	for i := 0; i < 4; i++ {
		out[i] = int32(uint32(u[i*4])<<24 | uint32(u[i*4+1])<<16 | uint32(u[i*4+2])<<8 | uint32(u[i*4+3]))
	}
	return out
}

// intsToUUID is the inverse of uuidToInts: the 4-int32 disk UUID -> the 16-byte uuid.UUID. Restores
// the persisted entity identity on load (Entity.load readUUID). A zero [4]int32 (a legacy record that
// never stored a UUID) yields the nil UUID; the caller keeps NewEntity's fresh uuid.New() only if it
// wants -- diskToEntity always overwrites, so a zero record loses its identity (acceptable: the
// pre-P0-01 records this handles were never read for UUID anyway).
func intsToUUID(in [4]int32) uuid.UUID {
	var u uuid.UUID
	for i := 0; i < 4; i++ {
		v := uint32(in[i])
		u[i*4] = byte(v >> 24)
		u[i*4+1] = byte(v >> 16)
		u[i*4+2] = byte(v >> 8)
		u[i*4+3] = byte(v)
	}
	return u
}

// reconstructMobRuntime rebuilds a reloaded mob's AI-bearing runtime + restores its persisted state,
// the LOAD analogue of the RNG-drawing spawnDeclaredMob (plugin_mob_decl.go). It is deliberately
// RNG-FREE (vanilla Entity.load draws no RNG): it reuses the SAME declaration the live spawn path
// uses (mobRegistry.declByBaseType keyed by the entity type) to attach the goalSelector /
// targetSelector / brain via buildAIFromDecl + reseedMobAI (both deterministic), then applies the
// saved equipment/attributes/effects/age/love/owner/anger/variant directly onto the entity. It does
// NOT call finalizeSpawn and draws NO new RNG, so respawning a saved mob on chunk load perturbs
// neither the shared levelRandom nor the per-mob AI stream (pig oracle safe). CITE
// LivingEntity/Mob/AgeableMob/TamableAnimal/NeutralMob.readAdditionalSaveData.
func reconstructMobRuntime(t *TickLoop, e *Entity, rec save.Entities) {
	// AI attach: route through the declaration registry the live spawn path uses. declByBaseType finds
	// the ONE dogfooded declaration whose base_type is this entity type (the vanilla_pig/zombie/wolf
	// plugin), so the reloaded mob gets the IDENTICAL goalSelector/targetSelector the spawn path builds
	// -- not a bare type+health husk. A registry miss (a test loop with no registry, or an unported
	// type) leaves e.ai nil: the mob still reloads with its state, it just has no goals (the same
	// graceful degrade a never-declared mob has). buildAIFromDecl draws NO RNG; reseedMobAI is a pure
	// deterministic reseed by entity id -- so the whole attach is RNG-free.
	if t != nil && t.mobRegistry != nil {
		if decl := t.mobRegistry.declByBaseType(e.typ); decl != nil {
			e.ai = buildAIFromDecl(t, decl)
			reseedMobAI(e.ai, e.id)
			// Brain-bearing types get their brain via the SAME attach helpers the spawn path uses (after
			// e.ai is built so the brain's per-mob rng is e.ai.rng). RNG-free attach.
			switch e.typ {
			case entity.HappyGhast.ID:
				attachHappyGhastBrain(e)
			case entity.Villager.ID:
				attachVillagerBrain(e)
			}
		}
	}
	e.absorptionAmount = rec.AbsorptionAmount
	// equipment + drop chances: restore the saved slots onto the live equipment array (the tracker's
	// detectMobEquipmentUpdates broadcasts them on the next tick, exactly like a spawn-time equip).
	for name, disk := range rec.Equipment {
		slot, ok := equipmentSlotByName(name)
		if !ok {
			continue
		}
		e.equipment[slot] = component.SlotData{
			ItemID: pk.VarInt(itemNameToID(disk.ID)),
			Count:  pk.VarInt(disk.Count),
		}
	}
	for name, chance := range rec.DropChances {
		if slot, ok := equipmentSlotByName(name); ok {
			e.equipmentDropChances[slot] = chance
		}
	}
	// attributes: SetBaseValue the saved base onto the entity's local instance (GetInstance clones the
	// supplier template when first touched), overriding the supplier default -- the same seedAttributes
	// mechanism the spawn path uses, but keyed by the saved short id. An attribute the type does not
	// have (GetInstance == nil) is skipped (the override cannot apply).
	if e.attributes != nil {
		for _, ad := range rec.Attributes {
			if inst := e.attributes.GetInstance(ad.ID); inst != nil {
				inst.SetBaseValue(ad.Base)
			}
		}
	}
	// active_effects: rebuild the entity-side MobEffectInstance map (LivingEntity.activeEffects).
	for _, ed := range rec.ActiveEffects {
		if e.mobEffects == nil {
			e.mobEffects = make(map[string]*activeEffect)
		}
		e.mobEffects[ed.ID] = &activeEffect{
			id:        ed.ID,
			duration:  int(ed.Duration),
			amplifier: int(ed.Amplifier),
			ambient:   ed.Ambient,
			visible:   ed.ShowParticles,
			showIcon:  ed.ShowIcon,
		}
	}
	e.canPickUpLoot = rec.CanPickUpLoot
	e.leftHanded = rec.LeftHanded
	// AgeableMob age machine: restore the breeding age (a negative value re-establishes a baby, which
	// refreshDimensions shrinks to the half-scale box). InLove restores the love countdown.
	e.breedAge = int(rec.Age)
	e.inLove = int(rec.InLove)
	if e.isBaby() {
		e.refreshDimensions()
	}
	// TamableAnimal: a non-nil-owner record re-establishes tame + the thin owner ref + sit pose.
	if rec.Owner != ([4]int32{}) {
		e.tame = true
		e.ownerUUID = rec.Owner[3]
	}
	e.inSittingPose = rec.Sitting
	// NeutralMob anger endpoint (a gametime) round-trips 1:1.
	e.angerEndTime = rec.AngerEndTime
	// per-type variant/state.
	e.sheepColor = rec.SheepColor
	e.sheared = rec.Sheared
	switch e.typ {
	case entity.Cat.ID:
		e.catVariant = rec.Variant
		e.catCollarColor = int(rec.CollarColor)
	case entity.Fox.ID:
		e.foxVariant = rec.Variant
	case entity.Rabbit.ID:
		e.rabbitVariant = rec.Variant
	}
	// Splice the spawn-time wire metadata the tracker's first AddEntity/SetEntityData needs so a
	// reloaded baby / tamed-or-sitting wolf / colored sheep renders correctly from the first packet
	// (the SAME seams spawnDeclaredMob uses). RNG-free byte splices.
	spliceReloadMetadata(e)
}

// spliceReloadMetadata appends the spawn-time DATA entries a reloaded mob needs on its first wire
// broadcast, mirroring the finalizeSpawn tail of spawnDeclaredMob (baby flag, wolf tame/sit flags,
// sheep wool color). Each splice is gated so an adult non-wolf non-sheep mob (the pig oracle path,
// which never reaches this file on its dogfood trace) appends NOTHING. RNG-free.
func spliceReloadMetadata(e *Entity) {
	if e.isBaby() {
		var buf bytes.Buffer
		_, _ = babyDataEntry(e.isBaby()).WriteTo(&buf)
		e.metadata = append(e.metadata, buf.Bytes()...)
	}
	if e.typ == entity.Wolf.ID {
		if flags := wolfFlagsByte(e.inSittingPose, e.tame); flags != 0 {
			var buf bytes.Buffer
			_, _ = wolfFlagsDataEntry(flags).WriteTo(&buf)
			e.metadata = append(e.metadata, buf.Bytes()...)
		}
	}
	if e.typ == entity.Sheep.ID {
		if wb := woolByteFor(e.sheepColor, e.sheared); wb != 0 {
			var buf bytes.Buffer
			_, _ = woolDataEntry(wb).WriteTo(&buf)
			e.metadata = append(e.metadata, buf.Bytes()...)
		}
	}
}

// drainSavedEntities respawns the persisted entities for a column when it becomes Ready (SUB-PERSIST,
// Part C load side), reading the parallel entities/r.x.z.mca region on the OWNER and reconstructing
// each save.Entities into a live *Entity that is added to the owning region store (the tracker then
// broadcasts AddEntity next tick, exactly like drainStructureSpawns). It is called from
// chunkReady.applyTo inside the withRegion block for the loaded column, so cur() is the owning region.
//
// DOUBLE-SPAWN GUARD: t.entitiesLoaded[pos] is set on the first drain; a subsequent Insert of the same
// column (reload / regen) short-circuits, so a column loaded twice never duplicates its saved entities.
// A "" persistDir (tests / ephemeral) is a no-op. A miss (never-saved cell) or an IO error is skipped
// (logged), and the column is still marked loaded so a bad cell is not retried every Insert.
//
// RNG SAFETY: diskToEntity does NOT draw the shared levelRandom (no FinalizeSpawn) nor the item toss
// velocities (it restores saved Motion), so this respawn perturbs no RNG stream (pig oracle safe).
func (t *TickLoop) drainSavedEntities(pos level.ChunkPos) {
	if t.persistDir == "" {
		return
	}
	if t.entitiesLoaded == nil {
		t.entitiesLoaded = make(map[level.ChunkPos]bool)
	}
	if t.entitiesLoaded[pos] {
		return // already drained for this column: never double-spawn
	}
	t.entitiesLoaded[pos] = true

	ents, ok, err := loadEntities(t.persistDir, pos)
	if err != nil {
		udebug("chunksave", "entity load %v: %v", pos, err)
		return
	}
	if !ok || len(ents) == 0 {
		return // never-saved cell / empty: nothing to respawn
	}
	for _, rec := range ents {
		e, ok := diskToEntity(t, rec)
		if !ok {
			continue // unknown/unported entity type: skip (build-data robustness)
		}
		t.cur().entities.add(e)
	}
}
