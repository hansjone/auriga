package auriga.workspace.config;

import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.workspace.app.SharedDshEntryTransport;
import auriga.workspace.app.SharedDshPlacements;
import auriga.workspace.app.SharedDshTickets;
import auriga.workspace.persistence.WorkspaceStore;
import io.nats.client.Connection;
import java.net.Proxy;
import java.net.ProxySelector;
import java.net.SocketAddress;
import java.net.URI;
import java.net.http.HttpClient;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.security.KeyFactory;
import java.security.spec.PKCS8EncodedKeySpec;
import java.time.Clock;
import java.time.Duration;
import java.util.Base64;
import java.util.List;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
@ConditionalOnProperty(name = "auriga.workspace.shared-dsh.enabled", havingValue = "true")
public class WorkspaceSharedDshEntryConfiguration {
  @Bean
  @ConditionalOnMissingBean(SharedDshTickets.SigningKey.class)
  SharedDshTickets.SigningKey sharedDshTicketSigningKey(Environment env) throws Exception {
    return loadSigningKey(
        Path.of(env.getRequiredProperty("auriga.workspace.shared-dsh.ticket-private-key-file")),
        env.getRequiredProperty("auriga.workspace.shared-dsh.ticket-key-id"));
  }

  static SharedDshTickets.SigningKey loadSigningKey(Path path, String kid) throws Exception {
    if (!path.isAbsolute()
        || !Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS)
        || kid.isBlank()
        || kid.length() > 128)
      throw new IllegalArgumentException(
          "Shared ticket signing requires a fixed regular private key and key ID");
    byte[] bytes;
    try (var input =
        Files.newInputStream(path, StandardOpenOption.READ, LinkOption.NOFOLLOW_LINKS)) {
      bytes = input.readNBytes(8193);
    }
    if (bytes.length > 8192)
      throw new IllegalArgumentException("Shared ticket signing key is too large");
    String pem = new String(bytes, StandardCharsets.US_ASCII).trim();
    String begin = "-----BEGIN " + "PRIVATE KEY-----";
    String end = "-----END " + "PRIVATE KEY-----";
    if (!pem.startsWith(begin) || !pem.endsWith(end))
      throw new IllegalArgumentException("Shared ticket signing requires a PKCS8 private key");
    String encoded = pem.substring(begin.length(), pem.indexOf(end)).replaceAll("\\s", "");
    var key =
        KeyFactory.getInstance("Ed25519")
            .generatePrivate(new PKCS8EncodedKeySpec(Base64.getDecoder().decode(encoded)));
    return new SharedDshTickets.SigningKey(kid, key);
  }

  @Bean(destroyMethod = "close")
  @ConditionalOnMissingBean({SharedDshTickets.Entry.class, SharedDshPlacements.EntryProbe.class})
  SharedDshEntryTransport sharedDshEntryTransport(
      DeploymentAccess access,
      Connection nats,
      WorkspaceStore store,
      SharedDshTickets.SigningKey signer,
      Clock clock) {
    var direct =
        new ProxySelector() {
          @Override
          public List<Proxy> select(URI uri) {
            return List.of(Proxy.NO_PROXY);
          }

          @Override
          public void connectFailed(URI uri, SocketAddress address, java.io.IOException failure) {}
        };
    var http =
        HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(2))
            .followRedirects(HttpClient.Redirect.NEVER)
            .proxy(direct)
            .build();
    return new SharedDshEntryTransport(access, nats, store, signer, clock, http);
  }
}
