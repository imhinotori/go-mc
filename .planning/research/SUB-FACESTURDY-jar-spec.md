# SUB-FACESTURDY — Block Face Sturdiness / Support Shapes (26.2 jar spec)

**Scope:** the `isFaceSturdy` / `canSupportCenter` predicate chain that decides whether a block
supports a torch / rail / sign / button / lever / repeater / redstone on a given face.

**Source:** `temp/cache/26.2-inner.jar` (unobfuscated), decompiled this session with
`javap -c -p` (Zulu 25). Every method below is cited with the exact bytecode it was read from.

**TL;DR — the critical question answered up front.** `isFaceSturdy` is a **precomputed
per-block-state lookup**: each `BlockState` builds, at registration, an `18-element boolean[]`
(`6 directions × 3 support types`) in `BlockBehaviour$BlockStateBase$Cache`, computed once with
`EmptyBlockGetter.INSTANCE` + `BlockPos.ZERO` (no world context). The runtime `isFaceSturdy`
call is a pure array read. Therefore the **faithful, lowest-risk Go implementation is a data table
`stateID → [6][3]bool` extracted from the jar by a reflection extractor** (exactly the
`GenBlockHardness.java` pattern already in `tools/java/`). We do **not** need to port the VoxelShape
boolean engine to get correct face-sturdiness. See **RECOMMENDED APPROACH** at the bottom.

---

## 0. Call graph (who calls what)

```
attachment block .canSurvive(state, level, pos)
  ├─ DiodeBlock        → canSurviveOn → BlockState.isFaceSturdy(.., UP,   RIGID)
  ├─ BaseRailBlock     → Block.canSupportRigidBlock(below) → isFaceSturdy(.., UP, RIGID)
  ├─ Button/Lever      → FaceAttachedHorizontalDirectionalBlock.canAttach
  │                        → BlockState.isFaceSturdy(neighbor, .., oppositeDir)      [FULL]
  ├─ WallTorch/WallSign→ isFaceSturdy(neighbor, .., facing)                          [FULL]
  ├─ BaseTorchBlock    → Block.canSupportCenter(below, UP) → isFaceSturdy(.., UP, CENTER)
  ├─ CeilingHangingSign→ isFaceSturdy(above, .., DOWN, CENTER)
  ├─ StandingSignBlock → BlockState.isSolid()           (NOT isFaceSturdy — see §6)
  ├─ RedStoneWireBlock → canSurviveOn → isFaceSturdy(below, .., UP) [FULL] || is(HOPPER)
  └─ VegetationBlock   → tag SUPPORTS_VEGETATION (already ported; unrelated to this chain)

BlockState.isFaceSturdy(getter,pos,dir)            = isFaceSturdy(getter,pos,dir, FULL)
BlockState.isFaceSturdy(getter,pos,dir,supportType)= cache.faceSturdy[dir.ordinal*3 + supportType.ordinal]
   (if cache==null → supportType.isSupporting(state,getter,pos,dir))   // dynamic-shape fallback

SupportType.isSupporting(state,getter,pos,dir):
  FULL   = Block.isFaceFull( state.getBlockSupportShape(getter,pos), dir )
  CENTER = !Shapes.joinIsNotEmpty( supportShape.getFaceShape(dir), column(2,0,10),  ONLY_SECOND )
  RIGID  = !Shapes.joinIsNotEmpty( supportShape.getFaceShape(dir), RIGID_SUPPORT_SHAPE, ONLY_SECOND )

state.getBlockSupportShape = state.getCollisionShape (default) = hasCollision ? getShape : EMPTY
```

---

## 1. `BlockState.isFaceSturdy` (the two overloads)

**Class:** `net.minecraft.world.level.block.state.BlockBehaviour$BlockStateBase`

### 1a. `isFaceSturdy(BlockGetter, BlockPos, Direction)` — 3-arg, delegates with FULL

