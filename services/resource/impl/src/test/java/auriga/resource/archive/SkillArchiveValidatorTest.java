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
    var out = new ByteArrayOutputStream();
    try (var gz = new GZIPOutputStream(out);
        var tar = new TarArchiveOutputStream(gz)) {
      for (var entry : files.entrySet()) {
        byte[] data = entry.getValue().getBytes(StandardCharsets.UTF_8);
        var header = new TarArchiveEntry(entry.getKey());
        header.setSize(data.length);
        header.setMode(0644);
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
  void acceptsScriptsAndRootDependencyManifestAndElevatesRiskToR2() throws Exception {
    var files = new LinkedHashMap<String, String>();
    files.put("SKILL.md", SKILL_MD);
    files.put("requirements.txt", "pydantic>=2.0\n");
    files.put("scripts/run.py", "print('hello')\n");
    files.put("scripts/helper.sh", "echo ok\n");
    files.put("references/config.toml", "mode = 'safe'\n");
    byte[] bytes = tarGz(files);
    assertEquals(
        "r2",
        SkillArchiveValidator.validate(new ByteArrayInputStream(bytes), bytes.length, manifest()));
  }

  @Test
  void rejectsDisallowedPathsAndBinaryExtensions() throws Exception {
    for (String bad :
        new String[] {"assets/payload.sh", "references/run.py", "scripts/bin.exe", "other/a.txt"}) {
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
  }
}
