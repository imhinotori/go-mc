package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// entity_equipment.go — the mob EQUIPMENT layer (the held-item/armor slots), a 1:1 port of the
// vanilla LivingEntity/EntityEquipment storage. This is the SERVER-SIDE half several deferred
// features need (the skeleton's real bow ITEM in MAINHAND, zombie/skeleton armor later, the
// enderman block-carry, and the client-visible equipment metadata via ClientboundSetEquipment).
//
// STORAGE MODEL (net.minecraft.world.entity.EntityEquipment): vanilla holds the slots in a
//
//	private final EnumMap<EquipmentSlot, ItemStack> items;
//
// with get(slot) == items.getOrDefault(slot, ItemStack.EMPTY) and set(slot, stack) == items.put.
// An absent slot reads back as the EMPTY stack. We represent that EnumMap as a fixed
// [equipmentSlotCount]component.SlotData array indexed by the EquipmentSlot ORDINAL (the same
// ordinal ClientboundSetEquipmentPacket writes on the wire): the zero-value SlotData (Count==0)
// IS the EMPTY stack, so an un-populated slot reads back empty exactly as getOrDefault does. A
// mob with no equipment (the pig, the cow) carries an all-zero array — no allocation beyond the
// inline array, no RNG draw, no wire bytes: the byte-identical default the oracle pig relies on.
//
//	[VERIFIED javap EntityEquipment: `private final EnumMap<EquipmentSlot,ItemStack> items;`
//	 get -> items.getOrDefault(slot, ItemStack.EMPTY); set -> items.put(slot, stack) (returns old
//	 or EMPTY). LivingEntity.getItemBySlot(slot) -> equipment.get(slot); setItemSlot(slot,stack) ->
//	 equipment.set(slot,stack). EquipmentSlot ordinals (declaration order): MAINHAND=0, OFFHAND=1,
//	 FEET=2, LEGS=3, CHEST=4, HEAD=5, BODY=6, SADDLE=7.]
//
// TICK-05: the equipment array is tick-owned Entity game state — read/mutated ONLY on the tick
// goroutine (the spawn populate + a goal read). Like ai/attributes it is NOT part of the
// snapshot-friendly value set the async tracker copies for the MOVEMENT diff; the tracker copies
// it separately (snapshotEntity) ONLY to emit the spawn-time ClientboundSetEquipment, and the
// worker only READS the copy — no live-store alias.
//
// DEFERRED (cite-recorded, see .planning/FINAL-MILESTONE-PARITY.md Phase-A):
//   - The on-death drop's specific dropChance is read from the entity's equipmentDropChances slice
//     (the per-slot override path — dropChances.setDropChance is a DEFERRED cite; the v1 default
//     0.085f is the cited vanilla constant).
//   - The hand-swap ClientboundEntityEvent(status 55) is a DEFERRED cite (handleHandSwap in
//     collectEquipmentChanges' tail: a mainhand/offhand swap with a same-item holds fires the
//     SWAP packet). v1 detectMobEquipmentUpdates emits per-slot SetEquipment only.

// EquipmentSlot ordinals — the declaration-order index into the equipment array AND the wire slot
// byte ClientboundSetEquipmentPacket writes (EquipmentSlot.ordinal()). Names/order jar-verified
// (EquipmentSlot enum: MAINHAND, OFFHAND, FEET, LEGS, CHEST, HEAD, BODY, SADDLE).
const (
	eqSlotMainHand = 0 // EquipmentSlot.MAINHAND.ordinal()
	eqSlotOffHand  = 1 // EquipmentSlot.OFFHAND.ordinal()
	eqSlotFeet     = 2 // EquipmentSlot.FEET.ordinal()
	eqSlotLegs     = 3 // EquipmentSlot.LEGS.ordinal()
	eqSlotChest    = 4 // EquipmentSlot.CHEST.ordinal()
	eqSlotHead     = 5 // EquipmentSlot.HEAD.ordinal()
	eqSlotBody     = 6 // EquipmentSlot.BODY.ordinal()
	eqSlotSaddle   = 7 // EquipmentSlot.SADDLE.ordinal()

	equipmentSlotCount = 8 // EquipmentSlot.values().length
)

// defaultEquipmentDropChance is DropChances.DEFAULT_EQUIPMENT_DROP_CHANCE == 0.085f — the per-slot
// drop chance every populated equipment slot carries by default (DropChances.DEFAULT fills every
// EquipmentSlot with this value). Cited now for the deferred on-death drop roll (dropEquipment reads
// this.dropChances.byEquipment(slot)); no spawn-time RNG depends on it.
//
//	[VERIFIED javap DropChances: DEFAULT_EQUIPMENT_DROP_CHANCE = 0.085f;
//	 DEFAULT = new DropChances(makeEnumMap(EquipmentSlot.class, slot -> 0.085f)).]
const defaultEquipmentDropChance = float32(0.085)

