import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.shapes.DiscreteVoxelShape;
import net.minecraft.world.phys.shapes.Shapes;
import net.minecraft.world.phys.shapes.VoxelShape;

import java.io.*;
import java.lang.reflect.Field;
import java.util.*;

/**
 * GenBlockCollisionShapes — Extracts the per-BLOCKSTATE collision VoxelShape for the Go
 * collision engine (the 1:1 port of Shapes.collide / VoxelShape.collideX / Entity.collide).
 *
 * WHAT is extracted, and WHY it is the faithful representation:
 *   - For every registered BlockState we call
 *     state.getCollisionShape(EmptyBlockGetter.INSTANCE, BlockPos.ZERO) — the same no-context
 *     read the vanilla per-state Cache performs at registration time (Cache.<init> calls
 *     state.getCollisionShape(EmptyBlockGetter.INSTANCE, BlockPos.ZERO)). For the dominant
 *     non-dynamic-shape blocks this IS the shape the live collision path uses
 *     (BlockStateBase.getCollisionShape(getter,pos) returns cache.collisionShape when the
 *     cache is present). Dynamic-shape blocks (hasDynamicShape(): moving_piston, scaffolding
 *     context variants, …) get their no-context value; those are flagged via the `large` list
 *     below because hasLargeCollisionShape() returns true when the cache is null.
 *   - We dump the DISCRETE GRID (per-axis coordinate lists + full-cell bit set), NOT the
 *     merged toAabbs() boxes. VoxelShape.collideX operates on the grid (findIndex over
 *     getCoords + DiscreteVoxelShape.isFullWide), and the grid granularity is OBSERVABLE:
 *     for a box already overlapping a shape, the first full slice BEYOND the box edge is a
 *     grid slice — merging adjacent cells (as toAabbs does) would lose internal boundaries
 *     and change the clamp in that (rare but real) case. CITE: javap VoxelShape.collideX —
 *     the triple loop over DiscreteVoxelShape.isFullWide with get(axis, idx) as the face.
 *   - Coordinates are read via the public VoxelShape.getCoords(Axis) (the exact DoubleList
 *     collideX indexes into: ArrayVoxelShape's DoubleArrayList or CubeVoxelShape's
 *     FractionalDoubleList — either way the same double values the live path sees), emitted
 *     with Double.toString (shortest round-trip form, parses back to identical bits in Go).
 *   - The full-cell set is read off the protected VoxelShape.shape field (reflection; this is
 *     an offline extractor, not runtime) via the public DiscreteVoxelShape.isFull(x,y,z),
 *     indexed (x*ySize + y)*zSize + z — the BitSetDiscreteVoxelShape.getIndex order.
 *
 * Also per shape:
 *   - "block": whether the returned VoxelShape is IDENTITY-equal to Shapes.block(). The
 *     BlockCollisions iterator has a fast path `shape == Shapes.block()` that uses a strict
 *     AABB.intersects test instead of Shapes.joinIsNotEmpty — identity, not geometry, selects
 *     the path, so it must be part of the dedup key. CITE: javap
 *     net.minecraft.world.level.BlockCollisions.computeNext — `if_acmpne` against
 *     Shapes.block().
 *
 * And per state:
 *   - `large`: BlockStateBase.hasLargeCollisionShape() — the Cache.largeCollisionShape flag
 *     (true when the shape extends outside the unit cube on any axis, OR when the cache is
 *     null). The BlockCollisions Cursor3D consults it for the ±1 border ring (type-1/face
 *     positions). CITE: javap BlockCollisions.computeNext — `getNextType == 1 &&
 *     !state.hasLargeCollisionShape() -> skip`.
 *
 * Output: block_collision_shapes.json in the current directory:
 *   {
 *     "shapes": [ {"x":[..],"y":[..],"z":[..],"full":[cellIdx,..],"block":bool}, ... ],
 *     "states": [ shapeId, shapeId, ... ],   // index == global block-state id (dense)
 *     "large":  [ stateId, ... ]             // states with hasLargeCollisionShape()
 *   }
 * Shapes are deduplicated by (coords, full-cells, block-identity); `states` maps every
 * global block-state id (Block.getId(state) == the Go StateID) to its shape row.
 */
public class GenBlockCollisionShapes {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        // Reflective access to the protected VoxelShape.shape (DiscreteVoxelShape) — the
        // grid holder collideX reads. Offline-extractor-only reflection.
        Field shapeField = VoxelShape.class.getDeclaredField("shape");
        shapeField.setAccessible(true);

        VoxelShape blockShape = Shapes.block();

