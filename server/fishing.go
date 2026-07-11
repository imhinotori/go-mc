package server

// fishing.go — the FISHING ROD + FISHING HOOK (bobber) subsystem, a 1:1 port of the unobfuscated
// Minecraft 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session):
//
//   - net.minecraft.world.item.FishingRodItem.use(level, player, hand): if the player has a hook out
//     -> hook.retrieve(stack) (reel: pull/loot) + discard; else -> spawn a new FishingHook (the cast).
//   - net.minecraft.world.entity.projectile.FishingHook.<init>(Player, Level, luck, lureSpeed): the
//     cast velocity from the player's look + a per-axis triangle spread.
//   - FishingHook.tick: the FLYING -> BOBBING state machine (fluid float, catchingFish countdowns).
//   - FishingHook.catchingFish(pos): timeUntilLured / timeUntilHooked / nibble countdowns + the bite.
//   - FishingHook.retrieve(stack): pull a hooked entity, else roll the FISHING loot table + spawn the
//     caught ItemEntity flying to the owner + award an XP orb.
//   - FishingHook.calculateOpenWater(pos): the 5x5x4 open-water test that gates treasure loot.
//
// RNG ISOLATION (the pig-oracle mandate): the bobber draws ALL its fishing RNG from a DEDICATED
// per-bobber entityRandom (fishingRNG) seeded from the bobber's entity id — NEVER a mob's per-entity
// stream and never the level stream. A bobber only exists after a rod cast, so the pig oracle (which
// casts no rod) draws nothing new and stays byte-identical. The bobber tick is gated on isFishingHook.
//
// SCOPE / cited deferrals:
//   - The catch/bite PARTICLES (bubble trail, splash, FISHING) and the fishing SOUNDS
//     (FISHING_BOBBER_THROW/RETRIEVE/SPLASH) are client cosmetics — CITE-DEFERRED (the timer + catch +
//     loot are the target). The RNG the vanilla particle branches would draw is NOT consumed here where
//     it is a pure cosmetic (the catchingFish RNG draws that DO gate state — the fishAngle triangle, the
//     teaseChance roll, the nibble reset — are all preserved in order; only the sendParticles calls are
//     dropped, and sendParticles draws no RNG).
//   - The Lure (lureSpeed) and Luck of the Sea (luck) ENCHANT reads are WIRED (enchant_effects.go:
//     enchFishingTimeReduction / enchFishingLuckBonus) — the data-driven FISHING_TIME_REDUCTION /
//     FISHING_LUCK_BONUS value effects folded over the held rod's enchantments, exactly
//     EnchantmentHelper.getFishingTimeReduction/getFishingLuckBonus. An UN-enchanted rod folds zero
//     entries -> luck 0 / lureSpeed 0 (the vanilla no-enchant default), the pig-oracle-safe path.
//   - The DATA_HOOKED_ENTITY / DATA_BITING synced metadata (the client's taut-line + bob visuals) is
//     CITE-DEFERRED (client cosmetic); the server-side biting flag + hooked id drive the gameplay.
//   - Durability hurt on the rod (FishingRodItem's hurtAndBreak) is WIRED (hurtHeldItem, durability.go): the
//     rod wears by the retrieve return (entity 5 / item 3 / loot 1 / on-ground 2) and breaks at max.
//   - Hooking a MOB/ItemEntity (checkCollision -> onHitEntity -> setHookedEntity) is CITE-DEFERRED
//     (needs the swept entity-hit against non-player entities); the retrieve PULL of a hooked entity is
//     ported and fires when fishingHookedID is set. The FISH catch is the primary target and is REAL.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/loot"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// fishingRodItemID is the numeric item id of minecraft:fishing_rod (data/item: id 1082). The rod-use
// dispatch gates on the held item being this id.
const fishingRodItemID = 1082

// fishingOwnerLuck is Player.getLuck() — the LUCK attribute value. v1 has no LUCK attribute source
// (potions/beacons), so it is the vanilla default 0 (a cited stub; structured to read the real LUCK
// attribute when it lands). Cite LivingEntity.getLuck (getAttributeValue(Attributes.LUCK)).
func fishingOwnerLuck(owner *tickPlayer) float32 { return 0 }

// FishingHook constants (verified CFR/javap — exact values).
const (
	fishStateFlying    int32 = 0                       // FishHookState.FLYING
	fishStateHooked    int32 = 1                       // FishHookState.HOOKED_IN_ENTITY
	fishStateBobbing   int32 = 2                       // FishHookState.BOBBING
	fishMaxOutOfWater  int32 = 10                      // FishingHook.MAX_OUT_OF_WATER_TIME
	fishGroundDespawn  int32 = 1200                    // FishingHook.tick: onGround life>=1200 -> discard
	fishStopDistanceSq       = 1024.0                  // shouldStopFishing: distanceToSqr(owner) <= 1024.0
	fishInertia              = 0.92                    // FishingHook.tick: deltaMovement.scale(0.92)
	fishFlyGravity           = 0.03                    // FishingHook.tick: add(0, -0.03, 0) when airborne (not water/hooked)
	fishEyeHeight            = playerStandingEyeHeight // player.getEyeY() basis for the cast origin
)

