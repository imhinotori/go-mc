package server

// mob_effect.go — MOB-EFFECT-01 (Task #9, witch prerequisite): a minimal-but-faithful mob-effect slice,
// ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this session — see the agent
// report). Scoped to the effects a Witch's SPLASH potions apply to a PLAYER: instant_damage (HARMING),
// poison, slowness, weakness. (heal/regeneration are Raider-only in Witch.performRangedAttack — deferred
// with the raid subsystem; the table is structured so they slot in later.)
//
// Verified behavior (net.minecraft.world.effect.*):
//   - instant_damage (HealOrHarmMobEffect, isHarm): instant, hurtServer(magic, 6<<amp). Splash-scaled
//     amount = (int)(scale*(6<<amp)+0.5). shouldApplyEffectTickThisTick = remaining>=1.
//   - poison (PoisonMobEffect): duration, interval = 25>>amp; on tick if health>1.0 hurt(magic,1.0).
//   - slowness (MobEffects.SLOWNESS): MOVEMENT_SPEED modifier -0.15*(amp+1) ADD_MULTIPLIED_TOTAL.
//   - weakness (MobEffects.WEAKNESS): ATTACK_DAMAGE modifier -4.0*(amp+1) ADD_VALUE.
//   - tickServer: counter = duration (counting DOWN); if shouldApply → applyEffectTick; then duration--;
//     remove at duration<=0 (LivingEntity.tickEffects + MobEffectInstance.tickServer).
//
// v1 STUBS (cited): isInvertedHealAndHarm() (undead flip) = false; isAffectedByPotions = !dead. Slowness on
// a PLAYER is a cited no-op observable (player movement is client-authoritative in v1 — the modifier is
// attached faithfully on MOVEMENT_SPEED so a server-side movement port later reads it, but v1 shows no
// visible slow). Weakness IS observable (the melee-combat port reads ATTACK_DAMAGE server-side).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// effect ids (the registry names the witch's potions carry).
const (
	effectInstantDamage = "minecraft:instant_damage"
	effectPoison        = "minecraft:poison"
	effectSlowness      = "minecraft:slowness"
	effectWeakness      = "minecraft:weakness"
	// BAD_OMEN / RAID_OMEN drive the village raid trigger (BadOmenMobEffect/RaidOmenMobEffect). A raider
	// captain kill grants BAD_OMEN; entering a village converts it to RAID_OMEN; RAID_OMEN expiry starts a
	// raid via PoiManager's village-center query. VERIFIED net.minecraft.world.effect.MobEffects.BAD_OMEN /
	// RAID_OMEN.
	effectBadOmen  = "minecraft:bad_omen"
	effectRaidOmen = "minecraft:raid_omen"
	// BEACON effect ids (BEACON-01, BeaconBlockEntity.BEACON_EFFECTS): the six power effects a beacon can
	// grant a player in range — speed/haste (level 1), resistance/jump_boost (level 2), strength (level 3),
	// regeneration (level 4 secondary). effectSpeed/effectRegeneration already exist (witch consts); the
	// remaining four are declared here. VERIFIED CFR MobEffects registrations (attribute modifiers cited on
	// applyEffectModifiers below).
	effectHaste         = "minecraft:haste"          // MobEffects.HASTE      (ATTACK_SPEED       +0.1 ADD_MULTIPLIED_TOTAL)
	effectMiningFatigue = "minecraft:mining_fatigue" // MobEffects.MINING_FATIGUE (ATTACK_SPEED -0.1 ADD_MULTIPLIED_TOTAL)
	effectResistance    = "minecraft:resistance"     // MobEffects.RESISTANCE (no attribute modifier; damage-reduction)
	effectJumpBoost     = "minecraft:jump_boost"     // MobEffects.JUMP_BOOST (SAFE_FALL_DISTANCE +1.0 ADD_VALUE)
	// MOVEMENT effect ids read by the mob physics path (server/physics.go tickPhysics + jump.go). These
	// have no attribute modifier — they are read directly by LivingEntity.getEffectiveGravity / travelInAir
	// / getJumpBoostPower. VERIFIED CFR MobEffects.SLOW_FALLING / LEVITATION registrations (plain effects).
	effectSlowFalling = "minecraft:slow_falling" // MobEffects.SLOW_FALLING (getEffectiveGravity min 0.01)
	effectLevitation  = "minecraft:levitation"   // MobEffects.LEVITATION  (travelInAir upward drift)
	effectStrength    = "minecraft:strength"     // MobEffects.STRENGTH   (ATTACK_DAMAGE      +3.0 ADD_VALUE)
	// CONDUIT effect id (CONDUIT-01, ConduitBlockEntity.applyEffects): the beneficial power an active conduit
	// grants a submerged/rained-on player in range. VERIFIED CFR MobEffects.CONDUIT_POWER =
	// register("conduit_power", new MobEffect(MobEffectCategory.BENEFICIAL, 1950417)) — a plain duration
	// effect with NO attribute modifiers (the underwater vision/breathing/mining bonuses are applied by
	// dedicated ConduitPower checks elsewhere, not by an attribute modifier on the effect), so addPlayerEffect
	// inserts it as a bare duration effect.
	effectConduitPower = "minecraft:conduit_power"
	// WITHER effect id (WitherSkull.onHitEntity applies it on a NORMAL/HARD hit): MobEffects.WITHER =
	// register("wither", new MobEffect(MobEffectCategory.HARMFUL, ...)). The per-tick damage-over-time is
	// WIRED (WitherMobEffect.applyEffectTick: hurtServer(wither(), 1.0F) every WitherMobEffect
	// .shouldApplyEffectTickThisTick 40>>amp ticks) on BOTH the player path (applyEffectTick/
	// effectShouldApplyThisTick) and the entity path (applyEntityEffectTick/entityEffectShouldApplyThisTick).
	// Unlike POISON, wither CAN kill (no health>1.0 floor). The 8.0 skull hit is the direct-hit gameplay.
	effectWither = "minecraft:wither"
	// EFFECT-BEHAVIOR-01: the self-contained movement/utility effects with REAL server-side behavior.
	// ABSORPTION (AbsorptionMobEffect): a MAX_ABSORPTION +4.0*(amp+1) ADD_VALUE modifier raises the
	// setAbsorptionAmount clamp ceiling, and onEffectStarted fills the hearts to max(current, 4*(amp+1)).
	// VERIFIED CFR MobEffects.ABSORPTION (addAttributeModifier MAX_ABSORPTION, effect.absorption, 4.0,
	// ADD_VALUE) + AbsorptionMobEffect.onEffectStarted/applyEffectTick/shouldApplyEffectTickThisTick.
	effectAbsorption = "minecraft:absorption"
	// HUNGER (HungerMobEffect): applyEffectTick on a Player -> causeFoodExhaustion(0.005*(amp+1)) every
	// tick (shouldApplyEffectTickThisTick == true). VERIFIED CFR HungerMobEffect.applyEffectTick.
	effectHunger = "minecraft:hunger"
	// INVISIBILITY (plain MobEffect): sets the shared Entity invisible flag (DATA_SHARED_FLAGS bit 5,
	// 1<<5 == 0x20) via LivingEntity.updateInvisibilityStatus -> setInvisible(hasEffect(INVISIBILITY)),
	// broadcast to trackers. VERIFIED CFR MobEffects.INVISIBILITY (plain MobEffect) + Entity.setInvisible
	// (setSharedFlag(5, value)) + LivingEntity.updateInvisibilityStatus.
	effectInvisibility = "minecraft:invisibility"
	// BLINDNESS (plain MobEffect): the Illusioner's IllusionerBlindnessSpellGoal casts it on its target
	// (MobEffectInstance(BLINDNESS, 400)). A plain effect (no special modifier), so addPlayerEffect handles
	// it generically (the client dims the victim's view). VERIFIED javap Illusioner$IllusionerBlindnessSpellGoal
	// .performSpellCasting: target.addEffect(new MobEffectInstance(MobEffects.BLINDNESS, 400), this).
	effectBlindness = "minecraft:blindness"
	// POTION-DRINK effect ids: the vanilla drinkable-potion effects (Potions static{}) not already declared
	// above. NIGHT_VISION / SLOW_FALLING / LUCK are plain MobEffects (no attribute modifier, no per-tick
	// action) whose drink grants presence + duration (v1 observable = hasEffect + duration countdown, the
	// client renders the vision/fall/loot behavior). WIND_CHARGED / WEAVING / OOZING / INFESTED are the
	// on-death "explosive/cobweb/slime/silverfish" MobEffects (MobEffectCategory.HARMFUL) applied by drinking
	// the corresponding potion; their on-expiry spawn behavior (WeavingMobEffect/OozingMobEffect/... .onMobRemoved
	// or applyEffectTick) is CITE-DEFERRED like the movement no-ops, so v1 attaches+ticks the effect and the
	// spawn lands once those subsystems exist. VERIFIED CFR net.minecraft.world.effect.MobEffects registrations
	// + net.minecraft.world.item.alchemy.Potions static block.
	effectNightVision = "minecraft:night_vision" // Potions.NIGHT_VISION (plain MobEffect)
	effectLuck        = "minecraft:luck"         // Potions.LUCK        (LUCK -> attribute; v1 presence-only)
	effectWindCharged = "minecraft:wind_charged" // Potions.WIND_CHARGED (WindChargedMobEffect)
	effectWeaving     = "minecraft:weaving"      // Potions.WEAVING      (WeavingMobEffect)
	effectOozing      = "minecraft:oozing"       // Potions.OOZING       (OozingMobEffect)
	effectInfested    = "minecraft:infested"     // Potions.INFESTED     (InfestedMobEffect)
)

