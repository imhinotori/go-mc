package advancement

// embed.go — the level/recipe/embed.go twin. It //go:embeds the NON-recipe
// advancement JSON tree (adventure/end/husbandry/nether/story == 126 files, the
// tree the client shows), extracted from the 26.2 inner jar's
// data/minecraft/advancement/**. Recipe advancements (minecraft:recipes/**) are
// hidden auto-grants that never appear in the tree screen and are intentionally
// NOT embedded (DEFERRED — cited in the server grant path).

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed data/advancement
var advancementFS embed.FS

// LoadAll walks the embedded advancement tree (io/fs.WalkDir over every *.json
// under data/advancement), parses each into an Advancement keyed
// "minecraft:<category>/<name>", and returns them sorted by id for determinism.
// A decode error aborts the whole load LOUDLY — never a silent drop (the
// level/recipe LoadAll discipline).
func LoadAll() ([]Advancement, error) {
	var out []Advancement
	walkErr := fs.WalkDir(advancementFS, "data/advancement", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		b, readErr := advancementFS.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("advancement: read %s: %w", p, readErr)
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, "data/advancement/"), ".json")
		id := "minecraft:" + rel
		a, parseErr := parseAdvancement(id, b)
		if parseErr != nil {
			return fmt.Errorf("advancement: parse %s: %w", p, parseErr)
		}
		out = append(out, a)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	// Insertion sort by id (126 ids, loaded once at boot) for deterministic order.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].ID > out[j].ID; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, nil
}
