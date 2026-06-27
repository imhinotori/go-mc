package structure

// village_entities.go — STRUCT-POLISH-02 Task 3: the village template entity spawns.
//
// A village is assembled from jigsaw .nbt templates (jigsaw_placement.go). Some of those
// templates store INHABITANTS in their `entities` list — a cat (village/common/animals/cat_*), a
// villager (village/<biome>/villagers/*), an iron golem — which template.go ALREADY parses + keeps
// as StructureTemplate.entities (no offline extraction; A4 resolved -> the list is in hand). This
// file ports StructureTemplate.placeEntities: for each stored entity, transform its position by
// the SAME pivot rotation/mirror the blocks use (transformPos / calculateRelativePosition), offset
// by the piece origin, clip to the chunk's writable box, read the entity id from its NBT, and
// RECORD a SpawnRequest through the view (the tick performs the only store add — Pitfall 5, never
// an off-tick live entity).
//
// Source: javap StructureTemplate.placeEntities + lambda$placeEntities$0:
//   - blockPos2 = transform(info.blockPos, mirror, rot, pivot).offset(origin)  [the box.isInside clip]
//   - vec3      = transform(info.pos,      mirror, rot, pivot).add(origin)      [the float spawn pos]
//   - read info.nbt "id"; createEntityIgnoreException; snapTo(vec3); finalizeSpawn(STRUCTURE).

import (
	"github.com/imhinotori/sulfur/world/levelgen"
)

// entityNBTID is the entity NBT's `id` field (the entity registry id, e.g. "minecraft:villager"
// or "minecraft:cat"). The .nbt entity compound stores it directly; that is all the spawn record
// needs (the tick resolves it to a data/entity type). Other entity NBT (Brain/VillagerData/...)
// is the finalizeSpawn/attribute subsystem's concern, deferred behind the cited stub.
type entityNBTID struct {
	ID string `nbt:"id"`
}

// PlaceEntities ports StructureTemplate.placeEntities: emit a SpawnRequest for every stored
// template entity at its rotation/mirror/origin-transformed world position, clipped to box.
//
// For each stored rawEntity:
//  1. blockPos = transformPos(entity.blockPos) + origin  -> the integer clip pos (box.isInside).
//  2. vec3     = transformVec3(entity.pos)     + origin  -> the float spawn position (snapTo).
//  3. read entity.NBT "id"; record SpawnRequest{id, vec3, PersistenceRequired} via view.RecordSpawn.
//
// pivotX/pivotZ default to (0,0) for the village pool elements (SinglePoolElement uses the ZERO
// pivot, same as the block placement). A missing/garbled entity id (no "id" field) is SKIPPED
// (createEntityIgnoreException's "ignore exception" — a build-data robustness no-op, never panic).
//
// finalizeSpawn (the mob's attribute/variant/profession init — cat variant, villager profession)
// is the CITED STUB on the tick side (server/structure_spawn.go): the mob spawns at its vanilla
// DEFAULT state, structured to become a real finalizeSpawn read when the attribute subsystem
// lands — never baked away (CLAUDE.md). PersistenceRequired is set: a structure inhabitant does
// not despawn (the jar marks village mobs persistent via their structure-spawn path).
//
// The rng is threaded for signature parity with PlaceInWorld (vanilla's placeEntities does not
// itself draw for the village inhabitants — the per-entity rotate/mirror is deterministic — but
// keeping it lets a future entity that DOES draw stay in lockstep).
func (t *StructureTemplate) PlaceEntities(view WorldGenView, origin Pos, rot Rotation, mir Mirror, pivotX, pivotZ int, box BoundingBox, _ levelgen.RandomSource) {
	for _, e := range t.entities {
		// (1) The clip pos: transform the entity's integer blockPos about the pivot, then offset
		// by origin (CFR transform(blockPos).offset(offsetPos)).
		bx, by, bz := transformPos(e.BlockPos[0], e.BlockPos[1], e.BlockPos[2], mir, rot, pivotX, pivotZ)
		wbx, wby, wbz := origin.X+bx, origin.Y+by, origin.Z+bz
		if !box.IsInside(wbx, wby, wbz) {
			continue // the cross-chunk clip (Pitfall #2) — only the owning chunk records this entity
		}

		// (2) The float spawn position: transform the entity's Vec3 pos about the pivot, then add
		// origin (CFR transform(vec3).add(offsetPos.x, .y, .z)). The transform of a float pos uses
		// the SAME pivot rotation; transformVec3 mirrors transformPos but preserves the fraction.
		fx, fy, fz := transformVec3(e.Pos, mir, rot, pivotX, pivotZ)
		wx := float64(origin.X) + fx
		wy := float64(origin.Y) + fy
		wz := float64(origin.Z) + fz

		// (3) The entity id from the stored NBT. A missing id -> skip (createEntityIgnoreException).
		var idTag entityNBTID
		if e.NBT.Type == 0 {
			continue // no entity NBT -> nothing to spawn
		}
		if err := e.NBT.Unmarshal(&idTag); err != nil || idTag.ID == "" {
			continue // garbled / id-less entity -> skip (the "ignore exception" robustness)
		}
		view.RecordSpawn(SpawnRequest{
			EntityType:          idTag.ID,
			X:                   wx,
			Y:                   wy,
			Z:                   wz,
			PersistenceRequired: true,
		})
	}
}

