package auriga.iam;

import java.nio.file.Path;
import java.util.List;
import org.springframework.core.env.Environment;

/** Validate the complete installation before either publisher can enter its lifecycle. */
final class ServiceTokenExportIsolation {
  private ServiceTokenExportIsolation() {}

  static void verify(Environment env) {
    verifyProjectors(env);
    String edge = "auriga.iam.edge-token-export.";
    String gateway = "auriga.iam.ai-gateway-token-export.";
    if (!env.getProperty(gateway + "enabled", Boolean.class, false)) return;
    int gatewayGroup = env.getRequiredProperty(gateway + "reader-group", Integer.class);
    if (gatewayGroup < 0) throw invalid();
    if (!env.getProperty(edge + "enabled", Boolean.class, false)) return;
    if (gatewayGroup == env.getProperty(edge + "reader-group", Integer.class, 65532))
      throw invalid();
    var edgePaths =
        List.of(path(env, edge + "token-file"), path(env, edge + "installation-secret-file"));
    var gatewayPaths =
        List.of(path(env, gateway + "token-file"), path(env, gateway + "installation-secret-file"));
    for (Path a : edgePaths)
      for (Path b : gatewayPaths)
        if (a.getParent().startsWith(b.getParent()) || b.getParent().startsWith(a.getParent()))
          throw invalid();
  }

  private static void verifyProjectors(Environment env) {
    var prefixes = new java.util.ArrayList<String>();
    for (String component : List.of("network", "edge", "broker")) {
      String prefix = "auriga.iam.msgnet-projector-token-export." + component + ".";
      if (env.getProperty(prefix + "enabled", Boolean.class, false)) {
        if (env.getRequiredProperty(prefix + "reader-group", Integer.class) < 0) throw invalid();
        prefixes.add(prefix);
      }
    }
    if (prefixes.isEmpty()) return;
    for (String existing :
        List.of("auriga.iam.edge-token-export.", "auriga.iam.ai-gateway-token-export."))
      if (env.getProperty(existing + "enabled", Boolean.class, false)) prefixes.add(existing);
    for (int i = 0; i < prefixes.size(); i++)
      for (int j = i + 1; j < prefixes.size(); j++)
        for (String a : List.of("token-file", "installation-secret-file"))
          for (String b : List.of("token-file", "installation-secret-file")) {
            Path left = path(env, prefixes.get(i) + a).getParent();
            Path right = path(env, prefixes.get(j) + b).getParent();
            if (left.startsWith(right) || right.startsWith(left)) throw invalid();
          }
  }

  private static Path path(Environment env, String property) {
    Path path = Path.of(env.getRequiredProperty(property));
    if (!path.isAbsolute() || !path.equals(path.normalize()) || path.getParent() == null)
      throw invalid();
    return path;
  }

  private static IllegalStateException invalid() {
    return new IllegalStateException(
        "Service token exports require disjoint private directories and reader groups");
  }
}