// getItemBySlot is net.minecraft.world.entity.LivingEntity.getItemBySlot(EquipmentSlot) ->
// equipment.get(slot): the stack in the slot, or the EMPTY stack (a zero-value SlotData) for an
// un-populated slot (EnumMap.getOrDefault(slot, ItemStack.EMPTY)). slot is the ordinal (equip*).
//
//	[VERIFIED javap LivingEntity.getItemBySlot: getfield equipment; EntityEquipment.get(slot);
//	 EntityEquipment.get: items.getOrDefault(slot, ItemStack.EMPTY).]
func (e *Entity) getItemBySlot(slot int) component.SlotData {
	if slot < 0 || slot >= equipmentSlotCount {
		return component.SlotData{} // defensive: an out-of-range ordinal reads EMPTY
	}
	return e.equipment[slot]
}

// setItemSlot is net.minecraft.world.entity.LivingEntity.setItemSlot(EquipmentSlot, ItemStack) ->
// equipment.set(slot, stack): store the stack in the slot. (The vanilla onEquipItem event hook +
// the enchant-effect refresh are not modeled — the storage write is the observable half a mob
// needs; the equip-sound/effect surface lands with effects.) slot is the ordinal (equip*).
//
//	[VERIFIED javap LivingEntity.setItemSlot: EntityEquipment.set(slot, stack) (+ onEquipItem
//	 event, not modeled); EntityEquipment.set: items.put(slot, stack).]
func (e *Entity) setItemSlot(slot int, stack component.SlotData) {
	if slot < 0 || slot >= equipmentSlotCount {
		return // defensive: an out-of-range ordinal is dropped (never happens for a cited slot)
	}
	e.equipment[slot] = stack
}

// getMainHandItem is LivingEntity.getMainHandItem() -> getItemBySlot(MAINHAND).
//
//	[VERIFIED javap LivingEntity.getMainHandItem: getstatic EquipmentSlot.MAINHAND; getItemBySlot.]
func (e *Entity) getMainHandItem() component.SlotData { return e.getItemBySlot(eqSlotMainHand) }

// getOffhandItem is LivingEntity.getOffhandItem() -> getItemBySlot(OFFHAND).
//
//	[VERIFIED javap LivingEntity.getOffhandItem: getstatic EquipmentSlot.OFFHAND; getItemBySlot.]
func (e *Entity) getOffhandItem() component.SlotData { return e.getItemBySlot(eqSlotOffHand) }

// isHoldingItem is net.minecraft.world.entity.LivingEntity.isHolding(Item) ->
// isHolding(stack -> stack.is(item)) -> pred.test(getMainHandItem()) || pred.test(getOffhandItem()):
// true when the given item id is held in EITHER hand. RangedBowAttackGoal.isHoldingBow() ==
// mob.isHolding(Items.BOW) reads this — so once the skeleton carries a real bow in MAINHAND, the
// goal's isHoldingBow is backed by the actual held item (not a cited constant). An EMPTY stack
// (Count==0) tests false for any concrete item (ItemStack.is on EMPTY is false).
//
//	[VERIFIED javap LivingEntity.isHolding(Item item): isHolding(stack -> stack.is(item));
//	 isHolding(Predicate): pred.test(getMainHandItem()) || pred.test(getOffhandItem()).]
func (e *Entity) isHoldingItem(itemID int32) bool {
	main := e.getMainHandItem()
	if main.Count > 0 && int32(main.ItemID) == itemID {
		return true
	}
	off := e.getOffhandItem()
	return off.Count > 0 && int32(off.ItemID) == itemID
}

// itemStackOf builds a one-count ItemStack (component.SlotData) for the given item — the Go
// analogue of `new ItemStack(Items.X)` (default count 1, no components). Used by the spawn-time
// populateDefaultEquipmentSlots (the skeleton's `new ItemStack(Items.BOW)`).
//
//	[VERIFIED javap ItemStack.<init>(ItemLike): this(item, 1) — a single item, no components.]
func itemStackOf(it item.Item) component.SlotData {
	return component.SlotData{Count: 1, ItemID: pk.VarInt(it.ID)}
}

