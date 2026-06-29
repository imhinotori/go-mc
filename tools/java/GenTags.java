import com.google.gson.Gson;
import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.zip.*;

/**
 * GenTags — Extracts damage-type and item tag membership from the 26.2 server jar.
 *
 * Reads the datapack tag JSON directly from the inner jar zip (the SAME data vanilla
 * net.minecraft.tags.TagLoader reads from the resource pack) — no Bootstrap binding,
 * no reflective mid-tick extraction. The datapack JSON is the authoritative source of
 * truth (CONTEXT Grey Area 1: "datapack/registries report is authoritative").
 *
 * Source semantics ported 1:1 from net.minecraft.tags.TagLoader.build:
 *   - A tag file is { "values": [ <entry>, ... ] }.
 *   - An <entry> is either a member resource-location string ("minecraft:fall"),
 *     or a REFERENCE to another tag, prefixed with '#' ("#minecraft:is_player_attack").
 *     A '#'-entry is RECURSIVELY expanded and its members merged (NOT stored literally) —
 *     exactly TagLoader.build's reference resolution.
 *   - An <entry> may also be the object form { "id": "...", "required": <bool> }
 *     (datapack value schema); the 26.2 jar uses only the bare-string form, but the
 *     object form is handled for forward-compatibility.
 *
 * Output: tags.json in the current directory (the extractor cwd = outputDir), shape:
 *   {
 *     "damage_type": { "bypasses_armor": ["minecraft:fall", ...], ... },
 *     "item":        { "pig_food": ["minecraft:carrot", ...], ... },
 *     "damage_type_elements": ["minecraft:arrow", "minecraft:fall", ...]   (sorted; the
 *         full damage_type registry element set, which has NO numeric id in registries.json
 *         because damage_type is a dynamic/datapack registry — the Go generator assigns ids)
 *   }
 * Member values are emitted as their FLATTENED resource-location strings; the Go generator
 * (tools/gen_tags.go) resolves them to numeric ids.
 */
public class GenTags {

    static final String DAMAGE_TYPE_PREFIX = "data/minecraft/tags/damage_type/";
    static final String ITEM_PREFIX = "data/minecraft/tags/item/";
    static final String DAMAGE_TYPE_ELEMENT_PREFIX = "data/minecraft/damage_type/";

    static final Gson GSON = new Gson();

    public static void main(String[] args) throws Exception {
        File innerJar = findInnerJar(args);
        if (innerJar == null) {
            System.err.println("GenTags: could not locate the inner server jar (-inner.jar) on the classpath or args");
            System.exit(1);
        }
        System.out.printf("GenTags: reading tags from %s%n", innerJar);

        // Raw per-family entry lists (values verbatim, including '#'-refs), keyed by tag name.
        Map<String, List<String>> damageRaw = new TreeMap<>();
        Map<String, List<String>> itemRaw = new TreeMap<>();
        Set<String> damageElements = new TreeSet<>();

        try (ZipFile zip = new ZipFile(innerJar)) {
            for (var entries = zip.entries(); entries.hasMoreElements(); ) {
                ZipEntry e = entries.nextElement();
                String name = e.getName();
                if (e.isDirectory()) continue;

                if (name.startsWith(DAMAGE_TYPE_PREFIX) && name.endsWith(".json")) {
                    damageRaw.put(tagName(name, DAMAGE_TYPE_PREFIX), readValues(zip, e));
                } else if (name.startsWith(ITEM_PREFIX) && name.endsWith(".json")) {
                    itemRaw.put(tagName(name, ITEM_PREFIX), readValues(zip, e));
                } else if (name.startsWith(DAMAGE_TYPE_ELEMENT_PREFIX) && name.endsWith(".json")
                        && !name.startsWith(DAMAGE_TYPE_PREFIX)) {
                    // A damage_type registry element (e.g. data/minecraft/damage_type/fall.json).
                    String id = name.substring(DAMAGE_TYPE_ELEMENT_PREFIX.length(), name.length() - ".json".length());
                    damageElements.add("minecraft:" + id);
                }
            }
        }

        // Recursively flatten '#'-refs (TagLoader.build) into flat member sets, per family.
        Map<String, List<String>> damageFlat = flattenAll(damageRaw);
        Map<String, List<String>> itemFlat = flattenAll(itemRaw);

        writeJson(damageFlat, itemFlat, new ArrayList<>(damageElements));

        System.out.printf("GenTags: wrote tags.json (%d damage_type tags, %d item tags, %d damage_type elements)%n",
            damageFlat.size(), itemFlat.size(), damageElements.size());
    }

    /** tagName turns "data/minecraft/tags/damage_type/bypasses_armor.json" into "bypasses_armor". */
    static String tagName(String entryName, String prefix) {
        String rest = entryName.substring(prefix.length());
        return rest.substring(0, rest.length() - ".json".length());
    }

