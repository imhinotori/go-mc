// gen_block_support generates level/block/support.go from block_support.json.
//
// block_support.json comes from the GenBlockSupport.java extractor: one row per registered
// BlockState carrying a packed bitmask of the precomputed block-support cache that drives
// net.minecraft.world.level.block.state.BlockBehaviour$BlockStateBase.isFaceSturdy 1:1.
//
// WHY this is a pure per-state lookup (not the VoxelShape engine): the vanilla Cache builds a
// boolean[18] (6 Directions x 3 SupportTypes) at registration time using EmptyBlockGetter (no
// world context), so isFaceSturdy(dir, type) is a pure function of the BlockState. See the
// GenBlockSupport.java header for the full javap citations.
//
// The mask packs, per state:
//
//	bits 0..17 : faceSturdy[18], bit (dir.ordinal()*3 + supportType.ordinal())
//	bit  18    : isSolid()  (the BlockBehaviour legacySolid field; signs use it)
//	bit  19    : isCollisionShapeFullBlock()
//	bit  20    : isSuffocating()  (no-context; the per-block isSuffocating StatePredicate)
//
// The row's `id` is the GLOBAL block-state id (Block.getId(state) == Block.BLOCK_STATE_REGISTRY
// id), which is identical to the Go StateID built from block_states.nbt — so the emitted table
// is a flat slice indexed directly by StateID.
package main

import (
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
)

// supportRow is one entry of block_support.json (see GenBlockSupport.java).
type supportRow struct {
	ID   int    `json:"id"`   // global block-state id (== Go StateID)
	Mask uint32 `json:"mask"` // packed faceSturdy[18] + isSolid + isCollisionShapeFullBlock
}