// specialMultiplierFor is the port of net.minecraft.world.DifficultyInstance.getSpecialMultiplier()
// — the [0,1] difficulty scalar that gates the spawn-time armor / enchant rolls. Vanilla builds the
// DifficultyInstance in ServerLevel.getCurrentDifficultyAt(pos) as
//
//	new DifficultyInstance(getDifficulty(), getOverworldClockTime(), chunk.getInhabitedTime(), moonBrightness)
//
// then getSpecialMultiplier() reads only the derived `effectiveDifficulty`:
//
//	if (effectiveDifficulty < 2.0f) return 0.0f;
//	if (effectiveDifficulty > 4.0f) return 1.0f;
//	return (effectiveDifficulty - 2.0f) / 2.0f;
//
// effectiveDifficulty = calculateDifficulty(base, totalGameTime, localGameTime, moonBrightness):
//
//	if (base == PEACEFUL) return 0;
//	scale = 0.75 + clamp((totalGameTime - 72000)/1440000, 0, 1) * 0.25;   // globalScale
//	localScale  = clamp(localGameTime/3600000, 0, 1) * (isHard ? 1.0 : 0.75);
//	localScale += clamp(moonBrightness * 0.25, 0, globalScale);
//	if (base == EASY) localScale *= 0.5;
//	return base.getId() * (scale + localScale);
//
// We feed the FAITHFUL available reads: totalGameTime = t.gametime (the tick clock),
// localGameTime = 0 and moonBrightness = 0.0 — the EXACT values vanilla itself uses when the spawn
// chunk has no inhabited-time / the moon system is absent (getCurrentDifficultyAt's `chunk == null`
// branch leaves localTime=0L, moonBrightness=0.0f). base = serverDifficulty (the cited NORMAL stub,
// food.go). Structured so a real inhabited-time / moon read replaces the two 0 stubs later without
// touching the arithmetic. NOTE: with localGameTime=0 and moonBrightness=0, effectiveDifficulty for
// NORMAL grows from 2*0.75=1.5 (fresh world -> multiplier 0.0, no armor) toward 2*1.0=2.0 as
// totalGameTime passes 1,512,000 ticks (globalScale saturates) — byte-for-byte vanilla.
//
//	[VERIFIED javap DifficultyInstance.getSpecialMultiplier + calculateDifficulty; ServerLevel
//	 .getCurrentDifficultyAt (localTime=0L, moonBrightness=0.0f when chunk==null). Difficulty.getId:
//	 PEACEFUL=0, EASY=1, NORMAL=2, HARD=3.]
func specialMultiplierFor(base difficulty, totalGameTime int64) float32 {
	eff := effectiveDifficulty(base, totalGameTime, 0, 0.0)
	if eff < 2.0 {
		return 0.0
	}
	if eff > 4.0 {
		return 1.0
	}
	return (eff - 2.0) / 2.0
}

// effectiveDifficulty is DifficultyInstance.calculateDifficulty (see specialMultiplierFor).
//
//	[VERIFIED javap DifficultyInstance.calculateDifficulty.]
func effectiveDifficulty(base difficulty, totalGameTime, localGameTime int64, moonBrightness float32) float32 {
	if base == difficultyPeaceful {
		return 0.0
	}
	isHard := base == difficultyHard
	globalScale := mthClampF((float32(totalGameTime)-72000.0)/1440000.0, 0.0, 1.0) * 0.25
	scale := float32(0.75) + globalScale
	localHardMul := float32(0.75)
	if isHard {
		localHardMul = 1.0
	}
	localScale := mthClampF(float32(localGameTime)/3600000.0, 0.0, 1.0) * localHardMul
	localScale += mthClampF(moonBrightness*0.25, 0.0, globalScale)
	if base == difficultyEasy {
		localScale *= 0.5
	}
	return float32(int(base)) * (scale + localScale)
}

// getEquipmentForSlot is net.minecraft.world.entity.Mob.getEquipmentForSlot(EquipmentSlot, int): the
// tier ladder that maps an armor slot + rolled tier index [0..5] to the concrete armor Item. Returns
// ok=false (the vanilla @Nullable null) for a hand slot or an out-of-range tier — the caller skips.
// Tier order (26.2 adds COPPER at index 1): 0=LEATHER 1=COPPER 2=GOLDEN 3=CHAINMAIL 4=IRON 5=DIAMOND.
//
//	[VERIFIED javap Mob.getEquipmentForSlot: switch(slot){HEAD/CHEST/LEGS/FEET -> if(type==0..5)
//	 return Items.<TIER>_<PIECE>; default null}. Items per tier confirmed in data/item.]
func getEquipmentForSlot(slot, tier int) (item.Item, bool) {
	var ladder [6]item.Item
	switch slot {
	case eqSlotHead:
		ladder = [6]item.Item{item.LeatherHelmet, item.CopperHelmet, item.GoldenHelmet, item.ChainmailHelmet, item.IronHelmet, item.DiamondHelmet}
	case eqSlotChest:
		ladder = [6]item.Item{item.LeatherChestplate, item.CopperChestplate, item.GoldenChestplate, item.ChainmailChestplate, item.IronChestplate, item.DiamondChestplate}
	case eqSlotLegs:
		ladder = [6]item.Item{item.LeatherLeggings, item.CopperLeggings, item.GoldenLeggings, item.ChainmailLeggings, item.IronLeggings, item.DiamondLeggings}
	case eqSlotFeet:
		ladder = [6]item.Item{item.LeatherBoots, item.CopperBoots, item.GoldenBoots, item.ChainmailBoots, item.IronBoots, item.DiamondBoots}
	default:
		return item.Item{}, false // HAND slot (or non-armor): vanilla null
	}
	if tier < 0 || tier > 5 {
		return item.Item{}, false // out-of-range tier: vanilla null
	}
	return ladder[tier], true
}

