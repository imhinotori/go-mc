package server

// ranged_bow_params_test.go -- pins the per-DECLARATION RangedBowAttackGoal ctor args resolved by
// rangedBowAttackGoal.resolveBowParams. The skeleton family builds RangedBowAttackGoal(this, 1.0, 40,
// 15.0); the Illusioner builds (this, 0.5, 20, 15.0). buildNativeGoal constructs the SAME goal class for
// both, so the divergence is latched per entity type. Cite Illusioner.registerGoals @6 (0.5d/20/15.0f) +
// AbstractSkeleton.reassessWeaponGoal (1.0d/40/15.0f).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// bowParamEntity makes a bare entity of the given type.
func bowParamEntity(typ entity.ID) *Entity {
	e := &Entity{id: 1, typ: typ}
	return e
}

// TestRangedBowParamsSkeletonDefault: an un-resolved goal defaults to the skeleton ctor args (1.0/40)
// for the skeleton family (and every non-illusioner hostile).
func TestRangedBowParamsSkeletonDefault(t *testing.T) {
	g := newRangedBowAttackGoal()
	if g.speedModifier >= 0 {
		t.Fatalf("fresh goal speedModifier = %v, want < 0 (unresolved sentinel)", g.speedModifier)
	}
	g.resolveBowParams(bowParamEntity(entity.Skeleton.ID))
	if math.Abs(g.speedModifier-1.0) > 1e-12 {
		t.Fatalf("skeleton bow speedModifier = %v, want 1.0", g.speedModifier)
	}
	if g.attackIntervalMin != 40 {
		t.Fatalf("skeleton bow attackIntervalMin = %d, want 40 (NORMAL)", g.attackIntervalMin)
	}
}

// TestRangedBowParamsIllusioner: the Illusioner resolves to the 0.5/20 ctor args -- move 0.5x, fire every
// 20 ticks -- NOT the skeleton's 1.0/40. Cite Illusioner.registerGoals @6 RangedBowAttackGoal(0.5d,20,15).
func TestRangedBowParamsIllusioner(t *testing.T) {
	g := newRangedBowAttackGoal()
	g.resolveBowParams(bowParamEntity(entity.Illusioner.ID))
	if math.Abs(g.speedModifier-0.5) > 1e-12 {
		t.Fatalf("illusioner bow speedModifier = %v, want 0.5", g.speedModifier)
	}
	if g.attackIntervalMin != 20 {
		t.Fatalf("illusioner bow attackIntervalMin = %d, want 20", g.attackIntervalMin)
	}
}

// TestRangedBowParamsLatchOnce: resolveBowParams is idempotent -- a second call with a DIFFERENT type does
// not clobber the latched values (the goal is bound to its first ticking entity).
func TestRangedBowParamsLatchOnce(t *testing.T) {
	g := newRangedBowAttackGoal()
	g.resolveBowParams(bowParamEntity(entity.Illusioner.ID))
	g.resolveBowParams(bowParamEntity(entity.Skeleton.ID)) // must NOT overwrite
	if math.Abs(g.speedModifier-0.5) > 1e-12 || g.attackIntervalMin != 20 {
		t.Fatalf("after re-resolve: speed=%v interval=%d, want latched 0.5/20", g.speedModifier, g.attackIntervalMin)
	}
}
