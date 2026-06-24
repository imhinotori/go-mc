# Phase 7: AI, Pathfinding, Commands & Chat - Research

**Researched:** 2026-06-24
**Domain:** Faithful vanilla-26.2 mob AI (GoalSelector tick model), synchronous A* navigation behind a request→snapshot→result seam, vanilla-style mob spawning, server-side command dispatch, and chat broadcast — all SYNCHRONOUS, tick-owned, behind the Phase-3/6 seams (no Phase-8 async, zero new deps).
**Confidence:** HIGH (existing fork seams + the exact Java port targets verified on disk/jar this session; chat & spawn wire surfaces jar-derived, flagged for the capture-diff gate that has sealed every prior phase).

## Summary

Phase 7 fills the last empty stub in the fixed tick pipeline — `tickAI()` in `server/tick_phases.go` — and adds command dispatch + chat broadcast. The architecture seams it needs already exist and were proven race-clean in Phase 6: `tickAI()` runs between `tickEntities` and `tickPhysics`; the tick-owned `entityStore` (`byID` + per-column `buckets` + `near()` broad-phase) holds the mobs; `moveEntity`/`blockSolidAt`/`collidePlayer` (`server/physics.go`) are the per-axis swept collision substrate navigation drives through; `world.ChunkManager.GetBlock`/`SetBlock` are the world reads; the `entityTracker` already emits `TeleportEntity`/`RotateHead` for any moved mob; and `server/command` is a fork-provided Brigadier graph (dispatch + serialize-to-`ClientboundCommands`) waiting to be wired into the join flow and the chat-command packet.

