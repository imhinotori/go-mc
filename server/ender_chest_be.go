package server

// ender_chest_be.go -- the ENDER CHEST block-entity + its ContainerOpenersCounter + ChestLidController, a
// 1:1 port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session). The ender
// chest's INVENTORY is PER-PLAYER (the 27-slot PlayerEnderChestContainer on the player NBT, shared across
// EVERY ender chest block in the world) -- so this block-entity carries NO container. What it DOES carry is
// the opener count + lid animation (the ContainerOpenersCounter play-sound + block-event, the
// ChestLidController openness lerp) that make the chest visibly open/close and play its sounds.
//
// 1:1 ANCHORS (VERIFIED javap this session):
//
//	net.minecraft.world.level.block.entity.EnderChestBlockEntity:
//	    fields chestLidController=new ChestLidController(); openersCounter=new ContainerOpenersCounter(){...};
//	    startOpen(user): if(!remove && !user.isSpectator()) openersCounter.incrementOpeners(...);
//	    stopOpen(user):  if(!remove && !user.isSpectator()) openersCounter.decrementOpeners(...);
//	    recheckOpen():   if(!remove) openersCounter.recheckOpeners(...);
//	    lidAnimateTick:  chestLidController.tickLid();     (client-side render, driven server-side here)
//	    triggerEvent(1,type): chestLidController.shouldBeOpen(type>0);
//	    anon ContainerOpenersCounter:
//	      onOpen:  playSound(ENDER_CHEST_OPEN, BLOCKS, 0.5, random.nextFloat()*0.1+0.9);
//	      onClose: playSound(ENDER_CHEST_CLOSE, BLOCKS, 0.5, random.nextFloat()*0.1+0.9);
//	      openerCountChanged: level.blockEvent(pos, Blocks.ENDER_CHEST, 1, newCount);
//	      isOwnContainer(p): p.getEnderChestInventory().isActiveChest(this).
//	net.minecraft.world.level.block.entity.ContainerOpenersCounter:
//	    CHECK_TICK_DELAY=5;
//	    incrementOpeners: prev=openCount++; if(prev==0){onOpen; gameEvent(OPEN); scheduleRecheck;}
//	                      openerCountChanged(prev, openCount); maxRange=max(range, maxRange);
//	    decrementOpeners: prev=openCount--; if(openCount==0){onClose; gameEvent(CLOSE); maxRange=0;}
//	                      openerCountChanged(prev, openCount);
//	net.minecraft.world.level.block.entity.ChestLidController:
//	    tickLid: oOpenness=openness; if(!shouldBeOpen && openness>0) openness=max(openness-0.1,0);
//	             else if(shouldBeOpen && openness<1) openness=min(openness+0.1,1);
//	    getOpenness(partial)=Mth.lerp(partial, oOpenness, openness).
//
// The lid block-event (client render sync) is applied to server state directly (shouldBeOpen), the bell BE
// precedent. The OPEN/CLOSE sounds ARE played. The scheduleRecheck(5) recount is replaced by the direct
// increment/decrement on open/close (v1 tracks the single opening/closing player per ender chest; the
// recount path exists to reconcile crashed/teleported viewers, a cited robustness deferral).

