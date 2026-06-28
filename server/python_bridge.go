package server

// python_bridge.go is the WORLD-BRIDGE (PLUGIN-06, Plan 26-03) — the cgo-free,
// BOTH-BUILDS half of the off-tick-python → tick-owned-world boundary. It defines
// the MUTATION-REQUEST types (plain scalars, NEVER a *py.Object or a live handle)
// and their owner-side apply through the EXISTING Phase-23 tick-owned seams
// (ChunkManager.SetBlock + broadcastBlockUpdate, the spawn seam). It compiles in
// the DEFAULT (no-tag) build: it carries no cgo, only the plain pythonMutation /
// pythonReadReq values + a server-side WorldBridge the python lane talks to through
// a plain-Go interface. The gopy-side REQUEST PRODUCERS (the set_block/spawn/log
// builtins handed to the .py) live behind //go:build python in
// python_bridge_python.go + plugin/python/bridge_python.go, so the server's default
// graph stays pure-Go static with ZERO gopython (THE #1 gate).
//
// THE SAFETY BOUNDARY (26-CONTEXT decision 4, the highest-risk surface):
//   - A WRITE: off-tick python builtin → constructs a pythonMutation (plain coords +
//     state, stamped with the owning plugin's capSet) → sends it on asyncIn2 → the
//     OWNER drains it in applyAsyncResults → pythonMutation.applyTo enforces the
//     capSet FIRST, then applies through the SAME Phase-23 seam worldHandle.setBlock
//     uses (t.world.SetBlock + broadcastBlockUpdate). This is the ONLY mutation point
//     (TICK-05). NO live tick-owned handle ever escapes to the off-tick goroutine —
//     the request/apply indirection IS the boundary (threat T-26-03).
//   - A READ: off-tick python builtin → sends a pythonReadReq (coords + a reply
//     channel) on asyncIn2 → the OWNER snapshots t.world.GetBlock and sends the
//     copied scalar back on the reply channel → the builtin returns the copy. The
//     off-tick side holds no live world reference — it gets a value copy snapshotted
//     on the owner.
//   - Capability-gated: a request carries the owning plugin's capSet (parsed from the
//     manifest `capabilities` at load via the SAME parseCapabilities the Starlark
//     handles use). applyTo drops a request the plugin lacks the grant for and logs a
//     capError (threat T-26-09 — no silent elevation).

import (
	"log"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
)

// mutationKind is the MINIMAL world-bridge vocabulary (26-CONTEXT decision 4 — keep
// it SMALL, leave the rest incremental). set_block + a simple spawn + log are the
// locked v1 surface; richer mutations (block entities, item drops, attribute edits)
// are DEFERRED to a follow-up plan once the round-trip is proven (cited deferral).
type mutationKind uint8

const (
	// mutSetBlock requests a world block change (capWorldWrite). Carries x,y,z,state.
	mutSetBlock mutationKind = iota
	// mutSpawn requests a simple entity spawn (capEntitiesWrite). Carries x,y,z (a
	// vanilla pig — the one spawn seam wired in Phase-8/24, spawnVanillaPig). The
	// `state` field is unused for a spawn (the spawn vocabulary is intentionally one
	// entity for v1; a typed spawn is the cited deferral).
	mutSpawn
	// mutLog requests a log line (no capability — observing/printing is harmless).
	// Carries the message in `note`.
	mutLog
)

// pythonMutation is a single off-tick-python mutation REQUEST. It carries ONLY plain
// scalars + the owning plugin's capSet + attribution — NEVER a *py.Object or a live
// *Entity/*ChunkManager pointer (the request/apply indirection is the safety
// boundary, 26-CONTEXT decision 4 / threat T-26-03). It satisfies the EXISTING
// asyncResult interface{ applyTo(*TickLoop) } (tick.go, UNCHANGED), so it rides the
// SAME asyncIn2 rejoin lane pathReady/pythonHookReady use; the owner drains it in
// applyAsyncResults and applyTo is the ONLY mutation point (TICK-05).
type pythonMutation struct {
	kind    mutationKind // which mutation (set_block / spawn / log)
	x, y, z int          // the target block/spawn coordinates (plain values)
	state   int          // the block state id for set_block (unused for spawn/log)
	note    string       // the message for log (unused for set_block/spawn)
	caps    capSet       // the owning plugin's grant, stamped at the request boundary
	plugin  string       // attribution for the capError / dropped-request log
}