// invisibleSharedFlagBit is Entity.FLAG_INVISIBLE — DATA_SHARED_FLAGS (index 0) bit 5 (1<<5 == 0x20):
// the client renders the entity invisible while set.
//
//	[VERIFIED javap Entity.setInvisible(boolean) -> setSharedFlag(5, value); the invisible flag is bit 5.]
const invisibleSharedFlagBit int8 = 0x20

// modifier ids (stable identity per effect, matching the vanilla effect.<name> ids).
const (
	slownessModifierID = "effect.slowness"
	weaknessModifierID = "effect.weakness"
	// BEACON effect modifier ids (VERIFIED CFR MobEffects.*: Identifier.withDefaultNamespace("effect.<name>")).
	speedModifierID         = "effect.speed"          // SPEED     -> MOVEMENT_SPEED
	hasteModifierID         = "effect.haste"          // HASTE     -> ATTACK_SPEED
	miningFatigueModifierID = "effect.mining_fatigue" // MINING_FATIGUE -> ATTACK_SPEED
	strengthModifierID      = "effect.strength"       // STRENGTH  -> ATTACK_DAMAGE
	jumpBoostModifierID     = "effect.jump_boost"     // JUMP_BOOST-> SAFE_FALL_DISTANCE
	// ABSORPTION modifier id (VERIFIED CFR MobEffects.ABSORPTION addAttributeModifier: MAX_ABSORPTION,
	// Identifier.withDefaultNamespace("effect.absorption"), 4.0, ADD_VALUE).
	absorptionModifierID = "effect.absorption" // ABSORPTION -> MAX_ABSORPTION
)

// Modifier amounts from MobEffects static init in the 26.2 jar. The non-round decimals are the exact
// double constants emitted by javap for the vanilla registrations.
const (
	effectSpeedAmount         = 0.20000000298023224
	effectSlownessAmount      = -0.15000000596046448
	effectHasteAmount         = 0.10000000149011612
	effectMiningFatigueAmount = -0.10000000149011612 // MobEffects.MINING_FATIGUE ATTACK_SPEED modifier per level (verified javap)
	effectStrengthAmount      = 3.0
	effectWeaknessAmount      = -4.0
	effectJumpBoostAmount     = 1.0
	effectAbsorptionAmount    = 4.0
)

// activeEffect is the Go stand-in for MobEffectInstance: effect id, remaining duration (ticks, counting
// down; -1 is infinite), amplifier (0-based, clamped to [0,255]), particle/icon flags, and the hidden
// lower-priority chain used when a shorter stronger effect temporarily overrides a longer weaker one.
// Ports MobEffectInstance fields/update/tickServer/downgradeToHiddenEffect from the 26.2 jar.
type activeEffect struct {
	id        string
	duration  int
	amplifier int
	ambient   bool
	visible   bool
	showIcon  bool
	hidden    *activeEffect
}

func newActiveEffect(id string, duration, amplifier int) *activeEffect {
	if amplifier < 0 {
		amplifier = 0
	}
	if amplifier > 255 {
		amplifier = 255
	}
	return &activeEffect{
		id:        id,
		duration:  duration,
		amplifier: amplifier,
		visible:   true,
		showIcon:  true,
	}
}

func cloneActiveEffect(in *activeEffect) *activeEffect {
	if in == nil {
		return nil
	}
	return &activeEffect{
		id:        in.id,
		duration:  in.duration,
		amplifier: in.amplifier,
		ambient:   in.ambient,
		visible:   in.visible,
		showIcon:  in.showIcon,
		hidden:    cloneActiveEffect(in.hidden),
	}
}

func activeEffectShorterThan(a, b *activeEffect) bool {
	if a == nil || b == nil || a.duration == -1 {
		return false
	}
	return a.duration < b.duration || b.duration == -1
}

func updateActiveEffect(cur, incoming *activeEffect) bool {
	if cur == nil || incoming == nil {
		return false
	}
	changed := false
	if incoming.amplifier > cur.amplifier {
		if activeEffectShorterThan(incoming, cur) {
			oldHidden := cur.hidden
			cur.hidden = cloneActiveEffect(cur)
			cur.hidden.hidden = oldHidden
		}
		cur.amplifier = incoming.amplifier
		cur.duration = incoming.duration
		changed = true
	} else if activeEffectShorterThan(cur, incoming) {
		if incoming.amplifier == cur.amplifier {
			cur.duration = incoming.duration
			changed = true
		} else if cur.hidden == nil {
			cur.hidden = cloneActiveEffect(incoming)
		} else {
			_ = updateActiveEffect(cur.hidden, incoming)
		}
	}
	if (!incoming.ambient && cur.ambient) || changed {
		cur.ambient = incoming.ambient
		changed = true
	}
	if incoming.visible != cur.visible {
		cur.visible = incoming.visible
		changed = true
	}
	if incoming.showIcon != cur.showIcon {
		cur.showIcon = incoming.showIcon
		changed = true
	}
	return changed
}

