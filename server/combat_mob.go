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
	"math/rand/v2"

	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/data/entity"
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

	// WITHER BOSS (Task): WitherBoss.hurtServer overrides the LivingEntity path with the boss-specific
	// immunity gates BEFORE the shared pipeline. Gated on e.wither != nil so every other mob is a zero-cost
	// skip (the pig oracle is untouched). Returns early (no damage) on: a source in WITHER_IMMUNE_TO or from
	// another WitherBoss; getInvulnerableTicks() > 0 (the charge-up) unless BYPASSES_INVULNERABILITY; the
	// isPowered() projectile shield vs an arrow/wind-charge; or an attacker in WITHER_FRIENDS. On a surviving
	// hit it arms destroyBlocksTick = 20 + adds 3 to every idleHeadUpdates entry, then falls through to the
	// shared pipeline (Monster.hurtServer). Cite WitherBoss.hurtServer.
	if e.wither != nil {
		if !t.witherHurtServerGate(e, src) {
			return // one of the boss immunity gates rejected the hit (WitherBoss.hurtServer -> false)
		}
	}

	// FIRE_RESISTANCE guard (LivingEntity.hurtServer bytecode 20-41): `if (source.is(IS_FIRE) &&
	// hasEffect(FIRE_RESISTANCE)) return false;` — AFTER isDeadOrDying, BEFORE the amount<0 clamp.
	// Live for mobs: the on_fire tick routes through here (fire.go) and the witch self-drinks a
	// fire-resistance potion (ai_goals_witch.go), so a fire-resistant mob must ignore fire damage.
	if src.is("is_fire") && entityHasEffect(e, effectFireResistance) {
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
		// flag2 == tookFullDamage; the EXCESS branch sets it false (bytecode 234), so the hurt-animation
		// tail below is SKIPPED for an excess hit (no broadcast, no markHurt, no dealDefaultKnockback — the
		// excess lands silently). Cite LivingEntity.hurtServer (tookFullDamage=false on the i-frame excess).
		t.actuallyHurtEntity(e, src, amount-e.lastHurt)
		e.lastHurt = amount
		flag2 = false
	} else {
		// Fresh hit (bytecode 240-271): record lastHurt, arm the 20-tick window, apply full damage, set
		// the hurt-flash duration/time. flag2 stays true → the hurt-animation tail runs below.
		e.lastHurt = amount
		e.invulnerableTime = hurtInvulnerableTicks
		t.actuallyHurtEntity(e, src, amount)
		e.hurtDuration = hurtDurationTicks
		e.hurtTime = e.hurtDuration
	}

	// tookFullDamage tail (bytecode 285-370): `if (tookFullDamage) { broadcastDamageEvent; markHurt;
	// dealDefaultKnockback; }`. Gated on flag2 so an i-frame EXCESS hit does NOT re-broadcast the hurt
	// flash or re-apply the 0.4 knockback (E-5 fix — previously it ran in both branches). Cite
	// LivingEntity.hurtServer (the tookFullDamage-gated broadcast/markHurt/dealDefaultKnockback block).
	if flag2 {
		t.broadcastMobDamageEvent(e, src)
	}

	// MOB DIFF (MOB-SUB-02): the flag2 block (bytecode 444-462) — `if (flag2) { this.lastDamageSource =
	// source; this.lastDamageStamp = getGameTime(); }`. The player port omits this (no PanicGoal reader);
	// the mob port stores the genuine source so PanicGoal (P31) reads its tag and wolf anger (P36) reads
	// its attacker. lastDamageStamp (the gameTime) is not modeled here (no consumer yet) — it slots in
	// alongside this store when a reader lands; the source itself is the MOB-SUB-02 deliverable.
	if flag2 {
		e.lastDamageSource = src
		// MOB-SUB-02 / P31 (Decision B): the faithful getLastDamageSource() != null signal — a real
		// source was recorded in this same flag2 store-point (LivingEntity.hurtServer bytecode 444-462).
		// PanicGoal.shouldPanic reads this bool (NOT typeTag==0, which is minecraft:in_fire, a real
		// panic_causes member — so the id cannot be the unset sentinel; cite data/tag/tags.go:152).
		e.hasLastDamage = true
		// Phase 35 (MOB-SUB-10): the attacker-ENTITY bookkeeping HurtByTargetGoal reads — the reader
		// combat_mob.go's doc (line 108) anticipated ("lastDamageStamp slots in alongside this store when
		// a reader lands"). LivingEntity.setLastHurtByMob records the causing entity ref + the gameTime
		// stamp on a fresh hit; here src.attacker is the causing entity id (damage_source.go:71, 0 for an
		// environmental hit) and t.gametime is the per-tick counter (the same int32 cast block_break.go:230
		// uses). PURE field writes, NO RNG — cannot perturb the pig oracle (the oracle pig deals/takes no
		// damage in its window). Cite LivingEntity.actuallyHurt/setLastHurtByMob.
		e.lastHurtByMob = src.attacker
		e.lastHurtByMobTimestamp = int32(t.gametime)

		// MOB-NEUT-01 (Phase 36-01): the NeutralMob persistent-anger trigger. A WOLF hit by a PLAYER
		// becomes angry — startPersistentAngerTimer sets the anger timer, and isAngryAt then gates the
		// wolf's @4 NearestAttackableTargetGoal<Player> so the provoked wolf retaliates. The anger model
		// is the gametime-ENDPOINT (W6-DISSOLVED): angerEndTime = gameTime + PERSISTENT_ANGER_TIME.sample,
		// and isAngry() compares it to gameTime — the anger EXPIRES automatically when gameTime passes it,
		// with NO per-tick decrement and NO ResetUniversalAngerTargetGoal (both dissolved). Wolf.PERSISTENT_
		// ANGER_TIME = TimeUtil.rangeOfSeconds(20,39) = UniformInt(400,780); sample = 400 + nextInt(381).
		//
		// GATES: typ == entity.Wolf.ID (only a wolf gets angry) AND the attacker is a PLAYER
		// (playerByEntityID resolves it — a wolf provoked by a mob does not start the player-anger timer;
		// the HurtByTargetGoal handles the generic retaliation). The single nextInt(381) draw is on the
		// wolf's OWN mobRandom stream — the pig (never a wolf) draws ZERO (its oracle stream is unperturbed),
		// and a non-player attacker draws ZERO. Uses the SAME mobRandom(e) the combat kinds draw from AND
		// the SAME t.gametime the MeleeAttackGoal cooldown reads.
		//	[VERIFIED javap Wolf.startPersistentAngerTimer: setTimeToRemainAngry(PERSISTENT_ANGER_TIME
		//	 .sample(this.random)); UniformInt.sample = minInclusive + nextInt(max-min+1) = 400 + nextInt(381).
		//	 NeutralMob.setPersistentAngerTarget(entity); setRemainingPersistentAngerTime → angerEndTime =
		//	 gameTime + sampled. NeutralMob.isAngry(): angerEndTime > 0 && (angerEndTime - gameTime) > 0.]
		// IRON GOLEM (Task): the golem is ALSO a NeutralMob. IronGolem.PERSISTENT_ANGER_TIME =
		// TimeUtil.rangeOfSeconds(20,39) == UniformInt(400,780) — IDENTICAL to the wolf's, so the same
		// 400 + nextInt(381) sample applies. A golem hit by a PLAYER becomes angry, and its @3 anger-gated
		// NearestAttackableTargetGoal<Player> (angry_player_target) then retaliates. Gated on typ ==
		// entity.Wolf.ID || entity.IronGolem.ID (only these two NeutralMobs get the player-anger timer) AND a
		// PLAYER attacker. The single nextInt(381) draw is on the mob's OWN mobRandom stream (the pig — never a
		// wolf/golem — draws ZERO). Cite IronGolem.startPersistentAngerTimer + NeutralMob.isAngry.
		// ZOMBIFIED PIGLIN (GAP): a NeutralMob too. ZombifiedPiglin.PERSISTENT_ANGER_TIME =
		// TimeUtil.rangeOfSeconds(20,39) == UniformInt(400,780) -- IDENTICAL to the wolf/golem, so the SAME
		// 400 + nextInt(381) sample. A zombified piglin hit by a PLAYER becomes angry (neutral-until-
		// provoked), and its anger-gated target goal + alertOthers pack-spread then retaliate. Added to the
		// wolf/golem gate; the single nextInt(381) draw is on the mob OWN stream (the pig draws ZERO). Cite
		// ZombifiedPiglin(NeutralMob).startPersistentAngerTimer.
		if (e.typ == entity.Wolf.ID || e.typ == entity.IronGolem.ID || e.typ == entity.ZombifiedPiglin.ID) && t.playerByEntityID(src.attacker) != nil {
			// DRAW (the anger timer, wolf/golem-gated, player-attacker-gated): UniformInt(400,780).sample =
			// 400 + nextInt(381). ONE draw per fresh hit on a wolf/golem by a player.
			e.angerEndTime = t.gametime + int64(400+mobRandom(e).nextInt(381))
			e.angerTarget = src.attacker // setPersistentAngerTarget(the attacking player)
		}

		// SQUID (Task): Squid.hurtServer tail -- after a landed hit, if getLastHurtByMob() != null,
		// spawnInk(). getLastHurtByMob() is non-null exactly when the attacker is a mob/player (src.attacker
		// != 0, an environmental hit leaves it 0). Squid-gated + inside the flag2 (tookFullDamage) block so
		// an i-frame excess hit does not re-ink. RNG-free. Cite Squid.hurtServer + Squid.spawnInk.
		if e.isSquid && src.attacker != 0 {
			t.squidSpawnInk(e)
		}
	}

	// Death-or-hurt-sound drive (bytecode 370-423): `if (isDeadOrDying()) { ...getDeathSound...; die(source); }
	// else if (tookFullDamage) { playHurtSound(source); playSecondaryHurtSound(source); }`. actuallyHurtEntity
	// has set the authoritative health; if it reached 0 run the death flow, OTHERWISE (the mob survived the
	// hit) play the HURT SOUND. Reaching here at all means tookFullDamage was true (the only non-tookFullDamage
	// path, the `amount <= lastHurt` i-frame rejection, returned early above), so the `else if (tookFullDamage)`
	// is unconditionally the hurt-sound branch on survival.
	//
	//	[VERIFIED javap LivingEntity.hurtServer (bytecode 370-423): if(isDeadOrDying()) { if(!checkTotem...)
	//	 { if(tookFullDamage) { makeSound(getDeathSound()); playSecondaryHurtSound(source); } die(source); } }
	//	 else if(tookFullDamage) playHurtSound(source). The DEATH SOUND (getDeathSound/makeSound) fires on
	//	 the killing blow, BEFORE die(); the HURT sound only on survival. Reaching this `if e.health <= 0`
	//	 here means tookFullDamage was true (the only non-tookFullDamage path, the `amount <= lastHurt`
	//	 i-frame rejection, returned early above), so the death-sound branch is unconditional on a kill.]
	if e.health <= 0 && !t.checkTotemDeathProtectionEntity(e, src) {
		// makeSound(getDeathSound()): the pig's death sound (entity.pig.death, id 1270), played BEFORE
		// die() exactly as the bytecode orders it (offset 390-395 makeSound, 403-405 die). checkTotem-
		// DeathProtection runs FIRST (bytecode 377-382): a mob holding a Totem of Undying survives at 1 HP
		// and CANCELS the death (gated on a totem actually held -- no totem -> the identical old kill path).
		t.playMobDeathSound(e)
		t.dieEntity(e, src)
	} else {
		// playHurtSound(source) -> makeSound(getHurtSound(source)) -> playSound(sound, getSoundVolume(),
		// getVoicePitch()). playSecondaryHurtSound is a v1 no-op (it is the shield/armor secondary clink,
		// with no v1 source). WR-05: the reported "no sound on hit" gap.
		t.playMobHurtSound(e)
	}

	// MOB-HOST-08 (Task #9): the EnderMan.hurtServer teleport reaction — a per-type post-hurt hook (gated
	// on typ == entity.Enderman.ID) run AFTER the shared hit lands. A projectile/indirect hit makes the
	// enderman dodge; a non-living-attacker hit teleports on nextInt(10)!=0; a player melee hit does NOT
	// teleport. Guarded on survival inside endermanHurtTeleport. ADDITIVE + enderman-gated (zero draws for
	// every non-enderman — the pig oracle stream is untouched). Cite EnderMan.hurtServer.
	if e.typ == entity.Enderman.ID {
		t.endermanHurtTeleport(e, src)
	}

	// MOB-HOST-05 (infest goals): Silverfish.hurtServer arms the SilverfishWakeUpFriendsGoal when hit by
	// an entity source (or an ALWAYS_TRIGGERS_SILVERFISH source) — a per-type post-hurt hook (gated on
	// typ == entity.Silverfish.ID) run AFTER the shared hit lands, the sibling of the enderman hook.
	// ADDITIVE + silverfish-gated (zero cost / zero RNG for every non-silverfish — the pig oracle stream
	// is untouched). Cite Silverfish.hurtServer + SilverfishWakeUpFriendsGoal.notifyHurt.
	if e.typ == entity.Silverfish.ID {
		t.silverfishNotifyHurt(e, src)
	}

	// SKILLS-01 (mob_skills.go): the declared-skill "damaged" trigger — the MythicMobs ~onDamaged
	// analogue. Fires AFTER the shared hit fully landed (the per-type post-hurt hooks above are its
	// siblings), SURVIVOR only (a lethal hit routes the "death" trigger through dieEntity instead).
	// Gated on e.skills != nil: every vanilla mob (the pig oracle) pays one nil-check and NOTHING
	// else — zero new draws on any vanilla stream.
	if e.skills != nil && e.health > 0 {
		// MODEL-M5 (H.2.3): thread the resolved hit bone into the damaged-trigger context so a
		// condition("hit_bone", value="head") can gate a headshot skill. src.hitBone is "" for every
		// non-model hit (the pig oracle path) -> the condition fails closed, no behavior change.
		t.fireMobSkillTrigger(e, triggerDamaged, skillTriggerCtx{attackerID: src.attacker, boneName: src.hitBone})
	}
}

