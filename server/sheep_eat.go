package server

// sheep_eat.go — Phase 34 (MOB-PASS-02): the Sheep HOST seams the vanilla_sheep .star calls — the
// EatBlockGoal eat (grass_block-below -> dirt + wool regrow + baby ageUp), the DATA_WOOL setSheared
// support, and the SHEAR interact (shears -> white-wool drop + setSheared(true) + the SHEEP_SHEAR sound).
// Every function is a literal 1:1 port of the unobfuscated 26.2 jar bytecode (verbatim in
// 34-JARNOTES.md:149-296), re-expressed in Go (no GPL paste) with the class/method cited.
//
// SCOPE DEFERRALS (cited, locked by 34-JARNOTES.md:88-91,241-286 + 34-CONTEXT.md):
//   - The EatBlockGoal tall-grass / fern branch (IS_EDIBLE = state.is(BlockTags.EDIBLE_FOR_SHEEP)) is
//     CITE-DEFERRED: EDIBLE_FOR_SHEEP is a BLOCK tag NOT extracted into data/tag (only damage-type + item
//     tags are). eatGrassBlock ports ONLY the grass_block-below eat (the common case). When block tags are
//     extracted, the tall-grass disjunct slots in ahead of the below-check — the structure is preserved,
//     the value is not baked away.
//   - The MOB_GRIEFING gamerule gate (vanilla wraps the block destroy/levelEvent in `if (MOB_GRIEFING)`)
//     is treated as its v1 default TRUE (mobGriefing defaults true in vanilla), so the eat always swaps
//     the block — no gamerule subsystem is wired yet; structured to become a real read later.
//   - level.levelEvent(2001, below, ...) (the block-break particle/sound client event) is CITE-DEFERRED:
//     no levelEvent seam exists. It is a pure client cosmetic with no gameplay/RNG effect; the eat-event
//     byte 10 (the head-down animation) IS broadcast via eat_broadcast_byte10 from the .star.
//   - v1 ships WHITE sheep ONLY (getColor() is always WHITE; the shear loot table is shearing/sheep/white;
//     dye-color mechanics deferred per 34-CONTEXT). The low DATA_WOOL color nibble is always 0.
//   - itemStack.hurtAndBreak(1, ...) (shears durability -1) is realized as a held-item shrink-by-1 (v1 has
//     no per-item durability subsystem) — see trySheepShear; documented there.

