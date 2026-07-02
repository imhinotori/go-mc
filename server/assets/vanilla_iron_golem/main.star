# vanilla_iron_golem — a 1:1 vanilla Iron-Golem dogfood, the village defender built as a Starlark plugin.
# A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR + javap
# -c -p this session). It declares net.minecraft.world.entity.animal.golem.IronGolem.registerGoals, reusing
# the proven Go goal runtime: the COMBAT/target goals are Go-native (kind=), and the passive stroll/look/
# around are the SAME mob-agnostic .star callbacks the zombie/ravager run (COPIED VERBATIM apart from the
# golem's own stroll interval). The IronGolem does NOT declare a FloatGoal (golems sink; verified — no
# FloatGoal in registerGoals), so this plugin has NO float goal.
#
# IronGolem.registerGoals() (VERIFIED CFR net.minecraft.world.entity.animal.golem.IronGolem):
#   goalSelector:
#     @1 MeleeAttackGoal(this, 1.0, true)                       <-- kind="melee_attack" (the CORE melee; the
#                                                                    golem's own doHurtTarget override — the
#                                                                    7.5+nextInt(15) damage + the 0.4 vertical
#                                                                    fling — is the Go-native ironGolemDidHurt
#                                                                    hook, ai_goals_iron_golem.go)
#     @2 MoveTowardsTargetGoal(this, 0.9, 32.0f)                <-- DEFERRED (nav-toward-target; DefaultRandomPos
#                                                                    .getPosTowards RNG helper absent — the
#                                                                    melee_attack goal already navigates to the
#                                                                    target, so this is combat-redundant; cited)
#     @2 MoveBackToVillageGoal(this, 0.6, false)                <-- DEFERRED (POI: findSectionClosestToVillage
#                                                                    section-toward-village nav; cited)
#     @4 GolemRandomStrollInVillageGoal(this, 0.6)              <-- .star (golem_stroll): the getPosition RNG
#                                                                    draw-order is ported FAITHFULLY (nextFloat
#                                                                    0.3 gate, then on miss the 0.7 gate); the
#                                                                    villager and POI legs are cited-empty (no
#                                                                    villagers / no POI-toward nav), so every
#                                                                    path falls through to getPositionTowards
#                                                                    Anywhere == LandRandomPos.getPos(mob,10,7)
#     @5 OfferFlowerGoal(this)                                  <-- DEFERRED (villager: the CANDIDATE_FOR_IRON_
#                                                                    GOLEM_GIFT target is a baby villager; cited)
#     @7 LookAtPlayerGoal(this, Player, 6.0f)                   <-- .star (look, dist 6)
#     @8 RandomLookAroundGoal(this)                             <-- .star (around)
#   targetSelector:
#     @1 DefendVillageTargetGoal(this)                          <-- DEFERRED (villager: defends a hurt villager;
#                                                                    no villager behavior merged; cited)
#     @2 HurtByTargetGoal(this)                                 <-- kind="hurt_by_target" (retaliate on hit)
#     @3 NearestAttackableTargetGoal<Player>(this, 10, true, false, this::isAngryAt)
#                                                               <-- kind="angry_player_target" (the anger-gated
#                                                                    PLAYER goal: an UN-provoked golem does NOT
#                                                                    aggro players; a golem hit by a player becomes
#                                                                    angry — combat_mob.go's NeutralMob anger
#                                                                    trigger, IronGolem PERSISTENT_ANGER_TIME =
#                                                                    rangeOfSeconds(20,39) == UniformInt(400,780))
#     @3 NearestAttackableTargetGoal<Mob>(this, 5, false, false, target -> target instanceof Enemy && !Creeper)
#                                                               <-- kind="iron_golem_hostile_target" (the golem
#                                                                    hunts hostile mobs — zombie/skeleton/spider/
#                                                                    husk/silverfish/witch/... but NOT a creeper)
#     @4 ResetUniversalAngerTargetGoal<IronGolem>(this, false)  <-- DEFERRED (gametime-endpoint anger auto-expires
#                                                                    — the same W6-DISSOLVED model the wolf uses;
#                                                                    no per-tick reset goal needed; cited)
#
# createAttributes (VERIFIED CFR IronGolem.createAttributes): Mob.createMobAttributes() (NOT Monster/Animal —
# no ATTACK_DAMAGE base-2/TEMPT_RANGE) .add(MAX_HEALTH 100).add(MOVEMENT_SPEED 0.25).add(KNOCKBACK_RESISTANCE
# 1.0).add(ATTACK_DAMAGE 15.0).add(STEP_HEIGHT 1.0). No ATTACK_KNOCKBACK override (stays createLivingAttributes
# default 0.0). See ironGolemSupplier (level/attribute/defaults.go).
#
# doHurtTarget (VERIFIED CFR IronGolem.doHurtTarget) — the Go-native override (ai_goals_iron_golem.go):
#   attackAnimationTick = 10; broadcastEntityEvent(this, (byte)4);
#   float ad = getAttackDamage() == getAttributeValue(ATTACK_DAMAGE) == 15.0;
#   float damage = (int)ad > 0 ? ad/2.0f + nextInt((int)ad) : ad;   // 15 -> 7.5 + nextInt(15) == [7.5, 21.5]
#   hurt = target.hurtServer(mobAttack(this), damage);              // ONE nextInt(15) draw on the golem stream
#   if (hurt) { scale = max(0, 1 - target KNOCKBACK_RESISTANCE); target.deltaMovement += (0, 0.4f*scale, 0); }
#   playSound(IRON_GOLEM_ATTACK) // client sound, server no-op
# The +0.4 vertical fling is IN ADDITION to hurtServer's own dealDefaultKnockback (the horizontal pop) — the
# golem's signature "pop into the air". isPlayerCreated (DATA_FLAGS_ID bit 0x01) gates canAttack: a player-
# created golem never targets players; the crackiness (Crackiness.GOLEM 0.75/0.5/0.25 byFraction health/max)
# is a CLIENT-derived texture overlay (the client computes it from the synced health) — the only server
# effect is the IRON_GOLEM_DAMAGE sound on a crack-level change, a cited client-side no-op.
#
# DEFERRED (cite-recorded, NEVER silently dropped): the 5 village/villager goals above (MoveTowardsTarget,
# MoveBackToVillage, OfferFlower, DefendVillage, ResetUniversalAnger) — each needs a not-yet-built villager/
# POI-nav piece; the iron-ingot heal (mobInteract, 25.0f) and the poppy offer are villager/interaction bits;
# the crack-change sound is a client-side no-op. The CORE hunt (angry-player + hostile-mob + hurt-by) + melee
# + the doHurtTarget damage/fling + isPlayerCreated (the phase goal) is fully wired.