// playMobHurtSound is the port of LivingEntity.playHurtSound(DamageSource) -> makeSound(getHurtSound(
// source)) -> Entity.playSound(SoundEvent, float, float) -> Level.playSound(...) for a mob, emitting the
// entity-attached hurt sound to every player tracking the mob. It is the WR-05 fix (the codebase had no
// sound packet at all). Decompiled this session:
//
//	playHurtSound(source): makeSound(getHurtSound(source));
//	makeSound(sound): if (sound != null) playSound(sound, getSoundVolume(), getVoicePitch());
//	getSoundVolume(): 1.0F (LivingEntity default);
//	getVoicePitch(): isBaby() ? (nextFloat()-nextFloat())*0.2 + 1.5 : (nextFloat()-nextFloat())*0.2 + 1.0;
//	Entity.playSound(sound, vol, pitch): if (!isSilent()) level.playSound(null, x, y, z, sound,
//	    getSoundSource(), vol, pitch);  // -> seeded with level.soundSeedGenerator.nextLong()
//
// The pig's getHurtSound resolves through its PigSoundVariant CLASSIC sound set to SoundEvents.PIG_HURT
// ("entity.pig.hurt", registry id 1269). getSoundSource() == SoundSource.NEUTRAL (Animal override). The
// volume is 1.0; the voice pitch is the non-baby formula (the v1 pig is never a baby): (nextFloat() -
// nextFloat()) * 0.2 + 1.0, drawn from the MOB's per-entity RNG (mobRandom — Mob.getRandom()). The seed
// is a FRESH server-generated draw (Level.soundSeedGenerator.nextLong() analogue — math/rand/v2, never
// client-supplied, the same event-time discipline death loot uses), NOT the mob's stream, so it does not
// perturb the pig oracle. The packet broadcasts to trackers exactly as the damage-event/death-status do.
//
// ORACLE NOTE: the getVoicePitch nextFloat()-nextFloat() draws come from the mob RNG, but they fire on a
// HIT event (inside applyDamageEntity), never during the AI tick the pig oracle (TestPluginPigEqualsGoNativePig)
// runs — that oracle deals NO damage in its 500-tick window — so the draws are outside the pinned stream
// (PITFALLS Pitfall 5). Confirmed: the oracle stays green.
//
//	[VERIFIED javap LivingEntity.playHurtSound/makeSound/getSoundVolume(==1.0F)/getVoicePitch
//	 ((nextFloat()-nextFloat())*0.2+1.0 non-baby); Pig.getHurtSound -> PigSoundVariant CLASSIC hurtSound
//	 == SoundEvents.PIG_HURT; Animal.getSoundSource == NEUTRAL; Entity.playSound -> Level.playSound (null
//	 player, x/y/z, sound, source, vol, pitch) seeded soundSeedGenerator.nextLong().]
func (t *TickLoop) playMobHurtSound(e *Entity) {
	// getVoicePitch() (non-baby): (nextFloat() - nextFloat()) * 0.2 + 1.0, from the mob's RandomSource.
	rng := mobRandom(e)
	pitch := (rng.nextFloat()-rng.nextFloat())*mobVoicePitchJitter + mobVoicePitchBase

	// The per-sound seed: a fresh server draw (Level.soundSeedGenerator.nextLong() analogue), never the
	// mob's stream and never client-supplied — the same event-time RNG discipline death loot uses.
	seed := rand.Int64()

	// playSound(sound, getSoundVolume()==1.0, pitch) -> Level.playSound(...) for the pig hurt sound on the
	// NEUTRAL category, broadcast to every player tracking the mob (sendToTrackingPlayers analogue). isSilent()
	// is a v1 constant-false (no DATA_SILENT mob is wired), so the sound always plays — cited.
	hurtID, _ := mobSoundIDs(e)
	t.broadcastToTrackers(e.id, encodeSoundEntity(hurtID, soundSourceNeutral, e.id, mobHurtSoundVolume, pitch, seed))
}