```
public boolean isFaceSturdy(BlockGetter, BlockPos, Direction);
   0: aload_0 1: aload_1 2: aload_2 3: aload_3
   4: getstatic  Field SupportType.FULL
   7: invokevirtual isFaceSturdy:(BlockGetter;BlockPos;Direction;SupportType;)Z
  10: ireturn
```

Pseudo-Go (faithful):
```go
func (st *BlockState) IsFaceSturdy(g BlockGetter, pos BlockPos, dir Direction) bool {
    return st.IsFaceSturdyT(g, pos, dir, SupportFull)   // 3-arg overload == FULL
}
```

### 1b. `isFaceSturdy(BlockGetter, BlockPos, Direction, SupportType)` — the real one

```
public boolean isFaceSturdy(BlockGetter, BlockPos, Direction, SupportType);
   0: aload_0  1: getfield cache
   4: ifnull  18                              // cache == null → compute live
   7: aload_0  8: getfield cache
  11: aload_3 (dir) 12: aload 4 (supportType)
  14: invokevirtual Cache.isFaceSturdy:(Direction;SupportType;)Z
  17: ireturn
  18: aload 4 (supportType)
  20: aload_0 21: asState()
  24: aload_1 (getter) 25: aload_2 (pos) 26: aload_3 (dir)
  27: invokevirtual SupportType.isSupporting:(BlockState;BlockGetter;BlockPos;Direction;)Z
  30: ireturn
```

**Key fact:** when the per-state `Cache` exists (it does for every static-shape state), this is a
**table lookup**. The `cache == null` branch (live recompute via `SupportType.isSupporting`) is only
taken for **dynamic-shape blocks** — see §5 (which blocks have `cache == null`).

Pseudo-Go (faithful):
```go
func (st *BlockState) IsFaceSturdyT(g BlockGetter, pos BlockPos, dir Direction, t SupportType) bool {
    if st.cache != nil {                       // the common case for every static-shape state
        return st.cache.faceSturdy[int(dir)*supportTypeCount+int(t)]  // pure array read
    }
    return t.isSupporting(st, g, pos, dir)     // dynamic-shape live recompute
}
```

---

## 2. `SupportType` enum + the three `isSupporting` predicates

**Class:** `net.minecraft.world.level.block.SupportType` (abstract enum; bodies in `$1/$2/$3`).
Ordinals: **FULL=0, CENTER=1, RIGID=2** (from `$values()` order). `supportTypeCount = 3`.

### 2a. FULL (`SupportType$1.isSupporting`)

```
   0: aload_1 1: aload_2 2: aload_3
   3: invokevirtual BlockState.getBlockSupportShape:(BlockGetter;BlockPos;)VoxelShape
   6: aload 4 (dir)
   8: invokestatic  Block.isFaceFull:(VoxelShape;Direction;)Z
  11: ireturn
```
`FULL == Block.isFaceFull( state.getBlockSupportShape(getter,pos), dir )`.
(The face of the support shape, in direction `dir`, is a **full 1×1 unit square**.)

### 2b. CENTER (`SupportType$2.isSupporting`)

Constructor field:
```
CENTER_SUPPORT_SHAPE = Block.column(2.0, 0.0, 10.0)
```
`Block.column(width=2, ymin=0, ymax=10)` → a centered 2×2-pixel column from y=0 to y=10 px
(see §3d: `box(7,0,7, 9,10,9)/16` → `[0.4375,0,0.4375]..[0.5625,0.625,0.5625]`).

```
   0..3: state.getBlockSupportShape(getter,pos)         → supportShape
   6,8 : supportShape.getFaceShape(dir)                 → faceShape
  11,12: getfield CENTER_SUPPORT_SHAPE
  15   : getstatic BooleanOp.ONLY_SECOND
  18   : invokestatic Shapes.joinIsNotEmpty(faceShape, CENTER_SUPPORT_SHAPE, ONLY_SECOND)
  21   : ifne 28 → push 0 ; else push 1                 // result = !joinIsNotEmpty
  29   : ireturn
```
`CENTER == !Shapes.joinIsNotEmpty( faceShape, CENTER_SUPPORT_SHAPE, ONLY_SECOND )`.