LOOK_DIST = 6.0   # IronGolem LookAtPlayerGoal(Player, 6.0f) lookDistance

# math is a host-predeclared global (starlark math.Module) — used by RandomLookAroundGoal.start cos/sin.
# --- constants (jar-confirmed) -----------------------------------------------------------------
# GolemRandomStrollInVillageGoal extends RandomStrollGoal(mob, speed, 240, false): the stroll interval is
# 240 (NOT the 120 default). reducedTickDelay(240) == positiveCeilDiv(240,2) == 120 in the jar; our full-
# rate driver runs the FULL 240 (the same identity rule the other strolls follow — the raw interval, not the
# jar's every-other-tick-halved value). Cite GolemRandomStrollInVillageGoal ctor.
STROLL_INTERVAL = 240            # RandomStrollGoal interval arg (GolemRandomStrollInVillageGoal(mob, 0.6, 240, false))
STROLL_H = 10                    # LandRandomPos.getPos horizontal radius (getPos(mob,10,7))
STROLL_V = 7                     # vertical radius
GOLEM_STROLL_ANYWHERE_P = 0.3    # getPosition: random.nextFloat() < 0.3f -> getPositionTowardsAnywhere
GOLEM_STROLL_VILLAGER_P = 0.7    # getPosition: else random.nextFloat() < 0.7f -> villager-first, else POI-first
LOOK_PROBABILITY = 0.02          # LookAtPlayerGoal.DEFAULT_PROBABILITY (nextFloat() < 0.02f)
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793 # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# GolemRandomStrollInVillageGoal(mob, 0.6)   flags {MOVE}   extends RandomStrollGoal(mob, 0.6, 240, false)
# RandomStrollGoal.canUse: nextInt(reducedTickDelay(interval)) gate (interval 240), then getPosition() (the
# golem override). Cite GolemRandomStrollInVillageGoal.getPosition + RandomStrollGoal.canUse.
#
# getPosition() draw-order (VERIFIED CFR GolemRandomStrollInVillageGoal.getPosition):
#   if (nextFloat() < 0.3f) return getPositionTowardsAnywhere();          // DRAW A
#   if (nextFloat() < 0.7f) { t = villager(); if t==null t = poi(); }     // DRAW B (villager-first)
#   else { t = poi(); if t==null t = villager(); }                        // (POI-first)
#   return t == null ? getPositionTowardsAnywhere() : t;
# The villager leg (getPositionTowardsVillagerWhoWantsGolem) and the POI leg (getPositionTowardsPoi) are
# CITE-DEFERRED (no villagers / no POI-toward nav), so both resolve to null and every branch falls through to
# getPositionTowardsAnywhere == LandRandomPos.getPos(mob, 10, 7). The nextFloat DRAW ORDER is preserved
# FAITHFULLY: DRAW A always; DRAW B is drawn ONLY when DRAW A >= 0.3f (exactly the jar's conditional).
# getPositionTowardsAnywhere is a plain LandRandomPos.getPos (NO WaterAvoidingRandomStrollGoal probability
# draw — the golem stroll is not water-avoiding). Cite GolemRandomStrollInVillageGoal.getPositionTowardsAnywhere.
# ============================================================================================
def golem_stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_INTERVAL) != 0:   # DRAW 0: RandomStrollGoal.canUse nextInt(interval) gate
        return False
    # getPosition() DRAW A: nextFloat() < 0.3f. On the <0.3 branch the jar returns getPositionTowardsAnywhere
    # immediately (no second draw); on the >=0.3 branch it draws DRAW B (the villager/POI selector). With the
    # villager/POI legs cited-empty both branches ultimately yield anywhere, but we honor the exact draw count.
    if entity.rand_float() >= GOLEM_STROLL_ANYWHERE_P:
        # DRAW B: nextFloat() < 0.7f — the villager-first vs POI-first selector. Its outcome is not consumed
        # (both legs are cited-empty -> null -> anywhere), but the DRAW is made exactly as the jar makes it.
        _ = entity.rand_float()
    # getPositionTowardsAnywhere == LandRandomPos.getPos(mob, 10, 7): the same 10-candidate xt/yt/zt sweep the
    # other strolls run (y BEFORE z draw order), NO land-mode probability float (not water-avoiding).
    flat = []
    for _ in range(10):
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # x offset (DRAW order 1 of 3)
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V   # y offset (DRAW order 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # z offset (DRAW order 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(1.0)   # 31st float: wantLandMode == 1.0 (LandRandomPos.getPos is always land mode)
    nav.path_to(*flat)
    return True

