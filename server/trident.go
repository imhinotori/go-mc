package server

// trident.go -- THE TRIDENT: a 1:1 port of net.minecraft.world.item.TridentItem (the throw / riptide
// item use) and net.minecraft.world.entity.projectile.arrow.ThrownTrident (the projectile: flight,
// onHitEntity damage 8 + Impaling, Loyalty return-to-owner + re-pickup, Channeling lightning), decompiled
// from temp/cache/26.2-inner.jar (javap -c -p this session).
//
// A ThrownTrident is an AbstractArrow SUBCLASS: it flies with the SAME physics as an arrow (projectile.go
// tickArrow: drag 0.99, gravity 0.05, swept block+entity hit, 1200-tick despawn). The trident-specific
// behavior (the dealtDamage latch, the Loyalty return) is layered on TOP of that shared flight -- exactly
// as ThrownTrident.tick runs its own pre-logic then calls super.tick() (AbstractArrow.tick). We set BOTH
// isArrow (so tickArrow drives it) AND isTrident (so tickArrow runs the trident pre-tick first).
//
// TridentItem is a TIMED-USE item (getUseDuration 72000), like the bow: use() startUsingItem, and the
// RELEASE_USE_ITEM action drives releaseUsing (gate: useDuration - timeLeft >= 10). Non-riptide -> throw a
// ThrownTrident (spawnProjectileFromRotation power 2.5, inaccuracy 1.0); riptide (in water/rain) -> launch
// the player. Wired into item_use.go (tryStartTridentUse / releaseUsingItem).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/registrydata"
)

// TridentItem / ThrownTrident constants (verified javap + enchant JSON -- exact values).
const (
	tridentThrowThreshold    int32 = 10    // TridentItem.THROW_THRESHOLD_TIME (bipush 10)
	tridentUseDuration       int32 = 72000 // TridentItem.getUseDuration
	tridentShootPower              = 2.5   // PROJECTILE_SHOOT_POWER (ldc 2.5f)
	tridentBaseDamage              = 8.0   // ThrownTrident.onHitEntity base hit (ldc 8.0f)
	tridentReturnPosLerp           = 0.015 // ThrownTrident.tick setPosRaw y-lerp (ldc2_w 0.015d)
	tridentReturnAccel             = 0.05  // ThrownTrident.tick return scale = 0.05*loyalty (ldc2_w 0.05d)
	tridentReturnKeepInert         = 0.95  // ThrownTrident.tick deltaMovement.scale(0.95) (ldc2_w 0.95d)
	tridentInGroundLatch           = 4     // dealtDamage once inGroundTime > 4
	tridentRiptideBase             = 1.5   // riptide.json trident_spin_attack_strength base
	tridentRiptidePerLevel         = 0.75  // riptide.json per_level_above_first
	tridentRiptideGroundPush       = 1.1999999284744263 // move(SELF, (0, 1.1999999, 0)) (ldc2_w)
)

// trident enchant wire names (the same resource-id keys stackEnchantments returns; the bow reads
// minecraft:infinity through the identical map). 0 == the enchant is absent (vanilla no-enchant default).
const (
	enchLoyalty    = "minecraft:loyalty"
	enchRiptide    = "minecraft:riptide"
	enchChanneling = "minecraft:channeling"
	enchImpaling   = "minecraft:impaling"
)

func itemIsTrident(itemID int32) bool { return item.ID(itemID) == item.Trident.ID }

// tridentSpinAttackStrength ports EnchantmentHelper.getTridentSpinAttackStrength: the riptide
// trident_spin_attack_strength value == 1.5 + 0.75*(level-1) for a riptide level, else 0. Same real
// stackEnchantments seam bow.go reads minecraft:infinity through. Cite riptide.json.
func tridentSpinAttackStrength(stack component.SlotData) float64 {
	level := stackEnchantments(stack)[enchRiptide]
	if level <= 0 {
		return 0
	}
	return tridentRiptideBase + tridentRiptidePerLevel*float64(level-1)
}