// playMobDeathSound is the port of the DEATH-sound limb of LivingEntity.hurtServer's lethal branch —
// `makeSound(getDeathSound())` (the killing-blow sound, distinct from the survival hurt sound). It is
// the sibling of playMobHurtSound (same makeSound -> getSoundVolume()==1.0 -> getVoicePitch() pitch ->
// Entity.playSound -> Level.playSound on the NEUTRAL category, broadcast to trackers), differing ONLY
// in the sound id: the pig's getDeathSound resolves through its PigSoundVariant CLASSIC sound set to
// SoundEvents.PIG_DEATH ("entity.pig.death", id 1270 — the death sibling of the CLASSIC hurt sound
// PIG_HURT/1269 the hurt path uses).
//
//	makeSound(sound): if (sound != null) playSound(sound, getSoundVolume(), getVoicePitch());
//	getSoundVolume(): 1.0F (LivingEntity default);
//	getVoicePitch(): non-baby (nextFloat()-nextFloat())*0.2 + 1.0, drawn from the MOB's per-entity RNG;
//	Entity.playSound: if (!isSilent()) level.playSound(null, x, y, z, sound, getSoundSource()==NEUTRAL,
//	    vol, pitch);  // seeded with a fresh server draw (Level.soundSeedGenerator.nextLong() analogue)
//
// The seed is a fresh server-generated math/rand/v2 draw (NEVER the mob's stream, never client-supplied
// — the same event-time discipline death loot / the hurt sound use), so it does not perturb the pig
// oracle. The voice-pitch nextFloat()-nextFloat() draws come from the mob RNG but fire on the DEATH
// event (inside applyDamageEntity's lethal branch), never during the AI tick the pig oracle exercises
// (that oracle deals NO damage and is never killed), so they are outside the pinned in-window stream
// (PITFALLS Pitfall 5) — the oracle stays green.
//
//	[VERIFIED javap LivingEntity.hurtServer death branch: makeSound(getDeathSound()) at offset 390-395
//	 (BEFORE die() at 403-405); LivingEntity.makeSound/getSoundVolume(==1.0F)/getVoicePitch (non-baby
//	 (nextFloat()-nextFloat())*0.2+1.0); Pig.getDeathSound -> getSoundSet().deathSound() -> PigSoundVariant
//	 CLASSIC deathSound == SoundEvents.PIG_DEATH; Animal.getSoundSource == NEUTRAL; data/soundid/soundid.go
//	 1270: "entity.pig.death".]
func (t *TickLoop) playMobDeathSound(e *Entity) {
	// getVoicePitch() (non-baby): (nextFloat() - nextFloat()) * 0.2 + 1.0, from the mob's RandomSource —
	// the SAME formula playMobHurtSound uses (it is LivingEntity.getVoicePitch, shared by every sound).
	rng := mobRandom(e)
	pitch := (rng.nextFloat()-rng.nextFloat())*mobVoicePitchJitter + mobVoicePitchBase

	// The per-sound seed: a fresh server draw (Level.soundSeedGenerator.nextLong() analogue), never the
	// mob's stream and never client-supplied — the event-time RNG discipline the hurt sound / death loot use.
	seed := rand.Int64()

	// playSound(getDeathSound()==PIG_DEATH, getSoundVolume()==1.0, pitch) -> Level.playSound(...) on the
	// NEUTRAL category, broadcast to every player tracking the dying mob. isSilent() is a v1 constant-false
	// (no DATA_SILENT mob wired) so the death sound always plays — cited.
	_, deathID := mobSoundIDs(e)
	t.broadcastToTrackers(e.id, encodeSoundEntity(deathID, soundSourceNeutral, e.id, mobHurtSoundVolume, pitch, seed))
}

