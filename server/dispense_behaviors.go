package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// dispense_behaviors.go ports the DispenserBlock.DISPENSER_REGISTRY special behaviors: the non-DEFAULT
// per-item DispenseItemBehavior entries DispenseItemBehavior.bootStrap() registers (net.minecraft.core
// .dispenser.*, temp/cache/26.2-inner.jar, javap -c -p this session). The DEFAULT loose-item eject lives
// in dispenser.go (dispenseDefaultBehavior); this file is the getDispenseMethod override table.
//
// PORTED (1:1, cited constants):
//   - ProjectileDispenseBehavior for ARROW / SNOWBALL / EGG. DEFAULT DispenseConfig (ProjectileItem
//     .DispenseConfig.Builder defaults, VERIFIED javap): power 1.1f, uncertainty 6.0f, positionFunction =
//     getDispensePosition(source, 0.7, Vec3(0,0.1,0)). Shoot velocity (Projectile.getMovementToShoot,
//     VERIFIED javap): normalize(step).add(triangle(0, 0.0172275*unc) x3).scale(power). The three triangle
//     draws are on the PROJECTILE own random (this.random) -- a dispensed projectile has no living owner,
//     so we seed a dedicated per-entity stream from the fresh entity id (the orbRNG/fishingRNG pattern),
//     NEVER the level or a mob stream, so the pinned pig-oracle streams are unperturbed.
//   - FlintAndSteelDispenseItemBehavior (OptionalDispenseItemBehavior): ignite the cell in FACING. The
//     BaseFireBlock.canBePlacedAt fire branch (igniteFireAt) and the TntBlock.prime branch are ported;
//     tryIgniteExplosiveEntities (entity-AABB) and campfire/candle canLight are cited deferrals. On success
//     the flint-and-steel wears by 1 (stack.hurtAndBreak(1)).
//
// DEFERRED (cited -- need a not-yet-built subsystem, NOT a paraphrase): FIRE_CHARGE/POTION/EXP_BOTTLE/
// FIREWORK/SPECTRAL+TIPPED ARROW/WIND_CHARGE projectiles (fire-charge draws the LEVEL random; the rest need
// effect/firework wiring); EquipmentDispenseItemBehavior / ShearsDispenseItemBehavior / dye (need a
// nearby-entity AABB scan); SpawnEggItemBehavior / Boat / Minecart / ShulkerBox / buckets / glass-bottle /
// bonemeal / head+pumpkin (block-place / mob-spawn variants, each its own port on this same seam).
//
// CITE: DispenserBlock.getDispenseMethod / DISPENSER_REGISTRY; ProjectileDispenseBehavior.execute;
//   ProjectileItem.DispenseConfig.Builder; Projectile.getMovementToShoot / spawnProjectileUsingShoot;
//   FlintAndSteelDispenseItemBehavior.execute; OptionalDispenseItemBehavior.

// dispenseProjectilePower is the DEFAULT ProjectileItem.DispenseConfig power (1.1f). CITE: ProjectileItem
// .DispenseConfig.Builder init (ldc 1.1f -> power). Used by ARROW / SNOWBALL / EGG.
const dispenseProjectilePower = 1.1

// dispenseProjectileUncertainty is the DEFAULT ProjectileItem.DispenseConfig uncertainty (6.0f). CITE:
// ProjectileItem.DispenseConfig.Builder init (ldc 6.0f -> uncertainty).
const dispenseProjectileUncertainty = 6.0

// dispenseProjectilePosScale is the DEFAULT position-function FACING scale (0.7d). CITE: ProjectileItem
// .DispenseConfig.Builder.lambda-new-0 (getDispensePosition(source, 0.7, Vec3(0,0.1,0))).
const dispenseProjectilePosScale = 0.7

// dispenseProjectilePosYOffset is the DEFAULT position-function y offset (0.1d) added AFTER the FACING
// step. CITE: ProjectileItem.DispenseConfig.Builder.lambda-new-0 (new Vec3(0.0, 0.1, 0.0)).
const dispenseProjectilePosYOffset = 0.1

// dispenseShootSpreadUnit is the 0.0172275 constant Projectile.getMovementToShoot multiplies by the
// uncertainty for each triangle spread draw. CITE: Projectile.getMovementToShoot (ldc2_w 0.0172275).
const dispenseShootSpreadUnit = 0.0172275

