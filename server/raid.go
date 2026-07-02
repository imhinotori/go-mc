package server

import (
	"math"

	"github.com/google/uuid"
)

// raid.go — the Raid EVENT lifecycle, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.entity.raid.Raid, temp/cache/26.2-inner.jar, CFR + javap this session).
//
// A Raid is the per-village pillager-raid state machine: it holds the wave schedule (numGroups by
// difficulty), the per-wave RaiderType spawn counts, the raidCooldownTicks countdown between waves,
// the RaidStatus (ONGOING/VICTORY/LOSS/STOPPED), and the ServerBossEvent MODEL (progress/name/visible/
// player set). Raid.tick(level) drives the whole thing: it activates on chunk presence, counts down
// the pre-wave cooldown, spawns each wave when the cooldown hits 0 and no raiders are alive, and
// transitions to VICTORY once the final wave is cleared.
//
// 1:1 ANCHOR (VERIFIED CFR net.minecraft.world.entity.raid.Raid):
//   - numGroups by difficulty: PEACEFUL 0, EASY 3, NORMAL 5, HARD 7 (getNumGroups).
//   - RaiderType.spawnsPerWaveBeforeBonus (1-indexed by wave; index 0 unused, indices 1..7):
//       VINDICATOR {0,0,2,0,1,4,2,5}  EVOKER {0,0,0,0,0,1,1,2}  PILLAGER {0,4,3,3,4,4,4,2}
//       WITCH      {0,0,0,0,3,0,0,1}  RAVAGER {0,0,0,1,0,1,0,2}
//   - raidCooldownTicks starts 300 (DEFAULT_PRE_RAID_TICKS); active raid decrements it while
//     raidersAlive==0 && hasMoreWaves; when it hits 0 (and groupsSpawned>0) it resets to 300 and
//     returns (the between-wave pause). shouldSpawnGroup = cooldown==0 && groupsSpawned<numGroups
//     (or bonus) && raidersAlive==0.
//   - status ONGOING at ctor. On final wave cleared + postRaidTicks>=40 -> VICTORY. ticksActive>=48000
//     (RAID_TIMEOUT_TICKS) -> stop() (STOPPED). PEACEFUL difficulty -> stop().
//   - the boss bar: ServerBossEvent(RED, NOTCHED_10). setProgress during cooldown = clamp((300-cd)/300);
//     updateBossbar during a wave = clamp(healthOfLivingRaiders / totalHealth). Its client packet
//     emission is now BUILT (bossbar.go: ClientboundBossEvent ADD/REMOVE/UPDATE_PROGRESS/UPDATE_NAME).
//     The MODEL (progress/name/visible/players) drives the wire: setProgress/setName broadcast an
//     UPDATE to the tracked players, and updatePlayers (by VALID_RAID_RADIUS_SQR distance) sends
//     ADD/REMOVE as players enter/leave range.
//
// v1 REDUCTIONS (cited, NOT silently dropped):
//   - VILLAGE/POI dependence: Raid.tick's isVillage/moveRaidCenterToNearbyVillageSection center checks
//     are CITE-DEFERRED — there is NO village/POI subsystem (grep PoiManager/village: no PoiManager).
//     The raid runs on its fixed center without the "raid drifted out of a village -> LOSS" guard. The
//     bad-omen->raid AUTO-trigger (Raids.createOrExtendRaid, which reads
//     level.getPoiManager().getInRange(VILLAGE)) is likewise deferred; a raid is instead started via the
//     test/dbg SEAM (raids.go createRaidAt), so the loop is fully EXERCISED and hasActiveRaid becomes real.
//   - the ravager-rider spawns (Pillager/Evoker/Vindicator riding a Ravager) and the leader ominous
//     banner are CITE-DEFERRED with the absent RaiderType mobs (only WITCH exists among the 22).
//   - findRandomSpawnPos's heightmap/village probing is reduced to the raid center (spawnGroup places
//     each raider AT the center) — the observable (a wave of witches spawns near center) is faithful.

