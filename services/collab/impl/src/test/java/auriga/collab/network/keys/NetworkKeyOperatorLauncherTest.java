package auriga.collab.network.keys;

import static auriga.collab.network.keys.NetworkKeyConfigurationTest.protectedFile;
import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfSystemProperty;
import org.junit.jupiter.api.io.TempDir;

/**
 * Explicit packaged-artifact check; regular unit compilation does not assume a prebuilt app jar.
 */
@EnabledIfSystemProperty(named = "auriga.operator.jar", matches = ".+")
class NetworkKeyOperatorLauncherTest {
  @TempDir(factory = NetworkKeyConfigurationTest.WorkspaceTempDirectory.class)
  Path directory;

  @Test
  void executableJarRunsOnlyOperatorAndRejectsUnknownPlanOrMismatchedPair() throws Exception {
    Path jar = Path.of(System.getProperty("auriga.operator.jar")).toAbsolutePath();
    assertTrue(Files.isRegularFile(jar));
    var m = new KeyTestMaterial();
    var release = m.release(m.payload(7, Instant.now().truncatedTo(ChronoUnit.SECONDS)));
    Path root = protectedFile(directory.resolve("root.der"), m.root.getPublic().getEncoded());
    Path signed =
        protectedFile(
            directory.resolve("signed.json"),
            release.signedKeyset().getBytes(StandardCharsets.UTF_8));
    Path ann =
        protectedFile(
            directory.resolve("announcement.json"),
            release.announcement().getBytes(StandardCharsets.UTF_8));
    String prefix = "auriga.collab.network.key-operations.";
    Path config =
        protectedFile(
            directory.resolve("config.properties"),
            (prefix
                    + "tenant-id=00000000-0000-0000-0000-000000000001\n"
                    + prefix
                    + "realm=2\n"
                    + prefix
                    + "root-kid=root\n"
                    + prefix
                    + "root-fingerprint="
                    + RootKeyPublication.fingerprint(m.root.getPublic())
                    + "\n"
                    + prefix
                    + "minimum-version=1\n")
                .getBytes(StandardCharsets.UTF_8));
    var plan =
        KeyTestMaterial.JSON.valueToTree(
            Map.of(
                "configFile",
                config.toString(),
                "rootFile",
                root.toString(),
                "expectedSignedFile",
                "",
                "expectedAnnouncementFile",
                "",
                "signedFile",
                signed.toString(),
                "announcementFile",
                ann.toString(),
                "stateFile",
                directory.resolve("unused-state.json").toString(),
                "privateFiles",
                Map.of(),
                "expectedHistoryFile",
                "",
                "expectedStateSha256",
                ""));
    Path path =
        protectedFile(
            directory.resolve("plan.json"),
            NetworkJson.canonical(plan).getBytes(StandardCharsets.UTF_8));
    assertEquals(0, launch(jar, path, "valid"));
    ((tools.jackson.databind.node.ObjectNode) plan).put("loader.main", "untrusted");
    protectedFile(path, NetworkJson.canonical(plan).getBytes(StandardCharsets.UTF_8));
    assertEquals(1, launch(jar, path, "unknown-field"));
    ((tools.jackson.databind.node.ObjectNode) plan).remove("loader.main");
    protectedFile(path, NetworkJson.canonical(plan).getBytes(StandardCharsets.UTF_8));
    var other = m.release(m.payload(8, Instant.now().truncatedTo(ChronoUnit.SECONDS)));
    protectedFile(ann, other.announcement().getBytes(StandardCharsets.UTF_8));
    assertEquals(1, launch(jar, path, "wrong-pair"));
    assertFalse(
        Files.exists(directory.resolve("unused-state.json")),
        "verification cannot install a state");
  }

  int launch(Path jar, Path plan, String label) throws Exception {
    return launch(directory, jar, plan, label, "verify");
  }

  static int launch(Path directory, Path jar, Path plan, String label, String action)
      throws Exception {
    Path loader = protectedFile(directory.resolve(label + "-loader.properties"), new byte[0]);
    String java =
        Path.of(
                System.getProperty("java.home"),
                "bin",
                System.getProperty("os.name").startsWith("Windows") ? "java.exe" : "java")
            .toString();
    Path output = directory.resolve(label + ".log");
    var builder =
        new ProcessBuilder(
            java,
            "-Dloader.main=auriga.collab.network.keys.NetworkKeyOperator",
            "-Dloader.config.location=" + loader.toUri(),
            "-cp",
            jar.toString(),
            "org.springframework.boot.loader.launch.PropertiesLauncher",
            action,
            plan.toString());
    String systemRoot = builder.environment().get("SystemRoot");
    builder.environment().clear();
    if (systemRoot != null) builder.environment().put("SystemRoot", systemRoot);
    builder.redirectErrorStream(true).redirectOutput(output.toFile());
    Process child = builder.start();
    try {
      assertTrue(
          child.waitFor(20, TimeUnit.SECONDS),
          "operator must exit rather than start a Spring server");
      String text = Files.readString(output);
      if (child.exitValue() == 0) {
        assertTrue(NetworkJson.parse(text.strip()).isObject(), text);
        if (action.equals("verify") || action.equals("preflight"))
          assertEquals("{}", text.strip(), text);
      } else
        assertEquals(
            "Network key operator rejected the operation; no automatic repair", text.strip(), text);
      assertFalse(text.contains("Flyway"));
      assertFalse(text.contains("Tomcat"));
      return child.exitValue();
    } finally {
      if (child.isAlive()) child.destroyForcibly();
    }
  }
}