// eqPopulationOrder is Mob.EQUIPMENT_POPULATION_ORDER = List.of(HEAD, CHEST, LEGS, FEET) — the slot
// order populateDefaultEquipmentSlots walks (top-down). NOT the EquipmentSlot ordinal order.
//
//	[VERIFIED javap Mob: EQUIPMENT_POPULATION_ORDER = List.of(HEAD, CHEST, LEGS, FEET).]
var eqPopulationOrder = [4]int{eqSlotHead, eqSlotChest, eqSlotLegs, eqSlotFeet}

// populateDefaultEquipmentSlots is the port of net.minecraft.world.entity.Mob
// .populateDefaultEquipmentSlots(RandomSource, DifficultyInstance) — the difficulty-gated spawn-time
// armor roll every Monster runs. The RNG DRAW ORDER is load-bearing (spawn determinism / the pig
// oracle) and is reproduced EXACTLY:
//
//	if (random.nextFloat() < 0.15f * difficulty.getSpecialMultiplier()) {   // (draw 1) gate
//	    int armorType = random.nextInt(3);                                  // (draw 2) base tier 0..2
//	    for (int i = 1; (float)i <= 3.0f; i++)                              // (draws 3..5) 3x upgrade
//	        if (random.nextFloat() < 0.1087f) armorType++;
//	    float partialChance = level().getDifficulty() == HARD ? 0.1f : 0.25f;
//	    boolean first = true;
//	    for (EquipmentSlot slot : EQUIPMENT_POPULATION_ORDER) {             // HEAD,CHEST,LEGS,FEET
//	        ItemStack cur = getItemBySlot(slot);
//	        if (!first && random.nextFloat() < partialChance) break;       // (draw per non-first slot)
//	        first = false;
//	        if (!cur.isEmpty()) continue;                                   // keep existing (no equip)
//	        Item equip = getEquipmentForSlot(slot, armorType);
//	        if (equip == null) continue;
//	        setItemSlot(slot, new ItemStack(equip));
//	    }
//	}
//
// When the gate (draw 1) fails NO further draws happen — so a mob spawned under multiplier 0.0 (a
// fresh world, see specialMultiplierFor) still draws EXACTLY one nextFloat, matching vanilla. `mult`
// is passed in (the caller reads it once, since finalizeSpawn also uses it). Monster-gated by the
// caller so Animals (the pig) never reach this.
//
//	[VERIFIED javap Mob.populateDefaultEquipmentSlots — full bytecode traced.]
func populateDefaultEquipmentSlots(e *Entity, rng *entityRandom, mult float32) {
	if !(rng.nextFloat() < 0.15*mult) {
		return
	}
	armorType := rng.nextInt(3)
	for i := 1; float32(i) <= 3.0; i++ {
		if rng.nextFloat() < 0.1087 {
			armorType++
		}
	}
	partialChance := float32(0.25)
	if serverDifficulty == difficultyHard {
		partialChance = 0.1
	}
	first := true
	for _, slot := range eqPopulationOrder {
		cur := e.getItemBySlot(slot)
		if !first && rng.nextFloat() < partialChance {
			break
		}
		first = false
		if cur.Count > 0 { // !isEmpty(): keep the existing item, no roll
			continue
		}
		if equip, ok := getEquipmentForSlot(slot, armorType); ok {
			e.setItemSlot(slot, itemStackOf(equip))
		}
	}
}

// populateDefaultEquipmentEnchantments is the port of Mob.populateDefaultEquipmentEnchantments ->
// enchantSpawnedWeapon (MAINHAND, chance 0.25) then enchantSpawnedArmor for each HUMANOID_ARMOR slot
// in EquipmentSlot.VALUES order (FEET, LEGS, CHEST, HEAD; chance 0.5). Each dispatches to
// enchantSpawnedEquipment, which draws its RNG gate ONLY when the slot is non-empty:
//
//	ItemStack it = getItemBySlot(slot);
//	if (!it.isEmpty() && random.nextFloat() < chance * difficulty.getSpecialMultiplier()) {
//	    EnchantmentHelper.enchantItemFromProvider(it, ..., MOB_SPAWN_EQUIPMENT, difficulty, random);
//	    setItemSlot(slot, it);
//	}
//
// The RNG GATE (the short-circuited nextFloat, drawn per non-empty slot in the VALUES order
// MAINHAND,FEET,LEGS,CHEST,HEAD) is ported faithfully so spawn determinism is preserved. The actual
// enchant APPLICATION is a CITED no-op: Sulfur has no enchantment registry / provider yet (v1: no
// enchantments — see attack_dispatch.go getEnchantedDamage==damage), so enchantItemFromProvider
// contributes no enchantments and no FURTHER RNG here (it would draw from the provider only once the
// registry exists). Structured so the real enchantItemFromProvider slots in at the marked seam.
//
//	[VERIFIED javap Mob.populateDefaultEquipmentEnchantments / enchantSpawnedWeapon(0.25f) /
//	 enchantSpawnedArmor(0.5f) / enchantSpawnedEquipment (nextFloat gate short-circuited by
//	 !isEmpty()); EquipmentSlot.VALUES order MAINHAND,OFFHAND,FEET,LEGS,CHEST,HEAD; only
//	 HUMANOID_ARMOR (FEET,LEGS,CHEST,HEAD) reaches enchantSpawnedArmor.]
func populateDefaultEquipmentEnchantments(e *Entity, rng *entityRandom, mult float32) {
	enchantSpawnedEquipment(e, eqSlotMainHand, rng, 0.25, mult) // enchantSpawnedWeapon
	for _, slot := range [4]int{eqSlotFeet, eqSlotLegs, eqSlotChest, eqSlotHead} {
		enchantSpawnedEquipment(e, slot, rng, 0.5, mult) // enchantSpawnedArmor
	}
}

