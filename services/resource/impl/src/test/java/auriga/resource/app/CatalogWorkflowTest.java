package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.org.v1.*;
import auriga.resource.api.*;
import auriga.resource.objectstore.*;
import auriga.resource.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.apache.commons.compress.archivers.tar.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Native PostgreSQL, forced RLS, separate migration/runtime roles and real transactional outbox.
 */
@ExtendWith(PostgresExtension.class)
class CatalogWorkflowTest {
  static PostgresExtension.ServiceRoles roles;
  static JdbcTemplate jdbc;
  static ResourceStore tx;
  static ResourceWorkflowStore db;
  static final UUID TENANT = UUID.randomUUID(),
      AUTHOR = UUID.randomUUID(),
      GENERAL = UUID.randomUUID(),
      SECURITY = UUID.randomUUID(),
      PUBLISHER = UUID.randomUUID();
  static final Set<String> PERMS =
      Set.of(
          ResourceAccess.READ,
          ResourceAccess.UPLOAD,
          "resource.versions.submit",
          "resource.versions.review",
          "resource.versions.publish",
          "resource.distributions.manage",
          CatalogReviewPolicy.permission());
  @TempDir Path objects;
  LocalObjectStore store;
  CatalogWorkflow catalog;
  ResourceWorkflow legacy;
  ResourceAccess access;
  AtomicBoolean enabled = new AtomicBoolean(true);
  Set<UUID> security = new HashSet<>();
  ResourceSigning signing;
  Clock clock = Clock.fixed(Instant.parse("2026-11-13T00:00:00Z"), ZoneOffset.UTC);

  @BeforeAll
  static void migrate() {
    roles =
        PostgresExtension.serviceRoles(
            CatalogWorkflowTest.class, "catalog_owner", "catalog_runtime", "resource");
    CommonMigrations.migrate(roles.owner().dataSource(), "resource");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("resource")
                .defaultSchema("resource")
                .locations("classpath:db/resource")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    tx = new ResourceStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
    db = new ResourceWorkflowStore(jdbc);
  }

