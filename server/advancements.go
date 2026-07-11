package server

// advancements.go - the ADVANCEMENTS system ported 1:1 from the unobfuscated 26.2
// inner jar (net.minecraft.advancements.Advancement / AdvancementHolder /
// DisplayInfo / AdvancementRequirements / AdvancementProgress / CriterionProgress
// + net.minecraft.server.PlayerAdvancements). Loads the embedded advancement tree
// (level/advancement), gives each player a per-advancement progress map, grants
// criteria, and emits ClientboundUpdateAdvancements on login and on grant.
//
// WIRE (VERIFIED javap ClientboundUpdateAdvancementsPacket.write):
//   Boolean reset
//   AdvancementHolder.LIST_STREAM_CODEC added (VarInt count; per: Identifier id + Advancement)
//   writeCollection(removed, Identifier) (VarInt count; per: Identifier)
//   writeMap(progress, Identifier, AdvancementProgress.serialize)
//   Boolean showAdvancements
// Advancement.write: writeOptional(parent, Identifier); writeOptional(display,
//   DisplayInfo); AdvancementRequirements.write; Boolean sendsTelemetryEvent.
//   (Criteria are NOT sent - only the requirements grouping.)
// DisplayInfo.serializeToNetwork: TRUSTED Component title; TRUSTED Component
//   description; ItemStackTemplate icon; writeEnum(type)=VarInt ordinal;
//   writeInt(flags: bit0 background | bit1 showToast | bit2 hidden); if background
//   present: Identifier background; Float x; Float y.
// ItemStackTemplate.STREAM_CODEC: Item holder (VarInt id+1) + VarInt count +
//   DataComponentPatch (empty = VarInt 0 added, VarInt 0 removed).
// AdvancementRequirements.write: VarInt numGroups; per group VarInt numStrings; per Utf.
// AdvancementProgress.serializeToNetwork: writeMap(criteria, Utf, CriterionProgress).
// CriterionProgress.serializeToNetwork: writeNullable(obtained) = Boolean present +
//   Long epochMillis.