// playerStandingEyeHeight is defined in breath.go (1.62). Referenced above via fishEyeHeight.

// fishTriangle is RandomSource.triangle(mode, deviation) == mode + deviation*(nextDouble()-nextDouble())
// (the 2-nextDouble draw ORDER is load-bearing for lockstep). Cite RandomSource.triangle.
func fishTriangle(r *entityRandom, mode, deviation float64) float64 {
	return mode + deviation*(r.nextDouble()-r.nextDouble())
}

// mthNextIntRange is Mth.nextInt(rng, lo, hi) == lo >= hi ? lo : lo + rng.nextInt(hi - lo + 1).
// Cite Mth.nextInt(RandomSource,int,int).
func mthNextIntRange(r *entityRandom, lo, hi int) int {
	if lo >= hi {
		return lo
	}
	return lo + r.nextInt(hi-lo+1)
}

// mthNextFloatRange is Mth.nextFloat(rng, lo, hi) == lo >= hi ? lo : lo + rng.nextFloat()*(hi-lo).
// Cite Mth.nextFloat(RandomSource,float,float).
func mthNextFloatRange(r *entityRandom, lo, hi float32) float32 {
	if lo >= hi {
		return lo
	}
	return lo + r.nextFloat()*(hi-lo)
}

// tryUseFishingRod is the FishingRodItem.use(level, player, hand) port, dispatched from useItemInHand
// BEFORE the food gate. If the held item is not a fishing rod it returns false (fall through). If the
// player already has a hook out, it reels (retrieve: pull/loot + discard the bobber); otherwise it
// casts a new FishingHook. The durability hurt + the throw/retrieve sounds are cited deferrals.
//
// Source: javap FishingRodItem.use (player.fishing != null ? retrieve+discard : new FishingHook + spawn).
func (t *TickLoop) tryUseFishingRod(p *tickPlayer, held component.SlotData, hand int32) bool {
	if int32(held.ItemID) != fishingRodItemID {
		return false
	}

	if p.fishingHookID != 0 {
		// Reel: hook.retrieve(stack) then discard the bobber (retrieve calls discard internally).
		hook := t.entityByIDAnyRegion(p.fishingHookID)
		if hook != nil {
			// FishingRodItem.use: itemStack.hurtAndBreak(hook.retrieve(itemStack), player, hand). The
			// retrieve return IS the durability damage (entity 5 / item-entity 3 / loot 1 / on-ground 2 / 0).
			dmg := t.fishingRetrieve(hook, p)
			if dmg > 0 {
				t.hurtHeldItem(p, ensureInventory(p), dmg)
			}
		} else {
			// The bobber vanished (e.g. discarded last tick); clear the stale link so the next
			// use casts fresh. (updateOwnerInfo(null) sets owner.fishing = null on discard.)
			p.fishingHookID = 0
		}
		// FishingRodItem.use: Level.playSound(FISHING_BOBBER_RETRIEVE ...) — cited deferral (cosmetic).
		return true
	}

	// Cast: new FishingHook(player, level, luck, lureSpeed) + Projectile.spawnProjectile. The ctor args
	// are read off the held rod's enchantments (FishingRodItem.use offsets 154-176):
	//   luck      = EnchantmentHelper.getFishingLuckBonus(level, rod, player)      (Luck of the Sea)
	//   lureSpeed = (int)(getFishingTimeReduction(level, rod, player) * 20.0F)     (Lure)
	luck := int32(t.enchFishingLuckBonus(held))
	lureSpeed := int32(t.enchFishingTimeReduction(held) * 20.0) // f2i truncation, exactly the bytecode
	t.spawnFishingHook(p, luck, lureSpeed)
	// FishingRodItem.use: Level.playSound(FISHING_BOBBER_THROW ...) + awardStat — cited deferral (cosmetic).
	return true
}

