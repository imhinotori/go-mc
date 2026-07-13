// tool.go contains helper types for the Tool data component.
package component

import (
	"io"

	pk "github.com/imhinotori/sulfur/net/packet"
)

type ToolRule struct {
	Blocks               IDSet
	Speed                pk.Option[pk.Float, *pk.Float]
	CorrectDropForBlocks pk.Option[pk.Boolean, *pk.Boolean]
}

func (r *ToolRule) ReadFrom(rd io.Reader) (n int64, err error) {
	return pk.Tuple{&r.Blocks, &r.Speed, &r.CorrectDropForBlocks}.ReadFrom(rd)
}

func (r ToolRule) WriteTo(w io.Writer) (n int64, err error) {
	return pk.Tuple{&r.Blocks, &r.Speed, &r.CorrectDropForBlocks}.WriteTo(w)
}

// ---------------------------------------------------------------------------
// Plain-Go default Tool component (DefaultTool table in tool_defaults_gen.go)
// ---------------------------------------------------------------------------
//
// ToolData / ToolRuleData are the resolved, tag-name-keyed form of the Tool data component's
// default value (vanilla Item.components() DataComponents.TOOL), as opposed to the wire Tool /
// ToolRule types above (which carry HolderSet-as-IDSet + pk.Option and are only for decoding a
// client-sent component PATCH). DefaultTool[itemName] returns a ToolData; a rule's Blocks is the
// list of block references it matches (a "#tag" name resolved via data/tag.BlockTags, or a bare
// "minecraft:x" concrete block id). HasSpeed / HasCorrectForDrops model the vanilla
// Optional<Float> speed / Optional<Boolean> correctForDrops (a rule may carry only one).
//
// The getMiningSpeed / isCorrectForDrops readers live in the server package (server/block_break.go)
// alongside their block-break callers, because they need block-state->resource-id resolution
// (level/block) that this leaf-level component package does not import. Cite
// net.minecraft.world.item.component.Tool (rules / defaultMiningSpeed / damagePerBlock /
// canDestroyBlocksInCreative) + Tool.Rule (blocks / speed / correctForDrops).

// ToolRuleData is one resolved Tool.Rule: the block references it matches plus its optional speed
// and correct-for-drops flags. Cite Tool.Rule.
type ToolRuleData struct {
	Blocks             []string // "#minecraft:mineable/shovel" (tag) or "minecraft:cobweb" (block id)
	Speed              float32  // valid only when HasSpeed
	HasSpeed           bool     // Optional<Float> speed present
	CorrectForDrops    bool     // valid only when HasCorrectForDrops
	HasCorrectForDrops bool     // Optional<Boolean> correctForDrops present
}

// ToolData is the resolved default Tool component of an item. Cite Tool.
type ToolData struct {
	Rules                      []ToolRuleData
	DefaultMiningSpeed         float32
	DamagePerBlock             int
	CanDestroyBlocksInCreative bool
}