// tridentLoyaltyLevel ports getLoyaltyFromItem == clamp(getTridentReturnToOwnerAcceleration, 0, 127). In
// vanilla data the acceleration equals the Loyalty enchant level (linear base 1.0, per_level 1.0). Cite loyalty.json.
func tridentLoyaltyLevel(stack component.SlotData) int {
	level := stackEnchantments(stack)[enchLoyalty]
	if level < 0 {
		level = 0
	}
	if level > 127 {
		level = 127
	}
	return level
}

// tryStartTridentUse is the TridentItem.use port for the right-click path: begin the draw (startUsingItem)
// unless the riptide gate fails. Vanilla use():
//
//	if (stack.nextDamageWillBreak()) return FAIL;
//	if (getTridentSpinAttackStrength(stack, player) > 0 && !player.isInWaterOrRain()) return FAIL;
//	player.startUsingItem(hand); return CONSUME;
//
// Returns true when the item was a trident (so useItemInHand stops), false to fall through. Trident-gated
// (a cheap id compare, no RNG draw -- the pig oracle is unperturbed). Cite TridentItem.use.
func (t *TickLoop) tryStartTridentUse(p *tickPlayer, _ *Inventory, held component.SlotData, hand int32) bool {
	if !itemIsTrident(int32(held.ItemID)) {
		return false
	}
	if stackNextDamageWillBreak(held) {
		return true // FAIL: a near-broken trident cannot be used (no-op, use belongs to the trident)
	}
	if tridentSpinAttackStrength(held) > 0 && !t.playerIsInWaterOrRain(p) {
		return true // FAIL: a riptide trident can only be drawn in water or rain
	}
	p.useItem = held
	p.useItemRemaining = tridentUseDuration
	p.useItemHand = hand
	t.broadcastUsingItem(p, true, hand)
	return true
}

// releaseTrident dispatches TridentItem.releaseUsing for a trident being used (the RELEASE_USE_ITEM
// action). Returns true when the used item was a trident so the caller does not also clear via
// stopUsingItem. Cite LivingEntity.releaseUsingItem -> TridentItem.releaseUsing.
func (t *TickLoop) releaseTrident(p *tickPlayer) bool {
	if !isUsingItem(p) {
		return false
	}
	if !itemIsTrident(int32(p.useItem.ItemID)) {
		return false
	}
	t.tridentReleaseUsing(p, p.useItem, p.useItemHand)
	return true
}

// tridentReleaseUsing is the 1:1 port of TridentItem.releaseUsing:
//
//	charge = getUseDuration - timeLeft; if (charge < 10) return;
//	f = getTridentSpinAttackStrength(stack, player);
//	if (f > 0 && !(isInWaterOrRain() && isPassenger())) return 0;   // bytecode short-circuit
//	if (stack.nextDamageWillBreak()) return;
//	awardStat; hurtWithoutBreaking(1);
//	if (f == 0) { stack = consumeAndReturn(1); spawnProjectileFromRotation(ThrownTrident::new, 0f, 2.5f, 1f); }
//	else { push(look*f/|look|); startAutoSpinAttack(20, 8, stack); if (onGround) move(SELF,(0,1.1999999,0)); }
//
// Cite TridentItem.releaseUsing.
func (t *TickLoop) tridentReleaseUsing(p *tickPlayer, stack component.SlotData, hand int32) {
	charge := tridentUseDuration - p.useItemRemaining
	if charge < tridentThrowThreshold {
		t.stopUsingItem(p)
		return // THROW_THRESHOLD_TIME: a tap-release does nothing
	}
	f := tridentSpinAttackStrength(stack)
	if f > 0 && t.playerIsInWaterOrRain(p) && p.vehicleID != 0 {
		t.stopUsingItem(p)
		return // riptide while a passenger in water/rain no-ops (bytecode short-circuit)
	}
	if stackNextDamageWillBreak(stack) {
		t.stopUsingItem(p)
		return
	}
	t.hurtHandItem(p, hand, 1) // hurtWithoutBreaking(1): the trident takes 1 durability

	if f == 0 {
		creative := p.gameMode == gameModeCreative
		thrown := stack // the ItemStack the projectile carries (copyWithCount(1))
		vx, vy, vz := playerViewVector(p.yaw, p.pitch)
		vx *= tridentShootPower
		vy *= tridentShootPower
		vz *= tridentShootPower
		t.spawnThrownTrident(p.entityID, p.x, p.y+playerStandingEyeHeight, p.z, vx, vy, vz, thrown, creative)
		if !creative {
			t.tridentConsumeHeld(p, hand)
		}
	} else {
		t.tridentRiptideLaunch(p, f)
	}
	t.stopUsingItem(p)
}

