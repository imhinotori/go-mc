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
)

// modifier ids (stable identity per effect, matching the vanilla effect.<name> ids).
const (
	slownessModifierID = "effect.slowness"
	weaknessModifierID = "effect.weakness"
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
		// tickCount passed to shouldApplyEffectTickThisTick is the remaining duration (counting DOWN).
		if effectShouldApplyThisTick(id, e.duration, e.amplifier) {
			t.applyEffectTick(p, id, e.amplifier)
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
	default:
		return false // slowness/weakness never tick (pure attribute modifiers)
	}
}

// applyEffectTick is the port of MobEffect.applyEffectTick for the witch's periodic effects.
func (t *TickLoop) applyEffectTick(p *tickPlayer, id string, amplifier int) {
	switch id {
	case effectPoison:
		// PoisonMobEffect: if health > 1.0 hurt magic 1.0 (poison never kills).
		if p.health > 1.0 {
			t.applyDamage(p, damageSourceMagic(), 1.0)
		}
	}
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
