// Nautilus family + Mannequin (NEW in 26.2) -- 1:1 ports verified against the unobfuscated 26.2 jar.
//
// AbstractNautilus (net.minecraft.world.entity.animal.nautilus.AbstractNautilus) is a brain-driven
// TamableAnimal aquatic mount: it implements PlayerRideableJumping + HasCustomInventoryScreen (a
// saddle-and-inventory rideable, like a horse but underwater). createAttributes (verified javap this
// session): Animal.createAnimalAttributes() + MAX_HEALTH 15.0 + MOVEMENT_SPEED 1.0 + ATTACK_DAMAGE 3.0 +
// KNOCKBACK_RESISTANCE 0.30000001192092896; isPushedByFluid() returns false. Nautilus uses the base
// attributes unchanged (plus a custom underwater air-supply); ZombieNautilus is the zombified variant with
// MOVEMENT_SPEED overridden to 1.100000023841858 (its variant temperate/warm skin is data-driven).
//
// The full vanilla brain (ZombieNautilusAi / NautilusAi behavior packages), the rideable mount packet path,
// and the custom inventory screen are the DEFERRED subsystems. What lands here 1:1: the entity's attributes
// (from the generated supplier, defaults.go), its spawn wiring, health seed, undead classification for
// ZombieNautilus, and a bounded passive swim AI (the existing water-mob goal set) so the mob is visibly
// alive in water. This is the same "faithful attributes + spawn + bounded goal walk, brain deferred" shape
// used for the other water mobs (water_mobs.go). Cite AbstractNautilus / Nautilus / ZombieNautilus.
//
// Mannequin (net.minecraft.world.entity.decoration.Mannequin) extends Avatar (a LivingEntity) -- it is a
// player-shaped DISPLAY entity, not an AI mob: it carries a ResolvableProfile (DATA_PROFILE), an immovable
// flag (DATA_IMMOVABLE), a description (DATA_DESCRIPTION) and the shown PlayerModelParts, and it has no
// goal AI (isEffectiveAi is a display gate). Per the task, we port its spawn + data (the immovable flag),
// not AI. Cite Mannequin.

package server

import "github.com/imhinotori/sulfur/data/entity"

// spawnNautilus creates a Nautilus (the tameable aquatic mount). Fixed MAX_HEALTH 15.0 / MOVEMENT_SPEED 1.0
// / ATTACK_DAMAGE 3.0 / KNOCKBACK_RESISTANCE 0.3 from the supplier (defaults.go). Marked isWaterMob so it
// uses the aquatic breath path (it breathes water) and isNautilus for its bounded swim tick. The brain,
// rideable mount, and custom inventory are DEFERRED. Cite Nautilus(EntityType, Level) +
// AbstractNautilus.createAttributes.
func (t *TickLoop) spawnNautilus(x, y, z float64) *Entity {
	n := NewEntity(t.idAlloc.AllocID(), entity.Nautilus, x, y, z)
	n.isWaterMob = true
	n.isNautilus = true
	initSpawnHealth(n)        // setHealth(getMaxHealth()) -> 15.0
	n.ai = newWaterMobAI(1.0) // AbstractNautilus MOVEMENT_SPEED 1.0
	reseedMobAI(n.ai, n.id)
	owner := t.regionForEntity(n)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(n)
	return n
}

// spawnZombieNautilus creates a ZombieNautilus (the zombified aquatic mount). Same AbstractNautilus base as
// Nautilus except MOVEMENT_SPEED overridden to 1.1 (supplier). Marked isZombieNautilus (undead
// classification: the breath/effect gates treat it like the other undead) + isWaterMob + isNautilus for the
// swim tick. The zombie->? conversion / variant skin + brain are DEFERRED. Cite ZombieNautilus(EntityType,
// Level) + ZombieNautilus.createAttributes.
func (t *TickLoop) spawnZombieNautilus(x, y, z float64) *Entity {
	n := NewEntity(t.idAlloc.AllocID(), entity.ZombieNautilus, x, y, z)
	n.isWaterMob = true
	n.isNautilus = true
	n.isZombieNautilus = true
	initSpawnHealth(n)                      // setHealth(getMaxHealth()) -> 15.0
	n.ai = newWaterMobAI(1.100000023841858) // ZombieNautilus MOVEMENT_SPEED override
	reseedMobAI(n.ai, n.id)
	owner := t.regionForEntity(n)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(n)
	return n
}

// spawnMannequin creates a Mannequin (the player-shaped, no-AI display entity, Avatar subclass). It gets no
// goal AI -- it is a static display. Its health is seeded from the living-default MaxHealth (Avatar carries
// no createAttributes override, so it folds the registration default via the nil/attribute fallback).
// immovable mirrors DATA_IMMOVABLE (a placed mannequin is not pushed). The ResolvableProfile / description /
// skin data are DEFERRED. Cite Mannequin(EntityType, Level) + Mannequin.setImmovable.
func (t *TickLoop) spawnMannequin(x, y, z float64, immovable bool) *Entity {
	m := NewEntity(t.idAlloc.AllocID(), entity.Mannequin, x, y, z)
	m.isMannequin = true
	m.mannequinImmovable = immovable
	initSpawnHealth(m) // setHealth(getMaxHealth())
	owner := t.regionForEntity(m)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(m)
	return m
}
