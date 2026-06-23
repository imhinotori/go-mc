package registrydata

// Update Tags (ClientboundConfigUpdateTags) for the 26.2 Configuration state.
//
// WHY THIS EXISTS (the unbound-tag trap, NET-04):
//
// A real vanilla 26.2 client validates, at Registry Loading time, that every tag
// REFERENCED by a registry entry it just received is actually BOUND by an Update
// Tags packet. Ender's RegistryData entries reference tags directly — e.g.
// dimension_type entries carry timelines:"#minecraft:in_overworld", enchantment
// entries carry exclusive_set:"#minecraft:exclusive_set/armor", and the
// sulfur_cube_archetype entries carry items:"#minecraft:sulfur_cube_archetype/bouncy".
// If those tags are not bound, the client aborts Registry Loading with
// "Unbound tags in registry …" and "Failed to parse value … from server".
//
// Sending an EMPTY Update Tags (VarInt 0) — the previous v1 stub — is therefore
// NOT sufficient: present-but-empty binds nothing, so every referenced tag stays
// unbound and the client crashes. The ONLY fix is to send the real vanilla tag
// set so the referenced tags bind.
//
// GROUND TRUTH (the 02-04 capture-diff): a real vanilla 26.2 server sends Update
// Tags for exactly 15 registries with these tagCounts —
//
//	block=265 item=224 worldgen/biome=68 entity_type=48 damage_type=34
//	enchantment=22 banner_pattern=11 fluid=6 game_event=5 timeline=4
//	instrument=3 point_of_interest_type=3 dialog=2 painting_variant=1 potion=1
//
// The embedded tags/ tree (//go:embed below) is the authoritative 26.2 datagen
// tag JSON for exactly those 15 registries, copied from the build-time datagen
// output into a committed, embedded path. The runtime reads ONLY the embedded FS,
// never the gitignored build-time tree.
//
// THE WIRE FORMAT (ClientboundUpdateTags, config state):
//
//	VarInt           registryCount
//	per registry:
//	  Identifier     registryId
//	  VarInt         tagCount
//	  per tag:
//	    Identifier   tagName            (e.g. minecraft:exclusive_set/armor)
//	    VarInt       entryCount
//	    entryCount × VarInt             (the NUMERIC network index of each entry)
//
// THE INDEX RESOLUTION (entry resource-location -> numeric network index):
//
//   - Built-in registries (block, item, entity_type, fluid, game_event,
//     point_of_interest_type, potion): the index is the position of the entry in
//     the generated data/registryid table — the authoritative network id order
//     extracted from the 26.2 jar.
//   - Datapack registries (worldgen/biome, damage_type, enchantment,
//     banner_pattern, instrument, painting_variant, dialog, timeline): the index
//     is the entry's position in that registry's RegistryData send order, which
//     Load() defines as the alphabetical (sorted) order of the embedded entry
//     filenames. resolveDatapackIndices below reproduces exactly that order.
//
// TAG-OF-TAGS: a tag value may be "#minecraft:other_tag" (a reference to another
// tag in the same registry). The wire format has no tag-reference — only numeric
// ids — so such references are EXPANDED to the union of their leaf entry indices
// (recursively, de-duplicated), exactly as vanilla flattens them.

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/imhinotori/go-mc/data/packetid"
	"github.com/imhinotori/go-mc/data/registryid"
	pk "github.com/imhinotori/go-mc/net/packet"
)

//go:embed tags
var tagFS embed.FS

// tagRegistry describes one registry's tag set: the on-disk directory under
// tags/, the minecraft registry id sent on the wire, and the source of the
// numeric network index for each member.
type tagRegistry struct {
	dir string // relative path under tags/
	id  string // minecraft registry id (wire Identifier)

	// builtinOrder is the authoritative network-id order for a BUILT-IN registry
	// (from data/registryid). Exactly one of builtinOrder / datapackDir is set.
	builtinOrder []string

	// datapackDir is the registries/ directory whose sorted entry filenames define
	// the RegistryData send order (== network index) for a DATAPACK registry.
	datapackDir string
}