func activeEffectHasRemaining(e *activeEffect) bool {
	return e != nil && (e.duration == -1 || e.duration > 0)
}

func tickDownActiveEffect(e *activeEffect) {
	if e == nil {
		return
	}
	if e.hidden != nil {
		tickDownActiveEffect(e.hidden)
	}
	if e.duration != -1 && e.duration != 0 {
		e.duration--
	}
}

func downgradeActiveEffect(e *activeEffect) bool {
	if e == nil || e.duration != 0 || e.hidden == nil {
		return false
	}
	hidden := e.hidden
	e.duration = hidden.duration
	e.amplifier = hidden.amplifier
	e.ambient = hidden.ambient
	e.visible = hidden.visible
	e.showIcon = hidden.showIcon
	e.hidden = hidden.hidden
	return true
}

// splashEffect is one entry of a thrown potion's payload (the PotionContents effect): the effect id, its
// base duration, and amplifier. On splash each is applied to nearby players scaled by proximity.
type splashEffect struct {
	id        string
	duration  int
	amplifier int
}

// isInstantEffect reports whether an effect id is an InstantaneousMobEffect (applied once at add, never
// duration-ticked). instant_damage/instant_health are the witch-relevant instants.
func isInstantEffect(id string) bool {
	return id == effectInstantDamage || id == effectInstantHealth
}

// addPlayerEffect is the port of LivingEntity.addEffect for a player target: an INSTANT effect applies its
// one-shot amount immediately (with the given proximity scale, splash path); a DURATION effect is inserted
// into the effect map (keeping the stronger/longer on a same-id collision) and its attribute modifiers (if
// any) are attached (onEffectAdded). Cite LivingEntity.addEffect + onEffectAdded.
func (t *TickLoop) addPlayerEffect(p *tickPlayer, ownerID int32, id string, duration, amplifier int, scale float64) {
	if p == nil || p.dead {
		return // isAffectedByPotions == !isDeadOrDying()
	}
	if isInstantEffect(id) {
		t.applyInstantEffect(p, ownerID, id, amplifier, scale)
		return
	}
	if p.activeEffects == nil {
		p.activeEffects = make(map[string]*activeEffect)
	}
	incoming := newActiveEffect(id, duration, amplifier)
	if existing, ok := p.activeEffects[id]; ok {
		oldAmp := existing.amplifier
		if updateActiveEffect(existing, incoming) {
			if oldAmp != existing.amplifier {
				t.removeEffectModifiers(p, id)
				t.applyEffectModifiers(p, id, existing.amplifier)
			}
			// ServerPlayer.onEffectUpdated -> connection.send(UpdateMobEffect(id, inst, false)).
			t.sendPlayerEffectUpdated(p, existing)
			t.onPlayerEffectStarted(p, id, existing.amplifier)
			return
		}
		t.onPlayerEffectStarted(p, id, incoming.amplifier)
		return
	}
	p.activeEffects[id] = incoming
	t.applyEffectModifiers(p, id, incoming.amplifier) // LivingEntity.onEffectAdded -> MobEffect.addAttributeModifiers
	// ServerPlayer.onEffectAdded -> connection.send(UpdateMobEffect(id, inst, true)) — blend=true on add.
	t.sendPlayerEffectAdded(p, incoming)
	t.onPlayerEffectStarted(p, id, incoming.amplifier) // MobEffectInstance.onEffectStarted -> MobEffect.onEffectStarted
}

// onPlayerEffectStarted is the port of MobEffect.onEffectStarted (called last in LivingEntity.addEffect,
// AFTER onEffectAdded attaches the attribute modifiers). ABSORPTION fills the hearts to
// max(getAbsorptionAmount(), 4*(amp+1)); INVISIBILITY updates the shared invisible flag. The base
// MobEffect.onEffectStarted is a no-op, so every other effect falls through. Cite
// AbsorptionMobEffect.onEffectStarted + LivingEntity.updateInvisibilityStatus.
func (t *TickLoop) onPlayerEffectStarted(p *tickPlayer, id string, amplifier int) {
	switch id {
	case effectAbsorption:
		// AbsorptionMobEffect.onEffectStarted: setAbsorptionAmount(Math.max(getAbsorptionAmount(),
		// (float)(4*(1+amp)))). The MAX_ABSORPTION +4*(amp+1) modifier attached in applyEffectModifiers
		// above raised the clamp ceiling first, so setAbsorptionAmount does not clamp the fill back to 0.
		fill := float32(effectAbsorptionAmount * float64(amplifier+1))
		if cur := p.getAbsorptionAmount(); cur > fill {
			fill = cur
		}
		p.setAbsorptionAmount(fill)
	case effectInvisibility:
		// LivingEntity.updateInvisibilityStatus: setInvisible(hasEffect(INVISIBILITY)) -> the invisible
		// bit is now present, so broadcast the shared-flags byte to trackers.
		t.broadcastPlayerInvisibleFlag(p)
	}
}

// broadcastPlayerInvisibleFlag pushes the DATA_SHARED_FLAGS byte (index 0) to every player tracking p,
// carrying the invisible bit (0x20) iff the player currently has INVISIBILITY. This is the port of
// LivingEntity.updateInvisibilityStatus's setInvisible(hasEffect(INVISIBILITY)) broadcast. v1 players
// carry no other server-side shared flags (no player remainingFireTicks/crouch/sprint wire — see
// fire.go's cited player-fire deferral), so the byte carries ONLY the invisible bit, exactly as a
// vanilla player with no other flags would rewrite it. Mirrors broadcastEntityFireFlag (BYTE serializer
// at index 0). Cite LivingEntity.updateInvisibilityStatus + Entity.setInvisible (setSharedFlag(5,...)).
func (t *TickLoop) broadcastPlayerInvisibleFlag(p *tickPlayer) {
	// Delegate to the unified shared-flags broadcast so the invisible and fall-flying (elytra) bits
	// coexist in the single DATA_SHARED_FLAGS byte -- rewriting it from only the invisible bit here
	// would clobber a concurrent FALL_FLYING flag (and vice-versa). playerSharedFlags composes both.
	t.broadcastPlayerSharedFlags(p)
}