`ONLY_SECOND(a,b) = !a && b`. `joinIsNotEmpty(faceShape, C, ONLY_SECOND)` is true iff some voxel of
`C` is **not** covered by `faceShape`. Negated: **CENTER is supported iff the support-shape's face
fully covers the central 2×2 column** (i.e. nothing of `C` pokes out uncovered).

### 2c. RIGID (`SupportType$3.isSupporting`)

Constructor field:
```
RIGID_SUPPORT_SHAPE = Shapes.join( Shapes.block(), Block.column(12.0,0.0,16.0), ONLY_FIRST )
```
`ONLY_FIRST(a,b)=a && !b`. So `RIGID_SUPPORT_SHAPE` = the **full block MINUS a centered 12×12-pixel
column** running the full height = a **frame/ring**: the full block with the central
`[2,16]×[*]×[2,16]` (px) column carved out. (`column(12,0,16)` → `box(2,0,2,14,16,14)/16`.)

```
   0..3: state.getBlockSupportShape(getter,pos)
   6,8 : .getFaceShape(dir)
  11,12: getfield RIGID_SUPPORT_SHAPE
  15   : getstatic BooleanOp.ONLY_SECOND
  18   : invokestatic Shapes.joinIsNotEmpty(faceShape, RIGID_SUPPORT_SHAPE, ONLY_SECOND)
  21   : ifne 28 → 0 ; else 1                            // result = !joinIsNotEmpty
  29   : ireturn
```
`RIGID == !Shapes.joinIsNotEmpty( faceShape, RIGID_SUPPORT_SHAPE, ONLY_SECOND )`.
**RIGID is supported iff the face fully covers the outer frame** (the central column is allowed to be
missing). Note RIGID is a *weaker* requirement than FULL on the rim but requires the whole frame.

> Observable consequence (for sanity-checking a port): a full cube satisfies all three. A top slab's
> UP face satisfies all three; its DOWN face satisfies none. A hopper's UP face is **not** FULL but
> redstone still survives on it via the explicit `is(HOPPER)` special-case in §6e.

---

## 3. The `Block` / `Shapes` primitives

### 3a. `Block.isFaceFull(VoxelShape, Direction)`

```
public static boolean isFaceFull(VoxelShape, Direction);
   0: aload_0 1: aload_1 2: invokevirtual VoxelShape.getFaceShape(dir) → faceShape
   6: invokestatic Block.isShapeFullBlock(faceShape)
  10: ireturn
```
`isFaceFull(shape,dir) = isShapeFullBlock( shape.getFaceShape(dir) )`.

### 3b. `Block.isShapeFullBlock(VoxelShape)`

```
   0: getstatic SHAPE_FULL_BLOCK_CACHE (LoadingCache)
   3: getUnchecked(shape) → Boolean ; booleanValue ; ireturn
```
Memoized predicate: "does this shape == the full unit cube". For a *face* shape (a 2-D projection
onto the unit square at the face plane) it means "the face is a solid 1×1 square". (Underlying
loader: `Shapes.faceShapeOccludes`-style full-coverage test on the 2-D face. For the Go port the
operational definition is: **the projected face shape covers the entire `[0,1]×[0,1]` unit square**.)

### 3c. `Shapes.joinIsNotEmpty(VoxelShape a, VoxelShape b, BooleanOp op)`

```
   0: op.apply(false,false) → if true throw IAE        // guard: op(0,0) must be false
  25: a.isEmpty()  → aEmpty
  30: b.isEmpty()  → bEmpty
  36: if (aEmpty && !... )  fast path …                // short-circuits on empties
  ...
  65: op.apply( !aEmpty , !bEmpty ) ... (then the discrete-voxel merge) → boolean
```
Operationally: returns **true iff there exists a cell where `op(inA, inB)` is true**. For
`ONLY_SECOND = (!a && b)`: true iff some voxel is in `b` but not in `a`. For `ONLY_FIRST = (a && !b)`:
true iff some voxel is in `a` but not in `b`.

