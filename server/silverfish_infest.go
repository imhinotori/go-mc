package server

// silverfish_infest.go — MOB-HOST-05 (infest goals): the Silverfish stone-infestation subsystem, a 1:1
// port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p / CFR this session):
//
//   - net.minecraft.world.level.block.InfestedBlock: the host<->infested block mapping
//     (BLOCK_BY_HOST_BLOCK, populated by the InfestedBlock/InfestedRotatedPillarBlock constructors in
//     Blocks), isCompatibleHostBlock, infestedStateByHost, hostStateByInfested, and spawnInfestation.
//   - net.minecraft.world.entity.monster.Silverfish$SilverfishMergeWithStoneGoal: canUse RNG gate +
//     the stone->infested conversion in start() (see silverfishMergeStoneGoal below).
//   - net.minecraft.world.entity.monster.Silverfish$SilverfishWakeUpFriendsGoal: the notifyHurt-armed
//     spiral search that de-infests nearby InfestedBlocks (summoning silverfish) when hurt (see
//     silverfishWakeFriendsGoal below).
//
// v1 STUBS / DEFERRALS (cited, NEVER silently dropped):
//   - The host<->infested mapping is a STATIC StateID table (silverfishHostToInfested / …Infested…) at
//     defaultBlockState() granularity — the 7 pairs Blocks registers (stone, cobblestone, stone_bricks,
//     mossy/cracked/chiseled stone bricks, deepslate). InfestedBlock.infestedStateByHost/hostStateByInfested
//     copy SHARED block properties (getNewStateWithProperties + copyProperty); the only host with a
//     non-trivial property is deepslate (axis, InfestedRotatedPillarBlock). The axis-copy is DEFERRED —
//     the table maps to the DEFAULT (axis=y) state — because the codegen block data is consumed at
//     StateID granularity here and the merge goal only ever converts a mob-adjacent block (an
//     axis-preserving deepslate conversion is a follow-up when a property-copy block API lands). Every
//     other pair has a single state, so the table is exact for them. Cite InfestedBlock + Blocks.
//   - SilverfishWakeUpFriendsGoal.tick's mobGriefing branch uses Level.destroyBlock(pos, true, silverfish)
//     which routes through InfestedBlock.spawnAfterBreak -> spawnInfestation (the loot-drop + silverfish
//     summon). v1 has no generic Level.destroyBlock/spawnAfterBreak-on-break pipeline, so
//     silverfishDestroyInfestedBlock sets the block to AIR and directly calls spawnInfestation (the
//     gameplay-observable summon). The BLOCK-DROP loot is DEFERRED (the break yields no item), cited
//     here — spawnAfterBreak's BLOCK_DROPS/PREVENTS_INFESTED_SPAWNS guards collapse to "always summon"
//     (mobGriefing default true, no enchantment subsystem). Cite InfestedBlock.spawnAfterBreak/spawnInfestation.
//   - GameRules.MOB_GRIEFING is not a v1 subsystem: it is a CITED CONSTANT == the vanilla default (true),
//     silverfishMobGriefing below, structured as a real read for a future gamerule subsystem.

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// silverfishMobGriefing is the CITED-CONSTANT stand-in for GameRules.MOB_GRIEFING (no gamerule
// subsystem in v1) == the vanilla default TRUE. Both SilverfishMergeWithStoneGoal.canUse and
// SilverfishWakeUpFriendsGoal.tick read it; structured as a real bool so a future gamerule subsystem
// swaps the source without touching the call sites. Cite GameRules.MOB_GRIEFING (default true).
const silverfishMobGriefing = true

// silverfishInfestPair is one host<->infested block registration (a BLOCK_BY_HOST_BLOCK entry). The
// StateIDs are the block's defaultBlockState() ids (level/block DefaultStateID, codegen-authoritative),
// so the table is the StateID-granularity image of InfestedBlock's HOST_TO_INFESTED_STATES /
// INFESTED_TO_HOST_STATES for the (property-free) hosts, plus the DEFAULT (axis=y) image for deepslate.
type silverfishInfestPair struct {
	host     block.StateID // the compatible host block's default state (e.g. minecraft:stone)
	infested block.StateID // its InfestedBlock's default state  (e.g. minecraft:infested_stone)
}

