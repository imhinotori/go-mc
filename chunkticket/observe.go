package chunkticket

import "github.com/imhinotori/sulfur/level"

// ObserveLoadedSquare is the observe-only seam wiring this ticket subsystem alongside
// the existing view-distance loader (server/world_stream.go). Given the player chunk
// centers and the (already server-clamped) view distance, it builds a DistanceManager,
// injects the player tickets exactly as DistanceManager.addPlayer + PlayerTicketTracker
// would, runs the propagation to a fixed point, and returns every column the ticket
// model considers loaded (loading level <= MAX_LEVEL).
//
// The PLAYER_LOADING tickets sit at PLAYER_TICKET_LEVEL(31) on every column within
// viewDistance of a player, so the loaded set is the union of the (2*viewDistance+1)^2
// squares around the players -- identical to the loader centerOutRing square for one
// player. CITE: DistanceManager.addPlayer / PlayerTicketTracker.updateViewDistance.
//
// SEAM: the live tick loop still drives loading via centerOutRing; TestObserveMatchesRing
// pins that this ticket-derived set equals that square so the switch is behaviour-neutral.
func ObserveLoadedSquare(centers []level.ChunkPos, viewDistance, simulationDistance int) map[level.ChunkPos]bool {
	dm := NewDistanceManager(simulationDistance)
	dm.UpdateViewDistance(viewDistance)
	for _, c := range centers {
		dm.AddPlayer(c)
	}
	dm.RunAllUpdates()
	out := make(map[level.ChunkPos]bool)
	for _, p := range dm.LoadedChunks() {
		out[p] = true
	}
	return out
}
