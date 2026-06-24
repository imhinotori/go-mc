import net.minecraft.SharedConstants;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.biome.Climate;
import net.minecraft.world.level.biome.MultiNoiseBiomeSourceParameterList;

import com.mojang.datafixers.util.Pair;

import java.io.*;
import java.util.*;

/**
 * GenBiomeParams — Extracts the OVERWORLD multi-noise biome climate parameter
 * list from the MC runtime (PARITY-01, Wave 7 input).
 *
 * WHY A JAVA EXTRACTOR (not a pure unzip like the rest of the worldgen data):
 *
 * The overworld biome climate parameters are BAKED into Java code
 * (OverworldBiomes / MultiNoiseBiomeSourceParameterList.Preset.OVERWORLD), NOT
 * shipped as loose JSON. The loose data file
 * data/minecraft/worldgen/multi_noise_biome_source_parameter_list/overworld.json
 * is just {"preset": "minecraft:overworld"} (37 bytes) — a pointer to the baked
 * preset. So the real 6-D climate boxes can only be obtained by reflecting over
 * the runtime, the way every other custom extractor here (GenBiomes, GenEntities)
 * reads baked registry data.
 *
 * WHAT IT EMITS:
 *
 * MultiNoiseBiomeSourceParameterList.knownPresets() returns, for OVERWORLD, a
 * Climate.ParameterList<ResourceKey<Biome>> — a list of (ParameterPoint, biome)
 * pairs. Each ParameterPoint is the 6-D climate box: temperature, humidity,
 * continentalness, erosion, depth, weirdness (each a Climate.Parameter min/max
 * span) plus a long offset. Climate stores coordinates QUANTIZED as longs
 * (float * 10000, see Climate.quantizeCoord), so we un-quantize back to floats
 * to emit the standard vanilla multi-noise JSON shape that the Wave-7 Go biome
 * source parses (each parameter as a [min, max] float pair; offset as a float).
 *
 * Output: biome_parameters.json — a JSON array of
 *   { "biome": "minecraft:<id>",
 *     "parameters": {
 *       "temperature":     [min, max],
 *       "humidity":        [min, max],
 *       "continentalness": [min, max],
 *       "erosion":         [min, max],
 *       "depth":           [min, max],
 *       "weirdness":       [min, max],
 *       "offset":          off } }
 *
 * No external JSON deps (mirrors GenBiomes.java's manual JSON writer).
 */
public class GenBiomeParams {

    // Climate.quantizeCoord multiplies floats by 10000 to store them as longs.
    static final double QUANTIZE = 10000.0;

    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        // knownPresets() maps each Preset -> Climate.ParameterList<ResourceKey<Biome>>.
        // We want the OVERWORLD preset's parameter list.
        var presets = MultiNoiseBiomeSourceParameterList.knownPresets();
        Climate.ParameterList<net.minecraft.resources.ResourceKey<net.minecraft.world.level.biome.Biome>> overworld =
            presets.get(MultiNoiseBiomeSourceParameterList.Preset.OVERWORLD);

        if (overworld == null) {
            throw new IllegalStateException(
                "OVERWORLD preset not present in MultiNoiseBiomeSourceParameterList.knownPresets()");
        }

        List<Pair<Climate.ParameterPoint, net.minecraft.resources.ResourceKey<net.minecraft.world.level.biome.Biome>>> values =
            overworld.values();

        try (PrintWriter pw = new PrintWriter(new FileWriter("biome_parameters.json"))) {
            pw.println("[");
            for (int i = 0; i < values.size(); i++) {
                var pair = values.get(i);
                Climate.ParameterPoint p = pair.getFirst();
                String biome = pair.getSecond().location().toString();

                pw.print("  {\"biome\": ");
                pw.print(jsonStr(biome));
                pw.print(", \"parameters\": {");
                pw.print("\"temperature\": ");      writeParam(pw, p.temperature());
                pw.print(", \"humidity\": ");        writeParam(pw, p.humidity());
                pw.print(", \"continentalness\": "); writeParam(pw, p.continentalness());
                pw.print(", \"erosion\": ");         writeParam(pw, p.erosion());
                pw.print(", \"depth\": ");           writeParam(pw, p.depth());
                pw.print(", \"weirdness\": ");       writeParam(pw, p.weirdness());
                pw.print(", \"offset\": ");          pw.print(num(p.offset() / QUANTIZE));
                pw.print("}}");
                pw.println(i < values.size() - 1 ? "," : "");
            }
            pw.println("]");
        }

        System.out.printf("GenBiomeParams: wrote biome_parameters.json (%d overworld biome boxes)%n",
            values.size());
    }

    // writeParam emits a Climate.Parameter as a [min, max] float pair, un-quantizing
    // the stored longs back to the float coordinate space (the vanilla JSON shape).
    static void writeParam(PrintWriter pw, Climate.Parameter param) {
        pw.print("[");
        pw.print(num(param.min() / QUANTIZE));
        pw.print(", ");
        pw.print(num(param.max() / QUANTIZE));
        pw.print("]");
    }

    // num formats a double without scientific notation and without a trailing
    // ".0" artifact beyond what JSON needs; keeps it a plain decimal token.
    static String num(double d) {
        if (d == Math.floor(d) && !Double.isInfinite(d)) {
            // Integral value (e.g. 0.0, 1.0) — emit as a decimal so it stays a float token.
            return String.format(Locale.ROOT, "%.1f", d);
        }
        // Trim to a stable precision; the quantize step caps real precision at 1e-4.
        return String.format(Locale.ROOT, "%s", d);
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
