package server

import (
	"math"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
)

// combat.go is ENT-05: the server-owned health / damage / death / respawn loop. Health is
// SERVER-owned (threat T-6-05) — a 26.2 client has NO health-setting packet; it can only
// REQUEST a respawn via ServerboundClientCommand(PERFORM_RESPAWN) (routed in server/tick.go
// dispatch). The server drives the whole loop from the tick-owned tickPlayer.health/food/
// saturation/dead fields, on the tick goroutine (TICK-05), so it is -race clean by the same
// single-owner discipline as the rest of the player state.
//
// ALL THREE WIRE LAYOUTS ARE JAR-VERIFIED (javap'd from temp/cache/26.2-inner.jar this
// session — the field order matched the plan exactly, no correction needed):
//
//	ClientboundSetHealth        = Float health + VarInt food + Float saturation
//	ClientboundPlayerCombatKill = VarInt playerId + Component message (TRUSTED_STREAM_CODEC,
//	                              an NBT component — chat.Message.WriteTo emits exactly that)
//	ClientboundRespawn          = CommonPlayerSpawnInfo.write (the Phase-5-SEALED
//	                              commonPlayerSpawnInfoEncoder) + a trailing Byte dataToKeep
//
// The Respawn packet REUSES the Phase-5-sealed commonPlayerSpawnInfoEncoder verbatim: the
// trailing dataToKeep byte is the ONLY new byte beyond the sealed encoder. We do NOT re-derive
// the spawn-info layout (06-RESEARCH Anti-Patterns: "don't rebuild the sealed encoder").

// ServerboundClientCommand action enum (jar-verified: ServerboundClientCommandPacket.Action
// has exactly two constants). PERFORM_RESPAWN (0) is the client's "I clicked Respawn" request;
// REQUEST_STATS (1) is the statistics screen open, a v1 no-op. dispatch routes only 0, and
// only when the player is actually dead.
const (
	clientCommandPerformRespawn = 0
	clientCommandRequestStats   = 1
)

// respawnDataKeepNone is the dataToKeep byte for a FULL respawn reset (v1): keep nothing.
// dataToKeep is a bitset the client ANDs in shouldKeep(flag) to decide whether to preserve
// attributes/metadata/etc across the respawn. 0 means a clean slate — the player respawns
// with default state, which is the v1 death-then-fresh-spawn behavior. (The vanilla "keep
// all data" dimension-change respawn would set bits here; that is a later concern.)
const respawnDataKeepNone = 0

// setHealth builds ClientboundSetHealth in the JAR-VERIFIED order: Float health, VarInt food,
// Float saturation (ClientboundSetHealthPacket.write: writeFloat, writeVarInt, writeFloat).
// The server sends it whenever a player's health/food/saturation changes so the client's HUD
// reflects the authoritative server state — the client never sets these itself (T-6-05).
func setHealth(health float32, food int32, sat float32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetHealth),
		pk.Float(health),
		pk.VarInt(food),
		pk.Float(sat),
	)
}

// setExperience builds ClientboundSetExperience in the JAR-VERIFIED order: Float experienceProgress,
// VarInt experienceLevel, VarInt totalExperience (ClientboundSetExperiencePacket.write: writeFloat,
// writeVarInt, writeVarInt — NOTE level BEFORE total, the opposite of the ctor arg order). The server
// sends it whenever the player's XP changes (WR-06: an XP-orb pickup) so the client's XP bar reflects
// the authoritative server state — the client never sets XP itself.
//
//	[VERIFIED javap net.minecraft.network.protocol.game.ClientboundSetExperiencePacket.write:
//	 writeFloat(experienceProgress); writeVarInt(experienceLevel); writeVarInt(totalExperience).]
func setExperience(progress float32, level, total int32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetExperience),
		pk.Float(progress),
		pk.VarInt(level),
		pk.VarInt(total),
	)
}

// ============================================================================================
// MELEE COMBAT 1:1 PORT (Plan 17-11) — LITERAL PORT of vanilla Java 26.2 (protocol 776), verified
// method-for-method against temp/cache/26.2-inner.jar via `javap -c -p` this session. No GPL
// source is pasted — the algorithm is re-expressed in Go — but the structure, ordering, numeric
// ops (float casts, clamps, the exact constants) are IDENTICAL to the bytecode.
//
// applyDamage is the LivingEntity.hurtServer(ServerLevel, DamageSource, float) port: the single
// server-authoritative damage entry point for EVERY source (attack, fall, environment), exactly
// as vanilla routes all damage through hurt -> hurtServer. It enforces the invulnerableTime
// i-frame rate limit (the anti-spam gate), then routes the surviving damage through actuallyHurt
// (armor + absorption), sets the authoritative SetHealth, and drives die() if lethal. Its callers
// (attack_dispatch.handleAttack, fall_damage.causeFallDamage) are unchanged — they call
// applyDamage(p, amount) exactly as before; the i-frame/armor logic is now applied here, as
// vanilla applies it in hurtServer for every caller.
// ============================================================================================