import (
	"math"
	"math/rand/v2"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/loot"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// eatGrassBlock is the EAT branch of net.minecraft.world.entity.ai.goal.EatBlockGoal.tick — the act the
// goal performs when its eat-animation timer hits adjustedTickDelay(4) (the .star owns the timer + the
// nextInt canUse gate; the HOST owns this block mutation). VERBATIM (34-JARNOTES.md:163-171), the
// grass_block-below path (the tall-grass disjunct is cite-deferred above):
//
//	below = pos.below();
//	if (getBlockState(below).is(Blocks.GRASS_BLOCK)) {
//	    level.levelEvent(2001, below, ...);              // particle (cite-deferred — no seam)
//	    level.setBlock(below, DIRT.defaultBlockState(), 2);
//	    mob.ate();                                       // Sheep.ate -> setSheared(false) + ageUp(60)
//	}
//
// The block read/write route through t.world() (the SOLE block accessor; commands_dbg.go uses it the same
// way). NO RNG. Cite net.minecraft.world.entity.ai.goal.EatBlockGoal.tick (eat branch).
func (t *TickLoop) eatGrassBlock(e *Entity) {
	if t.world() == nil {
		return
	}
	// pos.below(): the block directly under the mob's feet (the floor it stands on).
	below := pk.Position{
		X: int(math.Floor(e.x)),
		Y: int(math.Floor(e.y)) - 1,
		Z: int(math.Floor(e.z)),
	}
	st, ok := t.world().GetBlock(below, dimMinY)
	if !ok || st != block.DefaultStateID["minecraft:grass_block"] {
		return // not a grass_block below -> no eat (is(GRASS_BLOCK) failed)
	}
	// level.setBlock(below, DIRT, 2): the grass turns to dirt (the eaten turf).
	t.world().SetBlock(below, block.DefaultStateID["minecraft:dirt"], dimMinY)
	t.sheepAte(e) // mob.ate()
}

// sheepAte is net.minecraft.world.entity.animal.sheep.Sheep.ate — the Sheep override of Animal.ate that
// the EatBlockGoal eat triggers. VERBATIM (34-JARNOTES.md:175):
//
//	super.ate();                  // Animal.ate is a v1 no-op (no eatingSoundEvent/forced-age effect ported)
//	setSheared(false);            // the wool regrows
//	if (canAgeUp()) ageUp(60);    // a baby that just ate grows up by 60
//
// NO RNG. Cite net.minecraft.world.entity.animal.sheep.Sheep.ate.
func (t *TickLoop) sheepAte(e *Entity) {
	t.setSheared(e, false) // wool regrows: DATA_WOOL bit 0x10 cleared (cur & 0xEF)
	if e.canAgeUp() {
		e.ageUp(60) // ageUp(60): grow the baby toward adulthood (clamped at 0)
	}
}

// setSheared is net.minecraft.world.entity.animal.sheep.Sheep.setSheared(boolean) reduced to its
// observable effect — the DATA_WOOL sheared bit (0x10) toggle + the ClientboundSetEntityData broadcast so
// trackers re-render the sheep's wool/sheared body. VERBATIM (34-JARNOTES.md:275):
//
//	setSheared(b): DATA_WOOL = b ? (cur | 0x10) : (cur & 0xEF);
//
// Sulfur keeps the bool on the Entity (e.sheared) and DERIVES the wire byte from it: v1 ships WHITE sheep
// only, so the low color nibble (& 0xF) is always 0 (cur == 0) — `cur | 0x10` == 0x10 and `cur & 0xEF` ==
// 0x00. The broadcast mirrors the babyDataEntry splice (plugin_mob_decl.go:415-417), here via woolDataEntry
// (the BYTE-serializer DATA_WOOL entry). NO RNG. Cite Sheep.setSheared + Sheep.DATA_WOOL_ID.
func (t *TickLoop) setSheared(e *Entity, v bool) {
	e.sheared = v
	woolByte := byte(0x00) // low nibble = color; v1 WHITE (0) — cur == 0x00
	if v {
		woolByte |= 0x10 // setSheared(true): cur | 0x10
	}
	// setSheared(false): cur & 0xEF == 0x00 already (cur is 0x00), so woolByte stays 0x00.
	t.broadcastToTrackers(e.id, encodeSetEntityData(e, woolDataEntry(woolByte)))
}

// trySheepShear is the SHEAR path of net.minecraft.world.entity.animal.sheep.Sheep.mobInteract — the
// branch that runs BEFORE super.mobInteract (the feed/breed path), wired into handleInteract ahead of
// tryFeedAnimal (sheep-gated). VERBATIM (34-JARNOTES.md:242-254):
//
//	if (itemStack.is(Items.SHEARS)) {
//	    if (ServerLevel && readyForShearing()) {        // readyForShearing = !isSheared() && !isBaby()
//	        shear(sl, SoundSource.PLAYERS, itemStack);
//	        gameEvent(GameEvent.SHEAR, player);          // cite-deferred: no gameEvent seam, no net effect
//	        itemStack.hurtAndBreak(1, player, ...);      // shears durability -1 (v1 = held-item shrink 1)
//	        return SUCCESS_SERVER;
//	    }
//	    return CONSUME;                                  // not ready -> consume, NO fall-through to feed
//	}
//	return super.mobInteract(...);                       // not shears -> the feed/breed path
//
// Returns TRUE when the interact is a shears interact (whether or not it actually sheared — the not-ready
// CONSUME case also returns true so handleInteract does NOT fall through to feed), FALSE only when the held
// item is NOT shears (so the dispatcher falls through to tryFeedAnimal). The held item is read SERVER-SIDE
// (inv.get — never trusted from the Interact payload, T-34-14), exactly as tryFeedAnimal reads it. NO RNG
// in this dispatch (the 5-nextFloat shear scatter is drawn in shearSheep). Cite Sheep.mobInteract.
func (t *TickLoop) trySheepShear(p *tickPlayer, mob *Entity) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if slotIsEmpty(held) || int32(held.ItemID) != int32(item.Shears.ID) {
		return false // not shears -> super.mobInteract (the feed/breed path)
	}
	// itemStack.is(Items.SHEARS) is TRUE from here on -> the interact is consumed regardless of outcome.
	if mob.sheared || mob.isBaby() {
		return true // !readyForShearing() -> CONSUME (no shear, no feed fall-through)
	}
	t.shearSheep(mob)        // shear(): SHEEP_SHEAR sound + white-wool drop + setSheared(true)
	t.shrinkHeldItem(p, inv) // hurtAndBreak(1): v1 held-item shrink by 1 (no durability subsystem)
	return true              // SUCCESS_SERVER
}

