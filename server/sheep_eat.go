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
//   - Sheep wool COLOR is now live (e.sheepColor): finalizeSpawn seeds it via getRandomSheepColor (off the
//     LEVEL RandomSource), the dye interact (trySheepDye) recolors, the evoker WOLOLO recolors to RED, and
//     the shear picks the per-color loot table (shearing/sheep/<color>). The biome-tag routing of the spawn
//     color (SheepColorSpawnRules WARM/COLD configs) is CITE-DEFERRED to the TEMPERATE config: no biome-tag
//     read is wired for SPAWNS_WARM/COLD_VARIANT_FARM_ANIMALS, so getRandomSheepColor uses the TEMPERATE
//     weighted table (the overworld default) — the RNG draw order + thresholds are exact; a biome read slots
//     in ahead of the config pick later. The breed-offspring color (Sheep.getBreedOffspring -> getMixedColor,
//     which needs the dye-mixing recipe subsystem) is CITE-DEFERRED (the sheep has no host breed path yet;
//     breed() is pig-specific).
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
	// The wire DATA_WOOL byte is the wool color id (low nibble) OR the sheared bit (0x10) — woolByteFor
	// composes both from the live e.sheepColor + e.sheared, exactly as the jar's setSheared toggles bit
	// 0x10 while preserving the color nibble (cur & 0xF0 stays; only 0x10 changes).
	t.broadcastToTrackers(e.id, encodeSetEntityData(e, woolDataEntry(woolByteFor(e.sheepColor, v))))
}

// sheepGetColor is net.minecraft.world.entity.animal.sheep.Sheep.getColor() reduced to the DyeColor id
// (the low DATA_WOOL nibble). Sulfur keeps the id on e.sheepColor, so getColor == e.sheepColor & 0xF.
// VERBATIM (Sheep.getColor: DyeColor.byId(entityData.get(DATA_WOOL_ID) & 0xF)). NO RNG. Cite Sheep.getColor.
func sheepGetColor(e *Entity) byte {
	return e.sheepColor & 0x0F
}