The load-bearing constraint is the **STANDING MANDATE**: mob LOGIC must be PORTED DIRECTLY FROM JAVA (the unobfuscated 26.2 jar at `temp/cache/26.2-inner.jar`, javap-able, plus Paper/Leaf) so behavior is identical to a real server — a non-1:1 idiomatic-Go translation, never a GPL paste. This research names the EXACT classes. The decisive discovery for AI-02 is that **vanilla itself already implements the request→snapshot→result seam**: `PathFinder.findPath(PathNavigationRegion, Mob, Set<targets>, …)` reads a `PathNavigationRegion` — a *copied* `ChunkAccess[][]` snapshot built from min/max corners — never the live world. Porting that shape means async pathfinding (Phase 8 / OPT-01) swaps only the *executor*, exactly as the roadmap promises, with no rewrite. For chat (CMD-02), the jar confirms `ClientboundPlayerChat` carries the `globalIndex` VarInt first and drags the entire signed-message machinery (MessageSignature, SignedMessageBody$Packed, FilterMask, ChatType$Bound); `ClientboundSystemChat` is just `Component content` (NBT) + `Boolean overlay`. v1 should broadcast via **SystemChat** to skip the signature chain entirely, and handle the inbound `ServerboundChat` (decode message/timestamp/salt/signature/lastSeen, IGNORE the signing) — the globalIndex prefix is then *understood and documented* (CMD-02's literal ask) without shipping the fragile signed path.

**Primary recommendation:** Build five requirement-slices in dependency order. AI-01 first (port `GoalSelector`/`Goal`/`WrappedGoal` + a minimal passive-mob goal set, driven by `tickAI()` in the vanilla `serverAiStep` order: sensing → targetSelector → goalSelector.tick → tickRunningGoals). AI-02 next (port `PathFinder` A* + `WalkNodeEvaluator` + `PathNavigationRegion` snapshot + `GroundPathNavigation` path-following, executed INLINE behind a `pathRequest → snapshot → Path` function the Phase-8 pool will later run off-tick). AI-03 (a faithful-but-minimal `NaturalSpawner` per-chunk attempt). CMD-01 (wire the existing `command.Graph`: send `ClientboundCommands` at join, route `ServerboundChatCommand`, dispatch). CMD-02 (decode `ServerboundChat`, broadcast via `SystemChat`; document the PlayerChat globalIndex, defer signed PlayerChat to v2). Capture-diff two wire surfaces against a real vanilla 26.2 server: **`ClientboundCommands` (the command graph)** and **`ServerboundChat`/`SystemChat`** round-trips. The A* and goal logic are gameplay behavior (no wire), gated by the real-client visual check, not a byte-diff.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Mob goal/brain selection (AI-01) | Tick (`tickAI`, game state) | — | Goals are tick-owned mob state, mutated only on the tick goroutine (TICK-05). Ported from `GoalSelector.tick`. |
| A* path COMPUTE (AI-02) | Tick now (INLINE) → off-tick later | Phase 8 pool (OPT-01) | The compute is pure over an immutable `PathNavigationRegion` snapshot — the seam is async-ready by construction; Phase 7 runs it inline, Phase 8 swaps the executor. |
| A* path-following / motion (AI-02) | Tick (`tickAI` → `moveEntity`) | — | Stepping a mob along its `Path` mutates tick-owned position via the existing `entityStore.move`/`moveEntity`; must stay on-tick (bucket-consistency contract). |
| Mob spawning (AI-03) | Tick (`tickAI`, mutates `entityStore`) | World (read `ChunkManager` for placement) | Spawning adds entities to tick-owned state; placement checks read tick-owned chunks. Off-tick candidate scan is Phase 8 (OPT-03). |
| Command dispatch (CMD-01) | Tick (decode + `Graph.Execute` on owner) | Net (decode serverbound) | Commands mutate authoritative game state; must run on the tick goroutine. The graph SEND (`ClientboundCommands`) is per-connection at join. |
| Chat receive + broadcast (CMD-02) | Tick (decode + fan-out via `client.Send`) | Net (decode `ServerboundChat`) | Chat is server-mediated: decode on owner, broadcast `SystemChat` to every player's bounded outbound queue. |

## Standard Stack

Phase 7 adds **zero new third-party dependencies.** Everything is the fork's own packages + stdlib + ported-from-Java Go logic. (Per CLAUDE.md "Stack Patterns by Variant" and the TICK-05 mandate: do NOT introduce xsync/ants/conc — no async subsystem exists to optimize until Phase 8. The A* seam must be async-*ready*, executed *inline*.)

### Core (reuse — already on disk, verified this session)
| Package / Symbol | API surface | Purpose | Why reuse |
|---------|-------------|---------|-----------|
| `server.TickLoop.tickAI()` | empty stub at `tick_phases.go:122`, called between `tickEntities` and `tickPhysics` | The ONE fixed-pipeline slot Phase 7 fills (AI-01/02/03). | `[VERIFIED: tick_phases.go:122]` Do NOT add a new tick phase (preserves `TestTickPhaseOrder`); fold AI into this slot exactly as `tickDebug` folds into `tickEntities`. |
| `server.entityStore` | `add/get/remove/move`, `near(x,z,rangeChunks)`, per-column `buckets`, `byID` | Holds mobs; `near()` is the broad-phase for goals (LookAtPlayer) and spawn-cap accounting. | `[VERIFIED: entity_store.go]` Tick-owned plain maps; the bucket-consistency contract (`move()` re-buckets) is what keeps the tracker's `near()` correct after a mob walks. |
| `server.Entity` | `id/typ/uuid/x,y,z/vx,vy,vz/yaw,pitch,headYaw/onGround/width,height/metadata`, `AABB()`, `NewEntity(id, entity.Entity, x,y,z)` | The live mob instance the goals mutate and navigation moves. | `[VERIFIED: entity.go]` Snapshot-friendly plain-value hot fields — the same property that lets Phase-8 async copy a cheap path-request snapshot. |
| `(*TickLoop).moveEntity(e, dx,dy,dz)` | per-axis swept-AABB integrate + `onGround` + re-bucket via `entities.move` | Path-following motion: navigation produces a desired Δ; `moveEntity` resolves collision and lands the mob. | `[VERIFIED: physics.go:245]` Anti-tunneling per-axis sweep already correct; AI never re-implements collision. |
| `(*TickLoop).blockSolidAt(x,y,z int)` | non-air solid test via `world.GetBlock` | The block-passability primitive the ported `WalkNodeEvaluator` reads to classify a node (walkable/blocked/open). | `[VERIFIED: physics.go:76]` Single world-read mapping shared with physics — navigation and collision can never disagree on where a block is. |
| `world.ChunkManager.GetBlock(pos, minY)` | `(block.StateID, ok)`; `columnAndSection` mapping | The world read the path SNAPSHOT is built from and the spawner's placement checks use. | `[VERIFIED: world/manager.go:163]` `ok=false` for unloaded columns → treat as "don't path/spawn here" (mirrors physics treating it as air). |
| `level/block` | `block.IsAir(StateID)`, `block.ToStateID`, `block.StateList` | Node classification (air above + solid below = standable) and spawn-block validity. | `[VERIFIED: level/block]` 32366 states; `IsAir` is the passability test. |
| `data/entity` | `entity.Pig` (`ID 100`, Width/Height), 158 types | The v1 test mob (vanilla-renderable, already used by the throwaway debug pig). | `[VERIFIED: data/entity/entity.go:920,1546]` `entity.Pig.ID == 100`. The real ported AI REPLACES the debug pig's sinusoidal pacing. |
| `server/entity_encode.go` | `encodeTeleportEntity`, `encodeRotateHead`, `encodeMoveEntityPos/PosRot`, `encodeSetEntityData` | The tracker already emits these for any moved mob — AI-02 motion needs NO new entity encoder. | `[VERIFIED: entity_encode.go]` Jar-derived + capture-diff-sealed in Phase 6. A walking mob "just works" on the wire once `tickAI` moves it. |
| `server/command` (`command.Graph`) | `NewGraph()`, `Literal/Argument` builders, `Execute(ctx, cmd)`, `WriteTo` → `ClientboundCommands`, `ClientJoin(Client)` | The Brigadier dispatcher for CMD-01 — already serializes the command tree and parses an input string. | `[VERIFIED: server/command/*.go]` Fork-provided and complete enough for v1; wire it into the join flow + the chat-command route. |
| `chat.Message` | `WriteTo`/`MarshalNBT` (NBT Component), color/append builders | The `Component content` payload for `ClientboundSystemChat` (TRUSTED_STREAM_CODEC = NBT). | `[VERIFIED: chat/nbtmessage.go:19]` Already a `pk.FieldEncoder`; the Phase-2 double-encode bug is fixed. |
| `pk.*` codecs | `pk.VarInt`, `pk.String`, `pk.Long`, `pk.Boolean`, `pk.UUID`, `pk.Angle`, `pk.Position`, `pk.Marshal`, `pk.Packet.Scan` | Chat/command wire fields + defensive serverbound decode. | `[VERIFIED: net/packet/types.go]` |

### 776 Packet IDs (all present in `data/packetid` — verified)

| Requirement | Clientbound | Serverbound |
|-------------|-------------|-------------|
| AI-01/02/03 mob spawn/move | `ClientboundAddEntity`, `ClientboundSetEntityData`, `ClientboundTeleportEntity`, `ClientboundMoveEntityPos/PosRot/Rot`, `ClientboundRotateHead`, `ClientboundSetEntityMotion`, `ClientboundRemoveEntities` (all already wired in the tracker) | — |
| CMD-01 commands | `ClientboundCommands` | `ServerboundChatCommand`, `ServerboundChatCommandSigned`, `ServerboundCommandSuggestion` |
| CMD-02 chat | `ClientboundSystemChat` (v1), `ClientboundPlayerChat` / `ClientboundDisguisedChat` (jar-documented, deferred) | `ServerboundChat`, `ServerboundChatAck`, `ServerboundChatSessionUpdate` |

`[VERIFIED: data/packetid/packetid.go + *_string.go]` `ServerboundChat=9`, `ServerboundChatCommand=7`, `ClientboundSystemChat=121`, `ClientboundPlayerChat=65`, `ClientboundDisguisedChat=33`, `ClientboundCommands` present.

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| GoalSelector classic AI | Brain/Behavior memory system (`net.minecraft.world.entity.ai.Brain`, `behavior.*`) | The Brain system is HEAVIER (memory module + sensor + schedule + activity machinery) and is used by villagers/piglins/axolotls/warden. A v1 passive Pig uses the classic `GoalSelector` (Pigs are NOT brain mobs). **Port GoalSelector; defer Brain to a later phase.** See Open Question 1. |
| SystemChat broadcast (v1) | Full signed `PlayerChat` with globalIndex | PlayerChat drags MessageSignature + SignedMessageBody$Packed + FilterMask + ChatType$Bound + the chat-session/message-signature chain (jar-confirmed, see Wire Facts). SystemChat is `Component + Boolean` — renders identically as a server message. **Use SystemChat; document & defer signed PlayerChat to v2.** See Open Question 2. |
| Inline A* compute on the tick (Phase 7) | Off-tick goroutine pool now | TICK-05 forbids async in Phase 7; the SEAM is async-ready (pure compute over an immutable snapshot) but EXECUTED inline. Phase 8 (OPT-01) swaps the executor behind the seam. |
| Faithful-minimal `NaturalSpawner` | Full multi-category density spawner | v1 covers the per-chunk passive-creature attempt + cap accounting; defer mob-cap-per-player-distance refinements, structure spawns, and persistence-charge nuances. See Open Question 3. |
| Reuse `server/command` graph | Hand-roll a command parser | The fork graph already parses + serializes the Brigadier tree; rebuilding it is the exact "don't hand-roll" trap. |

**Installation:** None. No `go get`. `go.mod` is unchanged for Phase 7.

**Version verification:** No external packages added — no registry check applies. All "versions" are the pinned fork tree (branch `ender-776`); authority is the on-disk generated/fork code (re-read this session) and the unobfuscated `temp/cache/26.2-inner.jar` (javap, this session).

## Java Sources To Port

> **This is the load-bearing section for Phase 7 (the standing MANDATE).** Each row names the EXACT `net.minecraft.*` class in `temp/cache/26.2-inner.jar`, what to extract, and the v1 scope. All classes VERIFIED present this session (`javap -classpath temp/cache/26.2-inner.jar <class>` succeeded for every one). Port = idiomatic-Go non-1:1 translation of the ALGORITHM/BEHAVIOR; never a GPL paste. Wire layouts (entity move/spawn) are already sealed in Phase 6 and are NOT re-derived here.

### AI-01 — Goal model (port these)

| Class | Extract | v1 scope |
|-------|---------|----------|
| `net.minecraft.world.entity.ai.goal.GoalSelector` | The selection algorithm: `addGoal(priority int, goal)`, `tick()`, `tickRunningGoals(bool)`, and the **control-flag locking** (`Map<Goal$Flag, WrappedGoal> lockedFlags`, `disabledFlags`). `tick()` walks available goals; a goal can RUN iff every flag it needs is free (not locked by a higher-priority running goal); on start it locks its flags, on stop it frees them. `[VERIFIED: javap GoalSelector — addGoal(int,Goal); tick(); tickRunningGoals(boolean); lockedFlags/availableGoals/disabledFlags fields]` | Port fully — it is small and the heart of AI-01. |
| `net.minecraft.world.entity.ai.goal.Goal` (abstract) | The goal contract: `canUse()`, `canContinueToUse()` (defaults to `canUse`), `isInterruptable()`, `start()`, `stop()`, `tick()`, `requiresUpdateEveryTick()`, `getFlags()/setFlags(EnumSet<Flag>)`, `adjustedTickDelay(int)`/`reducedTickDelay(int)`. `[VERIFIED: javap Goal]` | Port the interface + default methods as a Go interface + embeddable base struct. |
| `net.minecraft.world.entity.ai.goal.Goal$Flag` | The four control flags: **MOVE, LOOK, JUMP, TARGET**. `[VERIFIED: javap Goal$Flag — exactly these 4]` | Port as a Go bitset/enum (4 bits). |
| `net.minecraft.world.entity.ai.goal.WrappedGoal` | The priority+running wrapper `GoalSelector` stores (priority int, isRunning, delegates the Goal contract). `[VERIFIED present]` | Port as a small struct. |
| `net.minecraft.world.entity.Mob.serverAiStep()` | **The tick ORDER** (jar-confirmed bytecode): `sensing.tick()` → `targetSelector.tick()` → `goalSelector.tick()` → `goalSelector.tickRunningGoals(canSimulate)`; then `customServerAiStep` → `aiStep` drives `navigation.tick()` + moveControl. `[VERIFIED: javap -c Mob serverAiStep — Sensing.tick, GoalSelector.tick ×2 (target+goal), GoalSelector.tickRunningGoals]` | Port the ORDER into `tickAI()`: for each mob, run targetSelector.tick (v1 may skip — passive), goalSelector.tick, tickRunningGoals, then navigation.tick + apply move. |
| Concrete goals for a passive Pig: `FloatGoal` (swim/avoid drowning), `WaterAvoidingRandomStrollGoal` (or `RandomStrollGoal`), `LookAtPlayerGoal`, `RandomLookAroundGoal`, `PanicGoal` | Each goal's `canUse/tick/start/stop` + its flags. `RandomStrollGoal`: pick a random reachable position within a radius, set the navigation target. `LookAtPlayerGoal`: find nearest player in range (via `near()`), face it. `[VERIFIED: all present in jar]` | v1: port `RandomStroll(Water-avoiding)` + `LookAtPlayer` + `RandomLookAround` for a visibly-alive Pig; `Float`/`Panic` optional (defer if no water/damage source in v1 test). |

**The Pig's actual goal set** (the canonical reference for "what a real Pig does"): javap `net.minecraft.world.entity.animal.pig.Pig.registerGoals` (note the moved package — `animal/pig/Pig.class`, not `animal/Pig`) to read the exact priorities. `[VERIFIED: jar path net/minecraft/world/entity/animal/pig/Pig.class]`

### AI-02 — Pathfinding (port these — the async-ready seam)

| Class | Extract | v1 scope |
|-------|---------|----------|
| `net.minecraft.world.level.PathNavigationRegion` | **THE SNAPSHOT TYPE — the async seam's keystone.** Constructed `(Level, BlockPos min, BlockPos max)`; it COPIES `ChunkAccess[][] chunks` for the bounded region and implements `getBlockState`/`getFluidState`/`getBlockEntity`/`getMinY` over that copy. The `PathFinder` reads ONLY this, never the live world. `[VERIFIED: javap PathNavigationRegion — protected final ChunkAccess[][] chunks; ctor(Level,BlockPos,BlockPos)]` | Port a Go `pathRegion` = an immutable snapshot of the block states in the bounding box around (mob → target), built on the tick from `ChunkManager`. **This is the request→snapshot→result boundary AI-02 mandates** — the snapshot is the immutable value an off-tick worker (Phase 8) consumes. |
| `net.minecraft.world.level.pathfinder.PathFinder` | **The A* core.** Ctor `(NodeEvaluator, maxVisitedNodes)`. Public `findPath(PathNavigationRegion region, Mob mob, Set<BlockPos> targets, float followRange, int accuracy, float searchDepthMultiplier)` → `Path`. Internals: `BinaryHeap openSet`, `distance(a,b) = a.distanceTo(b)` (the g-cost/edge), `getBestH(node, targets)` (the heuristic = min distance to any target), `reconstructPath`. The fixed-size `Node[] neighbors` (8) reused per expansion. `[VERIFIED: javap PathFinder — findPath signature, BinaryHeap openSet, distance(), getBestH(), reconstructPath()]` | Port the A* loop: pop lowest-f from the heap, expand neighbors via the evaluator, relax g, push, until target reached or `maxVisitedNodes`. Use a Go binary heap (`container/heap` or a hand-rolled one mirroring `BinaryHeap`). |
| `net.minecraft.world.level.pathfinder.BinaryHeap` | The open-set min-heap (insert/pop/changeCost). `[VERIFIED present]` | Port as a small index-tracked binary heap, or use stdlib `container/heap`. The vanilla one stores a heap index on each `Node` for O(log n) decrease-key. |
| `net.minecraft.world.level.pathfinder.Node` + `Target` | `Node{x,y,z, g, h, f, cameFrom, heapIdx, closed, …}`, `distanceTo`/`distanceManhattan`, hash by packed coord. `Target` wraps a goal node + bestH bookkeeping. `[VERIFIED present]` | Port the node struct + the packed-coordinate identity (so the open/closed sets dedupe). |
| `net.minecraft.world.level.pathfinder.WalkNodeEvaluator` (extends `NodeEvaluator`) | **The cost/passability heart.** `prepare(region, mob)`, `getStart()`, `getNeighbors(Node[], node)` (the 8 candidate moves incl. diagonals + step-up/down), `getPathType(...)` → `PathType` (OPEN/WALKABLE/BLOCKED/WATER/LAVA/FENCE/DOOR/…), and the **malus table**: each `PathType` has a movement penalty (e.g. WATER, DANGER_FIRE) the mob's `getPathfindingMalus(type)` weights; negative malus = impassable. `isNeighborValid`/`isDiagonalValid` reject corner-cutting. `getFloorLevel` snaps to the standable surface. `[VERIFIED: javap WalkNodeEvaluator — prepare, getNeighbors, getPathType*, findAcceptedNode, getFloorLevel, isNeighborValid, isDiagonalValid, SPACE_BETWEEN_WALL_POSTS]` | Port: a ground node is valid iff the block below is solid (`blockSolidAt`) AND the mob's height of air is clear above. Start with WALKABLE/BLOCKED/OPEN only; defer water/lava/fence/door malus to a later refinement (document the deferral). |
| `net.minecraft.world.entity.ai.navigation.PathNavigation` + `GroundPathNavigation` | The per-mob driver: `createPath(x,y,z, accuracy)` → builds the target set + region + calls `PathFinder.findPath`; `moveTo(x,y,z, speed)` sets the active `Path`; `tick()` advances the path index toward the next node and feeds the mob's moveControl; `isDone()`, `recomputePath()`, `shouldRecomputePath`. `createPathFinder(maxVisited)` is the abstract hook that wires `WalkNodeEvaluator`. `[VERIFIED: javap PathNavigation — createPath/moveTo/tick/isDone/getPath/recomputePath; GroundPathNavigation present]` | Port a `groundNavigation` per mob: `requestPath(targetX,Y,Z)` (the seam call) → snapshot region → A* → `Path`; `tick()` steps the mob toward `path.nextNode` by setting a desired Δ fed to `moveEntity`. |

**The seam contract (AI-02, the Phase-8 hinge):** structure pathfinding as a pure function

```
pathRequest{mob snapshot, targetX,Y,Z, region snapshot}  →  computePath(request)  →  Path (immutable)
```

`computePath` reads ONLY the snapshot (no `TickLoop`, no live `entityStore`, no `ChunkManager`). In Phase 7 `tickAI` calls it INLINE on the tick goroutine. In Phase 8 (OPT-01) the same `pathRequest` is handed to an `ants` pool; the resulting `Path` rejoins through the EXISTING `applyAsyncResults` seam (an `asyncResult` whose `applyTo` assigns the path to the mob), tolerated 1+ ticks late. **Do not let `computePath` close over tick-owned mutable state** — that is the single discipline that makes the swap free (mirrors how `chunkReady`/the chunk worker already works).

### AI-03 — Mob spawning (port these)

| Class | Extract | v1 scope |
|-------|---------|----------|
| `net.minecraft.world.level.NaturalSpawner` | `createState(spawnableChunkCount, entities, chunkGetter, mobCapCalculator)` (per-category live counts vs caps), `getFilteredSpawningCategories` (which categories are under cap), `spawnForChunk` / `spawnCategoryForChunk` (the per-chunk attempt: pick a random block column, find a standable Y, roll a mob from the biome's spawn list, place if `isValidEmptySpawnBlock` + `SpawnPlacements.checkSpawnRules`), `isValidEmptySpawnBlock`. Constants `SPAWN_DISTANCE_CHUNK`, `MAGIC_NUMBER` packing. `[VERIFIED: javap NaturalSpawner — createState, getFilteredSpawningCategories, spawnForChunk, spawnCategoryForChunk, isValidEmptySpawnBlock]` | v1: a faithful-but-minimal loop — for eligible loaded chunks near players, if the CREATURE category is under its per-chunk cap (`MobCategory.getMaxInstancesPerChunk()`), attempt one placement of the v1 test mob at a valid standable air block. Defer the full multi-category density math, structure spawns, and the `LocalMobCapCalculator` per-player distance weighting. |
| `net.minecraft.world.entity.MobCategory` | The category enum (**MONSTER, CREATURE, AMBIENT, AXOLOTLS, WATER_CREATURE, …**) + `getMaxInstancesPerChunk()` (the cap that bounds spawning). `[VERIFIED: javap MobCategory — the enum + getMaxInstancesPerChunk(); CREATURE cap is the relevant one for a Pig]` | Port the enum + the per-chunk cap. v1 only needs CREATURE. |
| `net.minecraft.world.entity.SpawnPlacements` | `checkSpawnRules(type, level, spawnType, pos, random)` + the placement TYPE (ON_GROUND / IN_WATER) and heightmap predicate per mob. `[VERIFIED present]` | Port the ON_GROUND check (solid below + 2 air above + light/heightmap predicate). v1 may relax the light check (document it). |

**The cap-accounting trap (Pitfall, below):** vanilla counts live mobs PER CATEGORY against a cap derived from `spawnableChunkCount × maxInstancesPerChunk`; getting the count wrong floods or starves the world. Use the tick-owned `entityStore` to count live mobs by category each spawn cycle.

### CMD-01 — Commands (mostly wire, minimal port)

No deep Java port needed — `server/command` already implements the Brigadier graph. The "port" is matching the SEND timing + the chat-command flow:
- `net.minecraft.network.protocol.game.ServerboundChatCommandPacket` — decode = **`readUtf()` → single String command** (no signing). `[VERIFIED: javap -c ServerboundChatCommandPacket — readUtf():String]` Route this string to `Graph.Execute`.
- `net.minecraft.commands.Commands` (server dispatcher) — the registration pattern (literal + argument nodes, permission gate). v1 registers a couple of literals (e.g. `/say`, `/me`) directly via the fork builders. `[present in jar if deeper reference needed]`

### CMD-02 — Chat (decode + understand the globalIndex)

| Class | Extract | v1 scope |
|-------|---------|----------|
| `net.minecraft.network.protocol.game.ServerboundChatPacket` | Decode order (jar bytecode): **`readUtf(256)` message → `readInstant()` timeStamp → `readLong()` salt → `readNullable(MessageSignature)` → `LastSeenMessages$Update`**. `[VERIFIED: javap -c ServerboundChatPacket constructor]` | Decode all five fields defensively; IGNORE the signature/lastSeen (server is authoritative, offline mode). Extract the message string, fan out. |
| `net.minecraft.network.protocol.game.ClientboundSystemChatPacket` | Fields = **`Component content` (TRUSTED_STREAM_CODEC = NBT) + `boolean overlay`**. STREAM_CODEC composites exactly those two. `[VERIFIED: javap ClientboundSystemChatPacket — content + overlay; static{} composite(TRUSTED_STREAM_CODEC, BOOL)]` | v1 broadcast: `pk.Marshal(ClientboundSystemChat, chat.Message{...}, pk.Boolean(false))`. The `chat.Message` WriteTo is the NBT Component. |
| `net.minecraft.network.protocol.game.ClientboundPlayerChatPacket` | The signed path (DOCUMENT, defer). Write order (jar bytecode): **`globalIndex` VarInt → `sender` UUID → `index` VarInt → nullable `MessageSignature` → `SignedMessageBody$Packed.write` (content/timeStamp/salt/lastSeen) → nullable `unsignedContent` Component → `FilterMask.write` → `ChatType$Bound.STREAM_CODEC` (Holder<ChatType> + name Component + Optional<Component> targetName)**. `[VERIFIED: javap -c ClientboundPlayerChatPacket write()]` | The CMD-02 "globalIndex prefix handled" is satisfied by DOCUMENTING this layout (globalIndex is field 1) and choosing SystemChat for v1. Defer the signed path to v2 (ONLINE mode). |

## Architecture Patterns

### System Architecture Diagram

```
                         ┌──────────────────────────────────────────────────────┐
   vanilla 26.2 client   │              TICK GOROUTINE (single owner)            │
        │   ▲            │                                                        │
   serverbound │ clientbound                                                      │
        ▼   │            │   drainInbound → dispatch (decode + route)             │
   ┌─────────────┐       │        │                                               │
   │ net reader  │─Intent─►   ServerboundChat ─────► broadcast queue (CMD-02)     │
   │ (1/conn)    │       │     ServerboundChatCommand ─► command.Graph.Execute    │
   └─────────────┘       │        │                              (CMD-01)         │
   ┌─────────────┐       │        ▼                                               │
   │ net writer  │◄─Send─┤   ── tickOnce() ORDERED PIPELINE ──                    │
   │ (1/conn)    │       │   resolveSubtickInputs                                 │
   └─────────────┘       │   tickWorld / tickChunks                               │
                         │   tickEntities                                         │
                         │   tickAI  ◄══════ PHASE 7 FILLS THIS STUB ═════════╗   │
                         │     │  per mob (ported serverAiStep order):        ║   │
                         │     │   1. goalSelector.tick / tickRunningGoals    ║   │
                         │     │      (AI-01: pick + run goals)               ║   │
                         │     │   2. navigation.tick:                        ║   │
                         │     │        if needs path → pathRequest ──┐       ║   │
                         │     │        ┌─ snapshot region from ◄─────┘       ║   │
                         │     │        │  ChunkManager (immutable)           ║   │
                         │     │        ▼                                     ║   │
                         │     │      computePath(request)  ── A* over ───────╫─► (Phase 8:
                         │     │        │   the SNAPSHOT only (AI-02)         ║    off-tick
                         │     │        ▼   = async-ready seam                ║    pool; rejoins
                         │     │      Path → step mob via moveEntity          ║    via applyAsync)
                         │     │   3. NaturalSpawner attempt (AI-03):         ║   │
                         │     │        count live by category vs cap →       ║   │
                         │     │        place test mob at valid air block ────╫─► entityStore.add
                         │     │                                              ║   │
                         │     └══════════════════════════════════════════════╝  │
                         │   tickPhysics (gravity/collide — runs AFTER AI moves)  │
                         │   applyAsyncResults (NO-OP; Phase 8 path rejoin)       │
                         │   tracker.Tick() ── emits TeleportEntity/RotateHead    │
                         │        │            for the moved mob (REUSED)         │
                         │   flushOutbound ── SystemChat broadcast, chunk stream  │
                         └────────┼──────────────────────────────────────────────┘
                                  │  reads (snapshot) ↓
                         ┌────────▼──────────┐
                         │  ChunkManager     │  (tick-owned; the path snapshot
                         │  GetBlock(pos)    │   is a COPY, never a live alias)
                         └───────────────────┘
```

Entry: `ServerboundChat`/`ServerboundChatCommand` route in `dispatch` (today both fall to the `default:` no-op — Phase 7 adds the cases). Mob AI runs entirely inside the `tickAI` slot. The path COMPUTE is a pure function over an immutable region snapshot (the seam); motion goes through the existing `moveEntity`; the moved mob is broadcast by the UNCHANGED tracker.

### Recommended Project Structure
```
server/
├── ai_goal.go          # NEW: ported GoalSelector + Goal interface + Goal$Flag bitset +
│                       #   WrappedGoal (AI-01). Pure tick-owned goal machinery.
├── ai_goals_passive.go # NEW: the concrete ported goals (RandomStroll, LookAtPlayer,
│                       #   RandomLookAround, optionally Float/Panic) for a v1 Pig (AI-01).
├── ai_mob.go           # NEW: per-mob AI state on/alongside Entity (goalSelector, navigation,
│                       #   moveControl target) + the serverAiStep-order driver tickAI calls.
├── pathfinder.go       # NEW: ported PathFinder A* + Node + binary heap + the
│                       #   computePath(pathRequest) PURE function (AI-02, the seam).
├── path_region.go      # NEW: ported PathNavigationRegion — the immutable block-state
│                       #   snapshot built from ChunkManager (AI-02 request→snapshot).
├── node_evaluator.go   # NEW: ported WalkNodeEvaluator (getNeighbors/getPathType/malus)
│                       #   reading blockSolidAt/GetBlock (AI-02).
├── navigation.go       # NEW: ported GroundPathNavigation (requestPath/moveTo/tick/isDone)
│                       #   stepping the mob via moveEntity (AI-02).
├── spawner.go          # NEW: ported NaturalSpawner-minimal (cap accounting via entityStore,
│                       #   per-chunk attempt, SpawnPlacements ON_GROUND check) (AI-03).
├── chat.go             # NEW: ServerboundChat decode + SystemChat broadcast (CMD-02).
├── commands.go         # NEW: build the command.Graph, send ClientboundCommands at join,
│                       #   route ServerboundChatCommand → Graph.Execute (CMD-01).
├── tick_phases.go      # EDIT: fill tickAI() — drive goals → navigation → spawn per mob.
├── tick.go             # EDIT: dispatch gains ServerboundChat / ServerboundChatCommand cases;
│                       #   tickPlayer/Entity gain AI + chat-broadcast plumbing.
├── debug.go            # EDIT/RETIRE: the sinusoidal debug pig is THROWAWAY — replace its
│                       #   motion with a real ported-AI mob (mandate); keep the spawn trigger.
└── gameplay_tick.go    # EDIT: AcceptPlayer sends ClientboundCommands in the bootstrap tail.
```

### Pattern 1: GoalSelector tick model behind `tickAI()` (AI-01)
**What:** Port `GoalSelector` — a priority-ordered set of `WrappedGoal`s with control-flag locking. Each tick: re-evaluate which goals `canUse()`/`canContinueToUse()`, respecting that a goal needs ALL its flags (MOVE/LOOK/JUMP/TARGET) free; start newly-eligible goals (locking flags), stop finished ones (freeing flags), then `tick()` every running goal.
**When to use:** `tickAI()`, per mob, in the jar-confirmed `serverAiStep` order (targetSelector first — v1 may skip for passive mobs — then goalSelector.tick, then tickRunningGoals).
**Critical:** runs ON the tick goroutine over tick-owned mob state (TICK-05). No goroutine, no xsync. A goal's `tick()` sets the navigation target or the look angle — it does NOT itself move the mob across goroutines.
```go
// Source: ported from GoalSelector.tick / Goal / Goal$Flag (javap-verified). Idiomatic Go,
// non-1:1. Runs inline on the tick goroutine.
func (t *TickLoop) tickAI() {
    t.trace("tickAI")
    for _, e := range t.aiMobsSnapshot() { // stable snapshot like tickPhysics
        m := e.ai // per-mob goalSelector + navigation
        m.goals.tick(t, e)         // start/stop goals by priority + flag locks
        m.goals.tickRunning(t, e)  // tick() every running goal
        m.navigation.tick(t, e)    // advance the active Path → desired Δ → moveEntity
    }
}
```

### Pattern 2: Request→snapshot→result A* (AI-02 — the Phase-8 hinge)
**What:** Port `PathFinder.findPath` + `WalkNodeEvaluator` + `PathNavigationRegion`. A path request snapshots the block states in the bounding box around (mob → target) into an immutable `pathRegion`, then runs A* over ONLY that snapshot, returning an immutable `Path`. The snapshot is what makes the compute relocatable off-tick (Phase 8) without a rewrite.
**When to use:** Inside `navigation.tick` when a mob needs a new path (no path, or `shouldRecomputePath`). Executed INLINE on the tick in Phase 7.
**Critical:** `computePath` must be a PURE function of its `pathRequest` — no `*TickLoop`, no live `entityStore`/`ChunkManager` pointer. Build the snapshot ON the tick (reads tick-owned chunks), then compute over the copy.
```go
// Source: ported from PathFinder.findPath(PathNavigationRegion, Mob, Set<targets>, …) +
// PathNavigationRegion (the COPIED ChunkAccess[][]). The snapshot is the immutable seam value.
type pathRequest struct {
    startX, startY, startZ int
    targetX, targetY, targetZ int
    region  *pathRegion // immutable block-state snapshot (PathNavigationRegion analogue)
    mobW, mobH float64   // mob bbox for node validity
    maxVisited int
}
// computePath reads ONLY req (no tick-owned state) → Phase 8 runs it in an ants pool unchanged.
func computePath(req pathRequest) *Path { /* A*: BinaryHeap open set, g=distanceTo, h=getBestH */ }

// On-tick caller (Phase 7): build snapshot from the tick-owned world, compute inline.
func (n *groundNavigation) requestPath(t *TickLoop, e *Entity, tx, ty, tz int) {
    region := snapshotRegion(t.world, e, tx, ty, tz) // reads ChunkManager.GetBlock → COPY
    n.path = computePath(pathRequest{ /* … */ region: region })
}
```

### Pattern 3: Path-following motion through the existing collision substrate (AI-02)
**What:** Port `GroundPathNavigation.tick` — advance the path's node index toward the next waypoint, compute a desired horizontal Δ toward it (+ jump when the next node is one block up), and feed it to the EXISTING `moveEntity` (which resolves collision, gravity, landing, and re-buckets). The tracker then emits `TeleportEntity`/`RotateHead` for the moved mob — no new entity encoder.
**When to use:** every tick a mob has an active `Path`.
**Critical:** motion goes through `moveEntity` so the bucket-consistency contract holds and the mob can't clip through walls; physics runs AFTER `tickAI` so gravity settles the post-move position.
```go
// Source: ported from GroundPathNavigation.tick + PathNavigation node-advance.
func (n *groundNavigation) tick(t *TickLoop, e *Entity) {
    if n.path == nil || n.path.done() { return }
    next := n.path.nextNode()
    dx, dz := next.x+0.5-e.x, next.z+0.5-e.z
    speed := n.speed // blocks/tick from the goal
    e.yaw = yawToward(dx, dz)
    t.moveEntity(e, clampStep(dx, speed), 0 /*gravity in tickPhysics*/, clampStep(dz, speed))
    if reached(e, next) { n.path.advance() }
}
```

### Pattern 4: Faithful-minimal NaturalSpawner (AI-03)
**What:** Port the per-chunk spawn attempt with cap accounting. Count live mobs by `MobCategory` from the tick-owned `entityStore`; for chunks near players, if CREATURE is under `maxInstancesPerChunk × spawnableChunks`, pick a random column, find a standable air block (`isValidEmptySpawnBlock` + `SpawnPlacements` ON_GROUND), and `entityStore.add` the test mob (which the tracker then spawns on clients via the existing `AddEntity`).
**When to use:** `tickAI()`, throttled (vanilla attempts every tick but most attempts no-op under cap); v1 can run every N ticks.
**Critical:** the cap count must read live mobs from `entityStore` each cycle (Pitfall: stale/uncounted mobs flood the world). Add via `entityStore.add` on the tick (TICK-05).

### Pattern 5: Command dispatch via the fork graph (CMD-01)
**What:** Build a `command.Graph` once (register v1 literals), send it per-connection as `ClientboundCommands` in the join bootstrap tail (so the client tab-completes), and route `ServerboundChatCommand`'s decoded string to `Graph.Execute` on the tick.
**When to use:** graph SEND at join (in `AcceptPlayer`/`sendPlayBootstrap` tail); `Execute` in `dispatch`/on-tick for `ServerboundChatCommand`.
**Critical:** `Execute` runs on the tick goroutine (commands mutate authoritative state); a parse error returns an error → reply via SystemChat, never panic (the dispatch `Scan`-error-to-no-op contract).
```go
// Source: server/command/serialize.go (ClientJoin → ClientboundCommands) + command.go (Execute).
// At join: cmdGraph.ClientJoin(clientAdapter)  // sends ClientboundCommands
// In dispatch (ServerboundChatCommand): decode readUtf string, then on-tick:
var s pk.String
if err := p.Scan(&s); err == nil { _ = cmdGraph.Execute(ctx, string(s)) }
```

### Pattern 6: Chat receive + SystemChat broadcast (CMD-02)
**What:** Decode `ServerboundChat` (message/timestamp/salt/signature/lastSeen — keep only the message string, ignore signing), then broadcast a `ClientboundSystemChat` (`chat.Message` Component + `overlay=false`) to every player's outbound queue.
**When to use:** `ServerboundChat` case in `dispatch`, resolved on the tick (so the player set is consistent).
**Critical:** decode defensively (Scan-error → no-op); broadcast via each player's `client.Send` (the bounded queue; the writeLoop stays the sole socket writer). Format like vanilla: `<name> message`.
```go
// Source: javap ServerboundChatPacket (readUtf(256), readInstant, readLong, readNullable(sig),
// LastSeenMessages$Update) + ClientboundSystemChatPacket (Component content + boolean overlay).
func (t *TickLoop) handleChat(p *tickPlayer, raw pk.Packet) {
    var msg pk.String; var ts pk.Long; var salt pk.Long // decode; ignore sig/lastSeen for v1
    if err := raw.Scan(&msg, &ts, &salt /*, sig, lastSeen */); err != nil { return }
    line := chat.Message{Text: "<" + p.name + "> " + string(msg)}
    out := pk.Marshal(int32(packetid.ClientboundSystemChat), line, pk.Boolean(false))
    for _, pl := range t.players { if pl.client != nil { pl.client.Send(out) } }
}
```

### Anti-Patterns to Avoid
- **Computing A* over the live world (or closing over `*TickLoop`):** breaks the async seam — Phase 8 can't move it off-tick, and an off-tick read would race. Snapshot first, compute over the copy.
- **Adding a new tick phase for AI:** fold into the existing `tickAI()` slot (preserves `TestTickPhaseOrder`), exactly as `tickDebug` folds into `tickEntities`.
- **Porting the Brain/Behavior system for a Pig:** Pigs are classic-GoalSelector mobs; the Brain machinery is unneeded weight for v1.
- **Shipping signed `PlayerChat` for v1:** the signature chain (MessageSignature, SignedMessageBody, MessageSignatureCache, session update) is offline-mode-irrelevant complexity. Use SystemChat; document the globalIndex layout.
- **Moving a mob without `moveEntity`/`entities.move`:** desyncs the per-section bucket the tracker's `near()` reads (stale-bucket bug) and lets mobs clip walls.
- **Spawning without cap accounting:** floods the world; count live mobs by category from `entityStore` each cycle.
- **Keeping the sinusoidal debug pig as the mob model:** it is a THROWAWAY `SULFUR_DEBUG` cosmetic — the mandate REQUIRES real ported AI to replace its motion.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Per-axis collision / landing for a walking mob | A new collision resolver | `(*TickLoop).moveEntity` | `[VERIFIED: physics.go:245]` Anti-tunneling sweep + onGround + re-bucket already correct. |
| Block passability test for nodes | A new world reader | `(*TickLoop).blockSolidAt` / `ChunkManager.GetBlock` | `[VERIFIED: physics.go:76, world/manager.go:163]` One mapping shared with physics — never disagree. |
| Mob broad-phase (nearest player, spawn-cap region) | A quadtree / new index | `entityStore.near(x,z,range)` | `[VERIFIED: entity_store.go:154]` Per-column buckets; vanilla buckets the same way. |
| Spawning/moving a mob on the wire | New AddEntity/TeleportEntity encoders | The existing `entityTracker` + `entity_encode.go` | `[VERIFIED: tracker.go, entity_encode.go]` A moved mob auto-broadcasts; Phase-6-sealed. |
| Command tree parse + serialize | A Brigadier reimplementation | `server/command` (`Graph`, builders, `ClientboundCommands` serialize) | `[VERIFIED: server/command/*.go]` Fork-provided and complete. |
| Chat Component encoding | A new NBT Component writer | `chat.Message` (`WriteTo`/`MarshalNBT`) | `[VERIFIED: chat/nbtmessage.go:19]` Already a `pk.FieldEncoder`; double-encode bug fixed in Phase 2. |
| Packed BlockPos / byte angles / VarInt | Manual bit-packing | `pk.Position`, `pk.Angle`, `pk.VarInt`, `degToByteAngle` | `[VERIFIED: net/packet/types.go, entity_encode.go:138]` |
| Binary min-heap for A* open set | A novel priority structure | `container/heap` (stdlib) or a `BinaryHeap`-faithful port | Stdlib heap is allocation-light and correct; vanilla's `BinaryHeap` adds decrease-key via a stored heap index — port that detail if profiling needs it. |

**Key insight:** ~70% of Phase 7 is REUSE (collision, world reads, broad-phase, entity wire, command graph, chat encoding) + faithful PORTS of named Java classes. The genuinely new Go is the ported goal/A*/spawn LOGIC and two small wire surfaces (chat in/out, command-graph send) — both jar-derived here and gated by the capture-diff method that sealed every prior phase.

## Runtime State Inventory

> Phase 7 is greenfield feature work (no rename/refactor/migration). All categories verified N/A this session.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | None new. (Mobs are NOT persisted to the entities region in v1 unless AI-03's plan opts in — entity persistence already exists from ENT-06 but spawning persistence is a deferrable nuance.) | None required for v1; if AI-03 persists spawned mobs, reuse the existing `save/region` entities path (ENT-06), no new store. |
| Live service config | None — pure Go server. | None. |
| OS-registered state | None. | None. |
| Secrets/env vars | None. (`SULFUR_DEBUG` is an existing toggle, not renamed.) | None. |
| Build artifacts | None — no module path or package renames this phase. | None. |

## Common Pitfalls

### Pitfall 1: A* reading the live world instead of an immutable snapshot
**What goes wrong:** if `computePath` reads `ChunkManager`/`entityStore` directly, Phase 8's off-tick swap races the tick (data race, corrupt paths), and even in Phase 7 a mid-compute block edit yields an inconsistent path.
**Why it happens:** it's the "easy" path — vanilla's API hides that `PathFinder` only ever touches the copied `PathNavigationRegion`, never `Level`.
**How to avoid:** build the `pathRegion` snapshot ON the tick (copy the block states in the bounding box), then compute over ONLY the copy. `computePath(pathRequest)` takes no `*TickLoop`. `[VERIFIED: PathNavigationRegion copies ChunkAccess[][]; PathFinder.findPath takes the region, not the Level]`
**Warning signs:** `-race` failure in Phase 8; mobs pathing through blocks that were just placed.

### Pitfall 2: The PlayerChat globalIndex / signed-chat wire trap
**What goes wrong:** trying to ship `ClientboundPlayerChat` mis-frames the packet — it has 8 fields starting with the `globalIndex` VarInt and including nullable MessageSignature, `SignedMessageBody$Packed`, FilterMask, and `ChatType$Bound` (a Holder + Component + Optional). One wrong field desyncs the stream; the signature also requires a real chat session the offline client never establishes.
**Why it happens:** the ≤773 wiki and training data predate the 1.21.x chat-session rework; the globalIndex (a recent addition) is easy to misplace.
**How to avoid:** **use `ClientboundSystemChat` for v1** (`Component content` + `Boolean overlay` — two fields, no signing). Document the PlayerChat layout (globalIndex first) to satisfy CMD-02's "prefix handled" without shipping it. `[VERIFIED: javap -c ClientboundPlayerChatPacket.write — globalIndex/sender/index/sig/body/unsignedContent/filterMask/chatType; ClientboundSystemChatPacket — content+overlay]`
**Warning signs:** client disconnect on chat ("decoder error"); chat never appears.

### Pitfall 3: Spawn cap miscount (flood or starvation)
**What goes wrong:** counting all entities (not per-category), or not counting at all, makes the world flood with mobs or never spawn any.
**Why it happens:** vanilla's `createState` builds a per-category live count vs a cap derived from spawnable-chunk count × `maxInstancesPerChunk`; it's easy to short-cut.
**How to avoid:** each spawn cycle, count live mobs by `MobCategory` from the tick-owned `entityStore`; only attempt CREATURE spawns when under cap. `[VERIFIED: NaturalSpawner.createState + MobCategory.getMaxInstancesPerChunk]`
**Warning signs:** dozens of pigs per chunk, or none ever appear.

### Pitfall 4: Goal flag-locking ignored (goals stomp each other)
**What goes wrong:** running multiple goals that all claim MOVE (e.g. stroll + panic) without flag-locking makes the mob jitter between conflicting targets.
**Why it happens:** the `GoalSelector` flag-lock (`lockedFlags`) is the non-obvious heart of correct goal arbitration.
**How to avoid:** port the lock: a goal runs only if all its flags are free; on start it locks them, on stop frees them; higher priority wins. `[VERIFIED: GoalSelector.lockedFlags/disabledFlags + tick()]`
**Warning signs:** mob vibrates in place; look and move fight each other.

### Pitfall 5: Moving a mob outside `moveEntity` (stale bucket + clip)
**What goes wrong:** writing `e.x/e.z` directly (not via `entities.move`/`moveEntity`) leaves the per-section bucket stale, so the tracker's `near()` misses the mob (it vanishes for players) and it can clip through walls.
**Why it happens:** the bucket is a DERIVED index; only `move()` keeps it consistent.
**How to avoid:** all mob motion goes through `moveEntity` (which calls `entities.move`). `[VERIFIED: entity_store.go:126 move(); physics.go:245 moveEntity]`
**Warning signs:** mob disappears when it walks across a chunk boundary; mob inside a wall.

### Pitfall 6: A* with no node budget (tick stall)
**What goes wrong:** an unbounded A* over an unreachable target visits the whole region and blows the MSPT budget (TICK-06), stalling the tick.
**Why it happens:** vanilla bounds it with `maxVisitedNodes`; omitting it lets a bad request run forever.
**How to avoid:** port `PathFinder.maxVisitedNodes` (and the `searchDepthMultiplier`); cap the open-set expansions; return a partial/no path on exhaustion. `[VERIFIED: PathFinder.setMaxVisitedNodes / findPath(...,maxVisitedNodes,...)]`
**Warning signs:** MSPT spikes when a mob targets an unreachable spot.

## Code Examples

### Snapshot the path region from the tick-owned world (the seam input)
```go
// Source: ported from PathNavigationRegion(Level, BlockPos min, BlockPos max) — copies the
// block states in the box so computePath touches no live world. Built ON the tick.
func snapshotRegion(w *world.ChunkManager, e *Entity, tx, ty, tz int) *pathRegion {
    minX, minY, minZ := boxMin(e, tx, ty, tz) // mob pos & target ± followRange, clamped
    maxX, maxY, maxZ := boxMax(e, tx, ty, tz)
    r := newPathRegion(minX, minY, minZ, maxX, maxY, maxZ)
    for x := minX; x <= maxX; x++ {
        for y := minY; y <= maxY; y++ {
            for z := minZ; z <= maxZ; z++ {
                s, ok := w.GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinY) // tick-owned read
                r.set(x, y, z, ok && !block.IsAir(s))                      // solid? (copy)
            }
        }
    }
    return r // immutable from here — the Phase-8 worker consumes exactly this
}
```

### GoalSelector flag-lock tick (the arbitration core)
```go
// Source: ported from GoalSelector.tick (javap-verified). MOVE/LOOK/JUMP/TARGET as a bitset.
func (gs *goalSelector) tick(t *TickLoop, e *Entity) {
    for _, wg := range gs.goals { // priority order
        if wg.running && !wg.goal.canContinueToUse(t, e) {
            wg.goal.stop(t, e); wg.running = false; gs.locked &^= wg.flags // free flags
        }
    }
    for _, wg := range gs.goals {
        if !wg.running && gs.locked&wg.flags == 0 && wg.goal.canUse(t, e) {
            wg.running = true; gs.locked |= wg.flags; wg.goal.start(t, e) // lock flags
        }
    }
}
```

### Broadcast chat via SystemChat (CMD-02)
```go
// Source: javap ClientboundSystemChatPacket = Component content (NBT) + boolean overlay.
out := pk.Marshal(int32(packetid.ClientboundSystemChat),
    chat.Message{Text: "<" + name + "> " + text}, pk.Boolean(false))
for _, pl := range t.players { if pl.client != nil { pl.client.Send(out) } }
```

## State of the Art

| Old Approach (≤773 wiki / training) | Current Approach (776, jar-verified) | When Changed | Impact |
|-------------------------------------|--------------------------------------|--------------|--------|
| Chat = plain `PlayerChat` with loose fields | `PlayerChat` carries `globalIndex` (VarInt, first) + full SignedMessageBody/MessageSignature/ChatType$Bound chain | 1.19→1.21.x chat-session rework | v1 uses SystemChat to skip the chain; globalIndex documented. |
| `ChatType` sent as a registry id | `ChatType$Bound` = Holder<ChatType> + name Component + Optional<targetName> | 1.19+ | Only relevant if PlayerChat/DisguisedChat is later shipped. |
| Modern mob AI = goals | Split: classic `GoalSelector` (most mobs incl. Pig) vs `Brain`/`Behavior` memory system (villagers/piglins/warden) | 1.14+ (Brain) | v1 ports GoalSelector for the Pig; Brain deferred. |
| `Pig` at `world.entity.animal.Pig` | Moved to `world.entity.animal.pig.Pig` | recent package reshuffle | javap the new path `animal/pig/Pig.class` for its goal set. |
| Pathfinding reads the world | Reads a copied `PathNavigationRegion` snapshot | long-standing, but THE enabler for async | The seam is vanilla-native; porting it makes OPT-01 a swap, not a rewrite. |

**Deprecated/outdated:** the ≤773 wiki chat packet layout; any `animal.Pig` (now `animal.pig.Pig`); any "pathfinding reads Level directly" assumption.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | A v1 passive Pig uses the classic `GoalSelector`, not the `Brain` system | Alternatives / Java Sources | If a chosen v1 test mob is brain-based, the goal port wouldn't drive it — but the Pig is GoalSelector (verify by javap `Pig.registerGoals`). Low risk: Pig is the chosen mob. |
| A2 | SystemChat (Component + overlay) renders an acceptable chat line for v1, satisfying CMD-02's "broadcast" | Pattern 6 / Open Q2 | If the milestone demands player-attributed signed chat, v1 falls short — but CMD-02's literal ask is "received and broadcast (globalIndex prefix handled)", which SystemChat + documenting the layout satisfies. Confirm in discuss. |
| A3 | The fork `command.Graph` is complete enough to dispatch v1 literals + serialize `ClientboundCommands` | Pattern 5 | If `WriteTo`/parser has a 776 gap, CMD-01 needs a fix — but the package is present and self-consistent; capture-diff the graph bytes. |
| A4 | A minimal `NaturalSpawner` (CREATURE-only, relaxed light check) is "vanilla-style enough" for AI-03 v1 | Pattern 4 / Open Q3 | If the gate demands full multi-category density parity, scope grows — flag for discuss. The mandate is "vanilla-style rules", not full parity. |
| A5 | WALKABLE/BLOCKED/OPEN path types (deferring water/lava/fence/door malus) suffice for a v1 Pig on flat ground | Java Sources (WalkNodeEvaluator) | A water/fence-heavy world would mis-path; v1 superflat is flat stone, so low risk. Document the deferral. |
| A6 | `ServerboundChat` signature/lastSeen can be decoded-and-ignored (offline mode) without the client disconnecting | Pattern 6 / Pitfall 2 | If the client requires a `ServerboundChatAck`/session handshake before accepting SystemChat back, an ack may be needed — capture-diff a real chat round-trip. |

## Open Questions

1. **GoalSelector vs Brain for the v1 mob**
   - What we know: `GoalSelector`, `Goal`, all passive goals, AND the Brain system are all present in the jar; the Pig is a classic GoalSelector mob.
   - What's unclear: nothing blocking — but confirm the exact v1 goal SET (stroll + lookAtPlayer + lookAround, plus optional float/panic) by reading `Pig.registerGoals`.
   - Recommendation: port `GoalSelector` + the three core passive goals; javap `animal/pig/Pig.class` for the authoritative priorities. Defer Brain entirely.

2. **PlayerChat-signed vs SystemChat shortcut for CMD-02**
   - What we know: SystemChat is two fields (no signing); PlayerChat is 8 fields + the signature chain + a chat session the offline client doesn't establish; the globalIndex is the first PlayerChat field.
   - What's unclear: whether the phase gate accepts server-attributed SystemChat (`<name> msg`) as "broadcast", or demands player-signed PlayerChat.
   - Recommendation: **v1 = SystemChat broadcast + document the globalIndex layout** (satisfies "prefix handled"); defer signed PlayerChat to v2/ONLINE. Confirm in discuss-phase.

3. **How faithful must AI-03 natural spawning be for v1**
   - What we know: the full `NaturalSpawner` is multi-category with per-player distance caps, structure spawns, and a `LocalMobCapCalculator`; the per-chunk CREATURE attempt + cap is the core.
   - What's unclear: whether the gate wants the full density model or a faithful-minimal CREATURE spawner.
   - Recommendation: v1 = faithful-minimal (CREATURE cap + per-chunk ON_GROUND placement near players); document deferred refinements. Confirm scope in discuss-phase.

4. **Does a v1 mob need a SetEntityData metadata entry to render/animate?**
   - What we know: Phase 6 emits the 0xFF-terminated empty metadata list and the debug pig rendered; the tracker already sends it.
   - What's unclear: whether a walking pig needs a pose/flag metadata entry to animate its legs.
   - Recommendation: spawn with empty metadata first; if the pig slides instead of walking, capture-diff vanilla's Pig SetEntityData (reuse the Phase-6 entry machinery already in `entity_encode.go`).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | all build/test | ✓ | 1.26.1 | — |
| 26.2 inner jar (javap source) | Java-source porting + wire derivation | ✓ | `temp/cache/26.2-inner.jar` | — |
| `javap` (JDK 25) | reading the net.minecraft AI/chat/spawn sources | ✓ | Zulu 25.0.3 | — |
| Real vanilla 26.2 server | capture-diff (command graph, chat round-trip) | ✓ | `temp/vanilla-scratch/server.jar` | — |
| Docker (golang:1.26, CGO for `-race`) | race gate | ✓ | 29.x | host `CGO_ENABLED=0`; `-race` runs in Docker (Phase 2-6 precedent) |
| PrismLauncher 26.2 client | visual gate (mob walks/paths; chat shows; `/cmd` works) | ✓ (operator) | 26.2 | human-verify check (autonomous:false), as Phase 4/5/6 |

**Missing dependencies with no fallback:** None.
**Missing dependencies with fallback:** `-race` needs CGO → runs in golang:1.26 Docker (established Phase 2-6 pattern).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (no third-party) |
| Config file | none (standard `go test`) |
| Quick run command | `go test ./server/... ./server/command/... ./world/...` |
| Full suite command | `docker run --rm -v "$PWD":/src -w /src golang:1.26 go test -race ./server/... ./server/command/... ./world/...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| AI-01 | GoalSelector flag-lock: higher-priority goal preempts; goals start/stop correctly | unit | `go test ./server/ -run TestGoalSelector` | ❌ Wave 0 (`server/ai_goal_test.go`) |
| AI-02 | `computePath` finds a path on flat ground; respects `maxVisitedNodes`; returns no-path for unreachable | unit | `go test ./server/ -run TestPathfinder` | ❌ Wave 0 (`server/pathfinder_test.go`) |
| AI-02 | `computePath` is PURE (no tick-owned state) — runnable from a snapshot alone | unit | `go test ./server/ -run TestPathRequestPure` | ❌ Wave 0 |
| AI-02 | Mob steps along its path via `moveEntity`, lands, re-buckets (tracker still sees it) | unit | `go test ./server/ -run TestNavigationFollow` | ❌ Wave 0 |
| AI-03 | Spawner respects CREATURE cap; places only on valid air-over-solid blocks | unit | `go test ./server/ -run TestSpawner` | ❌ Wave 0 (`server/spawner_test.go`) |
| CMD-01 | `command.Graph` dispatches a registered literal; serializes `ClientboundCommands` | unit | `go test ./server/ ./server/command/ -run TestCommand` | partial (`command_test.go` exists) |
| CMD-02 | `ServerboundChat` decodes (msg/ts/salt); `SystemChat` broadcast encodes; fan-out hits all players | unit | `go test ./server/ -run TestChat` | ❌ Wave 0 (`server/chat_test.go`) |
| CMD-01/02 | `ClientboundCommands` + `SystemChat`/`ServerboundChat` bytes match vanilla | capture-diff | `go test ./server/ -run TestChatCommandBytesVsVanillaCapture` | ❌ Wave 0 |
| AI-01..03, CMD-01/02 | Real client: a mob walks/paths around terrain; chat shows; a `/command` runs | manual | human-verify (PrismLauncher), autonomous:false | gate |

### Sampling Rate
- **Per task commit:** `go test ./server/... ./server/command/... ./world/...` (host)
- **Per wave merge:** full `-race` suite in golang:1.26 Docker
- **Phase gate:** full suite green + capture-diff fixtures (`07-CAPTURE-DIFF.md`: `ClientboundCommands`, `ServerboundChat`/`SystemChat`) committed + a BLOCKING real-client check (a ported-AI mob visibly walks/navigates; chat broadcasts; a command runs) — like Phases 4/5/6.

### Wave 0 Gaps
- [ ] `server/ai_goal_test.go` — GoalSelector flag-lock + priority preemption (AI-01)
- [ ] `server/pathfinder_test.go` — A* correctness, node budget, purity of `computePath` (AI-02)
- [ ] `server/navigation_test.go` — path-following via `moveEntity` + re-bucket (AI-02)
- [ ] `server/spawner_test.go` — cap accounting + valid placement (AI-03)
- [ ] `server/chat_test.go` — `ServerboundChat` decode + `SystemChat` broadcast fan-out (CMD-02)
- [ ] `server/commands_test.go` — graph send at join + `ServerboundChatCommand` route → `Execute` (CMD-01)
- [ ] Capture-diff fixtures: `07-CAPTURE-DIFF.md` for `ClientboundCommands`, `SystemChat`, `ServerboundChat` round-trip

## Security Domain

> `security_enforcement` not set to false in config — section included. The "user" is an unmodified vanilla client, but the server must stay robust against a MALICIOUS/malformed client (the standing threat model: net goroutines never touch game state; all serverbound decode is Scan-error-to-no-op).

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Offline mode for v1 (auth is v2/Phase 9). |
| V3 Session Management | partial | One writer goroutine / channel boundary already enforced (NET-05); Phase 7 adds no new session surface. |
| V4 Access Control | yes | Commands must gate on permission/role before mutating state; the server is authoritative for AI/spawning (client cannot spawn mobs or move them). |
| V5 Input Validation | yes | Every new serverbound decode (`ServerboundChat`, `ServerboundChatCommand`) must `Scan`-error to a no-op, never panic; chat length capped (`readUtf(256)`); command string bounded before `Execute`. |
| V6 Cryptography | no | None at this layer (chat signing is intentionally skipped via SystemChat; encryption is v2/Phase 9). |

### Known Threat Patterns for Sulfur (Go MC server, malicious client)
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Oversized / malformed chat (huge string, bad signature) | DoS | `readUtf(256)` bounds the message; decode-and-ignore signature; Scan-error → no-op (never panic). |
| Command injection / privileged command from an unprivileged client | Elevation of Privilege | `Graph.Execute` runs server-side; gate command nodes on permission before mutating state. |
| Path-request flood (mob targeting unreachable spots every tick) | DoS | `maxVisitedNodes` budget on A*; throttle recompute (`shouldRecomputePath`/`timeLastRecompute`). |
| Spawn flood via crafted positions | DoS | Spawning is SERVER-driven (client cannot trigger); cap accounting bounds it. |
| Chat spam flood | DoS | Rate-limit per player (vanilla uses a chat-spam counter); v1 may bound via the bounded outbound queue + an optional per-player cooldown. |
| Malformed `ServerboundChatCommand` payload | DoS | `Scan`-error → no-op; bound the string length before `Execute`. |

## Sources

### Primary (HIGH confidence)
- On-disk fork code (read this session): `server/tick.go`, `server/tick_phases.go`, `server/physics.go`, `server/entity.go`, `server/entity_store.go`, `server/tracker.go`, `server/entity_encode.go`, `server/debug.go`, `server/gameplay.go`, `server/gameplay_tick.go`, `server/command/*.go` (command.go, builders.go, parsers.go, serialize.go, component.go), `world/manager.go`, `chat/message.go`+`nbtmessage.go`, `data/entity/entity.go`, `data/packetid/*.go`.
- Jar bytecode (`javap -p [-c]`, `temp/cache/26.2-inner.jar`, unobfuscated 26.2, this session): `GoalSelector` (addGoal/tick/tickRunningGoals/lockedFlags), `Goal` + `Goal$Flag` (MOVE/LOOK/JUMP/TARGET), `WrappedGoal`, the passive goals (RandomStroll/WaterAvoidingRandomStroll/LookAtPlayer/RandomLookAround/Float/Panic), `Mob.serverAiStep` (sensing→targetSelector→goalSelector.tick→tickRunningGoals), `PathFinder` (findPath signature, BinaryHeap, distance/getBestH/reconstructPath), `Node`, `BinaryHeap`, `WalkNodeEvaluator` (prepare/getNeighbors/getPathType/getFloorLevel/isNeighborValid/isDiagonalValid), `PathNavigationRegion` (copied ChunkAccess[][] snapshot ctor), `PathNavigation`/`GroundPathNavigation` (createPath/moveTo/tick/isDone), `NaturalSpawner` (createState/getFilteredSpawningCategories/spawnForChunk/isValidEmptySpawnBlock), `MobCategory` (enum + getMaxInstancesPerChunk), `SpawnPlacements`, `ServerboundChatPacket` (readUtf256/readInstant/readLong/readNullable(sig)/LastSeenMessages$Update), `ServerboundChatCommandPacket` (readUtf String), `ClientboundSystemChatPacket` (content+overlay; composite codec), `ClientboundPlayerChatPacket` (write: globalIndex/sender/index/sig/body/unsignedContent/filterMask/chatType), `ClientboundDisguisedChatPacket` (message+chatType), `ChatType$Bound` (Holder+name+Optional), `SignedMessageBody$Packed`, jar path `animal/pig/Pig.class`.
- Prior phase artifacts: `06-RESEARCH.md`, `06-CAPTURE-DIFF.md` (the proven capture-diff method + the sealed entity encoders this phase reuses).

### Secondary (MEDIUM confidence)
- Training knowledge of vanilla AI/path/spawn ALGORITHM shape (goal arbitration, A* g/h costs, malus table, spawn cap math) — corroborated by the javap-verified class signatures above; the exact constants/predicates must be read from the jar during porting.

### Tertiary (LOW confidence)
- None relied upon. The ≤773 community wiki is explicitly NOT a source for 776 chat/command wire (STATE.md standing blocker).

## Metadata

**Confidence breakdown:**
- Reused fork seams (tickAI stub, entityStore/near, moveEntity, GetBlock, tracker, command graph, chat.Message): HIGH — every file/symbol read on disk this session.
- Java port targets (exact classes + tick order + A* snapshot seam): HIGH — every named class javap-verified present; `serverAiStep` order, `PathFinder.findPath` signature, and `PathNavigationRegion` snapshot confirmed from bytecode.
- Chat wire (SystemChat layout, ServerboundChat decode, PlayerChat globalIndex): HIGH (jar-derived this session); the round-trip is MEDIUM until the capture-diff gate (the project's standing seal method) confirms it byte-identical.
- Command graph (CMD-01): HIGH that the fork graph exists and serializes `ClientboundCommands`; MEDIUM that no 776 gap exists until the capture-diff.
- AI/spawn behavior parity (the ported logic): MEDIUM by nature — faithfulness is gated by the real-client visual check, not a byte-diff (no wire for goal/A* logic).

**Research date:** 2026-06-24
**Valid until:** 30 days (the fork is pinned; only a 26.x bump or a deeper jar finding would invalidate). The MEDIUM wire items (chat/command bytes) should be sealed by capture-diff during execution, not re-researched.