// tridentConsumeHeld ports ItemStack.consumeAndReturn(1) for the thrown trident: remove 1 from the held
// slot and resync the slot to the client. Creative-exempt (the caller gates on !creative).
func (t *TickLoop) tridentConsumeHeld(p *tickPlayer, hand int32) {
	inv := ensureInventory(p)
	slot := heldMenuSlot(p, hand)
	before := inv.snapshot()
	s := inv.get(slot)
	s.Count--
	if s.Count <= 0 {
		s = component.SlotData{Count: 0}
	}
	inv.set(slot, s)
	t.broadcastInventoryChanges(p, inv, before)
}

// tridentRiptideLaunch ports the riptide branch of TridentItem.releaseUsing: push the player along the
// look direction scaled to magnitude f, start the auto-spin-attack, hop 1.1999999 up if on ground.
//
//	look = (-sin(yaw)*cos(pitch), -sin(pitch), cos(yaw)*cos(pitch)); mag = sqrt(look . look);
//	push(look.x*f/mag, look.y*f/mag, look.z*f/mag);   // ADD to deltaMovement (Player.push)
//	startAutoSpinAttack(20, 8.0f, stack);
//	if (onGround) move(SELF, (0, 1.1999999, 0));
//
// Player velocity is client-authoritative, so the push is applied to the player store Entity (playerEntity)
// and pushed to the client via ClientboundSetEntityMotion -- the exact idiom the golem-fling / knockback
// paths use. Cite TridentItem.releaseUsing (Player.push + startAutoSpinAttack + move).
func (t *TickLoop) tridentRiptideLaunch(p *tickPlayer, f float64) {
	lx, ly, lz := playerViewVector(p.yaw, p.pitch)
	mag := math.Sqrt(lx*lx + ly*ly + lz*lz)
	if mag == 0 {
		mag = 1 // guard: a zero look vector cannot be normalized
	}
	dx := lx * f / mag
	dy := ly * f / mag
	dz := lz * f / mag
	if p.playerEntity != nil {
		p.playerEntity.vx += dx // Player.push ADDS to deltaMovement
		p.playerEntity.vy += dy
		p.playerEntity.vz += dz
	}
	// startAutoSpinAttack(20, 8.0f, stack): the 20-tick spin timer + the 8.0 spin CONTACT damage are a
	// CITE-DEFERRED follow-up (no LivingEntity.autoSpinAttackTicks subsystem yet). The player LAUNCH (the
	// observable riptide movement) is landed here. Cite LivingEntity.startAutoSpinAttack.
	if p.onGround && p.playerEntity != nil {
		p.playerEntity.vy += tridentRiptideGroundPush // move(SELF, (0, 1.1999999, 0)) ground hop
	}
	if p.playerEntity != nil && p.client != nil {
		p.client.Send(encodeSetEntityMotion(p.playerEntity))
	}
}

