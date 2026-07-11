// Package advancement holds the parsed vanilla advancement DEFINITION tree (the
// data/advancement/** JSONs extracted from the 26.2 inner jar) plus the model
// types the server-side PlayerAdvancements + the ClientboundUpdateAdvancements
// wire read. It is the level/recipe + level/loot twin: the DATA + PARSE stays in
// Go over the embedded jar JSON.
//
// SCOPE: this package embeds the NON-recipe advancement tree (adventure/end/
// husbandry/nether/story == the 126 advancements the client shows in the tree
// screen). The 1562 minecraft:recipes/** advancements are hidden auto-grants that
// do not appear in the tree screen and are DEFERRED (cited in the server-side
// grant path).
//
// The model mirrors net.minecraft.advancements.Advancement + DisplayInfo +
// AdvancementRequirements 1:1 for the fields that cross the network
// (ClientboundUpdateAdvancementsPacket.write): parent, display, requirements,
// sendsTelemetryEvent. The per-criterion trigger CONDITIONS are parsed only far
// enough to feed the wired triggers (trigger id + the inventory item ids); the
// full condition graph is not needed for the tree/progress wire.
package advancement

// Type is net.minecraft.advancements.AdvancementType (the display frame). The
// ORDINAL is the wire value (DisplayInfo.serializeToNetwork -> writeEnum(type)).
//
//	[VERIFIED javap net.minecraft.advancements.AdvancementType enum declaration
//	 order: TASK=0, CHALLENGE=1, GOAL=2.]
type Type int32

const (
	TypeTask      Type = 0
	TypeChallenge Type = 1
	TypeGoal      Type = 2
)

// TypeFromString maps the display.frame JSON string to the enum. An absent frame
// defaults to "task" (vanilla DisplayInfo.CODEC default), so the caller passes ""
// for a missing frame.
func TypeFromString(s string) Type {
	switch s {
	case "challenge":
		return TypeChallenge
	case "goal":
		return TypeGoal
	default:
		return TypeTask
	}
}

// Icon is the DisplayInfo.icon: an ItemStackTemplate. For the advancement tree
// the icon is a bare item id with an optional count and (rarely) no components.
// The wire form is ItemStackTemplate.STREAM_CODEC == Item.STREAM_CODEC(holder id)
// + VarInt count + DataComponentPatch. We carry only the item id + count; the
// component patch is EMPTY for every vanilla advancement icon in the embedded
// tree (verified: no icon JSON carries a `components` field), so the encoder
// writes the empty patch (VarInt 0, VarInt 0).
//
//	[VERIFIED javap ItemStackTemplate.STREAM_CODEC static: composite(Item.STREAM_CODEC,
//	 ByteBufCodecs.VAR_INT, DataComponentPatch.STREAM_CODEC).]
type Icon struct {
	ItemID string // e.g. "minecraft:grass_block"
	Count  int    // >=1; JSON "count" defaults to 1 when absent
}

// Display is net.minecraft.advancements.DisplayInfo (the tree cell). Only the
// network-serialized fields are modeled. announceChat is parsed but NOT written
// (DisplayInfo.serializeToNetwork does not send it — verified in the write
// disassembly: only title/description/icon/type/flags/background/x/y).
type Display struct {
	Title       string // translate key (all vanilla titles are {"translate": key})
	Description string // translate key
	Icon        Icon
	Frame       Type
	Background  string // "" == none (Optional<ResourceTexture>); e.g. "minecraft:gui/advancements/backgrounds/stone"
	ShowToast   bool
	Hidden      bool
}

// Advancement is net.minecraft.advancements.Advancement for the fields that cross
// the network + the trigger feed. Criteria maps a criterion NAME to its trigger
// id (+ the inventory item ids for inventory_changed). Requirements is the
// AND-of-OR grouping (a list of OR-groups of criterion names).
type Advancement struct {
	ID           string // the advancement id, e.g. "minecraft:story/root"
	Parent       string // "" == root (Optional<Identifier> empty)
	HasDisplay   bool
	Display      Display
	Criteria     map[string]Criterion
	Requirements [][]string // AND of ORs (each inner list is one OR-group)
	Telemetry    bool
}

// IsRoot mirrors Advancement.isRoot() == parent.isEmpty().
func (a *Advancement) IsRoot() bool { return a.Parent == "" }

// Criterion carries the trigger id + the parsed CONDITION fields the WIRED triggers
// read. Only the fields the wired triggers consult are parsed; other trigger
// conditions are ignored (the criterion still exists so the requirements grouping
// stays intact, but only wired triggers can grant it). The parsed fields mirror the
// vanilla TriggerInstance predicate fields 1:1 for the wired triggers:
//
//   - inventory_changed / consume_item / fishing_rod_hooked : Items -- the item ids
//     an ItemPredicate accepts (an EMPTY Items means "any item", matching a
//     TriggerInstance with an empty/absent ItemPredicate that matches every stack).
//   - player_killed_entity : EntityTypes -- the entity-type ids the entity_properties
//     predicate's minecraft:entity_type accepts (EMPTY means "any entity").
//   - placed_block : Blocks -- the block ids the ItemUsedOnLocationTrigger location
//     predicate's block_state_property accepts (EMPTY means "any block").
//   - changed_dimension : DimTo / DimFrom -- the ChangeDimensionTrigger to/from
//     dimension keys ("" means the Optional is absent -> that side matches any).
//
// A criterion with NO parsed condition fields (all slices empty, DimTo/DimFrom "")
// is a WILDCARD: the wired trigger for its id grants it unconditionally, matching a
// TriggerInstance whose predicate Optionals are all empty (slept_in_bed, tame_animal,
// and the husbandry/root consume_item all have empty conditions).
type Criterion struct {
	Trigger     string   // e.g. "minecraft:inventory_changed"
	Items       []string // item ids for inventory_changed/consume_item/fishing_rod_hooked ("#tag" left as-is)
	EntityTypes []string // entity-type ids for player_killed_entity (entity_properties -> entity_type)
	Blocks      []string // block ids for placed_block (location -> block_state_property.block)
	DimTo       string   // changed_dimension "to" ("" == Optional absent -> matches any)
	DimFrom     string   // changed_dimension "from" ("" == Optional absent -> matches any)
}
