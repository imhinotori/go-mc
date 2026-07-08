import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.material.MapColor;

import java.io.*;
import java.util.*;

// Extracts per-block default MapColor.id (0..63) via defaultBlockState().getMapColor().
public class GenBlockMapColor {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();
        List<String[]> rows = new ArrayList<>();
        for (Block block : BuiltInRegistries.BLOCK) {
            var key = BuiltInRegistries.BLOCK.getKey(block);
            BlockState state = block.defaultBlockState();
            MapColor mc = state.getMapColor(EmptyBlockGetter.INSTANCE, BlockPos.ZERO);
            rows.add(new String[]{ key.toString(), Integer.toString(mc.id) });
        }
        rows.sort(Comparator.comparing(r -> r[0]));
        try (PrintWriter pw = new PrintWriter(new FileWriter("block_map_color.json"))) {
            pw.println("[");
            for (int i = 0; i < rows.size(); i++) {
                String[] r = rows.get(i);
                pw.printf("  {\"key\": \"%s\", \"map_color\": %s}%s%n", r[0], r[1], i < rows.size()-1 ? "," : "");
            }
            pw.println("]");
        }
        System.out.printf("wrote block_map_color.json (%d blocks)%n", rows.size());
    }
}