// soundIDPigDeath is SoundEvents.PIG_DEATH's registry id ("entity.pig.death" == 1270 in
// data/soundid/soundid.go, generated from the jar's registries — the death sibling of PIG_HURT/1269).
// The pig's getDeathSound resolves to this via its CLASSIC PigSoundVariant death set.
//
//	[VERIFIED data/soundid/soundid.go: 1270: "entity.pig.death"; javap Pig.getDeathSound ->
//	 getSoundSet().deathSound() -> PigSoundVariant CLASSIC deathSound == SoundEvents.PIG_DEATH.]
const soundIDPigDeath int32 = 1270

// soundIDPigHurt is SoundEvents.PIG_HURT's registry id ("entity.pig.hurt" == 1269 in
// data/soundid/soundid.go, generated from the jar's registries). The pig's getHurtSound resolves to
// this via its CLASSIC PigSoundVariant hurt set.
//
//	[VERIFIED data/soundid/soundid.go: 1269: "entity.pig.hurt"; javap Pig.getHurtSound -> PigSoundVariant
//	 CLASSIC hurtSound == SoundEvents.PIG_HURT.]
const soundIDPigHurt int32 = 1269

// mobSoundIDs resolves a mob's (hurt, death) SoundEvent registry ids from its vanilla entity type —
// the faithful port of each Mob subclass's getHurtSound/getDeathSound override. In vanilla these live
// on the entity CLASS (not data-driven): Zombie/Spider override directly to ZOMBIE_*/SPIDER_*; Cow's
// CowSoundVariant CLASSIC and Pig's PigSoundVariant CLASSIC resolve to COW_*/PIG_*; Sheep/Chicken/
// Skeleton/Wolf resolve to their eponymous SHEEP_*/CHICKEN_*/SKELETON_*/WOLF_* sets. Keying on the
// host-set wire type (e.typ, never plugin-forgeable) reproduces that per-class dispatch. Unknown types
// fall back to the LivingEntity defaults' nearest analogue (pig) so the pig oracle path is byte-identical.
//
//	[VERIFIED javap/CFR 26.2: monster.zombie.Zombie.getHurtSound->ZOMBIE_HURT, getDeathSound->ZOMBIE_DEATH;
//	 monster.spider.Spider->SPIDER_HURT/SPIDER_DEATH; animal.cow.AbstractCow getSoundSet()==CowSoundVariant
//	 CLASSIC->COW_HURT/COW_DEATH; animal.Sheep->SHEEP_HURT/SHEEP_DEATH; animal.Chicken->CHICKEN_HURT/
//	 CHICKEN_DEATH; monster.Skeleton->SKELETON_HURT/SKELETON_DEATH; animal.wolf.Wolf getSoundSet() CLASSIC
//	 ->WOLF_HURT/WOLF_DEATH. Registry ids from data/soundid/soundid.go.]
func mobSoundIDs(e *Entity) (hurt int32, death int32) {
	switch e.typ {
	case entity.Zombie.ID:
		return 1881, 1874 // entity.zombie.hurt, entity.zombie.death
	case entity.Spider.ID:
		return 1580, 1579 // entity.spider.hurt, entity.spider.death
	case entity.Cow.ID:
		return 451, 452 // entity.cow.hurt, entity.cow.death
	case entity.Sheep.ID:
		return 1440, 1439 // entity.sheep.hurt, entity.sheep.death
	case entity.Chicken.ID:
		return 357, 358 // entity.chicken.hurt, entity.chicken.death
	case entity.Skeleton.ID:
		return 1490, 1481 // entity.skeleton.hurt, entity.skeleton.death
	case entity.Wolf.ID:
		return 1806, 1804 // entity.wolf.hurt, entity.wolf.death
	default:
		return soundIDPigHurt, soundIDPigDeath // pig + any unported mob: LivingEntity-default analogue
	}
}

// mobHurtSoundVolume is LivingEntity.getSoundVolume() == 1.0F (the default mob sound volume).
//
//	[VERIFIED javap LivingEntity.getSoundVolume: fconst_1; freturn.]
const mobHurtSoundVolume float32 = 1.0

// mobVoicePitchBase / mobVoicePitchJitter are the non-baby getVoicePitch() terms: the result is
// (nextFloat() - nextFloat()) * 0.2 + 1.0.
//
//	[VERIFIED javap LivingEntity.getVoicePitch (non-baby branch): (nextFloat() - nextFloat()) ; ldc 0.2f ;
//	 fmul ; fconst_1 ; fadd.]
const (
	mobVoicePitchBase   float32 = 1.0
	mobVoicePitchJitter float32 = 0.2
)

