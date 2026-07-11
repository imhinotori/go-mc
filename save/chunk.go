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

	// Health is LivingEntity.addAdditionalSaveData "Health" (a float). Zero for a non-living entity
	// (a dropped item overrides it below with its own Health key -- the ItemEntity also stores Health).
	// omitempty so a 0 health record (a non-living entity that never set it) emits no key. CITE
	// LivingEntity.addAdditionalSaveData (putFloat "Health").
	Health float32 `nbt:"Health,omitempty"`

	// Item / Age / PickupDelay are net.minecraft.world.entity.item.ItemEntity.addAdditionalSaveData:
	// the carried ItemStack ("Item"), the ticks-since-spawn ("Age"), and the pickup cooldown
	// ("PickupDelay"). A non-item entity leaves Item nil (omitempty drops the key) and Age/PickupDelay 0.
	// CITE ItemEntity.addAdditionalSaveData (store "Item", putShort "Age", putShort "PickupDelay").
	Item        *ItemStackDisk `nbt:"Item,omitempty"`
	Age         int16          `nbt:"Age,omitempty"`
	PickupDelay int16          `nbt:"PickupDelay,omitempty"`
}