// silverfishInfestPairs is the 7 InfestedBlock registrations Blocks makes (javap/CFR Blocks this
// session): stone, cobblestone, stone_bricks, mossy_stone_bricks, cracked_stone_bricks,
// chiseled_stone_bricks (InfestedBlock) + deepslate (InfestedRotatedPillarBlock). The StateID pairs are
// the level/block DefaultStateID values (verified against level/block/blocks.go this session). Cite
// Blocks.INFESTED_* + InfestedBlock's ctor (BLOCK_BY_HOST_BLOCK.put(hostBlock, this)).
var silverfishInfestPairs = [...]silverfishInfestPair{
	{host: 1, infested: 7760},      // minecraft:stone                 -> minecraft:infested_stone
	{host: 14, infested: 7761},     // minecraft:cobblestone           -> minecraft:infested_cobblestone
	{host: 7754, infested: 7762},   // minecraft:stone_bricks          -> minecraft:infested_stone_bricks
	{host: 7755, infested: 7763},   // minecraft:mossy_stone_bricks    -> minecraft:infested_mossy_stone_bricks
	{host: 7756, infested: 7764},   // minecraft:cracked_stone_bricks  -> minecraft:infested_cracked_stone_bricks
	{host: 7757, infested: 7765},   // minecraft:chiseled_stone_bricks -> minecraft:infested_chiseled_stone_bricks
	{host: 30417, infested: 32067}, // minecraft:deepslate             -> minecraft:infested_deepslate (axis-copy DEFERRED: default axis=y)
}

// silverfishHostToInfested / silverfishInfestedToHost are the two lookup maps built once from
// silverfishInfestPairs — the StateID-granularity images of InfestedBlock.HOST_TO_INFESTED_STATES and
// INFESTED_TO_HOST_STATES. Read-only after init (package-level var init runs before any tick), so a
// tick-goroutine read is race-free.
var (
	silverfishHostToInfested = func() map[block.StateID]block.StateID {
		m := make(map[block.StateID]block.StateID, len(silverfishInfestPairs))
		for _, p := range silverfishInfestPairs {
			m[p.host] = p.infested
		}
		return m
	}()
	silverfishInfestedToHost = func() map[block.StateID]block.StateID {
		m := make(map[block.StateID]block.StateID, len(silverfishInfestPairs))
		for _, p := range silverfishInfestPairs {
			m[p.infested] = p.host
		}
		return m
	}()
)

// isCompatibleHostBlock ports InfestedBlock.isCompatibleHostBlock(BlockState) ==
// BLOCK_BY_HOST_BLOCK.containsKey(state.getBlock()). At StateID granularity this is "is this state's id
// a registered host state" — exact for the property-free hosts, and the default-state image for
// deepslate. Cite InfestedBlock.isCompatibleHostBlock.
func isCompatibleHostBlock(state block.StateID) bool {
	_, ok := silverfishHostToInfested[state]
	return ok
}

// isInfestedBlock reports whether a state is an InfestedBlock state (the wake goal's `block instanceof
// InfestedBlock` test). Cite SilverfishWakeUpFriendsGoal.tick (`if (block instanceof InfestedBlock)`).
func isInfestedBlock(state block.StateID) bool {
	_, ok := silverfishInfestedToHost[state]
	return ok
}

// infestedStateByHost ports InfestedBlock.infestedStateByHost(BlockState): the infested state for a
// compatible host. At StateID granularity the property-copy (getNewStateWithProperties/copyProperty) is
// the identity for the property-free hosts and the default (axis=y) image for deepslate (DEFERRED,
// documented above). Cite InfestedBlock.infestedStateByHost.
func infestedStateByHost(host block.StateID) (block.StateID, bool) {
	s, ok := silverfishHostToInfested[host]
	return s, ok
}

// hostStateByInfested ports InfestedBlock.hostStateByInfested(BlockState): the host state for an
// infested state (the wake goal's de-infest). Cite InfestedBlock.hostStateByInfested.
func hostStateByInfested(infested block.StateID) (block.StateID, bool) {
	s, ok := silverfishInfestedToHost[infested]
	return s, ok
}

// entityEventSpawnParticles is the byte status Entity.spawnAnim() broadcasts (ClientboundEntityEvent /
// Level.broadcastEntityEvent(this, (byte)20)) — the "poof of smoke/spawn" particles a merged silverfish
// and a summoned infestation emit. Cite Entity.spawnAnim.
//
//	[VERIFIED javap Entity.spawnAnim(): level().broadcastEntityEvent(this, (byte)20).]
const entityEventSpawnParticles byte = 20

// silverfishSpawnAnim ports net.minecraft.world.entity.Entity.spawnAnim(): broadcast the status-20
// spawn/despawn particle event to every player tracking the mob. Cite Entity.spawnAnim.
func (t *TickLoop) silverfishSpawnAnim(e *Entity) {
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventSpawnParticles))
}