// broadcastMobDamageEvent is the port of the tookFullDamage hurt-animation broadcast inside
// net.minecraft.world.entity.LivingEntity.hurtServer:
//
//	if (tookFullDamage) {
//	    if (blocked && blocksAttacks != null) { ... }     // shield — not relevant to a mob (no items in v1)
//	    else { level.broadcastDamageEvent(this, source); }  // <-- THE RED FLASH
//	    if (!source.is(NO_IMPACT)) this.markHurt();          // velocity-changed metadata dirty marker
//	    if (!source.is(NO_KNOCKBACK)) { ... knockback ... }  // knockback — already deferred (combat path)
//	}
//
// tookFullDamage is true on BOTH sub-branches where damage actually landed (the i-frame EXCESS branch
// and the FRESH-window branch); it is false ONLY on the `amount <= lastHurt` early return — which
// never reaches here. ServerLevel.broadcastDamageEvent fans
// `getChunkSource().sendToTrackingPlayersAndSelf(entity, new ClientboundDamageEventPacket(entity,
// source))` out to every tracking player AND the entity itself; for a MOB the "and self" is a no-op
// (a mob has no connection), so broadcastToTrackers (the Sulfur sendToTrackingPlayers analogue, which
// already skips nobody relevant for a non-player actor) is the faithful fan-out — the SAME path
// death_mob.go uses for the death-status broadcast.
//
// sourceCause == sourceDirect == src.attacker (the causing entity for a direct hit; both the cause and
// the direct entity are the attacker for a melee hit, both absent/0 for an environmental source).
// sourceType is int32(src.typeTag) directly: the typeTag (the sorted index into DamageTypeNames) IS
// the client's damage_type holder id (both the config registrydata send order and DamageTypeNames are
// sorted alphabetically), so no remap is needed (asserted in the test).
//
//	[VERIFIED javap LivingEntity.hurtServer tookFullDamage -> Level.broadcastDamageEvent(this, source);
//	 ServerLevel.broadcastDamageEvent -> sendToTrackingPlayersAndSelf(entity, ClientboundDamageEventPacket).]
//
// markHurt() (the velocity-changed/hurt-marker metadata dirty flag) is DEFERRED here: it is NOT the
// red flash (broadcastDamageEvent IS), and the SynchedEntityData velocity-changed marker has no v1
// metadata reader yet. Cited deferral, structured so a markHurt analogue slots in unchanged when the
// metadata dirty path lands.
//
// KNOCKBACK (WR-04, the in-game "mob doesn't recoil" gap): the tookFullDamage tail's
// `if (!source.is(NO_KNOCKBACK)) dealDefaultKnockback(source, damage, blocked)` (bytecode 352-367) IS
// now ported — broadcastMobDamageEvent runs the FULL tookFullDamage tail in vanilla order
// (broadcastDamageEvent -> [markHurt deferred] -> dealDefaultKnockback), so the mob recoils on a hit.
// The damage amount (the `damage` arg dealDefaultKnockback receives) is hurtServer's fload_3 — the
// PRE-mitigation amount passed into hurtServer — but it is unused by dealDefaultKnockback/knockback in
// v1 (knockback's `damage` param feeds only the deferred projectile/effect branches), so it is not
// threaded here. blocked is the `iload 7` flag (shield-block); v1 has no shields, so blocked == false
// always, which makes indicateDamage fire — and indicateDamage is itself a deferred no-op (it is the
// client hurt-direction tilt, already covered by ClientboundDamageEvent's directional data).
//
//	[VERIFIED javap LivingEntity.hurtServer tookFullDamage tail (bytecode 321-367): broadcastDamageEvent
//	 -> if(!is(NO_IMPACT)) markHurt -> if(!is(NO_KNOCKBACK)) dealDefaultKnockback(source, damage, blocked).]
func (t *TickLoop) broadcastMobDamageEvent(e *Entity, src damageSource) {
	t.broadcastToTrackers(e.id, encodeDamageEvent(e.id, int32(src.typeTag), src.attacker, src.attacker))

	// `if (!source.is(NO_KNOCKBACK)) dealDefaultKnockback(...)` — a genuine tag read (no Phase-29 damage
	// type is in NO_KNOCKBACK, but the branch is the real read so a future no-knockback source skips it).
	if !src.is("no_knockback") {
		t.dealDefaultKnockbackEntity(e, src)
	}
}

// dealDefaultKnockbackEntity is the port of LivingEntity.dealDefaultKnockback(DamageSource, float,
// boolean) for a mob, the non-projectile branch (v1 has no projectiles). It resolves the source
// position (the attacker's position for a melee hit) and drives knockback away from it:
//
//	double xd = 0, zd = 0;
//	Entity direct = source.getDirectEntity();
//	if (direct instanceof Projectile) { ... }            // v1: no projectiles — skip
//	else if (source.getSourcePosition() != null) {
//	    Vec3 sp = source.getSourcePosition();
//	    xd = sp.x - this.getX();
//	    zd = sp.z - this.getZ();
//	}
//	this.knockback(0.4, xd, zd, source, damage);
//	if (!blocked) this.indicateDamage(xd, zd);           // indicateDamage is a no-op (bytecode `return`)
//
// getSourcePosition() returns damageSourcePosition if set, else the directEntity's position
// (DamageSource.getSourcePosition: `damageSourcePosition != null ? damageSourcePosition :
// directEntity != null ? directEntity.position() : null`). For a player melee hit the source carries
// the attacker entity (src.attacker), so the source position is the ATTACKER's (x,z) — resolved here
// via playerByEntityID. A source with no resolvable attacker position (attacker 0 / a departed
// attacker / an environmental hit) leaves xd==zd==0, and knockback's RNG guard then nudges the mob in
// a tiny random direction (vanilla's xd²+zd²<1e-5 guard) so even a zero-direction hit still recoils.
//
//	[VERIFIED javap LivingEntity.dealDefaultKnockback: getDirectEntity instanceof Projectile branch,
//	 else getSourcePosition()!=null -> xd = sp.x - getX(), zd = sp.z - getZ(); knockback(0.4, xd, zd,
//	 source, damage); if(!blocked) indicateDamage(xd, zd). LivingEntity.indicateDamage: `return` (no-op).]
func (t *TickLoop) dealDefaultKnockbackEntity(e *Entity, src damageSource) {
	// xd/zd default to 0 (the dconst_0 dstore at bytecode 0-4). v1 has no Projectile direct entity, so
	// only the getSourcePosition()!=null branch can set them.
	var xd, zd float64

	// source.getSourcePosition(): for a melee hit the directEntity == the attacker, whose position is
	// (attacker.x, attacker.z). Resolve the attacker tickPlayer; a nil resolve (no attacker / departed)
	// leaves the source position "null" (xd==zd==0), exactly as getSourcePosition returns null when
	// there is no damageSourcePosition and no directEntity.
	if src.attacker != 0 {
		if attacker := t.playerByEntityID(src.attacker); attacker != nil {
			xd = attacker.x - e.x
			zd = attacker.z - e.z
		}
	}

	// knockback(0.4, xd, zd, source, damage): the 0.4 is the ldc2_w #1792 (0.4000000059604645d) power.
	t.knockbackEntity(e, knockbackDefaultPower, xd, zd)

	// indicateDamage(xd, zd): LivingEntity.indicateDamage is `return` (a no-op for a mob — it is the
	// ServerPlayer-only client hurt-direction tilt, and the mob's directional flash already rides the
	// ClientboundDamageEvent). Cited no-op; `if (!blocked)` is always true in v1 (no shields).
}

// knockbackDefaultPower is the 0.4 power dealDefaultKnockback passes to knockback (ldc2_w
// 0.4000000059604645d — the double nearest 0.4f, exactly as the jar emits it).
//
//	[VERIFIED javap LivingEntity.dealDefaultKnockback: ldc2_w #1792 // double 0.4000000059604645d.]
const knockbackDefaultPower = 0.4000000059604645

// knockbackRngGuardThreshold is the xd²+zd² floor below which knockback nudges the direction with a
// tiny random vector (ldc2_w #493 // double 9.999999747378752E-6d — the double nearest the float 1e-5).
//
//	[VERIFIED javap LivingEntity.knockback: dload xd*xd + zd*zd ; ldc2_w 9.999999747378752E-6d ; dcmpg.]
const knockbackRngGuardThreshold = 9.999999747378752e-6

// knockbackRngNudgeScale is the 0.01 scale on each (nextDouble()-nextDouble()) random nudge component.
//
//	[VERIFIED javap LivingEntity.knockback: (random.nextDouble()-random.nextDouble()) ; ldc2_w 0.01d ; dmul.]
const knockbackRngNudgeScale = 0.01

// knockbackVerticalCap is the 0.4 cap on the on-ground vertical knockback (Math.min(0.4, dm.y/2 + power)).
//
//	[VERIFIED javap LivingEntity.knockback: ldc2_w #2299 // double 0.4d ; ... Math.min(D, D).]
const knockbackVerticalCap = 0.4