// applyTo runs on the OWNER goroutine inside applyAsyncResults — the world-bridge
// apply (PLUGIN-06, the ONLY mutation point, TICK-05). It enforces the capability
// gate FIRST (the Phase-23 capSet — a denied request is DROPPED with a capError-style
// log, NEVER applied, threat T-26-09), then applies through the EXISTING Phase-23
// tick-owned seam — literally the worldHandle.setBlock body (t.world.SetBlock +
// broadcastBlockUpdate) for set_block, the existing spawnVanillaPig seam for spawn.
// It re-validates the world is loaded (a set_block on an unloaded column is a no-op,
// exactly like worldHandle.setBlock returning False). Because the request carries
// plain scalars, a request tolerated 1+ ticks late is always safe to apply.
func (m pythonMutation) applyTo(t *TickLoop) {
	switch m.kind {
	case mutSetBlock:
		// CAPABILITY GATE FIRST (threat T-26-09): a plugin without world.write may NOT
		// mutate the world — DROP + log the denial (the author SEES it via the log, the
		// off-tick capError-equivalent; the request never reaches SetBlock).
		if !m.caps.has(capWorldWrite) {
			log.Printf("python plugin %q: %v (dropped set_block request at %d,%d,%d)",
				m.plugin, capError("world.write"), m.x, m.y, m.z)
			return
		}
		if t.world == nil {
			return // no world loaded: nothing to mutate (mirrors worldHandle.setBlock's nil guard)
		}
		// Apply through the EXACT Phase-23 world.write seam (worldHandle.setBlock body):
		// SetBlock persists in the tick-owned world; broadcastBlockUpdate tells the clients.
		pos := pk.Position{X: m.x, Y: m.y, Z: m.z}
		changed := t.world.SetBlock(pos, block.StateID(m.state), dimMinY)
		if changed {
			t.broadcastBlockUpdate(pos, block.StateID(m.state))
		}

	case mutSpawn:
		// CAPABILITY GATE FIRST (threat T-26-09): a spawn requires entities.write.
		if !m.caps.has(capEntitiesWrite) {
			log.Printf("python plugin %q: %v (dropped spawn request at %d,%d,%d)",
				m.plugin, capError("entities.write"), m.x, m.y, m.z)
			return
		}
		if t.entities == nil {
			return // defensive: the store is non-nil from NewTickLoop
		}
		// Apply through the EXISTING spawn seam (the Phase-8/24 spawnVanillaPig — the
		// only entity-add the server exposes today; a typed spawn is the cited deferral).
		// The add + id allocation are owner-only (TICK-05), so this is race-safe.
		t.spawnVanillaPig(float64(m.x)+0.5, float64(m.y), float64(m.z)+0.5)

	case mutLog:
		// No capability gate — a log line is harmless. Attributed to the plugin.
		log.Printf("python plugin %q: %s", m.plugin, m.note)
	}
}

// pythonReadReq is a single off-tick-python READ request (block_at). It carries the
// target coordinates + a buffered reply channel (capacity 1). The off-tick builtin
// sends it on asyncIn2 and BLOCKS on `reply`; the OWNER snapshots t.world.GetBlock in
// applyTo and sends back the COPIED scalar (state id + ok), so the off-tick side
// never holds a live world reference — it gets a value copy snapshotted on the owner
// (26-CONTEXT decision 4: a read goes request → tick-snapshot → return-a-copy). It
// satisfies asyncResult, so it rides the SAME asyncIn2 lane.
type pythonReadReq struct {
	x, y, z int                // the block to read on the owner
	caps    capSet             // the owning plugin's grant (capWorldRead enforced on apply)
	plugin  string             // attribution for a denied read log
	reply   chan pythonReadRes // buffered (cap 1) — the owner sends the snapshot copy back
}

// pythonReadRes is the value copy the owner snapshots and hands back: a block state
// id + an ok flag (false for an unloaded column or a denied read). Plain scalars only.
type pythonReadRes struct {
	state int
	ok    bool
}

