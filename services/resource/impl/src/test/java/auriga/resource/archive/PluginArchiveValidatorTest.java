package auriga.resource.archive;

import static auriga.resource.web.ResourceBody.JSON;
import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.resource.objectstore.*;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import tools.jackson.databind.node.ObjectNode;

class PluginArchiveValidatorTest {
  @TempDir Path root;
  final UUID tenant = UUID.randomUUID();

  ObjectNode pkg() {
    var pkg = JSON.createObjectNode().put("name", "@example/plugin").put("version", "1.0.0");
    pkg.putObject("dsh").putObject("bundle").put("patch", "./patch.js");
    return pkg;
  }

  String validate(Map<String, String> files) throws Exception {
    var parts =
        files.entrySet().stream()
            .map(
                e ->
                    new TarGzipArchiveValidatorTest.Part(
                        e.getKey(), false, e.getValue().getBytes(StandardCharsets.UTF_8)))
            .toArray(TarGzipArchiveValidatorTest.Part[]::new);
    byte[] archive = TarGzipArchiveValidatorTest.gzip(TarGzipArchiveValidatorTest.tar(parts));
    try (var store = new LocalObjectStore(root)) {
      var staged =
          store.putStaged(tenant, new ByteArrayInputStream(archive), ObjectStore.MAX_BYTES);
      var manifest = JSON.createObjectNode().put("version", "1.0.0");
      manifest
          .putObject("plugin")
          .put("packageName", "@example/plugin")
          .put("dshVersionRange", "*");
      return PluginArchiveValidator.validate(store, tenant, staged, manifest);
    }
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "scripts",
        "nestedScripts",
        "dependency",
        "optionalDependency",
        "missingBundled",
        "nestedDependency",
        "peer",
        "binding",
        "nestedBinding",
        "mixedCaseBinding",
        "gypfile",
        "nestedGypfile",
        "onlyBuiltDependencies",
        "allowBuilds",
        "nestedBuild",
        "missingPatch",
        "unsafePatch",
        "absentBundle",
        "reservedDependency"
      })
  void rejectsExecutableOrUnresolvedPackageMetadataAtEveryDepth(String scenario) throws Exception {
    var pkg = pkg();
    var nested = JSON.createObjectNode().put("name", "vendored").put("version", "1.0.0");
    var files = new LinkedHashMap<String, String>();
    files.put("package/patch.js", "export default {};");
    switch (scenario) {
      case "scripts" -> pkg.putObject("scripts").put("test", "node test.js");
      case "nestedScripts" -> nested.putObject("scripts").put("test", "node test.js");
      case "dependency" -> pkg.putObject("dependencies").put("remote", "*");
      case "optionalDependency" -> pkg.putObject("optionalDependencies").put("remote", "*");
      case "missingBundled" -> {
        pkg.putObject("dependencies").put("remote", "*");
        pkg.putArray("bundledDependencies").add("remote");
      }
      case "nestedDependency" -> nested.putObject("dependencies").put("remote", "*");
      case "peer" -> pkg.putObject("peerDependencies").put("other-host", "*");
      case "binding" -> files.put("package/binding.gyp", "{}");
      case "nestedBinding" -> files.put("package/node_modules/vendored/binding.gyp", "{}");
      case "mixedCaseBinding" -> files.put("package/node_modules/vendored/BiNdInG.GyP", "{}");
      case "gypfile" -> pkg.put("gypfile", true);
      case "nestedGypfile" -> nested.put("gypfile", true);
      case "onlyBuiltDependencies" ->
          pkg.putObject("pnpm").putArray("onlyBuiltDependencies").add("vendored");
      case "allowBuilds" -> pkg.putObject("pnpm").putObject("allowBuilds").put("vendored", false);
      case "nestedBuild" ->
          nested.putObject("pnpm").putArray("onlyBuiltDependencies").add("native");
      case "missingPatch" -> files.remove("package/patch.js");
      case "unsafePatch" ->
          ((ObjectNode) pkg.path("dsh").path("bundle")).put("patch", "../patch.js");
      case "absentBundle" -> pkg.remove("dsh");
      case "reservedDependency" -> {
        pkg.putObject("dependencies").put("node_modules", "*");
        pkg.put("bundledDependencies", true);
        files.put("package/node_modules/node_modules/package.json", "{}");
      }
      default -> throw new IllegalArgumentException(scenario);
    }
    files.put("package/package.json", pkg.toString());
    files.put("package/node_modules/vendored/package.json", nested.toString());
    var failure = assertThrows(AurigaException.class, () -> validate(files), scenario);
    String expectedRule =
        switch (scenario) {
          case "scripts", "nestedScripts" -> "lifecycle_scripts_disallowed";
          case "dependency",
              "optionalDependency",
              "missingBundled",
              "nestedDependency",
              "reservedDependency" ->
              "bundled_dependency_required";
          case "binding",
              "mixedCaseBinding",
              "nestedBinding",
              "gypfile",
              "nestedGypfile",
              "onlyBuiltDependencies",
              "allowBuilds",
              "nestedBuild" ->
              "native_build_disallowed";
          case "missingPatch", "absentBundle" -> "bundle_patch_missing";
          default -> null;
        };
    assertEquals(expectedRule, failure.params().get("rule"), scenario);
  }

  @Test
  void acceptsBundledDependenciesAndDshPeersWithoutExecutingCode() throws Exception {
    var pkg = pkg();
    pkg.putObject("dependencies").put("@example/dep", "1.0.0");
    pkg.putArray("bundleDependencies").add("@example/dep");
    pkg.putObject("peerDependencies").put("@deepseek-ai/dsh", "*").put("@deepseek-ai/dsh-sdk", "*");
    assertEquals(
        "r2",
        validate(
            Map.of(
                "package/package.json",
                pkg.toString(),
                "package/patch.js",
                "export default {};",
                "package/node_modules/@example/dep/package.json",
                "{\"description\":null,\"scripts\":null,\"dependencies\":null,\"optionalDependencies\":null,\"peerDependencies\":null}")));
  }
}