// transformVec3 ports StructureTemplate.transform(Vec3, mirror, rotation, pivot): the FLOAT
// analogue of transformPos. Mirror flips the axis about the block grid (LEFT_RIGHT: z -> 1-z;
// FRONT_BACK: x -> 1-x — the jar uses 1.0 - coord for the float mirror, NOT -coord, because an
// entity sits at a fractional cell), then rotate about the (px,pz) pivot. The rotation formulas
// mirror transformPos with the float pivot terms (+1 on the rotated axis the jar adds for the
// half-cell, matching CFR's `(double)(px - pz) + z` etc. with the +1 the integer transform omits).
//
// CFR StructureTemplate.transform(Vec3, ...):
//
//	LEFT_RIGHT : z -> 1.0 - z   ;  FRONT_BACK : x -> 1.0 - x
//	CCW90 : ( px - pz + z, y, px + pz + 1 - x )
//	CW90  : ( px + pz + 1 - z, y, pz - px + x )
//	CW180 : ( px*2 + 1 - x, y, pz*2 + 1 - z )
//	NONE  : ( x, y, z )
//
// For the village pool elements the pivot is ZERO (px=pz=0) and rotation is the piece rotation;
// the +1 half-cell terms keep the entity centered in its rotated cell (jar-exact). Source: CFR
// StructureTemplate.transform(Vec3, Mirror, Rotation, BlockPos) (the floating-point overload).
func transformVec3(pos []float64, mir Mirror, rot Rotation, pivotX, pivotZ int) (x, y, z float64) {
	// Defensive: a malformed entity Pos (not a 3-list) -> origin-relative zero (no panic).
	if len(pos) != 3 {
		return 0, 0, 0
	}
	x, y, z = pos[0], pos[1], pos[2]

	// Mirror first (float mirror uses 1.0 - coord, not -coord — the half-cell reflection).
	switch mir {
	case MirrorLeftRight:
		z = 1.0 - z
	case MirrorFrontBack:
		x = 1.0 - x
	}
	px, pz := float64(pivotX), float64(pivotZ)
	switch rot {
	case RotCounterclockwise90:
		return px - pz + z, y, px + pz + 1.0 - x
	case RotClockwise90:
		return px + pz + 1.0 - z, y, pz - px + x
	case RotClockwise180:
		return px*2 + 1.0 - x, y, pz*2 + 1.0 - z
	default: // RotNone
		return x, y, z
	}
}
