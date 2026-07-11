package server

import (
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// player_persist_ext.go -- the persistence extension covering the player .dat fields written as
// write-only constants (or not at all) before: XP, abilities, active mob effects, spawn point,
// game type and dimension. Every field is a literal port of the jar save/load keys:
//   - XpP/XpLevel/XpTotal/XpSeed  -> Player.addAdditionalSaveData (experienceProgress/
//     experienceLevel/totalExperience/enchantmentSeed).
//   - abilities                   -> Player.addAdditionalSaveData (Abilities$Packed.CODEC), the
//     flags DERIVED from the game type as GameType.updatePlayerAbilities does (gameMode is the
//     single ability source).
//   - active_effects              -> LivingEntity.addAdditionalSaveData (MobEffectInstance.CODEC).
//   - respawn                     -> ServerPlayer.addAdditionalSaveData (RespawnConfig.CODEC).
//   - playerGameType/previousPlayerGameType -> ServerPlayerGameMode.storeGameTypes (LEGACY_ID_CODEC).
//   - Dimension                   -> ServerPlayer.addAdditionalSaveData (putString Dimension).

// abilitiesForGameType is the 1:1 port of GameType.updatePlayerAbilities: the Abilities flag set is
// derived from the game type (gameMode is the servers sole authoritative player-ability source, so
// the save derives the compound as vanilla re-derives it on a mode change). flySpeed/walkSpeed keep
// the Abilities ctor defaults. CITE GameType.updatePlayerAbilities + GameType.isBlockPlacingRestricted.
//
//	VERIFIED javap GameType.updatePlayerAbilities(Abilities):
//	 CREATIVE  -> mayfly=1, instabuild=1, invulnerable=1 (flying stays 0);
//	 SPECTATOR -> mayfly=1, instabuild=0, invulnerable=1, flying=1;
//	 else      -> all four 0;
//	 mayBuild = not isBlockPlacingRestricted == not (ADVENTURE or SPECTATOR).
func abilitiesForGameType(gameMode int32) (invulnerable, flying, mayfly, instabuild, mayBuild bool) {
	switch gameMode {
	case gameModeCreative:
		mayfly, instabuild, invulnerable = true, true, true
	case gameModeSpectator:
		mayfly, invulnerable, flying = true, true, true
	default: // survival / adventure: all four stay false
	}
	mayBuild = !(gameMode == gameModeAdventure || gameMode == gameModeSpectator)
	return
}

// snapshotPlayerExtras fills the XP, abilities, active-effects, spawn, game-type and dimension fields
// of a save.PlayerData snapshot from the live (owner-goroutine) tickPlayer. Called by snapshotPlayer
// AFTER the base fields are copied, keeping the whole snapshot an immutable value taken on the owner
// (TICK-05 / T-6-15). Every read is a plain value copy or a fresh slice.
func snapshotPlayerExtras(data *save.PlayerData, p *tickPlayer) {
	data.Dimension = dimensionName(p.dimension)

	data.XpP = p.experienceProgress
	data.XpLevel = p.experienceLevel
	data.XpTotal = p.totalExperience
	data.XpSeed = p.enchantmentSeed

	// playerGameType + previousPlayerGameType (ServerPlayerGameMode.storeGameTypes). The server models
	// only the current gameMode; previous defaults to the same value (a faithful stub).
	data.PlayerGameType = p.gameMode
	data.PreviousPlayerGameType = p.gameMode

	invuln, flying, mayfly, instabuild, mayBuild := abilitiesForGameType(p.gameMode)
	data.Abilities.Invulnerable = boolToByte(invuln)
	data.Abilities.Flying = boolToByte(flying)
	data.Abilities.MayFly = boolToByte(mayfly)
	data.Abilities.InstantBuild = boolToByte(instabuild)
	data.Abilities.MayBuild = boolToByte(mayBuild)
	data.Abilities.FlySpeed = defaultFlyingSpeed
	data.Abilities.WalkSpeed = defaultWalkingSpeed

	if len(p.activeEffects) > 0 {
		effects := make([]save.MobEffectInstanceDisk, 0, len(p.activeEffects))
		for _, e := range p.activeEffects {
			effects = append(effects, activeEffectToDisk(e))
		}
		data.ActiveEffects = effects
	}

	if p.respawnPos != nil {
		data.Respawn = &save.RespawnConfigDisk{
			Dimension: dimensionName(p.respawnDimension),
			Pos:       [3]int32{int32(p.respawnPos.X), int32(p.respawnPos.Y), int32(p.respawnPos.Z)},
			Yaw:       p.respawnYaw,
			Pitch:     p.respawnPitch,
			Forced:    p.respawnForced,
		}
	}

	// attributes: LivingEntity.addAdditionalSaveData store("attributes", ..., getAttributes().pack()).
	// pack() emits one AttributeInstance$Packed per attribute instance (its base value + modifier list);
	// Ender packs the per-player attribute holder the same way. Nil when the holder is unallocated (a
	// player that never touched the combat path keeps all-default attributes -> pack() would just be the
	// supplier defaults; we only persist when the holder exists so an untouched save stays minimal).
	if p.attributes != nil {
		data.Attributes = packPlayerAttributes(p.attributes)
	}
}

// packPlayerAttributes is the AttributeMap.pack() port: it emits an AttributeInstance$Packed for each
// attribute the holder carries (its base value + its modifier list). The id is the vanilla ATTRIBUTE
// registry name; a key with no registry name (never modifier-touched, e.g. mining_efficiency) is
// skipped so we never write an unresolvable holder id. Modifiers carry the StringRepresentable
// operation name (add_value / add_multiplied_base / add_multiplied_total).
func packPlayerAttributes(h *attributeHolder) []save.AttributePacked {
	if h == nil || len(h.base) == 0 {
		return nil
	}
	out := make([]save.AttributePacked, 0, len(h.base))
	for attr, base := range h.base {
		name := attributeKeyName[attr]
		if name == "" {
			continue
		}
		packed := save.AttributePacked{ID: name, Base: base}
		if bucket := h.modifiers[attr]; len(bucket) > 0 {
			mods := make([]save.AttributeModifierDisk, 0, len(bucket))
			for _, m := range bucket {
				mods = append(mods, save.AttributeModifierDisk{
					ID:        m.ID,
					Amount:    m.Amount,
					Operation: operationName(m.Operation),
				})
			}
			packed.Modifiers = mods
		}
		out = append(out, packed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// operationName maps the ported Operation enum to the vanilla StringRepresentable serialized name
// (AttributeModifier$Operation: ADD_VALUE="add_value", ADD_MULTIPLIED_BASE="add_multiplied_base",
// ADD_MULTIPLIED_TOTAL="add_multiplied_total"). VERIFIED via javap Operation.<clinit>.
func operationName(op attribute.Operation) string {
	switch op {
	case attribute.AddMultipliedBase:
		return "add_multiplied_base"
	case attribute.AddMultipliedTotal:
		return "add_multiplied_total"
	default:
		return "add_value"
	}
}

// operationFromName is the inverse of operationName (a corrupt/unknown name -> ADD_VALUE, the id-0
// default).
func operationFromName(name string) attribute.Operation {
	switch name {
	case "add_multiplied_base":
		return attribute.AddMultipliedBase
	case "add_multiplied_total":
		return attribute.AddMultipliedTotal
	default:
		return attribute.AddValue
	}
}

// attributeKeyByName is the reverse of attributeKeyName, built once, for resolving a loaded
// "attributes" element id back to the holder key.
var attributeKeyByName = func() map[string]attributeKey {
	m := make(map[string]attributeKey, len(attributeKeyName))
	for k, name := range attributeKeyName {
		if name != "" {
			m[name] = k
		}
	}
	return m
}()

// applyLoadedPlayerAttributes is the AttributeMap.apply(List<Packed>) port: it restores each saved
// attribute's base value and modifier set onto the player's holder. Unknown attribute ids (not in the
// Ender holder) are skipped (a corrupt/forward save never injects an unmodeled attribute). Called from
// applyLoadedPlayerExtras.
func applyLoadedPlayerAttributes(p *tickPlayer, packed []save.AttributePacked) {
	if len(packed) == 0 {
		return
	}
	h := p.playerAttributes()
	for _, a := range packed {
		attr, ok := attributeKeyByName[a.ID]
		if !ok {
			continue
		}
		h.base[attr] = a.Base
		for _, md := range a.Modifiers {
			h.addModifier(attr, attribute.AttributeModifier{
				ID:        md.ID,
				Amount:    md.Amount,
				Operation: operationFromName(md.Operation),
			})
		}
	}
}

// activeEffectToDisk converts one live activeEffect into its MobEffectInstance.CODEC disk form,
// recursing on the hidden lower-priority effect (MobEffectInstance$Details.hiddenEffect).
func activeEffectToDisk(e *activeEffect) save.MobEffectInstanceDisk {
	d := save.MobEffectInstanceDisk{
		ID:            e.id,
		Amplifier:     int32(e.amplifier),
		Duration:      int32(e.duration),
		Ambient:       e.ambient,
		ShowParticles: e.visible,
		ShowIcon:      e.showIcon,
	}
	if e.hidden != nil {
		h := activeEffectToDisk(e.hidden)
		d.HiddenEffect = &h
	}
	return d
}

// diskToActiveEffect is the load inverse: rebuild a live activeEffect from a MobEffectInstanceDisk,
// recursing on hidden_effect. The amplifier is clamped to [0,255] exactly as newActiveEffect does.
func diskToActiveEffect(d save.MobEffectInstanceDisk) *activeEffect {
	amp := int(d.Amplifier)
	if amp < 0 {
		amp = 0
	}
	if amp > 255 {
		amp = 255
	}
	e := &activeEffect{
		id:        d.ID,
		duration:  int(d.Duration),
		amplifier: amp,
		ambient:   d.Ambient,
		visible:   d.ShowParticles,
		showIcon:  d.ShowIcon,
	}
	if d.HiddenEffect != nil {
		e.hidden = diskToActiveEffect(*d.HiddenEffect)
	}
	return e
}

// applyLoadedPlayerExtras restores the XP, abilities-source (gameMode), active effects, spawn point
// and dimension from a loaded save.PlayerData onto a freshly-built tickPlayer (accept goroutine,
// before register). gameMode is restored directly (the sole ability source); the ability gates read
// it at use time and the join bootstrap already sent the abilities packet from it.
func applyLoadedPlayerExtras(p *tickPlayer, loaded save.PlayerData) {
	p.experienceProgress = loaded.XpP
	p.experienceLevel = loaded.XpLevel
	p.totalExperience = loaded.XpTotal
	p.enchantmentSeed = loaded.XpSeed

	p.gameMode = loaded.PlayerGameType
	// Dimension: the join bootstrap always builds an OVERWORLD Login, so p.dimension stays overworld
	// here and the actual dimension placement rides the existing changeDimension path once the player
	// is registered (drainRegistrations reads joinDimension). This mirrors PlayerList.placeNewPlayer,
	// which places a loaded player into its persisted level via a Respawn. A non-overworld save thus
	// round-trips (data faithful) AND lands the client in the correct dimension with a proper rebuild.
	p.joinDimension = dimensionIndex(loaded.Dimension)

	if loaded.Respawn != nil {
		pos := pkPositionFromInts(loaded.Respawn.Pos)
		p.respawnPos = &pos
		p.respawnDimension = dimensionIndex(loaded.Respawn.Dimension)
		p.respawnYaw = loaded.Respawn.Yaw
		p.respawnPitch = loaded.Respawn.Pitch
		p.respawnForced = loaded.Respawn.Forced
	}

	if len(loaded.ActiveEffects) > 0 {
		p.activeEffects = make(map[string]*activeEffect, len(loaded.ActiveEffects))
		for _, d := range loaded.ActiveEffects {
			if d.ID == "" {
				continue
			}
			p.activeEffects[d.ID] = diskToActiveEffect(d)
		}
	}

	applyLoadedPlayerAttributes(p, loaded.Attributes)
}

// dimensionName maps the internal dimension index to its level ResourceKey (the ServerPlayer
// "Dimension" / GlobalPos "dimension" disk string). Overworld is the default for any unknown index.
func dimensionName(dim int) string {
	switch dim {
	case dimNether:
		return netherDimensionName
	case dimEnd:
		return endDimensionName
	default:
		return overworldDimensionName
	}
}

// dimensionIndex is the inverse of dimensionName: a level ResourceKey string back to the internal
// dimension index. An empty/unknown key resolves to the overworld (never inject an invalid dimension
// from a corrupt or pre-dimension save).
func dimensionIndex(name string) int {
	switch name {
	case netherDimensionName:
		return dimNether
	case endDimensionName:
		return dimEnd
	default:
		return dimOverworld
	}
}

// boolToByte encodes a Go bool as the 0/1 NBT byte the Abilities$Packed CODEC (and every other
// boolean-as-byte disk field) uses.
func boolToByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// pkPositionFromInts rebuilds a pk.Position from a saved [x,y,z] int-array (the BlockPos.CODEC disk
// form used by the respawn GlobalPos pos).
func pkPositionFromInts(p [3]int32) pk.Position {
	return pk.Position{X: int(p[0]), Y: int(p[1]), Z: int(p[2])}
}