  @BeforeEach
  void setup() throws Exception {
    store = new LocalObjectStore(objects);
    security.add(SECURITY);
    access =
        new ResourceAccess(
            new ResourceAccessInputs() {
              public Member member(UUID t, UUID a) {
                return Member.newBuilder()
                    .setTenantId(t.toString())
                    .setAccountId(a.toString())
                    .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                    .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                    .build();
              }

              public GetEffectivePermissionsResponse permissions(UUID t, UUID a) {
                var b =
                    GetEffectivePermissionsResponse.newBuilder()
                        .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                        .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE);
                for (String p : PERMS)
                  if (!p.equals(CatalogReviewPolicy.permission()) || security.contains(a))
                    b.addPermissions(p);
                return b.build();
              }

              public boolean allowPersonalPlugins(UUID t) {
                return true;
              }
            });
    var keys = KeyPairGenerator.getInstance("Ed25519");
    signing =
        new ResourceSigning(
            "release",
            keys.generateKeyPair().getPrivate(),
            "revoke",
            keys.generateKeyPair().getPrivate());
    var events =
        new OutboxEventPublisher(
            jdbc, "resource", "resource", new EventSchemas(), JSON, clock, () -> {});
    legacy = new ResourceWorkflow(tx, db, access, () -> store, () -> signing, events, clock);
    var catalogEvents =
        new CatalogEvents(
            new OutboxEventPublisher(jdbc, "resource", "resource", null, JSON, clock, () -> {}));
    catalog =
        new CatalogWorkflow(
            tx,
            db,
            access,
            legacy,
            () -> store,
            () -> signing,
            enabled::get,
            new ResourceCursor(new byte[32], clock),
            catalogEvents,
            clock);
  }

  @AfterEach
  void close() {
    store.close();
  }

  AurigaPrincipal actor(UUID id) {
    return new AurigaPrincipal(
        id.toString(),
        TENANT,
        PrincipalType.USER,
        TokenContext.ADMIN,
        "test",
        null,
        "member",
        List.of(),
        PERMS,
        clock.instant(),
        clock.instant(),
        "test");
  }

  static void assertError(ErrorCode expected, org.junit.jupiter.api.function.Executable action) {
    assertEquals(expected, assertThrows(AurigaException.class, action).errorCode());
  }

  static String key() {
    return UUID.randomUUID().toString();
  }

  static String etag(JsonNode n) {
    return "\"" + n.path("revision").asLong() + "\"";
  }

  static ObjectNode reason() {
    return JSON.createObjectNode().put("reason", "tested");
  }

  JsonNode create() {
    return catalog.create(
        actor(AUTHOR),
        JSON.createObjectNode()
            .put("kind", "skill")
            .put("name", "skill-" + key())
            .put("visibility", "tenant"),
        key(),
        "\"0\"");
  }

  record Upload(UUID pkg, UUID version, JsonNode value, byte[] manifest, byte[] payload) {}

  Upload upload(boolean command) throws Exception {
    JsonNode p = create();
    String name = p.path("name").asString();
    byte[] bytes =
        archive(
            name,
            "Example",
            command ? "Run curl https://example.com" : "Reference documentation only.",
            "SKILL.md");
    var manifest =
        JSON.createObjectNode()
            .put("schemaVersion", 1)
            .put("kind", "skill")
            .put("name", name)
            .put("version", "1.0.0")
            .put("description", "Example")
            .put("license", "MIT");
    manifest.set("author", JSON.createObjectNode().put("displayName", "Example"));
    var permission = JSON.createObjectNode();
    permission.putArray("tools");
    permission.putArray("outboundHosts");
    permission.putArray("dataAccess");
    manifest.set("permissions", permission);
    manifest.putArray("runtimes");
    manifest.set(
        "skill",
        JSON.createObjectNode()
            .put("entry", "SKILL.md")
            .put("contentProfile", "text-only")
            .put("dshVersionRange", "*"));
    manifest.set(
        "signature", JSON.createObjectNode().put("mode", "detached").put("algorithm", "Ed25519"));
    manifest
        .putArray("artifacts")
        .add(
            JSON.createObjectNode()
                .put("id", "payload")
                .put("role", "payload")
                .put("digest", CatalogManifest.digest(bytes))
                .put("sizeBytes", bytes.length)
                .put("mediaType", "application/gzip")
                .put("os", "any")
                .put("arch", "any"));
    byte[] raw = (" \n" + manifest + "\n").getBytes(StandardCharsets.UTF_8);
    UUID id = UUID.fromString(p.path("id").asString());
    var value =
        catalog.upload(
            actor(AUTHOR),
            id,
            JSON.createObjectNode()
                .put("version", "1.0.0")
                .put("expectedDigest", CatalogManifest.digest(raw)),
            raw,
            List.of(new ByteArrayInputStream(bytes)),
            key(),
            etag(p));
    return new Upload(id, UUID.fromString(value.path("id").asString()), value, raw, bytes);
  }

  static byte[] archive(String name, String description, String content, String path)
      throws Exception {
    var out = new ByteArrayOutputStream();
    try (var gzip = new java.util.zip.GZIPOutputStream(out);
        var tar = new TarArchiveOutputStream(gzip)) {
      byte[] text =
          ("---\nname: " + name + "\ndescription: " + description + "\n---\n" + content + "\n")
              .getBytes(StandardCharsets.UTF_8);
      var e = new TarArchiveEntry(path);
      e.setMode(0644);
      e.setSize(text.length);
      tar.putArchiveEntry(e);
      tar.write(text);
      tar.closeArchiveEntry();
    }
    return out.toByteArray();
  }

  JsonNode submit(Upload u) {
    return catalog.submit(actor(AUTHOR), u.pkg, u.version, reason(), key(), etag(u.value));
  }

  JsonNode decide(JsonNode review, UUID reviewer, String role) {
    var v = review.path("version");
    return catalog.decide(
        actor(reviewer),
        UUID.fromString(review.path("reviewId").asString()),
        JSON.createObjectNode()
            .put("decision", "approve")
            .put("reason", "checked")
            .put("role", role)
            .put("versionRevision", v.path("revision").asLong())
            .put("digest", v.path("digest").asString()),
        key(),
        etag(review));
  }

  JsonNode publish(Upload u, JsonNode review) {
    return catalog.publish(
        actor(PUBLISHER), u.pkg, u.version, reason(), key(), etag(review.path("version")));
  }

  @Test
  void rawBytesR1PublicationOutboxAndWriteRollback() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false);
            assertEquals("r1", u.value.path("risk").path("minimumLevel").asString());
            assertArrayEquals(
                u.manifest,
                catalog.download(actor(AUTHOR), u.pkg, u.version, null, false).readAllBytes());
            var review = decide(submit(u), GENERAL, "general");
            assertEquals("approved", review.path("status").asString());
            var published = publish(u, review);
            assertEquals("published", published.path("state").asString());
            assertEquals(
                CatalogManifest.digest(u.manifest),
                published
                    .path("resourceEnvelope")
                    .path("statement")
                    .path("manifestDigest")
                    .asString());
            assertEquals(
                List.of(CatalogManifest.digest(u.payload)),
                JSON.convertValue(
                    published.path("resourceEnvelope").path("statement").path("artifactDigests"),
                    List.class));
            assertEquals(
                1,
                tx.read(
                    () ->
                        db.rows(
                                "SELECT id FROM resource.outbox_event WHERE tenant_id=:tenant AND event_type='resource.catalog_version_published' AND subject_key=:p",
                                db.args("p", u.pkg.toString()))
                            .size()));
            assertError(
                CommonErrors.PRECONDITION_FAILED,
                () ->
                    catalog.upload(
                        actor(AUTHOR),
                        u.pkg,
                        JSON.createObjectNode()
                            .put("version", "1.0.0")
                            .put("expectedDigest", CatalogManifest.digest(u.manifest)),
                        u.manifest,
                        List.of(new ByteArrayInputStream(u.payload)),
                        key(),
                        "\"1\""));
            enabled.set(false);
            assertError(CommonErrors.UNAVAILABLE, this::create);
            assertEquals(
                "published",
                catalog.detail(actor(AUTHOR), u.pkg, u.version, false).path("state").asString());
            assertArrayEquals(
                u.payload,
                catalog
                    .download(actor(GENERAL), u.pkg, u.version, "payload", false)
                    .readAllBytes());
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void r2DistinctReviewersQualificationRevokedBeforePublication() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(true);
            assertEquals("r2", u.value.path("risk").path("minimumLevel").asString());
            var review = submit(u);
            JsonNode initial = review;
            assertError(CommonErrors.FORBIDDEN, () -> decide(initial, AUTHOR, "general"));
            assertError(CommonErrors.FORBIDDEN, () -> decide(initial, GENERAL, "security"));
            review = decide(review, SECURITY, "security");
            assertEquals("pending", review.path("status").asString());
            review = decide(review, SECURITY, "security");
            assertEquals("pending", review.path("status").asString());
            review = decide(review, GENERAL, "general");
            assertEquals("approved", review.path("status").asString());
            security.remove(SECURITY);
            JsonNode approved = review;
            assertError(
                auriga.common.error.ErrorCode.of(
                    "resource.review_required", 409, io.grpc.Status.Code.FAILED_PRECONDITION),
                () -> publish(u, approved));
            security.add(SECURITY);
            assertEquals("published", publish(u, review).path("state").asString());
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void tenantIsolationIdempotencyAndPluginNonExposure() {
    TenantContext.run(
        TENANT,
        () -> {
          var body =
              JSON.createObjectNode()
                  .put("kind", "skill")
                  .put("name", "skill-" + key())
                  .put("visibility", "tenant");
          String key = key();
          var p = catalog.create(actor(AUTHOR), body, key, "\"0\"");
          assertEquals(
              ResourceSigning.canonical(p),
              ResourceSigning.canonical(catalog.create(actor(AUTHOR), body, key, "\"0\"")));
          assertError(
              CommonErrors.CONFLICT,
              () ->
                  catalog.create(
                      actor(AUTHOR), body.deepCopy().put("visibility", "members"), key, "\"0\""));
          assertError(
              CommonErrors.VALIDATION_FAILED,
              () ->
                  catalog.create(
                      actor(AUTHOR), body.deepCopy().put("kind", "plugin"), key(), "\"0\""));
          UUID legacyId = UuidV7.next();
          tx.write(
              () ->
                  db.update(
                      "INSERT INTO resource.resource_package(id,tenant_id,owner_id,name,level,visibility,enabled,revision,created_at) VALUES(:id,:tenant,:o,'@example/plugin','l4','private',false,1,now())",
                      db.args("id", legacyId, "o", AUTHOR)));
          assertError(
              CommonErrors.NOT_FOUND, () -> catalog.detail(actor(AUTHOR), legacyId, null, false));
          assertEquals(
              1,
              tx.read(
                  () ->
                      db.rows(
                              "SELECT * FROM resource.catalog_plugin_projection WHERE tenant_id=:tenant AND package_id=:id",
                              db.args("id", legacyId))
                          .size()));
          UUID other = UUID.randomUUID();
          TenantContext.run(
              other,
              () ->
                  assertEquals(
                      0,
                      tx.read(
                          () ->
                              db.rows(
                                      "SELECT * FROM resource.catalog_package WHERE id=:id",
                                      db.args("id", UUID.fromString(p.path("id").asString())))
                                  .size())));
        });
  }

  @Test
  void rejectsPathAndNetworkSemantics() {
    for (String path : List.of("a//b", "a/../b", "a./b", "a /b", "CON.txt", "/absolute", "a\\b"))
      assertError(CommonErrors.VALIDATION_FAILED, () -> CatalogManifest.path(path));
    for (String host :
        List.of(
            "https://example.com",
            "example.com:80",
            "x@example.com",
            "*.example.com",
            "EXAMPLE.com",
            "01.2.3.4"))
      assertError(CommonErrors.VALIDATION_FAILED, () -> CatalogManifest.host(host));
    CatalogManifest.host("127.0.0.1");
    CatalogManifest.host("example.com");
    CatalogManifest.url("http://192.0.2.1/path");
    for (String url : List.of("https://u:p@example.com", "https://example.com/?token=secret"))
      assertError(CommonErrors.VALIDATION_FAILED, () -> CatalogManifest.url(url));
  }

  CatalogDelivery delivery(auriga.resource.api.UnifiedResourceWorkspace workspace) {
    var distributions = mock(ResourceDistributions.class);
    when(distributions.revocations())
        .thenAnswer(
            call -> {
              var existing = db.revocations();
              if (existing != null) return existing;
              var n =
                  JSON.createObjectNode()
                      .put("schemaVersion", 1)
                      .put("tenantId", TENANT.toString())
                      .put("revision", 1)
                      .put("issuedAt", clock.instant().toString())
                      .put("expiresAt", clock.instant().plusSeconds(86400).toString());
              n.putArray("revokedKids");
              n.putArray("revokedArtifacts");
              return signing.sign(n, true);
            });
    var directory = mock(ResourceWorkspace.class);
    when(directory.tenants()).thenReturn(List.of(TENANT));
    return new CatalogDelivery(
        tx,
        db,
        access,
        catalog,
        distributions,
        () -> workspace,
        new CatalogEvents(
            new OutboxEventPublisher(jdbc, "resource", "resource", null, JSON, clock, () -> {})),
        clock,
        new ResourceCursor(new byte[32], clock),
        () -> directory);
  }

  ObjectNode identity(Upload u) {
    return JSON.createObjectNode()
        .put("tenantId", TENANT.toString())
        .put("packageId", u.pkg.toString())
        .put("versionId", u.version.toString())
        .put("kind", "skill")
        .put("version", "1.0.0")
        .put("manifestDigest", CatalogManifest.digest(u.manifest));
  }

  @Test
  void memberTicketsReportsAndMaliciousWithdrawal() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false);
            publish(u, decide(submit(u), GENERAL, "general"));
            var d = delivery(null);
            UUID client = UUID.randomUUID();
            var selection = JSON.createObjectNode().put("clientId", client.toString());
            selection.set(
                "selection",
                JSON.createObjectNode().put("action", "install").set("identity", identity(u)));
            var installed = d.select(actor(GENERAL), selection, key(), "\"0\"");
            assertEquals("applying", installed.path("state").asString());
            var ticketBody =
                JSON.createObjectNode()
                    .put("clientId", client.toString())
                    .put("installationRevision", 1);
            ticketBody.set("identity", identity(u));
            var ticket = d.ticket(actor(GENERAL), ticketBody, key(), "\"1\"");
            var download =
                JSON.createObjectNode()
                    .put("clientId", client.toString())
                    .put("ticket", ticket.path("ticket").asString())
                    .put("artifactId", "payload");
            assertArrayEquals(
                u.payload,
                d.download(actor(GENERAL), download, key(), "\"1\"", false).readAllBytes());
            assertError(
                CommonErrors.NOT_FOUND,
                () -> d.download(actor(PUBLISHER), download, key(), "\"1\"", false));
            var report =
                ticketBody
                    .deepCopy()
                    .put("selectionRevision", 1)
                    .put("generation", 1)
                    .put("observedGeneration", 2)
                    .put("reportSource", "member")
                    .put("observedAt", clock.instant().toString())
                    .put("state", "applied");
            assertError(
                CatalogStatePolicy.conflict().errorCode(),
                () -> d.report(actor(GENERAL), report, key(), "\"1\""));
            report.put("observedGeneration", 1);
            assertEquals(
                "applied",
                d.report(actor(GENERAL), report, key(), "\"1\"").path("state").asString());
            var renew = ticketBody.deepCopy().put("selectionRevision", 1).put("releaseSequence", 1);
            var renewed = d.renew(actor(GENERAL), renew, key(), "\"1\"");
            assertEquals(identity(u), renewed.path("resource").path("identity"));
            assertTrue(renewed.has("ticket"));
            var current = catalog.detail(actor(AUTHOR), u.pkg, u.version, false);
            catalog.withdraw(
                actor(PUBLISHER),
                u.pkg,
                u.version,
                reason().put("mode", "malicious"),
                key(),
                etag(current));
            assertError(
                CommonErrors.NOT_FOUND,
                () -> d.download(actor(GENERAL), download, key(), "\"1\"", false));
            assertError(
                CommonErrors.NOT_FOUND,
                () -> catalog.download(actor(AUTHOR), u.pkg, u.version, "payload", false));
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void distributionPinsBytesAndWaitsForTrustedNodeObservation() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false);
            publish(u, decide(submit(u), GENERAL, "general"));
            UUID instance = UUID.randomUUID(), node = UUID.randomUUID();
            var workspace = mock(UnifiedResourceWorkspace.class);
            when(workspace.snapshot(TENANT, instance))
                .thenReturn(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance, GENERAL, node, 1, 2, 1, false, List.of(), List.of()));
            var d = delivery(workspace);
            var body = JSON.createObjectNode().put("instanceId", instance.toString());
            body.putArray("selections")
                .add(JSON.createObjectNode().put("action", "install").set("identity", identity(u)));
            String key = key();
            var intent = d.distribute(actor(PUBLISHER), body, key, "\"1\"");
            assertEquals("applying", intent.path("state").asString());
            assertEquals(
                ResourceSigning.canonical(intent),
                ResourceSigning.canonical(d.distribute(actor(PUBLISHER), body, key, "\"1\"")));
            var request =
                auriga.contracts.resource.v1.ResolveUnifiedResourcesRequest.newBuilder()
                    .setTenantId(TENANT.toString())
                    .setOwnerId(GENERAL.toString())
                    .setInstanceId(instance.toString())
                    .setNodeId(node.toString())
                    .setSelectionRevision(2)
                    .addSelections(CatalogProtocol.selection(body.path("selections").get(0)))
                    .build();
            var resolution = d.resolve(request);
            assertEquals(
                CatalogManifest.digest(u.manifest),
                resolution.getResources(0).getIdentity().getManifestDigest());
            assertEquals(resolution, d.resolve(request));
            var report =
                auriga.contracts.resource.v1.UnifiedInstallationReport.newBuilder()
                    .setIdentity(resolution.getResources(0).getIdentity())
                    .setClientId(instance.toString())
                    .setInstallationRevision(1)
                    .setSelectionRevision(2)
                    .setGeneration(3)
                    .setObservedGeneration(2)
                    .setState("applied")
                    .setReportSource("node")
                    .setObservedAt(clock.instant().toString())
                    .build();
            when(workspace.snapshot(TENANT, instance))
                .thenReturn(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance,
                        GENERAL,
                        node,
                        2,
                        3,
                        2,
                        true,
                        List.of(report),
                        List.of(resolution.getResources(0))));
            assertEquals(
                "applying",
                d.distribution(
                        actor(PUBLISHER), UUID.fromString(intent.path("distributionId").asString()))
                    .path("state")
                    .asString());
            report = report.toBuilder().setObservedGeneration(3).build();
            when(workspace.snapshot(TENANT, instance))
                .thenReturn(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance,
                        GENERAL,
                        node,
                        2,
                        3,
                        3,
                        true,
                        List.of(report),
                        List.of(resolution.getResources(0))));
            assertEquals(
                "applied",
                d.distribution(
                        actor(PUBLISHER), UUID.fromString(intent.path("distributionId").asString()))
                    .path("state")
                    .asString());
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void riskRaiseInvalidatesPreviousRevisionAndBadUploadClosesStreams() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false);
            var review = decide(submit(u), GENERAL, "general");
            var original = review.path("version");
            var raise =
                JSON.createObjectNode()
                    .put("decision", "approve")
                    .put("role", "security")
                    .put("reason", "command review required")
                    .put("digest", original.path("digest").asString())
                    .put("versionRevision", original.path("revision").asLong())
                    .put("riskLevel", "r2");
            var raised =
                catalog.decide(
                    actor(SECURITY),
                    UUID.fromString(review.path("reviewId").asString()),
                    raise,
                    key(),
                    etag(review));
            assertEquals("pending", raised.path("status").asString());
            assertEquals("r2", raised.path("version").path("risk").path("level").asString());
            assertError(
                CommonErrors.PRECONDITION_FAILED,
                () ->
                    catalog.decide(
                        actor(SECURITY),
                        UUID.fromString(raised.path("reviewId").asString()),
                        raise,
                        key(),
                        etag(raised)));
            var lower =
                raise
                    .deepCopy()
                    .put("versionRevision", raised.path("version").path("revision").asLong())
                    .put("riskLevel", "r1");
            assertError(
                CommonErrors.VALIDATION_FAILED,
                () ->
                    catalog.decide(
                        actor(SECURITY),
                        UUID.fromString(raised.path("reviewId").asString()),
                        lower,
                        key(),
                        etag(raised)));
            var first = decide(raised, SECURITY, "security");
            assertEquals("pending", first.path("status").asString());
            assertEquals("approved", decide(first, GENERAL, "general").path("status").asString());
            var closed = new AtomicBoolean();
            var stream =
                new ByteArrayInputStream(u.payload) {
                  @Override
                  public void close() throws IOException {
                    closed.set(true);
                    super.close();
                  }
                };
            assertError(
                CommonErrors.VALIDATION_FAILED,
                () ->
                    catalog.upload(
                        actor(AUTHOR),
                        u.pkg,
                        JSON.createObjectNode()
                            .put("version", "9.0.0")
                            .put("expectedDigest", CatalogManifest.digest(u.manifest)),
                        u.manifest,
                        List.of(stream),
                        key(),
                        "\"2\""));
            assertTrue(closed.get());
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void maliciousArtifactReuseWithdrawsEveryReferencingVersionAndPreservesHighWater()
      throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var first = upload(false);
            publish(first, decide(submit(first), GENERAL, "general"));
            var m = (ObjectNode) JSON.readTree(first.manifest);
            m.put("version", "1.0.1");
            byte[] raw = JSON.writeValueAsBytes(m);
            var pack = catalog.detail(actor(AUTHOR), first.pkg, null, false);
            var next =
                catalog.upload(
                    actor(AUTHOR),
                    first.pkg,
                    JSON.createObjectNode()
                        .put("version", "1.0.1")
                        .put("expectedDigest", CatalogManifest.digest(raw)),
                    raw,
                    List.of(new ByteArrayInputStream(first.payload)),
                    key(),
                    etag(pack));
            var second =
                new Upload(
                    first.pkg,
                    UUID.fromString(next.path("id").asString()),
                    next,
                    raw,
                    first.payload);
            publish(second, decide(submit(second), GENERAL, "general"));
            var old = catalog.detail(actor(AUTHOR), first.pkg, first.version, false);
            catalog.withdraw(
                actor(PUBLISHER),
                first.pkg,
                first.version,
                reason().put("mode", "malicious"),
                key(),
                etag(old));
            assertEquals(
                "withdrawn",
                catalog
                    .detail(actor(AUTHOR), second.pkg, second.version, false)
                    .path("state")
                    .asString());
            assertEquals(
                2L,
                tx.read(
                    () ->
                        db.row(
                                "SELECT release_high_water FROM resource.catalog_package WHERE tenant_id=:tenant AND id=:id",
                                db.args("id", first.pkg))
                            .get("release_high_water")));
            assertTrue(
                tx.read(
                        () ->
                            db.rows(
                                    "SELECT * FROM resource.resource_revoked_artifact WHERE tenant_id=:tenant AND package_id=:id",
                                    db.args("id", first.pkg))
                                .size())
                    >= 4);
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  ObjectNode selection(Upload u, String action) {
    var n = JSON.createObjectNode().put("action", action);
    n.set("identity", identity(u));
    return n;
  }

  ObjectNode memberSelection(Upload u, UUID client, String action) {
    var n = JSON.createObjectNode().put("clientId", client.toString());
    n.set("selection", selection(u, action));
    return n;
  }

  ObjectNode memberReport(JsonNode installed, String state) {
    var n =
        JSON.createObjectNode()
            .put("clientId", installed.path("clientId").asString())
            .put("installationRevision", installed.path("installationRevision").asLong())
            .put("selectionRevision", installed.path("selectionRevision").asLong())
            .put("generation", installed.path("generation").asLong())
            .put("observedGeneration", installed.path("generation").asLong())
            .put("reportSource", "member")
            .put("observedAt", clock.instant().toString())
            .put("state", state);
    n.set("identity", installed.path("resource").path("identity"));
    return n;
  }

  @Test
  void memberRemovalAcknowledgementAndReinstallKeepPersistentCas() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false);
            publish(u, decide(submit(u), GENERAL, "general"));
            var d = delivery(null);
            UUID client = UUID.randomUUID();
            var installed =
                d.select(actor(GENERAL), memberSelection(u, client, "install"), key(), "\"0\"");
            assertEquals(1, installed.path("resource").path("installationRevision").asLong());
            String removeKey = key();
            var remove = memberSelection(u, client, "remove");
            var tombstone = d.select(actor(GENERAL), remove, removeKey, "\"1\"");
            assertEquals("applying", tombstone.path("state").asString());
            assertEquals("REMOVED", tombstone.path("resource").path("desiredState").asString());
            assertEquals(2, tombstone.path("resource").path("installationRevision").asLong());
            assertFalse(tombstone.path("resource").path("enabled").asBoolean());
            assertEquals(
                ResourceSigning.canonical(tombstone),
                ResourceSigning.canonical(d.select(actor(GENERAL), remove, removeKey, "\"1\"")));
            assertError(
                CatalogStatePolicy.conflict().errorCode(),
                () -> d.report(actor(GENERAL), memberReport(tombstone, "applied"), key(), "\"2\""));
            assertError(
                CatalogStatePolicy.conflict().errorCode(),
                () ->
                    d.report(actor(GENERAL), memberReport(tombstone, "disabled"), key(), "\"2\""));
            var removed =
                d.report(actor(GENERAL), memberReport(tombstone, "removed"), key(), "\"2\"");
            assertEquals("removed", removed.path("state").asString());
            var history =
                d.installations(actor(GENERAL), client, null, 50, null).path("items").get(0);
            assertEquals(2, history.path("installationRevision").asLong());
            assertEquals(
                tombstone.path("generation").asLong() + 1, history.path("generation").asLong());
            assertError(
                CatalogStatePolicy.conflict().errorCode(),
                () -> d.report(actor(GENERAL), memberReport(history, "removed"), key(), "\"2\""));
            var reinstalled =
                d.select(actor(GENERAL), memberSelection(u, client, "install"), key(), "\"2\"");
            assertEquals(3, reinstalled.path("resource").path("installationRevision").asLong());
            assertEquals("ENABLED", reinstalled.path("resource").path("desiredState").asString());
            assertError(
                CatalogStatePolicy.conflict().errorCode(),
                () -> d.report(actor(GENERAL), memberReport(tombstone, "removed"), key(), "\"3\""));
            var renew =
                JSON.createObjectNode()
                    .put("clientId", client.toString())
                    .put("installationRevision", 3)
                    .put("selectionRevision", reinstalled.path("selectionRevision").asLong())
                    .put("releaseSequence", 1);
            renew.set("identity", identity(u));
            var renewed = d.renew(actor(GENERAL), renew, key(), "\"3\"");
            assertEquals(3, renewed.path("resource").path("installationRevision").asLong());
            assertEquals(
                "applied",
                d.report(actor(GENERAL), memberReport(reinstalled, "applied"), key(), "\"3\"")
                    .path("state")
                    .asString());
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  auriga.contracts.resource.v1.ResolveUnifiedResourcesRequest resolveRequest(
      UUID instance, UUID node, long revision, JsonNode selections) {
    var request =
        auriga.contracts.resource.v1.ResolveUnifiedResourcesRequest.newBuilder()
            .setTenantId(TENANT.toString())
            .setOwnerId(GENERAL.toString())
            .setInstanceId(instance.toString())
            .setNodeId(node.toString())
            .setSelectionRevision(revision);
    selections.forEach(selection -> request.addSelections(CatalogProtocol.selection(selection)));
    return request.build();
  }

  ObjectNode distributionBody(UUID instance, ObjectNode... selections) {
    var body = JSON.createObjectNode().put("instanceId", instance.toString());
    var values = body.putArray("selections");
    for (var s : selections) values.add(s);
    return body;
  }

  @Test
  void cloudTombstonesRequireExpectedRevisionAndRetireOnlyNextGeneration() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false);
            publish(u, decide(submit(u), GENERAL, "general"));
            UUID instance = UUID.randomUUID(), node = UUID.randomUUID();
            var workspace = mock(UnifiedResourceWorkspace.class);
            var current =
                new java.util.concurrent.atomic.AtomicReference<>(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance, GENERAL, node, 1, 2, 2, true, List.of(), List.of()));
            when(workspace.snapshot(TENANT, instance)).thenAnswer(call -> current.get());
            var d = delivery(workspace);
            var install = distributionBody(instance, selection(u, "install"));
            d.distribute(actor(PUBLISHER), install, key(), "\"1\"");
            var initial = d.resolve(resolveRequest(instance, node, 2, install.path("selections")));
            current.set(
                new UnifiedResourceWorkspace.Snapshot(
                    instance, GENERAL, node, 2, 3, 3, true, List.of(), initial.getResourcesList()));
            var remove = distributionBody(instance, selection(u, "remove"));
            String removalKey = key();
            var intent = d.distribute(actor(PUBLISHER), remove, removalKey, "\"2\"");
            var removal = d.resolve(resolveRequest(instance, node, 3, remove.path("selections")));
            var tombstone = removal.getResources(0);
            assertEquals(2, tombstone.getInstallationRevision());
            assertEquals("REMOVED", CatalogStatePolicy.desired(tombstone));
            var report =
                auriga.contracts.resource.v1.UnifiedInstallationReport.newBuilder()
                    .setIdentity(tombstone.getIdentity())
                    .setClientId(instance.toString())
                    .setInstallationRevision(2)
                    .setSelectionRevision(3)
                    .setGeneration(4)
                    .setObservedGeneration(4)
                    .setState("applied")
                    .setReportSource("node")
                    .setObservedAt(clock.instant().toString())
                    .build();
            current.set(
                new UnifiedResourceWorkspace.Snapshot(
                    instance, GENERAL, node, 3, 4, 4, true, List.of(report), List.of(tombstone)));
            assertEquals(
                "applying",
                d.distribution(
                        actor(PUBLISHER), UUID.fromString(intent.path("distributionId").asString()))
                    .path("state")
                    .asString());
            d.reconcile();
            verify(workspace, never()).refresh(any(), any(), any(), anyLong(), any());
            report = report.toBuilder().setState("removed").build();
            current.set(
                new UnifiedResourceWorkspace.Snapshot(
                    instance, GENERAL, node, 3, 4, 4, true, List.of(report), List.of()));
            assertEquals(
                "applying",
                d.distribution(
                        actor(PUBLISHER), UUID.fromString(intent.path("distributionId").asString()))
                    .path("state")
                    .asString());
            d.reconcile();
            verify(workspace, never())
                .refresh(
                    any(),
                    any(),
                    any(),
                    anyLong(),
                    argThat(
                        (auriga.contracts.resource.v1.ResolveUnifiedResourcesResponse response) ->
                            response.getResourcesCount() == 0));
            var acknowledged =
                new UnifiedResourceWorkspace.Snapshot(
                    instance, GENERAL, node, 3, 4, 4, true, List.of(report), List.of(tombstone));
            current.set(acknowledged);
            when(workspace.refresh(eq(TENANT), eq(instance), eq(GENERAL), eq(3L), any()))
                .thenAnswer(
                    call -> {
                      var resolution =
                          (auriga.contracts.resource.v1.ResolveUnifiedResourcesResponse)
                              call.getArgument(4);
                      assertEquals(0, resolution.getResourcesCount());
                      var next =
                          new UnifiedResourceWorkspace.Snapshot(
                              instance,
                              GENERAL,
                              node,
                              3,
                              5,
                              5,
                              true,
                              List.of(),
                              resolution.getResourcesList());
                      current.set(next);
                      return next;
                    });
            d.reconcile();
            assertEquals(1, acknowledged.resources().size());
            assertEquals(4, acknowledged.generation());
            assertEquals(
                0,
                d.resolve(resolveRequest(instance, node, 3, remove.path("selections")))
                    .getResourcesCount());
            assertEquals(
                "removed",
                d.distribution(
                        actor(PUBLISHER), UUID.fromString(intent.path("distributionId").asString()))
                    .path("state")
                    .asString());
            var reinstall = distributionBody(instance, selection(u, "install"));
            d.distribute(actor(PUBLISHER), reinstall, key(), "\"3\"");
            var again = d.resolve(resolveRequest(instance, node, 4, reinstall.path("selections")));
            assertEquals(3, again.getResources(0).getInstallationRevision());
            current.set(
                new UnifiedResourceWorkspace.Snapshot(
                    instance,
                    GENERAL,
                    node,
                    4,
                    6,
                    6,
                    true,
                    List.of(report),
                    again.getResourcesList()));
            var other = upload(false);
            publish(other, decide(submit(other), GENERAL, "general"));
            var batch =
                distributionBody(instance, selection(u, "enable"), selection(other, "install"));
            d.distribute(actor(PUBLISHER), batch, key(), "\"4\"");
            var resources =
                d.resolve(resolveRequest(instance, node, 5, batch.path("selections")))
                    .getResourcesList();
            assertEquals(2, resources.size());
            assertEquals(
                4,
                resources.stream()
                    .filter(x -> x.getIdentity().getPackageId().equals(u.pkg.toString()))
                    .findFirst()
                    .orElseThrow()
                    .getInstallationRevision());
            assertEquals(
                1,
                resources.stream()
                    .filter(x -> x.getIdentity().getPackageId().equals(other.pkg.toString()))
                    .findFirst()
                    .orElseThrow()
                    .getInstallationRevision());
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }
}