// shearSheep is net.minecraft.world.entity.animal.sheep.Sheep.shear(level, src, tool) — the actual shear:
// the SHEEP_SHEAR sound, the shearing loot drop (per rolled stack: a per-item ItemEntity + the
// 5-nextFloat scatter off the MOB stream), then setSheared(true). VERBATIM (34-JARNOTES.md:255-266):
//
//	level.playSound(null, this, SHEEP_SHEAR, src, 1.0, 1.0);
//	dropFromShearingLootTable(level, SHEAR_SHEEP, tool, (l, drop) -> {
//	    for (int i = 0; i < drop.getCount(); i++) {
//	        ItemEntity e = spawnAtLocation(l, drop.copyWithCount(1), 1.0f);
//	        e.setDeltaMovement(e.getDeltaMovement().add(
//	            (nextFloat()-nextFloat())*0.1, nextFloat()*0.05, (nextFloat()-nextFloat())*0.1)); // 5 nextFloat/stack
//	    }
//	});
//	setSheared(true);
//
// SHEAR_SHEEP for a WHITE sheep -> "minecraft:shearing/sheep/white" (white.json, 1-3 white_wool). The loot
// SEED is event-time rand.Int64() (OFF the mob stream, like death loot — does not perturb mobRandom(e));
// the per-stack scatter (5 nextFloat) is drawn from the MOB stream (mobRandom(e)) in jar order: x =
// (nF - nF)*0.1, y = nF*0.05, z = (nF - nF)*0.1. setSheared(true) is AFTER the drop (jar order).
// Cite Sheep.shear.
func (t *TickLoop) shearSheep(e *Entity) {
	// level.playSound(SHEEP_SHEAR, SoundSource.PLAYERS, 1.0, 1.0). soundid 1441 == entity.sheep.shear.
	t.broadcastToTrackers(e.id, encodeSoundEntity(1441, soundSourcePlayers, e.id, 1.0, 1.0, 0))

	tbl, err := loot.LoadTable("minecraft:shearing/sheep/white") // SHEAR_SHEEP, WHITE (v1)
	if err == nil {
		// The loot seed is event-time (server-generated), OFF the mob stream — same discipline as
		// dropMobLoot's death-loot seed. The shearing table is type-agnostic in the evaluator (the
		// "minecraft:shearing" type string is parsed metadata, not an eval gate) and rolls uniform(1,3)
		// white_wool from this seed (34-JARNOTES.md:288-295).
		seed := rand.Int64()
		ctx := loot.NewEntityLootContext(seed, 0, loot.EntityLootParams{}) // shearing has no kill/fire gates
		for _, stack := range loot.Roll(tbl, seed, ctx) {
			// for (i = 0; i < drop.getCount(); i++): spawn each unit as its own ItemEntity (copyWithCount(1)).
			for i := int32(0); i < int32(stack.Count); i++ {
				one := stack
				one.Count = 1
				ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y+e.height/2.0, e.z, one)
				// e.setDeltaMovement(getDeltaMovement().add(...)): the 5-nextFloat scatter, MOB stream
				// (this.random == mobRandom(e)), ON TOP of NewItemEntity's own ItemEntity.<init> toss.
				// Drawn in jar order — x first (2 nextFloat), then y (1 nextFloat), then z (2 nextFloat).
				ie.vx += float64((mobRandom(e).nextFloat() - mobRandom(e).nextFloat()) * 0.1) // x: 2 nextFloat
				ie.vy += float64(mobRandom(e).nextFloat() * 0.05)                             // y: 1 nextFloat
				ie.vz += float64((mobRandom(e).nextFloat() - mobRandom(e).nextFloat()) * 0.1) // z: 2 nextFloat
				t.regionForEntity(e).entities.add(ie)
			}
		}
	}
	t.setSheared(e, true) // setSheared(true) AFTER the drop (jar order)
}
