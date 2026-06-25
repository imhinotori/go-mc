package level

import (
	"math/bits"
	"testing"
)

// newHeightmapBS builds a single-column- orderable heightmap BitStorage sized for `secs`
// sections, identical to the heightmaps EmptyChunk allocates. Heights are stored relative
// to minY; index ordering is lz<<4|lx.
func newHeightmapBS(secs int) *BitStorage {
	return NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil)
}

// opaqueColumn is a tiny in-memory column model the tests close over for opaqueAt: a set
// of world-Y values that are "opaque". It lets a test simulate the existing-blocks the
// downward rescan re-tests after a surface block is removed.
type opaqueColumn map[int]bool

func (c opaqueColumn) at(y int) bool { return c[y] }

func TestHeightmapUpdate(t *testing.T) {
	const (
		secs = 24
		minY = -64
		lx   = 3
		lz   = 5
	)
	col := lz<<4 | lx

	// height returns the stored world-Y of "first available above surface".
	height := func(bs *BitStorage) int { return bs.Get(col) + minY }

	t.Run("place opaque on empty column sets y+1", func(t *testing.T) {
		bs := newHeightmapBS(secs)
		column := opaqueColumn{}
		// Initially the column is empty: stored height 0 → firstAvail == minY.
		// Place an opaque block at y=70: firstAvail must become 71.
		column[70] = true
		HeightmapUpdate(bs, lx, 70, lz, minY, true, column.at)
		if got := height(bs); got != 71 {
			t.Fatalf("after placing opaque at 70: firstAvail = %d, want 71", got)
		}
	})

	t.Run("place opaque at or above surface raises height", func(t *testing.T) {
		bs := newHeightmapBS(secs)
		column := opaqueColumn{}
		column[70] = true
		HeightmapUpdate(bs, lx, 70, lz, minY, true, column.at)
		// Now place a higher opaque block at 80 → height rises to 81.
		column[80] = true
		HeightmapUpdate(bs, lx, 80, lz, minY, true, column.at)
		if got := height(bs); got != 81 {
			t.Fatalf("after placing opaque at 80: firstAvail = %d, want 81", got)
		}
	})

	t.Run("place opaque well below surface is a no-op", func(t *testing.T) {
		bs := newHeightmapBS(secs)
		column := opaqueColumn{70: true}
		HeightmapUpdate(bs, lx, 70, lz, minY, true, column.at)
		before := height(bs) // 71
		// Place an opaque block at 60 (firstAvail-2 == 69, 60 <= 69) → no change.
		column[60] = true
		HeightmapUpdate(bs, lx, 60, lz, minY, true, column.at)
		if got := height(bs); got != before {
			t.Fatalf("placing opaque below surface changed firstAvail %d -> %d, want unchanged", before, got)
		}
	})

	t.Run("remove exact surface block rescans down to next opaque", func(t *testing.T) {
		bs := newHeightmapBS(secs)
		// Column of opaque blocks at 68,69,70; surface block is 70 (firstAvail 71).
		column := opaqueColumn{68: true, 69: true, 70: true}
		HeightmapUpdate(bs, lx, 70, lz, minY, true, column.at)
		if got := height(bs); got != 71 {
			t.Fatalf("setup firstAvail = %d, want 71", got)
		}
		// Remove the exact surface block (70): the NEW state at 70 is non-opaque, and 70
		// is the existing surface, so it must rescan down. opaqueAt still reports 68,69 as
		// opaque → new surface is 69, firstAvail 70.
		delete(column, 70)
		HeightmapUpdate(bs, lx, 70, lz, minY, false, column.at)
		if got := height(bs); got != 70 {
			t.Fatalf("after removing surface block 70: firstAvail = %d, want 70 (next opaque 69)", got)
		}
	})

	t.Run("remove surface with nothing opaque below floors to 0", func(t *testing.T) {
		bs := newHeightmapBS(secs)
		column := opaqueColumn{70: true}
		HeightmapUpdate(bs, lx, 70, lz, minY, true, column.at)
		// Remove the only opaque block → no opaque below → floor (stored 0).
		delete(column, 70)
		HeightmapUpdate(bs, lx, 70, lz, minY, false, column.at)
		if got := bs.Get(col); got != 0 {
			t.Fatalf("after removing the only opaque block: stored height = %d, want 0 (floor)", got)
		}
	})

	t.Run("non-opaque write below the surface is a no-op", func(t *testing.T) {
		bs := newHeightmapBS(secs)
		column := opaqueColumn{68: true, 69: true, 70: true}
		HeightmapUpdate(bs, lx, 70, lz, minY, true, column.at)
		before := height(bs) // 71
		// A non-opaque write at 65 (well below surface-1=70) must NOT touch the height:
		// y <= firstAvail-2 (65 <= 69) early-out.
		HeightmapUpdate(bs, lx, 65, lz, minY, false, column.at)
		if got := height(bs); got != before {
			t.Fatalf("non-opaque write below surface changed firstAvail %d -> %d, want unchanged", before, got)
		}
	})

	t.Run("non-opaque write at surface-1 but not exact surface is a no-op", func(t *testing.T) {
		bs := newHeightmapBS(secs)
		column := opaqueColumn{70: true}
		HeightmapUpdate(bs, lx, 70, lz, minY, true, column.at)
		before := height(bs) // 71
		// y == firstAvail-1 is 70 (the exact surface). A non-opaque write at 70 would
		// rescan; but a non-opaque write at any y != firstAvail-1 above the early-out
		// (e.g. y == firstAvail, 71) is not the surface block → no-op.
		HeightmapUpdate(bs, lx, 71, lz, minY, false, column.at)
		if got := height(bs); got != before {
			t.Fatalf("non-opaque write at non-surface y changed firstAvail %d -> %d, want unchanged", before, got)
		}
	})
}