// tickPlayerCombat runs the per-tick melee-combat bookkeeping for every connected player: the
// attack-strength ticker increment and the i-frame / hurt-flash decrements. It is the union of two
// vanilla per-tick sites, kept in ONE method so the tick pipeline takes a single additive call:
//
//   - net.minecraft.world.entity.player.Player.tick(): `this.attackStrengthTicker++` — the
//     attack cooldown recharges by 1 tick each tick (reset to 0 on an attack via
//     resetAttackStrengthTicker, which the attack path performs).
//   - net.minecraft.server.level.ServerPlayer.tick(): `if (this.invulnerableTime > 0) this.invulnerableTime--;`
//     — the i-frame grace window counts down. NOTE the player path: LivingEntity.tick SKIPS the
//     invulnerableTime decrement for a ServerPlayer (`instanceof ServerPlayer` guard) and
//     ServerPlayer.tick does it instead, unconditionally while > 0. We mirror the ServerPlayer
//     site (the player is always a ServerPlayer here).
//   - LivingEntity.tick(): `if (this.hurtTime > 0) this.hurtTime--;` — the red-flash timer (a
//     client visual; ported for field fidelity so hurtTime mirrors vanilla).
//
// resetAttackStrengthTicker is performed at the attack site, so the increment here is the pure
// recharge. Runs on the tick goroutine over tick-owned state (TICK-05) — no locking.
func (t *TickLoop) tickPlayerCombat() {
	// NOTE: deliberately NOT traced — like syncPlayerEntities/tickFallDamage, this runs INSIDE the
	// existing tickEntities phase, so adding a trace marker would change the asserted phase order
	// (TestTickPhaseOrder). The phase pipeline records only the top-level phases.
	for _, p := range t.players {
		if p == nil {
			continue
		}
		// Player.tick: attackStrengthTicker++ (the cooldown recharge).
		p.attackStrengthTicker++
		// ServerPlayer.tick: if (invulnerableTime > 0) invulnerableTime--.
		if p.invulnerableTime > 0 {
			p.invulnerableTime--
		}
		// LivingEntity.tick: if (hurtTime > 0) hurtTime--.
		if p.hurtTime > 0 {
			p.hurtTime--
		}
		// Player.tick: if (takeXpDelay > 0) takeXpDelay-- (WR-06: the XP-orb pickup cooldown). Ported
		// here alongside the other Player/LivingEntity per-tick decrements so the orb pickup path's
		// takeXpDelay==0 gate clears 2 ticks after each collect, exactly as vanilla.
		//   [VERIFIED javap Player.tick: if (takeXpDelay > 0) takeXpDelay--.]
		if p.takeXpDelay > 0 {
			p.takeXpDelay--
		}
	}
}

// resetAttackStrengthTicker is the port of Player.resetAttackStrengthTicker():
// `this.attackStrengthTicker = 0; this.itemSwapTicker = 0;`. The attack path calls it the moment a
// swing is registered so getAttackStrengthScale restarts from 0 (the just-attacked 0.2x damage
// floor). itemSwapTicker is not modeled in v1 (no item-swap cooldown), so only the attack ticker
// is reset.
func (p *tickPlayer) resetAttackStrengthTicker() {
	p.attackStrengthTicker = 0
}

// mthClampF mirrors net.minecraft.util.Mth.clamp(float value, float min, float max):
// `value < min ? min : (value > max ? max : value)`. Ported verbatim for the attack-strength
// scale and the armor curve, both of which clamp in vanilla.
func mthClampF(value, min, max float32) float32 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// hurtCooldownConst is the invulnerableTime threshold in
// LivingEntity.hurtServer: `(float) invulnerableTime > 10.0F`. While the grace window is in this
// upper half, a new hit only applies the EXCESS over lastHurt (the anti-spam rate limit).
const hurtCooldownConst float32 = 10.0

// hurtInvulnerableTicks is the invulnerableTime value set on a FRESH hit in hurtServer
// (`this.invulnerableTime = 20`). It decrements by 1 each tick (ServerPlayer.tick), so a player
// can take a fresh full-damage hit at most once every ~10 ticks (half a second).
const hurtInvulnerableTicks int32 = 20

// hurtDurationTicks is the red-flash duration set on a fresh hit
// (`this.hurtDuration = 10; this.hurtTime = this.hurtDuration`).
const hurtDurationTicks int32 = 10

// damageFoodExhaustion is the CITED stub for DamageSource.getFoodExhaustion() at the
// Player.actuallyHurt food-exhaustion site (Plan 17-19): the vanilla DamageSource DEFAULT food
// exhaustion is 0.1f (the value most sources carry). Taking damage drains hunger by this amount.
// v1 has no per-source DamageType table wired into the damage path, so this default stands in for
// every source; it is structured so a real `damageSource.getFoodExhaustion()` per-source read slots
// in here later with no call-site change.
const damageFoodExhaustion float32 = 0.1

