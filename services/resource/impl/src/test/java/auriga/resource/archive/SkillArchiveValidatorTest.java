package auriga.resource.archive;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.zip.GZIPOutputStream;
import org.apache.commons.compress.archivers.tar.TarArchiveEntry;
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.json.JsonMapper;

class SkillArchiveValidatorTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private static final String SKILL_MD =
      "---\nname: example-skill\ndescription: Example skill\n---\nUse this guide.\n";

  private static byte[] tarGz(Map<String, String> files) throws IOException {
    return tarGz(files, 0644, false);
  }

  private static byte[] tarGz(
      Map<String, String> files, int fileMode, boolean includeScriptsDirEntry) throws IOException {
    var out = new ByteArrayOutputStream();
    try (var gz = new GZIPOutputStream(out);
        var tar = new TarArchiveOutputStream(gz)) {
      if (includeScriptsDirEntry) {
        var dir = new TarArchiveEntry("scripts/");
        dir.setMode(0755);
        tar.putArchiveEntry(dir);
        tar.closeArchiveEntry();
      }
      for (var entry : files.entrySet()) {
        byte[] data = entry.getValue().getBytes(StandardCharsets.UTF_8);
        var header = new TarArchiveEntry(entry.getKey());
        header.setSize(data.length);
        header.setMode(fileMode);
        tar.putArchiveEntry(header);
        tar.write(data);
        tar.closeArchiveEntry();
      }
    }
    return out.toByteArray();
  }

  private static tools.jackson.databind.JsonNode manifest() {
    var root = JSON.createObjectNode();
    root.put("name", "example-skill");
    root.put("description", "Example skill");
    var perms = root.putObject("permissions");
    perms.putArray("tools");
    perms.putArray("outboundHosts");
    perms.putArray("dataAccess");
    return root;
  }

  @Test
  void acceptsPlainTextSkillAsR1() throws Exception {
    var files = new LinkedHashMap<String, String>();
    files.put("SKILL.md", SKILL_MD);
    files.put("references/guide.md", "Simple documentation without commands.");
    byte[] bytes = tarGz(files);
    assertEquals(
        "r1",
        SkillArchiveValidator.validate(new ByteArrayInputStream(bytes), bytes.length, manifest()));
  }

  @Test
  void acceptsScriptsOnlyAndElevatesRiskToR2() throws Exception {
    var files = new LinkedHashMap<String, String>();
    files.put("SKILL.md", SKILL_MD);
    files.put("scripts/helper.txt", "plain helper text without commands\n");
    byte[] bytes = tarGz(files, 0644, true);
    assertEquals(
        "r2",
        SkillArchiveValidator.validate(new ByteArrayInputStream(bytes), bytes.length, manifest()));
  }

  @Test
  void acceptsRootDependencyManifestOnlyAndElevatesRiskToR2() throws Exception {
    var files = new LinkedHashMap<String, String>();
    files.put("SKILL.md", SKILL_MD);
    files.put("requirements.txt", "pydantic>=2.0\n");
    byte[] bytes = tarGz(files);
    assertEquals(
        "r2",
        SkillArchiveValidator.validate(new ByteArrayInputStream(bytes), bytes.length, manifest()));
  }

  @Test
  void rejectsDisallowedPathsMixedCaseDirsAndSpecialModeBits() throws Exception {
    for (String bad :
        new String[] {
          "assets/payload.sh",
          "references/run.py",
          "Scripts/run.py",
          "References/guide.md",
          "scripts/bin.exe",
          "other/a.txt"
        }) {
      var files = new LinkedHashMap<String, String>();
      files.put("SKILL.md", SKILL_MD);
      files.put(bad, "content\n");
      byte[] bytes = tarGz(files);
      assertThrows(
          AurigaException.class,
          () ->
              SkillArchiveValidator.validate(
                  new ByteArrayInputStream(bytes), bytes.length, manifest()),
          "expected rejection for " + bad);
    }
    var setuidFiles = new LinkedHashMap<String, String>();
    setuidFiles.put("SKILL.md", SKILL_MD);
    byte[] setuidBytes = tarGz(setuidFiles, 04644, false);
    assertThrows(
        AurigaException.class,
        () ->
            SkillArchiveValidator.validate(
                new ByteArrayInputStream(setuidBytes), setuidBytes.length, manifest()));
    var collisionFiles = new LinkedHashMap<String, String>();
    collisionFiles.put("SKILL.md", SKILL_MD);
    collisionFiles.put("scripts/\u0398.txt", "a\n");
    collisionFiles.put("scripts/\u03f4.txt", "b\n");
    byte[] collisionBytes = tarGz(collisionFiles);
    assertThrows(
        AurigaException.class,
        () ->
            SkillArchiveValidator.validate(
                new ByteArrayInputStream(collisionBytes), collisionBytes.length, manifest()));
  }
}
