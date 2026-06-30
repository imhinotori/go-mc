# Phase 33 — pre-decompiled jar notes (scratch for the eventual CONTEXT)

Captured 2026-06-30 while Phase 32 was in flight, so the dependency chain is jar-verified before the
phase opens. This is the HARD GATE (pig parity dogfood) — S3 aging + breeding + the pig's last 2 goals
(BreedGoal@3, FollowParentGoal@5), closing the full 8-goal oracle.

## BreedGoal (net.minecraft.world.entity.ai.goal.BreedGoal), flags {MOVE, LOOK}, priority 3, speed 1.0
```java
PARTNER_TARGETING = forNonCombat().range(8.0).ignoreLineOfSight();
canUse():            if (!animal.isInLove()) return false; partner = getFreePartner(); return partner != null;
canContinueToUse():  partner.isAlive() && partner.isInLove() && loveTime < 60 && !partner.isPanicking();
stop():              partner=null; loveTime=0;
tick():              lookAt(partner); navigation.moveTo(partner, speed); ++loveTime;
                     if (loveTime >= adjustedTickDelay(60) && distanceToSqr(partner) < 9.0) breed();
getFreePartner():    nearby partnerClass (same class) in inflate(8.0) box, PARTNER_TARGETING; pick nearest
                     where animal.canMate(other) && !other.isPanicking().
breed():             spawnChildFromBreeding(level, partner) — sets both ages, resets inLove, XP orb, child.
```

## Animal (net.minecraft.world.entity.animal.Animal) — the in_love / breeding state
```java
private int inLove = 0;                       // love-mode countdown
isInLove()       = inLove > 0;
canFallInLove()  = inLove <= 0;
setInLove(player)= inLove = 600 (30s); (records the player for the XP/breeder)
aiStep tail:     if (getAge() != 0) inLove = 0;          // only an ADULT (age 0) can be in love
                 if (inLove > 0) { --inLove; if (inLove % 10 == 0) spawn heart particles }
mobInteract:     if (isFood(itemStack)) { age=getAge(); if (age==0 && canFallInLove()) { usePlayerItem; setInLove; } ... baby age-up on feed ... }
isFood(stack)    = ABSTRACT — Pig overrides: stack.is(ItemTags.PIG_FOOD).   (the Phase-32 read!)
canMate(other)   = other != this && other.getClass()==this.getClass() (same species) && isInLove() && other.isInLove().
NBT:             "InLove" int persisted.
```

## AgeableMob (net.minecraft.world.entity.AgeableMob) — aging
```java
BABY_START_AGE = -24000;   forcedAge=0; forcedAgeTimer=0;
DATA_BABY_ID (synched bool — the wire baby flag).
getAge()/setAge(int): age<0 = baby (ticks up toward 0); age>0 = breeding cooldown (ticks down toward 0); age==0 = adult ready.
ageUp(amount)/ tick: each tick setAge(age + (age<0?+1: age>0?-1:0)); a baby grows, an adult's cooldown decays.
isBaby() = getAge() < 0.
finalizeSpawnChildFromBreeding: child setAge(BABY_START_AGE); parents setAge(6000) (5min cooldown); reset inLove.
```

## FollowParentGoal (net.minecraft.world.entity.ai.goal.FollowParentGoal), priority 5, speed 1.1
NOTE: ctor does NOT setFlags → flags = EMPTY set (no MOVE/LOOK lock). Verify the v1 selector handles an
empty-flag goal (it claims no flag, so it never blocks/locks — it just navigates in tick). Decompiled:
```java
HORIZONTAL_SCAN_RANGE=8; VERTICAL_SCAN_RANGE=4; DONT_FOLLOW_IF_CLOSER_THAN=3;
canUse():
    if (animal.getAge() >= 0) return false;                      // only a BABY (age<0) follows
    parents = level.getEntitiesOfClass(animal.getClass(), boundingBox.inflate(8,4,8));
    closest=null; closestDistSqr=MAX;
    for (p : parents) { if (p.getAge() < 0 || distSqr(p) > closestDistSqr) continue;  // skip babies → keep ADULTS (age>=0)
                        closestDistSqr=distSqr(p); closest=p; }
    if (closest==null) return false;
    if (closestDistSqr < 9.0) return false;                      // already close enough (DONT_FOLLOW_IF_CLOSER_THAN 3 → 3²=9)
    parent=closest; return true;
canContinueToUse():
    if (animal.getAge() >= 0) return false;                      // grew up → stop
    if (!parent.isAlive()) return false;
    distSqr = distSqr(parent); return !(distSqr<9.0) && !(distSqr>256.0);   // follow while 3..16 blocks
start():  timeToRecalcPath = 0;
stop():   parent = null;
tick():   if (--timeToRecalcPath > 0) return;
          timeToRecalcPath = adjustedTickDelay(10);             // PURE INT — NO RNG (oracle-safe)
          navigation.moveTo(parent, speed);                     // = path_to(parent pos, 1.1)
```
RNG: NONE in FollowParentGoal (the entity scan + the int recalc timer are deterministic). Oracle-safe; it
only fires for a BABY with a nearby adult — the oracle pig is a lone adult → canUse false → zero effect.

## Pig.registerGoals (already confirmed, Phase 31): BreedGoal@3 (1.0), FollowParentGoal@5 (1.1).
## Pig.isFood = stack.is(ItemTags.PIG_FOOD) — reuses the Phase-32 itemInTag(pig_food) read.

