package data

import (
	"encoding/json"
	"testing"
)

// TestEmbedStructureTemplates proves the DATA half of STRUCT-05 part 1 is wired: the
// 483 village .nbt StructureTemplates (binary, gzip-wrapped) + the 62 village
// template_pool JSONs + the TOP-LEVEL empty.json terminator + the 40 processor_list
// JSONs are extracted, embedded, and resolve via the typed accessors.
//
// The counts are the STRUCT-05 "embedded data loads" acceptance — a partial extraction
// (a missing .nbt a pool references, or the missing empty.json terminator) would fail
// the 16-02 jigsaw Placer, so these assertions catch a build-data mismatch here.
func TestEmbedStructureTemplates(t *testing.T) {
	// 1. A known village .nbt embeds verbatim (non-empty) and BEGINS WITH THE GZIP
	//    MAGIC 0x1f 0x8b — proving the binary bytes survived copyZipEntry untransformed.
	t.Run("village_nbt_gzip_magic", func(t *testing.T) {
		raw, err := StructureTemplateNBT("village/plains/houses/plains_small_house_1")
		if err != nil {
			t.Fatalf("StructureTemplateNBT(plains_small_house_1): %v", err)
		}
		if len(raw) == 0 {
			t.Fatal("plains_small_house_1.nbt is empty")
		}
		if raw[0] != 0x1f || raw[1] != 0x8b {
			t.Errorf("plains_small_house_1.nbt magic = %#x %#x; want 0x1f 0x8b (gzip)", raw[0], raw[1])
		}
		// The minecraft: namespace must also resolve.
		if _, err := StructureTemplateNBT("minecraft:village/plains/houses/plains_small_house_1"); err != nil {
			t.Errorf("StructureTemplateNBT with namespace: %v", err)
		}
	})

	// 2. A known village template_pool resolves and parses as JSON.
	t.Run("village_template_pool", func(t *testing.T) {
		raw, err := TemplatePoolJSON("village/plains/houses")
		if err != nil {
			t.Fatalf("TemplatePoolJSON(village/plains/houses): %v", err)
		}
		var pool struct {
			Fallback string            `json:"fallback"`
			Elements []json.RawMessage `json:"elements"`
		}
		if err := json.Unmarshal(raw, &pool); err != nil {
			t.Fatalf("village/plains/houses.json does not parse as JSON: %v", err)
		}
		if len(pool.Elements) == 0 {
			t.Error("village/plains/houses pool has no elements")
		}
	})

	// 3. The TOP-LEVEL empty.json terminator resolves through TemplatePoolJSON("empty")
	//    — a not-found here breaks 16-02 jigsaw termination (Pitfall #6 depth-0 -> empty).
	t.Run("empty_terminator_pool", func(t *testing.T) {
		raw, err := TemplatePoolJSON("empty")
		if err != nil {
			t.Fatalf("TemplatePoolJSON(empty): %v — the terminator pool MUST resolve "+
				"or the jigsaw never terminates", err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("empty.json does not parse as JSON: %v", err)
		}
	})

	// 4. A known processor_list resolves and parses with its rule processor.
	t.Run("processor_list", func(t *testing.T) {
		raw, err := ProcessorListJSON("minecraft:mossify_10_percent")
		if err != nil {
			t.Fatalf("ProcessorListJSON(mossify_10_percent): %v", err)
		}
		var pl struct {
			Processors []struct {
				ProcessorType string `json:"processor_type"`
			} `json:"processors"`
		}
		if err := json.Unmarshal(raw, &pl); err != nil {
			t.Fatalf("mossify_10_percent.json does not parse as JSON: %v", err)
		}
		if len(pl.Processors) == 0 || pl.Processors[0].ProcessorType != "minecraft:rule" {
			t.Errorf("mossify_10_percent processors = %+v; want a minecraft:rule processor", pl.Processors)
		}
	})

	// 5. The counts: 62 village template_pools, 40 processor_lists, 483 village .nbt.
	t.Run("counts", func(t *testing.T) {
		pools, err := TemplatePoolIDs()
		if err != nil {
			t.Fatalf("TemplatePoolIDs: %v", err)
		}
		if len(pools) != 62 {
			t.Errorf("village template_pool count = %d; want 62", len(pools))
		}

		procs, err := ProcessorListIDs()
		if err != nil {
			t.Fatalf("ProcessorListIDs: %v", err)
		}
		if len(procs) != 40 {
			t.Errorf("processor_list count = %d; want 40", len(procs))
		}

		nbts := countNBT(t, "structure/village")
		if nbts != 483 {
			t.Errorf("village .nbt count = %d; want 483", nbts)
		}
	})

	// 6. A missing id errors clearly (the accessor contract).
	t.Run("missing_id_errors", func(t *testing.T) {
		if _, err := StructureTemplateNBT("village/does/not/exist"); err == nil {
			t.Error("StructureTemplateNBT(missing) returned nil error; want a clear error")
		}
		if _, err := TemplatePoolJSON("village/does/not/exist"); err == nil {
			t.Error("TemplatePoolJSON(missing) returned nil error; want a clear error")
		}
	})
}

// countNBT walks an embedded sub-tree and counts the .nbt entries (listNested only
// matches .json, so the binary .nbt count needs a direct walk).
func countNBT(t *testing.T, subdir string) int {
	t.Helper()
	n := 0
	walkNBT(t, subdir, &n)
	return n
}

func walkNBT(t *testing.T, subdir string, n *int) {
	t.Helper()
	ents, err := FS.ReadDir(subdir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", subdir, err)
	}
	for _, e := range ents {
		if e.IsDir() {
			walkNBT(t, subdir+"/"+e.Name(), n)
			continue
		}
		if len(e.Name()) > 4 && e.Name()[len(e.Name())-4:] == ".nbt" {
			*n++
		}
	}
}