import (
	"io"
	"time"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/advancement"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// advancementTree is the parsed advancement DEFINITION set, loaded ONCE at boot.
// Immutable after load (read-only on the tick), shared across all players.
type advancementTree struct {
	byID  map[string]*advancement.Advancement
	order []*advancement.Advancement
}

// loadAdvancementTree parses the embedded tree. A parse error returns an empty
// tree (never a boot crash).
func loadAdvancementTree() *advancementTree {
	t := &advancementTree{byID: make(map[string]*advancement.Advancement)}
	list, err := advancement.LoadAll()
	if err != nil {
		return t
	}
	t.order = make([]*advancement.Advancement, len(list))
	for i := range list {
		a := &list[i]
		t.byID[a.ID] = a
		t.order[i] = a
	}
	return t
}

// playerAdvancements is net.minecraft.server.PlayerAdvancements: the per-player
// progress (advancement id -> criterion name -> obtained epoch millis). Tick-owned.
type playerAdvancements struct {
	progress map[string]map[string]int64
}

func newPlayerAdvancements() *playerAdvancements {
	return &playerAdvancements{progress: make(map[string]map[string]int64)}
}

// grantCriterion is PlayerAdvancements.award: mark the criterion obtained if not
// already. Returns true only on a first-time grant.
//   [VERIFIED javap AdvancementProgress.grantProgress: getCriterion(name).grant()
//   iff !isDone(); CriterionProgress.grant sets obtained = Instant.now().]
func (pa *playerAdvancements) grantCriterion(id, criterion string) bool {
	m := pa.progress[id]
	if m == nil {
		m = make(map[string]int64)
		pa.progress[id] = m
	}
	if _, ok := m[criterion]; ok {
		return false
	}
	m[criterion] = time.Now().UnixMilli()
	return true
}

// isDone is AdvancementProgress.isDone: every requirement OR-group has at least one
// granted criterion. No requirements -> done vacuously (matches vanilla).
func (pa *playerAdvancements) isDone(a *advancement.Advancement) bool {
	m := pa.progress[a.ID]
	for _, group := range a.Requirements {
		got := false
		for _, name := range group {
			if _, ok := m[name]; ok {
				got = true
				break
			}
		}
		if !got {
			return false
		}
	}
	return true
}

// itemHolderID resolves a namespaced item id to its Item.STREAM_CODEC holder wire
// value (registry id + 1; 0 reserved for inline). Unknown -> air.
func itemHolderID(name string) int32 {
	id := indexOf(registryid.Item, name)
	if id < 0 {
		id = 0
	}
	return id + 1
}

// advIconField writes a DisplayInfo icon as an ItemStackTemplate: holder id
// (VarInt id+1) + VarInt count + empty DataComponentPatch (VarInt 0, VarInt 0).
type advIconField struct {
	holderID int32
	count    int32
}

func (f advIconField) WriteTo(w io.Writer) (int64, error) {
	var n int64
	for _, v := range []pk.VarInt{pk.VarInt(f.holderID), pk.VarInt(f.count), 0, 0} {
		m, err := v.WriteTo(w)
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// advDisplayField writes DisplayInfo.serializeToNetwork.
type advDisplayField struct {
	d advancement.Display
}

func (f advDisplayField) WriteTo(w io.Writer) (int64, error) {
	var n int64
	write := func(fe pk.FieldEncoder) error {
		m, err := fe.WriteTo(w)
		n += m
		return err
	}
	if err := write(chat.Message{Translate: f.d.Title}); err != nil {
		return n, err
	}
	if err := write(chat.Message{Translate: f.d.Description}); err != nil {
		return n, err
	}
	cnt := f.d.Icon.Count
	if cnt < 1 {
		cnt = 1
	}
	if err := write(advIconField{holderID: itemHolderID(f.d.Icon.ItemID), count: int32(cnt)}); err != nil {
		return n, err
	}
	if err := write(pk.VarInt(int32(f.d.Frame))); err != nil {
		return n, err
	}
	var flags int32
	if f.d.Background != "" {
		flags |= 1
	}
	if f.d.ShowToast {
		flags |= 2
	}
	if f.d.Hidden {
		flags |= 4
	}
	if err := write(pk.Int(flags)); err != nil {
		return n, err
	}
	if f.d.Background != "" {
		if err := write(pk.Identifier(f.d.Background)); err != nil {
			return n, err
		}
	}
	// x, y Floats: the definition JSON carries no coordinates (vanilla computes
	// them in TreeNodePosition at datapack load - DEFERRED); send 0,0. The client
	// runs its own layout pass, so the tree still renders.
	if err := write(pk.Float(0)); err != nil {
		return n, err
	}
	return n, write(pk.Float(0))
}

// advHolderField writes one AdvancementHolder: Identifier id + Advancement.write.
type advHolderField struct {
	a *advancement.Advancement
}

func (f advHolderField) WriteTo(w io.Writer) (int64, error) {
	var n int64
	write := func(fe pk.FieldEncoder) error {
		m, err := fe.WriteTo(w)
		n += m
		return err
	}
	if err := write(pk.Identifier(f.a.ID)); err != nil {
		return n, err
	}
	if f.a.Parent != "" {
		if err := write(pk.Boolean(true)); err != nil {
			return n, err
		}
		if err := write(pk.Identifier(f.a.Parent)); err != nil {
			return n, err
		}
	} else if err := write(pk.Boolean(false)); err != nil {
		return n, err
	}
	if f.a.HasDisplay {
		if err := write(pk.Boolean(true)); err != nil {
			return n, err
		}
		if err := write(advDisplayField{d: f.a.Display}); err != nil {
			return n, err
		}
	} else if err := write(pk.Boolean(false)); err != nil {
		return n, err
	}
	if err := write(pk.VarInt(int32(len(f.a.Requirements)))); err != nil {
		return n, err
	}
	for _, group := range f.a.Requirements {
		if err := write(pk.VarInt(int32(len(group)))); err != nil {
			return n, err
		}
		for _, s := range group {
			if err := write(pk.String(s)); err != nil {
				return n, err
			}
		}
	}
	return n, write(pk.Boolean(f.a.Telemetry))
}

// advProgressField writes AdvancementProgress.serializeToNetwork.
type advProgressField struct {
	criteria map[string]int64
}

func (f advProgressField) WriteTo(w io.Writer) (int64, error) {
	var n int64
	write := func(fe pk.FieldEncoder) error {
		m, err := fe.WriteTo(w)
		n += m
		return err
	}
	if err := write(pk.VarInt(int32(len(f.criteria)))); err != nil {
		return n, err
	}
	for name, obtained := range f.criteria {
		if err := write(pk.String(name)); err != nil {
			return n, err
		}
		if err := write(pk.Boolean(true)); err != nil {
			return n, err
		}
		if err := write(pk.Long(obtained)); err != nil {
			return n, err
		}
	}
	return n, nil
}

// encodeUpdateAdvancements builds ClientboundUpdateAdvancements. reset=true + the
// whole tree is the login sync; reset=false + a single changed holder is a grant delta.
func encodeUpdateAdvancements(reset bool, added []*advancement.Advancement, progress map[string]map[string]int64) pk.Packet {
	fields := make([]pk.FieldEncoder, 0, 4+len(added)+2*len(progress))
	fields = append(fields, pk.Boolean(reset))
	fields = append(fields, pk.VarInt(int32(len(added))))
	for _, a := range added {
		fields = append(fields, advHolderField{a: a})
	}
	fields = append(fields, pk.VarInt(0)) // removed set: empty
	fields = append(fields, pk.VarInt(int32(len(progress))))
	for id, crit := range progress {
		fields = append(fields, pk.Identifier(id), advProgressField{criteria: crit})
	}
	fields = append(fields, pk.Boolean(true)) // showAdvancements
	return pk.Marshal(int32(packetid.ClientboundUpdateAdvancements), fields...)
}

// encodeSelectAdvancementsTab builds ClientboundSelectAdvancementsTab.
//   [VERIFIED javap ClientboundSelectAdvancementsTabPacket: Optional<Identifier>
//   tab (writeNullable). An empty tab clears the selection.]
func encodeSelectAdvancementsTab(tab string) pk.Packet {
	if tab == "" {
		return pk.Marshal(int32(packetid.ClientboundSelectAdvancementsTab), pk.Boolean(false))
	}
	return pk.Marshal(int32(packetid.ClientboundSelectAdvancementsTab), pk.Boolean(true), pk.Identifier(tab))
}

// --- TickLoop integration: login sync, grant + toast, trigger feeds ---

// sendAdvancementsLogin sends the whole advancement tree + the player's current
// progress with reset=true (the login sync). Mirrors PlayerAdvancements.flushDirty
// called on player join with the full set. A nil tree or nil client is a no-op.
//	[VERIFIED javap PlayerList.sendPlayerPermissionLevel-adjacent join flow -> the
//	 server sends ClientboundUpdateAdvancements with reset=true + all advancements +
//	 the player's AdvancementProgress on (re)join.]
func (t *TickLoop) sendAdvancementsLogin(p *tickPlayer) {
	if t.advancements == nil || p == nil || p.client == nil || p.advancements == nil {
		return
	}
	added := t.advancements.order
	progress := make(map[string]map[string]int64, len(p.advancements.progress))
	for id, crit := range p.advancements.progress {
		if len(crit) == 0 {
			continue
		}
		cp := make(map[string]int64, len(crit))
		for name, obtained := range crit {
			cp[name] = obtained
		}
		progress[id] = cp
	}
	p.client.Send(encodeUpdateAdvancements(true, added, progress))
}

// grantAdvancementCriterion grants one criterion of one advancement to a player
// and, if newly granted, emits a delta ClientboundUpdateAdvancements (reset=false)
// carrying only that advancement + its updated progress. The client raises the
// toast itself when the advancement transitions to done AND display.show_toast is
// set (Advancement + DisplayInfo.shouldShowToast — the server does NOT send a
// separate toast packet; the toast is a client reaction to the completed
// advancement in the delta). A nil tree/player is a no-op.
//	[VERIFIED javap PlayerAdvancements.award: progress.grantProgress(name); if changed,
//	 registerListeners/unregisterListeners + this.progressChanged.add(advancement) so the
//	 next flushDirty sends a ClientboundUpdateAdvancements delta with that advancement.]
func (t *TickLoop) grantAdvancementCriterion(p *tickPlayer, id, criterion string) {
	if t.advancements == nil || p == nil || p.advancements == nil {
		return
	}
	a, ok := t.advancements.byID[id]
	if !ok {
		return
	}
	if _, hasCrit := a.Criteria[criterion]; !hasCrit {
		return
	}
	if !p.advancements.grantCriterion(id, criterion) {
		return // already granted: no delta, no toast
	}
	if p.client == nil {
		return
	}
	crit := p.advancements.progress[id]
	cp := make(map[string]int64, len(crit))
	for name, obtained := range crit {
		cp[name] = obtained
	}
	p.client.Send(encodeUpdateAdvancements(false, []*advancement.Advancement{a}, map[string]map[string]int64{id: cp}))
}

// triggerInventoryChanged is the minecraft:inventory_changed trigger feed. Called
// after a player picks up an item (the broadcastInventoryChanges seam): for every
// advancement whose inventory_changed criterion lists this item id, grant that
// criterion. This reaches the story/root ("crafting_table") + story/mine_stone +
// husbandry/plant_seed etc. class of advancements that gate on holding an item.
// Only the item-id form is matched here; a "#tag" predicate is DEFERRED (the tag
// expansion feed is not wired — cited), so a tag-only criterion is not granted.
//	[VERIFIED data: story/root criterion crafting_table trigger minecraft:inventory_changed
//	 conditions.items[0].items == "minecraft:crafting_table".]
func (t *TickLoop) triggerInventoryChanged(p *tickPlayer, itemID string) {
	if t.advancements == nil || p == nil || p.advancements == nil {
		return
	}
	for _, a := range t.advancements.order {
		for name, c := range a.Criteria {
			if c.Trigger != "minecraft:inventory_changed" {
				continue
			}
			for _, want := range c.Items {
				if want == itemID {
					t.grantAdvancementCriterion(p, a.ID, name)
					break
				}
			}
		}
	}
}

// advItemMatches reports whether the criterion Items predicate accepts itemID. An
// EMPTY Items slice is the any-item wildcard (a TriggerInstance whose ItemPredicate
// Optional is absent matches every stack) -- e.g. husbandry/root consume_item has no
// conditions. Only the literal item-id form is matched; a "#tag" entry is not expanded
// (the tag feed is not wired -- cited), so a tag-only predicate matches nothing.
//	[VERIFIED javap ItemPredicate: an absent/empty predicate .test == true; the id form
//	 tests HolderSet membership -- here the resolved id list.]
func advItemMatches(items []string, itemID string) bool {
	if len(items) == 0 {
		return true // absent ItemPredicate -> matches any item
	}
	for _, want := range items {
		if want == itemID {
			return true
		}
	}
	return false
}

// advStringMatches reports whether a single-id predicate slice accepts id. An EMPTY
// slice is the any wildcard (an absent Optional predicate matches everything). Shared
// by the entity-type (player_killed_entity) and block (placed_block) predicate checks.
func advStringMatches(want []string, id string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if w == id {
			return true
		}
	}
	return false
}

// triggerPlayerKilledEntity is the minecraft:player_killed_entity trigger feed
// (net.minecraft.advancements.triggers.KilledTrigger, CriteriaTriggers.PLAYER_KILLED_ENTITY).
// Fired from LivingEntity.die player-kill credit path (death_mob.go) with the KILLER
// ServerPlayer + the DYING entity type id. For every advancement whose
// player_killed_entity criterion entity_type predicate accepts victimType, grant it.
// A criterion with no entity_type (empty EntityTypes) is the any-mob wildcard.
//	[VERIFIED javap KilledTrigger.trigger(ServerPlayer, Entity, DamageSource): trigger(player,
//	 inst -> inst.matches(player, entity LootContext, source)); KilledTrigger.TriggerInstance
//	 .matches tests the entityPredicate (entity_properties -> minecraft:entity_type) against the
//	 killed entity. Data: adventure/kill_a_mob criterion minecraft:<mob> entity_type == that mob.]
func (t *TickLoop) triggerPlayerKilledEntity(killer *tickPlayer, victimType string) {
	if t.advancements == nil || killer == nil || killer.advancements == nil {
		return
	}
	for _, a := range t.advancements.order {
		for name, c := range a.Criteria {
			if c.Trigger != "minecraft:player_killed_entity" {
				continue
			}
			if advStringMatches(c.EntityTypes, victimType) {
				t.grantAdvancementCriterion(killer, a.ID, name)
			}
		}
	}
}

// triggerPlacedBlock is the minecraft:placed_block trigger feed
// (net.minecraft.advancements.triggers.ItemUsedOnLocationTrigger, CriteriaTriggers.PLACED_BLOCK).
// Fired from the block-place call site (block_interact.go) after the authoritative SetBlock,
// with the placing ServerPlayer + the placed block id. For every advancement whose
// placed_block criterion location.block predicate accepts blockID, grant it. A criterion
// with no block (empty Blocks) is the any-block wildcard.
//	[VERIFIED javap CriteriaTriggers.PLACED_BLOCK = ItemUsedOnLocationTrigger("placed_block");
//	 BlockItem.place tail -> setPlacedBlock(level, pos, player, stack, state) fires
//	 PLACED_BLOCK.trigger(player, pos, stack), matched against the location predicate
//	 block_state_property.block. Data: husbandry/plant_seed criterion block == the crop block.]
func (t *TickLoop) triggerPlacedBlock(p *tickPlayer, blockID string) {
	if t.advancements == nil || p == nil || p.advancements == nil {
		return
	}
	for _, a := range t.advancements.order {
		for name, c := range a.Criteria {
			if c.Trigger != "minecraft:placed_block" {
				continue
			}
			if advStringMatches(c.Blocks, blockID) {
				t.grantAdvancementCriterion(p, a.ID, name)
			}
		}
	}
}

// triggerChangedDimension is the minecraft:changed_dimension trigger feed
// (net.minecraft.advancements.triggers.ChangeDimensionTrigger, CriteriaTriggers.CHANGED_DIMENSION).
// Fired from ServerPlayer.changeDimension (dimension_travel.go) with the ServerPlayer + the
// from/to dimension keys. For every advancement whose changed_dimension criterion matches
// (from absent or == fromDim; to absent or == toDim), grant it.
//	[VERIFIED javap ChangeDimensionTrigger.trigger(ServerPlayer, ResourceKey from, ResourceKey to);
//	 ChangeDimensionTrigger.TriggerInstance.matches(from, to): if from present and from != actualFrom
//	 return false; if to present and to != actualTo return false; else true. Data: story/enter_the_nether
//	 to == minecraft:the_nether; story/enter_the_end to == minecraft:the_end.]
func (t *TickLoop) triggerChangedDimension(p *tickPlayer, fromDim, toDim string) {
	if t.advancements == nil || p == nil || p.advancements == nil {
		return
	}
	for _, a := range t.advancements.order {
		for name, c := range a.Criteria {
			if c.Trigger != "minecraft:changed_dimension" {
				continue
			}
			// TriggerInstance.matches: from absent OR from == actualFrom; to absent OR to == actualTo.
			if c.DimFrom != "" && c.DimFrom != fromDim {
				continue
			}
			if c.DimTo != "" && c.DimTo != toDim {
				continue
			}
			t.grantAdvancementCriterion(p, a.ID, name)
		}
	}
}

// triggerSleptInBed is the minecraft:slept_in_bed trigger feed
// (net.minecraft.advancements.triggers.PlayerTrigger, CriteriaTriggers.SLEPT_IN_BED). Fired from
// ServerPlayer.startSleeping (player_sleep.go) with the ServerPlayer. slept_in_bed is a bare
// PlayerTrigger (a player LootContext predicate that the vanilla tree leaves empty), so every
// slept_in_bed criterion is an unconditional grant on sleep.
//	[VERIFIED javap CriteriaTriggers.SLEPT_IN_BED = PlayerTrigger("slept_in_bed"); ServerPlayer
//	 .startSleeping -> CriteriaTriggers.SLEPT_IN_BED.trigger(this). Data: adventure/sleep_in_bed
//	 criterion slept_in_bed has no conditions (empty player predicate -> matches).]
func (t *TickLoop) triggerSleptInBed(p *tickPlayer) {
	if t.advancements == nil || p == nil || p.advancements == nil {
		return
	}
	for _, a := range t.advancements.order {
		for name, c := range a.Criteria {
			if c.Trigger != "minecraft:slept_in_bed" {
				continue
			}
			t.grantAdvancementCriterion(p, a.ID, name)
		}
	}
}

// triggerTameAnimal is the minecraft:tame_animal trigger feed
// (net.minecraft.advancements.triggers.TameAnimalTrigger, CriteriaTriggers.TAME_ANIMAL). Fired
// from TamableAnimal.tame (attack_dispatch.go wolf/cat, parrot.go) with the taming ServerPlayer.
// tame_animal optional entity predicate is absent in the vanilla tree (husbandry/tame_an_animal),
// so every tame_animal criterion is an unconditional grant on a successful tame.
//	[VERIFIED javap TameAnimalTrigger.trigger(ServerPlayer, Animal); TamableAnimal.tame ->
//	 CriteriaTriggers.TAME_ANIMAL.trigger((ServerPlayer)owner, this). Data: husbandry/tame_an_animal
//	 criterion tamed_animal has no conditions (empty entity predicate -> matches).]
func (t *TickLoop) triggerTameAnimal(p *tickPlayer) {
	if t.advancements == nil || p == nil || p.advancements == nil {
		return
	}
	for _, a := range t.advancements.order {
		for name, c := range a.Criteria {
			if c.Trigger != "minecraft:tame_animal" {
				continue
			}
			t.grantAdvancementCriterion(p, a.ID, name)
		}
	}
}

// triggerConsumeItem is the minecraft:consume_item trigger feed
// (net.minecraft.advancements.triggers.ConsumeItemTrigger, CriteriaTriggers.CONSUME_ITEM). Fired
// when a player finishes eating/drinking an item (LivingEntity.completeUsingItem -> finishUsingItem)
// with the ServerPlayer + the consumed item id. For every advancement whose consume_item criterion
// item predicate accepts itemID, grant it. An empty item predicate (husbandry/root) is the any-
// consumable wildcard. NOTE: the eat/drink completeUsingItem call site is not yet wired in v1, so
// this feed has no live caller yet -- it is the ready entry point the consume path calls when built
// (never baked away; documented gap).
//	[VERIFIED javap ConsumeItemTrigger.trigger(ServerPlayer, ItemStack); Player.eat/completeUsingItem
//	 -> CriteriaTriggers.CONSUME_ITEM.trigger(serverPlayer, stack). Data: husbandry/balanced_diet
//	 criteria item.items == the food id; husbandry/root consumed_item has no conditions.]
func (t *TickLoop) triggerConsumeItem(p *tickPlayer, itemID string) {
	if t.advancements == nil || p == nil || p.advancements == nil {
		return
	}
	for _, a := range t.advancements.order {
		for name, c := range a.Criteria {
			if c.Trigger != "minecraft:consume_item" {
				continue
			}
			if advItemMatches(c.Items, itemID) {
				t.grantAdvancementCriterion(p, a.ID, name)
			}
		}
	}
}

// triggerFishingRodHooked is the minecraft:fishing_rod_hooked trigger feed
// (net.minecraft.advancements.triggers.FishingRodHookedTrigger, CriteriaTriggers.FISHING_ROD_HOOKED).
// Fired from FishingHook.retrieve (fishing.go) with the ServerPlayer + each caught item id. For every
// advancement whose fishing_rod_hooked criterion item predicate accepts itemID, grant it.
//	[VERIFIED javap FishingRodHookedTrigger.trigger(ServerPlayer, ItemStack rod, FishingHook, Collection);
//	 FishingHook.retrieve -> CriteriaTriggers.FISHING_ROD_HOOKED.trigger((ServerPlayer)owner, rod, this,
//	 items). Data: husbandry/fishy_business criteria item.items == the fish id.]
func (t *TickLoop) triggerFishingRodHooked(p *tickPlayer, itemID string) {
	if t.advancements == nil || p == nil || p.advancements == nil {
		return
	}
	for _, a := range t.advancements.order {
		for name, c := range a.Criteria {
			if c.Trigger != "minecraft:fishing_rod_hooked" {
				continue
			}
			if advItemMatches(c.Items, itemID) {
				t.grantAdvancementCriterion(p, a.ID, name)
			}
		}
	}
}

// SetAdvancements loads the embedded advancement DEFINITION tree ONCE at boot and
// stores it on the loop (read-only thereafter). main() calls it before Run. A test
// that never calls it leaves t.advancements nil, so the login sync + triggers are
// cheap no-ops. Loading here (off the tick, before Run) keeps the boot IO off the
// hot path — the recipe-table boot discipline.
func (t *TickLoop) SetAdvancements() {
	t.advancements = loadAdvancementTree()
}
