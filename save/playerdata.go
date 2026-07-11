package save

import (
	"io"

	"github.com/imhinotori/sulfur/nbt"
)

type PlayerData struct {
	DataVersion int32

	Dimension    string
	Pos          [3]float64
	Motion       [3]float64
	Rotation     [2]float32
	FallDistance float32
	FallFlying   byte `nbt:"FallFlying"`
	OnGround     byte

	UUID [4]int32

	PlayerGameType         int32 `nbt:"playerGameType"`
	PreviousPlayerGameType int32 `nbt:"previousPlayerGameType"`
	Air                    int16
	DeathTime              int16
	Fire                   int16
	HurtTime               int16
	Health                 float32
	HurtByTimestamp        int32
	PortalCooldown         int32

	Invulnerable     byte
	SeenCredits      byte `nbt:"seenCredits"`
	SelectedItemSlot int32
	Score            int32
	AbsorptionAmount float32

	// Inventory / EnderItems are the modern (post-1.20.5) ItemStackWithSlot lists: each element is
	// {Slot byte, id, count(, components)} flattened. SUB-PERSIST: upgraded from the legacy
	// []Item{Count,Slot,id,Tag} (pre-1.20.5) to the 26.2 codec (ItemStackWithSlotDisk) so the
	// player .dat matches vanilla. CITE: net.minecraft.world.entity.player.Inventory.save emits
	// ItemStackWithSlot elements (the SAME codec ContainerHelper.saveAllItems uses).
	Inventory, EnderItems []ItemStackWithSlotDisk

	XpLevel int32
	XpP     float32
	XpTotal int32
	XpSeed  int32

	FoodExhaustionLevel float32 `nbt:"foodExhaustionLevel"`
	FoodLevel           int32   `nbt:"foodLevel"`
	FoodSaturationLevel float32 `nbt:"foodSaturationLevel"`
	FoodTickTimer       int32   `nbt:"foodTickTimer"`

	// ActiveEffects is LivingEntity.addAdditionalSaveData's "active_effects" list (the
	// MobEffectInstance CODEC list). Each element carries the effect Holder id + the
	// MobEffectInstance$Details fields (amplifier/duration/ambient/show_particles/show_icon +
	// optional hidden_effect). CITE net.minecraft.world.entity.LivingEntity.addAdditionalSaveData
	// (store("active_effects", MobEffectInstance.CODEC.listOf(), activeEffects.values())) +
	// MobEffectInstance.CODEC / MobEffectInstance$Details.MAP_CODEC.
	ActiveEffects []MobEffectInstanceDisk `nbt:"active_effects,omitempty"`

	// Respawn is ServerPlayer.addAdditionalSaveData's "respawn" compound (the modern spawn-point
	// record replacing the pre-26 SpawnX/SpawnY/SpawnZ/SpawnAngle/SpawnDimension keys). It wraps
	// LevelData$RespawnData{GlobalPos{dimension,pos}, yaw, pitch} + a `forced` boolean. Optional
	// (a player with no bed/anchor spawn writes no key -> nil, decodes to the world default). CITE
	// ServerPlayer.addAdditionalSaveData (storeNullable("respawn", RespawnConfig.CODEC, respawnConfig))
	// + ServerPlayer$RespawnConfig.CODEC + LevelData$RespawnData.MAP_CODEC + GlobalPos.CODEC.
	Respawn *RespawnConfigDisk `nbt:"respawn,omitempty"`

	// Attributes is LivingEntity.addAdditionalSaveData's "attributes" list -- the
	// AttributeInstance$Packed.LIST_CODEC (store("attributes", ..., getAttributes().pack())). Each
	// element is {id (Holder registry string), base (double), modifiers (list of {id, amount,
	// operation})}. SUB-PERSIST: replaced the DEAD pre-1.20.5 []struct{Base;Name} shape (never
	// written) with the real 26.2 codec. omitempty so an all-default entity writes no key. CITE
	// net.minecraft.world.entity.LivingEntity.addAdditionalSaveData (store "attributes",
	// AttributeInstance$Packed.LIST_CODEC, AttributeMap.pack()) + AttributeInstance$Packed.CODEC.
	Attributes []AttributePacked `nbt:"attributes,omitempty"`

	Abilities struct {
		FlySpeed     float32 `nbt:"flySpeed"`
		WalkSpeed    float32 `nbt:"walkSpeed"`
		Flying       byte    `nbt:"flying"`
		InstantBuild byte    `nbt:"instabuild"`
		Invulnerable byte    `nbt:"invulnerable"`
		MayBuild     byte    `nbt:"mayBuild"`
		MayFly       byte    `nbt:"mayfly"`
	} `nbt:"abilities"`

	RecipeBook struct {
		IsFilteringCraftable        byte `nbt:"isFilteringCraftable"`
		IsFurnaceFilteringCraftable byte `nbt:"isFurnaceFilteringCraftable"`
		IsFurnaceGUIOpen            byte `nbt:"isFurnaceGuiOpen"`
		IsGUIOpen                   byte `nbt:"isGuiOpen"`
	} `nbt:"recipeBook"`
}