// tagRegistries is the EXACT 15-registry Update Tags set a real vanilla 26.2
// server sends in the Configuration state (02-04 capture-diff). The order here is
// not wire-significant (each registry is self-describing via its Identifier), but
// it is kept stable for a reproducible capture-diff.
var tagRegistries = []tagRegistry{
	// Built-in registries: index from the generated data/registryid tables.
	{dir: "block", id: "minecraft:block", builtinOrder: registryid.Block},
	{dir: "item", id: "minecraft:item", builtinOrder: registryid.Item},
	{dir: "entity_type", id: "minecraft:entity_type", builtinOrder: registryid.EntityType},
	{dir: "fluid", id: "minecraft:fluid", builtinOrder: registryid.Fluid},
	{dir: "game_event", id: "minecraft:game_event", builtinOrder: registryid.GameEvent},
	{dir: "point_of_interest_type", id: "minecraft:point_of_interest_type", builtinOrder: registryid.PointOfInterestType},
	{dir: "potion", id: "minecraft:potion", builtinOrder: registryid.Potion},

	// Datapack registries: index from the RegistryData send order (sorted embedded
	// entry filenames, matching Load()).
	{dir: "worldgen/biome", id: "minecraft:worldgen/biome", datapackDir: "worldgen/biome"},
	{dir: "damage_type", id: "minecraft:damage_type", datapackDir: "damage_type"},
	{dir: "enchantment", id: "minecraft:enchantment", datapackDir: "enchantment"},
	{dir: "banner_pattern", id: "minecraft:banner_pattern", datapackDir: "banner_pattern"},
	{dir: "instrument", id: "minecraft:instrument", datapackDir: "instrument"},
	{dir: "painting_variant", id: "minecraft:painting_variant", datapackDir: "painting_variant"},
	{dir: "dialog", id: "minecraft:dialog", datapackDir: "dialog"},
	{dir: "timeline", id: "minecraft:timeline", datapackDir: "timeline"},
}

// tagFile is the on-disk JSON shape of a single tag: a list of member values,
// each either a plain resource-location ("minecraft:water") or a tag reference
// ("#minecraft:other_tag"). The 26.2 datagen tag JSON for these 15 registries
// uses no object-form ({id, required}) entries and no "replace" flag (verified
// against the embedded tree), so a []string of values is sufficient.
type tagFile struct {
	Values []string `json:"values"`
}

// resolvedTag is one tag ready for the wire: its name and the numeric entry
// indices (sorted ascending, de-duplicated) after expanding any #tag references.
type resolvedTag struct {
	name    string
	entries []int32
}

// resolvedRegistry is one registry's tag set ready for the wire.
type resolvedRegistry struct {
	id   string
	tags []resolvedTag
}

// loadTags resolves all 15 registries' tags into wire-ready form. It builds, per
// registry, an entry-name -> network-index map (from the built-in registryid
// order or the datapack send order), then for each tag expands tag-of-tag
// references and maps every leaf member to its index.
//
// A member referencing an entry NOT present in the registry, or a #tag reference
// that cannot be resolved, is a HARD error: the client would reject the registry,
// so we surface it at send time rather than ship a silently-broken tag set.
func loadTags() ([]resolvedRegistry, error) {
	out := make([]resolvedRegistry, 0, len(tagRegistries))
	for _, tr := range tagRegistries {
		index, err := tr.entryIndex()
		if err != nil {
			return nil, fmt.Errorf("registrydata: tag index %s: %w", tr.id, err)
		}

		base := path.Join("tags", tr.dir)
		tagNames, files, err := tagFilesUnder(base)
		if err != nil {
			return nil, fmt.Errorf("registrydata: list tags %s: %w", tr.id, err)
		}

		// Parse every tag file once so #tag references can be expanded.
		raw := make(map[string]tagFile, len(files))
		for i, name := range tagNames {
			data, err := tagFS.ReadFile(files[i])
			if err != nil {
				return nil, fmt.Errorf("registrydata: read tag %s/%s: %w", tr.id, name, err)
			}
			var tf tagFile
			if err := json.Unmarshal(data, &tf); err != nil {
				return nil, fmt.Errorf("registrydata: parse tag %s/%s: %w", tr.id, name, err)
			}
			raw[name] = tf
		}

		reg := resolvedRegistry{id: tr.id}
		for _, name := range tagNames {
			idxSet := map[int32]struct{}{}
			if err := expandTag(tr.id, name, raw, index, idxSet, map[string]bool{}); err != nil {
				return nil, err
			}
			entries := make([]int32, 0, len(idxSet))
			for ix := range idxSet {
				entries = append(entries, ix)
			}
			sort.Slice(entries, func(a, b int) bool { return entries[a] < entries[b] })
			reg.tags = append(reg.tags, resolvedTag{name: "minecraft:" + name, entries: entries})
		}
		out = append(out, reg)
	}
	return out, nil
}