// applyDamage is the port of net.minecraft.world.entity.LivingEntity.hurtServer(ServerLevel,
// DamageSource, float). It is the server-authoritative entry point for every damage source (fall,
// attack, environment); the client cannot veto or claim its own health (T-6-05). Runs on the tick
// goroutine over tick-owned state (TICK-05).
//
// Faithful bytecode trace (the v1-relevant slice; world-side/effect branches noted as stubs):
//
//	if (isInvulnerableTo(...)) return false;        // v1: only the dead guard below (no /invuln cmd)
//	if (isDeadOrDying()) return false;              // -> the p.dead guard
//	if (amount < 0.0F) amount = 0.0F;               // clamp negative damage to 0
//	float lastHurtSnapshot = amount;                // (vanilla's `f1`, used by the float-blocking
//	                                                //  branch which v1 does not model)
//	// applyItemBlocking / freeze-extra / helmet-damage / NaN-Infinity clamp: v1 stubs (no items,
//	// no freeze tags, no helmet) — amount passes through unchanged. The NaN/Infinity clamp to
//	// Float.MAX_VALUE is preserved (a damage of NaN/Inf becomes the finite max) for fidelity.
//	if ((float) invulnerableTime > 10.0F && !BYPASSES_COOLDOWN) {
//	    if (amount <= lastHurt) return false;       // not greater than the last hit: NO damage
//	    actuallyHurt(amount - lastHurt);            // only the EXCESS lands
//	    lastHurt = amount;
//	} else {
//	    lastHurt = amount;
//	    invulnerableTime = 20;
//	    actuallyHurt(amount);
//	    hurtDuration = 10; hurtTime = hurtDuration;
//	}
//	// resolveMob/Player responsibility, broadcastDamageEvent, markHurt, dealDefaultKnockback,
//	// death sounds: world-side/visual — the death drive is the die() call after actuallyHurt
//	// reduces health to 0.
func (t *TickLoop) applyDamage(p *tickPlayer, src damageSource, amount float32) {
	// isInvulnerableTo guard (Entity.isInvulnerableToBase, the FIRST hurtServer check): a creative or
	// spectator player has abilities.invulnerable == true, so it absorbs ALL damage EXCEPT the sources
	// tagged BYPASSES_INVULNERABILITY (generic_kill from /kill, out_of_world from the void). This is why
	// a creative player takes no attack/fall/lava/drown/lightning damage yet still dies to /kill and the
	// void. Cite Entity.isInvulnerableToBase (invulnerable && !is(BYPASSES_INVULNERABILITY)).
	if (p.gameMode == gameModeCreative || p.gameMode == gameModeSpectator) && !src.is("bypasses_invulnerability") {
		return
	}

	// isDeadOrDying() guard (bytecode: isDeadOrDying ifeq -> iconst_0 ireturn): a corpse takes no
	// further damage until it respawns.
	if p.dead {
		return
	}

	// FIRE_RESISTANCE guard (LivingEntity.hurtServer bytecode 20-41): a fire-tagged source is fully
	// negated for an entity holding MobEffects.FIRE_RESISTANCE. `if (source.is(IS_FIRE) &&
	// hasEffect(FIRE_RESISTANCE)) return false;` — AFTER isDeadOrDying, BEFORE the amount<0 clamp.
	// (Player fire sources are a cited v1 deferral, so this is dormant until one lands, but the 1:1
	// guard belongs here now.)
	if src.is("is_fire") && playerHasEffect(p, effectFireResistance) {
		return
	}

	// `if (amount < 0.0F) amount = 0.0F;` (bytecode: fload_3 fconst_0 fcmpg ifge -> fconst_0 fstore_3).
	if amount < 0.0 {
		amount = 0.0
	}

	// NaN/Infinity clamp to Float.MAX_VALUE (bytecode: Float.isNaN / Float.isInfinite -> ldc
	// 3.4028235E38f fstore_3). Preserved verbatim so a degenerate damage value becomes the finite
	// max rather than poisoning the health subtraction.
	if isNaN32(amount) || isInf32(amount) {
		amount = maxFloat32
	}

	// The invulnerableTime i-frame gate (bytecode 184–272): the anti-spam rate limit. While the
	// grace window is in its upper half ((float) invulnerableTime > 10.0F) and the source does not
	// bypass the cooldown (v1 has no BYPASSES_COOLDOWN damage types — always false), a new hit only
	// applies the EXCESS of amount over the previous hit's lastHurt; a hit that is not GREATER than
	// lastHurt deals nothing at all (return false). Otherwise (fresh window) the full amount lands,
	// lastHurt is recorded, invulnerableTime is armed to 20, and the hurt-flash timers are set.
	if float32(p.invulnerableTime) > hurtCooldownConst {
		// `if (amount <= lastHurt) return false;` (bytecode: fload_3 getfield lastHurt fcmpg ifgt
		// -> iconst_0 ireturn). A spam-click within the window with no greater damage is a no-op.
		if amount <= p.lastHurt {
			return
		}
		// `actuallyHurt(amount - lastHurt);` then `lastHurt = amount;` — only the excess lands.
		t.actuallyHurt(p, amount-p.lastHurt)
		p.lastHurt = amount
		// tookFullDamage == true on this i-frame EXCESS branch (the hit landed its excess), so vanilla
		// fires the hurt animation. See broadcastPlayerDamageEvent for the full cite.
		t.broadcastPlayerDamageEvent(p, src)
		t.dealDefaultKnockbackPlayer(p, src)
	} else {
		// Fresh hit (bytecode 240–271): record lastHurt, arm the 20-tick window, apply full damage,
		// set the hurt-flash duration/time.
		p.lastHurt = amount
		p.invulnerableTime = hurtInvulnerableTicks
		t.actuallyHurt(p, amount)
		p.hurtDuration = hurtDurationTicks
		p.hurtTime = p.hurtDuration
		// tookFullDamage == true on the fresh sub-branch too — broadcast the hurt animation.
		t.broadcastPlayerDamageEvent(p, src)
		t.dealDefaultKnockbackPlayer(p, src)
	}

	// Death drive: actuallyHurt has set the authoritative health (and sent SetHealth); if it
	// reached 0 raise the death screen. Mirrors hurtServer's isDeadOrDying() tail that plays the
	// death sound and the eventual death handling — v1's die() sends the PlayerCombatKill.
	if p.health <= 0 {
		t.die(p)
	}
}

