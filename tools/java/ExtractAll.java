///usr/bin/env java --source 21 "$0" "$@"; exit $?
// ^^^ allows running as: java ExtractAll.java (JEP 458, JDK 21+)

import java.io.*;
import java.nio.file.*;
import java.util.*;
import java.util.zip.*;

/**
 * ExtractAll — Extracts MC data from a pre-downloaded server jar.
 *
 * Runs inside a Docker/Podman container with these mounted volumes:
 *   /cache     — pre-downloaded server jar (by Go host)
 *   /jsons     — output directory for JSON files
 *   /java      — this file + custom Java extractors (read-only)
 *
 * Arguments:
 *   First argument is the Minecraft version (e.g., "1.21.11").
 *
 * The server jar and language files are downloaded by the Go host before
 * this container runs. This file handles extraction and data generation:
 *   1. Extract inner jar from bundler format
 *   2. Run MC --all data generator → reports/*.json
 *   3. Copy reports to /jsons/<version>/
 *   4. Compile and run custom Java extractors
 */
public class ExtractAll {

    static String version;
    static Path cacheDir  = Path.of("/cache");
    static Path jsonsDir  = Path.of("/jsons");
    static Path javaDir   = Path.of("/java");

    public static void main(String[] args) throws Exception {
        if (args.length > 0) {
            version = args[0];
        } else {
            fatal("Usage: ExtractAll <version> (e.g., 1.21.11)");
        }

        // Allow running outside container with custom paths.
        String cacheEnv = System.getenv("MC_CACHE_DIR");
        String jsonsEnv = System.getenv("MC_JSONS_DIR");
        String javaEnv = System.getenv("MC_JAVA_DIR");
        if (cacheEnv != null) cacheDir = Path.of(cacheEnv);
        if (jsonsEnv != null) jsonsDir = Path.of(jsonsEnv);
        if (javaEnv != null) javaDir = Path.of(javaEnv);

        log("ExtractAll: version=%s", version);
        log("  cache:      %s", cacheDir);
        log("  jsons:      %s", jsonsDir);
        log("  java:       %s", javaDir);

        // Step 1: Find pre-downloaded server jar.
        Path serverJar = cacheDir.resolve(version + "-server.jar");
        if (!Files.exists(serverJar) || Files.size(serverJar) == 0) {
            fatal("Server jar not found at %s (should be downloaded by Go host)", serverJar);
        }
        log("");
        log("Server jar: %s (%s)", serverJar, humanSize(Files.size(serverJar)));

        // Step 2: Extract inner jar from bundler format.
        Path innerJar = extractInnerJar(serverJar);

        // Step 3: Run --all data generator.
        Path reportsDir = runDataGenerator(serverJar);

        // Step 4: Copy --all reports to output.
        Path outputDir = jsonsDir.resolve(version);
        Files.createDirectories(outputDir);
        copyReports(reportsDir, outputDir);

        // Step 5: Run custom extractors (if present).
        runCustomExtractors(serverJar, innerJar, outputDir);

        // Done.
        log("");
        log("Extraction complete. Output:");
        try (var stream = Files.list(outputDir)) {
            stream.sorted().forEach(p -> {
                try {
                    long size = Files.size(p);
                    log("  %-30s %s", p.getFileName(), humanSize(size));
                } catch (IOException e) {
                    log("  %s (error: %s)", p.getFileName(), e.getMessage());
                }
            });
        }
    }

    // --- Step 2: Extract inner jar ---

    static Path extractInnerJar(Path bundledJar) throws Exception {
        Path innerPath = cacheDir.resolve(version + "-inner.jar");
        if (Files.exists(innerPath) && Files.size(innerPath) > 0) {
            log("  Inner jar cached: %s (%s)", innerPath, humanSize(Files.size(innerPath)));
            return innerPath;
        }

        log("");
        log("Extracting inner server jar from bundler format...");

        try (ZipFile zip = new ZipFile(bundledJar.toFile())) {
            // Look for META-INF/versions/*/server-*.jar.
            ZipEntry serverEntry = null;
            for (var entries = zip.entries(); entries.hasMoreElements(); ) {
                ZipEntry e = entries.nextElement();
                if (e.getName().startsWith("META-INF/versions/") && e.getName().endsWith(".jar")) {
                    serverEntry = e;
                    break;
                }
            }

            if (serverEntry == null) {
                // Not bundled — just copy.
                log("  Not bundled, using jar directly.");
                Files.copy(bundledJar, innerPath, StandardCopyOption.REPLACE_EXISTING);
                return innerPath;
            }

            log("  Found: %s (%s)", serverEntry.getName(), humanSize(serverEntry.getSize()));
            try (InputStream in = zip.getInputStream(serverEntry)) {
                Files.copy(in, innerPath, StandardCopyOption.REPLACE_EXISTING);
            }
        }

        log("  Extracted: %s", humanSize(Files.size(innerPath)));
        return innerPath;
    }