## THE DOGFOOD GATE (the hard part):
After this phase the pig has all 8 goals {0,1,3,4,4,5,6,7,8} — the FULL oracle. TestPluginPigEqualsGoNativePig
must drive a pig with EVERY goal and stay byte-identical. Breeding draws RNG (child spawn, XP) + aging is
stateful → the oracle must either (a) keep the oracle world free of partners/food/babies so breed/follow
never fire (zero draws — like float/panic/tempt stay dormant), OR (b) drive a full breeding scenario on BOTH
halves identically. Prefer (a) for the oracle + SEPARATE scenario tests for breed/age/follow behavior.
RNG-lockstep rule applies to every new draw (aging tick? breed child? follow re-scan?) — add to BOTH pigs.

## Subsystem work (the S3 build, beyond the 2 goals):
- Animal: inLove int + isInLove/canFallInLove/setInLove + the aiStep decrement (adult-only) + heart particles.
- AgeableMob: age int + isBaby + the per-tick ageUp + DATA_BABY_ID wire sync (the baby renders small).
- isFood (pig) = itemInTag(pig_food) — Phase 32.
- mobInteract feed path: feed pig_food to an adult → setInLove; feed to a baby → age-up. (Wire the use-on-entity / interact path.)
- breed(): spawnChild via spawnDeclaredMob (baby age), parent cooldown, XP orb, heart particles.
- canMate; getFreePartner nearby-same-class scan.
- NBT persist InLove + Age.
This is a LARGE phase — consider whether the planner splits it (S3 subsystem plan + the 2-goals plan + the gate plan) or one big sequential plan. Let the planner decide via the source-audit.

## Animal.mobInteract (THE FEED PATH — verbatim CFR)
```java
public InteractionResult mobInteract(Player player, InteractionHand hand) {
    ItemStack itemStack = player.getItemInHand(hand);
    if (this.isFood(itemStack)) {                                  // Pig.isFood = is(PIG_FOOD)
        int age = this.getAge();
        if (player instanceof ServerPlayer && age == 0 && this.canFallInLove()) {  // ADULT + not already in love
            this.usePlayerItem(player, hand, itemStack);           // consume 1
            this.setInLove(serverPlayer);                          // inLove = 600
            this.playEatingSound();
            return SUCCESS_SERVER;
        }
        if (this.canAgeUp()) {                                     // BABY (age<0) — canAgeUp = age<0 && forcedAgeTimer<=0
            this.usePlayerItem(player, hand, itemStack);
            this.ageUp(getSpeedUpSecondsWhenFeeding(-age), true);  // grow toward adult faster
            this.playEatingSound();
            return SUCCESS;
        }
    }
    return super.mobInteract(player, hand);
}
// getSpeedUpSecondsWhenFeeding(ageDelta) = (int)(ageDelta / 20 * 0.1f) — the baby-grow speedup on feed.
// usePlayerItem: if (!player.hasInfiniteMaterials()) itemStack.shrink(1).   (survival consumes 1)
```
Wire this into handleInteract (attack_dispatch.go:725, currently a v1 no-op): read held item, isFood?
adult+canFallInLove → setInLove+consume; baby+canAgeUp → ageUp+consume. playEatingSound = a sound emit (reuse the Phase-29 ClientboundSoundEntity seam).

## DATA_BABY_ID accessor index (computed — EXECUTOR MUST javap-confirm before wiring)
SynchedEntityData accessor chain (defineId order, the wire index):
  Entity:        indices 0-7  (8 accessors: 0=BYTE shared-flags, 1=DATA_AIR_SUPPLY_ID INT, 2=custom-name,
                              3=name-visible, 4=silent, 5=no-gravity, 6=POSE, 7=DATA_TICKS_FROZEN) — per entity_encode.go's documented map
  LivingEntity:  indices 8-14 (7: DATA_LIVING_ENTITY_FLAGS, DATA_EFFECT_PARTICLES, DATA_EFFECT_AMBIENCE_ID,
                              DATA_ARROW_COUNT_ID, DATA_STINGER_COUNT_ID, DATA_HEALTH_ID, SLEEPING_POS_ID)
  Mob:           index 15     (DATA_MOB_FLAGS_ID)
  AgeableMob:    index 16 = DATA_BABY_ID (BOOLEAN), index 17 = AGE_LOCKED (BOOLEAN)
=> DATA_BABY_ID = accessor index 16, serializer = BOOLEAN. The baby flag is a single Byte(16) +
   VarInt(BOOLEAN_serializer_id) + Boolean(isBaby) entry (mirror the DATA_AIR INT entry in entity_encode.go,
   swapping INT→BOOLEAN). VERIFY the BOOLEAN serializer id from the generated registry (EntityDataSerializers
   order) — it is a small VarInt; the executor confirms it the same way itemStackSerializerID/INT were pinned.

## Heart-particle (the flagged gap): breed() + the inLove%10 emit spawn ParticleTypes.HEART via
## ClientboundLevelParticlesPacket. NO particle encoder exists yet — Plan B must add a minimal
## ClientboundLevelParticles encode (particle id + long-distance bool + x,y,z + offset xyz + maxSpeed +
## count). Cite the packet; the HEART particle id comes from the generated particle registry.