// applyInstantEffect is the port of HealOrHarmMobEffect.applyInstantaneousEffect for a player victim: the
// harm amount = (int)(scale*(6<<amp)+0.5), dealt as indirect_magic (attributed to the thrower) or magic.
func (t *TickLoop) applyInstantEffect(p *tickPlayer, ownerID int32, id string, amplifier int, scale float64) {
	if playerIsInvertedHealAndHarm(p) {
		switch id {
		case effectInstantDamage:
			id = effectInstantHealth
		case effectInstantHealth:
			id = effectInstantDamage
		}
	}
	switch id {
	case effectInstantDamage:
		amount := int(scale*float64(int(6)<<uint(amplifier)) + 0.5)
		src := damageSourceMagic()
		if ownerID != 0 {
			src = damageSourceIndirectMagic(ownerID)
		}
		t.applyDamage(p, src, float32(amount))
	case effectInstantHealth:
		amount := int(scale*float64(int(4)<<uint(amplifier)) + 0.5)
		t.heal(p, float32(amount))
	}
}

func playerIsInvertedHealAndHarm(_ *tickPlayer) bool {
	return false
}

// applyEffectModifiers attaches an effect's attribute modifiers (onEffectAdded → addAttributeModifiers).
// slowness → MOVEMENT_SPEED -0.15*(amp+1) ADD_MULTIPLIED_TOTAL; weakness → ATTACK_DAMAGE -4.0*(amp+1)
// ADD_VALUE. The modifier amount scales by (amplifier+1) (MobEffect$AttributeTemplate.create).
func (t *TickLoop) applyEffectModifiers(p *tickPlayer, id string, amplifier int) {
	h := p.playerAttributes()
	switch id {
	case effectSlowness:
		h.addModifier(attrMovementSpeed, attribute.AttributeModifier{
			ID:        slownessModifierID,
			Amount:    effectSlownessAmount * float64(amplifier+1),
			Operation: attribute.AddMultipliedTotal,
		})
	case effectWeakness:
		h.addModifier(attrAttackDamage, attribute.AttributeModifier{
			ID:        weaknessModifierID,
			Amount:    effectWeaknessAmount * float64(amplifier+1),
			Operation: attribute.AddValue,
		})
	case effectSpeed:
		// MobEffects.SPEED: MOVEMENT_SPEED +0.2*(amp+1) ADD_MULTIPLIED_TOTAL (effect.speed).
		h.addModifier(attrMovementSpeed, attribute.AttributeModifier{
			ID:        speedModifierID,
			Amount:    effectSpeedAmount * float64(amplifier+1),
			Operation: attribute.AddMultipliedTotal,
		})
	case effectHaste:
		// MobEffects.HASTE: ATTACK_SPEED +0.1*(amp+1) ADD_MULTIPLIED_TOTAL (effect.haste).
		h.addModifier(attrAttackSpeed, attribute.AttributeModifier{
			ID:        hasteModifierID,
			Amount:    effectHasteAmount * float64(amplifier+1),
			Operation: attribute.AddMultipliedTotal,
		})
	case effectMiningFatigue:
		// MobEffects.MINING_FATIGUE: ATTACK_SPEED -0.1*(amp+1) ADD_MULTIPLIED_TOTAL (effect.mining_fatigue).
		h.addModifier(attrAttackSpeed, attribute.AttributeModifier{
			ID:        miningFatigueModifierID,
			Amount:    effectMiningFatigueAmount * float64(amplifier+1),
			Operation: attribute.AddMultipliedTotal,
		})
	case effectStrength:
		// MobEffects.STRENGTH: ATTACK_DAMAGE +3.0*(amp+1) ADD_VALUE (effect.strength).
		h.addModifier(attrAttackDamage, attribute.AttributeModifier{
			ID:        strengthModifierID,
			Amount:    effectStrengthAmount * float64(amplifier+1),
			Operation: attribute.AddValue,
		})
	case effectJumpBoost:
		// MobEffects.JUMP_BOOST: SAFE_FALL_DISTANCE +1.0*(amp+1) ADD_VALUE (effect.jump_boost).
		h.addModifier(attrSafeFallDistance, attribute.AttributeModifier{
			ID:        jumpBoostModifierID,
			Amount:    effectJumpBoostAmount * float64(amplifier+1),
			Operation: attribute.AddValue,
		})
	case effectAbsorption:
		// MobEffects.ABSORPTION: MAX_ABSORPTION +4.0*(amp+1) ADD_VALUE (effect.absorption). This raises the
		// setAbsorptionAmount clamp ceiling so onEffectStarted's heart-fill is not clamped back to 0.
		h.addModifier(attrMaxAbsorption, attribute.AttributeModifier{
			ID:        absorptionModifierID,
			Amount:    effectAbsorptionAmount * float64(amplifier+1),
			Operation: attribute.AddValue,
		})
		// effectResistance / effectRegeneration carry NO attribute modifier (RESISTANCE reduces damage in the
		// hurt calc; REGENERATION is a periodic heal — handled in effectShouldApplyThisTick/applyEffectTick).
	}
}

// removeEffectModifiers detaches an effect's attribute modifiers (onEffectsRemoved → removeAttributeModifiers).
func (t *TickLoop) removeEffectModifiers(p *tickPlayer, id string) {
	if p.attributes == nil {
		return
	}
	switch id {
	case effectSlowness:
		p.attributes.removeModifier(attrMovementSpeed, slownessModifierID)
	case effectWeakness:
		p.attributes.removeModifier(attrAttackDamage, weaknessModifierID)
	case effectSpeed:
		p.attributes.removeModifier(attrMovementSpeed, speedModifierID)
	case effectHaste:
		p.attributes.removeModifier(attrAttackSpeed, hasteModifierID)
	case effectMiningFatigue:
		p.attributes.removeModifier(attrAttackSpeed, miningFatigueModifierID)
	case effectStrength:
		p.attributes.removeModifier(attrAttackDamage, strengthModifierID)
	case effectJumpBoost:
		p.attributes.removeModifier(attrSafeFallDistance, jumpBoostModifierID)
	case effectAbsorption:
		// Detach the MAX_ABSORPTION modifier, then re-run setAbsorptionAmount(getAbsorptionAmount()) so the
		// now-lowered getMaxAbsorption() clamp trims any leftover absorption back to [0, maxAbsorption]. This
		// is vanilla's behavior: LivingEntity.onEffectsRemoved -> removeAttributeModifiers; the next
		// setAbsorptionAmount clamps against the reduced ceiling (a bare removal without a refresh would leave
		// stale hearts above the ceiling until the next hit).
		p.attributes.removeModifier(attrMaxAbsorption, absorptionModifierID)
		p.setAbsorptionAmount(p.getAbsorptionAmount())
	}
}

