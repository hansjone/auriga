package auriga.resource.archive;

import static auriga.resource.web.ResourceBody.*;

import auriga.resource.app.CatalogManifest;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import org.apache.commons.compress.compressors.gzip.GzipCompressorInputStream;
import org.yaml.snakeyaml.LoaderOptions;
import org.yaml.snakeyaml.Yaml;
import org.yaml.snakeyaml.constructor.SafeConstructor;
import tools.jackson.databind.JsonNode;

/** Validates without extraction; only a bounded, strict text-only ustar profile is accepted. */
public final class SkillArchiveValidator {
  private SkillArchiveValidator() {}

  public static String validate(InputStream source, long compressedSize, JsonNode manifest) {
    try (var owned = source;
        var buffered = new BufferedInputStream(owned, 8192)) {
      buffered.mark(TarGzipArchiveValidator.MAX_GZIP_HEADER + 1);
      try (var check =
          new java.util.zip.GZIPInputStream(new TarGzipArchiveValidator.HeaderInput(buffered))) {}
      buffered.reset();
      var header = new TarGzipArchiveValidator.HeaderInput(buffered);
      try (var gzip =
          GzipCompressorInputStream.builder()
              .setInputStream(header)
              .setDecompressConcatenated(false)
              .get()) {
        header.complete = true;
        var expanded =
            new TarGzipArchiveValidator.ExpandedInput(
                gzip, Math.min(20L * 1024 * 1024, compressedSize * 100));
        try (var tar = new StrictUstarInputStream(expanded, true)) {
          byte[] skill = null;
          var text = new StringBuilder();
          for (var entry = tar.getNextEntry(); entry != null; entry = tar.getNextEntry()) {
            if (tar.entries > 512 || entry.getSize() > 2097152) throw invalid();
            String path = entry.getName();
            if (entry.isDirectory() && path.endsWith("/"))
              path = path.substring(0, path.length() - 1);
            CatalogManifest.path(path);
            if (path.getBytes(StandardCharsets.UTF_8).length > 240
                || path.split("/").length > 8
                || (entry.getMode() & 0111) != 0 && !entry.isDirectory()) throw invalid();
            if (!path.equals("SKILL.md")
                && !path.equals("references")
                && !path.equals("assets")
                && !path.startsWith("references/")
                && !path.startsWith("assets/")) throw invalid();
            if (!entry.isDirectory()) {
              if (!path.matches(".*\\.(md|txt|json|yaml|yml)$")) throw invalid();
              byte[] bytes = tar.readNBytes(2097153);
              if (bytes.length != entry.getSize()) throw invalid();
              String decoded = CatalogManifest.utf8(bytes);
              if (decoded.indexOf(0) >= 0) throw invalid();
              text.append(decoded).append('\n');
              if (path.equals("SKILL.md")) {
                if (skill != null || bytes.length > 262144) throw invalid();
                skill = bytes;
              }
            }
            expanded.paddingRemaining = (512 - entry.getSize() % 512) % 512;
          }
          if (skill == null || tar.zeroRecords != 2) throw invalid();
          byte[] block = new byte[8192];
          for (int n; (n = expanded.read(block)) != -1; )
            for (int i = 0; i < n; i++) if (block[i] != 0) throw invalid();
          if (expanded.count % 512 != 0 || buffered.read() != -1) throw invalid();
          frontmatter(CatalogManifest.utf8(skill), manifest);
          boolean permissions = false;
          for (var p : manifest.path("permissions")) if (!p.isEmpty()) permissions = true;
          // Conservative floor: command fences, executable invocations and tool/network/file
          // directions.
          String content = text.toString().toLowerCase(Locale.ROOT);
          boolean command =
              content.matches(
                  "(?s).*(?:```|~~~|`[^`]+`|\\$ |\\b(?:curl|wget|bash|powershell|cmd\\.exe|python|node|npm|pnpm|sudo|execute|run command|invoke|tool call)\\b|\u6267\u884c|\u8fd0\u884c\u547d\u4ee4|\u8c03\u7528\u5de5\u5177|\u8bfb\u53d6\u6587\u4ef6|\u53d1\u9001\u8bf7\u6c42).*");
          return permissions || command ? "r2" : "r1";
        }
      }
    } catch (auriga.common.error.AurigaException e) {
      throw e;
    } catch (Exception e) {
      throw invalid();
    }
  }

  private static void frontmatter(String skill, JsonNode manifest) {
    if (!skill.startsWith("---\n") && !skill.startsWith("---\r\n")) throw invalid();
    String normalized = skill.replace("\r\n", "\n");
    int end = normalized.indexOf("\n---\n", 4);
    if (end < 0) throw invalid();
    var options = new LoaderOptions();
    options.setAllowDuplicateKeys(false);
    options.setMaxAliasesForCollections(0);
    options.setNestingDepthLimit(8);
    options.setCodePointLimit(262144);
    var yaml = new Yaml(new SafeConstructor(options));
    String raw = normalized.substring(4, end);
    // Reject tags and aliases (including scalar aliases) at the parser event level.
    for (var event : yaml.parse(new StringReader(raw))) {
      if (event instanceof org.yaml.snakeyaml.events.AliasEvent) throw invalid();
      if (event instanceof org.yaml.snakeyaml.events.ScalarEvent s && s.getTag() != null)
        throw invalid();
      if (event instanceof org.yaml.snakeyaml.events.CollectionStartEvent c && c.getTag() != null)
        throw invalid();
    }
    Object loaded = yaml.load(raw);
    if (!(loaded instanceof Map<?, ?> map)) throw invalid();
    if (!Set.of(
                "name",
                "description",
                "whenToUse",
                "metadata",
                "disable-model-invocation",
                "user-invocable")
            .containsAll(map.keySet())
        || !manifest.path("name").asString().equals(map.get("name"))
        || !manifest.path("description").asString().equals(map.get("description"))) throw invalid();
    for (String key : List.of("disable-model-invocation", "user-invocable"))
      if (map.containsKey(key) && !(map.get(key) instanceof Boolean)) throw invalid();
    if (map.containsKey("whenToUse")
        && (!(map.get("whenToUse") instanceof String s) || s.length() > 2048)) throw invalid();
    if (map.containsKey("metadata") && JSON.writeValueAsBytes(map.get("metadata")).length > 8192)
      throw invalid();
  }
}
