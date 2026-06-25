package feature

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
)

// ResolveBlockStateJSON decodes a {Name, Properties} block-state object from raw JSON
// and resolves it to a StateID via the shared resolveBlockState path. It is the
// EXPORTED entry point the Phase-12 feature bodies (OreFeature target states, etc.)
// use to resolve config block refs without re-deriving the {Name,Properties} shape.
// An empty payload, unknown block, or unknown property errors LOUDLY (FEAT-02).
func ResolveBlockStateJSON(raw json.RawMessage) (block.StateID, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("block state ref is empty")
	}
	var ref blockStateJSON
	if err := json.Unmarshal(raw, &ref); err != nil {
		return 0, fmt.Errorf("decoding block state ref: %w", err)
	}
	return resolveBlockState(ref)
}

// blockStateJSON is a {Name, Properties} block reference as it appears in the
// feature config JSON (e.g. {"Name":"minecraft:oak_log","Properties":{"axis":"y"}}).
// Properties values are strings; an empty/absent Properties means the registry
// default state. This is the SAME shape surface/rules.go's blockStateJSON uses
// (jar-confirmed in oak.json), so the resolver below is ported verbatim.
type blockStateJSON struct {
	Name       string            `json:"Name"`
	Properties map[string]string `json:"Properties,omitempty"`
}

// resolveBlockState resolves a {Name, Properties} block reference to its StateID,
// ported VERBATIM from world/levelgen/surface/rules.go (the single block-state
// resolution path the whole codebase shares). With no properties it is the registry
// default; with properties (string values) it encodes them as an NBT compound and
// decodes them onto the typed Block (matching level/block.State.Block) before the
// ToStateID lookup. An unknown block or property errors LOUDLY (FEAT-02).
func resolveBlockState(bs blockStateJSON) (block.StateID, error) {
	if bs.Name == "" {
		return 0, fmt.Errorf("block state ref has no Name")
	}
	base, ok := block.FromID[bs.Name]
	if !ok {
		return 0, fmt.Errorf("unknown block %q", bs.Name)
	}
	if len(bs.Properties) == 0 {
		sid, ok := block.ToStateID[base]
		if !ok {
			return 0, fmt.Errorf("no state id for default block %q", bs.Name)
		}
		return sid, nil
	}
	// nbt.Marshal(map[string]string) emits [TagCompound, nameLen(2 bytes), body...];
	// State.Properties wants {Type, Data=body} so strip the 3-byte name header.
	data, err := nbt.Marshal(bs.Properties)
	if err != nil {
		return 0, fmt.Errorf("encode properties for %q: %w", bs.Name, err)
	}
	if len(data) < 3 {
		return 0, fmt.Errorf("encoded properties for %q too short", bs.Name)
	}
	st := block.State{Name: bs.Name, Properties: nbt.RawMessage{Type: data[0], Data: data[3:]}}
	resolved, err := st.Block()
	if err != nil {
		return 0, fmt.Errorf("resolve %q with properties %v: %w", bs.Name, bs.Properties, err)
	}
	sid, ok := block.ToStateID[resolved]
	if !ok {
		return 0, fmt.Errorf("no state id for %q with properties %v", bs.Name, bs.Properties)
	}
	return sid, nil
}