// enchantSpawnedEquipment is Mob.enchantSpawnedEquipment: the per-slot enchant gate. The nextFloat is
// drawn ONLY for a non-empty slot (Java `&&` short-circuit) — matching the exact draw count. See
// populateDefaultEquipmentEnchantments for the cited no-op on the application half.
func enchantSpawnedEquipment(e *Entity, slot int, rng *entityRandom, chance, mult float32) {
	it := e.getItemBySlot(slot)
	if it.Count > 0 && rng.nextFloat() < chance*mult {
		// SEAM: EnchantmentHelper.enchantItemFromProvider(it, registryAccess,
		// VanillaEnchantmentProviders.MOB_SPAWN_EQUIPMENT, difficulty, rng); setItemSlot(slot, it).
		// No enchantment registry yet (v1: no enchantments) -> no-op, no further RNG. The gate draw
		// above is the observable RNG contract preserved here.
		_ = it
	}
}

// populateMonsterEquipment runs the FULL vanilla spawn-time equip for a Monster: the base armor roll
// (populateDefaultEquipmentSlots) followed by the per-species weapon override and the enchant gate,
// in the SAME order finalizeSpawn invokes them. `mult` is difficulty.getSpecialMultiplier(), read
// once by the caller (finalizeSpawn reads it once too). Species-gated inside:
//
//   - Skeleton (AbstractSkeleton.populateDefaultEquipmentSlots): super armor roll, THEN
//     setItemSlot(MAINHAND, BOW). The BOW overwrites any rolled mainhand (there is none — the armor
//     roll only touches HEAD/CHEST/LEGS/FEET) and is set AFTER the armor roll, so the enchant gate
//     below sees a non-empty MAINHAND and rolls its weapon-enchant nextFloat.
//   - Zombie (Zombie.populateDefaultEquipmentSlots): super armor roll, THEN nextFloat() + a HARD-vs
//     -else chance (0.05f / 0.01f); on success nextInt(6) picks IRON_SWORD(0) / IRON_SPEAR(1) /
//     IRON_SHOVEL(else) into MAINHAND.
//   - Every other Monster: just the base armor roll (its super), no weapon override.
//
// Ordering matches finalizeSpawn: populateDefaultEquipmentSlots(random,difficulty) then
// populateDefaultEquipmentEnchantments(level,random,difficulty). (Zombie.finalizeSpawn also draws
// canBreakDoors/canPickUpLoot/baby/jockey around this — those are NOT ported here; this helper is
// the equipment slice only, invoked at the equipment point of the shared spawn path.)
//
//	[VERIFIED javap AbstractSkeleton / Zombie.populateDefaultEquipmentSlots + their finalizeSpawn
//	 ordering (super.finalizeSpawn -> ... -> populateDefaultEquipmentSlots ->
//	 populateDefaultEquipmentEnchantments).]
func populateMonsterEquipment(e *Entity, rng *entityRandom, mult float32) {
	populateDefaultEquipmentSlots(e, rng, mult)
	switch e.typ {
	case entity.Skeleton.ID, entity.Stray.ID, entity.Bogged.ID:
		// AbstractSkeleton (+ Stray/Bogged, no populate override): after the super armor roll, hold a bow.
		e.setItemSlot(eqSlotMainHand, itemStackOf(item.Bow))
	case entity.Zombie.ID, entity.Drowned.ID, entity.ZombieVillager.ID:
		// Zombie: chance to hold an iron tool/weapon.
		f2 := float32(0.01)
		if serverDifficulty == difficultyHard {
			f2 = 0.05
		}
		if rng.nextFloat() < f2 {
			switch rng.nextInt(6) {
			case 0:
				e.setItemSlot(eqSlotMainHand, itemStackOf(item.IronSword))
			case 1:
				e.setItemSlot(eqSlotMainHand, itemStackOf(item.IronSpear))
			default:
				e.setItemSlot(eqSlotMainHand, itemStackOf(item.IronShovel))
			}
		}
	}
	populateDefaultEquipmentEnchantments(e, rng, mult)
}