// MobEffectInstanceDisk is the disk form of one net.minecraft.world.effect.MobEffectInstance as its
// CODEC serializes it (LivingEntity "active_effects" list element). ID is the effect Holder id (a
// namespaced registry string, "minecraft:<name>"); the remaining fields are MobEffectInstance$Details
// (amplifier/duration/ambient/show_particles/show_icon + optional recursive hidden_effect). The Details
// keys are OPTIONAL in the codec (amplifier default 0, duration default 0, ambient default false,
// show_particles default true, show_icon default = show_particles) but SUB-PERSIST always writes them
// (the encoder emits present fields; the decoder tolerates absent ones). CITE MobEffectInstance.CODEC
// ("id" fieldOf the effect Holder) + MobEffectInstance$Details.MAP_CODEC (amplifier/duration/ambient/
// show_particles/show_icon/hidden_effect).
type MobEffectInstanceDisk struct {
	ID        string `nbt:"id"`
	Amplifier int32  `nbt:"amplifier"`
	// Duration is a PLAIN int in 26.2. VERIFIED via javap MobEffectInstance$Details static
	// initializer: the "duration" field is Codec.INT.optionalFieldOf("duration", 0) -- there is NO
	// "infinite" codec alternative / Either / xmap in 26.2. The INFINITE_DURATION sentinel (-1) is
	// therefore written LITERALLY as the int -1 (no special-casing), which is exactly what this int32
	// does. (A pre-26 schema used a separate infinite-variant; 26.2 does not.)
	Duration      int32                  `nbt:"duration"`
	Ambient       bool                   `nbt:"ambient"`
	ShowParticles bool                   `nbt:"show_particles"`
	ShowIcon      bool                   `nbt:"show_icon"`
	HiddenEffect  *MobEffectInstanceDisk `nbt:"hidden_effect,omitempty"`
}

// RespawnConfigDisk is the disk form of ServerPlayer$RespawnConfig: the RespawnData record (a GlobalPos
// {dimension, pos} + yaw + pitch) plus a `forced` boolean. Pos is a BlockPos serialized by its codec as
// a 3-int list [x,y,z] (BlockPos.CODEC). CITE ServerPlayer$RespawnConfig.CODEC (RespawnData.MAP_CODEC
// forGetter respawnData + BOOL "forced") + LevelData$RespawnData.MAP_CODEC (GlobalPos.CODEC + "yaw"/
// "pitch") + GlobalPos.CODEC ("dimension"/"pos").
type RespawnConfigDisk struct {
	// The RespawnData record is inlined (RecordCodecBuilder flattens RespawnData.MAP_CODEC into the
	// RespawnConfig compound alongside `forced`), so its fields sit at the RespawnConfig level: the
	// GlobalPos (dimension + pos) and the yaw/pitch. `forced` is the RespawnConfig-level boolean.
	Dimension string   `nbt:"dimension"`
	Pos       [3]int32 `nbt:"pos"`
	Yaw       float32  `nbt:"yaw"`
	Pitch     float32  `nbt:"pitch"`
	Forced    bool     `nbt:"forced"`
}

// AttributePacked is the disk form of one net.minecraft.world.entity.ai.attributes.AttributeInstance
// as AttributeInstance$Packed.CODEC serializes it (the "attributes" list element). VERIFIED via javap
// AttributeInstance$Packed static initializer:
//   - id        : Codec fieldOf "id"        -- the Attribute Holder registry name ("minecraft:max_health")
//   - base      : Codec.DOUBLE optionalAlwaysPresentFieldOf "base" (default 0.0) -- ALWAYS written
//   - modifiers : AttributeModifier.CODEC.listOf() optionalFieldOf "modifiers" (default empty list)
//
// Modifiers is omitempty so a modifier-free attribute writes no "modifiers" key (matches the
// optionalFieldOf(empty) encode: an empty list is dropped).
type AttributePacked struct {
	ID        string                  `nbt:"id"`
	Base      float64                 `nbt:"base"`
	Modifiers []AttributeModifierDisk `nbt:"modifiers,omitempty"`
}

// AttributeModifierDisk is the disk form of net.minecraft.world.entity.ai.attributes.AttributeModifier
// (the record {Identifier id, double amount, Operation operation}) as AttributeModifier.CODEC
// serializes it. VERIFIED via javap AttributeModifier / Operation static initializers:
//   - id        : Codec fieldOf "id"        -- the modifier's stable Identifier string
//   - amount    : Codec.DOUBLE fieldOf "amount"
//   - operation : Operation.CODEC fieldOf "operation" -- StringRepresentable serialized name
//     (add_value / add_multiplied_base / add_multiplied_total)
type AttributeModifierDisk struct {
	ID        string  `nbt:"id"`
	Amount    float64 `nbt:"amount"`
	Operation string  `nbt:"operation"`
}

type Item struct {
	Count byte
	Slot  byte
	ID    string         `nbt:"id"`
	Tag   map[string]any `nbt:"tag"`
}

func ReadPlayerData(r io.Reader) (data PlayerData, err error) {
	_, err = nbt.NewDecoder(r).Decode(&data)
	return
}
