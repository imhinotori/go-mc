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
// keeps its original textual token, then build a github.com/imhinotori/sulfur/nbt/dynbt
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

	"github.com/imhinotori/sulfur/nbt/dynbt"
)

//go:embed registries
var registryFS embed.FS

// registryDirs maps an on-disk directory under registries/ to the Minecraft
// registry identifier sent in the RegistryData packet. The send order is the
// slice order below; entries within each registry are sorted alphabetically by
// key.
//
// The set AND order below are the EXACT 29-registry sequence a real vanilla 26.2
// server emits in the Configuration state, captured byte-for-byte by the 02-04
// capture-diff (temp/captureclient against the cached 26.2 jar; see
// NET04-CAPTURE-DIFF.md). The earlier 4-registry subset (chat_type, damage_type,
// dimension_type, worldgen/biome) is NOT sufficient: the 26.2 client builds its
// registry set from what the server sends after Select Known Packs, and a real
// client hangs at "Loading terrain…" when the variant / painting / enchantment /
// instrument / dialog / timeline registries it references are absent. Sending the
// full vanilla set is the only path that matches vanilla and lets an unmodified
// client reach Play (NET-04). worldgen/biome is the only nested directory.
var registryDirs = []struct {
	dir string // relative path under registries/
	id  string // minecraft registry id
}{
	{"worldgen/biome", "minecraft:worldgen/biome"},
	{"chat_type", "minecraft:chat_type"},
	{"trim_pattern", "minecraft:trim_pattern"},
	{"trim_material", "minecraft:trim_material"},
	{"wolf_variant", "minecraft:wolf_variant"},
	{"wolf_sound_variant", "minecraft:wolf_sound_variant"},
	{"pig_variant", "minecraft:pig_variant"},
	{"pig_sound_variant", "minecraft:pig_sound_variant"},
	{"frog_variant", "minecraft:frog_variant"},
	{"cat_variant", "minecraft:cat_variant"},
	{"cat_sound_variant", "minecraft:cat_sound_variant"},
	{"cow_sound_variant", "minecraft:cow_sound_variant"},
	{"cow_variant", "minecraft:cow_variant"},
	{"chicken_sound_variant", "minecraft:chicken_sound_variant"},
	{"chicken_variant", "minecraft:chicken_variant"},
	{"zombie_nautilus_variant", "minecraft:zombie_nautilus_variant"},
	{"painting_variant", "minecraft:painting_variant"},
	{"sulfur_cube_archetype", "minecraft:sulfur_cube_archetype"},
	{"dimension_type", "minecraft:dimension_type"},
	{"damage_type", "minecraft:damage_type"},
	{"banner_pattern", "minecraft:banner_pattern"},
	{"enchantment", "minecraft:enchantment"},
	{"jukebox_song", "minecraft:jukebox_song"},
	{"instrument", "minecraft:instrument"},
	{"test_environment", "minecraft:test_environment"},
	{"test_instance", "minecraft:test_instance"},
	{"dialog", "minecraft:dialog"},
	{"world_clock", "minecraft:world_clock"},
	{"timeline", "minecraft:timeline"},
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

// BiomeFiles returns the sorted *.json file names under registries/worldgen/biome (the
// embedded 26.2 biome registry). The natural-spawner biome MobSpawnSettings parse
// (server.loadBiomeSpawners) reads each with ReadBiome to build the per-biome weighted
// spawn lists. Exposed so the server package can read the SAME embedded biome data the
// RegistryData send path uses, without a second copy.
func BiomeFiles() ([]string, error) {
	return entryFiles(path.Join("registries", "worldgen/biome"))
}

// ReadBiome returns the raw JSON bytes of one biome registry entry (name is the *.json file
// name from BiomeFiles, e.g. "plains.json"). It reads ONLY the embedded FS -- the same
// authoritative jar-derived data the RegistryData path serializes.
func ReadBiome(name string) ([]byte, error) {
	return registryFS.ReadFile(path.Join("registries", "worldgen/biome", name))
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
	return valueToNBT(raw, "")
}

// floatFields names the registry-entry fields whose VANILLA wire type is
// TAG_Float (Codec.FLOAT / Codec.floatRange), not TAG_Double. This is the
// "carry the vanilla type schema" override (plan option c) for the one case the
// lexical int-vs-decimal heuristic cannot resolve: Gson serializes both float
// and double identically (e.g. "0.1"), so a decimal token alone cannot tell
// TAG_Float from TAG_Double. Without this override, vanilla float fields would
// serialize as TAG_Double — which the fork's typed decoder rejects (DamageType
// .Exhaustion is float32) AND the real 26.2 client rejects.
//
// Sources for the float typing (vanilla codecs / fork registry/codec.go):
//   - damage_type:  exhaustion                 (DamageType.Exhaustion float32)
//   - biome climate: temperature, downfall     (Biome.ClimateSettings, Codec.FLOAT)
//   - biome:        creature_spawn_probability  (MobSpawnSettings, Codec.floatRange)
//   - biome visual: probability                 (AmbientParticleSettings, Codec.FLOAT)
//
// Fields deliberately NOT listed (they are vanilla TAG_Double and so fall
// through to the default decimal->Double path):
//   - dimension audio mood:  offset             (AmbientMoodSettings, Codec.DOUBLE)
//   - dimension audio adds:  tick_chance         (AmbientAdditionsSettings, Codec.DOUBLE)
//   - dimension:             ambient_light, coordinate_scale (Codec.DOUBLE)
//   - biome spawn_costs:     charge, energy_budget (MobSpawnCost, Codec.DOUBLE)
//
// NOTE: the 26.2 attribute system introduces new visual modifier fields (e.g.
// water_fog_end_distance.argument) whose wire type is not yet documented; they
// default to TAG_Double here. Final byte-correctness against vanilla is the
// 02-04 capture-diff (threat T-2-06); the Wave-2 tag-type guard covers the
// well-established integer/byte/float fields.
var floatFields = map[string]bool{
	"exhaustion":                 true,
	"temperature":                true,
	"downfall":                   true,
	"creature_spawn_probability": true,
	"probability":                true,
}

// valueToNBT converts a decoded JSON value (using json.Number for numbers) into
// a dynbt.Value with the correct concrete NBT tag type. fieldName is the key the
// value is stored under in its parent compound ("" for the root / list elements),
// used to apply the floatFields type override.
func valueToNBT(v any, fieldName string) (*dynbt.Value, error) {
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
		return numberToNBT(t, fieldName)

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
			child, err := valueToNBT(t[k], k)
			if err != nil {
				return nil, err
			}
			comp.Set(k, child)
		}
		return comp, nil

	case []any:
		elems := make([]*dynbt.Value, 0, len(t))
		for _, e := range t {
			// List elements inherit the parent field name so that, e.g., a list
			// of probabilities keeps the float override.
			child, err := valueToNBT(e, fieldName)
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

// numberToNBT chooses the concrete numeric NBT tag for a JSON number token:
//   - a decimal-form token (contains '.', 'e' or 'E') is a floating-point value.
//     It is TagFloat if its field name is in floatFields (vanilla Codec.FLOAT),
//     otherwise TagDouble. This is the only place the lexical heuristic needs the
//     schema override, since Gson cannot distinguish float from double textually.
//   - an integral token is TagInt (TagLong only if it overflows int32, which does
//     not occur in the four embedded registries but is handled defensively).
func numberToNBT(n json.Number, fieldName string) (*dynbt.Value, error) {
	s := n.String()
	if strings.ContainsAny(s, ".eE") {
		f, err := n.Float64()
		if err != nil {
			return nil, fmt.Errorf("bad float token %q: %w", s, err)
		}
		if floatFields[fieldName] {
			return dynbt.NewFloat(float32(f)), nil
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