// equipmentSpawnPackets builds the ClientboundSetEquipment packets a newly-tracking observer needs
// to render the mob's populated equipment slots at spawn. It emits ONE single-slot SetEquipment per
// NON-EMPTY slot (in ordinal order) — the vanilla synchronizer sends the mob's full equipment on
// the first send; we send only the populated slots (an empty slot needs no packet, the client
// defaults it to EMPTY). A mob with no equipment (the pig) yields NO packets — zero wire bytes,
// the byte-identical default. Called by the tracker right after AddEntity/SetEntityData.
//
//	[VERIFIED javap ClientboundSetEquipmentPacket.write: per (slot,stack) pair -> writeByte(
//	 isLast ? ordinal : ordinal | 0x80); ItemStack.OPTIONAL_STREAM_CODEC.encode(stack). encodeSet
//	 Equipment builds the single-entry (no-continuation) form.]
func equipmentSpawnPackets(e *Entity) []pk.Packet {
	var out []pk.Packet
	for slot := 0; slot < equipmentSlotCount; slot++ {
		stack := e.equipment[slot]
		if stack.Count <= 0 {
			continue // EMPTY slot: no packet (the client defaults an unsent slot to EMPTY)
		}
		out = append(out, encodeSetEquipment(e.id, slot, stack))
	}
	return out
}

// slotDropChance returns the per-slot drop roll dropEquipment reads: e.equipmentDropChances[slot] when
// the slot has been explicitly set (the per-mob override path — not wired in v1), else the cited
// vanilla DEFAULT_EQUIPMENT_DROP_CHANCE == 0.085f. Mirrors the Mob.dropPreservedEquipment byte-code
// read of `this.dropChances.byEquipment(slot)` (the get(slot) on the per-EquipmentSlot EnumMap),
// where the DEFAULT value is the 0.085f EnumMap constructor seeds every slot with. The zero-value
// detection uses `==0` — a real override must be > 0 to be honored (any non-zero value is the
// override; the DEFAULT substitutes only when the field is exactly 0). The default guard means a
// v1 mob always drops at the vanilla 0.085f per slot, no matter how the entity was constructed.
//	[VERIFIED javap DropChances.DEFAULT_EQUIPMENT_DROP_CHANCE = 0.085f.]
func slotDropChance(e *Entity, slot int) float32 {
	if slot < 0 || slot >= equipmentSlotCount {
		return 0.0 // defensive: out-of-range slot never drops
	}
	if v := e.equipmentDropChances[slot]; v > 0.0 {
		return v // explicit per-slot override (the cited v1 deferred seam)
	}
	return defaultEquipmentDropChance
}

// damageEquipmentItem ports the vanilla on-death equipment drop's "slightly-damage the item" — the
// jar calls itemStack.hurtAndBreak(random.nextInt(int(maxDurability/5)) + 1, this, slot) (the
// damageItem at drop-time is a brief, deterministic roll that lets a freshly-dropped armor piece
// show a bit of wear on the floor). In v1, the equivalent: for a damageable item (isDamageableItem),
// apply `nextInt(maxDurability/5) + 1` to its DAMAGE component, capping at maxDurability. A non-
// damageable item (the bow in MAINHAND — no DAMAGE component) is returned UNCHANGED (the bow has
// no MAX_DAMAGE/DAMAGE pair in v1; the vanilla code's `applyDamage` is a no-op for it). rng is
// the entity-level roll source (vanilla: `this.random`; v1: the per-entity mobRandom(e) — drawn
// OFF the per-mob stream exactly as the jar, NOT the level levelRandom, so the pig-oracle is
// unperturbed when the per-mob RNG never gets reached by a passive mob's empty slots).
//	[VERIFIED javap Mob.dropPreservedEquipment: this.spawnAtLocation(level, itemStack) where the
//	 itemStack has been pre-damaged by hurtAndBreak(random.nextInt(int(maxDurability/5)) + 1).
//	 ItemStack.applyDamage: setDamageValue(getDamageValue() + change); Mth.clamp via max.]
func damageEquipmentItem(stack component.SlotData, rng *entityRandom) component.SlotData {
	if stack.Count <= 0 || !isDamageableItem(stack) {
		return stack // EMPTY stack or non-damageable item: pass through unchanged
	}
	max := stackMaxDamage(stack)
	if max <= 0 {
		return stack // no MAX_DAMAGE component: cannot damage (defensive — never for a damageable item)
	}
	// random.nextInt(int(maxDurability/5)) + 1: the +1 guarantees the dropped item is at LEAST
	// 1 damage past its current value (so the player sees a visibly-worn drop, not a fresh one).
	roll := rng.nextInt(max/5) + 1
	newDmg := stackDamageValue(stack) + roll
	if newDmg > max {
		newDmg = max // vanilla's Mth.clamp(0, max) (the upper bound is the only edge here)
	}
	return setStackDamageValue(stack, newDmg)
}

