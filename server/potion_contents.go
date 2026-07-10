package server

// potion_contents.go — the 1:1 port of the DRINK-a-potion subsystem: the built-in Potions effect
// table (net.minecraft.world.item.alchemy.Potions static{}) plus the PotionContents.onConsume ->
// applyToLivingEntity -> forEachEffect chain that applies each effect to the drinker on finish.
//
// Cited bytecode (FQCN.method), verified via `javap -c -p` on temp/cache/26.2-inner.jar this session:
//
//	net.minecraft.world.item.alchemy.Potions static{}: each `register(key, new Potion(name,
//	    new MobEffectInstance[]{...}))` in registry order. The register order defines the POTION
//	    registry index (matches data/registryid/potion.go). Every entry below transcribes the exact
//	    MobEffect holder + the MobEffectInstance ctor ints from the static block:
//	      ctor (Holder, I)   -> (effect, duration)          amplifier defaults 0
//	      ctor (Holder, I, I) -> (effect, duration, amplifier)
//	    For instantaneous effects (INSTANT_HEALTH/INSTANT_DAMAGE) the duration int is ignored at apply
//	    time (applyInstantaneousEffect is one-shot) — kept here for fidelity to the ctor args.
//	net.minecraft.world.item.alchemy.PotionContents.onConsume(Level, LivingEntity, ItemStack, Consumable):
//	    applyToLivingEntity(entity, stack.getOrDefault(POTION_DURATION_SCALE, 1.0f)).
//	net.minecraft.world.item.alchemy.PotionContents.applyToLivingEntity(LivingEntity, float scale):
//	    if (!(level instanceof ServerLevel)) return; forEachEffect(effect -> { if (effect.getEffect()
//	    .value().isInstantaneous()) effect.getEffect().value().applyInstantaneousEffect(level, null,
//	    player, entity, effect.getAmplifier(), 1.0); else entity.addEffect(effect); }, scale).
//	net.minecraft.world.item.alchemy.PotionContents.forEachEffect(Consumer, float scale):
//	    if (potion.isPresent()) for (effect : potion.value().getEffects()) consumer.accept(effect
//	    .withScaledDuration(scale)); for (effect : customEffects) consumer.accept(effect
//	    .withScaledDuration(scale)).
//	net.minecraft.world.effect.MobEffectInstance.withScaledDuration(float scale): duration' =
//	    isInfiniteDuration()||duration==0 ? duration : max(1, floor(duration * scale)). Default
//	    POTION_DURATION_SCALE is 1.0f, so duration' == duration (max(1, floor(d)) == d for d>=1).
//
// The apply path reuses the existing addPlayerEffect seam (server/mob_effect.go): an instantaneous
// effect (instant_health/instant_damage) is applied one-shot with scale 1.0 (matching the `1.0` double
// arg the applyToLivingEntity lambda passes to applyInstantaneousEffect); a duration effect is inserted
// into the player's effect map with the (scaled) duration + amplifier. ownerID is 0 (a self-drink has no
// attacker attribution — the lambda passes player as both cause and indirect-cause, but v1 magic damage
// self-attributes identically).
//
// Per-type-gated: applyPotionContents is only ever called from the potion branch of finishUsingItem
// (item_use.go), so a non-potion item never enters here. A pig drinks nothing — the pig oracle
// (TestPluginPigEqualsGoNativePig) is byte-identical (no shared RNG draw is added to any AI path).

