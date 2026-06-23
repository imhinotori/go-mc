package registrydata

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// wantTagCounts is the EXACT per-registry tagCount a real vanilla 26.2 server
// sends in the Configuration-state Update Tags packet (02-04 capture-diff,
// temp/captureclient/vanilla-capture-tags.txt). If WriteTags ever drifts from
// this set or these counts, the capture-diff is broken and a real client may
// reject the registry.
var wantTagCounts = map[string]int{
	"minecraft:block":                  265,
	"minecraft:item":                   224,
	"minecraft:worldgen/biome":         68,
	"minecraft:entity_type":            48,
	"minecraft:damage_type":            34,
	"minecraft:enchantment":            22,
	"minecraft:banner_pattern":         11,
	"minecraft:fluid":                  6,
	"minecraft:game_event":             5,
	"minecraft:timeline":               4,
	"minecraft:instrument":             3,
	"minecraft:point_of_interest_type": 3,
	"minecraft:dialog":                 2,
	"minecraft:painting_variant":       1,
	"minecraft:potion":                 1,
}

// TestUpdateTagsMatchesVanillaCounts proves loadTags resolves every member of
// every tag in all 15 registries (no unknown-entry / missing-#tag error) and that
// the registry set + per-registry tag counts match vanilla 26.2 exactly. A
// resolution failure here would mean a tag references an entry not in any sent
// registry — the HARD blocker the plan calls out.
func TestUpdateTagsMatchesVanillaCounts(t *testing.T) {
	regs, err := loadTags()
	if err != nil {
		t.Fatalf("loadTags: %v", err)
	}
	if len(regs) != len(wantTagCounts) {
		t.Fatalf("registry count = %d, want %d", len(regs), len(wantTagCounts))
	}
	got := map[string]int{}
	for _, r := range regs {
		got[r.id] = len(r.tags)
	}
	for id, want := range wantTagCounts {
		if got[id] != want {
			t.Errorf("%s tagCount = %d, want %d", id, got[id], want)
		}
	}
	for id := range got {
		if _, ok := wantTagCounts[id]; !ok {
			t.Errorf("unexpected registry %s in Update Tags", id)
		}
	}
}

// TestUpdateTagsBindsReferencedTags asserts the specific tags the real client
// reported as UNBOUND (the crash) are present AND non-empty so they actually
// bind: enchantment exclusive_set/*, timeline in_*, and the (legitimately empty
// but present) dialog tags.
func TestUpdateTagsBindsReferencedTags(t *testing.T) {
	regs, err := loadTags()
	if err != nil {
		t.Fatalf("loadTags: %v", err)
	}
	byID := map[string]resolvedRegistry{}
	for _, r := range regs {
		byID[r.id] = r
	}

	// enchantment exclusive_set tags must be present and non-empty (they reference
	// real enchantments that must bind).
	mustNonEmpty := map[string][]string{
		"minecraft:enchantment": {
			"minecraft:exclusive_set/armor",
			"minecraft:exclusive_set/boots",
			"minecraft:exclusive_set/bow",
			"minecraft:exclusive_set/crossbow",
			"minecraft:exclusive_set/damage",
			"minecraft:exclusive_set/mining",
			"minecraft:exclusive_set/riptide",
		},
		"minecraft:timeline": {
			"minecraft:in_end",
			"minecraft:in_nether",
			"minecraft:in_overworld",
		},
	}
	for regID, tagNames := range mustNonEmpty {
		reg, ok := byID[regID]
		if !ok {
			t.Fatalf("registry %s missing from Update Tags", regID)
		}
		idx := map[string][]int32{}
		for _, tg := range reg.tags {
			idx[tg.name] = tg.entries
		}
		for _, tn := range tagNames {
			entries, ok := idx[tn]
			if !ok {
				t.Errorf("%s: tag %s not present (client reported it UNBOUND)", regID, tn)
				continue
			}
			if len(entries) == 0 {
				t.Errorf("%s: tag %s is empty; it must bind real entries", regID, tn)
			}
		}
	}

	// dialog tags are present-but-empty in vanilla; presence alone binds them.
	dialog, ok := byID["minecraft:dialog"]
	if !ok {
		t.Fatal("minecraft:dialog missing from Update Tags")
	}
	dialogNames := map[string]bool{}
	for _, tg := range dialog.tags {
		dialogNames[tg.name] = true
	}
	for _, tn := range []string{"minecraft:pause_screen_additions", "minecraft:quick_actions"} {
		if !dialogNames[tn] {
			t.Errorf("dialog tag %s not present (client reported it UNBOUND)", tn)
		}
	}
}

// TestWriteTagsWireRoundTrip sends the real Update Tags through the wire framing
// (Pack/UnPack at threshold -1) and re-decodes the body, asserting the registry
// count, ids, tag counts, and entry-index lists survive — i.e. the bytes a real
// client receives are well-formed.
func TestWriteTagsWireRoundTrip(t *testing.T) {
	conn := &bufConn{}
	if err := WriteTags(conn); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}
	if len(conn.packets) != 1 {
		t.Fatalf("WriteTags emitted %d packets, want 1", len(conn.packets))
	}
	p := conn.packets[0]
	if p.ID != int32(packetid.ClientboundConfigUpdateTags) {
		t.Fatalf("packet id = %d, want ClientboundConfigUpdateTags", p.ID)
	}

	r := bytes.NewReader(p.Data)
	var regCount pk.VarInt
	if _, err := regCount.ReadFrom(r); err != nil {
		t.Fatalf("read registry count: %v", err)
	}
	if int(regCount) != len(wantTagCounts) {
		t.Fatalf("wire registry count = %d, want %d", regCount, len(wantTagCounts))
	}
	for i := 0; i < int(regCount); i++ {
		var id pk.Identifier
		if _, err := id.ReadFrom(r); err != nil {
			t.Fatalf("read registry id: %v", err)
		}
		var tagCount pk.VarInt
		if _, err := tagCount.ReadFrom(r); err != nil {
			t.Fatalf("read tag count for %s: %v", id, err)
		}
		if want, ok := wantTagCounts[string(id)]; !ok || int(tagCount) != want {
			t.Errorf("%s wire tagCount = %d, want %d", id, tagCount, want)
		}
		for j := 0; j < int(tagCount); j++ {
			var tagName pk.Identifier
			if _, err := tagName.ReadFrom(r); err != nil {
				t.Fatalf("read tag name in %s: %v", id, err)
			}
			var entryCount pk.VarInt
			if _, err := entryCount.ReadFrom(r); err != nil {
				t.Fatalf("read entry count for %s/%s: %v", id, tagName, err)
			}
			for k := 0; k < int(entryCount); k++ {
				var ix pk.VarInt
				if _, err := ix.ReadFrom(r); err != nil {
					t.Fatalf("read entry index in %s/%s: %v", id, tagName, err)
				}
				if ix < 0 {
					t.Errorf("%s/%s negative entry index %d", id, tagName, ix)
				}
			}
		}
	}
	if r.Len() != 0 {
		t.Errorf("Update Tags body has %d trailing bytes (framing drift)", r.Len())
	}
}
