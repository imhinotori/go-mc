# Phase 36 — pre-decompiled jar notes (Wolf — neutral/tameable, the most complex single mob)

Captured 2026-06-30 while Phase 34 executed (zero-idle pre-decompile). The wolf is the LAST v5 phase
and the single most complex mob: 12 goalSelector goals + 8 targetSelector goals + the TAMING + ANGER
(neutral) + SITTING + OWNER subsystems. It reuses the passive 8-goal set (30.1–33) AND the hostile
targetSelector + MeleeAttackGoal (35) AND adds wolf-specific: tame/owner, sit, beg, follow-owner,
collar/armor, universal-anger (the "neutral" behavior). DEPENDS conceptually on Phase 35 (targetSelector,
MeleeAttackGoal, NearestAttackableTargetGoal, HurtByTargetGoal, LeapAtTargetGoal, AvoidEntityGoal) — those
must exist before the wolf is faithful. Confirm 35 shipped those before planning 36.

## ⚠ 26.2 package: net.minecraft.world.entity.animal.wolf.Wolf (Wolf extends TamableAnimal)

## --- Wolf.registerGoals (CONFIRMED via CFR) ---
```
goalSelector:
   1 FloatGoal(this)
   1 TamableAnimal.TamableAnimalPanicGoal(this, 1.5, DamageTypeTags.PANIC_ENVIRONMENTAL_CAUSES)   <-- tamable panic variant
   2 SitWhenOrderedToGoal(this)                                                                    <-- NEW (sit subsystem)
   3 WolfAvoidEntityGoal<Llama>(this, this, Llama, 24.0, 1.5, 1.5)                                  <-- NEW (avoid + un-tamed gate)
   4 LeapAtTargetGoal(this, 0.4)                                                                    <-- from 35
   5 MeleeAttackGoal(this, 1.0, true)                                                               <-- from 35
   6 FollowOwnerGoal(this, 1.0, 10.0, 2.0)                                                          <-- NEW (owner subsystem)
   7 BreedGoal(this, 1.0)                                                                           <-- from 33
   8 WaterAvoidingRandomStrollGoal(this, 1.0)                                                       <-- from 30.1
   9 BegGoal(this, 8.0)                                                                             <-- NEW (beg at food-holding player)
  10 LookAtPlayerGoal(Player, 8.0)                                                                  <-- from 30
  10 RandomLookAroundGoal                                                                           <-- from 30
targetSelector:
   1 OwnerHurtByTargetGoal(this)                                                                    <-- NEW (owner subsystem)
   2 OwnerHurtTargetGoal(this)                                                                      <-- NEW
   3 HurtByTargetGoal(this).setAlertOthers()                                                        <-- from 35
   4 NearestAttackableTargetGoal<Player>(this, Player, 10, true, false, this::isAngryAt)            <-- anger-gated
   5 NonTameRandomTargetGoal<Animal>(this, Animal, false, PREY_SELECTOR)                            <-- NEW (untamed hunts prey)
   6 NonTameRandomTargetGoal<Turtle>(this, Turtle, false, Turtle.BABY_ON_LAND_SELECTOR)             <-- NEW
   7 NearestAttackableTargetGoal<AbstractSkeleton>(this, AbstractSkeleton, false)                   <-- wolves attack skeletons
   8 ResetUniversalAngerTargetGoal<Wolf>(this, true)                                                <-- NEW (anger timer reset)
```

## --- Attributes (Wolf.createAttributes — CONFIRMED) ---
Animal.createAnimalAttributes() + MOVEMENT_SPEED 0.3 + MAX_HEALTH 8.0 + ATTACK_DAMAGE 4.0
(tamed wolves get MAX_HEALTH 40 via setTame — verify the setTame health bump at exec.)