// genBlockSupport reads block_support.json and emits level/block/support.go.
func genBlockSupport(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "block_support.json")
	out := filepath.Join(goMCRoot, "level", "block", "support.go")

	var rows []supportRow
	if err := readJSON(jsonPath, &rows); err != nil {
		return fmt.Errorf("genBlockSupport: %w", err)
	}

	// Sort by id so the flat slice is dense and index == StateID.
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	// Verify ids are dense 0..N-1 (the flat-slice keying depends on it).
	for i, r := range rows {
		if r.ID != i {
			return fmt.Errorf("genBlockSupport: non-dense state ids: row %d has id %d (expected %d)", i, r.ID, i)
		}
	}

	var buf strings.Builder
	buf.WriteString(generatedHeader("gen_block_support.go", "block_support.json"))
	buf.WriteByte('\n')
	buf.WriteString("package block\n\n")

	buf.WriteString(`// SupportType mirrors net.minecraft.world.level.block.SupportType. The ordinal order
// (FULL=0, CENTER=1, RIGID=2) is the order used as the low component of the faceSturdy
// cache index. CITE: javap net.minecraft.world.level.block.SupportType — fields declared
// FULL, CENTER, RIGID in that order.
type SupportType uint8

const (
	SupportFull   SupportType = iota // FULL: the whole face is a full 16x16 square.
	SupportCenter                    // CENTER: a centered 2x2 (>= [6,6]..[10,10]) region.
	SupportRigid                     // RIGID: the whole face square AND it is non-fragile.
)

// supportTypeCount is SupportType.values().length (3). The faceSturdy cache index is
// dir.ordinal()*supportTypeCount + supportType.ordinal(). CITE: javap
// BlockBehaviour$BlockStateBase$Cache.getFaceSupportIndex.
const supportTypeCount = 3

const (
	// bit 18 holds isSolid() (the legacySolid field); bit 19 holds isCollisionShapeFullBlock();
	// bit 20 holds isSuffocating() (the no-context per-block isSuffocating StatePredicate result).
	bitIsSolid              = 18
	bitIsCollisionFullBlock = 19
	bitIsSuffocating        = 20
)

// blockSupport[stateID] packs the precomputed block-support cache for that state:
//
//	bits 0..17 : faceSturdy[18], bit (dir*supportTypeCount + supportType)
//	bit  18    : isSolid()
//	bit  19    : isCollisionShapeFullBlock()
//	bit  20    : isSuffocating()
//
// It is keyed directly by StateID (the global block-state id), matching StateList.
`)

	fmt.Fprintf(&buf, "var blockSupport = [...]uint32{\n")
	// Emit packed, several per line for compactness.
	const perLine = 8
	for i, r := range rows {
		if i%perLine == 0 {
			buf.WriteString("\t")
		}
		fmt.Fprintf(&buf, "0x%06x,", r.Mask)
		if i%perLine == perLine-1 || i == len(rows)-1 {
			buf.WriteByte('\n')
		} else {
			buf.WriteByte(' ')
		}
	}
	buf.WriteString("}\n\n")

	buf.WriteString(`// IsFaceSturdy reports BlockBehaviour$BlockStateBase.isFaceSturdy(EmptyBlockGetter, ZERO,
// dir, supportType) for the given state — a pure lookup into the vanilla per-state support
// cache (the table built with NO world context at registration time). This is the
// server-authoritative, no-context face-sturdiness used by worldgen ground checks and
// attachment-block survival. CITE: javap BlockBehaviour$BlockStateBase.isFaceSturdy(...,
// SupportType) — when the cache is present it returns cache.faceSturdy[getFaceSupportIndex].
//
// Out-of-range state ids and dir/supportType return false (vanilla never queries those here).
func IsFaceSturdy(s StateID, dir Direction, supportType SupportType) bool {
	if int(s) < 0 || int(s) >= len(blockSupport) {
		return false
	}
	if dir > East || supportType > SupportRigid {
		return false
	}
	idx := uint(dir)*supportTypeCount + uint(supportType)
	return blockSupport[s]&(1<<idx) != 0
}

// IsSolid reports BlockBehaviour$BlockStateBase.isSolid() (the legacySolid field) for the
// state. This is the flag a few blocks (e.g. signs via state.isSolid()) read — it is NOT the
// same as isFaceSturdy. CITE: javap BlockStateBase.isSolid — getfield legacySolid.
func IsSolid(s StateID) bool {
	if int(s) < 0 || int(s) >= len(blockSupport) {
		return false
	}
	return blockSupport[s]&(1<<bitIsSolid) != 0
}

// IsCollisionShapeFullBlock reports
// BlockBehaviour$BlockStateBase.isCollisionShapeFullBlock(EmptyBlockGetter, ZERO) for the
// state: Block.isShapeFullBlock(state.getCollisionShape(...)) with no world context. CITE:
// javap BlockBehaviour$BlockStateBase$Cache.<init> — putfield isCollisionShapeFullBlock.
func IsCollisionShapeFullBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(blockSupport) {
		return false
	}
	return blockSupport[s]&(1<<bitIsCollisionFullBlock) != 0
}

// IsSuffocating reports BlockBehaviour$BlockStateBase.isSuffocating(EmptyBlockGetter, ZERO) for
// the state: the per-block isSuffocating StatePredicate (default
// blocksMotion() && isCollisionShapeFullBlock(); some blocks override it) evaluated with no
// world context. This is the server-authoritative IN_WALL suffocation test. CITE: javap
// BlockBehaviour$BlockStateBase.isSuffocating — invokevirtual on the isSuffocating predicate
// field. The default predicate's isCollisionShapeFullBlock(getter,pos) is context-free for
// non-dynamic blocks, so the baked no-context value matches the live read for the suffocation
// case (a head inside a full cube — dirt/stone/sand/etc.).
func IsSuffocating(s StateID) bool {
	if int(s) < 0 || int(s) >= len(blockSupport) {
		return false
	}
	return blockSupport[s]&(1<<bitIsSuffocating) != 0
}
`)

	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("genBlockSupport: gofmt: %w", err)
	}
	if err := writeFile(out, formatted); err != nil {
		return fmt.Errorf("genBlockSupport: %w", err)
	}
	logf("genBlockSupport: wrote %s (%d states)", out, len(rows))
	return nil
}
