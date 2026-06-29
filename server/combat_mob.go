package server

// combat_mob.go — MOB-SUB-01: the keystone mob (*Entity) hurt pipeline, the sibling of the v3-sealed
// *tickPlayer combat path (server/combat.go). A LITERAL 1:1 port of vanilla Java 26.2 (protocol 776),
// verified method-for-method against temp/cache/26.2-inner.jar via `javap -c -p` this session. No GPL
// source is pasted — the algorithm is re-expressed in Go — but the structure, ordering, and numeric
// ops (float32 casts, clamps, the exact constants, the branch order) are IDENTICAL to the bytecode.
//
// It REUSES combat.go's free helpers verbatim (combatRulesGetDamageAfterAbsorb, maxF, isNaN32/isInf32,
// the hurt* constants) — the Player and LivingEntity math are IDENTICAL, so the mob path calls the SAME
// functions (never a duplicated armor curve). The mob path differs from the *tickPlayer path in EXACTLY
// the three jar-verified actuallyHurt tail diffs + the lastDamageSource set + the region-scoped Emit:
//   1. applyDamageEntity sets e.lastDamageSource = src in the fresh-hit flag2 block (MOB-SUB-02).
//   2. actuallyHurtEntity has NO causeFoodExhaustion (Player-only) and NO SetHealth client send (a mob
//      has no client) — it just mutates e.health (clamp 0).
//   3. the on_damage Emit is region-scoped (emitEntityEvent), not a raw t.plugins.Emit.

import (
	"math"

	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/plugin/host"
)