// tickPlayerEffects is the port of LivingEntity.tickEffects for a player: for each active effect, run its
// per-tick apply (poison damage), then count the duration down and remove it (+ its modifiers) at 0. Called
// once per tick per live player from the tick loop. hasEffect(id) below reads the same map.
func (t *TickLoop) tickPlayerEffects(p *tickPlayer) {
	if p == nil || len(p.activeEffects) == 0 {
		return
	}
	for id, e := range p.activeEffects {
		if !activeEffectHasRemaining(e) {
			delete(p.activeEffects, id)
			t.removeEffectModifiers(p, id)
			t.onPlayerEffectRemoved(p, id)
			continue
		}
		// MobEffectInstance.tickServer: if shouldApply, run applyEffectTick; a false return means the effect
		// consumed itself (BAD_OMEN converting / RAID_OMEN firing, or ABSORPTION depleted) -> remove it now
		// and skip the countdown. tickCount passed to shouldApplyEffectTickThisTick is the remaining duration
		// (counting DOWN).
		remaining := e.duration
		if remaining == -1 {
			remaining = 1 << 30
		}
		if effectShouldApplyThisTick(id, remaining, e.amplifier) {
			if !t.applyEffectTick(p, id, e.amplifier) {
				delete(p.activeEffects, id)
				t.removeEffectModifiers(p, id)
				t.onPlayerEffectRemoved(p, id)
				continue
			}
		}
		oldAmp := e.amplifier
		tickDownActiveEffect(e)
		if downgradeActiveEffect(e) && oldAmp != e.amplifier {
			t.removeEffectModifiers(p, id)
			t.applyEffectModifiers(p, id, e.amplifier)
		}
		if !activeEffectHasRemaining(e) {
			delete(p.activeEffects, id)
			t.removeEffectModifiers(p, id)
			t.onPlayerEffectRemoved(p, id)
		}
	}
}

// onPlayerEffectRemoved is the removal-side counterpart of onPlayerEffectStarted: it runs after the
// effect is deleted from the map + its attribute modifiers detached (LivingEntity.onEffectsRemoved).
// INVISIBILITY re-broadcasts the shared-flags byte (now with the invisible bit cleared, since
// hasEffect(INVISIBILITY) is false). Every other effect falls through. Cite
// LivingEntity.updateInvisibilityStatus (re-run after an effect is removed).
func (t *TickLoop) onPlayerEffectRemoved(p *tickPlayer, id string) {
	// ServerPlayer.onEffectsRemoved -> per removed effect connection.send(RemoveMobEffect(id, effect)).
	t.sendPlayerEffectRemoved(p, id)
	switch id {
	case effectInvisibility:
		t.broadcastPlayerInvisibleFlag(p)
	}
}

// effectShouldApplyThisTick is the port of MobEffect.shouldApplyEffectTickThisTick for the witch effects.
func effectShouldApplyThisTick(id string, remaining, amplifier int) bool {
	switch id {
	case effectPoison:
		interval := 25 >> amplifier // amp0=25, amp1=12, ...
		if interval <= 0 {
			return true
		}
		return remaining%interval == 0
	case effectWither:
		interval := 40 >> amplifier // WitherMobEffect: 40>>amp
		if interval <= 0 {
			return true
		}
		return remaining%interval == 0
	case effectBadOmen:
		return true // BadOmenMobEffect.shouldApplyEffectTickThisTick -> always true
	case effectRaidOmen:
		return remaining == 1 // RaidOmenMobEffect.shouldApplyEffectTickThisTick -> remainingDuration == 1
	case effectRegeneration:
		// RegenerationMobEffect.shouldApplyEffectTickThisTick: interval = 50 >> amp (amp0=50, amp1=25, ...);
		// remaining % interval == 0 (BEACON level-4 secondary can grant regeneration to a player in range).
		interval := 50 >> amplifier
		if interval <= 0 {
			return true
		}
		return remaining%interval == 0
	case effectAbsorption:
		return true // AbsorptionMobEffect.shouldApplyEffectTickThisTick -> always true
	case effectHunger:
		return true // HungerMobEffect.shouldApplyEffectTickThisTick -> always true
	default:
		return false // slowness/weakness/speed/haste/strength/jump_boost/resistance/invisibility never tick
	}
}

// applyEffectTick is the port of MobEffect.applyEffectTick. It returns whether the effect should be
// KEPT (Java returns boolean; false -> remove the effect this tick). The witch's periodic effects always
// return true; the BAD_OMEN/RAID_OMEN transitions return false when they fire (they consume themselves).
func (t *TickLoop) applyEffectTick(p *tickPlayer, id string, amplifier int) bool {
	switch id {
	case effectPoison:
		// PoisonMobEffect: if health > 1.0 hurt magic 1.0 (poison never kills).
		if p.health > 1.0 {
			t.applyDamage(p, damageSourceMagic(), 1.0)
		}
		return true
	case effectWither:
		t.applyDamage(p, damageSourceWither(), 1.0)
		return true
	case effectBadOmen:
		return t.applyBadOmenTick(p, amplifier)
	case effectRaidOmen:
		return t.applyRaidOmenTick(p)
	case effectRegeneration:
		// RegenerationMobEffect.applyEffectTick: if health < maxHealth heal 1.0. (BEACON level-4 secondary.)
		if p.health < maxHealth {
			t.heal(p, 1.0)
		}
		return true
	case effectAbsorption:
		// AbsorptionMobEffect.applyEffectTick: return getAbsorptionAmount() > 0. When the absorption hearts
		// are depleted (folded away by actuallyHurt), this returns false -> the effect removes itself.
		return p.getAbsorptionAmount() > 0.0
	case effectHunger:
		// HungerMobEffect.applyEffectTick: causeFoodExhaustion(0.005f * (amp+1)). causeFoodExhaustion is the
		// single exhaustion entry point (attack_dispatch.go): it honors the abilities.invulnerable guard then
		// routes into FoodData.addExhaustion, exactly Player.causeFoodExhaustion.
		t.causeFoodExhaustion(p, 0.005*float32(amplifier+1))
		return true
	}
	return true
}

// applyBadOmenTick is BadOmenMobEffect.applyEffectTick for a player: if the player is in a village (POI-
// dense) and the difficulty is not PEACEFUL and there is no active raid already at max raid-omen level,
// convert BAD_OMEN -> RAID_OMEN (600 ticks, same amplifier), record the raid-omen position, and REMOVE
// bad_omen (return false). Otherwise keep bad_omen (return true). VERIFIED BadOmenMobEffect.applyEffectTick.
func (t *TickLoop) applyBadOmenTick(p *tickPlayer, amplifier int) bool {
	if p == nil || p.dead {
		return true // !isSpectator() analogue (v1 has no spectator toggle) -> proceed only for a live player
	}
	if t.levelDifficulty == difficultyPeaceful {
		return true
	}
	pos := playerBlockPos(p)
	pm := t.cur().poiManager
	if pm == nil || !pm.isVillage(pos) {
		return true // not in a village -> keep bad_omen and keep scanning
	}
	// (raid == null || raid.getRaidOmenLevel() < raid.getMaxRaidOmenLevel())
	if rm := t.cur().raidsManager; rm != nil {
		if raid := rm.getRaidAtBlock(pos); raid != nil && raid.getRaidOmenLevel() >= raid.getMaxRaidOmenLevel() {
			return true
		}
	}
	t.addPlayerEffect(p, 0, effectRaidOmen, 600, amplifier, 1.0)
	imm := pos
	p.raidOmenPosition = &imm
	return false // player.addEffect(RAID_OMEN...) then return false -> bad_omen is consumed
}

