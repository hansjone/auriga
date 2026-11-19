package auriga.iam;

import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import org.springframework.core.env.Environment;

/** Validate the complete installation before any publisher can enter its lifecycle. */
final class ServiceTokenExportIsolation {
  private ServiceTokenExportIsolation() {}

  static void verify(Environment env) {
    String edge = "auriga.iam.edge-token-export.";
    String gateway = "auriga.iam.ai-gateway-token-export.";
    String agent = "auriga.iam.agent-token-export.";
    boolean edgeEnabled = env.getProperty(edge + "enabled", Boolean.class, false);
    boolean gatewayEnabled = env.getProperty(gateway + "enabled", Boolean.class, false);
    if (gatewayEnabled) {
      int gatewayGroup = env.getRequiredProperty(gateway + "reader-group", Integer.class);
      if (gatewayGroup < 0
          || (edgeEnabled
              && gatewayGroup == env.getProperty(edge + "reader-group", Integer.class, 65532)))
        throw invalid();
    }
    var exports = new ArrayList<List<Path>>();
    for (String prefix : List.of(edge, gateway, agent)) {
      if (!env.getProperty(prefix + "enabled", Boolean.class, false)) continue;
      exports.add(
          List.of(
              path(env, prefix + "token-file"), path(env, prefix + "installation-secret-file")));
    }
    for (int i = 0; i < exports.size(); i++)
      for (int j = i + 1; j < exports.size(); j++)
        for (Path a : exports.get(i))
          for (Path b : exports.get(j))
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