def golem_stroll_continue(entity, world, nav):
    return nav.has_path()

def golem_stroll_stop(entity, world, nav):
    nav.stop()

# ============================================================================================
# LookAtPlayerGoal(mob, Player, 6.0)   flags {LOOK}   — COPIED VERBATIM from vanilla_zombie/vanilla_ravager.
# ============================================================================================
def look_can_use(entity, world, nav):
    if entity.rand_float() >= LOOK_PROBABILITY:   # DRAW: nextFloat() < probability
        return False
    p = world.nearest_player(entity.x, entity.y, entity.z, LOOK_DIST)
    if p == None:
        return False
    entity.set_state("look_x", p[0])
    entity.set_state("look_y", p[1])
    entity.set_state("look_z", p[2])
    return True

def look_start(entity, world, nav):
    entity.set_state("look_time", 40 + entity.rand_int(40))   # lookTime = adjustedTickDelay(40 + nextInt(40))

def look_continue(entity, world, nav):
    if entity.get_state("look_time") <= 0:
        return False
    dx = entity.get_state("look_x") - entity.x
    dy = entity.get_state("look_y") - entity.y
    dz = entity.get_state("look_z") - entity.z
    d2 = dx * dx + dy * dy + dz * dz
    return d2 <= LOOK_DIST * LOOK_DIST

def look_tick(entity, world, nav):
    entity.set_look_at(entity.get_state("look_x"), entity.get_state("look_y"), entity.get_state("look_z"))
    entity.set_state("look_time", entity.get_state("look_time") - 1)

def look_stop(entity, world, nav):
    entity.set_state("look_time", 0)