// spawnThrownTrident creates a ThrownTrident at (x,y,z) with the given deltaMovement + owner, carrying the
// thrown ItemStack (its item id + enchant levels). It sets BOTH isArrow (so the shared arrow flight drives
// it) and isTrident (so tickArrow runs the trident pre-tick). Loyalty/Impaling/Channeling levels are read
// at spawn from the stack enchantments -- the SAME real stackEnchantments seam the bow reads. creative marks
// the pickup CREATIVE_ONLY. arrowBaseDamage stays 0 (a trident onHitEntity deals a FLAT 8, not the arrow
// velocity*baseDamage formula). Cite ThrownTrident.<init> + TridentItem.releaseUsing (Pickup).
func (t *TickLoop) spawnThrownTrident(shooterID int32, x, y, z, vx, vy, vz float64, stack component.SlotData, creative bool) *Entity {
	a := NewEntity(t.idAlloc.AllocID(), entity.Trident, x, y, z)
	a.isArrow = true // flies via the shared AbstractArrow physics (projectile.go tickArrow)
	a.isTrident = true
	a.arrowShooterID = shooterID
	a.arrowBaseDamage = 0 // ThrownTrident overrides onHitEntity -> flat 8
	a.vx, a.vy, a.vz = vx, vy, vz
	a.spawnData = shooterID + 1 // ClientboundAddEntity data: ownerId+1 (0 == none)

	ench := stackEnchantments(stack)
	a.tridentLoyalty = tridentLoyaltyLevel(stack)
	a.tridentImpaling = ench[enchImpaling]
	a.tridentChanneling = ench[enchChanneling]
	itemID := int32(stack.ItemID)
	if itemID == 0 {
		itemID = int32(item.Trident.ID) // getDefaultPickupItem == new ItemStack(TRIDENT)
	}
	a.tridentItemID = itemID
	a.tridentCreativeOnly = creative

	horiz := math.Sqrt(vx*vx + vz*vz)
	a.yaw = float32(math.Atan2(vx, vz) * 180.0 / math.Pi)
	a.pitch = float32(math.Atan2(vy, horiz) * 180.0 / math.Pi)
	a.headYaw = a.yaw

	owner := t.regionForEntity(a)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(a)
	return a
}

// tickTridentPre is the ThrownTrident.tick pre-logic that runs BEFORE the shared AbstractArrow physics
// (super.tick() == tickArrow flight):
//
//	if (inGroundTime > 4) dealtDamage = true;
//	if (loyalty > 0 && (dealtDamage || isNoPhysics) && owner != null) {
//	    if (!isAcceptibleReturnOwner()) { if (pickup==ALLOWED) spawnAtLocation(getPickupItem); discard; }
//	    else if (owner is Player && distanceTo(owner.eye) < owner.bbWidth + 1) { discard; return; }
//	    else { setNoPhysics(true); vec = owner.eye - pos;
//	           setPosRaw(x, y + vec.y*0.015*loyalty, z); scale = 0.05*loyalty;
//	           deltaMovement = deltaMovement.scale(0.95).add(vec.normalize().scale(scale)); }
//	}
//
// Returns handled=true when the trident was discarded (the caller skips the arrow physics this tick). Cite
// ThrownTrident.tick.
func (t *TickLoop) tickTridentPre(e *Entity) (handled bool) {
	if e.arrowInGround {
		e.tridentInGroundTime++
	} else {
		e.tridentInGroundTime = 0
	}
	if e.tridentInGroundTime > tridentInGroundLatch {
		e.tridentDealtDamage = true
	}

	loyalty := e.tridentLoyalty
	if loyalty <= 0 {
		return false
	}
	if !e.tridentDealtDamage && !e.tridentReturning {
		return false // return begins once the trident dealt damage or is already returning (noPhysics)
	}
	owner := t.playerByEntityID(e.arrowShooterID)
	if owner == nil {
		return false // getOwner() == null: no return
	}
	if owner.dead { // isAcceptibleReturnOwner() == false: drop the item + discard
		if !e.tridentCreativeOnly {
			t.spawnTridentPickupItem(e)
		}
		t.cur().entities.remove(e.id)
		return true
	}
	ex := owner.x - e.x
	ey := (owner.y + playerStandingEyeHeight) - e.y
	ez := owner.z - e.z
	dist := math.Sqrt(ex*ex + ey*ey + ez*ez)
	if dist < float64(playerWidth)+1.0 { // reached the owner: give the item back + discard
		t.tridentGiveBackToOwner(e, owner)
		t.cur().entities.remove(e.id)
		return true
	}
	e.tridentReturning = true // setNoPhysics(true)
	e.arrowInGround = false
	newY := e.y + ey*tridentReturnPosLerp*float64(loyalty) // setPosRaw y-lerp
	t.cur().entities.move(e, e.x, newY, e.z)
	scale := tridentReturnAccel * float64(loyalty)
	vmag := math.Sqrt(ex*ex + ey*ey + ez*ez)
	if vmag > 0 {
		e.vx = e.vx*tridentReturnKeepInert + (ex/vmag)*scale
		e.vy = e.vy*tridentReturnKeepInert + (ey/vmag)*scale
		e.vz = e.vz*tridentReturnKeepInert + (ez/vmag)*scale
	} else {
		e.vx *= tridentReturnKeepInert
		e.vy *= tridentReturnKeepInert
		e.vz *= tridentReturnKeepInert
	}
	return false // continue into the shared arrow physics (move+drag+gravity) to fly the trident home
}

