package loot

import (
	"testing"
)

// targetChestTables are the 5 target chest groups (STRUCT-POLISH-01). The parse
// tests assert each loads into the model with the pool/entry counts the JSON
// declares (a structural contract: a regression in the parser changes these).
var targetChestTables = []string{
	"minecraft:chests/desert_pyramid",
	"minecraft:chests/jungle_temple",
	"minecraft:chests/jungle_temple_dispenser",
	"minecraft:chests/igloo_chest",
	"minecraft:chests/abandoned_mineshaft",
	"minecraft:chests/simple_dungeon",
}

// TestLootParse loads each of the 5 target chest tables and asserts the parse
// succeeds with non-empty pools, every pool has a rolls provider, and every entry
// has a recognized type. (STRUCT-POLISH-01 Wave-0 scaffold.)
func TestLootParse(t *testing.T) {
	for _, id := range targetChestTables {
		tbl, err := LoadTable(id)
		if err != nil {
			t.Fatalf("LoadTable(%q): %v", id, err)
		}
		if len(tbl.Pools) == 0 {
			t.Errorf("%s: no pools parsed", id)
		}
		for pi, p := range tbl.Pools {
			if p.Rolls == nil {
				t.Errorf("%s pool %d: nil rolls", id, pi)
			}
			if p.BonusRolls == nil {
				t.Errorf("%s pool %d: nil bonusRolls (should default to ConstantValue(0))", id, pi)
			}
			if len(p.Entries) == 0 {
				t.Errorf("%s pool %d: no entries", id, pi)
			}
			for ei, e := range p.Entries {
				switch normalizeType(e.Type) {
				case "item":
					if e.itemID == 0 && e.Name != "minecraft:air" {
						t.Errorf("%s pool %d entry %d (%s): unresolved item id", id, pi, ei, e.Name)
					}
				case "empty":
					// no item
				default:
					t.Errorf("%s pool %d entry %d: unexpected entry type %q in chest", id, pi, ei, e.Type)
				}
			}
		}
	}
}

// TestLootParseCounts pins the exact pool/entry counts for two representative
// tables (the bytecode-derived structural contract). simple_dungeon has 3 pools
// (12, 10, 4 entries); jungle_temple has 2 pools (13, 2 entries).
func TestLootParseCounts(t *testing.T) {
	cases := []struct {
		id          string
		poolEntries []int
	}{
		{"minecraft:chests/simple_dungeon", []int{12, 10, 4}},
		{"minecraft:chests/jungle_temple", []int{13, 2}},
	}
	for _, c := range cases {
		tbl, err := LoadTable(c.id)
		if err != nil {
			t.Fatalf("LoadTable(%q): %v", c.id, err)
		}
		if len(tbl.Pools) != len(c.poolEntries) {
			t.Fatalf("%s: got %d pools, want %d", c.id, len(tbl.Pools), len(c.poolEntries))
		}
		for i, want := range c.poolEntries {
			if got := len(tbl.Pools[i].Entries); got != want {
				t.Errorf("%s pool %d: got %d entries, want %d", c.id, i, got, want)
			}
		}
	}
}

// TestLootParseUnknownTypeErrors asserts an unrecognized function/provider/entry
// type errors LOUDLY (no silent skip) — T-20-02 (Tampering).
func TestLootParseUnknownTypeErrors(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{
			"unknown entry type",
			`{"pools":[{"rolls":1.0,"entries":[{"type":"minecraft:bogus_entry","name":"minecraft:stone"}]}]}`,
		},
		{
			"unknown number provider type",
			`{"pools":[{"rolls":{"type":"minecraft:bogus_provider","min":1.0,"max":2.0},"entries":[{"type":"minecraft:item","name":"minecraft:stone"}]}]}`,
		},
		{
			"unknown function type",
			`{"pools":[{"rolls":1.0,"entries":[{"type":"minecraft:item","name":"minecraft:stone","functions":[{"function":"minecraft:bogus_fn"}]}]}]}`,
		},
		{
			"unknown condition type",
			`{"pools":[{"rolls":1.0,"entries":[{"type":"minecraft:item","name":"minecraft:stone","conditions":[{"condition":"minecraft:bogus_cond"}]}]}]}`,
		},
		{
			"unknown item",
			`{"pools":[{"rolls":1.0,"entries":[{"type":"minecraft:item","name":"minecraft:not_a_real_item"}]}]}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseTable([]byte(c.json)); err == nil {
				t.Errorf("expected loud error for %s, got nil", c.name)
			}
		})
	}
}

// TestNumberProviderDecode asserts a bare float decodes to ConstantValue and an
// object {type:uniform,min,max} decodes to *Uniform.
func TestNumberProviderDecode(t *testing.T) {
	c, err := parseNumberProvider([]byte("4.0"))
	if err != nil {
		t.Fatalf("constant decode: %v", err)
	}
	if _, ok := c.(ConstantValue); !ok {
		t.Errorf("bare float -> got %T, want ConstantValue", c)
	}
	u, err := parseNumberProvider([]byte(`{"type":"minecraft:uniform","min":1.0,"max":5.0}`))
	if err != nil {
		t.Fatalf("uniform decode: %v", err)
	}
	if _, ok := u.(*Uniform); !ok {
		t.Errorf("uniform object -> got %T, want *Uniform", u)
	}
}

// TestRollsCap asserts a constant `rolls` far above maxRolls is rejected at parse
// time (T-20-01 DoS / V5 Input Validation) once the roll engine bounds it — here
// we assert the constant decodes but the engine cap is enforced in roll.go's
// TestRollsCapEnforced. The parse itself tolerates the value; the cap is a roll-time
// guard. This test ensures a giant constant is at least representable (no overflow
// panic) so the engine can reject it cleanly.
func TestRollsCap(t *testing.T) {
	c, err := parseNumberProvider([]byte("2000000000"))
	if err != nil {
		t.Fatalf("giant constant decode: %v", err)
	}
	ctx := NewLootContext(1, 0)
	if got := c.GetInt(ctx); got <= maxRolls {
		t.Errorf("giant constant GetInt = %d, expected > maxRolls so the engine cap triggers", got)
	}
}
