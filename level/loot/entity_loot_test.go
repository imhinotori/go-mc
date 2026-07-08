package loot

// entity_loot_test.go — Phase 29 Plan 04, TASK 0 (the A4 de-risk, runs FIRST).
//
// Open Question 1 / Assumption A4 (29-RESEARCH): does the v3 loot evaluator parse + roll a
// "type":"minecraft:entity" loot table (the 94 embedded entity tables), or does parse.go reject
// the entity table type / its entity-context functions+conditions?
//
// THE RECORDED A4 OUTCOME (this test pins the scoping decision):
//
//   The TOP-LEVEL table type is NOT a parse gate — ParseTable ignores the unused top-level
//   "type" field, so LoadTable("minecraft:entities/pig") loads. BUT the pig pool entry carries
//   three entity-context functions/conditions the pre-29-04 evaluator did NOT implement and
//   ERRORED LOUDLY on:
//     - function minecraft:furnace_smelt              (gated by an any_of of entity_properties)
//     - function minecraft:enchanted_count_increase   (the looting bonus)
//     - condition minecraft:any_of / minecraft:entity_properties (the smelt gate)
//   So A4 = "the entity TABLE type parses, but the entity-context FUNCTIONS+CONDITIONS need
//   handlers" — a BOUNDED extension (not a large net-new subsystem). Plan 04 Task 2 adds them
//   with cited-stub defaults equal to the vanilla v1 state (pig never on fire, attacker has no
//   looting/smelts_loot enchant, killed_by_player from the death source), so they are a real
//   bounded port that produces the CORRECT v1 drop (porkchop 1-3), NOT a documented cut.
//
// The faithful v1 pig drop: ONE pool, rolls=1, a single item entry (porkchop) with set_count
// uniform[1,3]; furnace_smelt is gated FALSE (not on fire, no smelts_loot enchant) so porkchop
// is NOT smelted; enchanted_count_increase adds 0 (no looting). So a roll returns exactly one
// porkchop stack with count in [1,3].

import "testing"

// TestEntityLootRoll de-risks A4: loot.Roll over minecraft:entities/pig parses and rolls headless
// without panicking on the entity table type, returning exactly one porkchop stack with count 1-3
// (the faithful v1 drop: set_count[1,3], no smelt, no looting bonus).
func TestEntityLootRoll(t *testing.T) {
	tbl, err := LoadTable("minecraft:entities/pig")
	if err != nil {
		t.Fatalf("LoadTable(minecraft:entities/pig): %v (A4: entity table type / entity-context handlers not ported)", err)
	}

	// A deterministic seeded roll: a fixed seed reproduces the exact count via the LegacyRandomSource.
	const seed int64 = 12345
	stacks := Roll(tbl, seed, NewLootContext(seed, 0))

	if len(stacks) != 1 {
		t.Fatalf("pig roll: got %d stacks, want exactly 1 (single porkchop pool, rolls=1)", len(stacks))
	}
	s := stacks[0]
	if int(s.Count) < 1 || int(s.Count) > 3 {
		t.Fatalf("pig roll: porkchop count=%d, want 1-3 (set_count uniform[1,3])", int(s.Count))
	}

	// The item id must be porkchop (the only entry). itemNameToID resolves both forms.
	wantID, ok := itemNameToID["minecraft:porkchop"]
	if !ok {
		t.Fatalf("porkchop item id not resolvable (item table mismatch)")
	}
	if int32(s.ItemID) != wantID {
		t.Fatalf("pig roll: item id=%d, want %d (minecraft:porkchop)", int(s.ItemID), wantID)
	}
}

// TestSlimeLootCubeMobSize verifies the cube_mob size condition: the entities/slime table gates its
// slime_ball pool on type_specific/cube_mob.size==1, so a TINY (size 1) slime rolls slime_ball (uniform
// [0,2]) while a size-2 slime rolls NOTHING. This exercises the entityPropCubeMobSize condition + the
// CubeMobSize loot-context field.
func TestSlimeLootCubeMobSize(t *testing.T) {
	tbl, err := LoadTable("minecraft:entities/slime")
	if err != nil {
		t.Fatalf("LoadTable(minecraft:entities/slime): %v", err)
	}
	const seed int64 = 777

	// Size-1 (tiny) slime: the slime_ball pool fires (count 0..2). A count-0 roll yields no stack, so we
	// scan several seeds to prove at least one produces a slime_ball and none produce a wrong item.
	wantID, ok := itemNameToID["minecraft:slime_ball"]
	if !ok {
		t.Fatal("slime_ball item id not resolvable")
	}
	sawBall := false
	for s := int64(0); s < 40; s++ {
		stacks := Roll(tbl, s, NewEntityLootContext(s, 0, EntityLootParams{CubeMobSize: 1}))
		for _, st := range stacks {
			if int32(st.ItemID) != wantID {
				t.Fatalf("size-1 slime rolled item id=%d, want slime_ball %d", int(st.ItemID), wantID)
			}
			if int(st.Count) < 0 || int(st.Count) > 2 {
				t.Fatalf("size-1 slime slime_ball count=%d, want 0..2 (uniform)", int(st.Count))
			}
			if st.Count > 0 {
				sawBall = true // a >0 roll proves the size-1 pool fired with a real slimeball
			}
		}
	}
	if !sawBall {
		t.Fatal("size-1 slime never rolled a slime_ball across 40 seeds (the cube_mob.size==1 pool did not fire)")
	}

	// Size-2 slime: the cube_mob.size==1 condition FAILS, so the pool is skipped -> NO drops.
	for s := int64(0); s < 40; s++ {
		stacks := Roll(tbl, s, NewEntityLootContext(s, 0, EntityLootParams{CubeMobSize: 2}))
		if len(stacks) != 0 {
			t.Fatalf("size-2 slime rolled %d stacks, want 0 (cube_mob.size==1 gate fails)", len(stacks))
		}
	}
	_ = seed
}
