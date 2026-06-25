package structure

import (
	"math"

	"github.com/imhinotori/sulfur/world/levelgen/density"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// SurfaceSampler is the heightmap-at-STARTS column query (Pitfall #9): a cheap
// "surface Y at world (x,z)" that does NOT require the owner chunk's blocks to exist.
// Scattered temples (project_start_to_heightmap WORLD_SURFACE_WG) read this at STARTS
// time, BEFORE that chunk generates. 14-02/14-03 use it to choose a temple's Y.
type SurfaceSampler interface {
	// SampleSurfaceY returns the world-Y of the preliminary terrain surface at
	// (wx,wz) — vanilla's getBaseHeight, NOT the placed-block heightmap.
	SampleSurfaceY(wx, wz int) int
}

// routerSurfaceSampler samples the noise router's PreliminarySurfaceLevel density node
// directly — ONE density compute per quart cell, NO NoiseChunk fill, NO owner chunk.
//
// DECISION (Pitfall #9, documented): scattered structures need terrain height at STARTS
// time, before the chunk's blocks exist. Vanilla samples the noise generator's
// preliminary surface (getBaseHeight), NOT placed blocks — exactly
// NoiseChunk.preliminarySurfaceLevel(x,z) = floor(router.PreliminarySurfaceLevel.Compute
// (quart-snapped (x,0,z))). We sample that router node directly rather than generating
// the owner chunk (~768-corner NoiseChunk fill, ~116ms): SAME value (the router node is
// the source NoiseChunk reads), ~1/700 the cost, and it avoids a STARTS->Generate cycle.
//
// This is the PRELIMINARY surface (pre-carve, pre-surface-rule), exactly what vanilla's
// structure placement uses — NOT the final placed-block heightmap. The divergence is
// noted honestly: it is the same preliminary-vs-final gap vanilla itself has at placement.
type routerSurfaceSampler struct {
	router *router.Router
}

// NewRouterSurfaceSampler builds the column sampler over a bound router. PURE over
// (router seed, wx, wz): two calls at the same (x,z) return the same Y.
func NewRouterSurfaceSampler(r *router.Router) SurfaceSampler {
	return routerSurfaceSampler{router: r}
}

// SampleSurfaceY mirrors NoiseChunk.preliminarySurfaceLevel(x,z): snap x,z to quart
// resolution (QuartPos.toBlock(QuartPos.fromBlock)) and sample the bound
// preliminary_surface_level density function at (qx,0,qz), floored. No chunk fill.
func (s routerSurfaceSampler) SampleSurfaceY(wx, wz int) int {
	qx := (wx >> 2) << 2
	qz := (wz >> 2) << 2
	d := s.router.NoiseRouter.PreliminarySurfaceLevel.Compute(density.Context{X: qx, Y: 0, Z: qz})
	return int(math.Floor(d))
}