## --- TAMING (Wolf.mobInteract + tryToTame — CONFIRMED) ---
Wolf.isFood(stack) = stack.is(ItemTags.WOLF_FOOD).
Wolf.mobInteract(player, hand):
  if (isTame()) {
      if (isFood(stack) && getHealth() < getMaxHealth()) { feed(player,hand,stack, 2.0f, 2.0f); return SUCCESS; }   // heal-feed
      if (stack.is(WOLF_COLLAR_DYES) && isOwnedBy(player)) { ... setCollarColor; consume(1); SUCCESS }                // dye collar
      if (isEquippableInSlot(stack, BODY) && !isWearingBodyArmor() && isOwnedBy && !isBaby) { ...equip armor; SUCCESS }
      if (isInSittingPose() && isWearingBodyArmor() && isOwnedBy && armor.isDamaged() && armor.isValidRepairItem(stack)) {
          shrink(1); WOLF_ARMOR_REPAIR sound; armor repair = maxDamage*0.125; SUCCESS }                              // armor repair
      InteractionResult r = super.mobInteract(player, hand);     // feed/breed path
      if (r.consumesAction() || !isOwnedBy(player)) return r;
      setOrderedToSit(!isOrderedToSit()); jumping=false; navigation.stop(); setTarget(null);                          // SIT TOGGLE
      return SUCCESS.withoutItem();
  }
  // UNTAMED:
  if (isClientSide || !stack.is(Items.BONE) || isAngry()) return super.mobInteract(player, hand);
  stack.consume(1, player);
  tryToTame(player);
  return SUCCESS_SERVER;
Wolf.tryToTame(player):
  if (random.nextInt(3) == 0) {                                  // 1/3 TAME CHANCE — RNG (mob stream); lockstep if dogfooded
      tame(player); navigation.stop(); setTarget(null); setOrderedToSit(true); broadcastEntityEvent(this, (byte)7);  // hearts
  } else {
      broadcastEntityEvent(this, (byte)6);                       // smoke (tame failed)
  }
TamableAnimal.tame(player): setTame(true, true); setOwner(player); (CriteriaTriggers.TAME_ANIMAL on ServerPlayer).
⇒ RNG: tryToTame draws ONE nextInt(3) per BONE feed on an untamed, non-angry wolf. The hearts/smoke is a
  broadcastEntityEvent (status 7/6) — same mechanism as the pig breed hearts (EntityEvent-based).

## --- NEW subsystems the wolf needs (the real 36 work — decompile each at exec) ---
1. TAME/OWNER state: DATA_FLAGS (tame/sitting bits) + owner UUID (DATA_OWNERUUID_ID) + setTame/setOwner/
   isOwnedBy/isTame. TamableAnimal base. The owner UUID persists + drives FollowOwner/OwnerHurt goals.
2. SIT: SitWhenOrderedToGoal + isOrderedToSit/setOrderedToSit + isInSittingPose + the DATA_FLAGS sit bit.
   A sitting wolf does not wander (the goal claims MOVE/JUMP and parks the mob).
3. ANGER (the "neutral" behavior): NeutralMob interface — remainingPersistentAngerTime, persistentAngerTarget
   UUID, getAngryAt/isAngryAt/startPersistentAngerTimer (nextInt in PERSISTENT_ANGER_TIME range 20-39s),
   ResetUniversalAngerTargetGoal. A wolf hit by a player becomes angry (HurtByTargetGoal) for a random timer;
   the pack alerts (setAlertOthers). isAngryAt gates the NearestAttackableTargetGoal<Player>. RNG: the anger
   timer = UniformInt(20s,39s).sample(random). Lockstep-critical if dogfooded.
4. FollowOwnerGoal / OwnerHurtByTargetGoal / OwnerHurtTargetGoal: the owner-defense + teleport-to-owner goals.
5. BegGoal: wolf sits/begs facing a player holding WOLF_FOOD within 8 blocks (head-tilt visual + a canUse scan).
6. WolfAvoidEntityGoal / NonTameRandomTargetGoal: untamed-only gates (a tamed wolf doesn't flee llamas / hunt
   prey). PREY_SELECTOR = the huntable-animal predicate (sheep/rabbit/fox/etc).
7. NearestAttackableTargetGoal + MeleeAttackGoal + LeapAtTargetGoal + HurtByTargetGoal — REUSE Phase 35's ports.
   Combat damage (4.0 ATTACK_DAMAGE) ports the Phase-31 damage path.

## --- SCOPE NOTES for the planner (when 36 opens) ---
- HARD DEPENDENCY on Phase 35: targetSelector, MeleeAttackGoal, NearestAttackableTargetGoal, HurtByTargetGoal,
  LeapAtTargetGoal, AvoidEntityGoal must exist + be proven. If 35 deferred any, the wolf inherits the gap.
- The wolf is the FIRST tameable/neutral mob — taming, owner, sitting, anger are ALL new subsystems. Likely a
  multi-plan phase (taming+owner / sit+beg / anger+neutral-target / combat-reuse / the gate). Consider whether
  the wolf gets a full Go-vs-plugin oracle (like the pig) or a per-mob behavior test — lean behavior test +
  focused RNG tests (tryToTame nextInt(3), the anger-timer UniformInt) given the subsystem count.
