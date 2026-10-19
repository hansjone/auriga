package auriga.platform;

import auriga.iam.EmergencyAccounts;
import auriga.iam.IamStore;
import auriga.iam.ProviderManager;
import auriga.iam.SigningKeys;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.FileSystems;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.AclEntry;
import java.nio.file.attribute.AclEntryPermission;
import java.nio.file.attribute.AclEntryType;
import java.nio.file.attribute.AclFileAttributeView;
import java.nio.file.attribute.PosixFilePermission;
import java.nio.file.attribute.PosixFilePermissions;
import java.nio.file.attribute.UserPrincipal;
import java.time.Duration;
import java.time.Instant;
import java.util.EnumSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.core.annotation.Order;
import org.springframework.core.env.Environment;
import org.springframework.stereotype.Component;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/**
 * One-shot M1 seed: signing key, emergency account, default team-account provider. Credentials are
 * written only under the configured enrollment file (never logged).
 */
@Component
@Order(100)
@ConditionalOnExpression("${auriga.platform.combined:false} && ${auriga.platform.seed:false}")
public class DevBootstrap implements ApplicationRunner {
  private static final Logger log = LoggerFactory.getLogger(DevBootstrap.class);
  private static final JsonMapper JSON = JsonMapper.builder().build();
  static final UUID DEFAULT_TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001");

  private final SigningKeys keys;
  private final EmergencyAccounts emergency;
  private final ProviderManager providers;
  private final IamStore store;
  private final Environment env;

  public DevBootstrap(
      SigningKeys keys,
      EmergencyAccounts emergency,
      ProviderManager providers,
      IamStore store,
      Environment env) {
    this.keys = keys;
    this.emergency = emergency;
    this.providers = providers;
    this.store = store;
    this.env = env;
  }

  @Override
  public void run(ApplicationArguments args) throws Exception {
    boolean hasSigningKey =
        store.isolated(
            SigningKeys.PLATFORM,
            () ->
                store.all("signing_key").stream()
                    .anyMatch(k -> "current".equals(k.path("state").asString())));
    if (!hasSigningKey) {
      Duration prop =
          Duration.ofSeconds(
              env.getProperty("auriga.iam.jwks-propagation-seconds", Long.class, 30L));
      String kid = keys.publishNext();
      // Backdate publishedAt so activation succeeds without advancing the shared Clock.
      store.isolated(
          SigningKeys.PLATFORM,
          () -> {
            ObjectNode row = store.get("signing_key", kid, true);
            row.put(
                "publishedAt",
                Instant.parse(row.path("publishedAt").asString())
                    .minus(prop)
                    .minusSeconds(1)
                    .toString());
            store.save("signing_key", row);
            return null;
          });
      keys.activate(kid);
      log.info("IAM signing key activated");
    } else {
      log.info("IAM signing key already current; skipping publish");
    }

    Path enrollmentFile =
        Path.of(
                env.getProperty(
                    "auriga.platform.enrollment-file", ".dev/emergency-enrollment.json"))
            .toAbsolutePath()
            .normalize();
    if (!Files.exists(enrollmentFile)) {
      EmergencyAccounts.Enrollment enrollment = emergency.install(DEFAULT_TENANT);
      writeEnrollment(enrollmentFile, enrollment);
      log.info(
          "Emergency account enrolled; credentials written to {} (not logged)", enrollmentFile);
    } else {
      log.info("Emergency enrollment file already present; skipping emergency install");
    }

    store.tenant(
        DEFAULT_TENANT,
        () -> {
          boolean hasTeam =
              store.all("idp_provider").stream()
                  .anyMatch(p -> "team-account".equals(p.path("connectorType").asString()));
          if (!hasTeam) {
            String rpId = env.getProperty("auriga.platform.team.rp-id", "portal.example.com");
            String origin =
                env.getProperty("auriga.platform.team.origin", "https://portal.example.com");
            Map<String, Object> name = new LinkedHashMap<>();
            name.put("en", "Team account");
            name.put("zh-CN", "团队账号");
            Map<String, Object> display = new LinkedHashMap<>();
            display.put("name", name);
            display.put("order", 10);
            Map<String, Object> config = new LinkedHashMap<>();
            config.put("rpId", rpId);
            config.put("origins", List.of(origin));
            config.put("primaryStep", "form");
            Map<String, Object> admission = new LinkedHashMap<>();
            admission.put("mode", "invite_only");
            admission.put("mfa", "new_device");
            Map<String, Object> body = new LinkedHashMap<>();
            body.put("connectorType", "team-account");
            body.put("enabled", true);
            body.put("display", display);
            body.put("config", config);
            body.put("admission", admission);
            providers.create(JSON.valueToTree(body));
            log.info("Default team-account provider created (rpId={})", rpId);
          }
          return null;
        });
  }

  static void writeEnrollment(Path file, EmergencyAccounts.Enrollment enrollment) throws Exception {
    Path parent = file.getParent();
    if (parent != null) {
      Files.createDirectories(parent);
    }
    ObjectNode body = JSON.createObjectNode();
    body.put("accountId", enrollment.accountId().toString());
    body.put("providerId", enrollment.providerId().toString());
    body.put("username", enrollment.username());
    body.put("password", enrollment.password());
    body.put("totpSecret", enrollment.totpSecret());
    String content = body.toPrettyString() + "\n";

    Set<String> views = FileSystems.getDefault().supportedFileAttributeViews();
    if (views.contains("posix")) {
      var attrs =
          PosixFilePermissions.asFileAttribute(
              EnumSet.of(PosixFilePermission.OWNER_READ, PosixFilePermission.OWNER_WRITE));
      try (var out = Files.newOutputStream(Files.createFile(file, attrs))) {
        out.write(content.getBytes(StandardCharsets.UTF_8));
      }
      return;
    }
    if (views.contains("acl")) {
      Files.createFile(file);
      restrictWindowsAcl(file);
      Files.writeString(file, content, StandardCharsets.UTF_8);
      return;
    }
    throw new IOException(
        "Cannot restrict enrollment file permissions on this filesystem (need posix or acl)");
  }

  private static void restrictWindowsAcl(Path file) throws IOException {
    AclFileAttributeView view = Files.getFileAttributeView(file, AclFileAttributeView.class);
    if (view == null) {
      throw new IOException("AclFileAttributeView unavailable for " + file);
    }
    UserPrincipal owner = view.getOwner();
    if (owner == null) {
      owner =
          FileSystems.getDefault()
              .getUserPrincipalLookupService()
              .lookupPrincipalByName(System.getProperty("user.name"));
    }
    AclEntry entry =
        AclEntry.newBuilder()
            .setType(AclEntryType.ALLOW)
            .setPrincipal(owner)
            .setPermissions(
                EnumSet.of(
                    AclEntryPermission.READ_DATA,
                    AclEntryPermission.WRITE_DATA,
                    AclEntryPermission.APPEND_DATA,
                    AclEntryPermission.READ_ATTRIBUTES,
                    AclEntryPermission.WRITE_ATTRIBUTES,
                    AclEntryPermission.READ_NAMED_ATTRS,
                    AclEntryPermission.WRITE_NAMED_ATTRS,
                    AclEntryPermission.READ_ACL,
                    AclEntryPermission.SYNCHRONIZE,
                    AclEntryPermission.DELETE))
            .build();
    view.setAcl(List.of(entry));
  }
}
