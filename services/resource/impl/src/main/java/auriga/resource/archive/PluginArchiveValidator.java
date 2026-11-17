package auriga.resource.archive;

import static auriga.resource.web.ResourceBody.*;

import auriga.resource.objectstore.ObjectStore;
import java.util.*;
import tools.jackson.databind.JsonNode;

/** Validates pack output without executing npm, extracting files or trusting package metadata. */
public final class PluginArchiveValidator {
  private static final Set<String> LIFECYCLES =
      Set.of(
          "preinstall",
          "install",
          "postinstall",
          "prepublish",
          "prepublishOnly",
          "prepare",
          "preprepare",
          "postprepare",
          "prepack",
          "postpack",
          "publish",
          "postpublish",
          "preuninstall",
          "uninstall",
          "postuninstall");

  private PluginArchiveValidator() {}

  public static String validate(
      ObjectStore store, UUID tenant, ObjectStore.Staged stage, JsonNode manifest) {
    var result = new TarGzipArchiveValidator(store).validatePlugin(tenant, stage);
    if (result.packageManifest() == null
        || result.inventory().stream().noneMatch(e -> e.path().equals("package/package.json")))
      throw invalid();
    for (var entry : result.inventory()) {
      if (!entry.path().startsWith("package/") || entry.path().equals("package/binding.gyp"))
        throw invalid();
    }
    var pkg = parse(result.packageManifest(), 1048576, null, Set.of("name", "version"));
    if (!text(manifest.path("plugin"), "packageName", 214).equals(text(pkg, "name", 214))
        || !text(manifest, "version", 128).equals(text(pkg, "version", 128))) throw invalid();
    if (pkg.has("engines")) {
      if (!pkg.path("engines").isObject()) throw invalid();
      if (pkg.path("engines").has("dsh")
          && !text(manifest.path("plugin"), "dshVersionRange", 128)
              .equals(text(pkg.path("engines"), "dsh", 128))) throw invalid();
    }
    if (pkg.has("scripts")) {
      if (!pkg.path("scripts").isObject()) throw invalid();
      for (String name : pkg.path("scripts").propertyNames())
        if (LIFECYCLES.contains(name)) throw invalid();
    }
    return "r2";
  }
}
