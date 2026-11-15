package auriga.collab.network.keys;

import auriga.testsupport.PostgresExtension;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.time.Instant;
import java.util.Base64;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.platform.engine.discovery.DiscoverySelectors;
import org.junit.platform.launcher.core.LauncherDiscoveryRequestBuilder;
import org.junit.platform.launcher.core.LauncherFactory;
import org.junit.platform.launcher.listeners.SummaryGeneratingListener;
import org.springframework.boot.SpringApplication;

/** Runs the N Go client against the frozen C jar's MVC/security/PG implementation. */
@ExtendWith(PostgresExtension.class)
public class CNJavaInterop {
  static Path run, module;

  public static void main(String[] args) {
    run = Path.of(args[0]).toAbsolutePath();
    module = Path.of(args[1]).toAbsolutePath();
    var listener = new SummaryGeneratingListener();
    var launcher = LauncherFactory.create();
    launcher.registerTestExecutionListeners(listener);
    launcher.execute(LauncherDiscoveryRequestBuilder.request()
        .selectors(DiscoverySelectors.selectClass(CNJavaInterop.class)).build());
    listener.getSummary().printTo(new java.io.PrintWriter(System.out, true));
    listener.getSummary().printFailuresTo(new java.io.PrintWriter(System.out, true));
    System.exit(listener.getSummary().getTotalFailureCount() == 0
        && listener.getSummary().getTestsSucceededCount() == 1 ? 0 : 1);
  }

  static Path secret(String name, byte[] body) throws Exception {
    return NetworkKeyConfigurationTest.protectedFile(run.resolve(name), body);
  }

  static byte[] pem(String kind, byte[] der) {
    return ("-----BEGIN " + kind + "-----\n"
        + Base64.getMimeEncoder(64, new byte[] {'\n'}).encodeToString(der)
        + "\n-----END " + kind + "-----\n").getBytes(StandardCharsets.US_ASCII);
  }

  @Test
  void actualGoManagementClient() throws Exception {
    String production = MsgnetRenewalController.class.getProtectionDomain()
        .getCodeSource().getLocation().toString();
    if (!production.contains("/jar/BOOT-INF/lib/")) {
      throw new AssertionError("C production code must come from the verified jar: " + production);
    }
    var f = MsgnetExpectedSelectorsHttpTest.fixture();
    Map<String, Object> settings = new HashMap<>();
    MsgnetExpectedSelectorsHttpTest.properties((key, value) -> settings.put(key, value.get()));
    String password = UUID.randomUUID().toString();
    Instant now = Instant.now();
    var ca = MsgnetCertificates.issue("cn-test-ca", now.minusSeconds(60),
        now.plusSeconds(3600), List.of(), List.of(), null, null);
    var server = MsgnetCertificates.issue("localhost", now.minusSeconds(30),
        now.plusSeconds(1800), List.of("127.0.0.1"), List.of("serverAuth"),
        ca.certificate(), ca.pair().getPrivate());
    var peer = MsgnetCertificates.issue("projector", now.minusSeconds(30),
        now.plusSeconds(1800), List.of("localhost"), List.of("clientAuth"),
        ca.certificate(), ca.pair().getPrivate());
    var trust = KeyStore.getInstance("PKCS12");
    trust.load(null, null);
    trust.setCertificateEntry("ca", ca.certificate());
    var keys = KeyStore.getInstance("PKCS12");
    keys.load(null, null);
    keys.setKeyEntry("server", server.pair().getPrivate(), password.toCharArray(),
        new java.security.cert.Certificate[] {server.certificate(), ca.certificate()});
    var bytes = new java.io.ByteArrayOutputStream();
    keys.store(bytes, password.toCharArray());
    settings.put("server.ssl.key-store", secret("server.p12", bytes.toByteArray()).toUri().toString());
    bytes.reset();
    trust.store(bytes, password.toCharArray());
    settings.put("server.ssl.trust-store", secret("trust.p12", bytes.toByteArray()).toUri().toString());
    settings.put("server.ssl.key-store-password", password);
    settings.put("server.ssl.trust-store-password", password);
    settings.put("server.port", "0");
    settings.put("server.address", "127.0.0.1");
    settings.put("spring.main.banner-mode", "off");
    settings.put("auriga.logging.json", "false");
    settings.put("logging.level.root", "WARN");
    secret("ca.pem", pem("CERTIFICATE", ca.certificate().getEncoded()));
    secret("client.pem", pem("CERTIFICATE", peer.certificate().getEncoded()));
    secret("client.key", pem("PRIVATE KEY", peer.pair().getPrivate().getEncoded()));
    secret("service.jwt", f.token(f.base.tenant, true).getBytes(StandardCharsets.US_ASCII));
    secret("unprivileged.jwt", f.token(f.base.tenant, false).getBytes(StandardCharsets.US_ASCII));
    var app = new SpringApplication(MsgnetExpectedSelectorsHttpTest.App.class);
    app.setDefaultProperties(settings);
    Path emptyConfig = run.resolve("empty.properties");
    Files.writeString(emptyConfig, "");
    try (var context = app.run("--spring.config.location=" + emptyConfig.toUri())) {
      var meta = MsgnetRenewal.object()
          .put("origin", "https://127.0.0.1:" + context.getEnvironment().getProperty("local.server.port"))
          .put("tenant", f.base.tenant.toString())
          .put("now", f.base.wall.instant().toString())
          .put("root", Base64.getUrlEncoder().withoutPadding()
              .encodeToString(f.base.root.getPublic().getEncoded()))
          .put("c_source", production);
      Files.writeString(run.resolve("fixture.json"), MsgnetRenewal.canonical(meta));
      Files.writeString(run.resolve("first-bundle.json"), MsgnetRenewal.canonical(f.pki.bundle()));
      runGo("pending");
      f.base.project(f.pki, "network", "synthetic-projector", "loaded");
      f.pki.reconcile();
      if (!"new_leaves".equals(f.pki.bundle().path("payload").path("phase").asString())) {
        throw new AssertionError("C did not advance after verified loaded receipt");
      }
      runGo("complete");
    } finally {
      MsgnetExpectedSelectorsHttpTest.stop();
      for (String name : List.of("server.p12", "trust.p12", "client.pem", "client.key",
          "service.jwt", "unprivileged.jwt", "ca.pem")) {
        Files.deleteIfExists(run.resolve(name));
      }
    }
  }

  static void runGo(String mode) throws Exception {
    var builder = new ProcessBuilder("go", "test", "-race", "-p", "2", "-count=1", "-v", "-timeout=2m",
        "-run", "^TestCNJavaManagement$", "./collab/projection");
    builder.directory(module.toFile());
    builder.environment().put("GOWORK", "off");
    builder.environment().put("AURIGA_MSGNET_CN_FIXTURE", run.toString());
    builder.environment().put("AURIGA_MSGNET_CN_MODE", mode);
    builder.redirectErrorStream(true).redirectOutput(run.resolve("go-" + mode + ".log").toFile());
    var child = builder.start();
    try {
      if (!child.waitFor(180, TimeUnit.SECONDS) || child.exitValue() != 0) {
        throw new AssertionError("Go " + mode + " failed, see " + run.resolve("go-" + mode + ".log"));
      }
    } finally {
      if (child.isAlive()) child.destroyForcibly().waitFor();
    }
  }
}