// applyDamageEntity is the port of net.minecraft.world.entity.LivingEntity.hurtServer(ServerLevel,
// DamageSource, float) for a mob *Entity — the server-authoritative entry point for every damage
// source (attack, fall, environment), exactly as vanilla routes all mob damage through hurt ->
// hurtServer. It enforces the invulnerableTime i-frame rate limit, routes the surviving damage through
// actuallyHurtEntity, records the genuine lastDamageSource, and drives dieEntity if lethal. Runs on the
// owning region's goroutine over tick-owned state (TICK-05).
//
// Faithful bytecode trace (javap hurtServer this session — the v1-relevant slice; the world-side/
// effect/knockback branches are cited no-ops exactly as the player port leaves them):
//
//	if (isInvulnerableTo(...)) return false;        // v1: only the dead guard below (no /invuln)
//	if (isDeadOrDying()) return false;              // -> the health<=0 guard
//	if (amount < 0.0F) amount = 0.0F;               // clamp negative damage to 0
//	// applyItemBlocking / freeze-extra / helmet-damage: v1 stubs (no items/freeze/helmet) — pass through
//	if (Float.isNaN(amount) || Float.isInfinite(amount)) amount = 3.4028235E38F;  // NaN/Inf clamp
//	boolean flag2 = true;
//	if ((float) invulnerableTime > 10.0F && !source.is(BYPASSES_COOLDOWN)) {
//	    if (amount <= lastHurt) return false;       // not greater than the last hit: NO damage
//	    actuallyHurt(amount - lastHurt);            // only the EXCESS lands
//	    lastHurt = amount; flag2 = false;
//	} else {
//	    lastHurt = amount; invulnerableTime = 20;
//	    actuallyHurt(amount);
//	    hurtDuration = 10; hurtTime = hurtDuration;
//	}
//	if (flag2) { this.lastDamageSource = source; this.lastDamageStamp = getGameTime(); }  // bytecode 444-462
//	if (isDeadOrDying()) { ...; die(source); }      // the death drive
func (t *TickLoop) applyDamageEntity(e *Entity, src damageSource, amount float32) {
	// isDeadOrDying() guard (bytecode: isDeadOrDying ifeq -> iconst_0 ireturn): a dead mob takes no
	// further damage.
	if e.health <= 0 {
		return
	}

	// `if (amount < 0.0F) amount = 0.0F;`
	if amount < 0.0 {
		amount = 0.0
	}

	// NaN/Infinity clamp to Float.MAX_VALUE (`if (Float.isNaN || Float.isInfinite) amount = 3.4028235E38F`)
	// — verbatim from the player port, so a degenerate amount becomes the finite max (T-29-05).
	if isNaN32(amount) || isInf32(amount) {
		amount = maxFloat32
	}

	// flag2 (the fresh-hit marker; bytecode local 9). True unless the i-frame upper-half branch sets it
	// false — it gates the lastDamageSource store below.
	flag2 := true

	// The invulnerableTime i-frame gate (bytecode 185-272): while the grace window is in its upper half
	// ((float) invulnerableTime > 10.0F) and the source does not bypass the cooldown, a new hit only
	// applies the EXCESS over the previous hit's lastHurt; a hit not GREATER than lastHurt deals nothing.
	// Otherwise (fresh window) the full amount lands, lastHurt is recorded, invulnerableTime is armed to
	// 20, and the hurt-flash timers are set. src.is("bypasses_cooldown") is a GENUINE tag read (Plan 01
	// table) — no damage type in Phase 29 bypasses the cooldown, but the branch is the real read.
	if float32(e.invulnerableTime) > hurtCooldownConst && !src.is("bypasses_cooldown") {
		// `if (amount <= lastHurt) return false;` — a spam hit with no greater damage is a no-op.
		if amount <= e.lastHurt {
			return
		}
		// `actuallyHurt(amount - lastHurt);` then `lastHurt = amount; flag2 = false;` — only the excess.
		t.actuallyHurtEntity(e, src, amount-e.lastHurt)
		e.lastHurt = amount
		flag2 = false
	} else {
		// Fresh hit (bytecode 240-271): record lastHurt, arm the 20-tick window, apply full damage, set
		// the hurt-flash duration/time.
		e.lastHurt = amount
		e.invulnerableTime = hurtInvulnerableTicks
		t.actuallyHurtEntity(e, src, amount)
		e.hurtDuration = hurtDurationTicks
		e.hurtTime = e.hurtDuration
	}

	// MOB DIFF (MOB-SUB-02): the flag2 block (bytecode 444-462) — `if (flag2) { this.lastDamageSource =
	// source; this.lastDamageStamp = getGameTime(); }`. The player port omits this (no PanicGoal reader);
	// the mob port stores the genuine source so PanicGoal (P31) reads its tag and wolf anger (P36) reads
	// its attacker. lastDamageStamp (the gameTime) is not modeled here (no consumer yet) — it slots in
	// alongside this store when a reader lands; the source itself is the MOB-SUB-02 deliverable.
	if flag2 {
		e.lastDamageSource = src
	}

	// Death drive (`if (isDeadOrDying()) { ...; die(source); }`): actuallyHurtEntity has set the
	// authoritative health; if it reached 0 run the death flow.
	if e.health <= 0 {
		t.dieEntity(e, src)
	}
}

