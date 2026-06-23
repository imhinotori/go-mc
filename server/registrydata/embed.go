// Package registrydata embeds the authoritative Minecraft 26.2 registry content
// (dimension_type, worldgen/biome, damage_type, chat_type) and converts it to
// network-format NBT for the Configuration-state RegistryData send path.
//
// WHY THIS PACKAGE EXISTS (the stale-schema trap, NET-04):
//
// The client-side decode struct in registry/codec.go (registry.Dimension etc.)
// is a PRE-26.2 flat schema: it lacks the nested "attributes"/"default_clock"/
// "has_ender_dragon_fight"/"timelines" fields that real 26.2 dimension_type
// entries carry. Serializing that struct on the SERVER SEND path produces a
// registry the 26.2 client silently rejects ("Loading terrain…" hang). So the
// send path must use the REAL 26.2 jar-derived content, embedded here, NOT the
// stale struct.
//
// THE NBT-TYPING TRAP (why we do NOT use a json -> map[string]any -> nbt bridge):
//
// encoding/json decodes EVERY JSON number into float64 when the target is
// map[string]any/any, and the fork's nbt encoder maps float64 -> TagDouble.
// A naive bridge therefore serializes integer fields (min_y, tick_delay,
// monster_spawn_block_light_limit, …) as TagDouble -> a structurally malformed
// registry the client silently rejects.
//
// CONVERSION APPROACH (plan option "c" — carry the vanilla type schema, applied
// lexically): we decode the JSON with json.Decoder + UseNumber() so every number
// keeps its original textual token, then build a github.com/imhinotori/go-mc/nbt/dynbt
// tree where we choose the concrete NBT tag type per value:
//
//	JSON bool            -> TagByte   (Minecraft booleans are NBT bytes)
//	JSON string          -> TagString
//	JSON number with '.'/'e' (float/double form) -> TagDouble
//	JSON number, integral form                   -> TagInt
//	JSON object          -> TagCompound
//	JSON array           -> TagList
//
// This is TYPE-FAITHFUL because Minecraft's datagen (Gson) serializer is itself
// type-faithful: float/double fields are always emitted with a decimal point
// (e.g. coordinate_scale: 1.0, ambient_light: 0.0, offset: 2.0) while integer
// fields are emitted with no decimal (min_y: -64, tick_delay: 6000, weight: 10).
// Verified across all 128 embedded entries: no float/double field is serialized
// without a decimal point, so a bare-integer token always denotes a vanilla
// TagInt. The resulting dynbt tree carries exactly the vanilla wire tag types,
// which the Wave-2 TestRegistryNBTTagTypes guard asserts (TagInt/TagByte, never
// TagDouble for integer/byte fields).
//
// The runtime reads ONLY the embedded FS — never the gitignored build-time
// datagen tree the source was extracted from.
package registrydata

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/imhinotori/go-mc/nbt/dynbt"
)

//go:embed registries
var registryFS embed.FS

// registryDirs maps an on-disk directory under registries/ to the Minecraft
// registry identifier sent in the RegistryData packet. The send order is the
// slice order below; entries within each registry are sorted alphabetically by
// key. This order is reproducible from the embedded tree — the 02-04 capture-diff
// confirms the exact set/order vanilla emits (worldgen/biome is the only nested
// directory, hence the explicit relative paths).
var registryDirs = []struct {
	dir string // relative path under registries/
	id  string // minecraft registry id
}{
	{"chat_type", "minecraft:chat_type"},
	{"damage_type", "minecraft:damage_type"},
	{"dimension_type", "minecraft:dimension_type"},
	{"worldgen/biome", "minecraft:worldgen/biome"},
}

// Entry is one registry entry: its key (e.g. "minecraft:plains") and its
// payload as a type-faithful network-NBT tree.
type Entry struct {
	Key string
	NBT *dynbt.Value
}

// Registry is one registry id plus its ordered entries.
type Registry struct {
	ID      string
	Entries []Entry
}