// dealDefaultKnockbackPlayer is the PLAYER-victim port of LivingEntity.dealDefaultKnockback(DamageSource,
// float, boolean), the limb of hurtServer that runs on tookFullDamage when the source is not NO_KNOCKBACK.
// It is the sibling of dealDefaultKnockbackEntity (the mob-victim port, combat_mob.go): same 0.4 power,
// same source-position direction, but it pushes the PLAYER away and sends the resulting velocity to the
// player's own client (ServerPlayer is the authority for its motion, so the server-driven impulse must be
// pushed via ClientboundSetEntityMotion — done inside knockback()). This is what gives a player hit by a
// mob (or another player) the recoil "pop"; without it a mob hit dealt damage but no knockback.
//
//	double xd = 0, zd = 0;
//	Entity direct = source.getDirectEntity();
//	if (direct instanceof Projectile) { ... }            // v1: no projectiles — skip
//	else if (source.getSourcePosition() != null) {       // the attacker's position
//	    xd = sp.x - this.getX();
//	    zd = sp.z - this.getZ();
//	}
//	this.knockback(0.4, xd, zd, source, damage);
//	if (!blocked) this.indicateDamage(xd, zd);           // ServerPlayer hurt-direction tilt — cited stub
//
// getSourcePosition() resolves to the attacking entity's (x,z): the attacker may be a MOB (resolved in
// the current region's store — doHurtTarget runs inside that region's AI step) or a PLAYER (PvP). An
// environmental/anonymous source (attacker 0 — fall/drown/starve/suffocation) leaves xd==zd==0, and
// knockback's degenerate-direction handling yields no net horizontal push: faithful (those sources are
// NO_KNOCKBACK or position-less in vanilla, so they never knock the player around).
//
//	[VERIFIED javap LivingEntity.hurtServer: if(!source.is(NO_KNOCKBACK)) dealDefaultKnockback(source,
//	 damage, blocked); LivingEntity.dealDefaultKnockback: getSourcePosition()!=null -> xd = sp.x - getX(),
//	 zd = sp.z - getZ(); knockback(0.4, xd, zd, source, damage). knockback power ldc2_w 0.4000000059604645d.]
func (t *TickLoop) dealDefaultKnockbackPlayer(p *tickPlayer, src damageSource) {
	// `if (!source.is(NO_KNOCKBACK))` — a genuine tag read (DamageTypeTags.NO_KNOCKBACK). A source in
	// that set never knocks the victim back, exactly as vanilla skips dealDefaultKnockback for it.
	if src.is("no_knockback") {
		return
	}

	// xd/zd default 0 (the dconst_0 dstore). Resolve the attacker's (x,z) as getSourcePosition():
	// try a player attacker (PvP) first, then the current region's entity store (a mob attacker —
	// doHurtTarget ticks inside that region). A 0/departed attacker leaves xd==zd==0 (position null).
	var xd, zd float64
	if src.attacker != 0 {
		if attacker := t.playerByEntityID(src.attacker); attacker != nil {
			xd = attacker.x - p.x
			zd = attacker.z - p.z
		} else if mob, ok := t.cur().entities.get(src.attacker); ok && mob != nil {
			xd = mob.x - p.x
			zd = mob.z - p.z
		}
	}

	// knockback(0.4, xd, zd): the 0.4 is the ldc2_w 0.4000000059604645d power; knockback normalizes
	// (xd,0,zd) and pushes the player away from the attacker. Use the NO-SEND core: this is vanilla's
	// dealDefaultKnockback, whose knockback sets needsSync (a deferred flush), NOT an immediate send.
	// A mob-attack hit (no causeExtraKnockback follow-up, getKnockback==0) is then re-synced by the
	// tracker; a player-attack hit's causeExtraKnockback emits the single SetEntityMotion. indicateDamage
	// is a cited v1 stub (the directional flash rides the ClientboundDamageEvent).
	changed := t.knockbackNoSend(p, knockbackDefaultPower, xd, zd)

	// For a mob-attack victim there is NO causeExtraKnockback follow-up (a base mob's ATTACK_KNOCKBACK is
	// 0, so getKnockback is 0 and the extra-knockback strength>0 guard skips). Vanilla's needsSync flush
	// reaches the client via the tracker's velocity re-sync, but to make the impulse land THIS tick (no
	// 1-tick lag on a server-authoritative player motion) send the motion now when the attacker is a mob.
	// A PvP victim's causeExtraKnockback already sends the combined velocity, so skip the send there to
	// keep exactly one SetEntityMotion per hit.
	if changed && src.attacker != 0 && t.playerByEntityID(src.attacker) == nil {
		if p.playerEntity != nil && p.client != nil {
			p.client.Send(encodeSetEntityMotion(p.playerEntity))
		}
	}
}

// broadcastPlayerDamageEvent is the port of the tookFullDamage hurt-animation broadcast inside
// net.minecraft.world.entity.LivingEntity.hurtServer for a PLAYER victim:
//
//	if (tookFullDamage) {
//	    if (blocked && blocksAttacks != null) { ... }     // shield — not modeled in v1
//	    else { level.broadcastDamageEvent(this, source); }  // <-- THE RED FLASH
//	    ...
//	}
//
// tookFullDamage is true on BOTH sub-branches where damage actually landed (the i-frame EXCESS branch
// and the FRESH-window branch); it is false ONLY on the `amount <= lastHurt` early return — which
// never reaches here. ServerLevel.broadcastDamageEvent fans
// `getChunkSource().sendToTrackingPlayersAndSelf(entity, new ClientboundDamageEventPacket(entity,
// source))` to every tracking player AND the entity itself. For a PLAYER the "and self" is NOT a
// no-op (a player has a connection): the victim's own client must receive the packet to play its red
// flash + directional knockback tilt — broadcastToTrackers excludes the actor, so the victim is sent
// the packet DIRECTLY here, then the fan-out covers every OTHER tracking player.
//
// sourceCause == sourceDirect == src.attacker (the attacker entity id for a player-vs-X melee hit;
// both absent/0 for an environmental source: fall/drown/starve/suffocation). sourceType is
// int32(src.typeTag) directly (the sorted index IS the client's damage_type holder id — no remap; the
// mob test asserts the invariant).
//
//	[VERIFIED javap LivingEntity.hurtServer tookFullDamage -> Level.broadcastDamageEvent(this, source);
//	 ServerLevel.broadcastDamageEvent -> sendToTrackingPlayersAndSelf(entity, ClientboundDamageEventPacket).]
//
// This fixes the player red-flash that was equally missing (the math-only hurt port never broadcast
// the event). markHurt + knockback stay deferred exactly as the rest of the hurt tail leaves them.
func (t *TickLoop) broadcastPlayerDamageEvent(p *tickPlayer, src damageSource) {
	pkt := encodeDamageEvent(p.entityID, int32(src.typeTag), src.attacker, src.attacker)
	// "...AndSelf": the victim's own client plays its hurt animation (broadcastToTrackers skips the
	// actor, so the self-send is explicit here).
	if p.client != nil {
		p.client.Send(pkt)
	}
	// The "tracking players" fan-out: every OTHER player that can see the victim flashes it red too.
	t.broadcastToTrackers(p.entityID, pkt)
}