// actuallyHurtEntity is the port of net.minecraft.world.entity.LivingEntity.actuallyHurt(ServerLevel,
// DamageSource, float) — the LivingEntity tail (NOT Player.actuallyHurt). It applies the armor + magic
// reductions, folds in absorption, then subtracts the residual from health. It REUSES the combat.go
// free helpers (the math is identical between Player and LivingEntity).
//
// Faithful bytecode trace (javap actuallyHurt this session):
//
//	if (isInvulnerableTo(...)) return;                       // v1: never invulnerable
//	amount = getDamageAfterArmorAbsorb(source, amount);     // armor-points curve (REAL bypass tag read)
//	amount = getDamageAfterMagicAbsorb(source, amount);     // resistance/protection — v1 no-op
//	float f1 = amount;
//	amount = Math.max(amount - getAbsorptionAmount(), 0.0F);  // REUSE maxF
//	setAbsorptionAmount(getAbsorptionAmount() - (f1 - amount));
//	if (amount == 0.0F) return;                             // fully absorbed: no health change
//	// (NO causeFoodExhaustion — that is Player.actuallyHurt-only; DIFF 1)
//	getCombatTracker().recordDamage(source, amount);       // combat log — v1 stub
//	setHealth(getHealth() - amount);                        // DIFF 2: a mob has no client; just mutate health
//	setAbsorptionAmount(getAbsorptionAmount() - amount);
//	gameEvent(GameEvent.ENTITY_DAMAGE);                     // v1 stub
func (t *TickLoop) actuallyHurtEntity(e *Entity, src damageSource, amount float32) {
	// isInvulnerableTo guard: v1 has no invulnerability flag — constant false, preserved so a future
	// flag slots in here (exactly as the player port leaves it).
	const isInvulnerableTo = false
	if isInvulnerableTo {
		return
	}

	// Armor-points reduction (REAL bypasses_armor tag read), then magic reduction — in vanilla's order.
	amount = t.getDamageAfterArmorAbsorbEntity(e, src, amount)
	amount = t.getDamageAfterMagicAbsorbEntity(e, src, amount)

	// Absorption folding (Math.max(amount - absorption, 0); setAbsorption(absorption - (f1 - amount))).
	// A mob's MAX_ABSORPTION base is 0 (no absorption source wired), so getAbsorptionAmount() == 0,
	// withAbsorb == amount, and the setAbsorption write is a no-op — the FORMULA is kept verbatim so a
	// future MAX_ABSORPTION + effect slots in unchanged. REUSE maxF (combat.go).
	absorption := e.getAbsorptionAmount()
	withAbsorb := amount
	amount = maxF(amount-absorption, 0.0)
	e.setAbsorptionAmount(absorption - (withAbsorb - amount))

	// `if (amount == 0.0F) return;` — fully absorbed/blocked: no health change, no Emit.
	if amount == 0.0 {
		return
	}

	// DIFF 1: Player.actuallyHurt has causeFoodExhaustion HERE (combat.go:294) — LivingEntity.actuallyHurt
	// does NOT. A mob has no food/hunger, so this is correctly OMITTED for the mob path.

	// recordDamage (CombatTracker.recordDamage) — a v1 stub, exactly as the player path's combat tracker
	// is a stub. Cited no-op: the combat-log tracker is not modeled in v1.

	// DIFF 2: setHealth(getHealth() - amount). A mob has NO client, so unlike the player path there is
	// NO ClientboundSetHealth send — just mutate e.health. Clamp at 0 (health is never negative). The
	// wire reflection of mob health is entity metadata, a LATER concern.
	e.health -= amount
	if e.health < 0 {
		e.health = 0
	}

	// PLUGIN-02 on_damage seam — the LOCKED POST-mitigation site (mirrors combat.go:310): `amount` here
	// is the FINAL landed damage (after armor + magic + absorption folding), the value actually
	// subtracted from health. MOB DIFF 3: region-scoped via the owning region's emitEntityEvent (NOT a
	// raw t.plugins.Emit from a region goroutine — PITFALLS Pitfall 7 / T-29-02). This plan applies
	// damage synchronously on the mob's owning region (the same-region fast path), so the owner is
	// regionForEntity(e); the cross-region barrier-queue is Plan 03. Payload = entity id + final amount
	// as plain frozen scalars.
	t.regionForEntity(e).emitEntityEvent(host.EventDamage, host.DamageEvent{
		EntityID: int(e.id),
		Amount:   float64(amount),
	})

	// setAbsorptionAmount(getAbsorptionAmount() - amount): with absorption 0 this stays 0; kept for
	// fidelity (vanilla performs it unconditionally after the health subtraction).
	e.setAbsorptionAmount(e.getAbsorptionAmount() - amount)

	// gameEvent(GameEvent.ENTITY_DAMAGE): the world game-event broadcast — a v1 stub (no game-event
	// subsystem wired), cited here at the vanilla tail site.
}