// applyRaidOmenTick is RaidOmenMobEffect.applyEffectTick for a player: when the raid-omen effect reaches
// its final tick (shouldApply gates remaining==1), fire createOrExtendRaid at the recorded raid-omen
// position, clear the position, and REMOVE raid_omen (return false). VERIFIED RaidOmenMobEffect.applyEffectTick.
func (t *TickLoop) applyRaidOmenTick(p *tickPlayer) bool {
	if p == nil || p.raidOmenPosition == nil {
		return true
	}
	t.createOrExtendRaid(p, *p.raidOmenPosition)
	p.raidOmenPosition = nil
	return false
}

// playerHasEffect is the port of LivingEntity.hasEffect(Holder): whether the player currently carries the
// effect. The witch's potion-selection ladder reads it (don't re-apply a slowness the target already has).
func playerHasEffect(p *tickPlayer, id string) bool {
	if p == nil || p.activeEffects == nil {
		return false
	}
	_, ok := p.activeEffects[id]
	return ok
}

// playerEffectAmplifier is the port of LivingEntity.getEffect(Holder).getAmplifier(): the 0-based amplifier
// of the player's active effect (Resistance I = 0, Resistance IV = 3). Returns (0, false) when the player
// does not carry the effect — callers must gate on playerHasEffect (or the returned ok) before trusting the
// amplifier, exactly as the vanilla `hasEffect(...) ? getEffect(...).getAmplifier()` guard requires.
func playerEffectAmplifier(p *tickPlayer, id string) (int, bool) {
	if p == nil || p.activeEffects == nil {
		return 0, false
	}
	e, ok := p.activeEffects[id]
	if !ok {
		return 0, false
	}
	return e.amplifier, true
}

// splashPotionScale is the proximity factor for a splashed entity: scale = 1 - sqrt(dist)/4, where dist is
// the squared distance from the potion AABB to the entity AABB. Clamped to [0,1]. Cite ThrownSplashPotion
// .onHitAsPotion (d0 = 1 - sqrt(dist)/4).
func splashPotionScale(distSqr float64) float64 {
	s := 1.0 - math.Sqrt(distSqr)/4.0
	if s < 0 {
		s = 0
	}
	if s > 1 {
		s = 1
	}
	return s
}

// ============================================================================================
// Entity-side mob effects (MOB-HOST-07 witch self-drink). The witch drinks a potion on ITSELF, so the
// effect applies to an *Entity (not a tickPlayer). This is the entity analogue of the tickPlayer effect
// slice above, scoped to the witch self-buffs: instant_health (HEALING, instant self-heal), regeneration
// (the raid path, periodic heal), speed/water_breathing/fire_resistance (pure duration effects — no
// server-side observable beyond presence in v1, but ticked down + hasEffect-visible so the ladder's
// !hasEffect gate is faithful). Cite LivingEntity.addEffect / tickEffects + the effect classes.
//
// Verified behavior (net.minecraft.world.effect.*):
//   - instant_health (HealOrHarmMobEffect, !isHarm): instant, heal((int)(scale*(4<<amp)+0.5)); self-drink
//     scale=1.0 so HEALING amp0 heals 4. (VERIFIED CFR HealOrHarmMobEffect.applyInstantaneousEffect.)
//   - regeneration (RegenerationMobEffect): interval = 50>>amp; on tick if health<maxHealth heal(1.0).
//     (VERIFIED CFR RegenerationMobEffect.applyEffectTick / shouldApplyEffectTickThisTick.)
//   - speed/water_breathing/fire_resistance: duration effects with no v1 server-side per-tick action
//     (SPEED's MOVEMENT_SPEED buff + water-breath/fire-immunity are movement/breath/fire subsystem reads
//     deferred; the effect is attached + ticked so hasEffect is faithful and the buff lands when those
//     subsystems read it — sibling of the player-side slowness cited no-op).

// isInstantEntityEffect reports whether an entity effect id is an InstantaneousMobEffect (applied once at
// add, never duration-ticked). instant_health is the witch-self-drink instant.
func isInstantEntityEffect(id string) bool {
	return id == effectInstantHealth || id == effectInstantDamage
}

// entityHasEffect ports LivingEntity.hasEffect(Holder) for a mob: whether the entity currently carries the
// effect. The witch self-drink ladder reads it (don't re-drink a buff it already has).
func entityHasEffect(e *Entity, id string) bool {
	if e == nil || e.mobEffects == nil {
		return false
	}
	_, ok := e.mobEffects[id]
	return ok
}

// entityEffectAmplifier is the port of LivingEntity.getEffect(Holder).getAmplifier() for a mob: the
// 0-based amplifier of the entity's active effect (Levitation I = 0, Jump Boost II = 1). Returns
// (0, false) when the mob does not carry the effect — callers gate on the returned ok (or entityHasEffect)
// before trusting the amplifier, exactly as the vanilla `hasEffect(...) ? getEffect(...).getAmplifier()`
// guard requires. Sibling of playerEffectAmplifier over the entity-side mobEffects map.
func entityEffectAmplifier(e *Entity, id string) (int, bool) {
	if e == nil || e.mobEffects == nil {
		return 0, false
	}
	inst, ok := e.mobEffects[id]
	if !ok {
		return 0, false
	}
	return inst.amplifier, true
}

// entityCanBeAffected ports LivingEntity.canBeAffected for the tags currently relevant to mob effects.
// 26.2 jar: INVERTED_HEALING_AND_HARM -> #undead; IGNORES_POISON_AND_REGEN -> #undead;
// IMMUNE_TO_INFESTED -> silverfish; IMMUNE_TO_OOZING -> slime.
func entityCanBeAffected(e *Entity, id string) bool {
	if e == nil {
		return false
	}
	// WitherSkeleton.canBeAffected(MobEffectInstance): returns false for WITHER (a wither skeleton is
	// immune to its own on-hit effect), THEN falls through to super.canBeAffected (the undead
	// poison/regen block below). Gated on the wither-skeleton type so it is the exact per-type override
	// -- the other undead (skeleton/zombie/...) are NOT wither-immune. Cite WitherSkeleton.canBeAffected
	// (offset 0-11: MobEffectInstance.is(WITHER) -> iconst_0 ireturn; else super.canBeAffected).
	if e.typ == entity.WitherSkeleton.ID && id == effectWither {
		return false
	}
	if entityIsUndead(e) && (id == effectPoison || id == effectRegeneration) {
		return false
	}
	switch id {
	case "minecraft:infested":
		return e.typ != entity.Silverfish.ID
	case "minecraft:oozing":
		return e.typ != entity.Slime.ID
	default:
		return true
	}
}

func entityIsUndead(e *Entity) bool {
	if e == nil {
		return false
	}
	switch e.typ {
	case entity.Skeleton.ID, entity.Stray.ID, entity.WitherSkeleton.ID, entity.SkeletonHorse.ID,
		entity.Bogged.ID, entity.Parched.ID,
		entity.ZombieHorse.ID, entity.CamelHusk.ID, entity.Zombie.ID, entity.ZombieVillager.ID,
		entity.ZombifiedPiglin.ID, entity.Zoglin.ID, entity.Drowned.ID, entity.Husk.ID,
		entity.ZombieNautilus.ID,
		entity.Wither.ID, entity.Phantom.ID:
		return true
	default:
		return false
	}
}