// tridentGiveBackToOwner ports the Loyalty re-pickup: a returning trident that reaches its owner gives the
// pickup item (getPickupItem -> the TRIDENT stack) back to the owner inventory. Cite ThrownTrident.tryPickup.
func (t *TickLoop) tridentGiveBackToOwner(e *Entity, owner *tickPlayer) {
	if e.tridentCreativeOnly {
		return // CREATIVE_ONLY: no add
	}
	it, ok := item.ByID[item.ID(e.tridentItemID)]
	if !ok {
		it = &item.Trident
	}
	t.giveItemToPlayer(owner, it, int(it.StackSize), 1)
}

// spawnTridentPickupItem ports the drop-on-lost-owner branch: spawnAtLocation(getPickupItem, 0.1f). Cite
// ThrownTrident.tick.
func (t *TickLoop) spawnTridentPickupItem(e *Entity) {
	it, ok := item.ByID[item.ID(e.tridentItemID)]
	if !ok {
		it = &item.Trident
	}
	stack := component.SlotData{ItemID: pk.VarInt(it.ID), Count: 1}
	ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y, e.z, stack)
	t.cur().entities.add(ie)
}

// tridentOnHitEntity is the ThrownTrident.onHitEntity port for a player victim: damage = 8.0 + Impaling
// bonus (EnchantmentHelper.modifyDamage), attributed to the trident source (the shooter). Then set
// dealtDamage (so the trident does not re-hit and, with Loyalty, returns), run the Channeling lightning
// (post_attack), and bounce (deltaMovement *= (0.02, 0.2, 0.02)) rather than being consumed. Cite
// ThrownTrident.onHitEntity.
func (t *TickLoop) tridentOnHitEntity(e *Entity, victim *tickPlayer) {
	dmg := tridentBaseDamage + t.tridentImpalingBonus(e, victim)
	src := damageSourceTrident(e.arrowShooterID)
	e.tridentDealtDamage = true // dealtDamage = true BEFORE hurt (bytecode order)
	t.applyDamage(victim, src, float32(dmg))
	t.tridentChannelingStrike(e, victim.x, victim.y, victim.z)
	e.vx *= 0.02
	e.vy *= 0.2
	e.vz *= 0.02
}

// tridentImpalingBonus ports the Impaling damage add (impaling.json): level*2.5 vs a victim in
// minecraft:sensitive_to_impaling (the aquatic tag), else 0. A PLAYER is NOT sensitive_to_impaling, so a
// hit player takes 0 Impaling bonus -- the vanilla observable. When the aquatic-mob subsystem lands, this
// becomes a real tag membership read; today the seam returns the faithful 0 for the only victim kind
// (players). Cite EnchantmentHelper.modifyDamage + impaling.json.
func (t *TickLoop) tridentImpalingBonus(e *Entity, _ *tickPlayer) float64 {
	if e.tridentImpaling <= 0 {
		return 0
	}
	return 0 // victim is a player -> not in sensitive_to_impaling -> no bonus
}