// Raid constants (VERIFIED CFR Raid field initializers).
const (
	raidTimeoutTicks    = 48000 // RAID_TIMEOUT_TICKS — ticksActive cap -> stop()
	raidDefaultPreTicks = 300   // DEFAULT_PRE_RAID_TICKS — raidCooldownTicks start/reset between waves
	raidPostRaidLimit   = 40    // POST_RAID_TICK_LIMIT — post-final-wave delay before VICTORY
	raidMaxCelebration  = 600   // MAX_CELEBRATION_TICKS — VICTORY/LOSS boss-bar celebration window
	raidDefaultMaxOmen  = 5     // DEFAULT_MAX_RAID_OMEN_LEVEL
	raidLowMobThreshold = 2     // LOW_MOB_THRESHOLD — "Raiders remaining" bar name switch
)

// raidStatus is Raid$RaidStatus (VERIFIED CFR: ONGOING/VICTORY/LOSS/STOPPED).
type raidStatus int

const (
	raidStatusOngoing raidStatus = iota
	raidStatusVictory
	raidStatusLoss
	raidStatusStopped
)

func (s raidStatus) name() string {
	switch s {
	case raidStatusOngoing:
		return "ongoing"
	case raidStatusVictory:
		return "victory"
	case raidStatusLoss:
		return "loss"
	default:
		return "stopped"
	}
}

// raiderType is Raid$RaiderType (VERIFIED CFR). The ORDINAL is load-bearing (getPotentialBonusSpawns
// switches on it): VINDICATOR=0, EVOKER=1, PILLAGER=2, WITCH=3, RAVAGER=4. Each carries its
// spawnsPerWaveBeforeBonus table (1-indexed by wave). Only WITCH maps to a mob that exists among the
// merged mobs. As of the RAIDER task ALL FIVE RaiderTypes map to a boot-loaded declaration (Vindicator/
// Evoker/Pillager/Witch/Ravager), so raidCreateRaider spawns a REAL raider for each — the waves are live.
type raiderType struct {
	ordinal                  int
	mobName                  string // the vanilla_* declaration name, or "" if the RaiderType has no v1 mob
	spawnsPerWaveBeforeBonus [8]int
}

// raiderTypesValues is Raid$RaiderType.VALUES, in ordinal order (the spawnGroup iteration order).
// VERIFIED CFR RaiderType enum declaration.
var raiderTypesValues = []raiderType{
	{ordinal: 0, mobName: vanillaVindicatorMobName, spawnsPerWaveBeforeBonus: [8]int{0, 0, 2, 0, 1, 4, 2, 5}}, // VINDICATOR
	{ordinal: 1, mobName: vanillaEvokerMobName, spawnsPerWaveBeforeBonus: [8]int{0, 0, 0, 0, 0, 1, 1, 2}},     // EVOKER
	{ordinal: 2, mobName: vanillaPillagerMobName, spawnsPerWaveBeforeBonus: [8]int{0, 4, 3, 3, 4, 4, 4, 2}},   // PILLAGER
	{ordinal: 3, mobName: vanillaWitchMobName, spawnsPerWaveBeforeBonus: [8]int{0, 0, 0, 0, 3, 0, 0, 1}},      // WITCH
	{ordinal: 4, mobName: vanillaRavagerMobName, spawnsPerWaveBeforeBonus: [8]int{0, 0, 0, 1, 0, 1, 0, 2}},    // RAVAGER
}

// difficulty (PEACEFUL/EASY/NORMAL/HARD) + its constants are defined in food.go (the existing
// ServerLevel.getDifficulty() analogue); getNumGroups/getPotentialBonusSpawns reuse them here.

// getNumGroups ports Raid.getNumGroups(Difficulty) — VERIFIED CFR: PEACEFUL 0, EASY 3, NORMAL 5, HARD 7.
func getNumGroups(d difficulty) int {
	switch d {
	case difficultyEasy:
		return 3
	case difficultyNormal:
		return 5
	case difficultyHard:
		return 7
	default:
		return 0 // PEACEFUL
	}
}