- RNG lockstep paths: tryToTame nextInt(3), the persistent-anger timer UniformInt(20s,39s).sample, breed (reused),
  any NonTameRandomTarget/NearestAttackableTarget gate. Each needs draw-order discipline IF dogfooded.
- DEFER candidates (cite): wolf armor (body-armor equip/repair — needs the equipment/armor system), collar dye
  (needs DyeColor component on the wolf), wolf variants (the biome-variant texture), the BEG head-tilt visual
  (client-only). The CORE 36 must-have: tame (BONE + nextInt(3)), sit-toggle, follow-owner, owner-defense,
  neutral anger (hit→angry→pack-alert→attack), untamed prey-hunting, the wolf melee combat. Confirm scope vs ROADMAP.
- The pig oracle + cow/sheep/chicken stay green (the wolf is a separate mob). WOLF_FOOD / WOLF_COLLAR_DYES /
  WOLF_FOOD tags — confirm presence in data/tag at phase open.

## ============================================================================
## EXEC-TIME DECOMPILES (verbatim, captured 2026-06-30 at phase open during 35-close)
## ============================================================================

## --- TamableAnimal (the tame/owner/sit state) ---
## DATA_FLAGS_ID = Byte accessor (bits: 0x1 = sitting [in-sitting-pose], 0x4 = tame). 0x2 is unused here.
## DATA_OWNERUUID_ID = Optional<EntityReference<LivingEntity>> (the owner ref). orderedToSit = a plain bool field.
## setTame(isTame, includeSideEffects): DATA_FLAGS = isTame ? (cur | 4) : (cur & 0xFB);
##   if (includeSideEffects) applyTamingSideEffects();   // Wolf override = the MAX_HEALTH 8->40 + heal + ATTACK_DAMAGE
## setInSittingPose(b): DATA_FLAGS = b ? (cur | 1) : (cur & 0xFE).   isInSittingPose = (DATA_FLAGS & 1)!=0.
## isTame = (DATA_FLAGS & 4)!=0. isOrderedToSit/setOrderedToSit = the orderedToSit field. setOwner(LivingEntity)
##   sets DATA_OWNERUUID_ID. NO RNG. The DATA_FLAGS/owner accessor INDICES need a defineSynchedData chain count at
##   exec (like the Phase-33 dataBabyIndex=16 / Phase-34 dataWoolIndex=18 derivation) — TamableAnimal extends
##   Animal→AgeableMob; DATA_FLAGS_ID + DATA_OWNERUUID_ID are TamableAnimal's own accessors (after index 17 AGE_LOCKED).

## --- Wolf.mobInteract + tryToTame (the taming RNG — CONFIRMED) ---
## isFood = is(ItemTags.WOLF_FOOD).
## mobInteract: if (isTame()) { feed-heal / dye-collar / armor / armor-repair / sit-toggle (all owner-gated;
##   dye+armor+repair DEFERRED — no equipment/dye system) } else if (!clientSide && stack.is(BONE) && !isAngry()) {
##   stack.consume(1); tryToTame(player); return SUCCESS_SERVER; }.
## tryToTame(player): if (random.nextInt(3) == 0) { tame(player); navigation.stop(); setTarget(null);
##   setOrderedToSit(true); broadcastEntityEvent(this, (byte)7 /*hearts*/); } else broadcastEntityEvent(this, (byte)6 /*smoke*/);
##   ⇒ ONE nextInt(3) per BONE feed on an untamed non-angry wolf (mob stream — lockstep if dogfooded). Hearts(7)/
##   smoke(6) via the EntityEvent broadcast (the SAME mechanism the pig-breed hearts use). tame() = setTame(true,true)
##   + setOwner(player). Wire into handleInteract (the cow-milk/sheep-shear sibling), wolf-gated.

## --- SitWhenOrderedToGoal (NEW — flags {JUMP, MOVE}, NO RNG) ---
## canUse(): orderedToSit = isOrderedToSit(); if (!orderedToSit && !isTame()) return false; if (isInWater()) return false;
##   if (!onGround()) return false; owner = getOwner(); if (owner==null || owner.level!=mob.level) return true;
##   if (distanceToSqr(owner) < 144.0 && owner.getLastHurtByMob() != null) return false;   // don't sit if owner is fighting nearby
##   return orderedToSit;
## canContinueToUse(): isOrderedToSit(). start(): navigation.stop(); setInSittingPose(true). stop(): setInSittingPose(false).
## A sitting wolf parks (claims MOVE/JUMP). NO RNG.