// tridentOnHitMob is the ThrownTrident.onHitEntity port for a MOB (LivingEntity) victim -- the *Entity
// sibling of tridentOnHitEntity. It is the SAME bytecode trace: damage = 8.0 + Impaling (via the mob-aware
// impaling bonus), set dealtDamage BEFORE the hurt (so the trident does not re-hit and, with Loyalty,
// returns), route the hurt through applyDamageEntity (the LivingEntity.hurtServer port), run the Channeling
// lightning, and BOUNCE (deltaMovement *= (0.02, 0.2, 0.02)) rather than being consumed. The damage/bounce
// are kept identical to the player branch (a divergence would be a bug). The trident source carries the
// trident's position so the base 0.4 dealDefaultKnockback (inside applyDamageEntity) pushes the mob away
// from the trident. Cite ThrownTrident.onHitEntity.
func (t *TickLoop) tridentOnHitMob(e *Entity, victim *Entity) {
	dmg := tridentBaseDamage + t.tridentImpalingBonusMob(e, victim)
	src := damageSourceTrident(e.arrowShooterID)
	src.sourceX, src.sourceZ, src.hasSourcePos = e.x, e.z, true // directEntity (trident) position for knockback dir
	e.tridentDealtDamage = true                                 // dealtDamage = true BEFORE hurt (bytecode order)
	t.applyDamageEntity(victim, src, float32(dmg))
	t.tridentChannelingStrike(e, victim.x, victim.y, victim.z)
	e.vx *= 0.02
	e.vy *= 0.2
	e.vz *= 0.02
}

// tridentImpalingBonusMob ports the Impaling damage add (impaling.json) for a MOB victim: level*2.5 vs a
// victim whose entity type is in #minecraft:sensitive_to_impaling (the aquatic tag), else 0. Unlike the
// player branch (a player is never in the tag, so it hardcodes 0), a mob CAN be aquatic (guardian, squid,
// axolotl, ...), so this reads the GENUINE tag membership via registrydata.EntityTypeInTag -- the faithful
// EnchantmentHelper.modifyDamage requirement (entity_type == #sensitive_to_impaling). Cite
// EnchantmentHelper.modifyDamage + impaling.json (linear base 2.5, per_level_above_first 2.5 => level*2.5).
func (t *TickLoop) tridentImpalingBonusMob(e *Entity, victim *Entity) float64 {
	if e.tridentImpaling <= 0 {
		return 0
	}
	typeName := ""
	if idx := int(victim.typ); idx >= 0 && idx < len(registryid.EntityType) {
		typeName = registryid.EntityType[idx]
	}
	if typeName == "" {
		return 0
	}
	ok, err := registrydata.EntityTypeInTag(typeName, "#minecraft:sensitive_to_impaling")
	if err != nil || !ok {
		return 0 // not aquatic -> no Impaling bonus
	}
	return float64(e.tridentImpaling) * 2.5 // linear base 2.5 + per_level_above_first 2.5 == level*2.5
}

// tridentChannelingStrike ports the Channeling enchant lightning summon (channeling.json post_attack /
// hit_block): if the trident carries Channeling AND the world is thundering AND the trident can see the sky
// at the strike position, summon a lightning_bolt there. Cite channeling.json (weather_check thundering +
// location_check can_see_sky) + EnchantmentHelper summon_entity(lightning_bolt).
func (t *TickLoop) tridentChannelingStrike(e *Entity, x, y, z float64) {
	if e.tridentChanneling <= 0 {
		return
	}
	if !t.isThundering() {
		return // weather_check: thundering == true
	}
	probe := &Entity{x: x, y: y, z: z}
	if !t.canSeeSky(probe) {
		return // location_check can_see_sky
	}
	pos := pk.Position{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
	t.spawnLightningBolt(pos, false)
}