// spawnFishingHook is the FishingHook(Player, Level, luck, lureSpeed) constructor port: it seats the
// bobber at the caster's off-shoulder cast origin and gives it the look-derived launch velocity with the
// per-axis triangle spread, then stores it (the tracker broadcasts AddEntity next tick). It also links
// the caster (player.fishing) so a second use reels.
//
// Source: javap FishingHook.<init>(Player, Level, int, int).
func (t *TickLoop) spawnFishingHook(p *tickPlayer, luck, lureSpeed int32) *Entity {
	// The ctor's look basis (Mth trig, degrees->radians). yRot1/xRot1 are the player's look angles.
	xRot1 := float64(p.pitch)
	yRot1 := float64(p.yaw)
	yCos := math.Cos(-yRot1*(math.Pi/180.0) - math.Pi)
	ySin := math.Sin(-yRot1*(math.Pi/180.0) - math.Pi)
	xCos := -math.Cos(-xRot1 * (math.Pi / 180.0))
	xSin := math.Sin(-xRot1 * (math.Pi / 180.0))

	// snapTo(x1, eyeY, z1): the bobber starts 0.3 blocks off the shoulder at eye height.
	x1 := p.x - ySin*0.3
	y1 := p.y + fishEyeHeight // player.getEyeY()
	z1 := p.z - yCos*0.3

	b := NewEntity(t.idAlloc.AllocID(), entity.FishingBobber, x1, y1, z1)
	b.isFishingHook = true
	b.fishingOwnerID = p.entityID
	b.fishingLuck = maxInt32(0, luck)      // this.luck = Math.max(0, luck)
	b.fishingLure = maxInt32(0, lureSpeed) // this.lureSpeed = Math.max(0, lureSpeed)
	b.fishingState = fishStateFlying
	b.fishingOpenWater = true // FishingHook.openWater default true
	// Dedicated per-bobber RNG seeded from the bobber id (NEVER a mob/pig stream). Deterministic per id.
	b.fishingRNG = newEntityRandom(uint64(b.id))
	// getAddEntityPacket: the AddEntity "data" field for a bobber is the OWNER's entity id (or the
	// bobber's own id if no owner) — the client links the fishing line to the caster.
	b.spawnData = p.entityID

	// newMovement = new Vec3(-ySin, clamp(-(xSin/xCos), -5, 5), -yCos); normalize to 0.6 + per-axis
	// triangle(0.5, 0.0103365). NOTE: vanilla draws the triangle THREE times (once per axis component),
	// each a fresh 2-nextDouble draw — the draw order is load-bearing.
	mvx := -ySin
	mvy := clampF64(-(xSin / xCos), -5.0, 5.0)
	mvz := -yCos
	dist := math.Sqrt(mvx*mvx + mvy*mvy + mvz*mvz)
	fx := 0.6/dist + fishTriangle(b.fishingRNG, 0.5, 0.0103365)
	fy := 0.6/dist + fishTriangle(b.fishingRNG, 0.5, 0.0103365)
	fz := 0.6/dist + fishTriangle(b.fishingRNG, 0.5, 0.0103365)
	b.vx = mvx * fx
	b.vy = mvy * fy
	b.vz = mvz * fz

	// setYRot/setXRot from the launch vector (atan2 in degrees; 57.2957763671875 == 180/pi as a float).
	horiz := math.Sqrt(b.vx*b.vx + b.vz*b.vz)
	b.yaw = float32(math.Atan2(b.vx, b.vz) * 57.2957763671875)
	b.pitch = float32(math.Atan2(b.vy, horiz) * 57.2957763671875)
	b.headYaw = b.yaw

	owner := t.regionForEntity(b)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(b)
	p.fishingHookID = b.id // player.fishing = hook (updateOwnerInfo(this))
	return b
}

// tickFishingHooks drives every bobber in every region, the sibling of tickArrows/tickPotions. It runs
// on the coordinator (quiescent) and processes each region WITH that region registered (withRegion) so
// the bobber tick's t.cur() (the moveEntity re-bucket + the discard remove) resolves to the bobber's OWN
// store. A per-region snapshot keeps the loop stable across an in-loop discard.
func (t *TickLoop) tickFishingHooks() {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, r.entities.len())
		for _, e := range r.entities.all() {
			if e.isFishingHook {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickFishingHook(e)
			}
		})
	}
}