import (
	"bytes"

	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// potionEffect is one MobEffectInstance from a Potion's effect array: the effect id (registry name),
// its base duration in ticks, and 0-based amplifier — the transcribed ctor args from the Potions
// static block.
type potionEffect struct {
	id        string
	duration  int
	amplifier int
}

// potionEffects is the port of net.minecraft.world.item.alchemy.Potions static{}: index == the POTION
// registry index (data/registryid/potion.go), value == the Potion's MobEffectInstance[] transcribed
// from the bytecode. Empty slices are the "base" potions (WATER/MUNDANE/THICK/AWKWARD) that carry no
// effect. 45 entries, matching len(registryid.Potion).
var potionEffects = [][]potionEffect{
	/* 0  water              */ nil,
	/* 1  mundane            */ nil,
	/* 2  thick              */ nil,
	/* 3  awkward            */ nil,
	/* 4  night_vision       */ {{effectNightVision, 3600, 0}},
	/* 5  long_night_vision  */ {{effectNightVision, 9600, 0}},
	/* 6  invisibility       */ {{effectInvisibility, 3600, 0}},
	/* 7  long_invisibility  */ {{effectInvisibility, 9600, 0}},
	/* 8  leaping            */ {{effectJumpBoost, 3600, 0}},
	/* 9  long_leaping       */ {{effectJumpBoost, 9600, 0}},
	/* 10 strong_leaping     */ {{effectJumpBoost, 1800, 1}},
	/* 11 fire_resistance    */ {{effectFireResistance, 3600, 0}},
	/* 12 long_fire_resistance*/ {{effectFireResistance, 9600, 0}},
	/* 13 swiftness          */ {{effectSpeed, 3600, 0}},
	/* 14 long_swiftness     */ {{effectSpeed, 9600, 0}},
	/* 15 strong_swiftness   */ {{effectSpeed, 1800, 1}},
	/* 16 slowness           */ {{effectSlowness, 1800, 0}},
	/* 17 long_slowness      */ {{effectSlowness, 4800, 0}},
	/* 18 strong_slowness    */ {{effectSlowness, 400, 3}},
	/* 19 turtle_master      */ {{effectSlowness, 400, 3}, {effectResistance, 400, 2}},
	/* 20 long_turtle_master */ {{effectSlowness, 800, 3}, {effectResistance, 800, 2}},
	/* 21 strong_turtle_master*/ {{effectSlowness, 400, 5}, {effectResistance, 400, 3}},
	/* 22 water_breathing    */ {{effectWaterBreathing, 3600, 0}},
	/* 23 long_water_breathing*/ {{effectWaterBreathing, 9600, 0}},
	/* 24 healing            */ {{effectInstantHealth, 1, 0}},
	/* 25 strong_healing     */ {{effectInstantHealth, 1, 1}},
	/* 26 harming            */ {{effectInstantDamage, 1, 0}},
	/* 27 strong_harming     */ {{effectInstantDamage, 1, 1}},
	/* 28 poison             */ {{effectPoison, 900, 0}},
	/* 29 long_poison        */ {{effectPoison, 1800, 0}},
	/* 30 strong_poison      */ {{effectPoison, 432, 1}},
	/* 31 regeneration       */ {{effectRegeneration, 900, 0}},
	/* 32 long_regeneration  */ {{effectRegeneration, 1800, 0}},
	/* 33 strong_regeneration*/ {{effectRegeneration, 450, 1}},
	/* 34 strength           */ {{effectStrength, 3600, 0}},
	/* 35 long_strength      */ {{effectStrength, 9600, 0}},
	/* 36 strong_strength    */ {{effectStrength, 1800, 1}},
	/* 37 weakness           */ {{effectWeakness, 1800, 0}},
	/* 38 long_weakness      */ {{effectWeakness, 4800, 0}},
	/* 39 luck               */ {{effectLuck, 6000, 0}},
	/* 40 slow_falling       */ {{effectSlowFalling, 1800, 0}},
	/* 41 long_slow_falling  */ {{effectSlowFalling, 4800, 0}},
	/* 42 wind_charged       */ {{effectWindCharged, 3600, 0}},
	/* 43 weaving            */ {{effectWeaving, 3600, 0}},
	/* 44 oozing             */ {{effectOozing, 3600, 0}},
	/* 45 infested           */ {{effectInfested, 3600, 0}},
}

// potionDurationScaleComponentID is the wire type id of minecraft:potion_duration_scale
// (data/registryid/datacomponenttype.go index 52; component.NewComponent(52) -> *PotionDurationScale).
// PotionContents.onConsume reads stack.getOrDefault(POTION_DURATION_SCALE, 1.0f).
const potionDurationScaleComponentID = 52

// isPotionItem reports whether an item id is the drinkable minecraft:potion (the DRINK path). splash /
// lingering potions are THROWN (a separate ThrownSplashPotion/ThrownLingeringPotion entity path — CITE-
// DEFERRED, not drinking), so only the plain potion routes through the consume/finish chain here.
func isPotionItem(itemID int32) bool {
	return int(itemID) == itemID_("minecraft:potion")
}

// itemID_ is a small wrapper over itemID (enchant_helper.go) for clarity at the call site.
func itemID_(name string) int { return itemID(name) }

// glassBottleResult builds the survival finish-use result of a drunk potion: a fresh count-1
// minecraft:glass_bottle with no components. This is ItemStack.applyAfterUseComponentSideEffects ->
// UseRemainder.convertIntoRemainder for the potion's USE_REMAINDER=glass_bottle component: since a
// potion has max stack size 1, ItemStack.consume(1) always empties the stack (count 1 -> 0), so
// convertIntoRemainder's `stack.isEmpty()` branch returns the glass_bottle template directly (the
// count>1 onExtra branch is unreachable for a stack-size-1 item). VERIFIED PotionItem.finishUsingItem
// chain -> Item.finishUsingItem -> Consumable.onConsume (consume 1) then ItemStack.finishUsingItem's
// applyAfterUseComponentSideEffects -> UseRemainder.convertIntoRemainder.
func glassBottleResult() component.SlotData {
	return component.SlotData{Count: 1, ItemID: pk.VarInt(itemID("minecraft:glass_bottle"))}
}

// applyPotionContents is the port of PotionContents.onConsume -> applyToLivingEntity -> forEachEffect
// for a drinking PLAYER: read the stack's POTION_CONTENTS (the potion registry id + custom_effects) and
// the POTION_DURATION_SCALE (default 1.0), then apply — in vanilla order — first the potion's built-in
// effects (potion.getEffects()), then the custom_effects, each with its duration scaled. Each apply
// routes through addPlayerEffect: an instantaneous effect (isInstantaneous()) applies one-shot with
// scale 1.0; a duration effect is inserted with its (scaled) duration + amplifier. Tick-owned (called
// only from finishUsingItem on the tick goroutine).
func (t *TickLoop) applyPotionContents(p *tickPlayer, stack component.SlotData) {
	if p == nil {
		return
	}
	scale := t.potionDurationScale(stack)

	// forEachEffect: the potion's built-in effects first (potion.value().getEffects()).
	if pid, ok := readBottlePotion(stack); ok && int(pid) >= 0 && int(pid) < len(potionEffects) {
		for _, e := range potionEffects[pid] {
			t.applyScaledPotionEffect(p, e.id, e.duration, e.amplifier, scale)
		}
	}

	// forEachEffect tail: the component's custom_effects (PotionContents.customEffects). Each carries a
	// MobEffect registry index (ItemPotionEffect.ID) + details (amplifier, duration). withScaledDuration
	// scales the duration just like the built-in effects.
	for _, ce := range readCustomEffects(stack) {
		t.applyScaledPotionEffect(p, ce.id, ce.duration, ce.amplifier, scale)
	}
}

// applyScaledPotionEffect applies one potion effect through addPlayerEffect after the withScaledDuration
// transform (duration' = max(1, floor(duration*scale)) for a finite non-zero duration). An
// instantaneous effect (instant_health/instant_damage) ignores duration and applies once with scale 1.0
// (the `1.0` double the applyToLivingEntity lambda passes to applyInstantaneousEffect); a duration
// effect is inserted with the scaled duration. ownerID 0: a self-drink has no external attacker.
func (t *TickLoop) applyScaledPotionEffect(p *tickPlayer, id string, duration, amplifier int, scale float32) {
	dur := scalePotionDuration(duration, scale)
	// addPlayerEffect dispatches instant vs duration internally (isInstantEffect); the scale arg it takes
	// is the instantaneous magnitude scale, which vanilla passes as 1.0 for a drink (only the SPLASH path
	// proximity-scales the magnitude), so instant potions heal/harm the full 4<<amp / 6<<amp.
	t.addPlayerEffect(p, 0, id, dur, amplifier, 1.0)
}

// scalePotionDuration ports MobEffectInstance.withScaledDuration's mapDuration lambda: for a finite,
// non-zero base duration return max(1, floor(duration * scale)); an infinite (-1) or zero duration is
// returned unchanged. With the default scale 1.0 this is the identity for a positive duration.
func scalePotionDuration(duration int, scale float32) int {
	if duration == -1 || duration == 0 {
		return duration
	}
	// (int)(duration * scale) via Mth.floor(float), then Math.max(1, ...).
	scaled := int(float32(duration) * scale) // f2i truncates toward zero == floor for a non-negative product
	if scaled < 1 {
		scaled = 1
	}
	return scaled
}

// potionDurationScale ports stack.getOrDefault(DataComponents.POTION_DURATION_SCALE, 1.0f): read the
// component off the stack if present, else the vanilla default 1.0f. Structured as a real component
// read so a bottle carrying a non-default scale (a dispenser/loot potion) is honored later.
func (t *TickLoop) potionDurationScale(stack component.SlotData) float32 {
	const defaultScale float32 = 1.0
	if stackEmpty(stack) || len(stack.RawComponents) == 0 || int(stack.AddedCount) <= 0 {
		return defaultScale
	}
	r := bytes.NewReader(stack.RawComponents)
	for i := int32(0); i < int32(stack.AddedCount); i++ {
		var compType pk.VarInt
		if _, err := compType.ReadFrom(r); err != nil {
			return defaultScale
		}
		comp := component.NewComponent(int32(compType))
		if comp == nil {
			return defaultScale
		}
		if _, err := comp.ReadFrom(r); err != nil {
			return defaultScale
		}
		if int32(compType) == potionDurationScaleComponentID {
			if pds, ok := comp.(*component.PotionDurationScale); ok {
				return float32(pds.Float)
			}
			return defaultScale
		}
	}
	return defaultScale
}

// readCustomEffects decodes the PotionContents.customEffects list off a stack: each ItemPotionEffect's
// MobEffect registry index (ID) is resolved to its "minecraft:<name>" id via registryid.MobEffect, and
// its Details give the amplifier + duration. An entry whose id is out of the registry range is skipped
// (cannot resolve to a known effect). Returns nil when the stack has no potion_contents component or no
// custom effects.
func readCustomEffects(stack component.SlotData) []potionEffect {
	if stackEmpty(stack) || len(stack.RawComponents) == 0 || int(stack.AddedCount) <= 0 {
		return nil
	}
	r := bytes.NewReader(stack.RawComponents)
	for i := int32(0); i < int32(stack.AddedCount); i++ {
		var compType pk.VarInt
		if _, err := compType.ReadFrom(r); err != nil {
			return nil
		}
		comp := component.NewComponent(int32(compType))
		if comp == nil {
			return nil
		}
		if _, err := comp.ReadFrom(r); err != nil {
			return nil
		}
		if int32(compType) != potionContentsComponentID {
			continue
		}
		pc, ok := comp.(*component.PotionContents)
		if !ok || len(pc.CustomEffects) == 0 {
			return nil
		}
		out := make([]potionEffect, 0, len(pc.CustomEffects))
		for _, ce := range pc.CustomEffects {
			idx := int(ce.ID)
			if idx < 0 || idx >= len(registryid.MobEffect) {
				continue
			}
			out = append(out, potionEffect{
				id:        registryid.MobEffect[idx],
				duration:  int(ce.Details.Duration),
				amplifier: int(ce.Details.Amplifier),
			})
		}
		return out
	}
	return nil
}
