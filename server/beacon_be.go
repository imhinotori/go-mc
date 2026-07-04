package server

// beacon_be.go — the BEACON BLOCK-ENTITY (BEACON-01): a 1:1 port of
// net.minecraft.world.level.block.entity.BeaconBlockEntity.tick / updateBase / applyEffects over the 26.2
// jar (temp/cache/26.2-inner.jar, CFR/javap this session). The beacon is a per-tick DRIVE (the furnace/hopper
// twin, t.beacons keyed by world position) that (a) incrementally scans the beam column above itself to learn
// whether it is unobstructed to the sky, (b) every 80 game-ticks recomputes its pyramid LEVEL from the base
// blocks below and, when active, APPLIES its selected primary (+ level-4 secondary) mob effect to every player
// in range. The effect application is the load-bearing gameplay half; the beam VISUAL (color sections /
// ClientboundBlockEntityData beam render + stained-glass tint) is cite-deferred (client cosmetic).
//
// 1:1 jar (net.minecraft.world.level.block.entity.BeaconBlockEntity):
//
//	static final int MAX_LEVELS = 4;                       // pyramid layers 1..4
//	static final int LEVELS_NEEDED_FOR_SECONDARY = 4;      // secondary effect requires level 4
//	static final int BLOCKS_CHECK_PER_TICK = 10;           // beam scan increments 10 blocks/tick
//	BEACON_EFFECTS = [[SPEED,HASTE],[RESISTANCE,JUMP_BOOST],[STRENGTH],[REGENERATION]];
//
//	tick(level, pos, state, entity):
//	   // incremental beam scan from lastCheckY up to getHeight(WORLD_SURFACE,x,z), 10 blocks/tick, building
//	   // checkingBeamSections; a non-beam block with lightDampening>=15 (and not bedrock) obstructs it and
//	   // clears checkingBeamSections. The beacon block ITSELF is a BeaconBeamBlock (getColor()=WHITE), so it
//	   // seeds the first section — an unobstructed column above the beacon yields a non-empty beamSections.
//	   if (level.getGameTime() % 80L == 0) {
//	       if (!beamSections.isEmpty()) levels = updateBase(level, x, y, z);
//	       if (levels > 0 && !beamSections.isEmpty()) { applyEffects(...); playSound(BEACON_AMBIENT); }
//	   }
//	   if (lastCheckY >= lastSetBlock) { beamSections = checkingBeamSections; ... activate/deactivate sounds }
//
// v1 model of beamSections: the beam COLOR sections are the visual (cite-deferred). What is load-bearing is
// the OBSTRUCTION gate `!beamSections.isEmpty()`. Since the beacon block always seeds a section, beamSections
// is non-empty IFF the column above is unobstructed to the surface. We therefore model it with a boolean
// beamClear (committed from checkClear when the scan reaches the surface), which is the exact
// !beamSections.isEmpty() answer. The incremental lastCheckY state machine + 10-blocks/tick cadence are
// preserved so the activation timing matches vanilla tick-for-tick.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Beacon constants (BeaconBlockEntity static fields, VERIFIED CFR).
const (
	beaconMaxLevels            = 4  // MAX_LEVELS
	beaconLevelsForSecondary   = 4  // LEVELS_NEEDED_FOR_SECONDARY
	beaconBlocksCheckPerTick   = 10 // BLOCKS_CHECK_PER_TICK
	beaconApplyCadenceTicks    = 80 // level.getGameTime() % 80L == 0 (applyEffects + level recompute cadence)
	beaconLightDampeningOpaque = 15 // getLightDampening() >= 15 obstructs the beam (unless bedrock)
)

// beaconBaseBlocksTag is the bare name of net.minecraft.tags.BlockTags.BEACON_BASE_BLOCKS
// (BlockTags.create("beacon_base_blocks")). updateBase tests each pyramid-layer block against it. The tag
// resolves to {iron_block, gold_block, emerald_block, diamond_block, netherite_block}. CITE updateBase.
const beaconBaseBlocksTag = "beacon_base_blocks"

