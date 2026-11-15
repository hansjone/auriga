package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import java.nio.file.*;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.TimeUnit;

/** Actual IAM SQL and authenticated RPC feed the production edge broker KICK path. */
final class ManagedIsolationEdgeIntegration {
  static void run(Path directory, IamStore store, IamAdminDevices devices, UUID tenant, UUID device)
      throws Exception {
    var clock = new EdgeTokenFilePublisherTest.Time();
    clock.now = java.time.Instant.now();
    var signing =
        new com.nimbusds.jose.jwk.gen.ECKeyGenerator(com.nimbusds.jose.jwk.Curve.P_256)
            .keyID(UUID.randomUUID().toString())
            .generate();
    String secret = Secrets.random();
    var issuer = EdgeTokenFilePublisherTest.issuer(clock, secret, signing);
    var verifier = EdgeTokenFilePublisherTest.verifier(clock, signing);
    Path token = Files.writeString(directory.resolve("edge.jwt"), issuer.issue("edge", secret));
    var pki = new IamHopTlsTest();
    pki.dir = directory;
    var ca = IamHopTlsTest.certificate("test-ca", null, true);
    var server = IamHopTlsTest.certificate("localhost", ca, false);
    var peer = IamHopTlsTest.certificate("edge.internal", ca, false);
    Path caFile = pki.pem("ca.pem", "CERTIFICATE", ca.cert().getEncoded());
    Path cert = pki.pem("server.pem", "CERTIFICATE", server.cert().getEncoded());
    Path key = pki.pem("server-key.pem", "PRIVATE KEY", server.key().getPrivate().getEncoded());
    Path peerCert = pki.pem("edge.pem", "CERTIFICATE", peer.cert().getEncoded());
    Path peerKey = pki.pem("edge-key.pem", "PRIVATE KEY", peer.key().getPrivate().getEncoded());
    if (directory.getFileSystem().supportedFileAttributeViews().contains("posix"))
      for (var path : List.of(token, key, peerKey))
        Files.setPosixFilePermissions(
            path, java.nio.file.attribute.PosixFilePermissions.fromString("rw-------"));
    var grpc =
        new auriga.common.grpc.GrpcServer(
            "127.0.0.1",
            0,
            List.of(new IamGrpcAdminDevices(devices)),
            new auriga.common.grpc.AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:edge")),
            new auriga.common.grpc.GrpcServer.Tls(cert.toFile(), key.toFile(), caFile.toFile()),
            false,
            Set.of());
    Path root = Path.of("").toAbsolutePath();
    while (root != null && !Files.exists(root.resolve("infra/edge/msgnet_isolation.go")))
      root = root.getParent();
    assertNotNull(root);
    Process process = null;
    grpc.start();
    try {
      var config =
          Map.of(
              "Address",
              "127.0.0.1:" + grpc.port(),
              "TokenFile",
              token.toString(),
              "CA",
              caFile.toString(),
              "Cert",
              peerCert.toString(),
              "Key",
              peerKey.toString(),
              "Tenant",
              tenant.toString(),
              "Device",
              device.toString(),
              "Directory",
              directory.toString());
      Path file =
          Files.writeString(
              directory.resolve("managed-isolation.json"), Json.MAPPER.writeValueAsString(config));
      Path log = directory.resolve("managed-isolation-go.log");
      var builder =
          new ProcessBuilder(
              "go",
              "test",
              "-p=1",
              "-mod=readonly",
              "-count=1",
              "-run=^TestMsgnetIsolationWithProductionManagedBindings$",
              ".");
      builder.directory(root.resolve("infra/edge").toFile());
      builder.environment().put("GOWORK", "off");
      builder.environment().put("GOTOOLCHAIN", "local");
      builder.environment().put("GOMAXPROCS", "2");
      builder.environment().put("AURIGA_MSGNET_MANAGED_BINDINGS", file.toString());
      builder.redirectErrorStream(true).redirectOutput(log.toFile());
      process = builder.start();
      Path connection = directory.resolve("managed-binding.json");
      long deadline = System.nanoTime() + Duration.ofMinutes(2).toNanos();
      while (!Files.exists(connection)) {
        assertTrue(
            process.isAlive(), () -> "Go fixture failed before connection creation: " + read(log));
        assertTrue(System.nanoTime() < deadline, () -> "Go fixture startup timeout: " + read(log));
        Thread.sleep(10);
      }
      var binding = Json.read(Files.readString(connection));
      assertEquals(device.toString(), binding.path("device").asString());
      TenantContext.run(
          tenant,
          () ->
              store.tx(
                  () -> {
                    store.insert(
                        "network_ingress", binding.path("ingress").asString(), binding, null);
                    return null;
                  }));
      TenantContext.run(
          tenant,
          () -> {
            assertEquals(0, devices.connectionBindings().path("items").size());
            assertEquals(1, devices.messageConnectionBindings().path("items").size());
          });
      Files.writeString(directory.resolve("binding-ready"), "ready");
      assertTrue(
          process.waitFor(30, TimeUnit.SECONDS), "managed isolation consumer did not finish");
      assertEquals(0, process.exitValue(), read(log));
    } finally {
      if (process != null && process.isAlive()) {
        process.destroyForcibly();
        process.waitFor(5, TimeUnit.SECONDS);
      }
      grpc.stop();
    }
  }

  private static String read(Path file) {
    try {
      return Files.readString(file);
    } catch (java.io.IOException error) {
      return error.toString();
    }
  }
}