// expandTag adds the leaf entry indices of tag `name` (within registry regID) to
// idxSet, recursively expanding any "#minecraft:other_tag" reference. seen guards
// against cyclic tag references.
func expandTag(regID, name string, raw map[string]tagFile, index map[string]int32, idxSet map[int32]struct{}, seen map[string]bool) error {
	if seen[name] {
		return nil // already expanded on this path; break cycles defensively
	}
	seen[name] = true

	tf, ok := raw[name]
	if !ok {
		return fmt.Errorf("registrydata: tag %s references missing tag #minecraft:%s", regID, name)
	}
	for _, v := range tf.Values {
		if strings.HasPrefix(v, "#") {
			refName := strings.TrimPrefix(strings.TrimPrefix(v, "#"), "minecraft:")
			if err := expandTag(regID, refName, raw, index, idxSet, seen); err != nil {
				return err
			}
			continue
		}
		ix, ok := index[v]
		if !ok {
			return fmt.Errorf("registrydata: tag minecraft:%s in %s references unknown entry %q", name, regID, v)
		}
		idxSet[ix] = struct{}{}
	}
	return nil
}

// entryIndex returns the entry resource-location -> network-index map for the
// registry: either the built-in registryid order or the datapack send order.
func (tr tagRegistry) entryIndex() (map[string]int32, error) {
	if tr.builtinOrder != nil {
		m := make(map[string]int32, len(tr.builtinOrder))
		for i, name := range tr.builtinOrder {
			m[name] = int32(i)
		}
		return m, nil
	}
	return resolveDatapackIndices(tr.datapackDir)
}

// resolveDatapackIndices reproduces Load()'s send order for a datapack registry:
// the sorted entry filenames under registries/<dir>, keyed "minecraft:<name>".
// This MUST stay in lockstep with embed.go's Load (sorted filenames) so a tag
// member's index equals the position the entry occupies in the RegistryData
// packet Ender actually sends.
func resolveDatapackIndices(dir string) (map[string]int32, error) {
	base := path.Join("registries", dir)
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
	m := make(map[string]int32, len(names))
	for i, n := range names {
		key := "minecraft:" + strings.TrimSuffix(n, ".json")
		m[key] = int32(i)
	}
	return m, nil
}

// tagFilesUnder returns, for every *.json file at or below base, its tag name
// (the path relative to base, sans .json — so a nested file
// tags/enchantment/exclusive_set/armor.json yields "exclusive_set/armor") and its
// full embedded path. Both slices are index-aligned and sorted by tag name.
func tagFilesUnder(base string) (names []string, files []string, err error) {
	type pair struct{ name, file string }
	var pairs []pair
	walkErr := fs.WalkDir(tagFS, base, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		rel := strings.TrimPrefix(p, base+"/")
		name := strings.TrimSuffix(rel, ".json")
		pairs = append(pairs, pair{name: name, file: p})
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	sort.Slice(pairs, func(a, b int) bool { return pairs[a].name < pairs[b].name })
	names = make([]string, len(pairs))
	files = make([]string, len(pairs))
	for i, p := range pairs {
		names[i] = p.name
		files[i] = p.file
	}
	return names, files, nil
}

// tagsEncoder writes the full ClientboundUpdateTags body: VarInt(registryCount)
// then, per registry, Identifier(id) + VarInt(tagCount) + per tag Identifier(name)
// + VarInt(entryCount) + entryCount × VarInt(index).
type tagsEncoder []resolvedRegistry

func (regs tagsEncoder) WriteTo(w io.Writer) (int64, error) {
	var n int64
	write := func(f pk.FieldEncoder) error {
		m, err := f.WriteTo(w)
		n += m
		return err
	}

	if err := write(pk.VarInt(len(regs))); err != nil {
		return n, err
	}
	for _, reg := range regs {
		if err := write(pk.Identifier(reg.id)); err != nil {
			return n, err
		}
		if err := write(pk.VarInt(len(reg.tags))); err != nil {
			return n, err
		}
		for _, t := range reg.tags {
			if err := write(pk.Identifier(t.name)); err != nil {
				return n, err
			}
			if err := write(pk.VarInt(len(t.entries))); err != nil {
				return n, err
			}
			for _, ix := range t.entries {
				if err := write(pk.VarInt(ix)); err != nil {
					return n, err
				}
			}
		}
	}
	return n, nil
}

// buildUpdateTagsPacket resolves the embedded tag set and marshals it into a
// ClientboundConfigUpdateTags packet. Exposed (unexported) for the test to assert
// the resolved counts without a socket.
func buildUpdateTagsPacket() (pk.Packet, []resolvedRegistry, error) {
	regs, err := loadTags()
	if err != nil {
		return pk.Packet{}, nil, err
	}
	p := pk.Marshal(packetid.ClientboundConfigUpdateTags, tagsEncoder(regs))
	return p, regs, nil
}
