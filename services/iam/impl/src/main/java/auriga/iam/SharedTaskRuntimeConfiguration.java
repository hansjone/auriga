package auriga.iam;

import auriga.iam.api.CredentialInstallationScope;
import auriga.iam.api.PermissionResolver;
import auriga.workspace.api.SharedDshSessions;
import java.nio.file.*;
import java.security.KeyFactory;
import java.security.spec.PKCS8EncodedKeySpec;
import java.time.Clock;
import java.util.*;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

/** Explicit shared runtime configuration; an enabled deployment requires all live dependencies. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(name = "auriga.iam.shared-tasks.enabled", havingValue = "true")
public class SharedTaskRuntimeConfiguration {
  @Bean
  SharedTaskTokens sharedTaskTokens(Environment environment) {
    Map<String, SharedTaskTokens.Key> keys = new HashMap<>();
    for (String purpose : List.of("enterprise", "model", "message")) {
      String prefix = "auriga.iam.shared-tasks.signing-keys." + purpose;
      try {
        var path = Path.of(environment.getRequiredProperty(prefix + ".private-key-file"));
        if (!path.isAbsolute()) throw new IllegalArgumentException();
        byte[] content;
        try (var input = Files.newInputStream(path, LinkOption.NOFOLLOW_LINKS)) {
          content = input.readNBytes(8193);
        }
        String pem = new String(content, java.nio.charset.StandardCharsets.US_ASCII).trim();
        if (content.length > 8192
            || !pem.startsWith("-----BEGIN PRIVATE KEY-----")
            || !pem.endsWith("-----END PRIVATE KEY-----")) throw new IllegalArgumentException();
        String encoded =
            pem.substring(
                "-----BEGIN PRIVATE KEY-----".length(),
                pem.length() - "-----END PRIVATE KEY-----".length());
        var key =
            KeyFactory.getInstance("Ed25519")
                .generatePrivate(
                    new PKCS8EncodedKeySpec(
                        Base64.getDecoder().decode(encoded.replaceAll("\\s", ""))));
        keys.put(
            "auriga.shared." + purpose,
            new SharedTaskTokens.Key(environment.getRequiredProperty(prefix + ".kid"), key));
      } catch (java.io.IOException
          | java.security.GeneralSecurityException
          | RuntimeException error) {
        throw new IllegalArgumentException("Invalid shared task signing configuration");
      }
    }
    return new SharedTaskTokens(keys);
  }

  @Bean
  SharedTasks sharedTasks(
      IamStore store,
      SessionManager sessions,
      UacSessions uac,
      PermissionResolver permissions,
      SharedDshSessions ownership,
      SharedTaskTokens tokens,
      Clock clock,
      Environment environment) {
    return new SharedTasks(
        store,
        sessions,
        uac,
        permissions,
        ownership,
        tokens,
        clock,
        () ->
            environment.getProperty(
                "auriga.iam.shared-tasks.allow-raw-agent-token", Boolean.class, true));
  }

  @Bean
  IamGrpcSharedTasks iamGrpcSharedTasks(
      SharedTasks tasks, CredentialInstallationScope installation, IamGrpcSessions transportGuard) {
    return new IamGrpcSharedTasks(tasks, installation);
  }
}
