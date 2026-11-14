package auriga.iam;

import auriga.iam.api.*;
import java.io.ByteArrayInputStream;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.*;
import java.util.Arrays;
import javax.net.ssl.*;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;
import org.springframework.core.env.Environment;

/** Dedicated service identity and fixed trust; no JVM-global TLS or proxy changes. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(prefix = "auriga.iam.msgnet-ledger", name = "origin")
@ConditionalOnExpression("${auriga.services.iam.enabled:true}")
public class MessageNetworkLedgerConfiguration {
  @Bean(destroyMethod = "close")
  @ConditionalOnMissingBean(MessageNetworkControlLedger.class)
  RemoteMessageNetworkLedger msgnetRemoteLedger(Environment env) throws Exception {
    String prefix = "auriga.iam.msgnet-ledger.";
    byte[] passwordBytes =
        InstallationPinFile.read(Path.of(env.getRequiredProperty(prefix + "password-file")));
    char[] password = new String(passwordBytes, StandardCharsets.UTF_8).strip().toCharArray();
    Arrays.fill(passwordBytes, (byte) 0);
    try {
      var keys = KeyStore.getInstance("PKCS12");
      keys.load(
          new ByteArrayInputStream(
              InstallationPinFile.read(Path.of(env.getRequiredProperty(prefix + "key-store")))),
          password);
      boolean hasKey = false;
      for (var aliases = keys.aliases(); aliases.hasMoreElements(); )
        hasKey |= keys.isKeyEntry(aliases.nextElement());
      if (!hasKey) throw new IllegalStateException("message ledger mTLS identity required");
      var km = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
      km.init(keys, password);
      var roots = KeyStore.getInstance("PKCS12");
      roots.load(
          new ByteArrayInputStream(
              InstallationPinFile.read(Path.of(env.getRequiredProperty(prefix + "trust-store")))),
          password);
      var tm = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
      tm.init(roots);
      var tls = SSLContext.getInstance("TLS");
      tls.init(km.getKeyManagers(), tm.getTrustManagers(), new SecureRandom());
      Path token = Path.of(env.getRequiredProperty(prefix + "token-file"));
      return new RemoteMessageNetworkLedger(
          URI.create(env.getRequiredProperty(prefix + "origin")),
          tls,
          () -> {
            try {
              return new String(InstallationPinFile.read(token), StandardCharsets.UTF_8).strip();
            } catch (java.io.IOException unavailable) {
              throw new IllegalStateException("message ledger credential unavailable", unavailable);
            }
          });
    } finally {
      Arrays.fill(password, '\0');
    }
  }
}