// tickFishingHook is the FishingHook.tick port (server-side branch) for one bobber. It resolves the
// owner (discard if gone / rod dropped / too far), floats on water, runs the catchingFish countdown, and
// applies the fluid-bob + gravity + inertia physics. Cite FishingHook.tick.
func (t *TickLoop) tickFishingHook(e *Entity) {
	owner := t.playerByEntityID(e.fishingOwnerID)
	if owner == nil {
		// getPlayerOwner() == null -> discard.
		t.fishingDiscard(e, owner)
		return
	}
	// shouldStopFishing(owner): the owner must be holding a fishing rod and within 32 blocks.
	if t.fishingShouldStop(e, owner) {
		t.fishingDiscard(e, owner)
		return
	}

	if e.onGround {
		e.fishingLife++
		if e.fishingLife >= fishGroundDespawn {
			t.fishingDiscard(e, owner)
			return
		}
	} else {
		e.fishingLife = 0
	}

	// liquidHeight = fluidState(blockPos).getHeight if it is water; isInWater = liquidHeight > 0.
	bx := int(math.Floor(e.x))
	by := int(math.Floor(e.y))
	bz := int(math.Floor(e.z))
	liquidHeight := t.fishingWaterHeight(bx, by, bz)
	isInWater := liquidHeight > 0.0

	if e.fishingState == fishStateFlying {
		if e.fishingHookedID != 0 {
			e.vx, e.vy, e.vz = 0, 0, 0
			e.fishingState = fishStateHooked
			return
		}
		if isInWater {
			// setDeltaMovement(delta.multiply(0.3, 0.2, 0.3)); state = BOBBING.
			e.vx *= 0.3
			e.vy *= 0.2
			e.vz *= 0.3
			e.fishingState = fishStateBobbing
			return
		}
		// checkCollision: block/entity hit while flying. v1 ports the BLOCK landing (the fish catch
		// primary target); the entity-hit hookedIn set is a cited deferral (see file header). A block
		// hit is handled below by moveEntity's onGround/horizontalCollision -> ZERO.
	} else if e.fishingState == fishStateHooked {
		if e.fishingHookedID != 0 {
			hooked := t.entityByIDAnyRegion(e.fishingHookedID)
			if hooked == nil {
				e.fishingHookedID = 0
				e.fishingState = fishStateFlying
			} else {
				// setPos(hooked.x, hooked.getY(0.8), hooked.z): ride the hooked entity.
				t.cur().entities.move(e, hooked.x, hooked.y+hooked.height*0.8, hooked.z)
			}
		}
		return
	} else if e.fishingState == fishStateBobbing {
		// The bob-float toward the liquid surface.
		force := e.y + e.vy - float64(by) - liquidHeight
		if math.Abs(force) < 0.01 {
			force += math.Copysign(0.1, force)
		}
		e.vx *= 0.9
		e.vy = e.vy - force*float64(e.fishingRNG.nextFloat())*0.2
		e.vz *= 0.9

		// openWater update: while a catch is pending, keep it iff still open water (else re-eval true).
		if e.fishingNibble > 0 || e.fishingHooked > 0 {
			e.fishingOpenWater = e.fishingOpenWater && e.fishingOutOfWater < fishMaxOutOfWater && t.fishingOpenWaterAt(bx, by, bz)
		} else {
			e.fishingOpenWater = true
		}

		if isInWater {
			if e.fishingOutOfWater > 0 {
				e.fishingOutOfWater--
			}
			if e.fishingBiting {
				// add(0, -0.1 * syncRandom.nextFloat() * syncRandom.nextFloat(), 0). v1 reuses the
				// per-bobber RNG for the bite dip (the syncronizedRandom is a client-lockstep stream in
				// vanilla; on the server the observable effect is the two draws + the downward nudge).
				e.vy += -0.1 * float64(e.fishingRNG.nextFloat()) * float64(e.fishingRNG.nextFloat())
			}
			// catchingFish(blockPos) — the server-side catch countdown (always server here).
			t.fishingCatchingFish(e, bx, by, bz)
		} else {
			if e.fishingOutOfWater < fishMaxOutOfWater {
				e.fishingOutOfWater++
			}
		}
	}

	// applyGravity: if not in water AND not on ground AND not hooked -> add(0, -0.03, 0).
	if !isInWater && !e.onGround && e.fishingHookedID == 0 {
		e.vy -= fishFlyGravity
	}

	// move(MoverType.SELF, delta) — the per-axis collide move (sets onGround + zeros blocked velocity).
	t.moveEntity(e, e.vx, e.vy, e.vz)

	// updateRotation from the movement (the client render angles).
	horiz := math.Sqrt(e.vx*e.vx + e.vz*e.vz)
	if e.vx != 0 || e.vz != 0 {
		e.yaw = float32(math.Atan2(e.vx, e.vz) * 57.2957763671875)
		e.headYaw = e.yaw
	}
	e.pitch = float32(math.Atan2(e.vy, horiz) * 57.2957763671875)

	// FLYING + (onGround || horizontalCollision) -> deltaMovement = ZERO (the bobber sticks on land).
	if e.fishingState == fishStateFlying && (e.onGround || t.fishingHorizontalCollision(e)) {
		e.vx, e.vy, e.vz = 0, 0, 0
	}

	// deltaMovement.scale(0.92).
	e.vx *= fishInertia
	e.vy *= fishInertia
	e.vz *= fishInertia
}

