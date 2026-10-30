package auriga.collab.network.keys;

import java.nio.file.Path;
import java.util.Map;
import java.util.UUID;
import org.springframework.boot.context.properties.bind.Bindable;
import org.springframework.boot.context.properties.bind.Binder;
import org.springframework.core.env.Environment;

/** Trusted installation values; contains paths and a public pin, never private key bytes. */
public record NetworkKeySettings(
    UUID tenant,
    String realm,
    String rootKid,
    Path rootPublicFile,
    String rootFingerprint,
    Path signedFile,
    Path announcementFile,
    Path stateFile,
    String minimumVersion,
    Map<String, String> privateFiles) {
  private static final String PREFIX = "auriga.collab.network.key-operations.";

  static NetworkKeySettings from(Environment env) {
    return new NetworkKeySettings(
        UUID.fromString(required(env, "tenant-id")),
        required(env, "realm"),
        required(env, "root-kid"),
        Path.of(required(env, "root-public-file")),
        required(env, "root-fingerprint"),
        Path.of(required(env, "signed-file")),
        Path.of(required(env, "announcement-file")),
        Path.of(env.getRequiredProperty("auriga.collab.network.key-state-file")),
        required(env, "minimum-version"),
        Binder.get(env)
            .bind(PREFIX + "private-files", Bindable.mapOf(String.class, String.class))
            .orElse(Map.of()));
  }

  public NetworkKeySettings {
    privateFiles = Map.copyOf(privateFiles);
  }

  RootKeyPublication.Release mountedRelease() {
    return new RootKeyPublication.Release(
        ProtectedKeyFiles.readText(signedFile), ProtectedKeyFiles.readText(announcementFile));
  }

  private static String required(Environment env, String name) {
    String value = env.getRequiredProperty(PREFIX + name);
    if (value.isBlank()) throw ProtectedKeyFiles.invalid();
    return value;
  }
}
