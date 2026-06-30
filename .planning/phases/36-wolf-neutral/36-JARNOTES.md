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

## --- OPEN exec-time decompiles (do at 36 open) ---
TamableAnimal (tame/owner/sit DATA flags + defineSynchedData indices), SitWhenOrderedToGoal, FollowOwnerGoal,
BegGoal, OwnerHurtByTargetGoal, OwnerHurtTargetGoal, NonTameRandomTargetGoal, WolfAvoidEntityGoal,
ResetUniversalAngerTargetGoal, NeutralMob (startPersistentAngerTimer + the UniformInt anger range),
TamableAnimalPanicGoal, Wolf.setTame (the MAX_HEALTH 8→40 bump + ATTACK_DAMAGE), Wolf collar/armor, PREY_SELECTOR.