// actuallyHurt is the port of net.minecraft.world.entity.LivingEntity.actuallyHurt(ServerLevel,
// DamageSource, float). It applies the armor + magic (resistance/enchant) reductions, folds in
// absorption, then subtracts the residual from health and sends the authoritative SetHealth.
//
// Faithful bytecode trace:
//
//	if (isInvulnerableTo(...)) return;                       // v1: no /invuln — never invulnerable
//	amount = getDamageAfterArmorAbsorb(source, amount);     // armor-points curve
//	amount = getDamageAfterMagicAbsorb(source, amount);     // resistance/protection — v1 no-op
//	float withAbsorb = amount;                              // (vanilla's f1)
//	amount = Math.max(amount - getAbsorptionAmount(), 0.0F);
//	setAbsorptionAmount(getAbsorptionAmount() - (withAbsorb - amount));
//	float absorbed = withAbsorb - amount;                   // stat-only (DAMAGE_DEALT_ABSORBED)
//	if (amount == 0.0F) return;                             // fully absorbed: no health change
//	getCombatTracker().recordDamage(...);                   // combat log — v1 stub
//	setHealth(getHealth() - amount);
//	setAbsorptionAmount(getAbsorptionAmount() - amount);    // (vanilla; with absorption 0 this is a no-op)
//
// v1 has no absorption attribute wired (getAbsorptionAmount() == 0 always), so the absorption
// folding is a faithful no-op today — the FORMULA is present and correct so a future MAX_ABSORPTION
// + golden-apple effect slots in unchanged. Health is clamped at 0 (never negative) as the wire
// requires; the authoritative SetHealth is sent so the client HUD follows.
func (t *TickLoop) actuallyHurt(p *tickPlayer, amount float32) {
	// isInvulnerableTo guard: v1 has no invulnerability command/flag, so this is never true — the
	// branch is preserved (constant-false) so a future creative/invuln flag slots in here.
	const isInvulnerableTo = false
	if isInvulnerableTo {
		return
	}

	// Armor-points reduction, then magic (resistance/protection) reduction — in vanilla's order.
	amount = t.getDamageAfterArmorAbsorb(p, amount)
	amount = t.getDamageAfterMagicAbsorb(p, amount)

	// Absorption folding (Math.max(amount - absorption, 0); setAbsorption(absorption - (f1 - amount))).
	// getAbsorptionAmount() is 0 in v1 (no MAX_ABSORPTION wired), so withAbsorb == amount and the
	// setAbsorption write is a no-op — the formula is kept verbatim for when absorption arrives.
	absorption := p.getAbsorptionAmount()
	withAbsorb := amount
	amount = maxF(amount-absorption, 0.0)
	p.setAbsorptionAmount(absorption - (withAbsorb - amount))

	// `if (amount == 0.0F) return;` — fully absorbed/blocked: no health change, no SetHealth.
	if amount == 0.0 {
		return
	}

	// Plan 17-19: causeFoodExhaustion(damageSource.getFoodExhaustion()) — taking damage costs
	// hunger. In Player.actuallyHurt this sits AFTER the `amount == 0.0F` guard and BEFORE the
	// setHealth subtraction (recordDamage in between is a v1 combat-log stub), so it runs ONLY when
	// residual damage actually lands. getFoodExhaustion() is the per-source value; the vanilla
	// DamageSource default is 0.1f (most sources). v1 has no per-source DamageType wired here, so the
	// CITED constant damageFoodExhaustion (0.1f) stands in — structured so a per-source read slots in
	// later. The exhaustion routes through causeFoodExhaustion (the invulnerable guard + addExhaustion).
	t.causeFoodExhaustion(p, damageFoodExhaustion)

	// setHealth(getHealth() - amount): the actual HP subtraction. Clamp at 0 (the wire never
	// carries negative health) and push the authoritative SetHealth so the client HUD follows.
	p.health -= amount
	if p.health < 0 {
		p.health = 0
	}
	udebugPlayer(p, "combat", "damage=%.2f -> hp=%.2f", amount, p.health)

	// PLUGIN-02 (Plan 22) on_damage seam — the LOCKED POST-mitigation site: `amount` here is the
	// FINAL landed damage (after armor + magic + absorption folding above), the value actually
	// subtracted from health — NOT the raw applyDamage input. Emit even when health reached 0 (the
	// death emit is separate, fired from die() via applyDamage's lethal tail). This is the discrete
	// damage occurrence, NEVER a per-tick scan. Nil-guarded; payload = entity id + final amount as
	// plain frozen scalars.
	if t.plugins != nil {
		t.plugins.Emit(host.EventDamage, host.DamageEvent{
			EntityID: int(p.entityID),
			Amount:   float64(amount),
		})
	}
	// setAbsorptionAmount(getAbsorptionAmount() - amount): with absorption 0 this stays 0; kept
	// for fidelity (vanilla performs it unconditionally after the health subtraction).
	p.setAbsorptionAmount(p.getAbsorptionAmount() - amount)

	p.client.Send(setHealth(p.health, p.food, p.saturation))
}