// beaconBE is the tick-owned state of one beacon block-entity — the Go analogue of BeaconBlockEntity narrowed
// to the fields the serverTick + the menu read/write. The beam COLOR sections are cite-deferred (visual), so
// the beam state is reduced to the load-bearing obstruction booleans.
type beaconBE struct {
	levels    int // BeaconBlockEntity.levels (0..4) — the pyramid level (recomputed every 80 ticks)
	lastCheckY int // BeaconBlockEntity.lastCheckY — the incremental beam-scan cursor (set to minY-1 on load)

	// beamClear is the committed !beamSections.isEmpty() answer: true when the last full column scan reached
	// the surface UNOBSTRUCTED. The 80-tick applyEffects gate reads this (a beacon with a solid block over it
	// applies no effect). Committed from checkingClear when the scan reaches the surface.
	beamClear bool
	// checkingClear is the in-progress scan's obstruction verdict (the checkingBeamSections analogue): true
	// until the current scan hits an obstructing block (which clears it) — committed into beamClear at surface.
	checkingClear bool

	// primaryPower / secondaryPower are BeaconBlockEntity.primaryPower / secondaryPower: the selected effect
	// ids (empty string == null). Set by the BeaconMenu SetBeacon path (beacon_menu.go), filtered to the valid
	// effect set. applyEffects reads them.
	primaryPower   string
	secondaryPower string

	// payment is the BeaconMenu payment slot (PaymentSlot, slot 0): the single ingot the player "pays" to
	// select an effect. Consumed (remove 1) by a successful SetBeacon. Belongs to the menu, not the beacon's
	// long-lived state (vanilla's PaymentSlot backs a transient 1-slot Container, dropped on close), but is
	// held on the beaconBE so the open menu + the SetBeacon handler share it. Empty (Count 0) when unpaid.
	payment component.SlotData

	lastCheckYSet bool // whether lastCheckY was seeded (setLevel: lastCheckY = minY-1) — false forces a seed
}

// beaconWorldSurfaceTop is Level.getHeight(Heightmap.Types.WORLD_SURFACE, x, z): the Y of the first air block
// above the topmost non-air block in the column (WORLD_SURFACE predicate == NOT_AIR). The beam scan loop uses
// it as the upper bound (`checkPos.getY() <= lastSetBlock`). v1 has no live heightmap, so it scans the column
// DOWN from the build ceiling for the highest non-air block, matching getHeight(WORLD_SURFACE) = that Y + 1.
// CITE Heightmap.Types.WORLD_SURFACE (Usage.CLIENT, NOT_AIR).
func (t *TickLoop) beaconWorldSurfaceTop(x, z int) int {
	if t.world() == nil {
		return dimMinY
	}
	const columnTopY = dimMinY + 384 // -64 + 384 == 320 (overworld build ceiling; the highest possible block Y is 319)
	for cy := columnTopY; cy >= dimMinY; cy-- {
		s, ok := t.world().GetBlock(pk.Position{X: x, Y: cy, Z: z}, dimMinY)
		if !ok {
			continue
		}
		if !block.IsAir(s) {
			return cy + 1 // getHeight(WORLD_SURFACE) = highest non-air + 1 (the first air cell above the surface)
		}
	}
	return dimMinY
}

// beaconBeamObstructs ports the beam-scan obstruction test for a NON-beam block: the beam continues through a
// block whose `getLightDampening() < 15 || is(BEDROCK)` and is OBSTRUCTED (clears checkingBeamSections)
// otherwise. v1 has no light engine, so getLightDampening()>=15 is proxied by BlockState.isSuffocating (an
// opaque full-cube — the same proxy growth_block uses for getLightDampeningInto). Air/glass/leaves =
// transparent (suffocating=false); stone/dirt = opaque (suffocating=true); bedrock is explicitly transparent.
// CITE BeaconBlockEntity.tick beam branch `state.getLightDampening() < 15 || state.is(Blocks.BEDROCK)`.
//
//	[DEFERRED: the numeric getLightDampening read — no light engine in v1. isSuffocating is the faithful
//	 opaque-full-cube proxy for >=15 (glass/leaves report <15 correctly via their isSuffocating override).]
func (t *TickLoop) beaconBeamObstructs(s block.StateID) bool {
	if s == block.DefaultStateID["minecraft:bedrock"] {
		return false // state.is(Blocks.BEDROCK) -> transparent to the beam (never obstructs)
	}
	return t.isSuffocating(s) // getLightDampening() >= 15 proxy (opaque full cube)
}

