import net.minecraft.SharedConstants;
import net.minecraft.core.Direction;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.shapes.Shapes;
import net.minecraft.world.phys.shapes.VoxelShape;

import java.io.*;
import java.util.*;

/**
 * GenBlockLight — extracts the per-BLOCKSTATE data that drives the vanilla
 * net.minecraft.world.level.lighting light engine 1:1.
 *
 * Per state (indexed by the GLOBAL block-state id Block.getId(state) == the Go StateID):
 *   - lb : getLightDampening()  (LightEngine.getOpacity(state) = max(1, lb); the "opacity")
 *   - em : getLightEmission()   (block-light emission)
 *   - 6 face occlusion classes (Direction ordinal order DOWN,UP,NORTH,SOUTH,WEST,EAST),
 *     one per direction, mirroring LightEngine.getOcclusionShape(state, dir):
 *       * class EMPTY  : LightEngine.isEmptyShape(state) (== !canOcclude || !useShapeForLightOcclusion)
 *                        OR the per-face occlusion shape is empty.
 *       * class FULL   : getFaceOcclusionShape(dir) == Shapes.block() (the whole face square).
 *       * class PARTIAL: a proper sub-face shape; stored as a 16x16 coverage bitmask (256 bits
 *                        = 4x uint64) sampled on the 1/16 grid. All partial face shapes in 26.2
 *                        are exactly 1/16-grid-aligned (verified), so the sample is EXACT.
 *
 * LightEngine.shapeOccludes(from,to,dir) = Shapes.faceShapeOccludes(
 *     getOcclusionShape(from,dir), getOcclusionShape(to,dir.getOpposite())), and
 * Shapes.faceShapeOccludes(a,b):
 *     if a==block() || b==block() -> true
 *     if a.isEmpty() && b.isEmpty() -> false
 *     else -> OR(a,b) covers the full unit face.
 * With the class scheme: FULL acts as block(), EMPTY as empty(), PARTIAL as its 16x16 mask;
 * the "covers full face" test is (maskA | maskB) == ALL_ONES. This reproduces faceShapeOccludes
 * exactly (self-checked below against the live Shapes.faceShapeOccludes for a state-pair sample).
 *
 * Output: block_light.json in the current directory:
 *   {
 *     "masks": [ [u64,u64,u64,u64], ... ],   // dedup pool of 256-bit face masks
 *     "states": [ {"id":N,"lb":L,"em":E,"es":B,"f":[c0..c5]}, ... ] (es=1 iff LightEngine.isEmptyShape)
 *   }
 * where each ci is -1 for EMPTY, -2 for FULL, or an index into "masks" for PARTIAL.
 */
public class GenBlockLight {
    // Build the 16x16 mask for a given face shape + direction. u,v are the two in-plane axes.
    static long[] maskForDir(VoxelShape faceShape, Direction dir) {
        long[] m = new long[4];
        faceShape.forAllBoxes((x1, y1, z1, x2, y2, z2) -> {
            double u1, u2, v1, v2;
            switch (dir.getAxis()) {
                case Y -> { u1 = x1; u2 = x2; v1 = z1; v2 = z2; }
                case Z -> { u1 = x1; u2 = x2; v1 = y1; v2 = y2; }
                case X -> { u1 = z1; u2 = z2; v1 = y1; v2 = y2; }
                default -> throw new IllegalStateException();
            }
            int uu1 = (int) Math.round(u1 * 16), uu2 = (int) Math.round(u2 * 16);
            int vv1 = (int) Math.round(v1 * 16), vv2 = (int) Math.round(v2 * 16);
            for (int v = vv1; v < vv2; v++) {
                for (int u = uu1; u < uu2; u++) {
                    int bit = v * 16 + u;
                    m[bit >> 6] |= 1L << (bit & 63);
                }
            }
        });
        return m;
    }

    static final long[] ALL_ONES = {-1L, -1L, -1L, -1L};

    // Reference faceShapeOccludes via masks (FULL=all ones, EMPTY=zero, PARTIAL=mask).
    static boolean occludesMask(long[] a, long[] b) {
        for (int i = 0; i < 4; i++) if (((a[i] | b[i]) & ALL_ONES[i]) != ALL_ONES[i]) return false;
        return true;
    }

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        Direction[] dirs = Direction.values(); // ordinal: DOWN,UP,NORTH,SOUTH,WEST,EAST

        Map<String, Integer> maskPool = new LinkedHashMap<>();
        List<long[]> maskList = new ArrayList<>();

        // state rows sorted by id.
        TreeMap<Integer, int[]> rows = new TreeMap<>(); // id -> [lb, em, f0..f5]

        for (Block block : BuiltInRegistries.BLOCK) {
            for (BlockState st : block.getStateDefinition().getPossibleStates()) {
                int id = Block.getId(st);
                int lb = st.getLightDampening();
                int em = st.getLightEmission();
                boolean emptyShape = !st.canOcclude() || !st.useShapeForLightOcclusion();
                int[] row = new int[9];
                row[0] = lb;
                row[1] = em;
                row[8] = emptyShape ? 1 : 0;
                for (int di = 0; di < 6; di++) {
                    Direction d = dirs[di];
                    int cls;
                    if (emptyShape) {
                        cls = -1; // EMPTY
                    } else {
                        VoxelShape fs = st.getFaceOcclusionShape(d);
                        if (fs == Shapes.block()) {
                            cls = -2; // FULL
                        } else if (fs.isEmpty()) {
                            cls = -1; // EMPTY
                        } else {
                            long[] m = maskForDir(fs, d);
                            String key = m[0] + "," + m[1] + "," + m[2] + "," + m[3];
                            Integer idx = maskPool.get(key);
                            if (idx == null) {
                                idx = maskList.size();
                                maskPool.put(key, idx);
                                maskList.add(m);
                            }
                            cls = idx; // >=0 => PARTIAL mask index
                        }
                    }
                    row[2 + di] = cls;
                }
                rows.put(id, row);
            }
        }