        List<String> shapeRows = new ArrayList<>();   // JSON per unique shape, in first-seen order
        Map<String, Integer> shapeIds = new HashMap<>(); // dedup key -> shape row index
        Map<Integer, Integer> stateToShape = new TreeMap<>(); // global state id -> shape id
        List<Integer> largeStates = new ArrayList<>();

        for (Block block : BuiltInRegistries.BLOCK) {
            for (BlockState state : block.getStateDefinition().getPossibleStates()) {
                int id = Block.getId(state);

                VoxelShape shape = state.getCollisionShape(EmptyBlockGetter.INSTANCE, BlockPos.ZERO);
                boolean isBlockIdentity = (shape == blockShape);

                DiscreteVoxelShape grid = (DiscreteVoxelShape) shapeField.get(shape);
                double[] xs = coords(shape, Direction.Axis.X);
                double[] ys = coords(shape, Direction.Axis.Y);
                double[] zs = coords(shape, Direction.Axis.Z);
                int sx = grid.getXSize(), sy = grid.getYSize(), sz = grid.getZSize();

                // Full cells in BitSetDiscreteVoxelShape.getIndex order: (x*ySize + y)*zSize + z.
                List<Integer> full = new ArrayList<>();
                for (int x = 0; x < sx; x++)
                    for (int y = 0; y < sy; y++)
                        for (int z = 0; z < sz; z++)
                            if (grid.isFull(x, y, z))
                                full.add((x * sy + y) * sz + z);

                String row = shapeJson(xs, ys, zs, full, isBlockIdentity);
                Integer shapeId = shapeIds.get(row);
                if (shapeId == null) {
                    shapeId = shapeRows.size();
                    shapeRows.add(row);
                    shapeIds.put(row, shapeId);
                }
                stateToShape.put(id, shapeId);

                if (state.hasLargeCollisionShape()) {
                    largeStates.add(id);
                }
            }
        }

        // Sanity: dense state ids 0..N-1 (the Go table is a flat slice keyed by StateID).
        int n = stateToShape.size();
        for (int i = 0; i < n; i++) {
            if (!stateToShape.containsKey(i)) {
                System.err.printf("GenBlockCollisionShapes: WARNING non-dense state ids, missing %d%n", i);
                break;
            }
        }

        try (PrintWriter pw = new PrintWriter(new FileWriter("block_collision_shapes.json"))) {
            pw.println("{");
            pw.println("  \"shapes\": [");
            for (int i = 0; i < shapeRows.size(); i++) {
                pw.printf("    %s%s%n", shapeRows.get(i), i < shapeRows.size() - 1 ? "," : "");
            }
            pw.println("  ],");
            pw.print("  \"states\": [");
            int written = 0;
            for (Map.Entry<Integer, Integer> e : stateToShape.entrySet()) {
                if (written % 32 == 0) pw.printf("%n    ");
                pw.print(e.getValue());
                if (written < n - 1) pw.print(",");
                written++;
            }
            pw.printf("%n  ],%n");
            pw.print("  \"large\": [");
            for (int i = 0; i < largeStates.size(); i++) {
                if (i % 32 == 0) pw.printf("%n    ");
                pw.print(largeStates.get(i));
                if (i < largeStates.size() - 1) pw.print(",");
            }
            pw.printf("%n  ]%n");
            pw.println("}");
        }

        System.out.printf("GenBlockCollisionShapes: wrote block_collision_shapes.json (%d states, %d unique shapes, %d large)%n",
                n, shapeRows.size(), largeStates.size());
    }

    static double[] coords(VoxelShape shape, Direction.Axis axis) {
        var list = shape.getCoords(axis);
        double[] out = new double[list.size()];
        for (int i = 0; i < out.length; i++) out[i] = list.getDouble(i);
        return out;
    }

    static String shapeJson(double[] xs, double[] ys, double[] zs, List<Integer> full, boolean isBlock) {
        StringBuilder sb = new StringBuilder();
        sb.append("{\"x\": ").append(doubles(xs));
        sb.append(", \"y\": ").append(doubles(ys));
        sb.append(", \"z\": ").append(doubles(zs));
        sb.append(", \"full\": [");
        for (int i = 0; i < full.size(); i++) {
            if (i > 0) sb.append(',');
            sb.append(full.get(i));
        }
        sb.append("], \"block\": ").append(isBlock).append('}');
        return sb.toString();
    }

    // Double.toString: the shortest decimal that uniquely identifies the double — Go's
    // strconv.ParseFloat(_, 64) parses it back to the identical bits.
    static String doubles(double[] vals) {
        StringBuilder sb = new StringBuilder("[");
        for (int i = 0; i < vals.length; i++) {
            if (i > 0) sb.append(',');
            sb.append(Double.toString(vals[i]));
        }
        sb.append(']');
        return sb.toString();
    }
}