// knockbackEntity is the port of LivingEntity.knockback(double power, double xd, double zd,
// DamageSource, float damage, boolean comesFromEffect=false) for a mob — the velocity recoil itself.
// Decompiled verbatim (javap LivingEntity.knockback this session):
//
//	power *= 1.0 - getAttributeValue(KNOCKBACK_RESISTANCE);   // pig KB_RESIST base 0 -> power stays 0.4
//	if (power <= 0.0) return;
//	this.needsSync = true;                                    // v1: re-sync is implicit (tracker resends)
//	Vec3 dm = getDeltaMovement();
//	while (xd*xd + zd*zd < 9.999999747378752E-6) {            // degenerate-direction RNG guard
//	    xd = (random.nextDouble() - random.nextDouble()) * 0.01;
//	    zd = (random.nextDouble() - random.nextDouble()) * 0.01;
//	}
//	Vec3 kv = new Vec3(xd, 0.0, zd).normalize().scale(power);
//	setDeltaMovement(
//	    dm.x / 2.0 - kv.x,
//	    onGround() ? Math.min(0.4, dm.y / 2.0 + power) : dm.y,
//	    dm.z / 2.0 - kv.z);
//
// THE RNG DRAW (the while loop): the (nextDouble()-nextDouble())*0.01 nudges are drawn from the MOB's
// per-entity seeded RandomSource (mobRandom(e) == e.ai.rng, the Mob.getRandom() analogue), NOT the
// global pool — CLAUDE.md's "mirror the RNG source/draw-order EXACTLY". This is HIT-event-driven (it
// fires inside applyDamageEntity on a player hit), never inside the AI tick the pig oracle
// (TestPluginPigEqualsGoNativePig) exercises — that oracle deals NO damage in its 500-tick window — so
// these draws do not perturb the oracle's pinned in-window stream (PITFALLS Pitfall 5). The guard only
// trips when xd²+zd²<1e-5 (the attacker is essentially on top of the mob), so the draws are rare; the
// loop is ported exactly so that rare case still recoils 1:1.
//
//	[VERIFIED javap LivingEntity.knockback: power *= 1 - getAttributeValue(KNOCKBACK_RESISTANCE);
//	 if(power<=0) return; the xd²+zd²<9.999999747378752E-6 while-guard drawing this.random.nextDouble();
//	 Vec3(xd,0,zd).normalize().scale(power); setDeltaMovement(dm.x/2 - kv.x, onGround ? min(0.4, dm.y/2 +
//	 power) : dm.y, dm.z/2 - kv.z).]
func (t *TickLoop) knockbackEntity(e *Entity, power, xd, zd float64) {
	// power *= 1.0 - getAttributeValue(KNOCKBACK_RESISTANCE). The pig's KNOCKBACK_RESISTANCE base is 0
	// (registration default), so the multiplier is 1.0 and power stays 0.4; the read is GENUINE (the mob's
	// real *attribute.Map) so a future armor/effect modifier composes here with no code change.
	power *= 1.0 - e.getAttributeValue(attribute.KnockbackResistance)

	// `if (power <= 0.0) return;` — a fully knockback-resistant mob (resist >= 1.0) does not recoil.
	if power <= 0.0 {
		return
	}

	// needsSync = true: vanilla marks the entity for an immediate velocity re-sync. v1's tracker resends
	// the moved/teleported position next tick (sendEntityMotion / the move deltas), so the recoil is
	// observable without a separate needsSync field — cited no-op store.

	// Vec3 dm = getDeltaMovement() — snapshot the pre-knockback velocity (the dm.x/2 etc. all read THIS).
	dmx, dmy, dmz := e.vx, e.vy, e.vz

	// The degenerate-direction RNG guard: while xd²+zd² is below the 1e-5 floor, nudge (xd,zd) with a
	// tiny random vector drawn from the MOB's RNG so the normalize below is well-defined and the mob
	// still recoils in SOME direction (the attacker is right on top of it). Drawn from mobRandom(e).
	rng := mobRandom(e)
	for xd*xd+zd*zd < knockbackRngGuardThreshold {
		xd = (rng.nextDouble() - rng.nextDouble()) * knockbackRngNudgeScale
		zd = (rng.nextDouble() - rng.nextDouble()) * knockbackRngNudgeScale
	}

	// Vec3(xd, 0, zd).normalize().scale(power): the horizontal knockback impulse. normalize divides by
	// the length; with the guard above the length is always > 0 so this never divides by zero.
	length := math.Sqrt(xd*xd + zd*zd)
	kvx := xd / length * power
	kvz := zd / length * power

	// setDeltaMovement(dm.x/2 - kv.x, onGround ? min(0.4, dm.y/2 + power) : dm.y, dm.z/2 - kv.z): the
	// recoil halves the existing horizontal velocity and adds the impulse away from the source; the
	// vertical pop (capped at 0.4) only applies when the mob is on the ground (an airborne mob keeps its
	// falling velocity). tickPhysics integrates e.vx/vy/vz into the position next tick, so the mob visibly
	// flies back.
	e.vx = dmx/2.0 - kvx
	if e.onGround {
		e.vy = math.Min(knockbackVerticalCap, dmy/2.0+power)
	} else {
		e.vy = dmy
	}
	e.vz = dmz/2.0 - kvz
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
		// source.getWeaponItem() armor-effectiveness (Breach): nil unless the attacker's weapon carries an
		// armor_effectiveness enchant, so a no-enchant hit stays byte-identical (see combatRulesGet...).
		armorEff := t.enchArmorEffectivenessFn(enchEntityRef{mob: e}, src)
		amount = combatRulesGetDamageAfterAbsorb(amount, armorValue, armorToughness, armorEff)
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
	// EnchantmentHelper.getDamageProtection(level, this, source) (E-3): the EPF sum across the
	// MOB's equipment (a zombie in Protection armor reduces exactly as a player would). A mob with
	// no enchanted gear sums 0 and skips the curve — the pre-E-3 path, RNG-free.
	protection := t.enchDamageProtection(enchEntityRef{mob: e}, mobEquipRead(e), src)
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

// tickMobAging is the port of net.minecraft.world.entity.AgeableMob's per-tick aging — the server
// branch of aiStep (javap'd from temp/cache/26.2-inner.jar):
//
//	int age = getAge();
//	if (canAgeUp())  setAge(age + 1);   // canAgeUp() == isBaby() && !isAgeLocked() == (age < 0) (no age-lock in v1)
//	else if (age > 0) setAge(age - 1);  // breeding cooldown decays toward 0
//
// inlined to a pure-int machine on breedAge: a baby (<0) ages UP, an adult on cooldown (>0) decays
// DOWN, an un-fed adult (==0) is a NO-OP. setAge's 0-crossing side effect (flip DATA_BABY_ID + call
// ageBoundaryReached) is reproduced for the only crossing this tick can cause — the baby's -1 -> 0
// grow-up — via onGrewUp (restore the adult AABB + broadcast DATA_BABY_ID=false). A cooldown adult
// decaying to 0 does NOT cross the baby boundary (>0 -> 0, isBaby stays false), so no flip is needed.
//
// IT IS PURE INTEGER MATH (no RNG draw): the broadcast it can trigger is a tracker fan-out, not a
// random draw. Vanilla runs this in aiStep; we run it in the dedicated per-mob loop (tick_phases.go,
// the tickMobIFrames twin), structurally OUTSIDE serverAiStep's goal callbacks / navigation.tick, so
// it cannot perturb the per-mob RNG stream the pig oracle pins (the oracle pig is breedAge 0 -> this
// is a no-op on it). The observable gameplay is identical to the in-aiStep position (pure-int, no draw);
// the loop move is the cited oracle-preserving optimization (33-deviations.md).
//	[VERIFIED javap AgeableMob.aiStep server branch (offsets 74+): isAlive -> getAge -> canAgeUp ?
//	 iinc 1,1; setAge : (age>0 ? iinc 1,-1; setAge); AgeableMob.canAgeUp = isBaby() && !isAgeLocked().]
func (t *TickLoop) tickMobAging(e *Entity) {
	if e.breedAge < 0 {
		e.breedAge++
		if e.breedAge == 0 {
			t.onGrewUp(e) // -1 -> 0: restore adult dims + broadcast DATA_BABY_ID=false
		}
	} else if e.breedAge > 0 {
		e.breedAge--
	}

	// --- Animal.aiStep tail (the in-love decrement) — runs AFTER the AgeableMob aging above, exactly
	// as Animal.aiStep calls super.aiStep() (AgeableMob's aging) THEN runs its own tail. Ported verbatim
	// (33-JARNOTES.md:26-27, javap Animal.aiStep):
	//
	//	if (getAge() != 0) inLove = 0;                       // only an ADULT (age 0) can stay in love
	//	if (inLove > 0) { --inLove; if (inLove % 10 == 0) { ...heart particles... } }
	//
	// The `getAge() != 0` guard forces inLove to 0 the instant a mob is a baby OR on breeding cooldown
	// (breedAge != 0), so love-mode only ever runs on a breeding-ready adult — and is a pure-int no-op on
	// the un-fed oracle pig (breedAge 0, inLove 0: the guard does nothing, the `inLove > 0` block never
	// runs). This keeps the byte-identical gate (TestPluginPigEqualsGoNativePig): the lone-adult oracle
	// never feeds, so inLove stays 0 and this whole tail is dormant.
	//
	// THE HEART EMIT: vanilla's `inLove % 10 == 0` branch draws 3× random.nextGaussian()*0.02 (the
	// per-heart velocity) then calls Level.addParticle(HEART, ...). On a DEDICATED SERVER Level.addParticle
	// is the empty base no-op (ServerLevel does NOT override it — javap-confirmed: `Level.addParticle ->
	// return`), so NO packet is emitted from this tail; the client-facing hearts come from setInLove's
	// broadcastEntityEvent(this, 18) (broadcastHearts, fired on the FEED interaction). We STILL draw the 3
	// nextGaussian values here so the mob RNG stream stays in lockstep with vanilla's aiStep (load-bearing
	// for the breed/in-love scenario RNG and a future bit-exact source swap); they are dormant on the
	// oracle (inLove 0). This runs in the OUTSIDE-serverAiStep per-mob loop (the tickMobIFrames twin): the
	// only stream it touches is the mob's own RandomSource, and only while in love — never on the oracle.
	//	[VERIFIED javap Animal.aiStep: getAge ifeq -> inLove=0; inLove ifle skip; iinc inLove,-1; inLove
	//	 % 10 ifne skip; 3× random.nextGaussian()*0.02d; Level.addParticle(HEART, getRandomX(1),
	//	 getRandomY()+0.5, getRandomZ(1), gauss0, gauss1, gauss2). javap Level.addParticle == `return`.]
	if e.breedAge != 0 {
		e.inLove = 0
	}
	if e.inLove > 0 {
		e.inLove--
		if e.inLove%10 == 0 {
			t.emitInLoveHearts(e)
		}
	}
}

// emitInLoveHearts is the Animal.aiStep `inLove % 10 == 0` heart-particle limb. It draws the 3
// per-heart velocity gaussians from the mob's RandomSource (so the stream stays lockstep with vanilla
// — see tickMobAging's note) and would call Level.addParticle(HEART, ...), which is a no-op on a
// dedicated server (ServerLevel inherits the empty Level.addParticle). The CLIENT-visible hearts are
// delivered separately by broadcastHearts (the broadcastEntityEvent(this, 18) burst setInLove fires on
// the FEED interaction), exactly as vanilla: the server never spawns the aiStep hearts on the wire.
// Drawn ONLY while in love (dormant on the un-fed oracle pig), so it never perturbs the pinned oracle
// RNG. Cite Animal.aiStep (the gaussian draws + the no-op server addParticle).
func (t *TickLoop) emitInLoveHearts(e *Entity) {
	rng := mobRandom(e)
	// xd/yd/zd = nextGaussian() * 0.02 — the per-heart velocity (Level.addParticle's last 3 args). The
	// values feed a server-side no-op addParticle; we consume the 3 draws to keep the mob stream aligned
	// with vanilla's aiStep. Assigned to _ deliberately: the velocity never reaches a client from here.
	_ = rng.nextGaussian() * inLoveHeartGaussianScale
	_ = rng.nextGaussian() * inLoveHeartGaussianScale
	_ = rng.nextGaussian() * inLoveHeartGaussianScale
}

// inLoveHeartGaussianScale is the 0.02 factor Animal.aiStep applies to each nextGaussian() for the
// heart velocity (xd/yd/zd = nextGaussian() * 0.02).
//	[VERIFIED javap Animal.aiStep: ldc2_w 0.02d; dmul after each nextGaussian().]
const inLoveHeartGaussianScale = 0.02

// broadcastHearts fires the in-love / breeding HEART burst to the mob's trackers — the faithful
// dedicated-server heart path. Vanilla Animal.setInLove (and finalizeSpawnChildFromBreeding) calls
// Level.broadcastEntityEvent(this, (byte)18); on the server that becomes a ClientboundEntityEvent(id,
// 18) sent to every tracking player (ServerChunkCache.sendToTrackingPlayersAndSelf), and the CLIENT's
// Animal.handleEntityEvent(18) spawns the 7 hearts locally. NOTE: the server does NOT emit the per-tick
// aiStep `inLove % 10` hearts on the wire (its Level.addParticle is a no-op — see emitInLoveHearts);
// the ONLY wire hearts come from this event-18 burst on setInLove/breed. It is the SAME
// broadcastToTrackers fan-out the hurt/death sounds and the DATA_BABY_ID flip use (NOT an RNG draw).
// Called from the FEED path right after e.setInLove() (mirroring setInLove's own broadcastEntityEvent),
// and reusable by Plan C's breed() after the parents reset inLove.
//	[VERIFIED javap Animal.setInLove: bipush 18; Level.broadcastEntityEvent(this, 18) -> ServerLevel
//	 .broadcastEntityEvent -> ClientboundEntityEventPacket(this, 18) to trackers; Animal.handleEntityEvent
//	 spawns 7 HEART particles on event 18. The encodeLevelParticles encoder (entity_encode.go) is the
//	 general sendParticles wire-out; the in-love hearts specifically ride the EntityEvent, not that.]
func (t *TickLoop) broadcastHearts(e *Entity) {
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventInLoveHearts))
}

