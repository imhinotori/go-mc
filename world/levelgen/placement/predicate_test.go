package placement

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
)

func stateOf(t *testing.T, b block.Block) block.StateID {
	t.Helper()
	sid, ok := block.ToStateID[b]
	if !ok {
		t.Fatalf("no state id for %T", b)
	}
	return sid
}

// TestMatchingBlockTagAir: matching_block_tag "minecraft:air" keeps iff the block at
// the (offset) position is air. The fakeContext defaults unset blocks to StateID 0
// (air), so an unset position matches; a stone position does not.
func TestMatchingBlockTagAir(t *testing.T) {
	raw := json.RawMessage(`{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}`)
	p, err := ParsePredicate(raw)
	if err != nil {
		t.Fatalf("ParsePredicate: %v", err)
	}
	ctx := newFakeContext(-64, 384)
	// Unset position -> air -> matches.
	if !p.Test(ctx, 5, 10, 5) {
		t.Fatalf("matching_block_tag air: expected match on air position")
	}
	// Stone position -> no match.
	ctx.blocks[[3]int{5, 10, 5}] = stateOf(t, block.Stone{})
	if p.Test(ctx, 5, 10, 5) {
		t.Fatalf("matching_block_tag air: matched a stone position")
	}
}

// TestMatchingBlockTagOffset: the predicate honors its [dx,dy,dz] offset.
func TestMatchingBlockTagOffset(t *testing.T) {
	raw := json.RawMessage(`{"type":"minecraft:matching_block_tag","tag":"minecraft:air","offset":[0,-1,0]}`)
	p, err := ParsePredicate(raw)
	if err != nil {
		t.Fatalf("ParsePredicate: %v", err)
	}
	ctx := newFakeContext(-64, 384)
	// Block directly below (5,10,5) is stone -> offset(0,-1,0) reads stone -> no match.
	ctx.blocks[[3]int{5, 9, 5}] = stateOf(t, block.Stone{})
	if p.Test(ctx, 5, 10, 5) {
		t.Fatalf("offset predicate read the wrong position")
	}
}

// TestAllOfAnyOfNot exercises the composites over a known grid.
func TestAllOfAnyOfNot(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	stone := stateOf(t, block.Stone{})
	ctx.blocks[[3]int{0, 0, 0}] = stone

	// not(matching_blocks stone) at (0,0,0) -> the block IS stone -> not -> false.
	notRaw := json.RawMessage(`{"type":"minecraft:not","predicate":{"type":"minecraft:matching_blocks","blocks":"minecraft:stone"}}`)
	np, err := ParsePredicate(notRaw)
	if err != nil {
		t.Fatalf("parse not: %v", err)
	}
	if np.Test(ctx, 0, 0, 0) {
		t.Fatalf("not(matching stone) over a stone block returned true")
	}
	if !np.Test(ctx, 1, 1, 1) { // air there -> not stone -> true
		t.Fatalf("not(matching stone) over air returned false")
	}

	// all_of(solid, matching_blocks stone) at (0,0,0): both true.
	allRaw := json.RawMessage(`{"type":"minecraft:all_of","predicates":[{"type":"minecraft:solid"},{"type":"minecraft:matching_blocks","blocks":"minecraft:stone"}]}`)
	ap, err := ParsePredicate(allRaw)
	if err != nil {
		t.Fatalf("parse all_of: %v", err)
	}
	if !ap.Test(ctx, 0, 0, 0) {
		t.Fatalf("all_of(solid, stone) over stone returned false")
	}
	if ap.Test(ctx, 9, 9, 9) { // air -> solid false -> all_of false
		t.Fatalf("all_of over air returned true")
	}

	// any_of(matching_blocks stone, solid) over air at (9,9,9): both false -> false.
	anyRaw := json.RawMessage(`{"type":"minecraft:any_of","predicates":[{"type":"minecraft:matching_blocks","blocks":"minecraft:stone"},{"type":"minecraft:solid"}]}`)
	yp, err := ParsePredicate(anyRaw)
	if err != nil {
		t.Fatalf("parse any_of: %v", err)
	}
	if yp.Test(ctx, 9, 9, 9) {
		t.Fatalf("any_of over air returned true")
	}
	if !yp.Test(ctx, 0, 0, 0) { // stone -> matching true -> any true
		t.Fatalf("any_of over stone returned false")
	}
}

// TestWouldSurviveGate: would_survive keeps iff the block below is non-air ground and
// the position itself is air (the conservative ground gate).
func TestWouldSurviveGate(t *testing.T) {
	raw := json.RawMessage(`{"type":"minecraft:would_survive","state":{"Name":"minecraft:poppy"}}`)
	p, err := ParsePredicate(raw)
	if err != nil {
		t.Fatalf("ParsePredicate would_survive: %v", err)
	}
	ctx := newFakeContext(-64, 384)
	dirt := stateOf(t, block.Dirt{})

	// (5,10,5): air over air -> no ground -> drop.
	if p.Test(ctx, 5, 10, 5) {
		t.Fatalf("would_survive kept a floating (air-below) position")
	}
	// Put dirt below -> ground present, position air -> keep.
	ctx.blocks[[3]int{5, 9, 5}] = dirt
	if !p.Test(ctx, 5, 10, 5) {
		t.Fatalf("would_survive dropped a valid ground position")
	}
	// Position itself solid -> not replaceable -> drop.
	ctx.blocks[[3]int{5, 10, 5}] = stateOf(t, block.Stone{})
	if p.Test(ctx, 5, 10, 5) {
		t.Fatalf("would_survive kept a position already occupied by a solid block")
	}
}

// TestParsePredicateUnknownErrors: an unported predicate type errors loudly.
func TestParsePredicateUnknownErrors(t *testing.T) {
	if _, err := ParsePredicate(json.RawMessage(`{"type":"minecraft:made_up_predicate"}`)); err == nil {
		t.Fatalf("ParsePredicate accepted an unported type silently")
	}
	// An unported tag in matching_block_tag also errors loudly.
	if _, err := ParsePredicate(json.RawMessage(`{"type":"minecraft:matching_block_tag","tag":"minecraft:made_up_tag"}`)); err == nil {
		t.Fatalf("ParsePredicate accepted an unported tag silently")
	}
}
