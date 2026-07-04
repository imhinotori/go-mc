package registrydata

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// enchantment_cost.go exposes the per-enchantment MIN-COST parameters + the CURSE membership set the
// GrindstoneMenu's XP-return math (getExperienceFromItem -> Enchantment.getMinCost) and the
// grindstone/anvil curse-keeping (EnchantmentTags.CURSE) need — derived from the SAME embedded
// enchantment registry content the server sends (registries/enchantment) + the embedded curse tag
// (tags/enchantment/curse.json). NOT a hardcoded table: the numeric wire id is the entry's index in the
// alphabetically-sorted registries/enchantment tree (== EnchantmentOrder / the RegistryData send order),
// so this stays 1:1 with the datapack registry the client and server exchange.

// EnchantmentCost holds one enchantment's min-cost linear parameters — the vanilla
// Enchantment$Cost{base, perLevelAboveFirst}. getMinCost(level) = base + perLevelAboveFirst*(level-1)
// (1:1 net.minecraft.world.item.enchantment.Enchantment$Cost.calculate).
type EnchantmentCost struct {
	Base              int
	PerLevelAboveFirst int
}

// enchantmentJSON is the subset of the enchantment registry JSON this file reads: the min_cost linear
// definition (Enchantment.definition.minCost).
type enchantmentJSON struct {
	MinCost struct {
		Base              int `json:"base"`
		PerLevelAboveFirst int `json:"per_level_above_first"`
	} `json:"min_cost"`
}

// EnchantmentCosts returns, for each enchantment wire id (the index in the ordered enchantment
// registry), its min-cost parameters, plus the set of wire ids that are CURSES (members of
// #minecraft:enchantment/curse). The two are read from the same embedded content so a datapack change
// reflows both without a code edit. Any parse error is returned LOUDLY (never a silent empty table that
// would zero out grindstone XP).
func EnchantmentCosts() (costs []EnchantmentCost, curses map[int]bool, err error) {
	base := path.Join("registries", "enchantment")
	names, err := entryFiles(base)
	if err != nil {
		return nil, nil, fmt.Errorf("registrydata: list enchantment: %w", err)
	}
	// idByName maps "minecraft:<name>" -> wire id (index in the sorted entry list, == EnchantmentOrder).
	idByName := make(map[string]int, len(names))
	costs = make([]EnchantmentCost, len(names))
	for i, name := range names {
		data, readErr := registryFS.ReadFile(path.Join(base, name))
		if readErr != nil {
			return nil, nil, fmt.Errorf("registrydata: read enchantment %s: %w", name, readErr)
		}
		var ej enchantmentJSON
		if uErr := json.Unmarshal(data, &ej); uErr != nil {
			return nil, nil, fmt.Errorf("registrydata: parse enchantment %s: %w", name, uErr)
		}
		costs[i] = EnchantmentCost{Base: ej.MinCost.Base, PerLevelAboveFirst: ej.MinCost.PerLevelAboveFirst}
		idByName["minecraft:"+strings.TrimSuffix(name, ".json")] = i
	}

	// Curse set: members of tags/enchantment/curse.json (no #tag refs / no object entries in 26.2 —
	// verified: values are plain resource ids). Resolve each to its wire id.
	curses = make(map[int]bool)
	curseData, cErr := tagFS.ReadFile(path.Join("tags", "enchantment", "curse.json"))
	if cErr != nil {
		return nil, nil, fmt.Errorf("registrydata: read curse tag: %w", cErr)
	}
	var ct tagFile
	if uErr := json.Unmarshal(curseData, &ct); uErr != nil {
		return nil, nil, fmt.Errorf("registrydata: parse curse tag: %w", uErr)
	}
	for _, v := range ct.Values {
		if strings.HasPrefix(v, "#") {
			continue // 26.2 curse tag has no tag-of-tag refs; defensively skip if a future one appears.
		}
		if id, ok := idByName[v]; ok {
			curses[id] = true
		}
	}
	return costs, curses, nil
}