### 3d. `Shapes.faceShapeOccludes(VoxelShape a, VoxelShape b)` (documented; NOT on the sturdy path)

```
public static boolean faceShapeOccludes(VoxelShape a, VoxelShape b);
   0: if (a == Shapes.block() || b == Shapes.block()) return true
  16: if (a.isEmpty() && b.isEmpty()) return false
  32: return !Shapes.joinIsNotEmpty( Shapes.block(),
                                      joinUnoptimized(a, b, OR),
                                      ONLY_FIRST )
```
i.e. "the union of the two faces leaves no part of the full block uncovered". This is the
**light/occlusion** path (`skipRendering` / neighbor occlusion), **not** used by `isFaceSturdy`.
Included so a future occlusion port can reuse the same `joinIsNotEmpty` engine.

### 3e. `Block.column` / `Block.box` (pixel→block geometry)

```
column(w,ymin,ymax)        = column(w, w, ymin, ymax)
column(wx,wz,ymin,ymax):
    hx = wx/2 ; hz = wz/2
    return box( 8-hx, ymin, 8-hz,  8+hx, ymax, 8+hz )      // pixel coords, centered on 8
box(x1,y1,z1,x2,y2,z2):
    return Shapes.box( x1/16, y1/16, z1/16, x2/16, y2/16, z2/16 )   // px → block units
```
So `column(2,0,10)`  = `box(7,0,7, 9,10,9)`  = `[0.4375,0,0.4375]..[0.5625,0.625,0.5625]`.
`column(12,0,16)` = `box(2,0,2, 14,16,14)` = `[0.125,0,0.125]..[0.875,1,0.875]`.

---

## 4. Where the support shape comes from — `getBlockSupportShape / getCollisionShape / getShape`

**Class:** `net.minecraft.world.level.block.state.BlockBehaviour` (base) + `BlockStateBase` (dispatch).

```
BlockStateBase.getBlockSupportShape(g,pos) → Block.getBlockSupportShape(state,g,pos)
BlockBehaviour.getBlockSupportShape(state,g,pos):
   0..4: this.getCollisionShape(state,g,pos, CollisionContext.empty())
  10: areturn                                            // default: support == collision

BlockBehaviour.getCollisionShape(state,g,pos,ctx):
   0: getfield hasCollision
   4: ifeq 16
   7..: state.getShape(g,pos)        // hasCollision → the block's collision/outline shape
  16: Shapes.empty()                 // !hasCollision → EMPTY (e.g. non-colliding decoration)
  19: areturn

BlockBehaviour.getShape(state,g,pos,ctx):                // BASE implementation
   0: invokestatic Shapes.block()   ; areturn            // default shape == full unit cube
```

So for the **default** block (no `getShape` / `getCollisionShape` override, `hasCollision=true`):
support shape = `Shapes.block()` (full cube) → every face is FULL/CENTER/RIGID sturdy.

For **per-block overrides** (slabs, stairs, fences, walls, snow layers, soul-sand, farmland, paths,
chains, etc.) the block class overrides `getShape`/`getCollisionShape` to return a static `VoxelShape`
constant (almost always a `protected static final VoxelShape SHAPE = Block.box(...)` or a
state-indexed array). These are computed **once at class load**, do not read the world, and the
result is folded into the per-state `Cache` (next section). A small set of blocks read state/world
(`hasCollision=false` decorations return EMPTY; truly dynamic blocks have `cache == null`).

---

## 5. THE DATA-VS-CODE PIVOT — `BlockStateBase$Cache`

**Class:** `net.minecraft.world.level.block.state.BlockBehaviour$BlockStateBase$Cache`
Fields: `VoxelShape collisionShape`, `boolean largeCollisionShape`, `boolean[] faceSturdy`,
`boolean isCollisionShapeFullBlock`.

