package save

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"

	"github.com/imhinotori/sulfur/nbt"
)

// Chunk is 16* chunk
type Chunk struct {
	BlockEntities  []nbt.RawMessage `nbt:"block_entities"`
	BlockTicks     nbt.RawMessage   `nbt:"block_ticks"`
	CarvingMasks   map[string][]uint64
	DataVersion    int32
	Entities       []nbt.RawMessage    `nbt:"entities"`
	FluidTicks     nbt.RawMessage      `nbt:"fluid_ticks"`
	Heightmaps     map[string][]uint64 // keys: "WORLD_SURFACE_WG", "WORLD_SURFACE", "WORLD_SURFACE_IGNORE_SNOW", "OCEAN_FLOOR_WG", "OCEAN_FLOOR", "MOTION_BLOCKING", "MOTION_BLOCKING_NO_LEAVES"
	InhabitedTime  int64
	IsLightOn      byte `nbt:"isLightOn"`
	LastUpdate     int64
	Lights         []nbt.RawMessage
	PostProcessing nbt.RawMessage
	Sections       []Section `nbt:"sections"`
	Status         string
	Structures     nbt.RawMessage `nbt:"structures"`
	XPos           int32          `nbt:"xPos"`
	YPos           int32          `nbt:"yPos"`
	ZPos           int32          `nbt:"zPos"`
}

type Section struct {
	Y           int8
	BlockStates PaletteContainer[BlockState] `nbt:"block_states"`
	Biomes      PaletteContainer[BiomeState] `nbt:"biomes"`
	SkyLight    []byte                       `nbt:"SkyLight,omitempty"`
	BlockLight  []byte                       `nbt:"BlockLight,omitempty"`
}

type PaletteContainer[T any] struct {
	Palette []T      `nbt:"palette"`
	Data    []uint64 `nbt:"data"`
}

type BlockState struct {
	Name       string
	Properties nbt.RawMessage
}

type BiomeState string

// Load read column data from []byte
func (c *Chunk) Load(data []byte) (err error) {
	var r io.Reader = bytes.NewReader(data[1:])

	switch data[0] {
	default:
		err = errors.New("unknown compression")
	case 1:
		r, err = gzip.NewReader(r)
	case 2:
		r, err = zlib.NewReader(r)
	case 3:
		// none compression
	}
	if err != nil {
		return err
	}

	d := nbt.NewDecoder(r)
	// d.DisallowUnknownFields()
	_, err = d.Decode(c)
	return
}

func (c *Chunk) Data(compressingType byte) ([]byte, error) {
	var buff bytes.Buffer

	buff.WriteByte(compressingType)
	var w io.Writer
	switch compressingType {
	default:
		return nil, errors.New("unknown compression")
	case 1:
		w = gzip.NewWriter(&buff)
	case 2:
		w = zlib.NewWriter(&buff)
	case 3:
		w = &buff
	}
	err := nbt.NewEncoder(w).Encode(c, "")
	return buff.Bytes(), err
}

