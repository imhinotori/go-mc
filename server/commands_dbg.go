package server

import (
	"fmt"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
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
	case "cave_spider":
		// MOB (CaveSpider): spawn a vanilla cave spider (Spider behavior, CaveSpider wire type, max_health 12).
		// On a landed melee hit it applies POISON (NORMAL i*20 = 140 ticks, amp 0) via caveSpiderApplyPoison.
		e := t.spawnVanillaMob(vanillaCaveSpiderMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned cave_spider eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "illusioner":
		// MOB (Illusioner): spawn a vanilla illusioner (SpellcasterIllager). It hunts the nearest player,
		// fires a bow at range, and periodically casts BLINDNESS 400 on its target while turning invisible
		// (illusionerAiStep). The mirror-image clones are cite-deferred.
		e := t.spawnVanillaMob(vanillaIllusionerMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned illusioner eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "polar_bear":
		// MOB (PolarBear): spawn a vanilla polar bear (NEUTRAL animal, max_health 30). It strolls/looks like a
		// passive but retaliates (hurt_by_target) with a 6.0 maul when provoked.
		e := t.spawnVanillaMob(vanillaPolarBearMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned polar_bear eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "giant":
		// MOB (Giant): spawn a vanilla giant (INERT giant zombie, max_health 100). It registers NO goals -- it
		// just stands (Giant.registerGoals adds nothing). ATTACK_DAMAGE 50 is dealt only if it ever hits.
		e := t.spawnVanillaMob(vanillaGiantMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned giant eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "piglin_brute":
		// MOB (PiglinBrute): spawn a PiglinBrute directly (the always-hostile bastion guard, Go-native spawn
		// like the blaze/piglin). It acquires the nearest player within FOLLOW_RANGE (12) and melees for 7.0
		// when adjacent -- ALWAYS hostile (no gold neutrality, no barter, no zombify). Spawned 1 block up.
		e := t.spawnPiglinBrute(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned piglin_brute eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "drowned":
		// MOB-VARIANT (Drowned): spawn a vanilla drowned (Zombie behavior + a trident-throw when it rolls a
		// TRIDENT at spawn; it hunts + melees like a zombie, and burns in daylight). The water-nav goals are
		// deferred (no fluid subsystem). Drowned wire type.
		e := t.spawnVanillaMob(vanillaDrownedMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned drowned eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "stray":
		// MOB-VARIANT (Stray): spawn a vanilla stray (Skeleton behavior; its bow fires SLOWNESS-600 tipped
		// arrows). Stray wire type.
		e := t.spawnVanillaMob(vanillaStrayMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned stray eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "bogged":
		// MOB-VARIANT (Bogged): spawn a vanilla bogged (Skeleton behavior, MAX_HEALTH 16; its bow fires
		// POISON-100 tipped arrows; shear it for a red mushroom). Bogged wire type.
		e := t.spawnVanillaMob(vanillaBoggedMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned bogged eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "zombie_villager", "zombievillager":
		// MOB-VARIANT (ZombieVillager): spawn a vanilla zombie villager (Zombie behavior). Right-click it with
		// a golden apple WHILE it has WEAKNESS to start the cure -> after a 3600..6000-tick countdown it turns
		// into a Villager. ZombieVillager wire type.
		e := t.spawnVanillaMob(vanillaZombieVillagerMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned zombie_villager eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
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
	case "parrot":
		// PARROT (Task): spawn a vanilla parrot (flying passive TamableAnimal, Parrot wire type). It flies via
		// the soft-flyer nav (gravity applies), rolls 1 of 5 plumage variants at spawn, and is tamed with seeds.
		e := t.spawnParrot(p.x, p.y+3, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned parrot eid=%d (variant=%d) at (%.1f,%.1f,%.1f)", e.id, e.parrotVariant, p.x, p.y+3, p.z))
		}
	case "bat":
		// BAT (Task): spawn a vanilla bat (ambient flyer, Bat wire type). It spawns RESTING (hanging), wakes
		// when a player comes within 4 blocks or the ceiling is removed, then drifts to random targets. No attack.
		e := t.spawnBat(p.x, p.y+3, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned bat eid=%d (resting=%v) at (%.1f,%.1f,%.1f)", e.id, e.batResting, p.x, p.y+3, p.z))
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
	case "guardian":
		// GUARDIAN (Task): spawn an aquatic Guardian (the laser-beam hostile). It acquires the nearest player
		// within FOLLOW_RANGE (16), CHARGES a beam for 80 ticks in line of sight, then deals 1.0 (+2 on HARD)
		// indirect-magic damage + a 6.0 melee follow-up. A melee attacker striking it while stationary takes
		// 2.0 spike thorns. It drowns on land (water mob) + flops. Spawned 1 block up.
		e := t.spawnGuardian(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned guardian eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "elder_guardian", "elderguardian":
		// ELDER GUARDIAN (Task): spawn a boss-tier ElderGuardian (MAX_HEALTH 80, ATTACK_DAMAGE 8, bbox ~2.35x).
		// Same laser beam (60-tick charge, +2 elder bonus so 3.0 / 5.0-on-HARD indirect + 8.0 melee) + the 2.0
		// spike thorns, PLUS a MINING_FATIGUE III (6000-tick) AoE to every survival player within 50 blocks
		// every 1200 ticks. Spawned 1 block up.
		e := t.spawnElderGuardian(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned elder_guardian eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "phantom":
		// PHANTOM (Task): spawn a Phantom directly (the flying night hostile that dive-bombs). It scans for the
		// nearest player every 60 ticks, CIRCLES a high anchor above the target, then periodically SWOOPS down
		// (dive-bomb) to melee for 6.0 and climbs back. It BURNS in daylight (undead, 8s ignite roll). Spawned
		// 12 blocks up so it has room to circle + dive. Use at night to avoid the immediate daylight burn.
		e := t.spawnPhantom(p.x, p.y+12, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned phantom eid=%d at (%.1f,%.1f,%.1f) -- circles then dive-bombs; burns in daylight", e.id, p.x, p.y+12, p.z))
		}
	case "shulker":
		// SHULKER (Task): spawn a Shulker directly (the End box-turret hostile, 30 HP). It clings CLOSED
		// with +20 armor, OPENS to fire a homing ShulkerBullet at the nearest player within 15 blocks that
		// deals 4.0 + inflicts LEVITATION (200 ticks), and TELEPORTS to a new attach surface when disturbed.
		// Spawned 1 block up on the player column so it has a floor to cling to.
		e := t.spawnShulker(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned shulker eid=%d at (%.1f,%.1f,%.1f) -- closed+armored, opens to fire levitation bullets", e.id, p.x, p.y+1, p.z))
		}
	case "warden":
		// WARDEN (Task): spawn a Warden directly (the blind, sculk-summoned boss-tier hostile, 500 HP). It
		// EMERGES for 134 ticks (locked), then tracks by ANGER: a player within FOLLOW_RANGE (24) accrues
		// anger, and the warden hunts the highest-anger suspect -- MELEE-SLAMMING for 30 (+1.5 knockback)
		// when adjacent, or firing a SONIC BOOM (10 damage + knock-up, ignores armor/shields) when the
		// target is within 15 XZ / 20 Y but out of melee reach. With no anger for ~60s it DIGS AWAY +
		// despawns. Spawned 1 block up (box 0.9 x 2.9). Also summoned at sculk-shrieker warning level 4.
		e := t.spawnWarden(p.x, p.y+1, p.z, true)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned warden eid=%d at (%.1f,%.1f,%.1f) -- 500hp, emerges then hunts by anger (melee 30 / sonic-boom 10); digs away after ~60s idle", e.id, p.x, p.y+1, p.z))
		}
	case "magma_cube", "magmacube":
		// MAGMA CUBE (Task): spawn a hostile MagmaCube (the nether slime that hops, splits on death into
		// 2..4 smaller cubes, and touches for size+2 damage; per-size MAX_HEALTH size*size, MOVEMENT_SPEED
		// 0.2+0.1*size, ARMOR size*3). Fire+lava immune. Spawned at size 2.
		e := t.spawnMagmaCube(p.x, p.y+1, p.z, magmaCubeSpawnSize)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned magma_cube eid=%d size=%d at (%.1f,%.1f,%.1f)", e.id, e.cubeSize, p.x, p.y+1, p.z))
		}
	case "slime":
		// SLIME (Task): spawn a hostile Slime (the cube-mob that HOPS, splits on death into 2..4 half-size
		// slimes, and touches for size damage only when size>1 -- a tiny size-1 slime deals NO damage). Per-
		// size MAX_HEALTH size*size, MOVEMENT_SPEED 0.2+0.1*size, ATTACK_DAMAGE size (NO armor). Spawned at
		// size 2 so the split is visible (a size-2 slime -> 2..4 size-1 slimes, each dropping a slimeball).
		e := t.spawnSlime(p.x, p.y+1, p.z, slimeSpawnSize)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned slime eid=%d size=%d at (%.1f,%.1f,%.1f) -- kill it to see the split", e.id, e.cubeSize, p.x, p.y+1, p.z))
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
	case "wither", "wither_boss", "witherboss":
		// WITHER BOSS (Task): spawn the WitherBoss (300 HP nether boss). On spawn it charges up for 220
		// invulnerable ticks (immune, boss bar ramping 0->1), then detonates a power-7 explosion and fights:
		// its 3 heads shoot WitherSkull projectiles at the nearest player (8.0 dmg + WITHER effect + power-1
		// explosion on hit), it heals +1 every 20 ticks, and below 50%% HP it armors up (arrow/wind-charge
		// shield + it smashes the blocks in its AABB). Purple boss bar (darkened screen). On death it drops a
		// NETHER_STAR. Spawned 2 blocks up (its box is 0.9 x 3.5). Use /dbg on a clear area (the spawn explosion).
		e := t.spawnWither(p.x, p.y+2, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned wither eid=%d at (%.1f,%.1f,%.1f) -- 220-tick invuln charge-up, then it detonates + fights (boss bar sent)", e.id, p.x, p.y+2, p.z))
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
	case "zombified_piglin", "zpiglin":
		// ZOMBIFIED PIGLIN (GAP): the NEUTRAL nether undead. It IGNORES players until PROVOKED (hit it, or
		// hit a pack member nearby) -- then it goes angry, RETALIATES, and SPREADS its anger to nearby
		// zombified piglins (the anger pack). Fire/lava immune; does NOT burn in daylight. Spawned 1 block up.
		e := t.spawnZombifiedPiglin(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned zombified_piglin eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "panda":
		// PANDA (Task): spawn an adult Panda (the bamboo-jungle Animal, gene/variant system). MOVEMENT_SPEED
		// 0.15, ATTACK_DAMAGE 6, MAX_HEALTH 20 (a WEAK panda diverges to 10, a LAZY panda to speed 0.07 via
		// setAttributes). Rolls a main+hidden gene (getRandom x2); the observable variant is getVariantFrom
		// Genes(main,hidden). Passive goal walk (Float/Panic/Breed/Tempt(panda_food=bamboo)/Follow/Stroll/Look);
		// the roll/sneeze/sit/lie temperament cosmetics are DEFERRED. Spawned 1 block up.
		e := t.spawnPanda(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned panda eid=%d (main=%d hidden=%d variant=%d) at (%.1f,%.1f,%.1f)", e.id, e.pandaMainGene, e.pandaHiddenGene, pandaGetVariant(e), p.x, p.y+1, p.z))
		}
	case "snow_golem":
		// SNOW_GOLEM (Task): spawn a SnowGolem (the snow-trail ranged golem). MAX_HEALTH 4, MOVEMENT_SPEED 0.2.
		// Leaves a snow trail as it walks (aiStep, MOB_GRIEFING-gated); wears a pumpkin (shearable). The snowball
		// RangedAttack + melt-in-warm-biome are DEFERRED. Spawned 1 block up.
		e := t.spawnSnowGolem(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned snow_golem eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "armadillo":
		// ARMADILLO (Task): spawn an adult Armadillo. MAX_HEALTH 12, MOVEMENT_SPEED 0.14. Rolls up when a
		// sprinting/riding player is nearby (isScaredBy), sheds a scute periodically. Spawned 1 block up.
		e := t.spawnArmadillo(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned armadillo eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "breeze":
		// BREEZE (Task): spawn a hostile Breeze. MAX_HEALTH 30, MOVEMENT_SPEED 0.63, FOLLOW_RANGE 24,
		// ATTACK_DAMAGE 3. Fires wind charges (projectile DEFERRED) on the shoot cadence. Spawned 1 block up.
		e := t.spawnBreeze(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned breeze eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "creaking":
		// CREAKING (Task): spawn a hostile Creaking. MAX_HEALTH 1, MOVEMENT_SPEED 0.4, ATTACK_DAMAGE 3,
		// FOLLOW_RANGE 32. FREEZES when a player looks at it; activates + melees when caught unobserved.
		// Spawned 1 block up.
		e := t.spawnCreaking(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned creaking eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "copper_golem":
		// COPPER GOLEM (Task): spawn a CopperGolem. MAX_HEALTH 12, MOVEMENT_SPEED 0.2, STEP_HEIGHT 1.0.
		// Oxidizes over time (UNAFFECTED->EXPOSED->WEATHERED->OXIDIZED). Button/chest + statue DEFERRED.
		// Spawned 1 block up.
		e := t.spawnCopperGolem(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned copper_golem eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "bee":
		// BEE (Task): spawn an adult Bee (the flying passive/neutral animal). MAX_HEALTH 10, FLYING_SPEED 0.6,
		// MOVEMENT_SPEED 0.3, ATTACK_DAMAGE 2. Passive goal walk (Float/Tempt(bee_food)/Breed/Follow/Wander); the
		// hive/pollination + neutral-anger sting-pursuit are DEFERRED. Spawned 1 block up.
		e := t.spawnBee(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned bee eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "goat":
		// GOAT (Task): spawn an adult Goat (the mountain animal). MAX_HEALTH 10, MOVEMENT_SPEED 0.2, ATTACK_DAMAGE
		// 2. Passive goal walk (Float/Panic/Breed/Tempt(goat_food)/Follow/Stroll/Look); a rare screaming variant
		// (nextDouble() < 0.02). The RAM + high goat-jump are DEFERRED (brain). Spawned 1 block up.
		e := t.spawnGoat(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned goat eid=%d (screaming=%v) at (%.1f,%.1f,%.1f)", e.id, e.goatScreaming, p.x, p.y+1, p.z))
		}
	case "frog":
		// FROG (Task): spawn an adult Frog (the swamp animal). MOVEMENT_SPEED 1.0, MAX_HEALTH 10, ATTACK_DAMAGE 10,
		// STEP_HEIGHT 1.0 (full-block hop-up). Passive goal walk (Float/Panic/Breed/Tempt(frog_food)/Follow/Stroll/
		// Look); temperate variant. The long-jump + tongue-eat/frogspawn are DEFERRED (brain). Spawned 1 block up.
		e := t.spawnFrog(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned frog eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "camel":
		// CAMEL (Task): spawn an adult Camel (the desert AbstractHorse). MAX_HEALTH 32, MOVEMENT_SPEED 0.09,
		// STEP_HEIGHT 1.5, SAFE_FALL_DISTANCE 6. Passive goal walk (Float/Panic/Breed/Tempt(camel_food)/Follow/
		// Stroll/Look). The sit/stand pose + dash + 2-seat rideable are DEFERRED (brain). Spawned 1 block up.
		e := t.spawnCamel(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned camel eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "sniffer":
		// SNIFFER (Task): spawn an adult Sniffer (the ancient dig animal). MAX_HEALTH 14, MOVEMENT_SPEED 0.1.
		// Passive goal walk (Float/Panic/Breed/Tempt(sniffer_food)/Follow/Stroll/Look). The dig-for-seeds
		// DATA_STATE machine + ancient-seed drop are DEFERRED (brain). Spawned 1 block up.
		e := t.spawnSniffer(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned sniffer eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "allay":
		// ALLAY (Task): spawn an Allay (the flying helper, a "misc" PathfinderMob). MAX_HEALTH 20, FLYING_SPEED
		// 0.1, MOVEMENT_SPEED 0.1, ATTACK_DAMAGE 2. Flying passive goal walk (Float/Stroll/Look); NOT Ageable
		// (no baby, no breed). The item-fetch + follow-note + amethyst duplicate are DEFERRED (brain). Spawned 1 up.
		e := t.spawnAllay(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned allay eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "axolotl":
		// AXOLOTL (Task): spawn an adult Axolotl (the amphibious animal). MAX_HEALTH 14, MOVEMENT_SPEED 1.0,
		// ATTACK_DAMAGE 2, STEP_HEIGHT 1.0. Passive goal walk (Float/Panic/Breed/Tempt(axolotl_food)/Follow/
		// Stroll/Look); lucy variant. The play-dead self-regen + 5-color variant are DEFERRED (brain). Spawned 1 up.
		e := t.spawnAxolotl(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned axolotl eid=%d (variant=%d) at (%.1f,%.1f,%.1f)", e.id, e.axolotlVariant, p.x, p.y+1, p.z))
		}
	case "horse":
		// HORSE (Task): spawn an adult Horse (the rideable/tameable/breedable AbstractHorse). Attributes are
		// per-entity RANDOMIZED at spawn: MAX_HEALTH 15..30 (generateMaxHealth), MOVEMENT_SPEED 0.1125..0.3375
		// (generateSpeed), JUMP_STRENGTH 0.4..1.0 (generateJumpStrength) -- the exact RNG draw order. Passive
		// goal walk (Float/Panic/Breed/Tempt(horse_tempt_items)/Follow/Stroll/Look). The saddle/armor inventory
		// + rideable mount + jump-launch are the DEFERRED packet/GUI layer. Spawned 1 block up.
		e := t.spawnHorse(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned horse eid=%d (hp=%.1f jump=%.3f) at (%.1f,%.1f,%.1f)", e.id, e.health, e.horseJumpStrength, p.x, p.y+1, p.z))
		}
	case "donkey":
		// DONKEY (Task): spawn an adult Donkey (the chested AbstractChestedHorse). MAX_HEALTH 15..30 randomized;
		// MOVEMENT_SPEED 0.175 + JUMP_STRENGTH 0.5 (chested base). 5-column chest inventory when a chest is added
		// (DEFERRED GUI). Breeds with Horse -> MULE. Spawned 1 block up.
		e := t.spawnDonkey(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned donkey eid=%d (hp=%.1f) at (%.1f,%.1f,%.1f)", e.id, e.health, p.x, p.y+1, p.z))
		}
	case "mule":
		// MULE (Task): spawn an adult Mule (the STERILE Horse x Donkey hybrid). MAX_HEALTH 15..30 randomized;
		// chested base speed/jump. canMate is the AbstractHorse false base -> a mule cannot breed. Spawned 1 up.
		e := t.spawnMule(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned mule eid=%d (hp=%.1f sterile) at (%.1f,%.1f,%.1f)", e.id, e.health, p.x, p.y+1, p.z))
		}
	case "llama":
		// LLAMA (Task): spawn an adult Llama (the chested spitting AbstractChestedHorse). MAX_HEALTH 15..30
		// randomized; per-llama STRENGTH 1..5 (setRandomStrength: nextFloat()<0.04 -> 1+nextInt(5) else
		// 1+nextInt(3)) which sets the chest inventory columns. The spit ranged attack + caravan-follow are
		// wired as hooks (llamaSpit). Spawned 1 block up.
		e := t.spawnLlama(p.x, p.y+1, p.z, false, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned llama eid=%d (hp=%.1f strength=%d) at (%.1f,%.1f,%.1f)", e.id, e.health, e.llamaStrength, p.x, p.y+1, p.z))
		}
	case "trader_llama", "traderllama":
		// TRADER LLAMA (Task): spawn an adult TraderLlama (the wandering-trader llama variant -- same Llama
		// stats/spit/strength, flagged as trader). Spawned 1 block up.
		e := t.spawnLlama(p.x, p.y+1, p.z, false, true)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned trader_llama eid=%d (hp=%.1f strength=%d) at (%.1f,%.1f,%.1f)", e.id, e.health, e.llamaStrength, p.x, p.y+1, p.z))
		}
	case "skeleton_horse", "skeletonhorse":
		// SKELETON HORSE (Task): the undead AbstractHorse. Fixed MAX_HEALTH 15.0 / MOVEMENT_SPEED 0.2;
		// JUMP_STRENGTH randomized 0.4..1.0 (generateJumpStrength). The skeleton-trap (a lightning strike on a
		// trapped one spawns 4 skeleton riders) is the DEFERRED trap-charge subsystem -- a /dbg one is a plain
		// (non-trap) tameable undead mount. Spawned 1 block up.
		e := t.spawnSkeletonHorse(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned skeleton_horse eid=%d (hp=%.1f jump=%.3f) at (%.1f,%.1f,%.1f)", e.id, e.health, e.horseJumpStrength, p.x, p.y+1, p.z))
		}
	case "zombie_horse", "zombiehorse":
		// ZOMBIE HORSE (Task): the undead AbstractHorse. Fixed MAX_HEALTH 25.0; JUMP_STRENGTH via
		// generateZombieHorseJumpStrength (0.5 base) THEN MOVEMENT_SPEED via generateZombieHorseSpeed
		// ((9+3s)/42.16) -- the exact draw order. Tameable, no natural spawn. Spawned 1 block up.
		e := t.spawnZombieHorse(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned zombie_horse eid=%d (hp=%.1f jump=%.3f) at (%.1f,%.1f,%.1f)", e.id, e.health, e.horseJumpStrength, p.x, p.y+1, p.z))
		}
	case "nautilus":
		// NAUTILUS (Task, NEW 26.2): the tameable aquatic mount (TamableAnimal). MAX_HEALTH 15, MOVEMENT_SPEED
		// 1.0, ATTACK_DAMAGE 3.0, KNOCKBACK_RESISTANCE 0.3. Breathes underwater; the brain/rideable/inventory
		// are DEFERRED, so it runs the bounded passive swim goals. Spawned 1 block up.
		e := t.spawnNautilus(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned nautilus eid=%d (hp=%.1f) at (%.1f,%.1f,%.1f)", e.id, e.health, p.x, p.y+1, p.z))
		}
	case "zombie_nautilus", "zombienautilus":
		// ZOMBIE NAUTILUS (Task, NEW 26.2): the zombified aquatic mount -- same AbstractNautilus base but
		// MOVEMENT_SPEED 1.1, undead-classified. Bounded swim goals (brain DEFERRED). Spawned 1 block up.
		e := t.spawnZombieNautilus(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned zombie_nautilus eid=%d (hp=%.1f) at (%.1f,%.1f,%.1f)", e.id, e.health, p.x, p.y+1, p.z))
		}
	case "mannequin":
		// MANNEQUIN (Task, NEW 26.2): the player-shaped, no-AI display entity (Avatar subclass). No goal AI --
		// a static display carrying a ResolvableProfile + immovable flag (DEFERRED skin/profile data). Spawned
		// immovable (a placed mannequin is not pushed). Spawned 1 block up.
		e := t.spawnMannequin(p.x, p.y+1, p.z, true)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned mannequin eid=%d (hp=%.1f immovable=%v) at (%.1f,%.1f,%.1f)", e.id, e.health, e.mannequinImmovable, p.x, p.y+1, p.z))
		}
	case "squid":
		// SQUID (Task): the ink-jet cephalopod (MAX_HEALTH 10). Breathes underwater, DROWNS ON LAND. Its
		// aiStep runs the tentacle-rotation accumulator; a hit-by-mob spawns ink. Spawned 1 block up.
		e := t.spawnSquid(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned squid eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "glow_squid":
		// GLOW SQUID (Task): the glowing Squid variant (MAX_HEALTH 10, inherits Squid). The dark-ticks glow
		// DATA is DEFERRED (needs the glowing effect). Spawned 1 block up.
		e := t.spawnSquid(p.x, p.y+1, p.z, true)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned glow_squid eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "cod":
		// COD (Task): a schooling fish (MAX_HEALTH 3). Breathes underwater, DROWNS ON LAND. Spawned 1 up.
		e := t.spawnFish(entity.Cod, p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned cod eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "salmon":
		// SALMON (Task): a schooling fish (MAX_HEALTH 3). Breathes underwater, DROWNS ON LAND. Spawned 1 up.
		e := t.spawnFish(entity.Salmon, p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned salmon eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
		}
	case "pufferfish":
		// PUFFERFISH (Task): the puff fish (MAX_HEALTH 3). Puffs 0->1->2 when a scary mob nears (deflates
		// when it leaves) and stings (1+puffState dmg + POISON 60*puffState) on touch. Spawned 1 up.
		e := t.spawnFish(entity.Pufferfish, p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned pufferfish eid=%d (puff=%d) at (%.1f,%.1f,%.1f)", e.id, e.pufferPuffState, p.x, p.y+1, p.z))
		}
	case "tropical_fish":
		// TROPICAL FISH (Task): a schooling fish (MAX_HEALTH 3) with a packed 2-pattern variant (DEFAULT 0;
		// the 2-pattern client render is DEFERRED). Breathes underwater, DROWNS ON LAND. Spawned 1 up.
		e := t.spawnFish(entity.TropicalFish, p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned tropical_fish eid=%d (variant=%d) at (%.1f,%.1f,%.1f)", e.id, e.tropicalVariant, p.x, p.y+1, p.z))
		}
	case "dolphin":
		// DOLPHIN (Task): the fast swimmer (MAX_HEALTH 10, MOVEMENT_SPEED 1.2, ATTACK_DAMAGE 3). Moistness
		// 2400 in water; out of water it drains and takes dryOut damage at <= 0. Jump/play/treasure DEFERRED.
		e := t.spawnDolphin(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned dolphin eid=%d (moist=%d) at (%.1f,%.1f,%.1f)", e.id, e.dolphinMoistness, p.x, p.y+1, p.z))
		}
	case "tadpole":
		// TADPOLE (Task): the frog baby-stage (MOVEMENT_SPEED 1.0, MAX_HEALTH 6). Ages every tick and GROWS
		// INTO A FROG at ticksToBeFrog (24000). Breathes underwater, DROWNS ON LAND. Spawned 1 up.
		e := t.spawnTadpole(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned tadpole eid=%d (age=%d) at (%.1f,%.1f,%.1f)", e.id, e.tadpoleAge, p.x, p.y+1, p.z))
		}
	case "zoglin":
		// ZOGLIN (GAP): the TERMINAL undead a hoglin becomes off-nether. INDISCRIMINATELY hostile -- it
		// attacks ANY player OR mob (except other zoglins and creepers) and FLINGS the target upward (the
		// knock-up toss). MAX_HEALTH 40, hits for a 6.0-base roll. Never converts. Spawned 1 block up (adult).
		e := t.spawnZoglin(p.x, p.y+1, p.z, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned zoglin eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y+1, p.z))
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
	case "wandering_trader", "wanderingtrader", "trader":
		// WANDERING TRADER (Task): the roaming merchant (AbstractVillager; MAX_HEALTH 20, MOVEMENT_SPEED 0.7
		// via createMobAttributes). Right-click it to open the MerchantMenu -- its offers (buying + common +
		// uncommon slice of the vanilla wandering_trader trade table) are pre-built. It spawns with 2 trader
		// llamas beside it (the caravan; the leash/follow link is cite-deferred). despawnDelay starts at 0
		// (never auto-despawns unless armed via setDespawnDelay). The night-invisibility drink + the periodic
		// WanderingTraderSpawner are cite-deferred (mob-effect subsystem / no per-world spawn timer).
		e := t.spawnWanderingTrader(p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned wandering_trader eid=%d (hp=%.1f) + 2 trader llamas at (%.1f,%.1f,%.1f)", e.id, e.health, p.x, p.y, p.z))
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
	case "trident":
		// TRIDENT (trident.go): give the issuer a trident so they can right-click to throw a ThrownTrident
		// (power 2.5) and, in water/rain with Riptide, launch themselves. Loyalty returns the thrown trident;
		// Channeling strikes lightning on a hit during a thunderstorm. giveItemToPlayer uses the item's
		// StackSize (1 for a trident) so the trident lands in the first free hotbar/inventory slot.
		t.giveItemToPlayer(p, &item.Trident, int(item.Trident.StackSize), 1)
		t.broadcastSystemChat("[dbg] gave you a trident (right-click to throw; enchant Loyalty/Riptide/Channeling to test the rest)")
	case "throw-trident", "trident-throw":
		// Spawn a ThrownTrident directly from the player eye in the look direction (bypasses the draw), so the
		// projectile flight + hit + despawn can be observed without holding right-click. Survival pickup.
		vx, vy, vz := playerViewVector(p.yaw, p.pitch)
		e := t.spawnThrownTrident(p.entityID, p.x, p.y+playerStandingEyeHeight, p.z, vx*tridentShootPower, vy*tridentShootPower, vz*tridentShootPower, component.SlotData{ItemID: pk.VarInt(item.Trident.ID), Count: 1}, false)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned thrown trident eid=%d (power 2.5 in look dir)", e.id))
		}
	case "crafter":
		// CRAFTER (crafter.go): place a crafter 2 blocks in front (+X) oriented to eject EAST, pre-load its
		// 3x3 grid with the 2-plank vertical stick recipe, then place a redstone_block beside it and pulse
		// crafterNeighborChanged so it auto-crafts on the 4-tick delay -- a stick ItemEntity is ejected out
		// the front. Proves the redstone-driven auto-craft + eject end to end. CITE: CrafterBlock.
		if t.world() == nil {
			t.broadcastSystemChat("[dbg] crafter: no world")
			break
		}
		cx, cy, cz := int(p.x)+2, int(p.y), int(p.z)
		cpos := pk.Position{X: cx, Y: cy, Z: cz}
		state := block.ToStateID[block.Crafter{Orientation: block.EastUp}]
		t.world().SetBlock(cpos, state, dimMinY)
		t.broadcastBlockUpdate(cpos, state)
		c := t.resolveCrafter(cpos, state)
		c.items[0] = component.SlotData{ItemID: pk.VarInt(item.OakPlanks.ID), Count: 1}
		c.items[3] = component.SlotData{ItemID: pk.VarInt(item.OakPlanks.ID), Count: 1}
		if src, ok := block.DefaultStateID["minecraft:redstone_block"]; ok {
			spos := pk.Position{X: cx, Y: cy - 1, Z: cz}
			t.world().SetBlock(spos, src, dimMinY)
			t.broadcastBlockUpdate(spos, src)
		}
		t.crafterNeighborChanged(cpos, state)
		t.broadcastSystemChat(fmt.Sprintf("[dbg] placed a loaded crafter at (%d,%d,%d); it should craft 4 sticks in 4 ticks", cx, cy, cz))
	case "map":
		// MAP (map_item.go/map_use.go): give the issuer an empty map and immediately convert it to a filled
		// map centered on them (EmptyMapItem.use), so the terrain fills + the ClientboundMapItemData packet
		// streams as they hold it. CITE: EmptyMapItem.use + MapItem.inventoryTick.
		inv := ensureInventory(p)
		slot := heldWindowSlot(inv.heldSlot)
		inv.set(slot, component.SlotData{ItemID: pk.VarInt(item.Map.ID), Count: 1})
		t.tryUseEmptyMap(p, inv, inv.get(slot), interactionHandMain)
		t.broadcastSystemChat("[dbg] gave + opened a filled map; hold it to watch the terrain fill in")
	case "loom":
		// LOOM (loom_menu.go): give the issuer a white banner + a red dye + a creeper banner pattern so they
		// can right-click a loom, pick a pattern, and take the layered banner. Exercises the LoomMenu apply.
		inv := ensureInventory(p)
		inv.set(int16(windowMainFirst+0), component.SlotData{ItemID: pk.VarInt(item.WhiteBanner.ID), Count: 1})
		inv.set(int16(windowMainFirst+1), component.SlotData{ItemID: pk.VarInt(item.RedDye.ID), Count: 1})
		inv.set(int16(windowMainFirst+2), component.SlotData{ItemID: pk.VarInt(item.CreeperBannerPattern.ID), Count: 1})
		t.sendContent(p)
		t.broadcastSystemChat("[dbg] gave white_banner + red_dye + creeper_banner_pattern; place them in a loom")
	case "loot":
		// LOOT (chest_loot.go): place a simple_dungeon loot chest 2 blocks in front (at the feet), carrying
		// {LootTable, LootTableSeed} -- opening it rolls the real vanilla loot lazily (unpackLootTable).
		if t.world() != nil {
			pos := pk.Position{X: int(p.x), Y: int(p.y), Z: int(p.z) + 2}
			t.world().SetBlock(pos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle, Waterlogged: false}], dimMinY)
			t.world().SetBlockEntityAt(pos, block.EntityTypes["minecraft:chest"], dbgLootChestNBT("minecraft:chests/simple_dungeon", 0x5EED), dimMinY)
			t.broadcastSystemChat(fmt.Sprintf("[dbg] placed a simple_dungeon loot chest at (%d,%d,%d); open it", pos.X, pos.Y, pos.Z))
		}
	default:
		t.broadcastSystemChat("[dbg] usage: /dbg pig | cow | sheep | chicken | zombie | skeleton | spider | wolf | husk | mooshroom | silverfish | creeper | witch | rabbit | enderman | cat | fox | sulfur_cube | happy_ghast | endermite | turtle | ocelot | pillager | vindicator | evoker | ravager | dragon | iron_golem | villager | villager_farmer | wandering_trader | vex | ghast_hostile | blaze | phantom | shulker | strider | wither_skeleton | wither | hoglin | bee | goat | frog | camel | panda | snow_golem | sniffer | allay | axolotl | parrot | bat | squid | glow_squid | cod | salmon | pufferfish | tropical_fish | dolphin | tadpole | armadillo | breeze | creaking | copper_golem | fangs | water | pig-in-water | raid | rain | redstone | trade | trident | throw-trident | crafter | map | loom | loot")
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

// dbgLootChestNBT builds the bare {LootTable, LootTableSeed} chest-BE compound payload (the 3-byte root
// header stripped) for /dbg loot -- the same shape world/structure createChest emits, so unpackLootTable
// rolls it on first open. CITE ChestBlockEntity loot NBT.
func dbgLootChestNBT(table string, seed int64) nbt.RawMessage {
	doc, err := nbt.Marshal(struct {
		LootTable     string `nbt:"LootTable"`
		LootTableSeed int64  `nbt:"LootTableSeed"`
	}{LootTable: table, LootTableSeed: seed})
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x00}}
	}
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}
}