// getDamageAfterArmorAbsorbEntity is the port of LivingEntity.getDamageAfterArmorAbsorb(DamageSource,
// float) for a mob:
//
//	if (!source.is(BYPASSES_ARMOR)) {
//	    hurtArmor(source, amount);   // armor durability — v1 stub (no mob armor items)
//	    amount = CombatRules.getDamageAfterAbsorb(this, amount, source,
//	                 (float) getArmorValue(), (float) getAttributeValue(ARMOR_TOUGHNESS));
//	}
//	return amount;
//
// The bypass branch is a GENUINE src.is("bypasses_armor") read against the Plan-01 tag table — NEVER a
// const false (PITFALLS Pitfall 3, the keystone deliverable). The armor curve REUSES the shared
// combatRulesGetDamageAfterAbsorb (combat.go) — the same verified CombatRules port the player uses.
// getArmorValue() == Mth.floor(getAttributeValue(ARMOR)) (the math.Floor d2f cast exactly as the player
// path does at combat.go:570/571); ARMOR_TOUGHNESS read as a double then d2f. Both read the mob's REAL
// *attribute.Map via getAttributeValue (entity.go:200) — not a faked curve.
func (t *TickLoop) getDamageAfterArmorAbsorbEntity(e *Entity, src damageSource, amount float32) float32 {
	if !src.is("bypasses_armor") {
		// hurtArmor(source, amount): armor-durability damage — v1 stub (no mob armor items to damage).
		// getArmorValue() == Mth.floor((double) getAttributeValue(ARMOR)) — the d2f-after-floor cast site.
		armorValue := float32(math.Floor(e.getAttributeValue(attribute.Armor)))
		armorToughness := float32(e.getAttributeValue(attribute.ArmorToughness))
		amount = combatRulesGetDamageAfterAbsorb(amount, armorValue, armorToughness)
	}
	return amount
}

// getDamageAfterMagicAbsorbEntity is the port of LivingEntity.getDamageAfterMagicAbsorb(DamageSource,
// float) for a mob. The full vanilla method applies RESISTANCE mob-effect reduction + enchantment
// damage protection; v1 has neither, so every guarded branch is skipped and amount passes through —
// the structure (constant-false guards) is preserved so effects/enchants slot in later, exactly as the
// player port (combat.go:365) leaves it. Mirrors the player path verbatim (mobs share this method).
//
//	if (source.is(BYPASSES_EFFECTS)) return amount;          // v1: false
//	if (hasEffect(RESISTANCE) && !source.is(BYPASSES_RESISTANCE)) { ... }  // v1: no effects
//	if (amount <= 0.0F) return 0.0F;
//	if (source.is(BYPASSES_ENCHANTMENTS)) return amount;     // v1: false
//	float protection = <enchant damage protection>;          // v1: 0
//	if (protection > 0.0F) amount = CombatRules.getDamageAfterMagicAbsorb(amount, protection);
//	return amount;
func (t *TickLoop) getDamageAfterMagicAbsorbEntity(e *Entity, src damageSource, amount float32) float32 {
	// source.is("bypasses_effects"): a genuine tag read; if true the effect reduction is skipped.
	if src.is("bypasses_effects") {
		return amount
	}
	// hasEffect(RESISTANCE): v1 has no mob effects — the resistance branch is skipped (constant false).
	const hasResistance = false
	if hasResistance {
		_ = e // resistance curve slots in here when mob effects arrive (amplifier-scaled).
	}
	// `if (amount <= 0.0F) return 0.0F;`
	if amount <= 0.0 {
		return 0.0
	}
	if src.is("bypasses_enchantments") {
		return amount
	}
	// Enchantment damage protection: v1 has no enchantments -> protection 0 -> the
	// CombatRules.getDamageAfterMagicAbsorb call is skipped (the `if (protection > 0)` guard).
	const protection float32 = 0.0
	if protection > 0.0 {
		amount = combatRulesGetDamageAfterMagicAbsorb(amount, protection)
	}
	return amount
}