// fishingHorizontalCollision reports whether the bobber is pressed against a solid wall on its X/Z path
// this tick — the Entity.horizontalCollision analogue moveEntity's velocity-zeroing implies. Since
// moveEntity already zeroed a blocked X/Z velocity, a FLYING bobber that hit a wall has that axis at 0
// while still airborne; but to avoid a false positive on a legitimately-zero component we test the block
// cell in front. v1 uses the block-solidity in the four horizontal neighbors of the bobber cell.
func (t *TickLoop) fishingHorizontalCollision(e *Entity) bool {
	bx := int(math.Floor(e.x))
	by := int(math.Floor(e.y))
	bz := int(math.Floor(e.z))
	return t.isSolidAt(pk.Position{X: bx + 1, Y: by, Z: bz}) ||
		t.isSolidAt(pk.Position{X: bx - 1, Y: by, Z: bz}) ||
		t.isSolidAt(pk.Position{X: bx, Y: by, Z: bz + 1}) ||
		t.isSolidAt(pk.Position{X: bx, Y: by, Z: bz - 1})
}

// fishingShouldStop is FishingHook.shouldStopFishing(owner): the hook keeps fishing only while the owner
// can interact, holds a fishing rod in either hand, and is within 32 blocks (distSqr <= 1024). Otherwise
// it must be discarded. Cite FishingHook.shouldStopFishing.
func (t *TickLoop) fishingShouldStop(e *Entity, owner *tickPlayer) bool {
	if owner.dead {
		return true // !canInteractWithLevel() analogue
	}
	inv := ensureInventory(owner)
	main := inv.get(hotbarMenuSlotBase + inv.heldSlot)
	off := inv.get(offHandMenuSlot)
	holdingRod := int32(main.ItemID) == fishingRodItemID || int32(off.ItemID) == fishingRodItemID
	if holdingRod {
		dx := e.x - owner.x
		dy := e.y - owner.y
		dz := e.z - owner.z
		if dx*dx+dy*dy+dz*dz <= fishStopDistanceSq {
			return false
		}
	}
	return true
}

// fishingCatchingFish is FishingHook.catchingFish(blockPos): the fishing-speed weather modifiers, then
// the nibble -> timeUntilHooked -> timeUntilLured countdown cascade that ends in a bite (nibble armed).
// The particle sends are cited-deferred cosmetics; every RNG draw that GATES state is preserved in order.
// Cite FishingHook.catchingFish.
func (t *TickLoop) fishingCatchingFish(e *Entity, bx, by, bz int) {
	r := e.fishingRNG
	fishingSpeed := 1
	// if (nextFloat() < 0.25 && isRainingAt(above)) ++speed; if (nextFloat() < 0.5 && !canSeeSky(above)) --speed.
	// v1 has no weather/sky subsystem wired for the bobber: isRainingAt -> false (default not raining),
	// canSeeSky(above) -> true (open sky default). Both draws are STILL consumed in order (the roll is
	// evaluated left-to-right; the second operand only decides the increment). Cited stubs = vanilla
	// clear-weather / open-sky default, structured to read the real weather/sky later.
	if r.nextFloat() < 0.25 && t.fishingIsRainingAt(bx, by+1, bz) {
		fishingSpeed++
	}
	if r.nextFloat() < 0.5 && !t.fishingCanSeeSky(bx, by+1, bz) {
		fishingSpeed--
	}

	if e.fishingNibble > 0 {
		e.fishingNibble--
		if e.fishingNibble <= 0 {
			e.fishingLured = 0
			e.fishingHooked = 0
			e.fishingBiting = false // DATA_BITING = false
		}
		return
	}

	if e.fishingHooked > 0 {
		e.fishingHooked -= int32(fishingSpeed)
		if e.fishingHooked > 0 {
			// this.fishAngle += triangle(0, 9.188) — ALWAYS 2 nextDouble draws (drives fishAngle, which
			// carries into the bite direction, so it is state, not just cosmetic).
			e.fishingWanderAngle += float32(fishTriangle(r, 0.0, 9.188))
			// The wander cell for the bubble particle. If it is a WATER cell, vanilla draws nextFloat()
			// < 0.15 (the bubble-emit gate). The draw is CONDITIONAL on world geometry and MUST be
			// consumed when the cell is water to keep the stream draw-order faithful (the sendParticles
			// themselves are the cited-deferred cosmetic; the RNG DRAW is preserved).
			angle := float64(e.fishingWanderAngle) * (math.Pi / 180.0)
			fx := e.x + math.Sin(angle)*float64(e.fishingHooked)*0.1
			fz := e.z + math.Cos(angle)*float64(e.fishingHooked)*0.1
			fyCell := int(math.Floor(e.y)) // Mth.floor(y)+1 - 1 == floor(y)
			if t.fishingCellIsWater(int(math.Floor(fx)), fyCell, int(math.Floor(fz))) {
				_ = r.nextFloat() // < 0.15f bubble-emit gate (particle deferred; draw preserved).
			}
		} else {
			// The BITE: playSound pitch = 1 + (nextFloat() - nextFloat())*0.4 -> TWO draws (the sound is
			// deferred cosmetic, but the two draws are consumed in order); nibble = Mth.nextInt(20, 40);
			// DATA_BITING = true.
			_ = r.nextFloat() // splash-sound pitch draw 1 (deferred sound; draw preserved).
			_ = r.nextFloat() // splash-sound pitch draw 2.
			e.fishingNibble = int32(mthNextIntRange(r, 20, 40))
			e.fishingBiting = true
		}
		return
	}

	if e.fishingLured > 0 {
		e.fishingLured -= int32(fishingSpeed)
		teaseChance := float32(0.15)
		if e.fishingLured < 20 {
			teaseChance += float32(20-e.fishingLured) * 0.05
		} else if e.fishingLured < 40 {
			teaseChance += float32(40-e.fishingLured) * 0.02
		} else if e.fishingLured < 60 {
			teaseChance += float32(60-e.fishingLured) * 0.01
		}
		// The tease branch: nextFloat() < teaseChance is ALWAYS drawn. If it hits, an angle + dist are
		// drawn (2 draws), and if the tease cell is water a nextInt(2) is drawn — all preserved in order
		// (the sendParticles is the deferred cosmetic).
		if r.nextFloat() < teaseChance {
			angle := mthNextFloatRange(r, 0.0, 360.0) * (float32(math.Pi) / 180.0)
			dist := mthNextFloatRange(r, 25.0, 60.0)
			fx := e.x + float64(mthSinf(angle)*dist)*0.1
			fz := e.z + float64(mthCosf(angle)*dist)*0.1
			fyCell := int(math.Floor(e.y))
			if t.fishingCellIsWater(int(math.Floor(fx)), fyCell, int(math.Floor(fz))) {
				_ = r.nextInt(2) // splash-particle count draw (particle deferred; draw preserved).
			}
		}
		if e.fishingLured <= 0 {
			// fishAngle = Mth.nextFloat(0, 360); timeUntilHooked = Mth.nextInt(20, 80).
			e.fishingWanderAngle = mthNextFloatRange(r, 0.0, 360.0)
			e.fishingHooked = int32(mthNextIntRange(r, 20, 80))
		}
		return
	}

	// else: timeUntilLured = Mth.nextInt(100, 600) - lureSpeed.
	e.fishingLured = int32(mthNextIntRange(r, 100, 600)) - e.fishingLure
}