import (
	"math/rand/v2"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// enderChestOpenSoundID / enderChestCloseSoundID are SoundEvents.ENDER_CHEST_OPEN / _CLOSE
// (data/soundid/soundid.go: 584 block.ender_chest.open, 583 block.ender_chest.close). Played on
// SoundSource.BLOCKS. CITE EnderChestBlockEntity anon ContainerOpenersCounter.onOpen/onClose.
const (
	enderChestOpenSoundID  int32 = 584
	enderChestCloseSoundID int32 = 583
)

// enderChestBE is the tick-owned per-block state of one ender_chest -- the ContainerOpenersCounter openCount
// + the ChestLidController lid animation (openness/oOpenness/shouldBeOpen). It carries NO container: the
// ender inventory is per-PLAYER (tickPlayer.enderItems). CITE EnderChestBlockEntity (LidBlockEntity).
type enderChestBE struct {
	openCount    int
	openness     float32
	oOpenness    float32
	shouldBeOpen bool
}

// resolveEnderChest returns the tick-owned enderChestBE for pos, creating an empty one (openCount 0, lid
// closed) on first access -- the analogue of a freshly-placed EnderChestBlockEntity. There is no NBT to
// restore (the ender chest BE persists NO container; the per-player inventory lives on the player .dat).
// Returns nil when pos is not an ender chest (or the world is unloaded). Tick-owned (t.enderChests).
func (t *TickLoop) resolveEnderChest(pos pk.Position) *enderChestBE {
	if t.enderChests == nil {
		t.enderChests = make(map[pk.Position]*enderChestBE)
	}
	if b, ok := t.enderChests[pos]; ok {
		return b
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsEnderChest(state) {
		return nil
	}
	b := &enderChestBE{}
	t.enderChests[pos] = b
	return b
}

// enderChestIncrementOpeners ports ContainerOpenersCounter.incrementOpeners: prev=openCount++; if prev==0
// run onOpen (the ENDER_CHEST_OPEN sound) + start the lid opening (openerCountChanged -> triggerEvent(1,
// newCount>0) -> shouldBeOpen(true)). CITE ContainerOpenersCounter.incrementOpeners + the anon onOpen.
func (t *TickLoop) enderChestIncrementOpeners(pos pk.Position, b *enderChestBE) {
	prev := b.openCount
	b.openCount++
	if prev == 0 {
		// onOpen: playSound(ENDER_CHEST_OPEN, BLOCKS, 0.5f, random.nextFloat()*0.1f + 0.9f).
		pitch := rand.Float32()*0.1 + 0.9
		t.playSound(enderChestOpenSoundID, soundSourceBlocks, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, 0.5, pitch, rand.Int64())
		// gameEvent(CONTAINER_OPEN): cite-deferred (no BE gameEvent seam).
	}
	// openerCountChanged(prev, openCount) -> level.blockEvent(pos, Blocks.ENDER_CHEST, 1, openCount) ->
	// triggerEvent(1, openCount): chestLidController.shouldBeOpen(openCount > 0). Applied to server state.
	b.shouldBeOpen = b.openCount > 0
}

// enderChestDecrementOpeners ports ContainerOpenersCounter.decrementOpeners: prev=openCount--; if openCount
// reached 0 run onClose (the ENDER_CHEST_CLOSE sound) + close the lid. CITE
// ContainerOpenersCounter.decrementOpeners + the anon onClose.
func (t *TickLoop) enderChestDecrementOpeners(pos pk.Position, b *enderChestBE) {
	b.openCount--
	if b.openCount == 0 {
		pitch := rand.Float32()*0.1 + 0.9
		t.playSound(enderChestCloseSoundID, soundSourceBlocks, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, 0.5, pitch, rand.Int64())
	}
	if b.openCount < 0 {
		b.openCount = 0
	}
	// openerCountChanged -> triggerEvent(1, openCount) -> shouldBeOpen(openCount > 0).
	b.shouldBeOpen = b.openCount > 0
}

// tickEnderChests ticks every live ender_chest block-entity once per tick: the ChestLidController.tickLid
// openness lerp (0.1f step toward shouldBeOpen). A chest whose block was broken/replaced is dropped from the
// store. CITE EnderChestBlockEntity.lidAnimateTick -> ChestLidController.tickLid. Called from tickWorld.
func (t *TickLoop) tickEnderChests() {
	if len(t.enderChests) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, b := range t.enderChests {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !block.IsEnderChest(state) {
			delete(t.enderChests, pos)
			continue
		}
		t.enderChestTickLid(b)
	}
}

// enderChestTickLid ports ChestLidController.tickLid: oOpenness=openness; step openness by 0.1f toward the
// target (down when !shouldBeOpen && openness>0, up when shouldBeOpen && openness<1), clamped to [0,1]. CITE.
func (t *TickLoop) enderChestTickLid(b *enderChestBE) {
	b.oOpenness = b.openness
	if !b.shouldBeOpen && b.openness > 0.0 {
		b.openness = max(b.openness-0.1, 0.0)
	} else if b.shouldBeOpen && b.openness < 1.0 {
		b.openness = min(b.openness+0.1, 1.0)
	}
}