// onGrewUp is the breedAge -1 -> 0 boundary handler — the part of AgeableMob.setAge's 0-crossing side
// effect this server reproduces: restore the ADULT AABB (refreshDimensions, now that isBaby()==false)
// and broadcast DATA_BABY_ID=false to the entity's trackers so the client re-renders the pig at full
// size (the Plan-D "the baby grows to full size" live criterion). It is NOT an RNG draw — the broadcast
// is the same tracker fan-out the hurt/death sounds use (broadcastToTrackers). Cite AgeableMob.setAge
// (the DATA_BABY_ID flip on the 0-crossing) + AgeableMob.ageBoundaryReached.
//	[VERIFIED javap AgeableMob.setAge: on the sign-crossing it `entityData.set(DATA_BABY_ID, age<0)` then
//	 ageBoundaryReached(); here age becomes 0 (>=0) so DATA_BABY_ID := false.]
func (t *TickLoop) onGrewUp(e *Entity) {
	e.refreshDimensions()
	t.broadcastBabyFlag(e)
}

// broadcastBabyFlag pushes the entity's current DATA_BABY_ID (== e.isBaby()) to its trackers via
// ClientboundSetEntityData, so the client re-renders at the right size. It is the AgeableMob.setAge
// 0-crossing wire effect (entityData.set(DATA_BABY_ID, age<0)) realized as a tracker fan-out — the
// SAME broadcastToTrackers seam the hurt/death sounds and the damage-event use (combat_mob.go), NOT
// an RNG draw, so it never perturbs the per-mob oracle stream. Fired from onGrewUp on the -1 -> 0
// grow-up (pushes DATA_BABY_ID=false). Plan C's breed() can reuse it right after setting
// child.breedAge = BABY_START_AGE to push DATA_BABY_ID=true for a freshly-spawned baby.
//	[VERIFIED javap AgeableMob.setAge: on the sign-crossing entityData.set(DATA_BABY_ID, Boolean(age<0));
//	 SynchedEntityData broadcasts the changed value to trackers via ClientboundSetEntityData.]
func (t *TickLoop) broadcastBabyFlag(e *Entity) {
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, babyDataEntry(e.isBaby())))
}

