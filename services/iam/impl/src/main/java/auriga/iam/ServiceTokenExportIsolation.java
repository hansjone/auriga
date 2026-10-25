package auriga.iam;

import java.nio.file.Path;
import java.util.List;
import org.springframework.core.env.Environment;

/** Validate the complete installation before either publisher can enter its lifecycle. */
final class ServiceTokenExportIsolation {
  private ServiceTokenExportIsolation() {}

  static void verify(Environment env) {
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