// fishingRetrieve is the FishingHook.retrieve(rod) port: if a hooked entity is set, PULL it toward the
// owner (and return the entity-pull damage); if a catch is pending (nibble > 0), roll the FISHING loot
// table + spawn each caught ItemEntity flying to the owner + award an XP orb. Then discard the bobber.
// Cite FishingHook.retrieve.
func (t *TickLoop) fishingRetrieve(e *Entity, owner *tickPlayer) int {
	if owner == nil || owner.dead || t.fishingShouldStop(e, owner) {
		t.fishingDiscard(e, owner)
		return 0
	}
	dmg := 0
	if e.fishingHookedID != 0 {
		// pullEntity(hookedIn): delta = (owner.pos - hook.pos)*0.1 added to the hooked entity's velocity.
		if hooked := t.entityByIDAnyRegion(e.fishingHookedID); hooked != nil {
			hooked.vx += (owner.x - e.x) * 0.1
			hooked.vy += (owner.y - e.y) * 0.1
			hooked.vz += (owner.z - e.z) * 0.1
			if hooked.isItem {
				dmg = 3
			} else {
				dmg = 5
			}
		}
	} else if e.fishingNibble > 0 {
		// Roll the FISHING loot table. LootParams: ORIGIN=hook.pos, TOOL=rod, THIS_ENTITY=hook,
		// luck = this.luck + owner.getLuck() (LUCK attribute; v1 default 0). The loot seed is a fresh
		// draw from the bobber's OWN RNG (the deterministic proxy for vanilla's unseeded thread random).
		seed := e.fishingRNG.nextLong()
		luck := float32(e.fishingLuck) + fishingOwnerLuck(owner) // owner.getLuck() cited-stub 0
		items := t.fishingRollLoot(seed, luck, e.fishingOpenWater)
		for _, stack := range items {
			t.fishingSpawnCaughtItem(e, owner, stack)
			// award: new ExperienceOrb(owner.level(), owner.x, owner.y+0.5, owner.z+0.5, nextInt(6)+1).
			xp := e.fishingRNG.nextInt(6) + 1
			t.fishingAwardXP(owner, xp)
			// itemStack.is(FISHES) -> awardStat(FISH_CAUGHT): stat tracking is a cited deferral.
			// ADVANCEMENTS (advancements.go): minecraft:fishing_rod_hooked — FishingHook.retrieve fires
			// CriteriaTriggers.FISHING_ROD_HOOKED.trigger((ServerPlayer)owner, rod, this, items) with the
			// caught stacks. The trigger tests each criterion item predicate against the caught item id;
			// vanilla fires once with the full collection, but the per-item feed into the idempotent grant
			// is equivalent (a criterion matched by ANY caught item is granted once). Drives
			// husbandry/fishy_business (cod/salmon/pufferfish/tropical_fish). CITE: FishingHook.retrieve.
			if int(stack.ItemID) >= 0 && int(stack.ItemID) < len(registryid.Item) {
				t.triggerFishingRodHooked(owner, registryid.Item[stack.ItemID])
			}
		}
		dmg = 1
	}
	if e.onGround {
		dmg = 2
	}
	t.fishingDiscard(e, owner)
	return dmg
}