### Cache constructor (decompiled) — this is the whole answer

```
Cache(BlockState state):
  collisionShape = block.getCollisionShape(state, EmptyBlockGetter.INSTANCE, BlockPos.ZERO,
                                           CollisionContext.empty())
  // (throws if it has a collision shape AND an offset type but isn't dynamicShape)
  largeCollisionShape = any axis where collisionShape.min(axis) < 0 || collisionShape.max(axis) > 1
  faceSturdy = new boolean[ DIRECTIONS.length (6) * SUPPORT_TYPE_COUNT (3) ]   // == 18
  for (Direction d : Direction.values())                    // 6
    for (SupportType t : SupportType.values())              // 3  (FULL,CENTER,RIGID)
      faceSturdy[ getFaceSupportIndex(d,t) ] =
          t.isSupporting(state, EmptyBlockGetter.INSTANCE, BlockPos.ZERO, d)
  isCollisionShapeFullBlock =
      Block.isShapeFullBlock( state.getCollisionShape(EmptyBlockGetter.INSTANCE, BlockPos.ZERO) )

getFaceSupportIndex(d,t) = d.ordinal()*SUPPORT_TYPE_COUNT + t.ordinal()
Cache.isFaceSturdy(d,t)  = faceSturdy[ getFaceSupportIndex(d,t) ]
```

**Decisive observations:**

1. `faceSturdy[]` is computed with **`EmptyBlockGetter.INSTANCE` + `BlockPos.ZERO`** — *no world
   context*. So `isFaceSturdy` is a **pure function of the block state**, byte-for-byte. (The world
   arguments threaded through `isFaceSturdy(getter,pos,...)` are *unused* whenever the cache exists,
   which is every static-shape state.)
2. The result is exactly `18 booleans per state` = `[6 dir][3 supportType]bool`.
3. `isCollisionShapeFullBlock` and `legacySolid`/`isSolid` (§6c) fall out of the **same** Cache —
   so one extractor pass yields face-sturdiness **and** the suffocation/`isSolid` data Sulfur also
   needs (closing the `suffocation.go` and `StandingSignBlock` gaps in the same stroke).

### When is `cache == null` (the live-recompute branch)?

The `Cache` is built for every state **except** those whose block is flagged `dynamicShape` in its
`Properties` (the constructor guards against a static collision shape coexisting with an offset
function unless `dynamicShape`). Blocks that are genuinely dynamic-shape are rare and their
support-face behavior is environment-independent for our attachment purposes — the vast majority of
the registry (and **all** the blocks that matter as torch/rail/redstone supports: stone, dirt,
logs, slabs, stairs, hoppers, etc.) have a non-null cache and a fixed 18-bool vector. A few examples
of dynamic / offset blocks (flowers, grass with `OffsetType`) are never valid supports anyway
(EMPTY support shape → all-false vector). **Net: a static `stateID→[18]bool` table is faithful for
every block a real attachment can ever sit on.**

---

## 6. Attachment-block `canSurvive` (each cited)

All in `net.minecraft.world.level.block`. `pos` = the attachment block's own position.

### 6a. `DiodeBlock.canSurvive` (repeater + comparator) — UP / RIGID
```
canSurvive → canSurviveOn(level, below, level.getBlockState(below))
canSurviveOn(level,pos,state) = state.isFaceSturdy(level, pos, Direction.UP, SupportType.RIGID)
```
`below.isFaceSturdy(UP, RIGID)`.

### 6b. `BaseRailBlock.canSurvive` — UP / RIGID
```
canSurvive = Block.canSupportRigidBlock(level, pos.below())
Block.canSupportRigidBlock(g,pos) = g.getBlockState(pos).isFaceSturdy(g, pos, UP, RIGID)
```
`below.isFaceSturdy(UP, RIGID)`. (Same predicate as repeater.)