    // --- Step 3: Run --all data generator ---

    static Path runDataGenerator(Path serverJar) throws Exception {
        // Create a temp working dir for the generator (it creates files in cwd).
        Path workDir = cacheDir.resolve(version + "-datagen");
        Files.createDirectories(workDir);

        Path reportsDir = workDir.resolve("generated").resolve("reports");
        if (Files.exists(reportsDir.resolve("registries.json"))) {
            log("");
            log("Data generator output cached, skipping --all.");
            return reportsDir;
        }

        log("");
        log("Running MC --all data generator...");

        ProcessBuilder pb = new ProcessBuilder(
            "java",
            "-DbundlerMainClass=net.minecraft.data.Main",
            "-jar", serverJar.toAbsolutePath().toString(),
            "--all"
        );
        pb.directory(workDir.toFile());
        pb.inheritIO();

        Process proc = pb.start();
        int exit = proc.waitFor();
        if (exit != 0) {
            fatal("Data generator failed with exit code %d", exit);
        }

        if (!Files.exists(reportsDir.resolve("registries.json"))) {
            fatal("Data generator did not produce registries.json");
        }

        log("  Data generator complete.");
        return reportsDir;
    }

    // --- Step 4: Copy reports ---

    static void copyReports(Path reportsDir, Path outputDir) throws Exception {
        log("");
        log("Copying reports to %s ...", outputDir);

        String[] files = {"registries.json", "blocks.json", "items.json", "packets.json",
                          "commands.json", "datapack.json"};

        for (String name : files) {
            Path src = reportsDir.resolve(name);
            if (Files.exists(src)) {
                Files.copy(src, outputDir.resolve(name), StandardCopyOption.REPLACE_EXISTING);
                log("  %-25s %s", name, humanSize(Files.size(src)));
            }
        }

        // Also copy json-rpc-api-schema.json if present (1.21.11+).
        Path schema = reportsDir.resolve("json-rpc-api-schema.json");
        if (Files.exists(schema)) {
            Files.copy(schema, outputDir.resolve("json-rpc-api-schema.json"),
                       StandardCopyOption.REPLACE_EXISTING);
        }

        // 26.2+: the consolidated reports/items.json was split into per-item
        // component files under reports/minecraft/components/item/<id>.json.
        // The Go generators still expect a single items.json (map of
        // "minecraft:<id>" -> { "components": {...} }), so synthesize it when
        // the consolidated report is absent but the per-item directory exists.
        if (!Files.exists(outputDir.resolve("items.json"))) {
            aggregateItemComponents(reportsDir, outputDir);
        }
    }

    /**
     * Builds items.json from the 26.2+ per-item component files. Each source
     * file already has the shape { "components": {...} }; we map filename →
     * "minecraft:<filename>" and copy the body verbatim so no re-encoding (and
     * thus no precision loss) occurs.
     */
    static void aggregateItemComponents(Path reportsDir, Path outputDir) throws Exception {
        Path itemDir = reportsDir.resolve("minecraft").resolve("components").resolve("item");
        if (!Files.isDirectory(itemDir)) {
            log("  WARNING: no items.json and no per-item component dir at %s", itemDir);
            return;
        }

        List<Path> itemFiles = new ArrayList<>();
        try (var stream = Files.list(itemDir)) {
            stream.filter(p -> p.toString().endsWith(".json")).forEach(itemFiles::add);
        }
        itemFiles.sort(Comparator.comparing(p -> p.getFileName().toString()));

        StringBuilder sb = new StringBuilder();
        sb.append("{\n");
        for (int i = 0; i < itemFiles.size(); i++) {
            Path f = itemFiles.get(i);
            String fileName = f.getFileName().toString();
            String id = fileName.substring(0, fileName.length() - ".json".length());
            String body = Files.readString(f).trim();
            sb.append("  \"minecraft:").append(id).append("\": ").append(body);
            if (i < itemFiles.size() - 1) sb.append(',');
            sb.append('\n');
        }
        sb.append("}\n");

        Path out = outputDir.resolve("items.json");
        Files.writeString(out, sb.toString());
        log("  %-25s %s (synthesized from %d per-item files)",
            "items.json", humanSize(Files.size(out)), itemFiles.size());
    }