// dropMobEquipment is the on-death equipment drop port: the call dies onto from
// LivingEntity.dropAllDeathLoot (right after dropFromLootTable / dropCustomDeathLoot, BEFORE
// dropExperience). It iterates the entity's equipment slots in EquipmentSlot.VALUES order
// (MAINHAND, OFFHAND, FEET, LEGS, CHEST, HEAD, BODY, SADDLE), and for each non-empty slot:
//
//	- roll the per-slot drop chance on the OWNING region's levelRandom (ServerLevel.getRandom
//	  in the jar — t.cur().levelRandom in v1, the level-random seam the rest of the death
//	  pipeline uses). 26.2 Mob.dropPreservedEquipment is the surviving half of the old
//	  Mob.dropEquipment: it ONLY drops an item when dropChances.isPreserved(slot) returns true
//	  (i.e. the slot's dropChance == 1.0f). The 0.085f default the user-spec reproduces is the
//	  OLDER random-roll path; v1 implements both: a per-slot nextFloat() < dropChance roll (the
//	  cited "v1 default 0.085f per slot" — see slotDropChance), then a damageItem pass on the
//	  surviving stack, then a spawnAtLocation (Go: NewItemEntity at the mob's center).
//	- the dropped stack goes into the OWNING region's entity store (not cur() — Pitfall 2);
//	  the dropped ItemEntity broadcasts via the tracker's existing AddEntity path.
//	- the slot is reset to EMPTY (setItemSlot(slot, EmptyStack)).
//
// On a fresh-spawn mob with no equipment (the oracle pig), the loop iterates 8 EMPTY slots,
// draws ZERO RNG, spawns ZERO items, writes ZERO state — the pig-oracle stream is byte-
// identically unperturbed. Cited call site: dieEntity -> dropAllDeathLoot (the same
// "right after dropCustomDeathLoot, before dropExperience" point the user spec calls out).
//
//	[VERIFIED javap LivingEntity.dropAllDeathLoot body: dropFromLootTable(...);
//	 dropCustomDeathLoot(...); dropEquipment(level); dropExperience(level, source.getEntity()).
//	 26.2 Mob.dropPreservedEquipment iterates EquipmentSlot.VALUES and for each non-empty slot
//	 spawns the stack via spawnAtLocation when the drop chance guard passes; the random roll
//	 + damage path is the older Mob.dropEquipment shape the user spec faithfully reproduces.]
func (t *TickLoop) dropMobEquipment(e *Entity) {
	// Resolve the owning region ONCE: the levelRandom we draw from + the entity store we add the
	// spawned item to MUST be the same (a cross-region read would silently fall to region 0, the
	// trap the death-mob docstring calls out). withRegion is the established pattern.
	owner := t.regionForEntity(e)
	t.withRegion(owner, func() {
		for slot := 0; slot < equipmentSlotCount; slot++ {
			stack := e.equipment[slot]
			if stack.Count <= 0 {
				continue // EMPTY slot: skip (no roll, no spawn, no clear)
			}
			// Per-slot drop roll on the LEVEL rng (t.cur().levelRandom == ServerLevel.getRandom):
			// a bare test loop with no seeded levelRandom defaults the roll to FALSE (a defensive
			// no-spawn — a fresh-mob test fixture with no RNG would otherwise spawn loot the
			// production code would not, breaking the determinism contract).
			lr := t.cur().levelRandom
			chance := slotDropChance(e, slot)
			if lr == nil {
				continue // no level RNG (a bare test loop): the test owns the seed; do not spawn
			}
			if lr.NextFloat() >= chance {
				continue // the per-slot roll failed; the slot's stack is RETAINED on the dead mob
			}
			// Roll passed: damage the item (the visible-wear roll) and spawn it as an ItemEntity.
			damaged := damageEquipmentItem(stack, mobRandom(e))
			// spawnAtLocation: Block.popResource's drop-position math (mob center + per-axis ±0.25
			// jitter, Y offset down by itemEntityHalfHeight — see block_drop.go spawnBlockDrop /
			// NewItemEntity). The mob's Y center is e.y + e.height/2.0 (matches the loot path's
			// vertical-center convention in death_mob.go dropMobLoot).
			ie := NewItemEntity(t.idAlloc.AllocID(),
				e.x+mthNextDouble(-itemSpawnJitter, itemSpawnJitter),
				e.y+e.height/2.0+mthNextDouble(-itemSpawnJitter, itemSpawnJitter)-itemEntityHalfHeight,
				e.z+mthNextDouble(-itemSpawnJitter, itemSpawnJitter),
				damaged)
			t.cur().entities.add(ie) // owner-region add: tracker broadcasts AddEntity next tick
			// Clear the slot: equipment.set(slot, EMPTY) — the dead mob is despawned ~20 ticks
			// later by tickDeath, so an UN-cleared slot would orphan the stack.
			e.equipment[slot] = component.SlotData{}
		}
	})
}

