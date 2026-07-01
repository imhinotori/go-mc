import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.block.Block;

import java.io.*;
import java.util.*;

/**
 * GenBlockResistance — Extracts per-block explosion resistance from the MC runtime so the Go
 * server can port net.minecraft.world.level.ExplosionDamageCalculator.getBlockExplosionResistance
 * 1:1. Block.getExplosionResistance() returns the BlockBehaviour.Properties explosionResistance
 * field, HARDCODED in Java Block constructors, so it is NOT in the --all blocks.json report; it
 * must be reflected off each registered Block here (the same reason GenBlockHardness exists for
 * destroyTime).
 *
 * Output: block_resistance.json in the current directory.
 *
 * Fields per block (one row per registered Block, keyed by Block.ID() resource string):
 *   key         — registry name (e.g., "minecraft:stone")
 *   resistance  — Block.getExplosionResistance() (the BlockBehaviour explosionResistance field).
 *                 ExplosionDamageCalculator.getBlockExplosionResistance returns
 *                 max(block.getExplosionResistance(), fluidState.getExplosionResistance()); this
 *                 extractor emits the block half, the Go port applies the fluid max separately.
 *
 * Verified jar values (spot-checked against the canonical 26.2 jar this session):
 *   stone 6.0, dirt 0.5, grass_block 0.6, obsidian 1200.0, bedrock 3600000.0,
 *   oak_log 2.0, oak_leaves 0.2, sand 0.5, cobblestone 6.0, water/lava 100.0, air 0.0.
 */
public class GenBlockResistance {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        // One row per registered block, keyed by resource id.
        List<String[]> rows = new ArrayList<>(); // [key, resistance]

        for (Block block : BuiltInRegistries.BLOCK) {
            var key = BuiltInRegistries.BLOCK.getKey(block);
            float resistance = block.getExplosionResistance();
            rows.add(new String[] {
                key.toString(),
                floatStr(resistance),
            });
        }

        // Deterministic output (sorted by key) so the generated Go table is reproducible
        // regardless of registry iteration order.
        rows.sort(Comparator.comparing(r -> r[0]));

        try (PrintWriter pw = new PrintWriter(new FileWriter("block_resistance.json"))) {
            pw.println("[");
            for (int i = 0; i < rows.size(); i++) {
                String[] r = rows.get(i);
                pw.printf("  {\"key\": %s, \"resistance\": %s}%s%n",
                    jsonStr(r[0]), r[1], i < rows.size() - 1 ? "," : "");
            }
            pw.println("]");
        }

        System.out.printf("GenBlockResistance: wrote block_resistance.json (%d blocks)%n", rows.size());
    }

    static String floatStr(float f) {
        return Float.toString(f);
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