// isBeaconBeamBlock reports whether a block state is a BeaconBeamBlock — the interface stained-glass panes,
// stained glass, tinted glass and the BEACON BLOCK ITSELF implement (BeaconBlock implements BeaconBeamBlock,
// getColor()=WHITE). The beacon's own position seeds the first beam section. v1 tracks only the beacon itself
// as the beam-block seed (the stained-glass color tint is cite-deferred visual), so a glass block in the
// column is treated as a transparent non-beam block (lightDampening 0 -> passes) — the OBSTRUCTION answer is
// identical (glass never obstructs), only the beam COLOR (deferred) would differ. CITE BeaconBeamBlock.
func isBeaconBeamBlock(s block.StateID, beaconX, beaconY, beaconZ int, checkX, checkY, checkZ int) bool {
	// The beacon block itself (the scan's first position) is the beam-block seed.
	return checkX == beaconX && checkY == beaconY && checkZ == beaconZ
}

// beaconServerTick ports BeaconBlockEntity.tick(level, pos, state, entity) EXACTLY: the incremental beam-column
// scan (10 blocks/tick, obstruction gate), the every-80-tick pyramid-level recompute (updateBase) + effect
// application (applyEffects), and the commit of the scan result at the surface. Runs on the tick goroutine.
//
// 1:1 net.minecraft.world.level.block.entity.BeaconBlockEntity.tick
func (t *TickLoop) beaconServerTick(pos pk.Position, b *beaconBE) {
	if t.world() == nil {
		return
	}
	x, y, z := pos.X, pos.Y, pos.Z

	// setLevel(level): lastCheckY = level.getMinY() - 1. v1 seeds it lazily on the first tick.
	if !b.lastCheckYSet {
		b.lastCheckY = dimMinY - 1
		b.lastCheckYSet = true
	}

	// if (entity.lastCheckY < y) { checkPos = pos; checkingBeamSections = new ArrayList(); lastCheckY = y-1; }
	// else { checkPos = new BlockPos(x, lastCheckY + 1, z); }
	var checkY int
	if b.lastCheckY < y {
		checkY = y
		b.checkingClear = true // fresh scan: unobstructed until proven otherwise (empty checkingBeamSections)
		b.lastCheckY = y - 1
		// The beacon block itself (checkPos == pos) is the beam-block seed (BeaconBlock implements
		// BeaconBeamBlock). lastBeamSection starts null, so seeding it here mirrors the size()<=1 add branch.
	} else {
		checkY = b.lastCheckY + 1
	}

	// int lastSetBlock = level.getHeight(WORLD_SURFACE, x, z);
	lastSetBlock := t.beaconWorldSurfaceTop(x, z)

	// for (int i = 0; i < 10 && checkPos.getY() <= lastSetBlock; ++i) { ... }
	for i := 0; i < beaconBlocksCheckPerTick && checkY <= lastSetBlock; i++ {
		s, _ := t.world().GetBlock(pk.Position{X: x, Y: checkY, Z: z}, dimMinY)
		if isBeaconBeamBlock(s, x, y, z, x, checkY, z) {
			// beam block: extends/starts a beam section (the visual). The obstruction verdict is unchanged
			// (a beam block never obstructs). checkingClear stays true.
		} else {
			// non-beam block: continue only if it does NOT obstruct AND we already have a beam section
			// (lastBeamSection != null). The beacon block at y seeds the section on i==0, so from i>=1 the
			// section exists. If it obstructs (or the section is absent), clear + break.
			hasSection := b.checkingClear && checkY > y // a section exists once past the seed row (beacon at y)
			if hasSection && !t.beaconBeamObstructs(s) {
				// increaseHeight() — beam passes; keep scanning.
			} else {
				// checkingBeamSections.clear(); lastCheckY = lastSetBlock; break;
				b.checkingClear = false
				b.lastCheckY = lastSetBlock
				break
			}
		}
		checkY++
		b.lastCheckY++
	}

	// int previousLevels = entity.levels;
	// if (level.getGameTime() % 80L == 0L) { ... }
	if t.GameTime()%int64(beaconApplyCadenceTicks) == 0 {
		// if (!beamSections.isEmpty()) levels = updateBase(level, x, y, z);
		if b.beamClear {
			b.levels = t.beaconUpdateBase(x, y, z)
		}
		// if (levels > 0 && !beamSections.isEmpty()) { applyEffects(...); playSound(BEACON_AMBIENT); }
		if b.levels > 0 && b.beamClear {
			t.beaconApplyEffects(pos, b.levels, b.primaryPower, b.secondaryPower)
			// playSound(BEACON_AMBIENT): cite-deferred (no sound-event emit seam for BE ambient in v1).
		}
	}

	// if (entity.lastCheckY >= lastSetBlock) { lastCheckY = minY-1; beamSections = checkingBeamSections; ... }
	if b.lastCheckY >= lastSetBlock {
		b.lastCheckY = dimMinY - 1
		// entity.beamSections = entity.checkingBeamSections — commit the scan verdict.
		b.beamClear = b.checkingClear
		// The activate/deactivate BEACON sounds + the CONSTRUCT_BEACON advancement trigger are cite-deferred
		// (no sound-event / advancement emit seam in v1; the effect application is the observable target).
	}
}

