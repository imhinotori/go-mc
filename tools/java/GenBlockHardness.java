import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.state.BlockState;

import java.io.*;
import java.util.*;

/**
 * GenBlockHardness — Extracts per-block break-time inputs from the MC runtime so the Go
 * server can port net.minecraft.world.level.block.state.BlockBehaviour.getDestroyProgress
 * 1:1. These two values are HARDCODED in Java Block constructors (BlockBehaviour.Properties
 * destroyTime / requiresCorrectToolForDrops), so they are NOT in the --all blocks.json
 * report; they must be reflected off defaultBlockState() here.
 *
 * Output: block_hardness.json in the current directory.
 *
 * Fields per block (one row per registered Block, keyed by Block.ID() resource string):
 *   key            — registry name (e.g., "minecraft:stone")
 *   destroy_speed  — BlockState.getDestroySpeed(EmptyBlockGetter.INSTANCE, BlockPos.ZERO),
 *                    the BlockBehaviour destroyTime (a.k.a. hardness). -1.0 == unbreakable
 *                    (bedrock, barrier, …). The BlockGetter/BlockPos are unused by the base
 *                    BlockBehaviour.getDestroySpeed (it returns the constant destroyTime), so
 *                    EmptyBlockGetter + ORIGIN are the faithful no-context arguments.
 *   requires_tool  — BlockState.requiresCorrectToolForDrops(): the "needs the right tool to
 *                    drop" flag that selects getDestroyProgress's 30 vs 100 divisor.
 *
 * Verified jar values (spot-checked against the canonical 26.2 jar this session):
 *   stone 1.5/true, dirt 0.5/false, grass_block 0.6/false, obsidian 50.0/true,
 *   bedrock -1.0, oak_log 2.0/false, oak_leaves 0.2/false, sand 0.5/false,
 *   coal_ore 3.0/true, water/lava 100.0.
 */
public class GenBlockHardness {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        // One row per registered block, in registry order, keyed by resource id.
        List<String[]> rows = new ArrayList<>(); // [key, destroySpeed, requiresTool]

        for (Block block : BuiltInRegistries.BLOCK) {
            var key = BuiltInRegistries.BLOCK.getKey(block);
            BlockState state = block.defaultBlockState();

            // BlockBehaviour.getDestroySpeed: the constant destroyTime (hardness). The base
            // implementation ignores the BlockGetter/BlockPos, so EmptyBlockGetter.INSTANCE +
            // BlockPos.ZERO are the no-context arguments (matching how the Go port calls it).
            float destroySpeed = state.getDestroySpeed(EmptyBlockGetter.INSTANCE, BlockPos.ZERO);
            boolean requiresTool = state.requiresCorrectToolForDrops();

            rows.add(new String[] {
                key.toString(),
                floatStr(destroySpeed),
                Boolean.toString(requiresTool),
            });
        }

        // Deterministic output (sorted by key) so the generated Go table is reproducible
        // regardless of registry iteration order.
        rows.sort(Comparator.comparing(r -> r[0]));

        try (PrintWriter pw = new PrintWriter(new FileWriter("block_hardness.json"))) {
            pw.println("[");
            for (int i = 0; i < rows.size(); i++) {
                String[] r = rows.get(i);
                pw.printf("  {\"key\": %s, \"destroy_speed\": %s, \"requires_tool\": %s}%s%n",
                    jsonStr(r[0]), r[1], r[2], i < rows.size() - 1 ? "," : "");
            }
            pw.println("]");
        }

        System.out.printf("GenBlockHardness: wrote block_hardness.json (%d blocks)%n", rows.size());
    }

    // floatStr emits a float as a JSON number that round-trips to the exact same float32 in
    // Go. Java's Float.toString gives the shortest decimal that uniquely identifies the
    // float, which Go's strconv.ParseFloat(_, 32) parses back to the identical bits.
    static String floatStr(float f) {
        return Float.toString(f);
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