func entityIsInvertedHealAndHarm(e *Entity) bool {
	return entityIsUndead(e)
}

// addEntityEffect ports LivingEntity.addEffect for a mob self-target. scale is fixed 1.0 for self-drink;
// splash and projectile paths use addEntityEffectWithSource to pass the thrower and proximity scale.
func (t *TickLoop) addEntityEffect(e *Entity, id string, duration, amplifier int) {
	t.addEntityEffectWithSource(e, 0, id, duration, amplifier, 1.0)
}

func (t *TickLoop) addEntityEffectWithSource(e *Entity, ownerID int32, id string, duration, amplifier int, scale float64) {
	if e == nil || !e.isAlive() || e.dead {
		return // isAffectedByPotions == !isDeadOrDying()
	}
	// WitherBoss.addEffect(MobEffectInstance, Entity) { return false; } -- the wither is immune to EVERY mob
	// effect (unconditional override, checked before canBeAffected). VERIFIED javap WitherBoss.addEffect:
	// iconst_0; ireturn. Cite WitherBoss.addEffect.
	if e.wither != nil {
		return
	}
	if !entityCanBeAffected(e, id) {
		return
	}
	if isInstantEntityEffect(id) {
		t.applyInstantEntityEffect(e, ownerID, id, amplifier, scale)
		return
	}
	if e.mobEffects == nil {
		e.mobEffects = make(map[string]*activeEffect)
	}
	incoming := newActiveEffect(id, duration, amplifier)
	if existing, ok := e.mobEffects[id]; ok {
		oldAmp := existing.amplifier
		if updateActiveEffect(existing, incoming) {
			if oldAmp != existing.amplifier {
				t.removeEntityEffectModifiers(e, id)
				t.applyEntityEffectModifiers(e, id, existing.amplifier)
			}
			// LivingEntity.onEffectUpdated -> sendEffectToPassengers(inst) (blend=false).
			t.sendEntityEffect(e, existing)
			t.onEntityEffectStarted(e, id, existing.amplifier)
			return
		}
		t.onEntityEffectStarted(e, id, incoming.amplifier)
		return
	}
	e.mobEffects[id] = incoming
	t.applyEntityEffectModifiers(e, id, incoming.amplifier)
	// LivingEntity.onEffectAdded -> sendEffectToPassengers(inst) (blend=false).
	t.sendEntityEffect(e, incoming)
	t.onEntityEffectStarted(e, id, incoming.amplifier)
}

// applyInstantEntityEffect ports HealOrHarmMobEffect.applyInstantaneousEffect for mobs. Inverted
// heal/harm reads the vanilla tag chain inverted_healing_and_harm -> undead.
func (t *TickLoop) applyInstantEntityEffect(e *Entity, ownerID int32, id string, amplifier int, scale float64) {
	if entityIsInvertedHealAndHarm(e) {
		switch id {
		case effectInstantDamage:
			id = effectInstantHealth
		case effectInstantHealth:
			id = effectInstantDamage
		}
	}
	switch id {
	case effectInstantHealth:
		amount := int(scale*float64(int(4)<<uint(amplifier)) + 0.5)
		entityHeal(e, float32(amount))
	case effectInstantDamage:
		amount := int(scale*float64(int(6)<<uint(amplifier)) + 0.5)
		src := damageSourceMagic()
		if ownerID != 0 {
			src = damageSourceIndirectMagic(ownerID)
		}
		t.applyDamageEntity(e, src, float32(amount))
	}
}

func (t *TickLoop) onEntityEffectStarted(e *Entity, id string, amplifier int) {
	switch id {
	case effectAbsorption:
		fill := float32(effectAbsorptionAmount * float64(amplifier+1))
		if cur := e.getAbsorptionAmount(); cur > fill {
			fill = cur
		}
		e.setAbsorptionAmount(fill)
	case effectInvisibility:
		t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, sharedFlagsDataEntry(entitySharedFlags(e))))
	}
}

func (t *TickLoop) onEntityEffectRemoved(e *Entity, id string) {
	// LivingEntity.onEffectsRemoved for a mob: the base does NOT push a RemoveMobEffect to passengers
	// (intentional no-op seam, mirroring the player path).
	t.sendEntityEffectRemoved(e, id)
	switch id {
	case effectInvisibility:
		t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, sharedFlagsDataEntry(entitySharedFlags(e))))
	}
}

func entitySharedFlags(e *Entity) int8 {
	var flags int8
	if e != nil && e.remainingFireTicks > 0 {
		flags |= fireSharedFlagBit
	}
	if entityHasEffect(e, effectInvisibility) {
		flags |= invisibleSharedFlagBit
	}
	return flags
}

func (t *TickLoop) applyEntityEffectModifiers(e *Entity, id string, amplifier int) {
	if e == nil || e.attributes == nil {
		return
	}
	add := func(attr *attribute.Attribute, modID string, amount float64, op attribute.Operation) {
		inst := e.attributes.GetInstance(attr.Name())
		if inst == nil {
			return
		}
		inst.RemoveModifier(modID)
		inst.AddTransientModifier(attribute.AttributeModifier{
			ID:        modID,
			Amount:    amount * float64(amplifier+1),
			Operation: op,
		})
		markEntityAttrDirty(e, attr.Name())
	}
	switch id {
	case effectSlowness:
		add(attribute.MovementSpeed, slownessModifierID, effectSlownessAmount, attribute.AddMultipliedTotal)
	case effectWeakness:
		add(attribute.AttackDamage, weaknessModifierID, effectWeaknessAmount, attribute.AddValue)
	case effectSpeed:
		add(attribute.MovementSpeed, speedModifierID, effectSpeedAmount, attribute.AddMultipliedTotal)
	case effectHaste:
		add(attribute.AttackSpeed, hasteModifierID, effectHasteAmount, attribute.AddMultipliedTotal)
	case effectStrength:
		add(attribute.AttackDamage, strengthModifierID, effectStrengthAmount, attribute.AddValue)
	case effectJumpBoost:
		add(attribute.SafeFallDistance, jumpBoostModifierID, effectJumpBoostAmount, attribute.AddValue)
	case effectAbsorption:
		add(attribute.MaxAbsorption, absorptionModifierID, effectAbsorptionAmount, attribute.AddValue)
	}
}

func (t *TickLoop) removeEntityEffectModifiers(e *Entity, id string) {
	if e == nil || e.attributes == nil {
		return
	}
	remove := func(attr *attribute.Attribute, modID string) {
		if inst := e.attributes.GetInstance(attr.Name()); inst != nil {
			if _, ok := inst.GetModifier(modID); ok {
				inst.RemoveModifier(modID)
				markEntityAttrDirty(e, attr.Name())
			}
		}
	}
	switch id {
	case effectSlowness:
		remove(attribute.MovementSpeed, slownessModifierID)
	case effectWeakness:
		remove(attribute.AttackDamage, weaknessModifierID)
	case effectSpeed:
		remove(attribute.MovementSpeed, speedModifierID)
	case effectHaste:
		remove(attribute.AttackSpeed, hasteModifierID)
	case effectStrength:
		remove(attribute.AttackDamage, strengthModifierID)
	case effectJumpBoost:
		remove(attribute.SafeFallDistance, jumpBoostModifierID)
	case effectAbsorption:
		remove(attribute.MaxAbsorption, absorptionModifierID)
		e.setAbsorptionAmount(e.getAbsorptionAmount())
	}
}