// silverfishSpawnInfestation ports net.minecraft.world.level.block.InfestedBlock.spawnInfestation(
// ServerLevel, BlockPos): create a Silverfish (EntitySpawnReason.TRIGGERED), snapTo the block center
// (x+0.5, y, z+0.5, yRot 0, xRot 0), addFreshEntity, spawnAnim. The Silverfish is spawned from the
// declared silverfish plugin decl (the v1 silverfish IS a declared mob). If no silverfish decl is
// registered (EntityType.create == null analogue) the summon is a no-op, matching the `if (silverfish
// != null)` guard. Cite InfestedBlock.spawnInfestation.
//
//	[VERIFIED CFR InfestedBlock.spawnInfestation: Silverfish s = SILVERFISH.create(level, TRIGGERED);
//	 if (s != null) { s.snapTo(pos.getX()+0.5, pos.getY(), pos.getZ()+0.5, 0.0f, 0.0f);
//	 level.addFreshEntity(s); s.spawnAnim(); }.]
func (t *TickLoop) silverfishSpawnInfestation(pos pk.Position) {
	if t.mobRegistry == nil {
		return
	}
	decl := t.mobRegistry.declByBaseType(entity.Silverfish.ID)
	if decl == nil {
		return // EntityType.create(...) == null: no silverfish decl registered — the `!= null` guard fails
	}
	// snapTo(pos.getX()+0.5, pos.getY(), pos.getZ()+0.5, 0, 0): the block-center spawn (feet at pos.getY()).
	sx := float64(pos.X) + 0.5
	sy := float64(pos.Y)
	sz := float64(pos.Z) + 0.5
	s := t.spawnDeclaredMob(decl, sx, sy, sz)
	if s == nil {
		return
	}
	t.silverfishSpawnAnim(s)
}

// silverfishDestroyInfestedBlock ports the mobGriefing branch of SilverfishWakeUpFriendsGoal.tick:
// Level.destroyBlock(pos, true, silverfish). v1 has no generic destroyBlock pipeline, so this sets the
// block to AIR (the block IS destroyed) and directly runs spawnInfestation (the InfestedBlock
// spawnAfterBreak -> spawnInfestation summon that destroyBlock-with-dropBlock triggers on an
// InfestedBlock). The BLOCK-DROP loot is DEFERRED (documented in the file header). Cite
// SilverfishWakeUpFriendsGoal.tick (Level.destroyBlock true) + InfestedBlock.spawnAfterBreak/spawnInfestation.
func (t *TickLoop) silverfishDestroyInfestedBlock(pos pk.Position) {
	if t.world() == nil {
		return
	}
	air := block.ToStateID[block.Air{}]
	if t.world().SetBlock(pos, air, dimMinY) {
		t.broadcastBlockUpdate(pos, air)
	}
	// spawnAfterBreak on an InfestedBlock (BLOCK_DROPS default true, no PREVENTS_INFESTED_SPAWNS
	// enchantment subsystem) -> spawnInfestation: summon a silverfish at the broken position.
	t.silverfishSpawnInfestation(pos)
}

// silverfishNotifyHurt ports Silverfish.hurtServer's SilverfishWakeUpFriendsGoal trigger + the goal's
// notifyHurt(). Silverfish.hurtServer (CFR this session):
//
//	if ((source.getEntity() != null || source.is(DamageTypeTags.ALWAYS_TRIGGERS_SILVERFISH))
//	    && this.friendsGoal != null) {
//	    this.friendsGoal.notifyHurt();
//	}
//
// notifyHurt(): if (lookForFriends == 0) lookForFriends = adjustedTickDelay(20);
//
// Called from applyDamageEntity (combat_mob.go) for a live silverfish AFTER the shared hit lands, the
// same per-type post-hurt hook site as endermanHurtTeleport. source.getEntity() != null == src.attacker
// != 0; source.is(ALWAYS_TRIGGERS_SILVERFISH) is the genuine tag read. friendsGoal != null is always
// true for the vanilla_silverfish decl (it declares the wake goal @3), so the arm fires whenever the
// hurt condition holds. Cite Silverfish.hurtServer + SilverfishWakeUpFriendsGoal.notifyHurt.
func (t *TickLoop) silverfishNotifyHurt(e *Entity, src damageSource) {
	if e.ai == nil {
		return
	}
	// (source.getEntity() != null || source.is(ALWAYS_TRIGGERS_SILVERFISH)) — else no arm.
	if src.attacker == 0 && !src.is("always_triggers_silverfish") {
		return
	}
	// notifyHurt(): arm lookForFriends to adjustedTickDelay(20) ONLY when it is currently 0.
	if e.ai.silverfishLookForFriends == 0 {
		e.ai.silverfishLookForFriends = adjustedTickDelay(silverfishWakeLookForFriends, false) // SilverfishWakeUpFriendsGoal: requiresUpdateEveryTick=false, decimated selector decrement -> ceil(20/2)=10
	}
}