## --- NeutralMob anger (the "neutral" behavior) + Wolf.startPersistentAngerTimer (RNG) ---
## Wolf: PERSISTENT_ANGER_TIME = TimeUtil.rangeOfSeconds(20, 39) = UniformInt(400, 780) ticks (20*20 .. 39*20).
## startPersistentAngerTimer(): setTimeToRemainAngry(PERSISTENT_ANGER_TIME.sample(this.random));
##   ⇒ ONE UniformInt(400,780).sample(random) per anger trigger — RNG (mob stream, lockstep). UniformInt.sample =
##   minInclusive + nextInt(maxInclusive - minInclusive + 1) = 400 + nextInt(381). Port that exact draw.
## isAngryAt(entity, level): canAttack(entity) && ((isValidPlayerTarget(entity) && isAngryAtAllPlayers) ||
##   persistentAngerTarget matches entity). Gates the targetSelector NearestAttackableTargetGoal<Player>(isAngryAt).
## A wolf hit by a player (HurtByTargetGoal reads lastHurtByMob — Phase 31/35) → startPersistentAngerTimer →
##   angry for the random timer → attacks. ResetUniversalAngerTargetGoal decrements + clears the timer.

## --- OwnerHurtByTargetGoal.canUse (NEW — owner-defense, NO RNG) ---
## if (!isTame() || isOrderedToSit()) return false; owner = getOwner(); if (owner==null) return false;
## ownerLastHurtBy = owner.getLastHurtByMob(); ts = owner.getLastHurtByMobTimestamp();
## return ts != this.timestamp && canAttack(ownerLastHurtBy, DEFAULT) && wantsToAttack(ownerLastHurtBy, owner);
##   ⇒ the wolf targets whoever hit its owner. OwnerHurtTargetGoal is the mirror (targets whoever the OWNER attacked —
##   owner.getLastHurtMob). Both read the owner's hurt bookkeeping (the same lastHurtByMob fields, owner-side). NO RNG.
##   These need an OWNER reference + the owner's lastHurtByMob/lastHurtMob — extend the Phase-35 bookkeeping owner-side.

## --- Wolf.createAttributes (re-confirmed) + setTame side-effects ---
## Animal base + MOVEMENT_SPEED 0.3 + MAX_HEALTH 8.0 + ATTACK_DAMAGE 4.0. applyTamingSideEffects (Wolf override):
##   the tamed-wolf MAX_HEALTH 40 + full heal — decompile Wolf.applyTamingSideEffects at exec for the exact bump.

## --- Wolf.applyTamingSideEffects (the tamed-HP bump — CONFIRMED) ---
## if (isTame()) { getAttribute(MAX_HEALTH).setBaseValue(40.0); setHealth(40.0); } else { setBaseValue(8.0); }
## ⇒ tame → MAX_HEALTH 8→40 + full heal to 40; untame → 40→8. Called by setTame(_, includeSideEffects=true).

## --- FollowOwnerGoal (NEW — NO RNG) ---
## canUse(): owner = getOwner(); if (owner==null) return false; if (unableToMoveToOwner()) return false;
##   if (distanceToSqr(owner) < startDistance²) return false;  this.owner = owner; return true;   // start = 10² = 100
## canContinueToUse(): !unableToMoveToOwner() && distanceToSqr(owner) > stopDistance² && !isOrderedToSit().  // stop = 2² = 4
## tick(): if (!shouldTryTeleportToOwner()) lookControl.setLookAt(owner, 10, maxHeadXRot);
##   if (--timeToRecalcPath > 0) return; timeToRecalcPath = adjustedTickDelay(10) /*=10 identity*/;
##   if (ownerFarAway) tryToTeleportToOwner(); else navigation.moveTo(owner, speedModifier);
## NO RNG. The teleport (tryToTeleportToOwner — a safe-pos scan around the owner) can DEFER for v1 (the moveTo
##   path-follow is the core observable; cite the teleport deferral). adjustedTickDelay(10)=10 (identity, Phase-34).