    /** readValues parses a tag file's "values" array into the verbatim entry strings. */
    static List<String> readValues(ZipFile zip, ZipEntry e) throws IOException {
        List<String> out = new ArrayList<>();
        try (InputStreamReader r = new InputStreamReader(zip.getInputStream(e), StandardCharsets.UTF_8)) {
            JsonObject obj = GSON.fromJson(r, JsonObject.class);
            if (obj == null || !obj.has("values")) return out;
            JsonArray arr = obj.getAsJsonArray("values");
            for (JsonElement el : arr) {
                if (el.isJsonPrimitive()) {
                    out.add(el.getAsString());
                } else if (el.isJsonObject()) {
                    // Object form: { "id": "...", "required": <bool> } — take the id verbatim
                    // (the '#' nested-ref marker, if any, is carried inside the id string).
                    JsonObject vo = el.getAsJsonObject();
                    if (vo.has("id")) out.add(vo.get("id").getAsString());
                }
            }
        }
        return out;
    }

    /** flattenAll resolves '#'-refs for every tag in a family (TagLoader.build semantics). */
    static Map<String, List<String>> flattenAll(Map<String, List<String>> raw) {
        Map<String, List<String>> flat = new TreeMap<>();
        for (String tag : raw.keySet()) {
            LinkedHashSet<String> members = new LinkedHashSet<>();
            resolve(tag, raw, members, new HashSet<>());
            List<String> sorted = new ArrayList<>(members);
            Collections.sort(sorted);
            flat.put(tag, sorted);
        }
        return flat;
    }

    /**
     * resolve recursively expands a tag's entries into member resource-locations, following
     * '#'-prefixed references to other tags in the SAME family (TagLoader.build). The seen
     * set guards against cyclic tag references (vanilla TagLoader also detects cycles).
     */
    static void resolve(String tag, Map<String, List<String>> raw, Set<String> members, Set<String> seen) {
        if (!seen.add(tag)) return; // cycle guard
        List<String> entries = raw.get(tag);
        if (entries == null) return;
        for (String entry : entries) {
            if (entry.startsWith("#")) {
                // Reference to another tag: strip '#' and the "minecraft:" namespace, recurse.
                String refTag = stripNamespace(entry.substring(1));
                resolve(refTag, raw, members, seen);
            } else {
                members.add(entry); // a member resource-location ("minecraft:fall")
            }
        }
    }

    /** stripNamespace removes the "minecraft:" namespace from a tag reference. */
    static String stripNamespace(String s) {
        int i = s.indexOf(':');
        return i >= 0 ? s.substring(i + 1) : s;
    }

    /** findInnerJar locates the inner server jar via args[0] or the java.class.path. */
    static File findInnerJar(String[] args) {
        if (args.length > 0) {
            File f = new File(args[0]);
            if (f.exists()) return f;
        }
        // ExtractAll puts the inner jar FIRST on the classpath (buildExtractorClasspath).
        String cp = System.getProperty("java.class.path", "");
        for (String part : cp.split(File.pathSeparator)) {
            if (part.endsWith("-inner.jar")) {
                File f = new File(part);
                if (f.exists()) return f;
            }
        }
        return null;
    }

    /** writeJson hand-writes tags.json (PrintWriter + jsonStr, no external JSON emit dep). */
    static void writeJson(Map<String, List<String>> damage, Map<String, List<String>> item,
                          List<String> damageElements) throws IOException {
        try (PrintWriter pw = new PrintWriter(new FileWriter("tags.json"))) {
            pw.println("{");
            pw.print("  \"damage_type\": ");
            writeTagMap(pw, damage, "  ");
            pw.println(",");
            pw.print("  \"item\": ");
            writeTagMap(pw, item, "  ");
            pw.println(",");
            pw.print("  \"damage_type_elements\": ");
            writeStringArray(pw, damageElements, "  ");
            pw.println();
            pw.println("}");
        }
    }

    static void writeTagMap(PrintWriter pw, Map<String, List<String>> m, String indent) {
        pw.println("{");
        List<String> keys = new ArrayList<>(m.keySet());
        for (int i = 0; i < keys.size(); i++) {
            String k = keys.get(i);
            pw.printf("%s  %s: ", indent, jsonStr(k));
            writeStringArrayInline(pw, m.get(k));
            pw.println(i < keys.size() - 1 ? "," : "");
        }
        pw.printf("%s}", indent);
    }

    static void writeStringArray(PrintWriter pw, List<String> a, String indent) {
        pw.print("[");
        for (int i = 0; i < a.size(); i++) {
            pw.print(jsonStr(a.get(i)));
            if (i < a.size() - 1) pw.print(", ");
        }
        pw.print("]");
    }

    static void writeStringArrayInline(PrintWriter pw, List<String> a) {
        pw.print("[");
        for (int i = 0; i < a.size(); i++) {
            pw.print(jsonStr(a.get(i)));
            if (i < a.size() - 1) pw.print(", ");
        }
        pw.print("]");
    }

    static String jsonStr(String s) {
        return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }
}