// dispenseArrowBaseDamage is the default Arrow base damage (a shooter-less dispensed Arrow carries the
// vanilla default). Mirrors bowArrowBaseDamage (bow.go, 2.0). CITE: ArrowItem.asProjectile (default Arrow
// ctor -> AbstractArrow default baseDamage 2.0).
const dispenseArrowBaseDamage = 2.0

// dispenseSpecialBehavior is DispenserBlock.getDispenseMethod non-DEFAULT branch: dispatch the held item
// to its registered DispenseItemBehavior. Returns (remainder, true) when a special behavior fired (the
// caller writes the remainder back to the slot); (_, false) to fall through to the DEFAULT eject.
//
// 1:1 net.minecraft.world.level.block.DispenserBlock.getDispenseMethod (DISPENSER_REGISTRY lookup).
func (t *TickLoop) dispenseSpecialBehavior(pos pk.Position, state block.StateID, facing block.Direction, stack component.SlotData) (component.SlotData, bool) {
	if stackEmpty(stack) {
		return stack, false
	}
	switch item.ID(int32(stack.ItemID)) {
	case item.Arrow.ID, item.Snowball.ID, item.Egg.ID:
		return t.dispenseProjectile(pos, state, facing, stack), true
	case item.FlintAndSteel.ID:
		return t.dispenseFlintAndSteel(pos, facing, stack), true
	}
	return stack, false
}

// dispenseProjectile ports ProjectileDispenseBehavior.execute for the DEFAULT-config projectiles (arrow /
// snowball / egg): spawn the projectile at the dispense position, shoot it out the FACING with the DEFAULT
// power/uncertainty, shrink the source stack by 1, and return the remainder.
//
//	position = center + 0.7*FACING + Vec3(0, 0.1, 0);
//	projectile = asProjectile(level, position, stack, FACING);
//	spawnProjectileUsingShoot(projectile, level, stack, stepX, stepY, stepZ, 1.1f, 6.0f);
//	stack.shrink(1); return stack.
//
// 1:1 net.minecraft.core.dispenser.ProjectileDispenseBehavior.execute (+ Projectile.getMovementToShoot).
func (t *TickLoop) dispenseProjectile(pos pk.Position, _ block.StateID, facing block.Direction, stack component.SlotData) component.SlotData {
	sx, sy, sz := dirVec(facing)

	// position = center + 0.7*FACING + Vec3(0, 0.1, 0). center = (X+0.5, Y+0.5, Z+0.5).
	posX := float64(pos.X) + 0.5 + dispenseProjectilePosScale*float64(sx)
	posY := float64(pos.Y) + 0.5 + dispenseProjectilePosScale*float64(sy) + dispenseProjectilePosYOffset
	posZ := float64(pos.Z) + 0.5 + dispenseProjectilePosScale*float64(sz)

	// A dispensed projectile has no living owner; seed a dedicated per-entity stream from the fresh entity
	// id (the orbRNG / fishingRNG pattern) so the three triangle draws are draw-order-faithful yet NEVER
	// touch the level / a mob stream. Allocate the id up front so the seed matches the spawned entity.
	id := t.idAlloc.AllocID()
	rng := newEntityRandom(uint64(id))

	// getMovementToShoot: normalize(step).add(triangle(0, 0.0172275*unc) x3).scale(power).
	vx, vy, vz := dispenseMovementToShoot(rng, float64(sx), float64(sy), float64(sz),
		dispenseProjectilePower, dispenseProjectileUncertainty)

	switch item.ID(int32(stack.ItemID)) {
	case item.Arrow.ID:
		// asProjectile: new Arrow(level, x, y, z, stack.copyWithCount(1), null); pickup ALLOWED; no owner.
		t.withRegion(t.regionForColumn(columnOf(posX, posZ)), func() {
			t.spawnArrow(-1, posX, posY, posZ, vx, vy, vz, dispenseArrowBaseDamage)
		})
	case item.Snowball.ID:
		t.withRegion(t.regionForColumn(columnOf(posX, posZ)), func() {
			t.spawnThrowable(-1, throwSnowball, posX, posY, posZ, vx, vy, vz)
		})
	case item.Egg.ID:
		t.withRegion(t.regionForColumn(columnOf(posX, posZ)), func() {
			t.spawnThrowable(-1, throwEgg, posX, posY, posZ, vx, vy, vz)
		})
	}

	// levelEvent(dispenseConfig.overrideDispenseEvent().orElse(1000)) -- the dispense sound; cited no-op.
	t.dispenserLevelEvent(pos, 1000)

	// stack.shrink(1); return stack.
	work := stack
	work.Count = toVar(int(work.Count) - 1)
	if work.Count <= 0 {
		return component.SlotData{Count: 0}
	}
	return work
}

