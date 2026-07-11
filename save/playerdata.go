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

	Attributes []struct {
		Base float64
		Name string
	}

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
	ID            string                 `nbt:"id"`
	Amplifier     int32                  `nbt:"amplifier"`
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