// dieEntity is implemented in death_mob.go (Plan 29-04): the full LivingEntity.die port (loot + XP
// + the death-status broadcast + owner-region store removal). applyDamageEntity's lethal tail calls
// it; the seam Plan 02 left here is replaced there with no call-site change.

// checkTotemDeathProtectionEntity is the port of net.minecraft.world.entity.LivingEntity
// .checkTotemDeathProtection(DamageSource) for a mob *Entity victim -- the sibling of the *tickPlayer
// checkTotemDeathProtection (combat.go). When the mob would die holding a Totem of Undying in either hand
// (EntityEquipment MAINHAND/OFFHAND), the totem is consumed, the mob is revived at 1 HP, all its effects
// are cleared and the totem death_protection effects applied, and the totem-pop entity event (35) is
// broadcast to trackers -- the death is CANCELLED (returns true). Returns false (death proceeds) when the
// source bypasses invulnerability or no totem is held. Runs on the owning region goroutine (TICK-05).
//
// Faithful bytecode (javap LivingEntity.checkTotemDeathProtection this session): BYPASSES_INVULNERABILITY
// early-out; the InteractionHand.values scan (getItemInHand == getMainHandItem/getOffhandItem) reading
// DataComponents.DEATH_PROTECTION; on a hit copy + shrink(1); the ServerPlayer stats branch is skipped for
// a mob (a mob is not a ServerPlayer); setHealth(1.0F); DeathProtection.applyEffects (ClearAllStatusEffects
// + ApplyStatusEffects REGENERATION 900/1, ABSORPTION 100/1, FIRE_RESISTANCE 800/0); broadcastEntityEvent(
// this, 35). A mob has NO client, so unlike the player path there is no self-send / SetHealth wire -- just
// the tracker broadcast + the health mutation. The totem is identified by its item id (Items.TOTEM_OF_
// UNDYING == 1333), the DEATH_PROTECTION == TOTEM_OF_UNDYING component (v1 has no per-item component store).
//
//	[VERIFIED javap LivingEntity.checkTotemDeathProtection (shared with the player port); the mob differs
//	 ONLY in: the ServerPlayer branch is skipped, and there is no client (no SetHealth send). Effect table
//	 from DeathProtection <clinit> (REGENERATION 900/1, ABSORPTION 100/1, FIRE_RESISTANCE 800/0).]
func (t *TickLoop) checkTotemDeathProtectionEntity(e *Entity, src damageSource) bool {
	// if (source.is(BYPASSES_INVULNERABILITY)) return false -- /kill and the void ignore the totem.
	if src.is("bypasses_invulnerability") {
		return false
	}
	// Scan MAINHAND then OFFHAND for a totem. No totem in either hand -> return false (the identical old
	// kill path, zero side effects -- the oracle pig holds no totem, so it always takes this false branch).
	found := false
	var totemSlot int
	for _, slot := range []int{eqSlotMainHand, eqSlotOffHand} {
		held := e.getItemBySlot(slot)
		if held.Count > 0 && int32(held.ItemID) == totemOfUndyingItemID {
			found = true
			totemSlot = slot
			break
		}
	}
	if !found {
		return false
	}
	// held.shrink(1): consume one totem from the holding slot (setItemSlot after decrement).
	held := e.getItemBySlot(totemSlot)
	held.Count--
	if held.Count <= 0 {
		held = component.SlotData{Count: 0}
	}
	e.setItemSlot(totemSlot, held)
	// setHealth(1.0F): revive at 1 HP (a mob has no client, so no SetHealth wire -- just the mutation).
	e.health = 1.0
	// DeathProtection.applyEffects: ClearAllStatusEffects then the three ApplyStatusEffects.
	t.entityRemoveAllEffects(e)
	t.addEntityEffect(e, effectRegeneration, totemRegenerationDuration, totemRegenerationAmplifier)
	t.addEntityEffect(e, effectAbsorption, totemAbsorptionDuration, totemAbsorptionAmplifier)
	t.addEntityEffect(e, effectFireResistance, totemFireResistanceDuration, totemFireResistanceAmplifier)
	// level().broadcastEntityEvent(this, (byte) 35): the totem-pop animation, fanned to every tracking
	// player (a mob has no self-client, so this is the whole broadcast -- the "and self" is a no-op).
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventTotemOfUndying))
	return true
}

// entityRemoveAllEffects is the port of LivingEntity.removeAllEffects() for a mob -- the ClearAllStatus-
// EffectsConsumeEffect the totem death_protection applies first. v1 mobs attach no attribute modifiers for
// the self-buff effects (see mob_effect.go tickMobEffects), so removal is a plain map clear.
func (t *TickLoop) entityRemoveAllEffects(e *Entity) {
	if e == nil || len(e.mobEffects) == 0 {
		return
	}
	for id := range e.mobEffects {
		delete(e.mobEffects, id)
	}
}