// fishingRollLoot rolls the gameplay/fishing loot table at the given seed/luck/open-water, returning the
// caught stacks. The FISHING table references the junk/treasure/fish sub-tables (NestedLootTable) and
// gates treasure on the in_open_water condition — both handled by the level/loot engine.
func (t *TickLoop) fishingRollLoot(seed int64, luck float32, openWater bool) []component.SlotData {
	tbl, err := loot.LoadTable("minecraft:gameplay/fishing")
	if err != nil {
		return nil
	}
	ctx := loot.NewFishingLootContext(seed, luck, openWater)
	return loot.Roll(tbl, seed, ctx)
}

// fishingSpawnCaughtItem spawns a caught ItemEntity at the bobber and gives it the vanilla "fly to the
// owner" velocity: (owner.pos - hook.pos)*0.1 horizontally, plus the sqrt(sqrt(distSq))*0.08 vertical
// arc. Cite FishingHook.retrieve (the ItemEntity setDeltaMovement).
func (t *TickLoop) fishingSpawnCaughtItem(e *Entity, owner *tickPlayer, stack component.SlotData) {
	ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y, e.z, stack)
	xa := owner.x - e.x
	ya := owner.y - e.y
	za := owner.z - e.z
	// setDeltaMovement(xa*0.1, ya*0.1 + sqrt(sqrt(xa^2+ya^2+za^2))*0.08, za*0.1) — OVERRIDES the
	// ItemEntity ctor's random toss (that toss's draws are the item entity's own random, not the
	// bobber RNG, and are immediately overwritten here).
	ie.vx = xa * 0.1
	ie.vy = ya*0.1 + math.Sqrt(math.Sqrt(xa*xa+ya*ya+za*za))*0.08
	ie.vz = za * 0.1
	t.cur().entities.add(ie)
}

// fishingAwardXP spawns the catch's XP orb(s) at the owner (owner.x, owner.y+0.5, owner.z+0.5), the
// ExperienceOrb.award split loop. Cite FishingHook.retrieve (new ExperienceOrb).
func (t *TickLoop) fishingAwardXP(owner *tickPlayer, value int) {
	if value <= 0 {
		return
	}
	t.awardExperienceOrbsAt(owner.x, owner.y+0.5, owner.z+0.5, value)
}

// fishingDiscard removes the bobber from its store and clears the owner's fishing link
// (updateOwnerInfo(null) -> owner.fishing = null). Cite FishingHook.remove/updateOwnerInfo.
func (t *TickLoop) fishingDiscard(e *Entity, owner *tickPlayer) {
	if owner != nil && owner.fishingHookID == e.id {
		owner.fishingHookID = 0
	}
	if t.cur() != nil && t.cur().entities != nil {
		t.cur().entities.remove(e.id)
	}
}

// fishingWaterHeight returns the water fluid height at a block cell (FluidState.getHeight for water,
// else 0). For a source (amount 8) with water directly above -> 1.0; else amount/9. Cite
// FlowingFluid.getHeight / getOwnHeight.
func (t *TickLoop) fishingWaterHeight(x, y, z int) float64 {
	id := t.blockStateAt(x, y, z)
	fs := decodeFluid(id)
	if !fs.isWater {
		return 0.0
	}
	// hasSameAbove -> 1.0 (water flowing down from above fills the cell fully).
	if _, aboveIsWater := waterLevelOf(t.blockStateAt(x, y+1, z)); aboveIsWater {
		return 1.0
	}
	return float64(fs.amount) / 9.0
}

