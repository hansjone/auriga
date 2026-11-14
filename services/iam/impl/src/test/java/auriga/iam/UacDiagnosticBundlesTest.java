package auriga.iam;

import static auriga.iam.UacFlowPostgresTest.*;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.audit.api.UacAuditEvents;
import auriga.common.context.*;
import auriga.common.error.*;
import auriga.common.events.*;
import auriga.common.security.*;
import auriga.ops.api.UacOpsAlerts;
import auriga.testsupport.PostgresExtension;
import com.sun.net.httpserver.HttpServer;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.MessageDigest;
import java.time.*;
import java.util.*;
import java.util.concurrent.atomic.*;
import java.util.function.Supplier;
import java.util.zip.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class UacDiagnosticBundlesTest {
  @TempDir Path temporaryRoot;

  @BeforeAll
  static void database() {
    UacFlowPostgresTest.migrate();
  }

  static final Set<String> PERMISSIONS =
      Set.of("iam.providers.read", "audit.events.read", "ops.alerts.read");

  static AurigaPrincipal admin(Fixture f) {
    return new AurigaPrincipal(
        f.account.toString(),
        f.tenant,
        PrincipalType.USER,
        TokenContext.ADMIN,
        UUID.randomUUID().toString(),
        null,
        "member",
        List.of(),
        PERMISSIONS,
        f.time.instant(),
        null,
        UUID.randomUUID().toString());
  }

  static <T> T call(Fixture f, Supplier<T> action) {
    return RequestContext.start(null, null).withPrincipal(admin(f)).call(() -> f.call(action));
  }

  UacDiagnosticBundles bundles(
      Fixture f, UacAuditEvents audit, UacOpsAlerts ops, UacLokiSource loki, int size) {
    var publisher =
        new OutboxEventPublisher(
            store.networkJdbc(), "iam", "iam", new EventSchemas(), Json.MAPPER, f.time, null);
    return new UacDiagnosticBundles(
        store,
        new UacProviderService(store, f.uac.providers, f.uac.adapter, f.sessions, f.audit, f.time),
        f.uac,
        diagnosticSettings(f),
        audit,
        ops,
        loki,
        publisher,
        f.time,
        true,
        temporaryRoot,
        size);
  }

  static UacAuditEvents audit(Fixture f) {
    var source = mock(UacAuditEvents.class);
    var row =
        Json.object(
            "id",
            UUID.randomUUID().toString(),
            "tenantId",
            f.tenant.toString(),
            "time",
            f.time.instant().minusSeconds(1).toString(),
            "action",
            "iam.uac.token_verify",
            "result",
            "success",
            "actor",
            Json.object("type", "system"),
            "target",
            Json.object("type", "provider", "id", f.provider),
            "details",
            Json.object(
                "providerId",
                f.provider,
                "purpose",
                "login_sso",
                "chain",
                "login",
                "outcome",
                "success"));
    when(source.readUac(any(), any(), any(), nullable(UUID.class), anyInt()))
        .thenReturn(new UacAuditEvents.Page(List.of(row.toString()), false));
    return source;
  }

  static UacOpsAlerts ops() {
    var source = mock(UacOpsAlerts.class);
    when(source.readUac(any(), any(), any(), anyInt()))
        .thenReturn(new UacOpsAlerts.Page(List.of(), false));
    return source;
  }

  static Map<String, byte[]> unzip(byte[] body) throws Exception {
    Map<String, byte[]> result = new LinkedHashMap<>();
    try (var zip = new ZipInputStream(new ByteArrayInputStream(body))) {
      for (ZipEntry entry; (entry = zip.getNextEntry()) != null; ) {
        assertFalse(entry.getName().contains("/"));
        assertNull(result.put(entry.getName(), zip.readAllBytes()));
      }
    }
    return result;
  }

  static final class Loki implements AutoCloseable {
    final HttpServer server;
    final AtomicInteger status = new AtomicInteger(200), calls = new AtomicInteger();
    final AtomicReference<List<String>> lines = new AtomicReference<>(List.of());
    final AtomicReference<String> raw = new AtomicReference<>(),
        query = new AtomicReference<>(),
        tenant = new AtomicReference<>();

    Loki() throws Exception {
      server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
      server.createContext(
          "/loki/api/v1/query_range",
          exchange -> {
            calls.incrementAndGet();
            query.set(
                URLDecoder.decode(exchange.getRequestURI().getRawQuery(), StandardCharsets.UTF_8));
            tenant.set(exchange.getRequestHeaders().getFirst("X-Scope-OrgID"));
            String body = raw.get();
            if (body == null)
              body =
                  Json.object(
                          "status",
                          "success",
                          "data",
                          Json.object(
                              "resultType",
                              "streams",
                              "result",
                              List.of(
                                  Json.object(
                                      "values",
                                      lines.get().stream()
                                          .map(line -> List.of("1", line))
                                          .toList()))))
                      .toString();
            byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
            exchange.sendResponseHeaders(status.get(), bytes.length);
            try (var output = exchange.getResponseBody()) {
              output.write(bytes);
            }
            exchange.close();
          });
      server.start();
    }

    String origin() {
      return "http://127.0.0.1:" + server.getAddress().getPort();
    }

    UacLokiSource source() {
      return new UacLokiSource(origin());
    }

    public void close() {
      server.stop(0);
    }
  }

  @Test
  void completeBundleHasVerifiedManifestHashesFilteredSourcesAndNoSecretsOrPrivateConfiguration()
      throws Exception {
    try (var f = new Fixture("token-verify", 3);
        var loki = new Loki()) {
      f.login();
      f.run();
      loki.lines.set(stageEvents(f).stream().map(JsonNode::toString).toList());
      var exporter = bundles(f, audit(f), ops(), loki.source(), UacDiagnosticBundles.MAX_BYTES);
      AtomicInteger checks = new AtomicInteger();
      var result =
          call(
              f,
              () -> exporter.build(Json.object("providerId", f.provider), checks::incrementAndGet));
      assertFalse(result.partial());
      assertEquals(3, checks.get());
      assertTrue(
          result.filename().matches("uac-diagnostics-[0-9]{8}T[0-9]{6}Z-[a-f0-9-]{36}\\.zip"));
      var files = unzip(result.bytes());
      assertEquals(
          Set.of(
              "manifest.json",
              "uac-steps.jsonl",
              "audit.jsonl",
              "ops-alerts.json",
              "sanitized-config.json"),
          files.keySet());
      var manifest = Json.MAPPER.readTree(files.get("manifest.json"));
      new ContractSchemas()
          .validate("auth/uac-control.schema.json#/$defs/UacDiagnosticManifest", manifest);
      for (JsonNode file : manifest.path("files")) {
        byte[] bytes = files.get(file.path("name").asString());
        assertEquals(bytes.length, file.path("bytes").asInt());
        assertEquals(
            HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes)),
            file.path("sha256").asString());
      }
      String plain =
          files.values().stream()
              .map(b -> new String(b, StandardCharsets.UTF_8))
              .reduce("", String::concat);
      for (String secret :
          List.of(
              f.token,
              f.replacement,
              f.origin,
              "test-system",
              "Test department",
              "test_portal_token")) assertFalse(plain.contains(secret));
      assertEquals(f.tenant.toString(), loki.tenant.get());
      assertTrue(loki.query.get().contains("uac_tenant=\"" + f.tenant + "\""));
      assertTrue(loki.query.get().contains("uac_provider=\"" + f.provider + "\""));
      assertTrue(loki.query.get().contains("direction=forward"));
      f.call(
          () ->
              store.tx(
                  () -> {
                    assertEquals(
                        1,
                        store
                            .networkJdbc()
                            .queryForObject(
                                "SELECT count(*) FROM iam.outbox_event WHERE tenant_id=? AND id=? AND event_type='audit.recorded'",
                                Integer.class,
                                f.tenant,
                                UUID.fromString(manifest.path("exportAuditId").asString())));
                    return null;
                  }));
      try (var children = Files.list(temporaryRoot)) {
        assertEquals(0, children.count());
      }
      var again = call(f, () -> exporter.build(Json.object("providerId", f.provider), () -> {}));
      assertNotEquals(result.filename(), again.filename());
    }
  }

  @Test
  void missingLokiAndMalformedAndOutOfScopeLinesAreExplicitButAuditDenialNeverExportsPartial()
      throws Exception {
    try (var f = new Fixture("token-verify", 3);
        var loki = new Loki()) {
      f.login();
      var exporter = bundles(f, audit(f), ops(), loki.source(), UacDiagnosticBundles.MAX_BYTES);
      loki.status.set(503);
      var partial = call(f, () -> exporter.build(Json.object("providerId", f.provider), () -> {}));
      assertTrue(partial.partial());
      var manifest = Json.MAPPER.readTree(unzip(partial.bytes()).get("manifest.json"));
      assertTrue(manifest.path("missingSources").toString().contains("loki"));
      loki.status.set(200);
      var foreign =
          stageEvents(f)
              .getFirst()
              .deepCopy()
              .put("tenantId", UUID.randomUUID().toString())
              .put("empNo", "FOREIGN_PERSON");
      loki.lines.set(List.of("<html>private raw token</html>", foreign.toString()));
      var malformed =
          call(f, () -> exporter.build(Json.object("providerId", f.provider), () -> {}));
      assertTrue(malformed.partial());
      String all =
          unzip(malformed.bytes()).values().stream()
              .map(b -> new String(b, StandardCharsets.UTF_8))
              .reduce("", String::concat);
      assertFalse(all.contains("FOREIGN_PERSON"));
      assertFalse(all.contains("private raw token"));
      var denied = mock(UacAuditEvents.class);
      when(denied.readUac(any(), any(), any(), nullable(UUID.class), anyInt()))
          .thenThrow(new AurigaException(CommonErrors.FORBIDDEN));
      assertEquals(
          "common.forbidden",
          assertThrows(
                  AurigaException.class,
                  () ->
                      call(
                          f,
                          () ->
                              bundles(
                                      f,
                                      denied,
                                      ops(),
                                      loki.source(),
                                      UacDiagnosticBundles.MAX_BYTES)
                                  .build(Json.object("providerId", f.provider), () -> {})))
              .errorCode()
              .code());
    }
  }

  @Test
  void invalidInputsForeignProviderFinalPermissionRevocationAndEmptySourcesRefuseDownload()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var exporter = bundles(f, audit(f), ops(), null, UacDiagnosticBundles.MAX_BYTES);
      for (JsonNode input :
          List.of(
              Json.object("sinceSeconds", 86401),
              Json.object("sinceSeconds", 0),
              Json.object("sinceSeconds", 1.5),
              Json.object("url", "http://example.com"),
              Json.object("path", "../vault"),
              Json.object("tenantId", UUID.randomUUID().toString())))
        assertEquals(
            "common.validation_failed",
            assertThrows(
                    AurigaException.class, () -> call(f, () -> exporter.build(input, () -> {})))
                .errorCode()
                .code());
      assertEquals(
          "common.not_found",
          assertThrows(
                  AurigaException.class,
                  () ->
                      call(
                          f,
                          () ->
                              exporter.build(
                                  Json.object("providerId", UUID.randomUUID().toString()),
                                  () -> {})))
              .errorCode()
              .code());
      f.login();
      AtomicInteger checks = new AtomicInteger();
      assertEquals(
          "common.forbidden",
          assertThrows(
                  AurigaException.class,
                  () ->
                      call(
                          f,
                          () ->
                              exporter.build(
                                  Json.object("providerId", f.provider),
                                  () -> {
                                    if (checks.incrementAndGet() == 3)
                                      throw new AurigaException(CommonErrors.FORBIDDEN);
                                  })))
              .errorCode()
              .code());
      try (var children = Files.list(temporaryRoot)) {
        assertEquals(0, children.count());
      }
    }
    try (var f = new Fixture("token-verify", 3)) {
      var emptyAudit = mock(UacAuditEvents.class);
      when(emptyAudit.readUac(any(), any(), any(), nullable(UUID.class), anyInt()))
          .thenReturn(new UacAuditEvents.Page(List.of(), false));
      assertEquals(
          "common.unavailable",
          assertThrows(
                  AurigaException.class,
                  () ->
                      call(
                          f,
                          () ->
                              bundles(f, emptyAudit, ops(), null, UacDiagnosticBundles.MAX_BYTES)
                                  .build(Json.object("providerId", f.provider), () -> {})))
              .errorCode()
              .code());
    }
  }

  @Test
  void boundedBundleReportsTruncationAndExpiredDetailCannotBeDownloaded() throws Exception {
    try (var f = new Fixture("token-verify", 3);
        var loki = new Loki()) {
      f.login();
      ObjectNode sample = stageEvents(f).getFirst();
      List<String> lines = new ArrayList<>();
      for (int i = 0; i < 2000; i++)
        lines.add(sample.deepCopy().put("eventId", UUID.randomUUID().toString()).toString());
      loki.lines.set(lines);
      var small = bundles(f, audit(f), ops(), loki.source(), 256 * 1024);
      var result = call(f, () -> small.build(Json.object("providerId", f.provider), () -> {}));
      assertTrue(result.partial());
      assertTrue(result.bytes().length <= 256 * 1024);
      var manifest = Json.MAPPER.readTree(unzip(result.bytes()).get("manifest.json"));
      assertTrue(manifest.path("files").toString().contains("\"truncated\":true"));
      var settings = diagnosticSettings(f);
      var on =
          f.call(
              () ->
                  settings.put(
                      f.provider,
                      Json.object("enabled", true),
                      UacDiagnosticSettings.etag(settings.get(f.provider))));
      var event =
          sample.deepCopy().put("eventId", UUID.randomUUID().toString()).put("level", "DEBUG");
      event.set(
          "udsRawSanitized",
          Json.object("bo", Json.object("code", "0000", "privateSecret", f.token)));
      loki.lines.set(List.of(event.toString()));
      AtomicInteger checks = new AtomicInteger();
      var exporter = bundles(f, audit(f), ops(), loki.source(), UacDiagnosticBundles.MAX_BYTES);
      assertEquals(
          "common.unavailable",
          assertThrows(
                  AurigaException.class,
                  () ->
                      call(
                          f,
                          () ->
                              exporter.build(
                                  Json.object("providerId", f.provider),
                                  () -> {
                                    if (checks.incrementAndGet() == 2)
                                      settings.put(
                                          f.provider,
                                          Json.object("enabled", false),
                                          UacDiagnosticSettings.etag(on));
                                  })))
              .errorCode()
              .code());
      try (var children = Files.list(temporaryRoot)) {
        assertEquals(0, children.count());
      }
    }
  }

  @Test
  void lokiPermissionFailureAndUnsafeTrustedOriginsFailClosed() throws Exception {
    for (String invalid :
        List.of(
            "file:///etc/passwd",
            "https://user:secret@example.com",
            "https://example.com/query",
            "https://example.com/?query=anything"))
      assertThrows(IllegalArgumentException.class, () -> new UacLokiSource(invalid));
    try (var loki = new Loki()) {
      loki.status.set(403);
      assertEquals(
          "common.forbidden",
          assertThrows(
                  AurigaException.class,
                  () ->
                      loki.source()
                          .read(
                              UUID.randomUUID(),
                              null,
                              null,
                              Instant.now().minusSeconds(60),
                              Instant.now()))
              .errorCode()
              .code());
      loki.status.set(200);
      loki.raw.set("<html>private data</html>");
      var invalid =
          loki.source()
              .read(UUID.randomUUID(), null, null, Instant.now().minusSeconds(60), Instant.now());
      assertFalse(invalid.available());
      assertEquals("invalid_log", invalid.reason());
    }
  }

  @Test
  void privateTemporaryDirectoryRejectsTraversalAndOverwriteAndCleansFiles() throws Exception {
    assertThrows(IOException.class, () -> new UacDiagnosticTemp(temporaryRoot.resolve("..")));
    Path created;
    try (var temporary = new UacDiagnosticTemp(temporaryRoot)) {
      created = temporary.directory;
      assertTrue(Files.exists(created));
      assertThrows(
          IOException.class,
          () ->
              temporary.publish(
                  Map.of("../vault", new byte[0], "manifest.json", new byte[0]), 1024));
      byte[] bytes =
          temporary.publish(Map.of("manifest.json", "{}".getBytes(StandardCharsets.UTF_8)), 1024);
      assertTrue(bytes.length > 0);
      assertThrows(
          IOException.class, () -> temporary.publish(Map.of("manifest.json", new byte[0]), 1024));
    }
    assertFalse(Files.exists(created));
  }

  @Test
  void aTemporaryRootThroughASymlinkOrWindowsJunctionIsRejected() throws Exception {
    Path target = Files.createDirectory(temporaryRoot.resolve("target")),
        link = temporaryRoot.resolve("link");
    if (System.getProperty("os.name").startsWith("Windows")) {
      var process =
          new ProcessBuilder(
                  "cmd.exe", "/d", "/c", "mklink", "/J", link.toString(), target.toString())
              .redirectErrorStream(true)
              .start();
      process.getInputStream().readAllBytes();
      assertEquals(0, process.waitFor());
    } else Files.createSymbolicLink(link, target);
    try {
      assertThrows(IOException.class, () -> new UacDiagnosticTemp(link));
      try (var files = Files.list(target)) {
        assertEquals(0, files.count());
      }
    } finally {
      Files.delete(link);
    }
  }
}