// getDamageAfterArmorAbsorb is the port of
// LivingEntity.getDamageAfterArmorAbsorb(DamageSource, float):
//
//	if (!source.is(BYPASSES_ARMOR)) {
//	    hurtArmor(source, amount);   // durability damage to worn armor — v1 stub (no armor items)
//	    amount = CombatRules.getDamageAfterAbsorb(this, amount, source,
//	                 (float) getArmorValue(), (float) getAttributeValue(ARMOR_TOUGHNESS));
//	}
//	return amount;
//
// v1 has no BYPASSES_ARMOR damage types (always false -> the armor curve always applies) and no
// armor items, so getArmorValue() == floor(ARMOR attribute) == 0 and ARMOR_TOUGHNESS == 0, making
// the curve a full-damage pass-through TODAY. The FORMULA is present and correct so a synthetic or
// future armor attribute reduces damage exactly as vanilla.
func (t *TickLoop) getDamageAfterArmorAbsorb(p *tickPlayer, amount float32) float32 {
	// source.is(BYPASSES_ARMOR): v1 has no such damage types — constant false, so the armor curve
	// always applies (the common melee/fall case).
	const bypassesArmor = false
	if !bypassesArmor {
		// hurtArmor(source, amount) -> Player.hurtArmor -> doHurtEquipment(FEET,LEGS,CHEST,HEAD): each worn
		// damageable armor piece with damage_on_hurt takes max(1, floor(amount/4)) durability and breaks at
		// max. v1 has no BYPASSES_ARMOR source so this always runs for a combat hit; the source-immune
		// (canBeHurtBy) check is a cited constant-true (durability.go). Runs BEFORE the absorb curve, as vanilla.
		t.doHurtEquipment(p, amount)
		// getArmorValue() == Mth.floor(getAttributeValue(ARMOR)); ARMOR_TOUGHNESS read as a double
		// then d2f, exactly as vanilla.
		armorValue := float32(p.getArmorValue())
		armorToughness := float32(p.getAttributeValue(attrArmorToughness))
		amount = combatRulesGetDamageAfterAbsorb(amount, armorValue, armorToughness)
	}
	return amount
}

// getDamageAfterMagicAbsorb is the port of
// LivingEntity.getDamageAfterMagicAbsorb(DamageSource, float). The full vanilla method applies the
// RESISTANCE mob-effect reduction and enchantment damage protection. v1 has neither effects nor
// enchantments, so every guarded branch is skipped and amount passes through unchanged — the
// structure is preserved (constant-false guards) so effects/enchants slot in here later.
//
//	if (source.is(BYPASSES_EFFECTS)) return amount;          // v1: false
//	if (hasEffect(RESISTANCE) && !source.is(BYPASSES_RESISTANCE)) { ... resistance curve ... }  // v1: no effects
//	if (amount <= 0.0F) return 0.0F;
//	if (source.is(BYPASSES_ENCHANTMENTS)) return amount;     // v1: false
//	float protection = <enchant damage protection>;          // v1: 0
//	if (protection > 0.0F) amount = CombatRules.getDamageAfterMagicAbsorb(amount, protection);
//	return amount;
func (t *TickLoop) getDamageAfterMagicAbsorb(p *tickPlayer, amount float32) float32 {
	const bypassesEffects = false
	if bypassesEffects {
		return amount
	}
	// hasEffect(RESISTANCE): v1 has no mob effects — the resistance reduction branch is skipped.
	const hasResistance = false
	if hasResistance {
		// Resistance curve (amplifier-scaled): preserved as a documented no-op branch. When mob
		// effects arrive: k = (amplifier+1)*5; amount = max(amount * (25-k)/25, 0).
		_ = p
	}
	// `if (amount <= 0.0F) return 0.0F;` (bytecode: fload_2 fconst_0 fcmpg ifgt -> fconst_0 freturn).
	if amount <= 0.0 {
		return 0.0
	}
	const bypassesEnchantments = false
	if bypassesEnchantments {
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

// die drives the death flow: it sends ClientboundPlayerCombatKill (which raises the client's
// death screen) and sets the tick-owned dead flag so the player stops taking damage and a
// subsequent ServerboundClientCommand(PERFORM_RESPAWN) is honored. The combat message is a
// minimal generic death text for v1 (the real damage-source attribution is a later concern).
// Runs on the tick goroutine (TICK-05).
func (t *TickLoop) die(p *tickPlayer) {
	p.dead = true
	p.client.Send(playerCombatKill(p.entityID, chat.Text("You died")))

	// PLUGIN-02 (Plan 22) on_entity_death seam: fire ONCE per death here, at the discrete death
	// occurrence — NEVER from a per-tick scan. Nil-guarded; the payload carries the dead entity's id
	// + its wire type id (a player's type id is 0 here — the player's own Entity carries the real
	// type; v1 routes all death through the player path) as plain frozen scalars.
	if t.plugins != nil {
		t.plugins.Emit(host.EventEntityDeath, host.EntityDeathEvent{
			EntityID: int(p.entityID),
			TypeID:   0,
		})
	}
}

// playerCombatKill builds ClientboundPlayerCombatKill in the JAR-VERIFIED order: VarInt
// playerId + Component message (ClientboundPlayerCombatKillPacket.STREAM_CODEC composites
// ByteBufCodecs.VAR_INT then ComponentSerialization.TRUSTED_STREAM_CODEC). chat.Message.WriteTo
// emits the NBT component the TRUSTED_STREAM_CODEC expects. This packet IS the death screen on
// the client.
func playerCombatKill(playerID int32, message chat.Message) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundPlayerCombatKill),
		pk.VarInt(playerID),
		message,
	)
}

// respawnPacket builds ClientboundRespawn by REUSING the Phase-5-sealed
// commonPlayerSpawnInfoEncoder (CommonPlayerSpawnInfo.write) followed by the trailing Byte
// dataToKeep — the ONLY new byte beyond the sealed encoder (jar-verified:
// ClientboundRespawnPacket.write calls commonPlayerSpawnInfo.write(buf) then buf.writeByte(
// dataToKeep)). We do NOT re-derive the spawn-info layout; the encoder is the same one the
// Login bootstrap uses, capture-diff-sealed in Phase 5.
func respawnPacket(dataToKeep byte) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundRespawn),
		commonPlayerSpawnInfoEncoder{},
		pk.Byte(dataToKeep),
	)
}

