package server

import (
	"fmt"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// runDbgCommand is a DEV-only debug dispatcher (wired to /dbg <sub>) so the operator can spawn mobs and
// build a water box on demand to reproduce mob-physics bugs in-game without world editing. Operator-gated
// via the same command.tp permission. Runs on the issuer's region/tick context.
func (t *TickLoop) runDbgCommand(p *tickPlayer, sub string) {
	switch sub {
	case "pig":
		e := t.spawnVanillaPig(p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned pig eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "cow":
		e := t.spawnVanillaMob(vanillaCowMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned cow eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "sheep":
		e := t.spawnVanillaMob(vanillaSheepMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned sheep eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "chicken":
		e := t.spawnVanillaMob(vanillaChickenMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned chicken eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "zombie":
		e := t.spawnVanillaMob(vanillaZombieMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned zombie eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "skeleton":
		e := t.spawnVanillaMob(vanillaSkeletonMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned skeleton eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "spider":
		e := t.spawnVanillaMob(vanillaSpiderMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned spider eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "wolf":
		// MOB-NEUT-01 (Phase 36): spawn a vanilla wolf. spawnVanillaMob returns nil until Plan C's
		// vanilla_wolf declaration boot-loads (the registry lookup is a graceful nil, never a panic — the
		// `if e != nil` guard mirrors every other arm), so /dbg wolf is a safe no-op until the .star lands.
		e := t.spawnVanillaMob(vanillaWolfMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned wolf eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "husk":
		// MOB-VARIANT (Task #9): spawn a vanilla husk (Zombie behavior, Husk wire type).
		e := t.spawnVanillaMob(vanillaHuskMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned husk eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "mooshroom":
		// MOB-VARIANT (Task #9): spawn a vanilla mooshroom (Cow behavior, Mooshroom wire type).
		e := t.spawnVanillaMob(vanillaMooshroomMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned mooshroom eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "silverfish":
		// MOB-HOST-05 (Task #9): spawn a vanilla silverfish (hunt + melee, Silverfish wire type).
		e := t.spawnVanillaMob(vanillaSilverfishMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned silverfish eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "creeper":
		// MOB-HOST-06 (Task #9): spawn a vanilla creeper (hunt + swell fuse + explosion, Creeper wire type).
		e := t.spawnVanillaMob(vanillaCreeperMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned creeper eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "witch":
		// MOB-HOST-07 (Task #9): spawn a vanilla witch (hunt + splash-potion attack, Witch wire type).
		e := t.spawnVanillaMob(vanillaWitchMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned witch eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "rabbit":
		// MOB-PASS-05 (Task #9): spawn a vanilla rabbit (ambient passive, Rabbit wire type).
		e := t.spawnVanillaMob(vanillaRabbitMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned rabbit eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "enderman":
		// MOB-HOST-08 (Task #9): spawn a vanilla enderman (hunt + melee + teleport, Enderman wire type).
		e := t.spawnVanillaMob(vanillaEndermanMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned enderman eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "cat":
		// MOB-NEUT-03 (Task #9): spawn a vanilla cat (tameable with fish, Cat wire type).
		e := t.spawnVanillaMob(vanillaCatMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned cat eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "fox":
		// MOB-PASS-06 (Task #9): spawn a vanilla fox (ambient passive, Fox wire type).
		e := t.spawnVanillaMob(vanillaFoxMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned fox eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "sulfur_cube", "sulfurcube":
		// MOB-CUBE (SulfurCube): spawn a vanilla sulfur cube (jump-move + split-on-death + size scaling,
		// SulfurCube wire type). Natural spawn size is 2 (SulfurCube.setSpawnSize: adult -> size 2).
		e := t.spawnVanillaMob(vanillaSulfurCubeMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned sulfur_cube eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "happy_ghast", "happyghast", "ghast":
		// happy_ghast (Task): spawn a vanilla happy ghast (hovering flyer, HappyGhast wire type). It floats
		// in place then wanders via the native RandomFloatAround hover (happyGhastAiStep); it does NOT fall.
		e := t.spawnVanillaMob(vanillaHappyGhastMobName, p.x, p.y+3, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned happy_ghast eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+3, p.z))
		}
	case "endermite":
		// MOB-PREY (Task #9): spawn a vanilla endermite (small MONSTER, Endermite wire type). It hunts + melees
		// like a silverfish and DESPAWNS after ~2 min (endermiteAiStep life>=2400) unless made persistent.
		e := t.spawnVanillaMob(vanillaEndermiteMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned endermite eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "turtle":
		// MOB-PREY (Task #9): spawn a vanilla turtle (beach CREATURE, Turtle wire type). It strolls/panics/breeds
		// + is tempted by seagrass; the water-nav + egg-lay goals are cite-deferred (.star header).
		e := t.spawnVanillaMob(vanillaTurtleMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned turtle eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "ocelot":
		// MOB-PREY (Task #9): spawn a vanilla ocelot (jungle CREATURE, Ocelot wire type). It is tempted by
		// cod/salmon + breeds/strolls; the hunt (leap/attack/prey-target) + trust are cite-deferred (.star header).
		e := t.spawnVanillaMob(vanillaOcelotMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned ocelot eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "pillager":
		// RAIDER (Task): spawn a vanilla pillager (crossbow illager, Pillager wire type). Hunts the player +
		// fires the crossbow (pillager_crossbow_attack); patrols via long_distance_patrol.
		e := t.spawnVanillaMob(vanillaPillagerMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned pillager eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "vindicator":
		// RAIDER (Task): spawn a vanilla vindicator (iron-axe illager, Vindicator wire type). Hunts + melees
		// the player; the Johnny name-check + door-break are cite-deferred (.star header).
		e := t.spawnVanillaMob(vanillaVindicatorMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned vindicator eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "evoker":
		// RAIDER (Task): spawn a vanilla evoker (spellcaster, Evoker wire type). Casts the summon/fangs/wololo
		// spells; SUMMON_VEX now spawns real Vexes and FANGS spawns real EvokerFangs (see vex.go / evoker_fangs.go).
		e := t.spawnVanillaMob(vanillaEvokerMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned evoker eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "vex":
		// VEX (Task): spawn a Vex directly (the code-spawned flying Monster the evoker's SUMMON_VEX summons).
		// It flies (VexMoveControl), charges the nearest player (VexChargeAttackGoal), wanders, and starves out
		// (setLimitedLife 20*(30+nextInt(90)) -- here a fixed 600 for the debug spawn). ownerID 0 (no evoker).
		bx, by, bz := int(p.x), int(p.y)+1, int(p.z)
		e := t.spawnVex(0, p.x, p.y+1, p.z, bx, by, bz, 600)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned vex eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "ghast_hostile", "hostile_ghast":
		// GHAST (Task): spawn a hostile Ghast directly (the floating fireball-shooter). It drifts on the
		// RandomFloatAroundGoal, acquires the nearest player within 100 blocks, and charges up (0..20) to
		// shoot a LargeFireball that explodes on impact (explosionPower 1). Spawned 5 blocks up so it hovers.
		e := t.spawnGhast(p.x, p.y+5, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned hostile ghast eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+5, p.z))
		}
	case "blaze":
		// BLAZE (Task): spawn a hostile Blaze directly (the nether melee-or-fireball-burst hostile). It
		// acquires the nearest player within FOLLOW_RANGE (48), meleees for 6.0 when adjacent, and at range
		// fires a 3-SmallFireball burst (attack-step cadence) that ignites + deals 5.0 fire damage. It takes
		// 1.0 drown damage per tick in water/rain (isSensitiveToWater). Spawned 1 block up.
		e := t.spawnBlaze(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned blaze eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "magma_cube", "magmacube":
		// MAGMA CUBE (Task): spawn a hostile MagmaCube (the nether slime that hops, splits on death into
		// 2..4 smaller cubes, and touches for size+2 damage; per-size MAX_HEALTH size*size, MOVEMENT_SPEED
		// 0.2+0.1*size, ARMOR size*3). Fire+lava immune. Spawned at size 2.
		e := t.spawnMagmaCube(p.x, p.y+1, p.z, magmaCubeSpawnSize)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned magma_cube eid=%d size=%d at (%.1f,%.1f,%.1f)", e.id, e.cubeSize, p.x, p.y+1, p.z))
		}
	case "strider":
		// STRIDER (Task): spawn a Strider (the nether lava-walker). It rides the lava surface without sinking
		// (canStandOnFluid(LAVA)) and, off a warm block / out of lava, enters the cold suffocating state that
		// slows it (MOVEMENT_SPEED -0.34 ADD_MULTIPLIED_BASE). Fire+lava immune.
		e := t.spawnStrider(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned strider eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "wither_skeleton", "witherskeleton":
		// WITHER SKELETON (GAP): spawn a WitherSkeleton (the nether melee skeleton). It holds a STONE_SWORD,
		// hits for 4.0, and inflicts WITHER 200 (10s) on a landed melee hit. Fire immune (nether), tall box
		// (0.7 x 2.4). Spawned 1 block up.
		e := t.spawnWitherSkeleton(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned wither_skeleton eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "hoglin":
		// HOGLIN (GAP): spawn an adult Hoglin (the nether beast). MAX_HEALTH 40, hits for a 6.0-base damage
		// roll and FLINGS the target upward (the knock-up toss). It converts to a Zoglin after > 300 ticks in
		// a non-nether dimension. Spawned 1 block up (adult).
		e := t.spawnHoglin(p.x, p.y+1, p.z, false, p.dimension)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned hoglin eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "piglin":
		// PIGLIN (Task): spawn an adult Piglin directly (the flagship nether hostile). It acquires the nearest
		// player NOT wearing gold armor within FOLLOW_RANGE and melees for 5.0 when adjacent; a gold-armored
		// player is NEUTRAL (never targeted). Right-click it with a gold ingot to BARTER (consumes 1 ingot,
		// drops a piglin_bartering roll). OFF the nether it zombifies after 300 ticks -> zombified_piglin.
		// Spawned 1 block up as an adult.
		e := t.spawnPiglin(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned piglin eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "fangs":
		// VEX + FANGS (Task): spawn an EvokerFangs directly (the code-spawned projectile the evoker's FANGS
		// spell places). It warms up, bites for 6.0 magic at warmupDelayTicks==-8, then despawns (~22 ticks).
		// ownerID 0 (no evoker -> plain magic), warmupDelay 0 (immediate telegraph).
		e := t.spawnEvokerFangs(0, p.x, p.y, p.z, 0.0, 0)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned evoker_fangs eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "iron_golem", "golem":
		// IRON GOLEM (Task): spawn a vanilla iron golem (village defender, IronGolem wire type). Melees with
		// the range-roll damage (15/2 + nextInt(15)) + the +0.4 vertical fling; hunts hostile mobs + retaliates
		// on hit (angry-player + hurt-by targets). The village/villager goals are cite-deferred.
		e := t.spawnVanillaMob(vanillaIronGolemMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned iron_golem eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "villager":
		// VILLAGER (Task): spawn a vanilla villager (BRAIN mob, Villager wire type). It runs the ported CORE
		// brain (Swim/LookAtTargetSink/MoveToTargetSink + AcquirePoi(JOB_SITE) + AssignProfessionFromJobSite):
		// place a job-site block (e.g. composter) near it and it claims the POI (-> IS_OCCUPIED -> the area
		// isVillage() -> a REAL bad-omen raid can fire) and takes the matching profession. Trades + merchant
		// menu are cite-deferred (no merchant-menu subsystem). Spawns professionless at level 1.
		e := t.spawnVanillaMob(vanillaVillagerMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned villager eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "villager_farmer":
		// VILLAGER (test-only): a FARMER level-1 villager with offers already assigned — the merchant-menu
		// path needs a villager whose getOffers() is non-empty (a professionless villager's mobInteract
		// gate returns before opening, which is jar-faithful). This shortcut sets VillagerData(farmer, 1) so
		// villagerGetOffers builds farmerLevel1Offers, so right-clicking it opens the trade screen. Skips the
		// AcquirePoi job-site claim (which /dbg villager exercises) — this arm is for exercising the MENU.
		e := t.spawnVanillaMob(vanillaVillagerMobName, p.x, p.y, p.z)
		if e != nil {
			e.villagerProfession = "farmer"
			e.villagerLevel = 1
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned villager_farmer eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "ravager":
		// RAIDER (Task): spawn a vanilla ravager (raid beast, Ravager wire type). Hunts + melees + roars
		// (ravagerAiStep: attackTick/roar AoE/stun; the leaf-trample + stun-trigger are cite-deferred).
		e := t.spawnVanillaMob(vanillaRavagerMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned ravager eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "dragon", "ender_dragon", "enderdragon":
		// ENDER DRAGON (Task): spawn the full End dragon fight -- the EnderDragon boss (200 HP, 8 sub-part
		// hitboxes, HOLDING circling flight) at the fight origin (0,128,0) plus a ring of 10 EndCrystals
		// (the healing beacons: while a crystal is the dragon's nearestCrystal it heals +1 every 10 ticks;
		// destroy a crystal to make the dragon take a 10.0 head hit). Boss bar (PINK/PROGRESS/fog/music) is
		// sent to every player. The obsidian-pillar towers + exit-portal podium are cite-deferred (the fight
		// spawns a bare crystal ring + places an END_PORTAL + DRAGON_EGG at the origin on death). One fight
		// per world (idempotent via t.endDragonFightInit); use spawnEnderDragon directly for a bare dragon.
		d := t.spawnEndDragonFight()
		if d != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned ender dragon fight: dragon eid=%d at (0,128,0) + 10 end crystals (destroy a crystal to hit the dragon; boss bar sent)", d.id))
		} else {
			t.broadcastSystemChat("[dbg] ender dragon fight already initialized this world (one fight per world)")
		}
	case "water":
		t.dbgFillWater(p)
		t.broadcastSystemChat("[dbg] filled a water box around you")
	case "pig-in-water", "piw":
		t.dbgFillWater(p)
		e := t.spawnVanillaPig(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] water box + pig eid=%d dropped in", e.id))
		}
	case "raid":
		// RAID subsystem (raid.go/raids.go): start a raid at the player's position via the createRaidAt SEAM
		// (the CITE-DEFERRED bad-omen village auto-trigger stand-in — no POI subsystem). Difficulty NORMAL
		// (5 waves), raidOmenLevel 1. The raid ticks on the coordinator each tick (raidsTickAllRegions),
		// counting down the 300-tick pre-wave cooldown then spawning waves. ALL FIVE RaiderTypes now spawn REAL
		// raiders (vindicator/evoker/pillager/witch/ravager per the wave table).
		rm := t.only().ensureRaidsManager()
		raid := rm.createRaidAt(int(p.x), int(p.y), int(p.z), difficultyNormal, 1)
		t.broadcastSystemChat(fmt.Sprintf("[dbg] started raid id=%d at (%d,%d,%d) numGroups=%d (waves spawn after the 300-tick cooldown; all 5 RaiderTypes now spawn real raiders)", raid.id, int(p.x), int(p.y), int(p.z), raid.numGroups))
	case "rain":
		// WEATHER (test-only, e2e bot): force the world into raining NOW and broadcast the START_RAINING
		// game event to every player, so the interaction bot can assert the client receives it. Sets the
		// weather flags/level directly (the real advanceWeatherCycle path is unit-tested); this is only a
		// deterministic trigger for the live-client check.
		t.weather.raining = true
		t.weather.rainLevel = 1.0
		t.weather.rainTime = 12000
		t.broadcastGameEvent(gameEventStartRaining, 0)
		t.broadcastGameEvent(gameEventRainLevelChange, 1.0)
		t.broadcastSystemChat("[dbg] forced rain (START_RAINING broadcast)")
	case "trade":
		// MERCHANT (test-only, e2e bot): spawn a FARMER villager AND open its trade screen for the issuer in
		// one shot — no client interact round-trip (which races the villager's tracker AddEntity on a busy
		// server). Proves the full ClientboundMerchantOffers path server-side deterministically.
		e := t.spawnVanillaMob(vanillaVillagerMobName, p.x, p.y, p.z)
		if e != nil {
			e.villagerProfession = "farmer"
			e.villagerLevel = 1
			if t.openMerchantMenu(p, e) {
				t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned farmer villager eid=%d + opened trade screen", e.id))
			} else {
				t.broadcastSystemChat("[dbg] trade: openMerchantMenu returned false")
			}
		}
	case "redstone":
		// REDSTONE (test-only, e2e bot): build a minimal lit rig — a redstone_block (constant-15 source)
		// with a redstone_wire beside it — then run onRedstoneEdit so the wire recomputes to POWER 15 and
		// its ClientboundBlockUpdate reaches the client. Proves the signal graph drives a real block-state
		// change a vanilla client observes. Placed at feet level 2 blocks in front (+X) on the ground.
		bx, by, bz := int(p.x)+2, int(p.y), int(p.z)
		srcPos := pk.Position{X: bx, Y: by, Z: bz}
		wirePos := pk.Position{X: bx + 1, Y: by, Z: bz}
		src, okS := block.DefaultStateID["minecraft:redstone_block"]
		wire, okW := block.DefaultStateID["minecraft:redstone_wire"]
		if !okS || !okW || t.world() == nil {
			t.broadcastSystemChat("[dbg] redstone: missing block states or world")
			break
		}
		t.world().SetBlock(srcPos, src, dimMinY)
		t.world().SetBlock(wirePos, wire, dimMinY)
		t.broadcastBlockUpdate(srcPos, src)
		t.broadcastBlockUpdate(wirePos, wire)
		// Recompute the wire's power from its new redstone_block neighbor; the evaluator sets POWER 15 and
		// broadcasts the wire's updated state.
		t.onRedstoneEdit(wirePos)
		t.broadcastSystemChat(fmt.Sprintf("[dbg] placed redstone_block(%d,%d,%d)+wire(%d,%d,%d); wire should be POWER 15", bx, by, bz, bx+1, by, bz))
	default:
		t.broadcastSystemChat("[dbg] usage: /dbg pig | cow | sheep | chicken | zombie | skeleton | spider | wolf | husk | mooshroom | silverfish | creeper | witch | rabbit | enderman | cat | fox | sulfur_cube | happy_ghast | endermite | turtle | ocelot | pillager | vindicator | evoker | ravager | dragon | iron_golem | villager | villager_farmer | vex | ghast_hostile | blaze | strider | wither_skeleton | hoglin | fangs | water | pig-in-water | raid | rain | redstone | trade")
	}
}

// dbgFillWater fills a 5x5 (x,z) by 5-deep (y) water box centered on the player, on a solid stone floor,
// so a mob spawned in it is genuinely submerged with fluid above its head (FloatGoal.canUse precondition).
func (t *TickLoop) dbgFillWater(p *tickPlayer) {
	if t.world() == nil {
		return
	}
	cx, cy, cz := int(p.x), int(p.y), int(p.z)
	water := waterStateID(0)
	stone := block.DefaultStateID["minecraft:stone"]
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			// solid floor 1 below the box
			t.world().SetBlock(pk.Position{X: cx + dx, Y: cy - 1, Z: cz + dz}, stone, dimMinY)
			for dy := 0; dy <= 4; dy++ {
				t.world().SetBlock(pk.Position{X: cx + dx, Y: cy + dy, Z: cz + dz}, water, dimMinY)
			}
		}
	}
}
