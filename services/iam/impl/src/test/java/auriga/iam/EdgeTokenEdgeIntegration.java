package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.security.AurigaPrincipal;
import java.nio.file.*;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.TimeUnit;

/**
 * Test-only overlay adds a driver without editing or substituting the production Go edge client.
 */
final class EdgeTokenEdgeIntegration {
  static void run(
      Path dir,
      AdminCsrfPostgresTest.Time time,
      SessionManager sessions,
      UUID tenant,
      UUID account,
      SessionManager.Issued session,
      AurigaPrincipal principal,
      String csrf)
      throws Exception {
    var key =
        new com.nimbusds.jose.jwk.gen.ECKeyGenerator(com.nimbusds.jose.jwk.Curve.P_256)
            .keyID(UUID.randomUUID().toString())
            .generate();
    var clock = new EdgeTokenFilePublisherTest.Time();
    clock.now = time.instant();
    String secret = Secrets.random();
    var issuer = EdgeTokenFilePublisherTest.issuer(clock, secret, key);
    Path token = dir.resolve("export/service.jwt");
    if (!dir.getFileSystem().supportedFileAttributeViews().contains("posix")) {
      try (var publisher =
          new EdgeTokenFilePublisher(issuer, dir.resolve("secret"), token, 65532)) {
        assertThrows(IllegalStateException.class, publisher::start);
        assertFalse(Files.exists(token));
      }
      return;
    }
    Path installation =
        Files.createDirectory(
            dir.resolve("installation"),
            PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")));
    Path credential =
        Files.writeString(
            installation.resolve("edge.secret"), secret, StandardOpenOption.CREATE_NEW);
    Files.setPosixFilePermissions(credential, PosixFilePermissions.fromString("rw-------"));
    var pki = new IamHopTlsTest();
    pki.dir = dir;
    var ca = IamHopTlsTest.certificate("test-ca", null, true);
    var serverCert = IamHopTlsTest.certificate("localhost", ca, false);
    var edgeCert = IamHopTlsTest.certificate("edge.internal", ca, false);
    var caFile = pki.pem("ca.pem", "CERTIFICATE", ca.cert().getEncoded());
    var cert = pki.pem("server.pem", "CERTIFICATE", serverCert.cert().getEncoded());
    var privateKey =
        pki.pem("server-key.pem", "PRIVATE KEY", serverCert.key().getPrivate().getEncoded());
    var edge = pki.pem("edge.pem", "CERTIFICATE", edgeCert.cert().getEncoded());
    var edgeKey = pki.pem("edge-key.pem", "PRIVATE KEY", edgeCert.key().getPrivate().getEncoded());
    var verifier = EdgeTokenFilePublisherTest.verifier(clock, key);
    var server =
        new auriga.common.grpc.GrpcServer(
            "127.0.0.1",
            0,
            List.of(new IamGrpcSessions(sessions)),
            new auriga.common.grpc.AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:edge")),
            new auriga.common.grpc.GrpcServer.Tls(
                cert.toFile(), privateKey.toFile(), caFile.toFile()),
            false,
            Set.of());
    Path root = Path.of("").toAbsolutePath();
    while (root != null && !Files.exists(root.resolve("infra/edge/iam_rpc.go")))
      root = root.getParent();
    assertNotNull(root, "production edge source must exist for the integration evidence");
    Path driver = dir.resolve("consumer_test.go");
    try (var input = EdgeTokenEdgeIntegration.class.getResourceAsStream("/ip1f-edge-consumer.go")) {
      assertNotNull(input);
      Files.copy(input, driver);
    }
    Path overlay =
        Files.writeString(
            dir.resolve("overlay.json"),
            Json.MAPPER.writeValueAsString(
                Map.of(
                    "Replace",
                    Map.of(
                        root.resolve("infra/edge/ip1f_issuer_file_test.go").toString(),
                        driver.toString()))));
    Process process = null;
    server.start();
    try (var publisher =
        new EdgeTokenFilePublisher(
            issuer, credential, token, (int) Files.getAttribute(dir, "unix:gid"))) {
      Path config =
          Files.writeString(
              dir.resolve("consumer.json"),
              Json.MAPPER.writeValueAsString(
                  Map.ofEntries(
                      Map.entry("Address", "127.0.0.1:" + server.port()),
                      Map.entry("TokenFile", token.toString()),
                      Map.entry("CA", caFile.toString()),
                      Map.entry("Cert", edge.toString()),
                      Map.entry("Key", edgeKey.toString()),
                      Map.entry("Tenant", tenant.toString()),
                      Map.entry("Account", account.toString()),
                      Map.entry("Session", principal.sessionId()),
                      Map.entry("Cookie", session.cookie()),
                      Map.entry("CSRF", csrf),
                      Map.entry("Now", time.instant().getEpochSecond()))));
      Files.setPosixFilePermissions(config, PosixFilePermissions.fromString("rw-------"));
      var builder =
          new ProcessBuilder(
              "go",
              "test",
              "-mod=readonly",
              "-overlay=" + overlay,
              "-count=1",
              "-run=^TestIP1fIssuerFileConsumer$",
              ".");
      builder.directory(root.resolve("infra/edge").toFile());
      builder.environment().put("IP1F_CONSUMER_CONFIG", config.toString());
      builder.environment().put("GOWORK", "off");
      builder.environment().put("GOTOOLCHAIN", "local");
      builder.redirectErrorStream(true).redirectOutput(dir.resolve("consumer.log").toFile());
      process = builder.start();
      await(dir, process, 0);
      publisher.start();
      EdgeTokenFilePublisherTest.awaitToken(token);
      String first = Files.readString(token);
      Object inode = Files.getAttribute(token, "unix:ino");
      advance(dir, 0);
      await(dir, process, 1);
      clock.now = clock.now.plusSeconds(240);
      time.now = clock.now;
      assertTrue(publisher.refresh());
      assertNotEquals(inode, Files.getAttribute(token, "unix:ino"));
      String second = Files.readString(token);
      assertNotEquals(first, second);
      advance(dir, 1);
      await(dir, process, 2);
      clock.now = clock.now.plusSeconds(61);
      time.now = clock.now;
      assertThrows(auriga.common.error.AurigaException.class, () -> verifier.verify(first));
      advance(dir, 2);
      await(dir, process, 3);
      Files.writeString(credential, Secrets.random());
      assertFalse(publisher.refresh());
      assertEquals(second, Files.readString(token));
      clock.now = clock.now.plusSeconds(240);
      time.now = clock.now;
      advance(dir, 3);
      await(dir, process, 4);
      Files.writeString(credential, secret);
      assertTrue(publisher.refresh());
      advance(dir, 4);
      await(dir, process, 5);
      publisher.stop();
      assertFalse(Files.exists(token));
      advance(dir, 5);
      await(dir, process, 6);
      publisher.start();
      EdgeTokenFilePublisherTest.awaitToken(token);
      advance(dir, 6);
      await(dir, process, 7);
      advance(dir, 7);
      assertTrue(process.waitFor(20, TimeUnit.SECONDS), "Go edge driver must terminate");
      assertEquals(0, process.exitValue(), Files.readString(dir.resolve("consumer.log")));
    } finally {
      if (process != null && process.isAlive()) {
        var descendants = process.descendants().toList();
        for (var child : descendants) child.destroyForcibly();
        process.destroyForcibly();
        process.waitFor(10, TimeUnit.SECONDS);
        for (var child : descendants) child.onExit().get(10, TimeUnit.SECONDS);
      }
      server.stop();
    }
  }

  private static void advance(Path dir, int stage) throws Exception {
    Files.writeString(dir.resolve("continue-" + stage), "");
  }

  private static void await(Path dir, Process process, int stage) throws Exception {
    long deadline = System.nanoTime() + Duration.ofSeconds(stage == 0 ? 180 : 20).toNanos();
    try (var watcher = dir.getFileSystem().newWatchService()) {
      dir.register(watcher, StandardWatchEventKinds.ENTRY_CREATE);
      while (!Files.exists(dir.resolve("stage-" + stage))) {
        assertTrue(process.isAlive(), () -> "Go consumer exited: " + readLog(dir));
        assertTrue(
            System.nanoTime() < deadline,
            () -> "Go consumer did not reach stage " + stage + ": " + readLog(dir));
        var event = watcher.poll(1, TimeUnit.SECONDS);
        if (event != null) event.reset();
      }
    }
  }

  private static String readLog(Path dir) {
    try {
      return Files.readString(dir.resolve("consumer.log"));
    } catch (java.io.IOException e) {
      return "consumer log unavailable";
    }
  }
}