// performRespawn completes the death->respawn loop for a DEAD player (the dispatch route only
// calls it when player.dead is true). It:
//
//  1. sends ClientboundRespawn (the sealed spawn-info encoder + dataToKeep) so the client tears
//     down its death screen and rebuilds a fresh ClientLevel;
//  2. resets the tick-owned health/food/saturation to a full survival player and clears dead;
//  3. re-teleports the player to the world spawn with a FRESH teleport id and re-arms the
//     confirm gate (confirmedTeleport=false), mirroring the bootstrap, so movement is gated
//     until the client echoes the new teleport (PLAY-02 / T-5-03);
//  4. resets the streamer (centerSent=false + clears sentChunks) so flushOutbound re-streams
//     the whole view ring into the fresh ClientLevel (a respawn rebuilds the client's world,
//     so every column must be re-sent — exactly the recenter discipline, but full).
//
// Runs on the tick goroutine over tick-owned state (TICK-05); every Send goes through the
// bounded outbound queue so the writeLoop stays the sole socket writer (T-5-06).
func (t *TickLoop) performRespawn(p *tickPlayer) {
	// (1) Respawn: rebuild the client's world. dataToKeep=0 → a full reset (v1 death respawn).
	// ClientboundRespawn tears down the old ClientLevel and builds a fresh, EMPTY one — so the
	// client now waits for the SAME bootstrap framing the join used before it will render the
	// world: a GameEvent(LEVEL_CHUNKS_LOAD_START) that tells the client "chunks are coming"
	// (without it the client sits on "Loading terrain…" indefinitely after a respawn), the
	// PlayerPosition teleport, and the early-Play tail (abilities + held slot). Mirroring the
	// bootstrap here is the load-bearing fix: a respawn is, to the client, a second join into a
	// fresh ClientLevel.
	p.client.Send(respawnPacket(respawnDataKeepNone))
	p.client.Send(writeGameEventPacket(gameEventLevelChunksLoadStart, 0))

	// (2) Restore a full survival player AND push the authoritative SetHealth. Resetting the
	// tick-owned health alone is NOT enough — the client's HUD still shows 0 HP (and keeps the
	// death screen up) until it receives a ClientboundSetHealth with the restored value. Send it
	// so the client clears the death overlay and shows full hearts.
	p.health = maxHealth
	p.food = maxFood
	p.saturation = defaultSaturation
	p.dead = false
	p.client.Send(setHealth(p.health, p.food, p.saturation))

	// (3) Re-teleport to the world spawn with a fresh, tick-allocated teleport id, re-arming the
	// confirm gate. 17-06 spawn-inside-a-block fix: prefer the SAFE spawn point (the ported vanilla
	// PlayerSpawnFinder column over the FULLY-DECORATED spawn chunk — the air cell ON a standable
	// floor, which may NOT be the (8,8) center when a tree/structure occupies it), the SAME
	// placement the join bootstrap uses. Only when no safe point was wired (SetSpawnPoint never
	// called — e.g. a SetSpawn-only unit test) does it fall back to the blind center column.
	spawnX := 8.5 // chunk (0,0) block center
	spawnZ := 8.5
	spawnY := float64(t.spawnSurfaceY + 2)
	if t.hasSpawnPoint {
		spawnX, spawnY, spawnZ = t.spawnPoint.X, t.spawnPoint.Y, t.spawnPoint.Z
	}
	p.center = chunkCenterOf(int32(math.Floor(spawnX)), int32(math.Floor(spawnZ)))
	teleportID := t.nextTeleportID()
	p.awaitingTeleport = teleportID
	p.confirmedTeleport = false
	p.client.Send(writePlayerPositionPacket(teleportID, spawnX, spawnY, spawnZ, 0, 0))

	// (4) Re-send the early-Play tail (abilities + held slot) the join bootstrap sends, so the
	// fresh ClientLevel has the player's movement abilities + hotbar selection restored.
	p.client.Send(writePlayerAbilities(false, false, false, false, defaultFlyingSpeed, defaultWalkingSpeed))
	p.client.Send(writeSetHeldSlot(defaultHeldSlot))

	// (5) Reset the streamer so the fresh ClientLevel re-streams the full ring. flushOutbound
	// re-emits SetChunkCacheCenter under !centerSent and re-sends every column under the cleared
	// sent-set.
	p.centerSent = false
	p.sentChunks = make(map[level.ChunkPos]bool)

	// (6) Reset the combat i-frame state so a respawned player starts with a clean grace window
	// and hurt-flash timers. Vanilla's respawn rebuilds the LivingEntity (a fresh ServerPlayer),
	// so invulnerableTime/lastHurt/hurtTime all start at 0 — a freshly respawned player can take a
	// full hit immediately. attackStrengthTicker is left to keep ramping (the attack cooldown is
	// not death-reset in vanilla), but the damage-side gate must be clean.
	p.invulnerableTime = 0
	p.lastHurt = 0
	p.hurtTime = 0
	p.hurtDuration = 0

	// (7) Re-send the authoritative inventory ContainerSetContent. ClientboundRespawn (step 1) tore
	// down the old ClientLevel — including the client's inventory menu — so without this the player's
	// inventory renders EMPTY/invisible after a death-respawn even though the server still holds every
	// item. Vanilla re-syncs it: PlayerList.respawn() calls ServerPlayer.initInventoryMenu() ->
	// initMenu(inventoryMenu) -> menu.sendAllDataToRemote() (the full ContainerSetContent for window 0).
	// sendContent is that exact re-sync (bumps stateID + sends the snapshot). The bootstrapped flag is
	// also re-armed false so syncJoinInventories stays consistent for any later first-tick path.
	//   [VERIFIED javap: net.minecraft.server.players.PlayerList.respawn -> player.initInventoryMenu();
	//    ServerPlayer.initInventoryMenu -> initMenu(this.inventoryMenu) -> sendAllDataToRemote.]
	p.bootstrapped = true // already past join-bootstrap; the explicit resend below is the respawn sync
	t.sendContent(p)
}

