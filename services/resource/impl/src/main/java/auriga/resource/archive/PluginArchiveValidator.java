package auriga.resource.archive;

import static auriga.resource.web.ResourceBody.*;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.resource.app.CatalogManifest;
import auriga.resource.objectstore.ObjectStore;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Validates signed pack bytes without extracting or running package-manager code. */
public final class PluginArchiveValidator {
  private static final String PACKAGE_NAME = "(?:@[a-z0-9-][a-z0-9._-]*[/])?[a-z0-9-][a-z0-9._-]*";

  private PluginArchiveValidator() {}

  public static String validate(
      ObjectStore store, UUID tenant, ObjectStore.Staged stage, JsonNode manifest) {
    var result = new TarGzipArchiveValidator(store).validatePlugin(tenant, stage);
    var files = new HashSet<String>();
    for (var entry : result.inventory()) {
      if (!entry.path().startsWith("package/")) throw invalid();
      if (entry.path().toLowerCase(Locale.ROOT).endsWith("/binding.gyp"))
        throw invalidRule("native_build_disallowed");
      files.add(entry.path());
    }
    if (result.packageManifest() == null || !files.contains("package/package.json"))
      throw invalid();
    var pkg = metadata(result.packageManifest(), Set.of("name", "version"));
    String name = text(pkg, "name", 214);
    if (!validPackageName(name)
        || !text(manifest.path("plugin"), "packageName", 214).equals(name)
        || !text(manifest, "version", 128).equals(text(pkg, "version", 128))) throw invalid();
    if (pkg.has("engines")) {
      if (!pkg.path("engines").isObject()) throw invalid();
      if (pkg.path("engines").has("dsh")
          && !text(manifest.path("plugin"), "dshVersionRange", 128)
              .equals(text(pkg.path("engines"), "dsh", 128))) throw invalid();
    }
    var bundle = pkg.path("dsh").path("bundle");
    if (!bundle.hasNonNull("patch")) throw invalidRule("bundle_patch_missing");
    String patch = text(bundle, "patch", 240);
    if (patch.startsWith("./")) patch = patch.substring(2);
    CatalogManifest.path(patch);
    if (!files.contains("package/" + patch)) throw invalidRule("bundle_patch_missing");
    for (var entry : result.packageManifests().entrySet())
      validateMetadata(entry.getKey(), metadata(entry.getValue(), Set.of()), files);
    return "r2";
  }

  private static ObjectNode metadata(byte[] bytes, Set<String> required) {
    try {
      // Optional npm metadata can contain null-valued properties.
      return parse(
          bytes, 1048576, null, required, Set.copyOf(JSON.readTree(bytes).propertyNames()));
    } catch (RuntimeException failure) {
      throw invalid();
    }
  }

  private static AurigaException invalidRule(String rule) {
    return AurigaException.of(CommonErrors.VALIDATION_FAILED, "rule", rule);
  }

  private static boolean validPackageName(String name) {
    return name.matches(PACKAGE_NAME) && !Set.of("node_modules", "favicon.ico").contains(name);
  }

  private static void validateMetadata(String path, JsonNode pkg, Set<String> files) {
    if (!pkg.isObject()) throw invalid();
    if (pkg.has("gypfile")) throw invalidRule("native_build_disallowed");
    if (pkg.hasNonNull("scripts")
        && (!pkg.path("scripts").isObject() || !pkg.path("scripts").isEmpty()))
      throw invalidRule("lifecycle_scripts_disallowed");
    if (pkg.hasNonNull("pnpm")) {
      var pnpm = pkg.path("pnpm");
      if (!pnpm.isObject()) throw invalid();
      for (String declaration :
          List.of(
              "onlyBuiltDependencies",
              "allowBuilds",
              "onlyBuiltDependenciesFile",
              "dangerouslyAllowAllBuilds"))
        if (pnpm.has(declaration)) throw invalidRule("native_build_disallowed");
    }
    String base = path.substring(0, path.length() - "package.json".length());
    var bundled =
        pkg.has("bundledDependencies")
            ? pkg.path("bundledDependencies")
            : pkg.path("bundleDependencies");
    for (String field : List.of("dependencies", "optionalDependencies")) {
      var deps = pkg.path(field);
      if (pkg.hasNonNull(field) && !deps.isObject()) throw invalid();
      for (String dep : deps.propertyNames()) {
        boolean included = bundled.isBoolean() && bundled.asBoolean();
        if (bundled.isArray())
          for (var item : bundled) included |= item.isString() && item.asString().equals(dep);
        if (!validPackageName(dep)
            || !included
            || !files.contains(base + "node_modules/" + dep + "/package.json"))
          throw invalidRule("bundled_dependency_required");
      }
    }
    var peers = pkg.path("peerDependencies");
    if (pkg.hasNonNull("peerDependencies") && !peers.isObject()) throw invalid();
    for (String peer : peers.propertyNames())
      if (!peer.matches("@deepseek-ai/dsh(?:$|-.*)")) throw invalid();
  }
}