// beaconUpdateBase ports BeaconBlockEntity.updateBase(level, x, y, z): count the pyramid levels below the
// beacon. For each layer step 1..4 at y-step, the (2*step+1)² ring [x-step..x+step]×[z-step..z+step] must be
// entirely #beacon_base_blocks; the highest contiguous such layer sets levels. A gap breaks the count.
//
// 1:1 net.minecraft.world.level.block.entity.BeaconBlockEntity.updateBase
func (t *TickLoop) beaconUpdateBase(x, y, z int) int {
	levels := 0
	for step := 1; step <= beaconMaxLevels; step++ {
		ly := y - step
		if ly < dimMinY { // ly >= level.getMinY()
			break
		}
		isOk := true
		for lx := x - step; lx <= x+step && isOk; lx++ {
			for lz := z - step; lz <= z+step; lz++ {
				s, ok := t.world().GetBlock(pk.Position{X: lx, Y: ly, Z: lz}, dimMinY)
				if ok && blockInTag(s, beaconBaseBlocksTag) {
					continue
				}
				isOk = false
				break
			}
		}
		if !isOk {
			break
		}
		levels = step
	}
	return levels
}

// beaconApplyEffects ports BeaconBlockEntity.applyEffects(level, pos, beaconLevel, primaryPower,
// secondaryPower): if a primary is selected, apply it (and, at level 4, an upgraded amplifier or the distinct
// secondary) to every Player whose hitbox intersects the beacon's range AABB.
//
//	double range = beaconLevel * 10 + 10;
//	int amp = 0; if (beaconLevel >= 4 && primary == secondary) amp = 1;
//	int duration = (9 + beaconLevel * 2) * 20;
//	AABB bb = new AABB(pos).inflate(range).expandTowards(0, level.getHeight(), 0);
//	for (Player p : level.getEntitiesOfClass(Player.class, bb)) p.addEffect(new MobEffectInstance(primary, duration, amp, true, true));
//	if (beaconLevel >= 4 && primary != secondary && secondary != null)
//	    for (Player p : players) p.addEffect(new MobEffectInstance(secondary, duration, 0, true, true));
//
// 1:1 net.minecraft.world.level.block.entity.BeaconBlockEntity.applyEffects
func (t *TickLoop) beaconApplyEffects(pos pk.Position, beaconLevel int, primaryPower, secondaryPower string) {
	if primaryPower == "" {
		return // primaryPower == null -> nothing to apply
	}
	rng := float64(beaconLevel*10 + 10) // radius = beaconLevel*10 + 10

	baseAmp := 0
	if beaconLevel >= beaconLevelsForSecondary && primaryPower == secondaryPower {
		baseAmp = 1 // level 4 with primary==secondary upgrades the amplifier (a "level II" effect)
	}
	durationTicks := (9 + beaconLevel*2) * 20 // (9 + level*2) * 20

	// AABB(pos).inflate(range).expandTowards(0, getHeight(), 0): the 1-block beacon cube inflated by range on
	// all faces, then extended UP by the world height (so the whole vertical column above is covered).
	// getHeight() == the build height (dimMinY..dimMinY+384) span; expandTowards(0, +h, 0) raises maxY by h.
	minX := float64(pos.X) - rng
	minY := float64(pos.Y) - rng
	minZ := float64(pos.Z) - rng
	maxX := float64(pos.X) + 1 + rng
	maxY := float64(pos.Y) + 1 + rng + beaconWorldHeight // expandTowards(0, getHeight(), 0)
	maxZ := float64(pos.Z) + 1 + rng

	players := t.beaconPlayersInAABB(minX, minY, minZ, maxX, maxY, maxZ)
	for _, p := range players {
		// player.addEffect(new MobEffectInstance(primary, duration, amp, ambient=true, showParticles=true)).
		t.addPlayerEffect(p, 0, primaryPower, durationTicks, baseAmp, 1.0)
	}
	if beaconLevel >= beaconLevelsForSecondary && primaryPower != secondaryPower && secondaryPower != "" {
		for _, p := range players {
			t.addPlayerEffect(p, 0, secondaryPower, durationTicks, 0, 1.0)
		}
	}
}