## --- Wolf wire type + WOLF_FOOD tag (CONFIRMED present) ---
## entity.Wolf.ID = 149 (data/entity/entity.go:1362). WOLF_FOOD tag = data/tag/tags.go:375 (18 meat item ids).
## categoryOf wolf → CREATURE (the Phase-34/35 categoryOf pattern; confirm data/entity Wolf.Type == "creature" at exec).

## --- OwnerHurtTargetGoal.canUse (the mirror of OwnerHurtBy — NO RNG) ---
## if (!isTame() || isOrderedToSit()) return false; owner = getOwner(); if (owner==null) return false;
## ownerLastHurt = owner.getLastHurtMob(); ts = owner.getLastHurtMobTimestamp();   // what the OWNER ATTACKED (vs hurt-by)
## return ts != this.timestamp && canAttack(ownerLastHurt, DEFAULT) && wantsToAttack(ownerLastHurt, owner);
## ⇒ needs the owner's lastHurtMob (attack-side) bookkeeping — the player-side mirror of lastHurtByMob. NO RNG.

## --- FollowOwnerGoal start/canContinue/stop (NO RNG) — completes the tick already captured ---
## start(): timeToRecalcPath = 0; oldWaterCost = getPathfindingMalus(WATER); setPathfindingMalus(WATER, 0.0);  // follow thru water
## canContinueToUse(): if (navigation.isDone()) return false; if (unableToMoveToOwner()) return false;
##   return !(distanceToSqr(owner) <= stopDistance²);   // stopDistance 2 → 4
## stop(): owner = null; navigation.stop(); setPathfindingMalus(WATER, oldWaterCost);
## (tick already in JARNOTES: lookAt owner + adjustedTickDelay(10) recalc + teleport-if-far / moveTo. teleport DEFERS.)

## --- TamableAnimalPanicGoal extends PanicGoal — REUSE the Phase-31 PanicGoal ---
## A thin PanicGoal subclass (ctor passes speedModifier + the panic damage-type tag); the tick() override is minor
## (it nudges movement toward water/away). v1: reuse the existing PanicGoal (kind/.star) at speed 1.5 — the tamed
## override is a cite-deferrable refinement. NO new RNG beyond PanicGoal's own.

## --- ⚠ v1 SIMPLIFICATION: ResetUniversalAngerTargetGoal is a NO-OP in v1 (CONFIRMED) ---
## ResetUniversalAngerTargetGoal.canUse(): UNIVERSAL_ANGER gamerule != false && wasHurtByPlayer().
## The UNIVERSAL_ANGER gamerule DEFAULTS FALSE in vanilla → this goal NEVER fires under default gamerules.
## ⇒ v1 can DECLARE it (faithful goal-set completeness) as a canUse=false stub (cite the gamerule default), OR
## OMIT it with a documented deferral. The PER-MOB persistent-anger timer (the wolf's own timeToRemainAngry,
## decremented in NeutralMob.updatePersistentAnger each tick) is what actually expires the anger — that IS in
## scope (the UniformInt(400,780) timer counts down; isAngryAt returns false when it hits 0). ResetUniversal is
## ONLY the all-players gamerule path, dead in v1.

## --- ✅ DATA_FLAGS / DATA_OWNERUUID accessor INDICES (CONFIRMED via javap — no longer open) ---
## Chain (continuing the Phase-33 dataBabyIndex=16 / Phase-34 dataWoolIndex=18-for-Sheep derivations):
##   Entity 0-7, LivingEntity 8-14, Mob 15, AgeableMob 16=DATA_BABY_ID + 17=AGE_LOCKED, Animal adds NONE,
##   TamableAnimal.defineSynchedData (VERIFIED): super; define(DATA_FLAGS_ID, (byte)0); define(DATA_OWNERUUID_ID, empty).
##   ⇒ dataWolfFlagsIndex = 18 (BYTE serializer id 0); DATA_OWNERUUID_ID = 19 (Optional<EntityReference> — a
##   complex serializer, server-side; its WIRE broadcast DEFERS, the client uses it for owner-glow/collar = deferred).
##   (Note: Sheep's DATA_WOOL is ALSO index 18 — that's fine, indices are PER-CLASS-HIERARCHY; a wolf is not a sheep.)
## Wolf adds MORE after (DATA_INTERESTED 20, COLLAR 21, ANGER_END_TIME 22, VARIANT 23, SOUND_VARIANT 24) — ALL DEFERRED
##   (collar/variant/the begging-interested flag). v1 MUST-HAVE metadata = DATA_FLAGS index 18 (the sit 0x1 / tame 0x4
##   bits the client renders: sitting pose + tamed collar-render). Build a wolfFlagsDataEntry(flagsByte) mirroring
##   babyDataEntry(16)/woolDataEntry(18): Byte(18) + VarInt(0=BYTE) + Byte(flags). Broadcast on tame/sit change.

