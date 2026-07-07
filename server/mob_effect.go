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
	effectHaste      = "minecraft:haste"      // MobEffects.HASTE      (ATTACK_SPEED       +0.1 ADD_MULTIPLIED_TOTAL)
	effectResistance = "minecraft:resistance" // MobEffects.RESISTANCE (no attribute modifier; damage-reduction)
	effectJumpBoost  = "minecraft:jump_boost" // MobEffects.JUMP_BOOST (SAFE_FALL_DISTANCE +1.0 ADD_VALUE)
	effectStrength   = "minecraft:strength"   // MobEffects.STRENGTH   (ATTACK_DAMAGE      +3.0 ADD_VALUE)
	// CONDUIT effect id (CONDUIT-01, ConduitBlockEntity.applyEffects): the beneficial power an active conduit
	// grants a submerged/rained-on player in range. VERIFIED CFR MobEffects.CONDUIT_POWER =
	// register("conduit_power", new MobEffect(MobEffectCategory.BENEFICIAL, 1950417)) — a plain duration
	// effect with NO attribute modifiers (the underwater vision/breathing/mining bonuses are applied by
	// dedicated ConduitPower checks elsewhere, not by an attribute modifier on the effect), so addPlayerEffect
	// inserts it as a bare duration effect.
	effectConduitPower = "minecraft:conduit_power"
	// WITHER effect id (WitherSkull.onHitEntity applies it on a NORMAL/HARD hit): MobEffects.WITHER =
	// register("wither", new MobEffect(MobEffectCategory.HARMFUL, ...)). A duration effect whose per-tick
	// damage-over-time (WitherMobEffect.applyEffectTick: hurt 1.0 magic every 40>>amp ticks) is CITE-DEFERRED
	// — like the movement-buff no-ops, presence is stored via addEntityEffect and the periodic DoT lands when
	// the mob effect-tick table grows a wither branch. The 8.0 skull hit is the visible gameplay here.
	effectWither = "minecraft:wither"
)

// modifier ids (stable identity per effect, matching the vanilla effect.<name> ids).
const (
	slownessModifierID = "effect.slowness"
	weaknessModifierID = "effect.weakness"
	// BEACON effect modifier ids (VERIFIED CFR MobEffects.*: Identifier.withDefaultNamespace("effect.<name>")).
	speedModifierID     = "effect.speed"      // SPEED     -> MOVEMENT_SPEED
	hasteModifierID     = "effect.haste"      // HASTE     -> ATTACK_SPEED
	strengthModifierID  = "effect.strength"   // STRENGTH  -> ATTACK_DAMAGE
	jumpBoostModifierID = "effect.jump_boost" // JUMP_BOOST-> SAFE_FALL_DISTANCE
)

// activeEffect is the Go stand-in for MobEffectInstance (the fields the witch slice needs): the effect id,
// the remaining duration (ticks, counting down), and the amplifier (0-based).
type activeEffect struct {
	id        string
	duration  int
	amplifier int
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
	return id == effectInstantDamage
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
	// update merge: keep the stronger (higher amp) or, at equal amp, the longer duration.
	if existing, ok := p.activeEffects[id]; ok {
		if amplifier < existing.amplifier || (amplifier == existing.amplifier && duration <= existing.duration) {
			return
		}
		t.removeEffectModifiers(p, id) // re-apply the modifier at the new amplifier below
	}
	p.activeEffects[id] = &activeEffect{id: id, duration: duration, amplifier: amplifier}
	t.applyEffectModifiers(p, id, amplifier)
}