// Raid is the ported net.minecraft.world.entity.raid.Raid. Single-owner (TICK-05): created + ticked
// ONLY on the coordinator goroutine (raids.go ticks it at the quiescent barrier), so it needs no lock.
// The groupRaiderMap analogue is a wave->raider-set membership; a raider carries a back-pointer to its
// Raid (mobAI.currentRaid) so hasActiveRaid() is a live query.
type Raid struct {
	id int // the Raids-manager id (raids.go), for identity + logging

	// center is the raid origin (BlockPos analogue). Waves spawn near it; the VALID_RAID_RADIUS_SQR
	// membership check keys off it (updateRaiders). Plain ints (block coords).
	centerX, centerY, centerZ int

	difficulty difficulty
	numGroups  int // getNumGroups(difficulty), cached at ctor (Raid.numGroups)

	status           raidStatus
	started          bool // Raid.started — set true once the first wave spawns
	active           bool // Raid.active — set from level.hasChunkAt(center) each tick
	ticksActive      int64
	raidOmenLevel    int
	groupsSpawned    int
	postRaidTicks    int
	celebrationTicks int
	// raidCooldownTicks is the pre-wave countdown (starts 300). While it is >0 and no raiders are
	// alive, tick decrements it and drives the boss-bar progress; at 0 the next wave spawns.
	raidCooldownTicks int
	totalHealth       float32 // sum of the current wave's raiders' max health (for the boss bar)

	// groupRaiderMap is Raid.groupRaiderMap: wave -> the set of live raiders spawned in that wave.
	// A raider is removed from its set on death/despawn (removeFromRaid). getTotalRaidersAlive sums
	// the set sizes. Keyed by the 1-based wave (groupNumber).
	groupRaiderMap map[int]map[int32]*Entity

	// bossEvent is the ServerBossEvent MODEL (progress/name/visible/players). Its client packet
	// emission is cite-deferred (no BossEvent wire); the model is computed faithfully.
	bossEvent serverBossEvent

	// rng is the raid's own RandomSource (Raid.random = RandomSource.create()) — drawn by
	// getPotentialBonusSpawns (the bonus-spawn nextInt) and the wave sound seed. Deterministic per
	// raid id here (the raid stream is NOT vanilla-seed-pinned; only the draw ORDER is observable).
	rng *entityRandom

	// heroesOfTheVillage is Raid.heroesOfTheVillage (a Set<UUID>): the players who slew a raid captain,
	// persisted in Raid.MAP_CODEC as "heroes_of_the_village" (UUIDUtil.CODEC_SET). It is populated by
	// the (cite-deferred) hero-of-the-village effect grant; carried here so the SavedData round-trips
	// the set faithfully (raid_persist.go). Empty for a raid that has awarded no heroes yet.
	heroesOfTheVillage []uuid.UUID
}

// serverBossEvent is the MODEL half of net.minecraft.server.level.ServerBossEvent: the fields a raid
// mutates (progress/name/visible) and the player set it tracks. The client packet emission is
// cite-deferred (no ClientboundBossEventPacket in net/packet — grep: zero hits), so this holds the
// state a future BossEvent wire would serialize. Cite ServerBossEvent(RED, NOTCHED_10).
type serverBossEvent struct {
	// id is the BossEvent id (BossEvent.id — Mth.createInsecureUUID(this.random)). It keys every
	// ADD/REMOVE/UPDATE_* packet the bar sends. Generated once at raid creation; stable per bar.
	id uuid.UUID

	name     string
	progress float32
	visible  bool

	// color/overlay are BossEvent.color/overlay (RED, NOTCHED_10 for a raid). They ride the ADD
	// payload and (were setColor/setOverlay ever called) an UPDATE_STYLE; the raid never changes them.
	color   bossBarColor
	overlay bossBarOverlay

	// darkenScreen/playBossMusic/createWorldFog are BossEvent's three property flags. All false for a
	// raid (the vanilla default — Raid never sets them), carried in the ADD/UPDATE_PROPERTIES flags byte.
	darkenScreen   bool
	playBossMusic  bool
	createWorldFog bool

	// players is the set of player entity ids currently shown the bar (ServerBossEvent.players, driven
	// by updatePlayers). A thin id set (never live pointers — the Folia rule). addPlayer sends ADD to a
	// newly-tracked player; removePlayer sends REMOVE.
	players map[int32]struct{}
}