// applyTo runs on the OWNER goroutine inside applyAsyncResults — the read-snapshot.
// It enforces capWorldRead FIRST (a denied read returns ok=false + logs), then
// snapshots t.world.GetBlock and sends the COPIED scalar back on the reply channel.
// The send is non-blocking by construction (reply is buffered cap 1, written once);
// the off-tick builtin is the sole reader. NO live world reference crosses back — the
// off-tick side receives only the value copy (threat T-26-03).
func (r pythonReadReq) applyTo(t *TickLoop) {
	if !r.caps.has(capWorldRead) {
		log.Printf("python plugin %q: %v (dropped block_at request at %d,%d,%d)",
			r.plugin, capError("world.read"), r.x, r.y, r.z)
		r.reply <- pythonReadRes{ok: false}
		return
	}
	if t.world == nil {
		r.reply <- pythonReadRes{ok: false}
		return
	}
	pos := pk.Position{X: r.x, Y: r.y, Z: r.z}
	state, ok := t.world.GetBlock(pos, dimMinY)
	r.reply <- pythonReadRes{state: int(state), ok: ok}
}

// serverWorldBridge is the server-side, cgo-free WorldBridge the off-tick python
// builtins talk to (through the host.WorldBridge plain-Go interface, no *py.Object).
// It is STAMPED with one plugin's capSet at load (parsed from the manifest via the
// SAME parseCapabilities the Starlark handles use), so every request it produces
// carries that plugin's grant. Its methods CONSTRUCT a plain request and ENQUEUE it
// on asyncIn2 (the owner drains + applies) — they NEVER mutate tick state themselves
// (the off-tick goroutine calls them; the mutation happens on the owner in applyTo).
type serverWorldBridge struct {
	t      *TickLoop
	caps   capSet
	plugin string
}

// compile-time assertion: the server bridge satisfies the host's plain-Go seam.
var _ host.WorldBridge = (*serverWorldBridge)(nil)

// SetBlock enqueues a set_block mutation REQUEST onto asyncIn2 (the owner applies it
// through the Phase-23 seam in pythonMutation.applyTo). Called OFF-TICK by the python
// builtin; it only constructs + sends a plain value (no tick-state touch here). The
// send is non-blocking-safe: asyncIn2 is bounded (asyncIn2Buffer); a full channel
// would block the off-tick worker, never the tick — but the small pluginPool +
// drop-on-overload upstream bounds the request rate (threat T-26-10).
func (b *serverWorldBridge) SetBlock(x, y, z, state int) {
	b.t.asyncIn2 <- pythonMutation{
		kind: mutSetBlock, x: x, y: y, z: z, state: state, caps: b.caps, plugin: b.plugin,
	}
}

// Spawn enqueues a spawn mutation REQUEST (the owner applies it through the existing
// spawn seam). Off-tick; constructs + sends a plain value only.
func (b *serverWorldBridge) Spawn(x, y, z int) {
	b.t.asyncIn2 <- pythonMutation{
		kind: mutSpawn, x: x, y: y, z: z, caps: b.caps, plugin: b.plugin,
	}
}

// Log enqueues a log REQUEST (the owner prints it attributed to the plugin). Off-tick.
func (b *serverWorldBridge) Log(msg string) {
	b.t.asyncIn2 <- pythonMutation{kind: mutLog, note: msg, caps: b.caps, plugin: b.plugin}
}

// BlockAt issues a READ request and BLOCKS on the reply (request → owner snapshot →
// return-a-copy). The owner snapshots t.world.GetBlock in pythonReadReq.applyTo and
// sends the copied scalar back; this returns it. NO live handle crosses — only the
// value copy. Called OFF-TICK by the block_at builtin. The reply channel is buffered
// (cap 1) so the owner's send never blocks.
func (b *serverWorldBridge) BlockAt(x, y, z int) (int, bool) {
	reply := make(chan pythonReadRes, 1)
	b.t.asyncIn2 <- pythonReadReq{x: x, y: y, z: z, caps: b.caps, plugin: b.plugin, reply: reply}
	res := <-reply
	return res.state, res.ok
}

// newServerWorldBridge builds a per-plugin world bridge stamped with the capSet
// parsed from the plugin's manifest capability strings (the SAME parseCapabilities
// the Phase-23 Starlark handles use, so the python lane reuses the EXACT capability
// derivation). An unknown capability string is rejected LOUDLY here (parseCapabilities
// errors) — consistent with the load-time "unknown capability errors" discipline.
func (t *TickLoop) newServerWorldBridge(plugin string, capabilities []string) (*serverWorldBridge, error) {
	caps, err := parseCapabilities(capabilities)
	if err != nil {
		return nil, err
	}
	return &serverWorldBridge{t: t, caps: caps, plugin: plugin}, nil
}
