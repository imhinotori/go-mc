# vanilla_villager — a 1:1 vanilla Villager dogfood, the village NPC built as a Starlark plugin. A LITERAL
# port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR + javap this session). Unlike the
# classic-goal mobs, the Villager is a BRAIN mob: net.minecraft.world.entity.npc.villager.Villager has NO
# registerGoals override — its AI is the ported Brain (server/brain_villager.go), driven from
# Villager.customServerAiStep. So this plugin declares the mob (base_type + attributes) with an EMPTY goal
# list: there are no classic goalSelector/targetSelector goals to declare, and no .star goal callbacks.
#
# THE BRAIN (host-native, server/brain_villager.go) — the LOAD-BEARING port:
#   CORE activity (VillagerGoalPackages.getCorePackage, the 1:1 slice):
#     @0  Swim(0.8)
#     @0  LookAtTargetSink(45, 90)
#     @1  MoveToTargetSink()
#     @6  AcquirePoi(acquirableJobSite, JOB_SITE, POTENTIAL_JOB_SITE, onlyIfAdult, empty, (l,p)->true)
#            -> claims a job-site POI within SCAN_RANGE 48 -> take() -> the POI is IS_OCCUPIED -> the area
#               isVillage() returns true -> a REAL bad-omen raid can be created/extended.
#     @10 AssignProfessionFromJobSite() -> promotes POTENTIAL_JOB_SITE to JOB_SITE + assigns the profession
#               whose job-site == the claimed POI (profession key path == POI key path, 1:1).
#   DEFERRED (cite-recorded, never silently dropped): the rest of getCorePackage (doors, bells, raid status,
#   trading LookAndFollowTradingPlayerSink, gossip/gift, GoToPotentialJobSite/YieldJobSite/PoiCompetitorScan
#   nav, the HOME/MEETING AcquirePoi legs) + the WORK/MEET/PANIC/REST activity packages + the merchant menu
#   (ClientboundMerchantOffers — no merchant-menu subsystem) + the trade tables beyond the FARMER_1 sample
#   (server/villager_trades.go) + gossip/reputation price economy + village worldgen. Each needs a
#   not-yet-built subsystem; the job-site acquisition + profession assignment (the phase goal) is fully wired.
#
# createAttributes (VERIFIED CFR Villager.createAttributes): Mob.createMobAttributes().add(MOVEMENT_SPEED,
# 0.5) — NO MAX_HEALTH override (createMobAttributes default MAX_HEALTH 20.0), NO ATTACK_DAMAGE (villager
# does not attack), FOLLOW_RANGE 16.0 (createMobAttributes default). See villagerSupplier (level/attribute/
# defaults.go). Cite net.minecraft.world.entity.npc.villager.Villager.createAttributes + makeBrain.

# base_type "villager" -> renders as entity.Villager.ID (140). The brain (Swim/LookAtTargetSink/
# MoveToTargetSink + AcquirePoi(JOB_SITE) + AssignProfessionFromJobSite) is host-native; there are no
# classic goals, so goals is EMPTY. Cite Villager.registerBrainGoals (brain mob, empty goalSelector).
declare_mob(
    name = "vanilla_villager",
    base_type = "villager",
    attributes = {
        "max_health": 20.0,       # createMobAttributes default MAX_HEALTH 20.0 (no Villager override)
        "movement_speed": 0.5,    # Villager.createAttributes: MOVEMENT_SPEED 0.5
        "follow_range": 16.0,     # createMobAttributes default FOLLOW_RANGE 16.0
    },
    goals = [],
)