## --- ⚠ PLAN-CHECK BLOCKER FIX (B1+B2): the target goal needs a CLASS param + an anger gate ---
## The Phase-35 nearestAttackableTargetGoal (ai_goals_target.go) is PLAYER-ONLY (findTarget hardcoded to
## nearestPlayerIDAt) + NO anger gate + a no-param ctor. The wolf needs TWO things it can't do:
##   (a) @4 NearestAttackableTargetGoal<Player>(isAngryAt) — target the player ONLY when angry (a wild un-hit
##       wolf must NOT aggro players; vanilla gates on isAngryAt). Currently it aggros any player on sight.
##   (b) @7 NearestAttackableTargetGoal<AbstractSkeleton> — target skeletons (skeleton exists from Phase 35).
##       Currently a 2nd bare nearest_attackable_target resolves to the SAME player goal → never targets a skeleton.
## FIX (lands in 36-01, wave-1 solo — preserves disjointness): parameterize the target goal.
##   - Give nearestAttackableTargetGoal a targetClass field (PLAYER | SKELETON) + an optional angerGate func(e)bool.
##   - findTarget switches on targetClass: PLAYER → nearestPlayerIDAt (existing); SKELETON → nearest-entity scan
##     for entity.Skeleton.ID within FOLLOW_RANGE (a new nearestEntityOfTypeAt, the mob-vs-mob analogue — javap
##     NearestAttackableTargetGoal.findTarget's getNearestEntity(getEntitiesOfClass(type,...)) branch).
##   - canUse: after the nextInt(10) gate + findTarget, if angerGate != nil && !angerGate(e) → no target (the
##     isAngryAt gate). The HOSTILES keep angerGate=nil (they always aggro) — so Phase-35 behavior UNCHANGED.
##   - Add NEW kinds to buildNativeGoal: "angry_player_target" (PLAYER + the wolf isAngryAt gate) +
##     "skeleton_target" (SKELETON, no anger gate). OR a parameterized kind with a class/gate arg. The wolf .star
##     declares kind="angry_player_target" @4 + kind="skeleton_target" @7; the hostiles keep "nearest_attackable_target".
##   - Update the buildNativeGoal panic valid-kinds list + the flags() assertion (both still flagTarget).
##   - isAngryAt(e): true iff the wolf's anger timer is live (see below) AND the target is the anger target /
##     a valid player. Port NeutralMob.isAngryAt (canAttack && (angryAtAllPlayers || persistentAngerTarget matches)).

## --- ✅ W6 DISSOLVED: the anger timer is a GAMETIME-ENDPOINT, NO per-tick decrement needed ---
## NeutralMob.isAngry() (CONFIRMED): endTime = getPersistentAngerEndTime(); return endTime > 0 && (endTime - gameTime) > 0.
## So setTimeToRemainAngry(N) = set angerEndTime = gameTime + N (the DATA_ANGER_END_TIME accessor, index 22 — but
## v1 can hold it as a plain server-side int64 field, NO wire broadcast needed). isAngry just compares to gameTime —
## the anger EXPIRES automatically when gameTime passes angerEndTime. NO decrement task, NO ResetUniversalAnger
## (dead under default gamerule). On a player hit: angerEndTime = gameTime + (400 + mobRandom(e).nextInt(381)) +
## set the anger target = the attacker. isAngryAt reads this. This is the MeleeAttackGoal-cooldown gametime pattern.
## ⇒ The anger model = ONE int64 field (angerEndTime) + the attacker ref, set at the combat store-point, read by
## the angry_player_target goal's gate. Clean, no counter, no ResetUniversalAnger goal.

## --- STILL OPEN (truly deferred — no decompile needed) ---
## DEFERRED: BegGoal, WolfAvoidEntityGoal, NonTameRandomTargetGoal prey, collar/armor/dye, WolfVariant, PREY_SELECTOR,
## the FollowOwner teleport (moveTo path-follow is the core), the all-players universal-anger path, the owner-UUID
## wire broadcast (server-side owner ref is enough for the goals; the client owner-glow defers).