// sheepSetColor is net.minecraft.world.entity.animal.sheep.Sheep.setColor(DyeColor) reduced to its
// observable effect — the DATA_WOOL low-nibble color set + the ClientboundSetEntityData broadcast so
// trackers re-render the wool color. VERBATIM (Sheep.setColor: DATA_WOOL = (cur & 0xF0) | (c.getId() &
// 0xF)). Sulfur keeps the id on e.sheepColor and derives the wire byte (woolByteFor: color nibble | the
// preserved sheared bit). NO RNG. Cite Sheep.setColor + Sheep.DATA_WOOL_ID.
func (t *TickLoop) sheepSetColor(e *Entity, colorID byte) {
	e.sheepColor = colorID & 0x0F
	t.broadcastToTrackers(e.id, encodeSetEntityData(e, woolDataEntry(woolByteFor(e.sheepColor, e.sheared))))
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
// SHEAR_SHEEP routes by the sheep's DyeColor -> "minecraft:shearing/sheep/<color>" (e.g. white.json,
// 1-3 white_wool; red.json, 1-3 red_wool). The loot
// SEED is event-time rand.Int64() (OFF the mob stream, like death loot — does not perturb mobRandom(e));
// the per-stack scatter (5 nextFloat) is drawn from the MOB stream (mobRandom(e)) in jar order: x =
// (nF - nF)*0.1, y = nF*0.05, z = (nF - nF)*0.1. setSheared(true) is AFTER the drop (jar order).
// Cite Sheep.shear.
func (t *TickLoop) shearSheep(e *Entity) {
	// level.playSound(SHEEP_SHEAR, SoundSource.PLAYERS, 1.0, 1.0). soundid 1441 == entity.sheep.shear.
	t.broadcastToTrackers(e.id, encodeSoundEntity(1441, soundSourcePlayers, e.id, 1.0, 1.0, 0))

	// SHEAR_SHEEP (== "minecraft:shearing/sheep") routes by the sheep's DyeColor via the root table's
	// entity_properties(sheep/color) alternatives to the per-color pool "shearing/sheep/<color>" (each a
	// 1..3 uniform roll of that color's wool). Sulfur's loot evaluator does not evaluate the sheep-color
	// entity predicate, so we select the SAME per-color table directly by e.sheepColor — producing the
	// identical drop the predicate-routed root table would (structurally faithful; the observable drop set
	// is byte-identical). Cite BuiltInLootTables.SHEAR_SHEEP (data/loot_table/shearing/sheep.json alternatives).
	tbl, err := loot.LoadTable("minecraft:shearing/sheep/" + dyeColorName(sheepGetColor(e)))
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

// trySheepDye is net.minecraft.world.item.DyeItem.interactLivingEntity's Sheep branch — the DYE path that
// recolors a live, un-sheared sheep when right-clicked with a dye whose color differs from the sheep's.
// It is wired into handleInteract as a sheep-gated interact (a dye ItemStack on a Sheep target), the
// sibling of trySheepShear. VERBATIM (DyeItem.interactLivingEntity):
//
//	if (target instanceof Sheep sheep && sheep.isAlive() && !sheep.isSheared()
//	        && (dye = itemStack.get(DataComponents.DYE)) != null && sheep.getColor() != dye) {
//	    sheep.level().playSound(player, sheep, DYE_USE, SoundSource.PLAYERS, 1.0, 1.0);
//	    if (!isClientSide) { sheep.setColor(dye); itemStack.shrink(1); }
//	    return SUCCESS;
//	}
//	return PASS;
//
// Returns TRUE when the held item is a dye AND the recolor conditions hold (the interact is consumed so
// handleInteract does NOT fall through to feed); FALSE when the held item is not a dye (fall through) OR
// the dye matched an already-sheared / same-color sheep (PASS — no consume, but also not a feed item, so
// a false here harmlessly falls to tryFeedAnimal which no-ops on a non-food dye). The held item is read
// SERVER-SIDE (inv.get — never trusted from the Interact payload), exactly as trySheepShear/tryFeedAnimal.
// NO RNG. Cite DyeItem.interactLivingEntity (Sheep branch).
func (t *TickLoop) trySheepDye(p *tickPlayer, mob *Entity) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if slotIsEmpty(held) {
		return false // empty hand -> not a dye -> super.mobInteract (feed path)
	}
	// itemStack.get(DataComponents.DYE): a non-dye item yields null -> PASS (fall through).
	dye, ok := dyeColorIDOf(int32(held.ItemID))
	if !ok {
		return false // not a dye item -> fall through to the feed path
	}
	// sheep.isAlive() && !sheep.isSheared() && sheep.getColor() != dye: a sheared sheep or a same-color
	// dye is a no-op PASS (vanilla returns PASS; here false so the feed path — which no-ops on a dye — runs).
	if !mob.isAlive() || mob.sheared || sheepGetColor(mob) == byte(dye) {
		return false
	}
	// level.playSound(player, sheep, DYE_USE, SoundSource.PLAYERS, 1.0, 1.0). soundid 571 == item.dye.use.
	t.broadcastToTrackers(mob.id, encodeSoundEntity(571, soundSourcePlayers, mob.id, 1.0, 1.0, 0))
	// !isClientSide: sheep.setColor(dye) + itemStack.shrink(1). The server is authoritative (always the
	// server branch here); setColor broadcasts the DATA_WOOL recolor to trackers.
	t.sheepSetColor(mob, byte(dye))
	t.shrinkHeldItem(p, inv) // itemStack.shrink(1)
	return true              // SUCCESS
}

// getRandomSheepColor is net.minecraft.world.entity.animal.sheep.Sheep.getRandomSheepColor(level, pos) ->
// SheepColorSpawnRules.getSheepColor(biome, level.getRandom()): the spawn-time wool DyeColor a sheep is
// finalized with. It draws off the LEVEL RandomSource (level.getRandom() — the region's levelRandom, NOT
// the mob's per-entity stream), so a spawning sheep NEVER perturbs the pig oracle's mob stream (and the
// pig, never a sheep, never reaches this — zero draws for it).
//
// BIOME CITE-DEFERRAL: SheepColorSpawnRules.getSheepColorConfiguration branches on biome tags
// (SPAWNS_WARM/COLD_VARIANT_FARM_ANIMALS) to a WARM/COLD/TEMPERATE weighted table. No biome-tag read is
// wired, so this uses the TEMPERATE config (the overworld default) — the value is NOT baked away: a biome
// read slots in ahead of the config pick later. The RNG DRAW ORDER + thresholds below are EXACT for the
// TEMPERATE table.
//
// TEMPERATE WeightedList (insertion order, total 100): BLACK(5), GRAY(5), LIGHT_GRAY(5), BROWN(3),
// commonColors(WHITE)(82). commonColors(WHITE) is a nested WeightedList (total 500): WHITE(499), PINK(1).
// WeightedList.getRandomOrThrow draws nextInt(totalWeight) then walks entries in insertion order
// subtracting each weight until the running selection goes negative (WeightedList$Compact.get). So:
//
//	sel = level.nextInt(100)               // DRAW 1 (outer table)
//	 sel<5   -> BLACK;   sel<10 -> GRAY;   sel<15 -> LIGHT_GRAY;   sel<18 -> BROWN
//	 else (commonColors provider .get(random)):
//	   sel2 = level.nextInt(500)           // DRAW 2 (nested, ONLY on the commonColors branch)
//	   sel2<499 -> WHITE ; else PINK
//
// The nested nextInt(500) is drawn ONLY when the outer pick lands in the commonColors bucket (sel>=18) —
// the single(...) providers return their constant with NO further draw. This matches the jar exactly
// (single = random -> color; weighted = random -> elements.getRandomOrThrow(random).get(random)).
//
//	[VERIFIED CFR SheepColorSpawnRules.TEMPERATE_SPAWN_CONFIGURATION + WeightedList.getRandomOrThrow +
//	 WeightedList$Compact.get; Sheep.getRandomSheepColor(level, pos) -> getSheepColor(biome, getRandom()).]
func getRandomSheepColor(lr sheepLevelRandom) byte {
	sel := lr.NextIntN(100) // DRAW 1: outer TEMPERATE table nextInt(100)
	switch {
	case sel < 5:
		return dyeBlack // BLACK
	case sel < 10:
		return dyeGray // GRAY
	case sel < 15:
		return dyeLightGray // LIGHT_GRAY
	case sel < 18:
		return dyeBrown // BROWN
	default: // commonColors(WHITE) bucket (weight 82) -> the nested nextInt(500)
		sel2 := lr.NextIntN(500) // DRAW 2: nested commonColors nextInt(500)
		if sel2 < 499 {
			return dyeWhite // WHITE
		}
		return dyePink // PINK
	}
}

// sheepLevelRandom is the minimal RandomSource surface getRandomSheepColor needs (level.getRandom()
// .nextInt(bound)) — satisfied by *levelgen.LegacyRandomSource (the region levelRandom). Kept as an
// interface so the color draw is unit-testable against a mirror LegacyRandomSource.
type sheepLevelRandom interface {
	NextIntN(bound int32) int32
}

// DyeColor ids used by the sheep spawn/WOLOLO paths (DyeColor enum order, javap-verified).
const (
	dyeWhite     byte = 0  // DyeColor.WHITE
	dyeGray      byte = 7  // DyeColor.GRAY
	dyeLightGray byte = 8  // DyeColor.LIGHT_GRAY
	dyeBlue      byte = 11 // DyeColor.BLUE
	dyeBrown     byte = 12 // DyeColor.BROWN
	dyeRed       byte = 14 // DyeColor.RED
	dyeBlack     byte = 15 // DyeColor.BLACK
	dyePink      byte = 6  // DyeColor.PINK
)