// ============================================================================================
// CombatRules ports — net.minecraft.world.damagesource.CombatRules, the armor + protection curves.
// Verified against `javap -c -p net.minecraft.world.damagesource.CombatRules` this session.
// ============================================================================================

// combatRulesGetDamageAfterAbsorb is the port of
// CombatRules.getDamageAfterAbsorb(LivingEntity, float damage, DamageSource, float armor,
// float armorToughness). The enchantment armor-effectiveness modifier (modifyArmorEffectiveness)
// has no enchantments in v1, so the `weapon != null && serverLevel` branch is skipped and `f3`
// (the clamped 0..1 effectiveness factor) is the raw armorClamp.
//
// Faithful bytecode trace:
//
//	float f = 2.0F + armorToughness / 4.0F;
//	float armorClamp = Mth.clamp(armor - damage / f, armor * 0.2F, 20.0F);
//	float f3 = armorClamp / 25.0F;
//	// (enchant armor-effectiveness modifier) -> v1: f4 = f3 (no enchantments)
//	float f5 = 1.0F - f4;
//	return damage * f5;
//
// With armor=0, armorToughness=0: f=2.0, armorClamp = clamp(0 - damage/2, 0, 20) = 0 (since the
// inner value is <= 0 and the min is armor*0.2 = 0), f3=0, f5=1.0 -> returns damage (full).
// With a synthetic armor=20, toughness=0: f=2.0, inner = 20 - damage/2; for damage=10 that is 15,
// clamped to [4, 20] -> 15, f3=0.6, f5=0.4 -> 10 * 0.4 = 4.0 (vanilla's reduced value).
func combatRulesGetDamageAfterAbsorb(damage, armor, armorToughness float32) float32 {
	f := 2.0 + armorToughness/4.0
	armorClamp := mthClampF(armor-damage/f, armor*0.2, 20.0)
	f3 := armorClamp / 25.0
	// Enchantment armor-effectiveness (modifyArmorEffectiveness) — v1 has no enchantments and no
	// weapon component to read, so f4 == f3 (the `aload weapon ifnull` / non-ServerLevel branch).
	f4 := f3
	f5 := 1.0 - f4
	return damage * f5
}

// combatRulesGetDamageAfterMagicAbsorb is the port of
// CombatRules.getDamageAfterMagicAbsorb(float damage, float protection):
//
//	float f = Mth.clamp(protection, 0.0F, 20.0F);
//	return damage * (1.0F - f / 25.0F);
//
// v1 never reaches it (protection is always 0, so getDamageAfterMagicAbsorb's `if (protection > 0)`
// guard is false), but the function is present and correct for when enchantments arrive.
func combatRulesGetDamageAfterMagicAbsorb(damage, protection float32) float32 {
	f := mthClampF(protection, 0.0, 20.0)
	return damage * (1.0 - f/25.0)
}

// getArmorValue is the port of LivingEntity.getArmorValue():
// `Mth.floor(getAttributeValue(Attributes.ARMOR))`. With no armor items the ARMOR attribute is its
// base 0.0, so this returns 0 in v1; the floor of the double attribute is the exact vanilla op.
func (p *tickPlayer) getArmorValue() int {
	return int(math.Floor(p.getAttributeValue(attrArmor)))
}

// getAbsorptionAmount is the port of LivingEntity.getAbsorptionAmount(): the current absorption
// (golden-apple "yellow heart") shield. v1 has no absorption source wired, so it is always 0 — the
// accessor exists so actuallyHurt's absorption folding reads/writes it faithfully (and a future
// MAX_ABSORPTION + effect slots in by storing a non-zero value here). The field is added to the
// holder lazily via the dedicated absorption field below.
func (p *tickPlayer) getAbsorptionAmount() float32 {
	return p.absorptionAmount
}

// setAbsorptionAmount is the port of LivingEntity.setAbsorptionAmount(float): vanilla clamps it to
// [0, getMaxAbsorption()]. With MAX_ABSORPTION base 0 the clamp pins it to 0 in v1; the clamp is
// preserved (max read from the attribute) so a future MAX_ABSORPTION raises the ceiling unchanged.
func (p *tickPlayer) setAbsorptionAmount(amount float32) {
	maxAbsorb := float32(p.getAttributeValue(attrMaxAbsorption))
	if amount < 0 {
		amount = 0
	}
	if amount > maxAbsorb {
		amount = maxAbsorb
	}
	p.absorptionAmount = amount
}

// maxFloat32 is java.lang.Float.MAX_VALUE (3.4028235E38f) — the NaN/Infinity clamp target in
// hurtServer.
const maxFloat32 float32 = math.MaxFloat32

// maxF mirrors java.lang.Math.max(float, float) for the absorption folding
// (`Math.max(amount - absorption, 0.0F)`).
func maxF(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// isNaN32 / isInf32 mirror java.lang.Float.isNaN / Float.isInfinite for the hurtServer
// NaN/Infinity clamp. float32 has no method form, so they are expressed via the float64 widening
// (NaN and ±Inf survive the widening, so the math.IsNaN/IsInf checks are exact).
func isNaN32(f float32) bool { return math.IsNaN(float64(f)) }
func isInf32(f float32) bool { return math.IsInf(float64(f), 0) }