// newRaid ports the Raid(BlockPos, Difficulty) ctor (VERIFIED CFR): active=true, raidCooldownTicks=300,
// boss bar RED/NOTCHED_10 progress 0, numGroups=getNumGroups(difficulty), status=ONGOING.
func newRaid(id int, cx, cy, cz int, d difficulty, seed uint64) *Raid {
	return &Raid{
		id:                id,
		centerX:           cx,
		centerY:           cy,
		centerZ:           cz,
		difficulty:        d,
		numGroups:         getNumGroups(d),
		status:            raidStatusOngoing,
		active:            true,
		raidCooldownTicks: raidDefaultPreTicks,
		groupRaiderMap:    map[int]map[int32]*Entity{},
		bossEvent: serverBossEvent{
			// id is Mth.createInsecureUUID(this.random) — a per-raid insecure UUID. Sourced from a
			// google/uuid random UUID here (the id need only be STABLE per bar, not vanilla-seed-pinned;
			// only the ADD/REMOVE/UPDATE key identity is observable).
			id:       uuid.New(),
			name:     "event.minecraft.raid",
			progress: 0.0,
			visible:  true,
			color:    bossBarColorRed,         // ServerBossEvent(..., RED, ...)
			overlay:  bossBarOverlayNotched10, // ServerBossEvent(..., NOTCHED_10)
			players:  map[int32]struct{}{},
		},
		rng: newEntityRandom(seed),
	}
}

// --- status predicates (VERIFIED CFR Raid.isOver/isVictory/isLoss/isStopped/isActive) ---

func (r *Raid) isStopped() bool { return r.status == raidStatusStopped }
func (r *Raid) isVictory() bool { return r.status == raidStatusVictory }
func (r *Raid) isLoss() bool    { return r.status == raidStatusLoss }
func (r *Raid) isOver() bool    { return r.isVictory() || r.isLoss() }
func (r *Raid) isActive() bool  { return r.active }
func (r *Raid) isStarted() bool { return r.started }

// getRaidOmenLevel ports Raid.getRaidOmenLevel.
func (r *Raid) getRaidOmenLevel() int { return r.raidOmenLevel }

// getMaxRaidOmenLevel ports Raid.getMaxRaidOmenLevel (VERIFIED CFR: returns 5 == DEFAULT_MAX_RAID_OMEN_LEVEL).
func (r *Raid) getMaxRaidOmenLevel() int { return raidDefaultMaxOmen }

// absorbRaidOmen ports Raid.absorbRaidOmen(ServerPlayer): read the player's RAID_OMEN effect amplifier,
// add (amp+1) to raidOmenLevel, clamp to [0, maxRaidOmenLevel]. Returns false if the player has no
// raid-omen effect. VERIFIED CFR Raid.absorbRaidOmen. (awardStat(RAID_TRIGGER) + CriteriaTriggers.RAID_OMEN
// are cite-deferred — no stats/advancement subsystem; the omen-level math is faithful.)
func (r *Raid) absorbRaidOmen(p *tickPlayer) bool {
	eff := p.activeEffects[effectRaidOmen]
	if eff == nil {
		return false
	}
	r.raidOmenLevel += eff.amplifier + 1
	if r.raidOmenLevel < 0 {
		r.raidOmenLevel = 0
	}
	if r.raidOmenLevel > r.getMaxRaidOmenLevel() {
		r.raidOmenLevel = r.getMaxRaidOmenLevel()
	}
	return true
}

// getTotalRaidersAlive ports Raid.getTotalRaidersAlive: sum of the per-wave raider-set sizes.
func (r *Raid) getTotalRaidersAlive() int {
	total := 0
	for _, set := range r.groupRaiderMap {
		total += len(set)
	}
	return total
}

// hasFirstWaveSpawned ports Raid.hasFirstWaveSpawned: groupsSpawned > 0.
func (r *Raid) hasFirstWaveSpawned() bool { return r.groupsSpawned > 0 }

// isFinalWave ports Raid.isFinalWave: groupsSpawned == numGroups.
func (r *Raid) isFinalWave() bool { return r.groupsSpawned == r.numGroups }

// hasBonusWave ports Raid.hasBonusWave: raidOmenLevel > 1.
func (r *Raid) hasBonusWave() bool { return r.raidOmenLevel > 1 }

// hasSpawnedBonusWave ports Raid.hasSpawnedBonusWave: groupsSpawned > numGroups.
func (r *Raid) hasSpawnedBonusWave() bool { return r.groupsSpawned > r.numGroups }

// hasMoreWaves ports Raid.hasMoreWaves: if a bonus wave is due, !hasSpawnedBonusWave; else !isFinalWave.
func (r *Raid) hasMoreWaves() bool {
	if r.hasBonusWave() {
		return !r.hasSpawnedBonusWave()
	}
	return !r.isFinalWave()
}

// shouldSpawnBonusGroup ports Raid.shouldSpawnBonusGroup: isFinalWave && raidersAlive==0 && hasBonusWave.
func (r *Raid) shouldSpawnBonusGroup() bool {
	return r.isFinalWave() && r.getTotalRaidersAlive() == 0 && r.hasBonusWave()
}