// fishingOpenWaterAt is FishingHook.calculateOpenWater(pos): the 5x5 x-z column across y in [-1, 2] must
// be a clean water body (each layer uniformly ABOVE_WATER or INSIDE_WATER, transitioning air-over-water
// exactly once). Any solid/mixed layer -> not open water (treasure ineligible). Cite
// FishingHook.calculateOpenWater / getOpenWaterTypeForArea / getOpenWaterTypeForBlock.
func (t *TickLoop) fishingOpenWaterAt(bx, by, bz int) bool {
	const (
		owInvalid = 0 // OpenWaterType.INVALID
		owAbove   = 1 // OpenWaterType.ABOVE_WATER
		owInside  = 2 // OpenWaterType.INSIDE_WATER
	)
	previous := owInvalid
	for y := -1; y <= 2; y++ {
		layer := t.fishingOpenWaterLayer(bx, by+y, bz)
		switch layer {
		case owInside: // case 2 -> return false (INSIDE_WATER as a whole layer is not a valid transition)
			return false
		case owInvalid: // case 0
			if previous != owInvalid {
				return false
			}
		case owAbove: // case 1
			if previous == owAbove {
				return false
			}
		}
		previous = layer
	}
	return true
}

// fishingOpenWaterLayer is getOpenWaterTypeForArea over the 5x5 (bx-2..bx+2, bz-2..bz+2) at y: the
// reduce of every cell's OpenWaterType — uniform -> that type, mixed -> INVALID. Cite
// getOpenWaterTypeForArea (BlockPos.betweenClosedStream reduce).
func (t *TickLoop) fishingOpenWaterLayer(bx, y, bz int) int {
	const (
		owInvalid = 0
		owAbove   = 1
		owInside  = 2
	)
	first := true
	acc := owInvalid
	for x := bx - 2; x <= bx+2; x++ {
		for z := bz - 2; z <= bz+2; z++ {
			cell := t.fishingOpenWaterBlock(x, y, z)
			if first {
				acc = cell
				first = false
				continue
			}
			if acc != cell {
				return owInvalid // mixed layer
			}
		}
	}
	if first {
		return owInvalid
	}
	return acc
}

// fishingOpenWaterBlock is getOpenWaterTypeForBlock: air/lily_pad -> ABOVE_WATER; a full water SOURCE
// with an empty collision shape -> INSIDE_WATER; anything else -> INVALID. v1 tests air (state 0) and
// water-source; lily pad + the collision-shape emptiness reduce to "the cell is a water source" for the
// INSIDE_WATER case (a source has no collision shape). Cite getOpenWaterTypeForBlock.
func (t *TickLoop) fishingOpenWaterBlock(x, y, z int) int {
	const (
		owInvalid = 0
		owAbove   = 1
		owInside  = 2
	)
	id := t.blockStateAt(x, y, z)
	if block.IsAir(id) || block.IsLilyPad(id) {
		return owAbove
	}
	level, isWater := waterLevelOf(id)
	if isWater && level == 0 { // level 0 == a full water SOURCE (fluidState.isSource())
		return owInside
	}
	return owInvalid
}

// fishingIsRainingAt is Level.isRainingAt(pos): whether rain is falling at the cell. v1 has no weather
// subsystem for the bobber -> false (the clear-weather cited-stub default). Structured to read the real
// weather state when it lands. Cite Level.isRainingAt.
func (t *TickLoop) fishingIsRainingAt(x, y, z int) bool { return false }

// fishingCanSeeSky is Level.canSeeSky(pos): whether the sky is directly visible above the cell. v1 has
// no heightmap/sky subsystem for the bobber -> true (the open-sky cited-stub default). Structured to
// read the real heightmap when it lands. Cite Level.canSeeSky.
func (t *TickLoop) fishingCanSeeSky(x, y, z int) bool { return true }

// clampF64 is the double form of Mth.clamp(value, min, max).
func clampF64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// maxInt32 is Math.max for int32 (the luck/lureSpeed floor-at-0 in the FishingHook ctor).
func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// fishingCellIsWater is BlockState.is(Blocks.WATER) at a cell — the catchingFish wander/tease-cell
// water test that gates the conditional particle RNG draws. Any water state id (source or flowing).
func (t *TickLoop) fishingCellIsWater(x, y, z int) bool {
	_, isWater := waterLevelOf(t.blockStateAt(x, y, z))
	return isWater
}

// mthSinf / mthCosf are Mth.sin / Mth.cos (a lookup table in vanilla; functionally sin/cos) on a
// float32 argument, returning float32 — used by the tease-particle heading math.
func mthSinf(rad float32) float32 { return float32(math.Sin(float64(rad))) }
func mthCosf(rad float32) float32 { return float32(math.Cos(float64(rad))) }