### 6c. `StandingSignBlock.canSurvive` — `isSolid()` (NOT isFaceSturdy)
```
canSurvive = level.getBlockState(pos.below()).isSolid()
```
`BlockState.isSolid()` returns the cached `legacySolid` field:
```
calculateSolid():
  if (Properties.forceSolidOn)  return true
  if (Properties.forceSolidOff) return false
  if (cache == null) return false
  shape = cache.collisionShape
  if (shape.isEmpty()) return false
  AABB b = shape.bounds()
  if (b.getSize()  >= 0.7291666666666666) return true     // 0.7291666… == 35/48
  if (b.getYsize() >= 1.0)                return true
  return false
```
So `isSolid` is *also* a precomputable per-state boolean (forceSolidOn/Off are `Properties` flags;
the rest is a function of the cached collision-shape bounds). **Extract it alongside faceSturdy.**

### 6d. `ButtonBlock` / `LeverBlock` — via `FaceAttachedHorizontalDirectionalBlock.canAttach` — FULL
Both extend `FaceAttachedHorizontalDirectionalBlock`. No own `canSurvive`.
```
FaceAttachedHorizontalDirectionalBlock.canSurvive:
  = canAttach(level, pos, getConnectedDirection(state).getOpposite())
canAttach(level, pos, dir):
  BlockPos rel = pos.relative(dir)
  return level.getBlockState(rel).isFaceSturdy(level, rel, dir.getOpposite())   // 3-arg → FULL
```
`getConnectedDirection(state)` = the direction the block points *away from* its support
(FACE=FLOOR→UP, CEILING→DOWN, WALL→its horizontal FACING). The neighbor in `getConnectedDirection`
must have a **FULL** face toward the attachment. So a wall button on the north wall:
neighbor-to-north, `isFaceSturdy(SOUTH, FULL)`.

### 6e. `RedStoneWireBlock.canSurvive` — UP / FULL, OR hopper
```
canSurvive:
  below = pos.below() ; belowState = level.getBlockState(below)
  return canSurviveOn(level, below, belowState)
canSurviveOn(g,pos,state):
  return state.isFaceSturdy(g, pos, Direction.UP)     // 3-arg → FULL
      || state.is(Blocks.HOPPER)
```
`below.isFaceSturdy(UP, FULL) || below is HOPPER`. (Hopper UP face is not FULL, hence the explicit OR.)

### 6f. `StandingSignBlock` vs `WallSignBlock` vs `CeilingHangingSignBlock`
- `StandingSignBlock.canSurvive` = `below.isSolid()` (see §6c).
- `WallSignBlock.canSurvive` = `state.relative(FACING.getOpposite())` neighbor `.isSolid()`
  (NOT isFaceSturdy — wall signs use `isSolid`, same as standing signs).
- `CeilingHangingSignBlock.canSurvive` = `above.isFaceSturdy(above, DOWN, CENTER)`.

### 6g. Torches
- `BaseTorchBlock.canSurvive` (standing torch) = `Block.canSupportCenter(level, pos.below(), UP)`:
  ```
  canSupportCenter(level,pos,dir):
    state = level.getBlockState(pos)
    if (dir == DOWN && state.is(BlockTags.UNSTABLE_BOTTOM_CENTER)) return false   // e.g. scaffolding
    return state.isFaceSturdy(level, pos, dir, SupportType.CENTER)
  ```
  → `below.isFaceSturdy(UP, CENTER)` (the `UNSTABLE_BOTTOM_CENTER` guard only fires for `dir==DOWN`).
- `WallTorchBlock.canSurvive` = `WallTorchBlock.canSurvive(level, pos, FACING)`:
  ```
  static canSurvive(level,pos,facing):
    rel = pos.relative(facing.getOpposite())
    return level.getBlockState(rel).isFaceSturdy(level, rel, facing)   // 3-arg → FULL
  ```
  → neighbor behind the torch, `isFaceSturdy(facing, FULL)`. (You already have this.)

### Support-type summary table