        // Sanity: dense ids 0..N-1.
        int n = rows.size();
        for (int i = 0; i < n; i++) if (!rows.containsKey(i)) { System.err.println("WARNING non-dense id " + i); break; }

        // SELF-CHECK: compare mask-based faceShapeOccludes with live Shapes.faceShapeOccludes for a
        // representative set of block-state pairs (across every direction), including partial-vs-partial.
        selfCheck(dirs, maskList);

        try (PrintWriter pw = new PrintWriter(new BufferedWriter(new FileWriter("block_light.json")))) {
            pw.println("{");
            pw.println("  \"masks\": [");
            for (int i = 0; i < maskList.size(); i++) {
                long[] m = maskList.get(i);
                pw.printf("    [%d,%d,%d,%d]%s%n", m[0], m[1], m[2], m[3], i < maskList.size() - 1 ? "," : "");
            }
            pw.println("  ],");
            pw.println("  \"states\": [");
            int w = 0;
            for (Map.Entry<Integer, int[]> e : rows.entrySet()) {
                int[] r = e.getValue();
                pw.printf("    {\"id\":%d,\"lb\":%d,\"em\":%d,\"es\":%d,\"f\":[%d,%d,%d,%d,%d,%d]}%s%n",
                        e.getKey(), r[0], r[1], r[8], r[2], r[3], r[4], r[5], r[6], r[7], w < n - 1 ? "," : "");
                w++;
            }
            pw.println("  ]");
            pw.println("}");
        }
        System.out.printf("GenBlockLight: wrote block_light.json (%d states, %d dedup face masks)%n", n, maskList.size());
    }

    // Verifies (maskA|maskB)==ALL for the PARTIAL cases matches the live engine's faceShapeOccludes,
    // and cross-checks FULL/EMPTY fast paths, for a broad state-pair sample over all 6 directions.
    static void selfCheck(Direction[] dirs, List<long[]> maskList) {
        List<BlockState> states = new ArrayList<>();
        for (Block block : BuiltInRegistries.BLOCK)
            for (BlockState st : block.getStateDefinition().getPossibleStates())
                states.add(st);
        // sample every 137th state paired with every 149th state (coprime-ish) => broad coverage.
        int mismatches = 0, checks = 0;
        for (int i = 0; i < states.size(); i += 137) {
            for (int j = 0; j < states.size(); j += 149) {
                BlockState from = states.get(i), to = states.get(j);
                for (Direction d : dirs) {
                    boolean vanilla = liveShapeOccludes(from, to, d);
                    boolean mine = myShapeOccludes(from, to, d);
                    checks++;
                    if (vanilla != mine) {
                        mismatches++;
                        if (mismatches <= 20)
                            System.err.printf("MISMATCH from=%s to=%s dir=%s vanilla=%b mine=%b%n",
                                    BuiltInRegistries.BLOCK.getKey(from.getBlock()), BuiltInRegistries.BLOCK.getKey(to.getBlock()), d, vanilla, mine);
                    }
                }
            }
        }
        System.out.printf("GenBlockLight self-check: %d pairs*dirs checked, %d mismatches%n", checks, mismatches);
        if (mismatches > 0) throw new IllegalStateException("mask-based faceShapeOccludes does not match vanilla");
    }

    static boolean liveShapeOccludes(BlockState fromState, BlockState toState, Direction dir) {
        // mirror LightEngine.shapeOccludes exactly.
        VoxelShape fromShape = getOcclusionShape(fromState, dir);
        VoxelShape toShape = getOcclusionShape(toState, dir.getOpposite());
        return Shapes.faceShapeOccludes(fromShape, toShape);
    }

    static VoxelShape getOcclusionShape(BlockState state, Direction dir) {
        boolean emptyShape = !state.canOcclude() || !state.useShapeForLightOcclusion();
        return emptyShape ? Shapes.empty() : state.getFaceOcclusionShape(dir);
    }

    static boolean myShapeOccludes(BlockState fromState, BlockState toState, Direction dir) {
        long[] a = classMask(fromState, dir);
        long[] b = classMask(toState, dir.getOpposite());
        if (a == ALL_ONES || b == ALL_ONES) return true;
        if (isZero(a) && isZero(b)) return false;
        return occludesMask(a, b);
    }

    static final long[] ZERO = {0, 0, 0, 0};

    static boolean isZero(long[] m) { return m[0]==0&&m[1]==0&&m[2]==0&&m[3]==0; }

    static long[] classMask(BlockState st, Direction d) {
        boolean emptyShape = !st.canOcclude() || !st.useShapeForLightOcclusion();
        if (emptyShape) return ZERO;
        VoxelShape fs = st.getFaceOcclusionShape(d);
        if (fs == Shapes.block()) return ALL_ONES;
        if (fs.isEmpty()) return ZERO;
        return maskForDir(fs, d);
    }
}