// beaconWorldHeight is Level.getHeight() — the number of buildable Y levels (LevelHeightAccessor.getHeight()).
// Overworld == 384 (from getMinY()=-64 up 384 to the build ceiling). Used by applyEffects' expandTowards to
// cover the full vertical column above the beacon. CITE LevelHeightAccessor.getHeight.
const beaconWorldHeight = 384.0

// beaconPlayersInAABB ports Level.getEntitiesOfClass(Player.class, aabb): every player whose collision hitbox
// (width 0.6, height 1.8, feet at p.y) INTERSECTS the given AABB. AABB.intersects tests overlap on all three
// axes. Tick-owned (reads the tick-owned player table). CITE getEntitiesOfClass + AABB.intersects.
func (t *TickLoop) beaconPlayersInAABB(minX, minY, minZ, maxX, maxY, maxZ float64) []*tickPlayer {
	var out []*tickPlayer
	const halfW = playerWidth / 2 // 0.3
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		// player hitbox: [x-0.3, x+0.3] × [y, y+1.8] × [z-0.3, z+0.3].
		pMinX, pMaxX := p.x-halfW, p.x+halfW
		pMinY, pMaxY := p.y, p.y+playerHeight
		pMinZ, pMaxZ := p.z-halfW, p.z+halfW
		// AABB.intersects: overlap on all three axes (this.minX < other.maxX && this.maxX > other.minX && ...).
		if minX < pMaxX && maxX > pMinX && minY < pMaxY && maxY > pMinY && minZ < pMaxZ && maxZ > pMinZ {
			out = append(out, p)
		}
	}
	return out
}

// tickBeacons ticks every live beacon block-entity once per tick (the ServerLevel-side blockEntityTicker
// fan-out for BeaconBlockEntity.tick). Called from tickWorld (the tickFurnaces/tickHoppers twin). Each beacon
// reads the block at its position; if it is no longer a beacon (broken/replaced/unloaded), the block-entity is
// dropped from the store (its ticker is removed in vanilla). A nil world leaves beacons un-ticked (tests may
// drive beaconServerTick directly). Tick-owned (TICK-05).
func (t *TickLoop) tickBeacons() {
	if len(t.beacons) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, b := range t.beacons {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !isBeaconBlock(state) {
			delete(t.beacons, pos)
			continue
		}
		t.beaconServerTick(pos, b)
	}
}