| Block (canSurvive)            | Neighbor          | Direction (face queried)        | SupportType | Extra        |
|-------------------------------|-------------------|---------------------------------|-------------|--------------|
| DiodeBlock (repeater/comp.)   | below             | UP                              | RIGID       | —            |
| BaseRailBlock (all rails)     | below             | UP                              | RIGID       | —            |
| RedStoneWireBlock             | below             | UP                              | FULL        | `\|\| HOPPER`|
| Button / Lever                | connectedDir nbr  | connectedDir (toward support)   | FULL        | —            |
| WallTorch                     | behind (facing⁻¹) | facing                          | FULL        | —            |
| BaseTorch (standing)          | below             | UP                              | CENTER      | UNSTABLE_BC  |
| CeilingHangingSign            | above             | DOWN                            | CENTER      | —            |
| StandingSign                  | below             | (isSolid)                       | —           | `isSolid()`  |
| WallSign                      | facing⁻¹ nbr      | (isSolid)                       | —           | `isSolid()`  |

---

## 7. RECOMMENDED APPROACH — data table (extract, don't port the VoxelShape engine)

### The decision: **codegen a per-state data table from the jar.** HIGH confidence.

Three options were on the table:

| Option | Fidelity | Effort | Verdict |
|--------|----------|--------|---------|
| **(A) Reflection extractor → `stateID→[6][3]bool` table** (the `GenBlockHardness.java` pattern) | **1:1 exact** — the bytes are literally `cache.faceSturdy[]` copied out of the runtime | low — one Java extractor + one Go generator, mirrors existing `gen_block_hardness.go` | ✅ **ADOPT** |
| (B) Hand-port the VoxelShape boolean engine (`Shapes.joinIsNotEmpty`, `DiscreteVoxelShape`, `IndexMerger`, `getFaceShape`, `isShapeFullBlock`) + every block's `getShape` override | 1:1 only if you also port ~hundreds of per-block shape constants perfectly — high drift risk | very high — a whole physics-shape subsystem, most of it unused by Sulfur today | ❌ reject (premature; this is the *collision* engine, a separate future phase) |
| (C) Hand-written `[6][3]bool` table | not 1:1 — 29 671 states × 18 bits transcribed by hand is infeasible and unverifiable | absurd | ❌ reject |

### Why (A) is provably 1:1