    // --- Step 5: Custom extractors ---

    static void runCustomExtractors(Path serverJar, Path innerJar, Path outputDir) throws Exception {
        String[] extractors = {"GenEntities", "GenComponents", "GenBlockEntities", "GenBlockProperties", "GenBiomes", "GenComponentSchema", "GenBiomeParams", "GenBlockHardness", "GenBlockResistance", "GenBlockSupport", "GenTags"};
        List<String> found = new ArrayList<>();

        for (String name : extractors) {
            Path src = javaDir.resolve(name + ".java");
            if (Files.exists(src)) {
                found.add(name);
            }
        }

        if (found.isEmpty()) {
            log("");
            log("No custom extractors found in %s (optional, skipping).", javaDir);
            return;
        }

        // Extract library jars from the bundled server jar.
        String classpath = buildExtractorClasspath(serverJar, innerJar);

        log("");
        log("Compiling custom extractors: %s", String.join(", ", found));

        // Compile all found extractors.
        List<String> javacArgs = new ArrayList<>(List.of(
            "javac", "-cp", classpath, "-proc:none",
            "-d", outputDir.toAbsolutePath().toString()
        ));
        for (String name : found) {
            javacArgs.add(javaDir.resolve(name + ".java").toAbsolutePath().toString());
        }

        int compileExit = new ProcessBuilder(javacArgs)
            .inheritIO().start().waitFor();
        if (compileExit != 0) {
            log("  WARNING: extractor compilation failed (exit %d). Skipping.", compileExit);
            return;
        }

        // Run each extractor.
        String runCp = classpath + ":" + outputDir.toAbsolutePath();
        for (String name : found) {
            log("  Running %s...", name);
            ProcessBuilder pb = new ProcessBuilder(
                "java", "-cp", runCp, name
            );
            pb.directory(outputDir.toFile());
            pb.inheritIO();

            int exit = pb.start().waitFor();
            if (exit != 0) {
                log("  WARNING: %s failed with exit code %d", name, exit);
            }
        }
    }

    /**
     * Extract library jars from the bundled server jar and build a classpath string.
     * The bundled jar contains META-INF/libraries/*.jar and META-INF/classpath-joined.
     */
    static String buildExtractorClasspath(Path serverJar, Path innerJar) throws Exception {
        Path libsDir = cacheDir.resolve(version + "-libs");
        Path doneMarker = libsDir.resolve(".extracted");

        if (!Files.exists(doneMarker)) {
            log("Extracting library jars from bundled server...");
            Files.createDirectories(libsDir);

            try (ZipFile zip = new ZipFile(serverJar.toFile())) {
                for (var entries = zip.entries(); entries.hasMoreElements(); ) {
                    ZipEntry e = entries.nextElement();
                    if (e.getName().startsWith("META-INF/libraries/") && e.getName().endsWith(".jar")) {
                        Path dest = libsDir.resolve(e.getName());
                        Files.createDirectories(dest.getParent());
                        try (InputStream in = zip.getInputStream(e)) {
                            Files.copy(in, dest, StandardCopyOption.REPLACE_EXISTING);
                        }
                    }
                }
            }
            Files.writeString(doneMarker, "ok");
            log("  Library jars extracted.");
        }

        // Build classpath: inner jar + all library jars.
        StringBuilder cp = new StringBuilder();
        cp.append(innerJar.toAbsolutePath());

        try (var stream = Files.walk(libsDir)) {
            stream.filter(p -> p.toString().endsWith(".jar")).forEach(p -> {
                cp.append(':').append(p.toAbsolutePath());
            });
        }

        return cp.toString();
    }

    // --- Utility ---

    static String humanSize(long bytes) {
        if (bytes >= 1024 * 1024) return String.format("%.1f MB", bytes / (1024.0 * 1024.0));
        if (bytes >= 1024) return String.format("%.1f KB", bytes / 1024.0);
        return bytes + " B";
    }

    static void log(String fmt, Object... args) {
        System.out.printf(fmt + "%n", args);
    }

    static void fatal(String fmt, Object... args) {
        System.err.printf("ExtractAll: " + fmt + "%n", args);
        System.exit(1);
    }
}