type Entities struct {
	// ID is the entity-type registry key ("minecraft:pig", "minecraft:item", ...) -- Entity.save writes
	// it under "id" (EntityType.CODEC). Empty on a type-less/legacy record. SUB-PERSIST (Part C): added
	// so a saved entity can be reconstructed by type on chunk reload. CITE Entity.save (put "id").
	ID string `nbt:"id"`

	Pos, Motion  [3]float64
	Rotation     [2]float32
	FallDistance float32 `nbt:"fall_distance"`
	Fire, Air    int16

	OnGround       bool
	Invulnerable   bool
	PortalCooldown int32
	UUID           [4]int32

	CustomName        string
	CustomNameVisible bool
	Silent            bool
	NoGravity         bool
	Glowing           bool
	TicksFrozen       int32
	HasVisualFire     bool
	Tags              []string

	// Health is the "Health" tag, whose vanilla TAG TYPE depends on the entity kind writing it:
	// LivingEntity.addAdditionalSaveData writes it via putFloat (TAG_Float) -- a mob's health; ItemEntity
	// .addAdditionalSaveData writes it via putShort (TAG_Short) -- a dropped item's health. An entity is
	// EITHER living OR an item, so a record populates at most one form; *OptNum carries the value plus its
	// tag type so BOTH serialize with the vanilla-exact tag (float vs short). nil (omitempty) => no key,
	// byte-identical to a non-living/non-item record that never set health. CITE LivingEntity
	// .addAdditionalSaveData (putFloat "Health") / ItemEntity.addAdditionalSaveData (putShort "Health").
	Health *OptNum `nbt:"Health,omitempty"`

	// Item / Age / PickupDelay are net.minecraft.world.entity.item.ItemEntity.addAdditionalSaveData:
	// the carried ItemStack ("Item"), the ticks-since-spawn ("Age"), and the pickup cooldown
	// ("PickupDelay"). A non-item entity leaves Item nil (omitempty drops the key) and Age/PickupDelay 0.
	// CITE ItemEntity.addAdditionalSaveData (store "Item", putShort "Age", putShort "PickupDelay").
	Item *ItemStackDisk `nbt:"Item,omitempty"`
	// Age is the "Age" tag, whose vanilla TAG TYPE depends on the entity kind writing it: ItemEntity
	// .addAdditionalSaveData writes it via putShort (TAG_Short) -- the ticks-since-spawn (ItemEntity.age,
	// a short); AgeableMob.addAdditionalSaveData writes it via putInt (TAG_Int) -- the breeding age
	// (AgeableMob.getAge(), an int). An entity is EITHER an item OR an ageable mob, so the one key never
	// carries both; *OptNum carries the value plus its tag type so an item emits TAG_Short and a mob emits
	// TAG_Int -- matching each vanilla reader (item getShortOr / mob getIntOr). CITE
	// ItemEntity/AgeableMob.addAdditionalSaveData (putShort/putInt "Age").
	Age *OptNum `nbt:"Age,omitempty"`
	// PickupDelay is ItemEntity.addAdditionalSaveData "PickupDelay" via putShort (TAG_Short); int16 maps
	// to TAG_Short. Item-only, so no cross-kind tag conflict. CITE ItemEntity.addAdditionalSaveData.
	PickupDelay int16 `nbt:"PickupDelay,omitempty"`

	// --- MOB save contract (P0-01): the LivingEntity/Mob/AgeableMob/TamableAnimal/NeutralMob extra
	// tags a mob writes, so a reloaded mob reconstructs its full runtime (equipment, attributes,
	// effects, age/love/owner/anger, per-type variant), NOT just type+health. Each tag mirrors a
	// vanilla NBT key + type EXACTLY; every field is omitempty so a plain non-living entity (a dropped
	// item) emits none of them and the on-disk shape is byte-identical to the pre-P0-01 record. ---

	// AbsorptionAmount is LivingEntity.addAdditionalSaveData "AbsorptionAmount" (a float). CITE
	// LivingEntity.addAdditionalSaveData (putFloat "AbsorptionAmount").
	AbsorptionAmount float32 `nbt:"AbsorptionAmount,omitempty"`

	// Equipment is LivingEntity.addAdditionalSaveData "equipment" -- the EntityEquipment.CODEC
	// unbounded map keyed by the LOWERCASE EquipmentSlot name ("mainhand"/"offhand"/"feet"/"legs"/
	// "chest"/"head") -> ItemStack. Only non-empty slots are written (EntityEquipment.CODEC skips
	// EMPTY). CITE LivingEntity.addAdditionalSaveData (store "equipment", EntityEquipment.CODEC).
	Equipment map[string]ItemStackDisk `nbt:"equipment,omitempty"`

	// DropChances is Mob.addAdditionalSaveData "drop_chances" -- the per-slot drop probability map,
	// keyed by the same LOWERCASE slot name, written only for a slot whose chance differs from the
	// default. CITE Mob.addAdditionalSaveData (store "drop_chances").
	DropChances map[string]float32 `nbt:"drop_chances,omitempty"`

	// Attributes is LivingEntity.addAdditionalSaveData "attributes" -- the AttributeInstance list;
	// each entry carries the attribute id ("id") + its base ("base"). Only instances whose base
	// differs from the supplier default are written (AttributeMap.save). CITE LivingEntity
	// .addAdditionalSaveData (store "attributes", AttributeMap.save).
	Attributes []AttributeDisk `nbt:"attributes,omitempty"`

	// ActiveEffects is LivingEntity.addAdditionalSaveData "active_effects" -- the MobEffectInstance
	// list. CITE LivingEntity.addAdditionalSaveData (store "active_effects").
	ActiveEffects []MobEffectDisk `nbt:"active_effects,omitempty"`

	// PersistenceRequired / CanPickUpLoot / LeftHanded are Mob.addAdditionalSaveData booleans. CITE
	// Mob.addAdditionalSaveData (putBoolean "PersistenceRequired"/"CanPickUpLoot"/"LeftHanded").
	PersistenceRequired bool `nbt:"PersistenceRequired,omitempty"`
	CanPickUpLoot       bool `nbt:"CanPickUpLoot,omitempty"`
	LeftHanded          bool `nbt:"LeftHanded,omitempty"`

	// ForcedAge / AgeLocked are AgeableMob.addAdditionalSaveData "ForcedAge"/"AgeLocked" (the breeding
	// age machine; the AgeableMob "Age" itself shares the Age field above). CITE
	// AgeableMob.addAdditionalSaveData (putInt "ForcedAge", putBoolean "AgeLocked").
	ForcedAge int32 `nbt:"ForcedAge,omitempty"`
	AgeLocked bool  `nbt:"AgeLocked,omitempty"`

	// InLove is Animal.addAdditionalSaveData "InLove" (the love-mode countdown). CITE
	// Animal.addAdditionalSaveData (putInt "InLove").
	InLove int32 `nbt:"InLove,omitempty"`

	// Owner / Sitting are TamableAnimal.addAdditionalSaveData "Owner" (the owner UUID) + "Sitting".
	// CITE TamableAnimal.addAdditionalSaveData (store "Owner", putBoolean "Sitting").
	Owner   [4]int32 `nbt:"Owner,omitempty"`
	Sitting bool     `nbt:"Sitting,omitempty"`

	// AngerEndTime is NeutralMob.addPersistentAngerSaveData "anger_end_time" -- the persistent-anger
	// GAMETIME ENDPOINT (a Long: getPersistentAngerEndTime()), read back verbatim via
	// setPersistentAngerEndTime. The runtime carries the identical gametime endpoint (angerEndTime), so
	// it round-trips 1:1. The "angry_at" target UUID is a cited reduction (the runtime anger target is a
	// THIN entity id that does not survive a reload; the endpoint alone preserves the "is angry" window).
	// CITE NeutralMob.addPersistentAngerSaveData (putLong "anger_end_time").
	AngerEndTime int64 `nbt:"anger_end_time,omitempty"`

	// The per-type variant/state tags. Each is written only for the owning type (omitempty), so a mob
	// of a different type emits none. These mirror the finalizeSpawn results the load path must NOT
	// re-roll (load is RNG-free): SheepColor/Sheared (Sheep), Variant (the int-variant mobs: Cat/Fox/
	// Rabbit), CollarColor (Cat). CITE the per-type addAdditionalSaveData ("Color"/"Sheared"/"variant"/
	// "CollarColor").
	SheepColor  byte  `nbt:"Color,omitempty"`
	Sheared     bool  `nbt:"Sheared,omitempty"`
	Variant     int32 `nbt:"variant,omitempty"`
	CollarColor int32 `nbt:"CollarColor,omitempty"`
}

// AttributeDisk is one AttributeMap.save list entry: the attribute id + its base value. CITE
// AttributeInstance.save (putString "id", putDouble "base"); the modifier list is a cited deferral
// (the runtime AttributeInstance only round-trips the base override today).
type AttributeDisk struct {
	ID   string  `nbt:"id"`
	Base float64 `nbt:"base"`
}

// MobEffectDisk is one MobEffectInstance.save list entry (the subset the runtime activeEffect
// carries). CITE MobEffectInstance.save (putString "id", putByte "amplifier", putInt "duration",
// putBoolean "ambient"/"show_particles"/"show_icon").
type MobEffectDisk struct {
	ID            string `nbt:"id"`
	Amplifier     byte   `nbt:"amplifier,omitempty"`
	Duration      int32  `nbt:"duration,omitempty"`
	Ambient       bool   `nbt:"ambient,omitempty"`
	ShowParticles bool   `nbt:"show_particles,omitempty"`
	ShowIcon      bool   `nbt:"show_icon,omitempty"`
}