// dispenseMovementToShoot ports Projectile.getMovementToShoot(x, y, z, power, uncertainty):
// normalize(x,y,z).add(triangle(0, 0.0172275*unc) x3).scale(power). Three triangle draws in x,y,z order on
// the projectile own random. VERIFIED javap Projectile.getMovementToShoot (Vec3.normalize; triangle x3 with
// dconst_0 center, ldc2_w 0.0172275 * unc deviation; Vec3.add; Vec3.scale power).
func dispenseMovementToShoot(rng *entityRandom, x, y, z, power, uncertainty float64) (float64, float64, float64) {
	// Vec3.normalize(): scale by 1/length, or ZERO when length < 1e-4 (Vec3.normalize epsilon).
	length := math.Sqrt(x*x + y*y + z*z)
	if length < 1.0e-4 {
		x, y, z = 0, 0, 0
	} else {
		x, y, z = x/length, y/length, z/length
	}
	dev := dispenseShootSpreadUnit * uncertainty
	// triangle(0, dev) = 0 + dev*(nextDouble() - nextDouble()); three draws in x,y,z order.
	x += dispenseTriangle(rng, 0.0, dev)
	y += dispenseTriangle(rng, 0.0, dev)
	z += dispenseTriangle(rng, 0.0, dev)
	return x * power, y * power, z * power
}

// dispenseTriangle is RandomSource.triangle(center, deviation) = center + deviation*(nextDouble() -
// nextDouble()). Two nextDouble draws in that order. CITE: RandomSource.triangle.
func dispenseTriangle(rng *entityRandom, center, deviation float64) float64 {
	a := rng.nextDouble()
	b := rng.nextDouble()
	return center + deviation*(a-b)
}

// dispenseFlintAndSteel ports FlintAndSteelDispenseItemBehavior.execute: ignite the cell in FACING. On a
// valid fire cell it lights a fire (igniteFireAt); on a TNT block it primes it and clears the block. On any
// success it wears the flint-and-steel by 1 (stack.hurtAndBreak(1)); a miss returns the stack unchanged and
// posts the fail sound. The tryIgniteExplosiveEntities (entity-AABB) and campfire/candle canLight
// sub-branches are cited deferrals (no nearby-entity scan / generic LIT setter seam here).
//
// 1:1 net.minecraft.core.dispenser.FlintAndSteelDispenseItemBehavior.execute (+ OptionalDispenseItemBehavior).
func (t *TickLoop) dispenseFlintAndSteel(pos pk.Position, facing block.Direction, stack component.SlotData) component.SlotData {
	if t.world() == nil {
		return stack
	}
	target := relative(pos, facing)

	success := false
	// (2) BaseFireBlock.canBePlacedAt(level, target, direction): igniteFireAt requires target air +
	// canSurvive + writes BaseFireBlock.getState + schedules the FireBlock tick (the same setBlockAndUpdate
	// the dispense behavior does). It returns true iff a fire was lit.
	if t.portalAirAt(target) {
		var lit bool
		t.withRegion(t.regionForColumn(columnOf(float64(target.X)+0.5, float64(target.Z)+0.5)), func() {
			lit = t.igniteFireAt(target)
		})
		if lit {
			success = true
		}
	}

	// (4) TntBlock: TntBlock.prime(level, target) + setBlock(target, AIR). Only when the fire branch did
	// not already consume the cell.
	if !success {
		if targetState, ok := t.world().GetBlock(target, dimMinY); ok && isTntBlock(targetState) {
			if t.primeTntBlock(target) {
				air := t.airState()
				if t.world().SetBlock(target, air, dimMinY) {
					t.broadcastBlockUpdate(target, air)
					t.onBlockTickEdit(target)
				}
				success = true
			}
		}
	}

	if !success {
		// setSuccess(false): the fail sound (levelEvent 1001), a cited no-op.
		t.dispenserLevelEvent(pos, 1001)
		return stack
	}

	// setSuccess(true): the dispense sound (levelEvent 1000), a cited no-op; then stack.hurtAndBreak(1).
	t.dispenserLevelEvent(pos, 1000)
	worn, _ := t.stackHurtAndBreak(stack, 1, false)
	return worn
}