// applyInstantEffect is the port of HealOrHarmMobEffect.applyInstantaneousEffect for a player victim: the
// harm amount = (int)(scale*(6<<amp)+0.5), dealt as indirect_magic (attributed to the thrower) or magic.
func (t *TickLoop) applyInstantEffect(p *tickPlayer, ownerID int32, id string, amplifier int, scale float64) {
	switch id {
	case effectInstantDamage:
		amount := int(scale*float64(int(6)<<uint(amplifier)) + 0.5)
		src := damageSourceMagic()
		if ownerID != 0 {
			src = damageSourceIndirectMagic(ownerID)
		}
		t.applyDamage(p, src, float32(amount))
	}
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
			Amount:    -0.15 * float64(amplifier+1),
			Operation: attribute.AddMultipliedTotal,
		})
	case effectWeakness:
		h.addModifier(attrAttackDamage, attribute.AttributeModifier{
			ID:        weaknessModifierID,
			Amount:    -4.0 * float64(amplifier+1),
			Operation: attribute.AddValue,
		})
	case effectSpeed:
		// MobEffects.SPEED: MOVEMENT_SPEED +0.2*(amp+1) ADD_MULTIPLIED_TOTAL (effect.speed).
		h.addModifier(attrMovementSpeed, attribute.AttributeModifier{
			ID:        speedModifierID,
			Amount:    0.2 * float64(amplifier+1),
			Operation: attribute.AddMultipliedTotal,
		})
	case effectHaste:
		// MobEffects.HASTE: ATTACK_SPEED +0.1*(amp+1) ADD_MULTIPLIED_TOTAL (effect.haste).
		h.addModifier(attrAttackSpeed, attribute.AttributeModifier{
			ID:        hasteModifierID,
			Amount:    0.1 * float64(amplifier+1),
			Operation: attribute.AddMultipliedTotal,
		})
	case effectStrength:
		// MobEffects.STRENGTH: ATTACK_DAMAGE +3.0*(amp+1) ADD_VALUE (effect.strength).
		h.addModifier(attrAttackDamage, attribute.AttributeModifier{
			ID:        strengthModifierID,
			Amount:    3.0 * float64(amplifier+1),
			Operation: attribute.AddValue,
		})
	case effectJumpBoost:
		// MobEffects.JUMP_BOOST: SAFE_FALL_DISTANCE +1.0*(amp+1) ADD_VALUE (effect.jump_boost).
		h.addModifier(attrSafeFallDistance, attribute.AttributeModifier{
			ID:        jumpBoostModifierID,
			Amount:    1.0 * float64(amplifier+1),
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
	case effectStrength:
		p.attributes.removeModifier(attrAttackDamage, strengthModifierID)
	case effectJumpBoost:
		p.attributes.removeModifier(attrSafeFallDistance, jumpBoostModifierID)
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
		// MobEffectInstance.tickServer: if shouldApply, run applyEffectTick; a false return means the effect
		// consumed itself (BAD_OMEN converting / RAID_OMEN firing) -> remove it now and skip the countdown.
		// tickCount passed to shouldApplyEffectTickThisTick is the remaining duration (counting DOWN).
		if effectShouldApplyThisTick(id, e.duration, e.amplifier) {
			if !t.applyEffectTick(p, id, e.amplifier) {
				delete(p.activeEffects, id)
				t.removeEffectModifiers(p, id)
				continue
			}
		}
		e.duration--
		if e.duration <= 0 {
			delete(p.activeEffects, id)
			t.removeEffectModifiers(p, id)
		}
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
	default:
		return false // slowness/weakness/speed/haste/strength/jump_boost/resistance never tick (pure modifiers)
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
	if serverDifficulty == difficultyPeaceful {
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

// addEntityEffect ports LivingEntity.addEffect for a mob self-target: an INSTANT effect (HEALING) applies
// its one-shot amount immediately; a DURATION effect is inserted into the mobEffects map (keeping the
// stronger/longer on a same-id collision). scale is fixed 1.0 for the self-drink (potion.forEachEffect).
// Cite LivingEntity.addEffect + onEffectAdded.
func (t *TickLoop) addEntityEffect(e *Entity, id string, duration, amplifier int) {
	if e == nil || !e.isAlive() || e.dead {
		return // isAffectedByPotions == !isDeadOrDying()
	}
	if isInstantEntityEffect(id) {
		t.applyInstantEntityEffect(e, id, amplifier)
		return
	}
	if e.mobEffects == nil {
		e.mobEffects = make(map[string]*activeEffect)
	}
	if existing, ok := e.mobEffects[id]; ok {
		if amplifier < existing.amplifier || (amplifier == existing.amplifier && duration <= existing.duration) {
			return
		}
	}
	e.mobEffects[id] = &activeEffect{id: id, duration: duration, amplifier: amplifier}
}

// applyInstantEntityEffect ports HealOrHarmMobEffect.applyInstantaneousEffect for a self-drinking mob:
// HEALING (!isHarm, self scale 1.0) heals (int)(1.0*(4<<amp)+0.5). isInvertedHealAndHarm()==false (the
// witch is not undead), so HEALING heals. Cite HealOrHarmMobEffect.applyInstantaneousEffect + heal.
func (t *TickLoop) applyInstantEntityEffect(e *Entity, id string, amplifier int) {
	switch id {
	case effectInstantHealth:
		amount := int(1.0*float64(int(4)<<uint(amplifier)) + 0.5)
		entityHeal(e, float32(amount))
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
		if entityEffectShouldApplyThisTick(id, ef.duration, ef.amplifier) {
			t.applyEntityEffectTick(e, id, ef.amplifier)
		}
		ef.duration--
		if ef.duration <= 0 {
			delete(e.mobEffects, id)
		}
	}
}

// entityEffectShouldApplyThisTick ports MobEffect.shouldApplyEffectTickThisTick for the witch self-buffs.
func entityEffectShouldApplyThisTick(id string, remaining, amplifier int) bool {
	switch id {
	case effectRegeneration:
		interval := 50 >> amplifier // RegenerationMobEffect: 50>>amp (amp0=50, amp1=25, ...)
		if interval <= 0 {
			return true
		}
		return remaining%interval == 0
	default:
		return false // speed/water_breathing/fire_resistance never tick (pure presence)
	}
}

// applyEntityEffectTick ports MobEffect.applyEffectTick for the witch's periodic self-buffs.
func (t *TickLoop) applyEntityEffectTick(e *Entity, id string, amplifier int) {
	switch id {
	case effectRegeneration:
		// RegenerationMobEffect: if health < maxHealth heal 1.0.
		if e.health < float32(e.getAttributeValue(attribute.MaxHealth)) {
			entityHeal(e, 1.0)
		}
	}
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