// Load reads the embedded registry tree and converts every entry to a
// type-faithful network-NBT (dynbt) tree. The returned slice is in send order.
// It has no external filesystem or build-time-artifact dependency — everything
// is read from the embedded FS.
func Load() ([]Registry, error) {
	out := make([]Registry, 0, len(registryDirs))
	for _, rd := range registryDirs {
		reg := Registry{ID: rd.id}
		base := path.Join("registries", rd.dir)

		names, err := entryFiles(base)
		if err != nil {
			return nil, fmt.Errorf("registrydata: list %s: %w", rd.id, err)
		}

		for _, name := range names {
			data, err := registryFS.ReadFile(path.Join(base, name))
			if err != nil {
				return nil, fmt.Errorf("registrydata: read %s/%s: %w", rd.id, name, err)
			}
			val, err := jsonToNBT(data)
			if err != nil {
				return nil, fmt.Errorf("registrydata: convert %s/%s: %w", rd.id, name, err)
			}
			key := "minecraft:" + strings.TrimSuffix(name, ".json")
			reg.Entries = append(reg.Entries, Entry{Key: key, NBT: val})
		}
		out = append(out, reg)
	}
	return out, nil
}

// entryFiles returns the *.json file names directly under base, sorted.
func entryFiles(base string) ([]string, error) {
	ents, err := fs.ReadDir(registryFS, base)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// jsonToNBT decodes a single registry-entry JSON document into a type-faithful
// dynbt tree. The root of every registry entry is a JSON object -> TagCompound.
func jsonToNBT(data []byte) (*dynbt.Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // keep original number tokens so we can distinguish int vs double

	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return valueToNBT(raw)
}

// valueToNBT converts a decoded JSON value (using json.Number for numbers) into
// a dynbt.Value with the correct concrete NBT tag type.
func valueToNBT(v any) (*dynbt.Value, error) {
	switch t := v.(type) {
	case nil:
		// No NBT null tag exists; registry entries never contain JSON null in
		// the 26.2 datapack data. Represent as an empty compound defensively so
		// a stray null does not silently become a TagDouble.
		return dynbt.NewCompound(), nil

	case bool:
		// Minecraft NBT has no boolean tag; booleans are TagByte (0/1).
		return dynbt.NewBoolean(t), nil

	case string:
		return dynbt.NewString(t), nil

	case json.Number:
		return numberToNBT(t)

	case map[string]any:
		comp := dynbt.NewCompound()
		// Preserve a deterministic field order (alphabetical). NBT compound
		// field order is not wire-significant, but determinism keeps the
		// embedded output reproducible for the capture-diff.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child, err := valueToNBT(t[k])
			if err != nil {
				return nil, err
			}
			comp.Set(k, child)
		}
		return comp, nil

	case []any:
		elems := make([]*dynbt.Value, 0, len(t))
		for _, e := range t {
			child, err := valueToNBT(e)
			if err != nil {
				return nil, err
			}
			elems = append(elems, child)
		}
		return dynbt.NewList(elems...), nil

	default:
		return nil, fmt.Errorf("unsupported JSON value type %T", v)
	}
}

// numberToNBT chooses TagInt vs TagDouble (vs TagLong for out-of-range ints)
// from the LEXICAL form of the JSON number token. A token containing '.', 'e'
// or 'E' is a float/double in Minecraft's type-faithful serialization; an
// integral token is a TagInt (TagLong only if it overflows int32, which does
// not occur in the four embedded registries but is handled defensively).
func numberToNBT(n json.Number) (*dynbt.Value, error) {
	s := n.String()
	if strings.ContainsAny(s, ".eE") {
		f, err := n.Float64()
		if err != nil {
			return nil, fmt.Errorf("bad float token %q: %w", s, err)
		}
		return dynbt.NewDouble(f), nil
	}
	i, err := n.Int64()
	if err != nil {
		return nil, fmt.Errorf("bad integer token %q: %w", s, err)
	}
	if i < int64(int32(-1<<31)) || i > int64(int32(^uint32(0)>>1)) {
		return dynbt.NewLong(i), nil
	}
	return dynbt.NewInt(int32(i)), nil
}