// tickMobEffects ports LivingEntity.tickEffects for a mob: for each active effect, run its per-tick apply
// (regeneration heal), then count the duration down and remove it at 0. Called once per tick per live
// witch from the tick loop (witch-gated in tick_phases.go, sibling of tickPlayerEffects). No modifiers are
// attached for the witch self-buffs in v1 (SPEED's MOVEMENT_SPEED buff is a movement-subsystem read
// deferred, like the player-side slowness no-op), so removal is a plain delete.
func (t *TickLoop) tickMobEffects(e *Entity) {
	if e == nil || len(e.mobEffects) == 0 {
		return
	}
	for id, ef := range e.mobEffects {
		if !activeEffectHasRemaining(ef) {
			delete(e.mobEffects, id)
			t.removeEntityEffectModifiers(e, id)
			t.onEntityEffectRemoved(e, id)
			continue
		}
		remaining := ef.duration
		if remaining == -1 {
			remaining = 1 << 30
		}
		if entityEffectShouldApplyThisTick(id, remaining, ef.amplifier) {
			if !t.applyEntityEffectTick(e, id, ef.amplifier) {
				delete(e.mobEffects, id)
				t.removeEntityEffectModifiers(e, id)
				t.onEntityEffectRemoved(e, id)
				continue
			}
		}
		oldAmp := ef.amplifier
		tickDownActiveEffect(ef)
		if downgradeActiveEffect(ef) && oldAmp != ef.amplifier {
			t.removeEntityEffectModifiers(e, id)
			t.applyEntityEffectModifiers(e, id, ef.amplifier)
		}
		if !activeEffectHasRemaining(ef) {
			delete(e.mobEffects, id)
			t.removeEntityEffectModifiers(e, id)
			t.onEntityEffectRemoved(e, id)
		}
	}
}

// entityEffectShouldApplyThisTick ports MobEffect.shouldApplyEffectTickThisTick for the witch self-buffs.
func entityEffectShouldApplyThisTick(id string, remaining, amplifier int) bool {
	switch id {
	case effectPoison:
		interval := 25 >> amplifier
		if interval <= 0 {
			return true
		}
		return remaining%interval == 0
	case effectWither:
		interval := 40 >> amplifier
		if interval <= 0 {
			return true
		}
		return remaining%interval == 0
	case effectRegeneration:
		interval := 50 >> amplifier // RegenerationMobEffect: 50>>amp (amp0=50, amp1=25, ...)
		if interval <= 0 {
			return true
		}
		return remaining%interval == 0
	case effectAbsorption:
		return true
	case effectHunger:
		return true
	default:
		return false // speed/water_breathing/fire_resistance never tick (pure presence)
	}
}

// applyEntityEffectTick ports MobEffect.applyEffectTick for the witch's periodic self-buffs.
func (t *TickLoop) applyEntityEffectTick(e *Entity, id string, amplifier int) bool {
	switch id {
	case effectPoison:
		if e.health > 1.0 {
			t.applyDamageEntity(e, damageSourceMagic(), 1.0)
		}
		return true
	case effectWither:
		t.applyDamageEntity(e, damageSourceWither(), 1.0)
		return true
	case effectRegeneration:
		// RegenerationMobEffect: if health < maxHealth heal 1.0.
		if e.health < float32(e.getAttributeValue(attribute.MaxHealth)) {
			entityHeal(e, 1.0)
		}
		return true
	case effectAbsorption:
		return e.getAbsorptionAmount() > 0.0
	case effectHunger:
		return true
	}
	return true
}

// entityHeal ports LivingEntity.heal(float): if health>0, setHealth(health+heal) clamped to [0,maxHealth].
// Cite LivingEntity.heal + setHealth (Mth.clamp(health, 0, getMaxHealth())).
func entityHeal(e *Entity, heal float32) {
	if e.health <= 0 {
		return
	}
	maxH := float32(e.getAttributeValue(attribute.MaxHealth))
	h := e.health + heal
	if h > maxH {
		h = maxH
	}
	if h < 0 {
		h = 0
	}
	e.health = h
}

// markEntityAttrDirty flags a mob attribute (by ATTRIBUTE registry name) for the next sync flush
// (AttributeMap.attributesToSync.add). Lazily allocates the set so a never-modified mob keeps a nil
// attrDirty and emits ZERO attribute packets.
func markEntityAttrDirty(e *Entity, name string) {
	if e == nil {
		return
	}
	if e.attrDirty == nil {
		e.attrDirty = make(map[string]bool)
	}
	e.attrDirty[name] = true
}

// drainEntityAttrDirty returns one attrSnapshot per dirty mob attribute (its current BASE value + the
// active modifier list) and CLEARS the dirty set — the port of ServerEntity.sendChanges draining
// AttributeMap.getAttributesToSync(). Reads the live AttributeInstance so the base + modifier set are
// exactly what the fold sees. Returns nil when nothing is dirty or the mob has no AttributeMap.
func drainEntityAttrDirty(e *Entity) []attrSnapshot {
	if e == nil || len(e.attrDirty) == 0 {
		return nil
	}
	snaps := make([]attrSnapshot, 0, len(e.attrDirty))
	for name := range e.attrDirty {
		if e.attributes == nil {
			continue
		}
		inst := e.attributes.GetInstance(name)
		if inst == nil {
			continue
		}
		snaps = append(snaps, attrSnapshot{
			name:      name,
			baseValue: inst.BaseValue(),
			modifiers: inst.Modifiers(),
		})
	}
	e.attrDirty = nil
	return snaps
}

// entityModifiedAttrs returns one attrSnapshot per mob attribute that currently carries at least one
// modifier — the subset ServerEntity.sendPairingData would sync to a newly-tracking client that a
// modifier-free client would not already know from the entity-type default. A mob with NO modified
// attribute (the pig oracle) returns nil, so its spawn emits ZERO UpdateAttributes packets (preserving
// the byte-identical default spawn). Read on the tick OWNER (snapshotEntity) — never off-tick.
func entityModifiedAttrs(e *Entity) []attrSnapshot {
	if e == nil || e.attributes == nil {
		return nil
	}
	var snaps []attrSnapshot
	for name, inst := range e.attributes.LocalInstances() {
		if inst == nil {
			continue
		}
		mods := inst.Modifiers()
		if len(mods) == 0 {
			continue // only attributes carrying a modifier need a spawn sync (base defaults are client-known)
		}
		snaps = append(snaps, attrSnapshot{name: name, baseValue: inst.BaseValue(), modifiers: mods})
	}
	return snaps
}