# ============================================================================================
# RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true — COPIED VERBATIM
# ============================================================================================
def around_can_use(entity, world, nav):
    return entity.rand_float() < LOOK_AROUND_PROBABILITY

def around_start(entity, world, nav):
    d = TWO_PI * entity.rand_double()           # nextDouble() heading (DRAW 1)
    entity.set_state("rel_x", math.cos(d))
    entity.set_state("rel_z", math.sin(d))
    entity.set_state("look_time", 20 + entity.rand_int(20))   # lookTime = 20 + nextInt(20) (DRAW 2)

def around_continue(entity, world, nav):
    return entity.get_state("look_time") >= 0

def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)

# --- the declaration ---------------------------------------------------------------------------
# base_type "iron_golem" -> renders as entity.IronGolem.ID (id 70). Real IronGolem attributes
# (Mob.createMobAttributes + MAX_HEALTH 100, MOVEMENT_SPEED 0.25, KNOCKBACK_RESISTANCE 1.0, ATTACK_DAMAGE
# 15, STEP_HEIGHT 1.0 — NO ATTACK_KNOCKBACK override). Melee + targets are Go-native (kind=); the
# doHurtTarget damage/fling is ironGolemDidHurt (a per-type hook in checkAndPerformAttack); stroll/look/
# around are .star callbacks. Cite IronGolem.registerGoals + IronGolem.createAttributes.
declare_mob(
    name = "vanilla_iron_golem",
    base_type = "iron_golem",
    attributes = {
        "max_health": 100.0,           # MAX_HEALTH 100.0
        "movement_speed": 0.25,        # MOVEMENT_SPEED 0.25
        "knockback_resistance": 1.0,   # KNOCKBACK_RESISTANCE 1.0 (a golem is not knocked back)
        "attack_damage": 15.0,         # ATTACK_DAMAGE 15.0 (the doHurtTarget base: 15/2 + nextInt(15))
        "step_height": 1.0,            # STEP_HEIGHT 1.0
    },
    goals = [
        # @1 MeleeAttackGoal(this, 1.0, true) [MOVE] — kind. The golem-specific doHurtTarget (damage + fling)
        # is the ironGolemDidHurt hook fired inside checkAndPerformAttack (ai_goals_attack.go, golem-gated).
        # Cite IronGolem.registerGoals @1.
        goal(priority = 1, flags = ["MOVE"], kind = "melee_attack"),
        # @4 GolemRandomStrollInVillageGoal(this, 0.6) [MOVE] — .star (golem_stroll). can_use commits the
        # candidates (path_to), so NO start kwarg. Cite IronGolem.registerGoals @4.
        goal(priority = 4, flags = ["MOVE"], can_use = golem_stroll_can_use, stop = golem_stroll_stop, can_continue = golem_stroll_continue),
        # @7 LookAtPlayerGoal(Player, 6.0) [LOOK] — .star. Cite IronGolem.registerGoals @7.
        goal(priority = 7, flags = ["LOOK"], can_use = look_can_use, start = look_start, tick = look_tick, stop = look_stop, can_continue = look_continue),
        # @8 RandomLookAroundGoal [MOVE, LOOK] — .star. Cite IronGolem.registerGoals @8.
        goal(priority = 8, flags = ["MOVE", "LOOK"], can_use = around_can_use, start = around_start, tick = around_tick, can_continue = around_continue, requires_update_every_tick = True),
        # targetSelector @2 HurtByTargetGoal(this) [TARGET] — kind. Cite IronGolem.registerGoals targetSelector @2.
        goal(priority = 2, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @3 NearestAttackableTargetGoal<Player>(this, 10, true, false, this::isAngryAt) [TARGET]
        # — kind="angry_player_target" (the anger-gated player goal: an un-provoked golem does NOT aggro
        # players). Cite IronGolem.registerGoals targetSelector @3 (Player, isAngryAt).
        goal(priority = 3, flags = ["TARGET"], kind = "angry_player_target"),
        # targetSelector @3 NearestAttackableTargetGoal<Mob>(this, 5, false, false, Enemy && !Creeper) [TARGET]
        # — kind="iron_golem_hostile_target" (the golem hunts hostile mobs, never a creeper). Cite
        # IronGolem.registerGoals targetSelector @3 (Mob, Enemy && !Creeper).
        goal(priority = 3, flags = ["TARGET"], kind = "iron_golem_hostile_target"),
    ],
)