// getAbsorptionAmount is the port of LivingEntity.getAbsorptionAmount() for a mob: the current
// absorption shield. No mob absorption source is wired in v1, so it is always 0 — the accessor exists
// so actuallyHurtEntity's absorption folding reads/writes it faithfully (a future MAX_ABSORPTION +
// effect stores a non-zero value here). Held off the Entity's absorptionAmount field would mirror the
// tickPlayer.absorptionAmount; the mob has no such field yet, so this returns the constant 0 — the
// FORMULA is preserved and behavior-identical (withAbsorb == amount).
func (e *Entity) getAbsorptionAmount() float32 {
	// No mob absorption field in v1 (a mob never gains a golden-apple shield); the vanilla
	// getAbsorptionAmount returns the absorptionAmount field, base 0 for a mob with no effect.
	return 0.0
}

// setAbsorptionAmount is the port of LivingEntity.setAbsorptionAmount(float) for a mob: vanilla clamps
// it to [0, getMaxAbsorption()]. A mob's MAX_ABSORPTION base is 0, so the clamp pins it to 0 in v1 (no
// mob has an absorption shield). The folding in actuallyHurtEntity always passes 0 here (withAbsorb ==
// amount), so this is a faithful no-op store — kept so a future absorption field slots in unchanged.
func (e *Entity) setAbsorptionAmount(amount float32) {
	// No-op store in v1: a mob carries no absorption shield. The clamp [0, MAX_ABSORPTION=0] would pin
	// any value to 0; preserved as a documented no-op (the value is never read back while base is 0).
	_ = amount
}

// tickMobIFrames is the port of the i-frame decrement block of net.minecraft.world.entity.LivingEntity
// .baseTick (javap this session, bytecode 450-491):
//
//	if (this.hurtTime > 0) this.hurtTime--;
//	if (this.invulnerableTime > 0 && !(this instanceof ServerPlayer)) this.invulnerableTime--;
//
// For a MOB the `!(this instanceof ServerPlayer)` guard is always TRUE, so the decrement happens in
// baseTick (whereas a player decrements invulnerableTime in ServerPlayer.tick — combat.go:106). It is
// PURE INTEGER MATH (no RNG draw), so it cannot perturb the per-mob RNG stream the pig oracle pins
// (PITFALLS Pitfall 5); it is driven from a dedicated per-mob loop in tickAI, structurally OUTSIDE
// serverAiStep's goal callbacks / navigation.tick.
func (t *TickLoop) tickMobIFrames(e *Entity) {
	if e.hurtTime > 0 {
		e.hurtTime--
	}
	if e.invulnerableTime > 0 {
		e.invulnerableTime--
	}
}

// dieEntity is the Plan-04 SEAM for net.minecraft.world.entity.LivingEntity.die(DamageSource): the full
// death flow (dropAllDeathLoot -> dropFromLootTable + dropExperience, broadcastEntityEvent(this, 3),
// setPose(DYING), store-remove + RemoveEntities broadcast) lands in Phase 29 Plan 04. THIS plan stops
// at "health hits 0": it records the lethal source so the death flow has it and marks the mob dead, so
// applyDamageEntity's lethal tail compiles and a dead mob takes no further damage (the health<=0 guard).
//
// TODO(Plan 29-04): replace this minimal seam with the real LivingEntity.die port (loot + XP + the
// death broadcast + store removal). Until then the mob is simply pinned at health 0 (isDeadOrDying), so
// it stops being hurt; it is not yet removed from the store (Plan 04's hard requirement).
func (t *TickLoop) dieEntity(e *Entity, src damageSource) {
	// Pin health at 0 (the isDeadOrDying state) and record the lethal source for the Plan-04 death flow.
	if e.health > 0 {
		e.health = 0
	}
	e.lastDamageSource = src
}