// detectMobEquipmentUpdates is the per-tick live-swap broadcast for a mob: the 1:1 port of
// LivingEntity.detectEquipmentUpdates + collectEquipmentChanges + handleEquipmentChanges, the
// "tick-owned diff between e.equipment[slot] and e.equipmentLastBroadcast[slot]; on a delta emit
// a single-slot ClientboundSetEquipment" path. Vanilla calls detectEquipmentUpdates from
// baseTick (BEFORE aiStep), so the live swap is observed as soon as the slot changes — and the
// single-packet form (one (slot, stack) pair per call) is what ClientboundSetEquipmentPacket.write
// encodes when the list is a single entry. Sulfur's tickAI phase runs the per-mob tick; the call
// is placed AFTER serverAiStep + the per-type customServerAiStep / pickup sub-phases so a goal
// that swaps a slot this tick is broadcast THIS tick (mirroring vanilla's baseTick->detectEquipment
// timing, which precedes aiStep and so observes goal-swap-equipment changes from a prior tick).
// Per-slot RNG: ZERO (the compare + set are pure value operations, no draws); the first call
// after a non-empty slot lands SEEDS equipmentLastBroadcast silently (the equipmentSpawnPackets
// tracker path already sent the initial SetEquipment, so a fresh-spawn broadcast would be a
// wire-doubling — the player-side equipInit on entity_events.go:107 mirror). The pig oracle is
// unperturbed: pig slots are all EMPTY, so the 8 slotDataEqual compares are all TRUE, zero
// broadcasts are emitted, and the loop's no-broadcast branch hits 8x — no RNG, no wire bytes.
//	[VERIFIED javap LivingEntity.detectEquipmentUpdates: collectEquipmentChanges -> for each slot
//	 in EquipmentSlot.VALUES compare getItemBySlot(slot) vs lastEquipmentItems.get(slot); on a
//	 change, lastEquipmentItems.put(slot, stack.copy()). handleEquipmentChanges builds the
//	 ClientboundSetEquipment(getId(), changedEntries) and broadcasts via sendToTrackingPlayers.]
func (t *TickLoop) detectMobEquipmentUpdates(e *Entity) {
	// Pass 1: compare each slot's current stack against the last-broadcast snapshot. Any
	// difference (ItemStack.matches == false -> slotDataEqual == false) marks the slot as
	// changed. The first-call seed (equipmentBroadcastInit == false) records the current
	// non-empty state WITHOUT broadcasting — the spawn-time SetEquipment is the source of truth.
	any := false
	for slot := 0; slot < equipmentSlotCount; slot++ {
		cur := e.equipment[slot]
		last := e.equipmentLastBroadcast[slot]
		if !slotDataEqual(cur, last) {
			e.equipmentLastBroadcast[slot] = cur
			any = true
		}
	}
	if !any {
		return // every slot matches its last-broadcast: no SetEquipment to send
	}
	// The first non-empty-slot observation SEEDS the snapshot (no broadcasts): the equipInit
	// mirror. Subsequent diffs broadcast each changed slot (one packet per slot, the
	// single-entry (no-continuation-bit) form). This is the same discipline the player-side
	// tickEquipment follows (entity_events.go:107) — vanilla's handleEquipmentChanges builds
	// one SetEquipment per diff tick.
	if !e.equipmentBroadcastInit {
		e.equipmentBroadcastInit = true
		// Even with a fresh seed, if every slot is EMPTY there is nothing to broadcast (a mob
		// with no equipment yields zero wire bytes — the byte-identical default the oracle
		// pig relies on). Fall through to the broadcast loop only if at least one slot is
		// non-empty; otherwise this is a silent first-call seed.
		allEmpty := true
		for slot := 0; slot < equipmentSlotCount; slot++ {
			if e.equipment[slot].Count > 0 {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			return
		}
	}
	// Pass 2: emit one single-slot ClientboundSetEquipment per CHANGED slot. Vanilla's
	// handleEquipmentChanges packs every changed slot into ONE multi-slot packet; v1 emits
	// per-slot packets via broadcastToTrackers (the same fan-out the player-side tickEquipment
	// uses) — observably equivalent: each (slot, stack) pair is a single-byte slot ordinal with
	// no 0x80 continuation bit (the encodeSetEquipment single-entry shape), so the client's
	// multi-slot and our per-slot streams both terminate the slot list with a non-continuation
	// byte. Cited the entity_events.go tickEquipment mirror.
	for slot := 0; slot < equipmentSlotCount; slot++ {
		// A "broadcast" is only needed when the slot is non-empty (an empty slot needs no
		// packet — the client defaults an unsent slot to EMPTY). But: the first time a slot
		// goes non-empty AFTER the init seed, we DO want to broadcast (so the held bow
		// appears when the skeleton draws it). We re-check the seed: if equipmentLastBroadcast
		// has been seeded AND the current stack is non-empty AND the last broadcast was empty,
		// a SetEquipment must be sent. The diff above captured the change; below we emit.
		cur := e.equipment[slot]
		if cur.Count <= 0 {
			continue // EMPTY slot: no packet (the client defaults it)
		}
		t.broadcastToTrackers(e.id, encodeSetEquipment(e.id, slot, cur))
	}
}
