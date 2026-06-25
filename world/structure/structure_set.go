package structure

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// structureSetJSON is the parsed structure_set JSON model. Only the random_spread
// placement body is needed for STRUCT-01 (the four temples); the concentric_rings
// type (stronghold, Phase-15) is recognized by its type tag but its body is left
// unparsed here. The "structures" weighted list is parsed so 14-02 can resolve which
// structure id a set places.
type structureSetJSON struct {
	Placement  placementJSON        `json:"placement"`
	Structures []structureEntryJSON `json:"structures"`
}

// placementJSON is the polymorphic placement body. The type tag selects the union;
// random_spread carries spacing/separation/salt + the optional spread_type and
// frequency_reduction_method. salt/spacing/separation are required for random_spread.
type placementJSON struct {
	Type                     string   `json:"type"`
	Salt                     *int     `json:"salt"`
	Spacing                  *int     `json:"spacing"`
	Separation               *int     `json:"separation"`
	SpreadType               *string  `json:"spread_type"`
	Frequency                *float32 `json:"frequency"`
	FrequencyReductionMethod *string  `json:"frequency_reduction_method"`
}

// structureEntryJSON is one weighted structure ref in a set.
type structureEntryJSON struct {
	Structure string `json:"structure"`
	Weight    int    `json:"weight"`
}

// StructureSet is the loaded, typed structure set: its random_spread placement plus
// the structure ids it can place (with weights). 14-02 reads Structures to resolve
// which temple a set owns; the placement drives where it lands.
type StructureSet struct {
	ID         string
	Placement  RandomSpreadStructurePlacement
	Structures []WeightedStructure
}

// WeightedStructure is a structure id + its selection weight within a set.
type WeightedStructure struct {
	Structure string
	Weight    int
}

// LoadStructureSet loads + parses an embedded structure_set by registry id (e.g.
// "minecraft:desert_pyramids" or bare "desert_pyramids"). It REQUIRES a random_spread
// placement (the temple sets); a concentric_rings set returns an error here (Phase-15
// will extend this). The absent spread_type field defaults to LINEAR and the absent
// frequency_reduction_method defaults to "default" — the codec defaults, made explicit.
func LoadStructureSet(id string) (StructureSet, error) {
	raw, err := data.StructureSetJSON(id)
	if err != nil {
		return StructureSet{}, err
	}
	var js structureSetJSON
	if err := json.Unmarshal(raw, &js); err != nil {
		return StructureSet{}, fmt.Errorf("structure: parsing structure_set %q: %w", id, err)
	}

	pj := js.Placement
	if pj.Type != "minecraft:random_spread" && pj.Type != "random_spread" {
		return StructureSet{}, fmt.Errorf("structure: structure_set %q placement type %q is not random_spread (STRUCT-01 supports random_spread only)", id, pj.Type)
	}
	if pj.Spacing == nil || pj.Separation == nil || pj.Salt == nil {
		return StructureSet{}, fmt.Errorf("structure: structure_set %q random_spread missing spacing/separation/salt", id)
	}

	// spread_type: ABSENT in all four temple sets -> default LINEAR (the
	// RandomSpreadStructurePlacement codec default). Made explicit + tested.
	spread := SpreadLinear
	if pj.SpreadType != nil {
		switch *pj.SpreadType {
		case "linear", "minecraft:linear":
			spread = SpreadLinear
		case "triangular", "minecraft:triangular":
			spread = SpreadTriangular
		default:
			return StructureSet{}, fmt.Errorf("structure: structure_set %q unknown spread_type %q", id, *pj.SpreadType)
		}
	}

	// frequency: ABSENT -> 1.0 (the codec default: always generate, no reduction).
	freq := float32(1.0)
	if pj.Frequency != nil {
		freq = *pj.Frequency
	}

	method := FreqDefault
	if pj.FrequencyReductionMethod != nil {
		method = ParseFrequencyReductionMethod(*pj.FrequencyReductionMethod)
	}

	set := StructureSet{
		ID: id,
		Placement: RandomSpreadStructurePlacement{
			Spacing:         *pj.Spacing,
			Separation:      *pj.Separation,
			Salt:            *pj.Salt,
			SpreadType:      spread,
			Frequency:       freq,
			FrequencyMethod: method,
		},
	}
	for _, e := range js.Structures {
		set.Structures = append(set.Structures, WeightedStructure{Structure: e.Structure, Weight: e.Weight})
	}
	return set, nil
}

// HasStructureBiomes loads + parses the has_structure biome-tag allow-list for a
// structure id (e.g. "desert_pyramid" -> {minecraft:desert}). The returned set is the
// biome ids in which the structure's start may generate; 14-02/14-03 gate each temple
// origin on GetBiome ∈ this set (the load-bearing half of "vanilla positions"). The
// returned map is keyed by the full namespaced biome id.
func HasStructureBiomes(structureID string) (map[string]bool, error) {
	raw, err := data.HasStructureBiomeTag(structureID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	visited := make(map[string]bool)
	if err := resolveBiomeTagValues(raw, set, visited, structureID); err != nil {
		return nil, err
	}
	return set, nil
}

// resolveBiomeTagValues flattens a worldgen/biome tag's "values" into the concrete-biome set,
// resolving nested "#minecraft:is_*" category references recursively (the mineshaft + mesa
// has_structure tags are expressed almost entirely as nested category refs — e.g. mineshaft
// references #is_ocean/#is_river/#is_badlands/..., and is_ocean nests #is_deep_ocean). A flat
// "minecraft:<biome>" value is added directly; a "#minecraft:<tag>" value loads that biome
// category tag (BiomeCategoryTag) and recurses. The visited set guards against tag cycles.
//
// Source: the vanilla TagLoader semantics (a tag's # entries are unioned in). The Phase-14
// temples had FLAT has_structure tags (no nested refs), so this path is first exercised here.
func resolveBiomeTagValues(raw []byte, set, visited map[string]bool, fromTag string) error {
	var tag struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(raw, &tag); err != nil {
		return fmt.Errorf("structure: parsing biome tag %q: %w", fromTag, err)
	}
	for _, v := range tag.Values {
		if len(v) > 0 && v[0] == '#' {
			ref := v[1:] // strip the leading '#'
			if visited[ref] {
				continue
			}
			visited[ref] = true
			nested, err := data.BiomeCategoryTag(ref)
			if err != nil {
				return fmt.Errorf("structure: resolving nested biome tag %q (from %q): %w", ref, fromTag, err)
			}
			if err := resolveBiomeTagValues(nested, set, visited, ref); err != nil {
				return err
			}
			continue
		}
		set[v] = true
	}
	return nil
}