The runtime value Minecraft uses **is** `cache.faceSturdy[d*3+t]`, computed once with
`EmptyBlockGetter` + `BlockPos.ZERO` — i.e. with *no inputs other than the block state*. An extractor
that calls `state.isFaceSturdy(EmptyBlockGetter.INSTANCE, BlockPos.ZERO, d, t)` for every registered
state reads back the **identical bits the game uses at runtime**. There is no float-cast, RNG, or
world-context subtlety to drift on — the jar does the VoxelShape math for us and we copy the boolean
output. This is strictly more faithful than porting the shape engine (which would re-derive the same
booleans through code we'd have to keep bug-for-bug identical).

### Extractor spec (new `tools/java/GenBlockSupport.java`, mirrors GenBlockHardness)

Iterate **every BlockState** (not just `defaultBlockState()` — slabs/stairs/snow differ by state):

```java
for (Block block : BuiltInRegistries.BLOCK)
  for (BlockState state : block.getStateDefinition().getPossibleStates()) {
    int sturdy = 0;                                  // pack 18 bits
    for (Direction d : Direction.values())           // ordinal 0..5  D,U,N,S,W,E
      for (SupportType t : SupportType.values())     // ordinal 0..2  FULL,CENTER,RIGID
        if (state.isFaceSturdy(EmptyBlockGetter.INSTANCE, BlockPos.ZERO, d, t))
          sturdy |= 1 << (d.ordinal()*3 + t.ordinal());
    boolean solid = state.isSolid();                              // §6c — for signs + suffocation
    boolean collFull = state.isCollisionShapeFullBlock(EmptyBlockGetter.INSTANCE, BlockPos.ZERO);
    emit(globalStateId(state), sturdy, solid, collFull);
  }
```

Key the rows by the **global palette state id** (the same index Sulfur's `block.StateList` /
`block_states.nbt` uses — verify the iteration order matches `gen_blocks.go`'s state numbering; if
the registry order differs, key by `(blockName, sortedProperties)` and resolve to `StateID` in the
Go generator, exactly as `gen_block_hardness.go` keys by resource id). Emit a compact JSON
(`{"id":N,"sturdy":<int 0..2^18>,"solid":bool,"coll_full":bool}`); the Go generator
(`gen_block_support.go`) writes a `level/block/support.go` with:

```go
// faceSturdy[stateID] packs [dir*3+supportType] → bit. Generated; do not edit.
var faceSturdyBits []uint32           // len == len(StateList)
var legacySolidBits []uint64          // bitset
var collisionFullBits []uint64        // bitset (closes suffocation.go's isCollisionShapeFullBlock gap)

func IsFaceSturdy(id StateID, dir Direction, t SupportType) bool {
    return faceSturdyBits[id]&(1<<(uint(dir)*3+uint(t))) != 0
}
func IsSolid(id StateID) bool { ... }                  // StandingSign/WallSign
func IsCollisionShapeFullBlock(id StateID) bool { ... } // suffocation.go real read
```

### What this unlocks / replaces in current Sulfur code

- **`world/feature_misc.go::faceSturdyUp`** (currently "not air, not fluid" proxy, §`feature_misc.go:458`)
  → replace body with `block.IsFaceSturdy(st, Up, SupportFull)`. The conservative proxy
  over-reports sturdiness for slabs/leaves/etc.; the real table fixes it with zero call-site change.
- **`server/suffocation.go::isSuffocating`** (v1 "any non-air non-fluid" proxy) → the cited real
  predicate is `blocksMotion() && isCollisionShapeFullBlock()`; the extractor's `coll_full` bit is
  exactly `isCollisionShapeFullBlock`. Plumb `block.IsCollisionShapeFullBlock(id)` (× a `blocksMotion`
  bit, also derivable from the same Cache/Properties pass) to close that documented gap.
- **`server/block_survival.go`** is the template to extend: add an attachment-survival family
  (torch/rail/redstone/repeater/comparator/button/lever/sign) whose `canSurvive` calls the table per
  §6. Same `updateShape → updateOrDestroy → destroyBlock` recursion already implemented for vegetation
  — only the predicate differs. `WallTorchBlock` already done; the rest follow §6 verbatim.

### Required companion data (same extractor pass, cheap)

`SUPPORTS_VEGETATION` is already handled. For the new attachment family you additionally need:
- `BlockTags.UNSTABLE_BOTTOM_CENTER` membership (standing-torch `canSupportCenter` DOWN guard — but
  torches query `UP`, so it never fires for torches; needed only if you port other CENTER/DOWN users).
- `Blocks.HOPPER` state ids (redstone OR-clause) — already resolvable from `block.StateList`.
- `getConnectedDirection(state)` for buttons/levers — a pure function of the `FACE`+`FACING`
  properties (FLOOR→UP, CEILING→DOWN, WALL→FACING); port directly, no data needed.

### Bottom line

Ship **(A)**: add `GenBlockSupport.java` + `gen_block_support.go` producing
`level/block/support.go` with `IsFaceSturdy(stateID,dir,supportType)`, `IsSolid`,
`IsCollisionShapeFullBlock`. It is a literal copy of the runtime's own precomputed `faceSturdy[]`
array — the maximally faithful implementation — and it simultaneously closes the `faceSturdyUp`,
`isSuffocating`, and sign/attachment-survival gaps with one reproducible codegen step. Do **not** port
the VoxelShape boolean engine for this purpose; that is the collision subsystem and belongs to a
later, separate phase (where `getFaceShape`/`joinIsNotEmpty` would be needed for *dynamic* runtime
shapes — irrelevant to attachment support, which is fully static).
```