// shouldSpawnGroup ports Raid.shouldSpawnGroup: cooldown==0 && (groupsSpawned<numGroups || bonus) &&
// raidersAlive==0.
func (r *Raid) shouldSpawnGroup() bool {
	return r.raidCooldownTicks == 0 &&
		(r.groupsSpawned < r.numGroups || r.shouldSpawnBonusGroup()) &&
		r.getTotalRaidersAlive() == 0
}

// getHealthOfLivingRaiders ports Raid.getHealthOfLivingRaiders: sum of every live raider's health.
func (r *Raid) getHealthOfLivingRaiders() float32 {
	var h float32
	for _, set := range r.groupRaiderMap {
		for _, raider := range set {
			h += raider.health
		}
	}
	return h
}

// updateBossbar ports Raid.updateBossbar (VERIFIED CFR): raidEvent.setProgress(clamp(healthOf
// LivingRaiders / totalHealth, 0, 1)). setProgress broadcasts UPDATE_PROGRESS on change, so this is a
// *TickLoop method now (it emits the wire). The totalHealth<=0 guard is a defensive port of the
// vanilla 0/0 -> NaN division (Mth.clamp(NaN) is undefined); progress 0 is the observable pre-wave value.
func (t *TickLoop) updateBossbar(r *Raid) {
	if r.totalHealth <= 0 {
		t.bossSetProgress(r, 0)
		return
	}
	t.bossSetProgress(r, clampF32(r.getHealthOfLivingRaiders()/r.totalHealth, 0, 1))
}

// raidStop ports Raid.stop (VERIFIED CFR): active=false; raidEvent.removeAllPlayers() (sends REMOVE to
// every tracked player); status=STOPPED. It is a *TickLoop method now because removeAllPlayers emits
// the wire.
//
//	[VERIFIED CFR Raid.stop: this.active = false; this.raidEvent.removeAllPlayers(); this.status = STOPPED.]
func (t *TickLoop) raidStop(r *Raid) {
	r.active = false
	t.bossRemoveAllPlayers(r)
	r.status = raidStatusStopped
}

// getDefaultNumSpawns ports Raid.getDefaultNumSpawns: isBonusWave ? table[numGroups] : table[wav].
func (r *Raid) getDefaultNumSpawns(rt raiderType, wav int, isBonusWave bool) int {
	if isBonusWave {
		return rt.spawnsPerWaveBeforeBonus[r.numGroups]
	}
	return rt.spawnsPerWaveBeforeBonus[wav]
}

// getPotentialBonusSpawns ports Raid.getPotentialBonusSpawns (VERIFIED CFR switch on RaiderType.ordinal
// + DifficultyInstance). The DRAWS (random.nextInt) are on the raid's own stream (r.rng), draw-order-
// faithful. The final bonusSpawns>0 ? random.nextInt(bonusSpawns+1) : 0 is the observable tail draw.
func (r *Raid) getPotentialBonusSpawns(rt raiderType, wav int, d difficulty, isBonusWave bool) int {
	isEasy := d == difficultyEasy
	isNormal := d == difficultyNormal
	var bonusSpawns int
	switch rt.ordinal {
	case 3: // WITCH
		if !isEasy && wav > 2 && wav != 4 {
			bonusSpawns = 1
		} else {
			return 0
		}
	case 0, 2: // VINDICATOR, PILLAGER
		switch {
		case isEasy:
			bonusSpawns = r.rng.nextInt(2)
		case isNormal:
			bonusSpawns = 1
		default:
			bonusSpawns = 2
		}
	case 4: // RAVAGER
		if !isEasy && isBonusWave {
			bonusSpawns = 1
		} else {
			bonusSpawns = 0
		}
	default: // EVOKER (ordinal 1) + anything else
		return 0
	}
	if bonusSpawns > 0 {
		return r.rng.nextInt(bonusSpawns + 1)
	}
	return 0
}

// clampF32 (Mth.clamp for float32) is defined in tick_phases.go; the boss-bar progress reuses it.

// raidFloor is math.Floor returning an int (BlockPos.containing coord conversion) — negative-safe. Used
// by the patrol goal + raider positioning where a float world coord becomes a block coord.
func raidFloor(v float64) int { return int(math.Floor(v)) }
