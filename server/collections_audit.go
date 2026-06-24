package server

// collections_audit.go is the OPT-04 CONTENTION AUDIT (Phase 8, Wave 4, 08-06). It runs AFTER
// OPT-01 (08-02 async pathfinding), OPT-02 (08-04 async entity tracker), and OPT-03 (08-05 async
// mob spawning) have LANDED and revealed the real cross-async-boundary contention points. Its job
// is NOT to "xsync everything" — it is to DECIDE, per hot-path collection, between a plain
// (single-owner) map and a lock-free xsync/v4 type, JUSTIFIED by contention. The load-bearing
// finding, grounded in those three swaps:
//
//   Every OPT subsystem SNAPSHOTS its input ON the owner goroutine before submitting to a pool
//   worker. So NO pool worker ever reads a LIVE tick-owned collection — the worker reads an
//   immutable copy. Therefore every tick-owned collection STAYS A PLAIN MAP (single-owner =
//   faster than a sharded concurrent map). This is the 08-RESEARCH Open Question 1 / Pitfall 1
//   resolution: snapshot-and-stay-plain is the DEFAULT; xsync is introduced ONLY where a value is
//   GENUINELY written/read across the async boundary.
//
// OPT-04's literal requirement — "hot-path collections use lock-free/specialized variants
// (xsync/v4); worker pools use ants/v2" — is satisfied here CONCRETELY:
//
//   (1) The ants/v2 pools (pathPool / trackerPool / spawnPool, one per subsystem, from 08-01) ARE
//       the "worker pools use ants/v2" deliverable. Their sizing + non-blocking drop discipline is
//       recorded below.
//   (2) ONE justified xsync/v4 usage exercises the dep with a real contention rationale (NOT a
//       blanket map swap): asyncSubmitDrops, an xsync.Counter the off-tick submit paths Inc() and
//       the tick goroutine Value()-reads each tick to republish telemetry (TickStats.AsyncDrops).
//   (3) A gate (collections_audit_test.go) ENFORCES the snapshot discipline so a future change
//       that reintroduces a live cross-boundary read fails the build.
//
// ============================================================================================
// THE PER-COLLECTION CONTENTION AUDIT
// ============================================================================================
//
// Legend: "read LIVE by a worker?" = does an OPT-01/02/03 pool worker dereference this collection
// off-tick, concurrently with a tick-goroutine write? If NO (the worker reads an owner-built
// snapshot instead), the collection stays a PLAIN map — single-owner, no synchronization, faster.
//
// | Collection                | Owner      | Read LIVE by a worker? | Decision    | Rationale (which OPT snapshots it)                                                    |
// |---------------------------|------------|------------------------|-------------|--------------------------------------------------------------------------------------|
// | entityStore.byID          | tick       | NO                     | PLAIN map   | OPT-01 reads e.id (a value) on the owner; OPT-02 copies position+dims into the diff   |
// |                           |            |                        |             | snapshot on the owner; OPT-03 re-resolves the live count on the owner in applyTo.     |
// |                           |            |                        |             | tickAI/tickPhysics range a copied []*Entity snapshot, never the live map off-tick.    |
// | entityStore.buckets       | tick       | NO                     | PLAIN map   | near() returns a FRESH copied slice on the owner (entity_store.go:near). No worker     |
// |                           |            |                        |             | walks the bucket map; the broad-phase result is snapshotted before any Submit.        |
// | ChunkManager.columns      | tick       | NO                     | PLAIN map   | OPT-01 snapshotRegion + OPT-03 snapshotSpawnColumns COPY the needed solidity into an   |
// |                           |            |                        |             | immutable region/snapshot ON the owner; the worker reads the copy, never m.columns.   |
// | clientIndex               | tick       | NO                     | PLAIN map   | Only dispatch (owner) and drainRegistrations (owner) touch it; no worker resolves a   |
// |                           |            |                        |             | client off-tick. Results carry an entity-id VALUE re-resolved on the owner in applyTo. |
// | players ([]*tickPlayer)   | tick       | NO                     | PLAIN slice | OPT-02 snapshots each player's position+tracked on the owner before Submit; applyTo    |
// |                           |            |                        |             | re-resolves the player by id (playerByEntityID) on the owner. No worker ranges it.     |
// | tickPlayer.tracked        | tick       | NO                     | PLAIN map   | OPT-02 copies the tracked set into the diff snapshot on the owner; the worker computes |
// |                           |            |                        |             | a DELTA (added/removed value slices); applyTo mutates p.tracked owner-side only.       |
// | tickPlayer.sentChunks     | tick       | NO                     | PLAIN map   | Touched only by tickChunks/flushOutbound on the owner; no async subsystem reads it.    |
// |---------------------------|------------|------------------------|-------------|--------------------------------------------------------------------------------------|
// | idAlloc (EntityIDAllocator)| tick field | YES (atomic)           | atomic.Int32| THE genuine cross-boundary primitive: the accept goroutine claims a player id OFF-tick |
// |                           |            |                        | (NOT xsync) | while the tick claims entity ids ON-tick — both through one atomic.Add. An atomic      |
// |                           |            |                        |             | counter IS the lock-free/specialized variant for a counter; xsync would not "upgrade"  |
// |                           |            |                        |             | it. So OPT-04 is ALREADY satisfied here by the existing atomic — no swap (T-6-08).     |
// | asyncSubmitDrops          | shared     | YES (multi-writer+read)| xsync.Counter| THE justified xsync/v4 use: the submit paths (OPT-01/02/03 via submitOrDrop) Inc() it  |
// |                           |            |                        |             | and the tick Value()-reads it each tick for telemetry. A plain int64 would race the    |
// |                           |            |                        |             | read against the increments; xsync.Counter is the correct striped lock-free primitive. |
//
// CONCLUSION (snapshot-and-stay-plain): EVERY tick-owned collection stays a PLAIN map/slice. The
// snapshot discipline of OPT-01/02/03 means no worker reads a live tick-owned collection, so a
// concurrent map would only add overhead with zero correctness benefit (08-RESEARCH Pitfall 1 /
// CLAUDE.md "a tick-only map stays a plain map — single-owner = faster"). The ONLY values crossing
// the async boundary are (a) the immutable channel message (asyncResult on asyncIn2), (b) the
// already-lock-free idAlloc atomic, and (c) the one justified xsync.Counter below. xsync is NOT
// blanket-applied; each decision is justified by its contention (or lack thereof).
//
// ============================================================================================
// THE ants/v2 POOL CONFIRMATION (OPT-04 "worker pools use ants/v2")
// ============================================================================================
//
// pathPool / trackerPool / spawnPool (server/tick.go, constructed in NewTickLoop from 08-01) ARE
// the "worker pools use ants/v2" deliverable — ONE bounded, recycling pool per async subsystem
// (08-RESEARCH Pattern 1). Their audited properties:
//
//   - SIZING: pathPool = runtime.NumCPU() (pathfinding is the heavy, frequent, CPU-bound A*
//     compute — it wants every core). trackerPool / spawnPool = asyncSmallPoolSize (2): their
//     submits are SPARSE (a per-player visibility diff is cheap; a spawn scan runs only every
//     spawnInterval ticks), so a small pool is ample and caps idle workers. Sizing each pool to
//     its subsystem's steady state is the 08-RESEARCH Pitfall-4 guidance.
//   - BOUNDED + RECYCLING: ants caps the goroutine count and reuses workers, so a burst of submits
//     (many mobs pathing at once) can NEVER blow up the goroutine count — the "goroutine per mob"
//     blowup the ants pool exists to prevent.
//   - NON-BLOCKING DROP-ON-OVERLOAD (the bounded-pool guarantee): every pool is built with
//     ants.WithNonblocking(true) (newAsyncPool), so a Submit into a saturated pool returns
//     ants.ErrPoolOverload and submitOrDrop DROPS the work (the subsystem keeps its last action and
//     re-requests next tick) rather than parking the OWNER goroutine and stalling the tick
//     (08-RESEARCH Pitfall 4; the in-process analogue of world.Worker.Request's drop-on-full).
//   - RELEASED on shutdown by TickLoop.Close().
//
// No new pool is created in this plan — the three are confirmed and their rationale recorded.
//
// AsyncDrops surfaces the one justified xsync.Counter so observers (operators / tests) can read the
// async-substrate overload rate off the published telemetry snapshot WITHOUT touching the counter
// directly. It is the tick-goroutine read half of the cross-boundary counter: submitOrDrop (any
// submit path) increments asyncSubmitDrops; recordMSPT republishes its Value() into TickStats each
// tick. The xsync.Counter makes that concurrent inc-vs-read -race clean by construction.
func (t *TickLoop) AsyncDrops() int64 { return asyncSubmitDrops.Value() }
