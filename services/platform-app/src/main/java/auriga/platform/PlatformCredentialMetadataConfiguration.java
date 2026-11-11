package auriga.platform;

import auriga.collab.network.keys.NetworkKeyOperations;
import auriga.collab.network.keys.NetworkKeySettings;
import auriga.common.context.TenantContext;
import auriga.iam.api.CredentialMetadataSource;
import java.nio.file.*;
import java.time.Instant;
import java.util.*;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;
import org.springframework.core.env.Environment;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(prefix = "auriga.iam", name = "issuer")
@ConditionalOnExpression("${auriga.services.iam.enabled:true}")
public class PlatformCredentialMetadataConfiguration {
  @Bean
  CredentialMetadataSource platformCredentialMetadata(
      Environment env,
      ObjectProvider<NetworkKeyOperations> operations,
      ObjectProvider<NetworkKeySettings> settings) {
    return new Source(env, operations, settings);
  }

  static final class Source implements CredentialMetadataSource {
    private final Environment env;
    private final ObjectProvider<NetworkKeyOperations> operations;
    private final ObjectProvider<NetworkKeySettings> settings;

    Source(
        Environment env,
        ObjectProvider<NetworkKeyOperations> operations,
        ObjectProvider<NetworkKeySettings> settings) {
      this.env = env;
      this.operations = operations;
      this.settings = settings;
    }

    @Override
    public String name() {
      return "platform-keysets";
    }

    @Override
    public Snapshot snapshot() {
      var entries = new ArrayList<Entry>();
      var gaps = new ArrayList<Gap>();
      read(
          "network-keyset",
          () -> {
            var configured = settings.getIfAvailable();
            String tenant =
                configured == null
                    ? env.getProperty("auriga.collab.network.key-operations.tenant-id", "")
                    : configured.tenant().toString();
            if (!TenantContext.require().toString().equals(tenant)) {
              gaps.add(new Gap("network-keyset", "not_configured"));
              return;
            }
            var active = operations.getIfAvailable();
            JsonNode signed =
                active == null
                    ? file(env.getProperty("auriga.collab.network.key-operations.signed-file"))
                    : JsonMapper.builder().build().readTree(active.current().signedKeyset());
            if (signed == null) {
              gaps.add(new Gap("network-keyset", "not_configured"));
              return;
            }
            String realm =
                configured == null
                    ? env.getProperty("auriga.collab.network.key-operations.realm", "")
                    : configured.realm();
            if (realm.isEmpty() || !realm.equals(signed.path("keyset").path("realm").asString()))
              throw new IllegalArgumentException();
            network(signed, entries);
            String root = signed.path("root_auth").path("kid").asString("");
            if (!root.isEmpty() && TenantContext.require().equals(TenantContext.PLATFORM_TENANT)) {
              entries.add(
                  new Entry(
                      "network-root:" + root,
                      "keyset",
                      "network-root",
                      root,
                      null,
                      null,
                      "active"));
              gaps.add(new Gap("network-root", "validity_unavailable"));
            }
          },
          gaps);
      read(
          "identity-header",
          () -> {
            if (!TenantContext.require().equals(TenantContext.PLATFORM_TENANT)) {
              gaps.add(new Gap("identity-header", "not_configured"));
              return;
            }
            var keys = file(env.getProperty("auriga.credentials.identity-keyset-file"));
            if (keys == null) {
              gaps.add(new Gap("identity-header", "not_configured"));
              return;
            }
            for (var key : keys.path("keys")) {
              String kid = key.path("kid").asString("");
              if (kid.isEmpty()) throw new IllegalArgumentException();
              Instant issued = time(key, "issuedAt"), expires = time(key, "expiresAt");
              entries.add(
                  new Entry(
                      "identity:" + kid,
                      "keyset",
                      "identity-header",
                      kid,
                      issued,
                      expires,
                      state(key.path("state").asString(""))));
              if (issued == null || expires == null)
                gaps.add(new Gap("identity-header", "validity_unavailable"));
            }
          },
          gaps);
      read(
          "panel-manifest",
          () -> {
            if (!TenantContext.require().equals(TenantContext.PLATFORM_TENANT)) {
              gaps.add(new Gap("panel-manifest", "not_configured"));
              return;
            }
            var manifest = file(env.getProperty("auriga.credentials.panel-manifest-file"));
            if (manifest == null) {
              gaps.add(new Gap("panel-manifest", "not_configured"));
              return;
            }
            String kid = manifest.path("signature").path("kid").asString("");
            if (kid.isEmpty()) throw new IllegalArgumentException();
            entries.add(
                new Entry(
                    "panel-manifest",
                    "keyset",
                    "panel-manifest",
                    kid,
                    time(manifest, "issuedAt"),
                    time(manifest, "expiresAt"),
                    "active"));
            if (time(manifest, "issuedAt") == null || time(manifest, "expiresAt") == null)
              gaps.add(new Gap("panel-manifest", "validity_unavailable"));
            gaps.add(new Gap("panel-manifest", "key_validity_unavailable"));
          },
          gaps);
      return new Snapshot(entries, gaps.stream().distinct().toList());
    }

    private void read(String source, Runnable work, List<Gap> gaps) {
      try {
        work.run();
      } catch (RuntimeException e) {
        gaps.add(new Gap(source, "unavailable"));
      }
    }
  }

  static JsonNode file(String path) {
    if (path == null || path.isBlank()) return null;
    try {
      Path file = Path.of(path);
      if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS) || Files.size(file) > 1024 * 1024)
        throw new IllegalArgumentException();
      try (var input = Files.newInputStream(file, LinkOption.NOFOLLOW_LINKS)) {
        byte[] raw = input.readNBytes(1024 * 1024 + 1);
        if (raw.length > 1024 * 1024) throw new IllegalArgumentException();
        return JsonMapper.builder().build().readTree(raw);
      }
    } catch (java.io.IOException e) {
      throw new IllegalArgumentException();
    }
  }

  static Instant time(JsonNode node, String field) {
    return node.hasNonNull(field) ? Instant.parse(node.path(field).asString()) : null;
  }

  static String state(String value) {
    return switch (value.toLowerCase(Locale.ROOT)) {
      case "current", "active", "key_state_current" -> "active";
      case "next", "key_state_next" -> "next";
      case "retired", "retiring", "key_state_retiring" -> "retiring";
      case "revoked", "key_state_revoked" -> "revoked";
      default -> throw new IllegalArgumentException();
    };
  }

  static void network(JsonNode signed, List<CredentialMetadataSource.Entry> entries) {
    var keyset = signed.path("keyset");
    String version = keyset.path("keyset_version").asString("");
    if (version.isEmpty() || !keyset.path("keys").isArray()) throw new IllegalArgumentException();
    entries.add(
        new CredentialMetadataSource.Entry(
            "network-keyset",
            "keyset",
            "network-keyset",
            version,
            time(keyset, "issued_at"),
            time(keyset, "valid_until"),
            "active"));
    for (var key : keyset.path("keys")) {
      String kid = key.path("kid").asString("");
      if (kid.isEmpty()) throw new IllegalArgumentException();
      entries.add(
          new CredentialMetadataSource.Entry(
              "network:" + kid,
              "keyset",
              "network-keyset",
              version + ":" + kid,
              time(key, "not_before"),
              time(key, "not_after"),
              state(key.path("state").asString())));
    }
  }
}
