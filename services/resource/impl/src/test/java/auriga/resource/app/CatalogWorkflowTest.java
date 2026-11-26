package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;
import static auriga.resource.web.ResourceBody.uuid;
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
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.ValueSource;
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
  ResourceAccess access;
  AtomicBoolean enabled = new AtomicBoolean(true);
  Set<UUID> security = new HashSet<>();
  Map<UUID, Set<UUID>> groups = new HashMap<>();
  Set<UUID> knownGroups = new HashSet<>();
  Set<UUID> externalMembers = new HashSet<>();
  AtomicBoolean personalAllowed = new AtomicBoolean(true);
  ResourceSigning signing;
  KeyPair releaseKey, revocationKey, downgradeKey;
  java.util.concurrent.atomic.AtomicReference<Instant> now =
      new java.util.concurrent.atomic.AtomicReference<>(Instant.parse("2026-11-13T00:00:00Z"));
  Clock clock =
      new Clock() {
        public ZoneId getZone() {
          return ZoneOffset.UTC;
        }

        public Clock withZone(ZoneId zone) {
          return this;
        }

        public Instant instant() {
          return now.get();
        }
      };

  void advance(long seconds) {
    now.updateAndGet(value -> value.plusSeconds(seconds));
  }

  void reconcile(CatalogDelivery delivery) {
    advance(6);
    delivery.reconcile();
  }

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
    TenantContext.run(
        TENANT,
        () ->
            tx.write(
                () ->
                    db.update(
                        "UPDATE resource.catalog_assignment SET active=false WHERE tenant_id=:tenant",
                        db.args())));
    store = new LocalObjectStore(objects);
    security.add(SECURITY);
    access =
        new ResourceAccess(
            new ResourceAccessInputs() {
              public Member member(UUID t, UUID a) {
                return Member.newBuilder()
                    .setTenantId(t.toString())
                    .setAccountId(a.toString())
                    .setMemberType(
                        externalMembers.contains(a)
                            ? MemberType.MEMBER_TYPE_EXTERNAL
                            : MemberType.MEMBER_TYPE_MEMBER)
                    .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                    .addAllGroupIds(
                        groups.getOrDefault(a, Set.of()).stream().map(UUID::toString).toList())
                    .build();
              }

              public GetEffectivePermissionsResponse permissions(UUID t, UUID a) {
                var b =
                    GetEffectivePermissionsResponse.newBuilder()
                        .setMemberType(
                            externalMembers.contains(a)
                                ? MemberType.MEMBER_TYPE_EXTERNAL
                                : MemberType.MEMBER_TYPE_MEMBER)
                        .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE);
                for (String p : PERMS)
                  if (!p.equals(CatalogReviewPolicy.permission()) || security.contains(a))
                    b.addPermissions(p);
                return b.build();
              }

              public boolean allowPersonalPlugins(UUID t) {
                return personalAllowed.get();
              }

              public boolean groupExists(UUID t, UUID group) {
                return knownGroups.contains(group);
              }
            });
    var keys = KeyPairGenerator.getInstance("Ed25519");
    releaseKey = keys.generateKeyPair();
    revocationKey = keys.generateKeyPair();
    downgradeKey = keys.generateKeyPair();
    signing =
        new ResourceSigning(
            "release",
            releaseKey.getPrivate(),
            "revoke",
            revocationKey.getPrivate(),
            "downgrade",
            downgradeKey.getPrivate());
    var events =
        new OutboxEventPublisher(
            jdbc, "resource", "resource", new EventSchemas(), JSON, clock, () -> {});
    var operations = new ResourceOperations(tx, db, access, events, clock);
    var catalogEvents =
        new CatalogEvents(
            new OutboxEventPublisher(jdbc, "resource", "resource", null, JSON, clock, () -> {}));
    catalog =
        new CatalogWorkflow(
            tx,
            db,
            access,
            operations,
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
    return actor(id, TENANT);
  }

  AurigaPrincipal actor(UUID id, UUID tenant) {
    return new AurigaPrincipal(
        id.toString(),
        tenant,
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

  JsonNode approvePublish(Upload upload) {
    var review = decide(submit(upload), GENERAL, "general");
    if (upload.value.path("kind").asString().equals("plugin"))
      review = decide(review, SECURITY, "security");
    return publish(upload, review);
  }

  JsonNode assign(CatalogDelivery delivery, Upload upload, String scope, UUID target, String mode) {
    var body =
        JSON.createObjectNode()
            .put("scope", scope)
            .put("targetId", target.toString())
            .put("installationMode", mode);
    body.set("identity", identity(upload));
    return delivery.createAssignment(actor(PUBLISHER), body, key(), "\"0\"");
  }

  ObjectNode choose(UUID client, Upload upload, String action) {
    var body = JSON.createObjectNode().put("clientId", client.toString());
    body.set(
        "selection",
        JSON.createObjectNode().put("action", action).set("identity", identity(upload)));
    return body;
  }

  Upload nextSkillVersion(Upload first, String version) {
    var manifest = (ObjectNode) first.value.path("manifest").deepCopy();
    manifest.put("version", version);
    byte[] raw = manifest.toString().getBytes(StandardCharsets.UTF_8);
    var value =
        catalog.upload(
            actor(AUTHOR),
            first.pkg,
            JSON.createObjectNode()
                .put("version", version)
                .put("expectedDigest", CatalogManifest.digest(raw)),
            raw,
            List.of(new ByteArrayInputStream(first.payload)),
            key(),
            etag(catalog.detail(actor(AUTHOR), first.pkg, null, false)));
    var next = new Upload(first.pkg, uuid(value, "id"), value, raw, first.payload);
    approvePublish(next);
    return next;
  }

  @Test
  void removingInactiveAssignmentDoesNotAdvanceRevisionOrEmitAnotherEvent() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          var upload = upload(false);
          approvePublish(upload);
          var delivery = delivery(null);
          var rule = assign(delivery, upload, "member", GENERAL, "required");
          assertEquals(rule, delivery.assignment(actor(PUBLISHER), uuid(rule, "id")));
          assertEquals(
              rule, tx.read(() -> delivery.assignment(actor(PUBLISHER), uuid(rule, "id"))));
          var first = delivery.removeAssignment(actor(PUBLISHER), uuid(rule, "id"), key(), "\"1\"");
          assertEquals(first, delivery.assignment(actor(PUBLISHER), uuid(rule, "id")));
          UUID otherTenant = UUID.randomUUID();
          TenantContext.run(
              otherTenant,
              () ->
                  assertError(
                      CommonErrors.NOT_FOUND,
                      () -> delivery.assignment(actor(PUBLISHER, otherTenant), uuid(rule, "id"))));
          long events =
              tx.read(
                  () ->
                      ((Number)
                              db.row(
                                      "SELECT count(*) AS n FROM resource.outbox_event WHERE tenant_id=:tenant",
                                      db.args())
                                  .get("n"))
                          .longValue());
          var second =
              delivery.removeAssignment(actor(PUBLISHER), uuid(rule, "id"), key(), "\"2\"");
          assertEquals(first, second);
          assertEquals(
              events,
              tx.read(
                  () ->
                      ((Number)
                              db.row(
                                      "SELECT count(*) AS n FROM resource.outbox_event WHERE tenant_id=:tenant",
                                      db.args())
                                  .get("n"))
                          .longValue()));
          return null;
        });
  }

  @ParameterizedTest
  @ValueSource(booleans = {true, false})
  void memberReconciliationIsolatesOnePackageAndRecoversAfterFailure(boolean installed)
      throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          var bad = upload(false);
          var good = upload(false);
          approvePublish(bad);
          approvePublish(good);
          var delivery = delivery(null);
          UUID client = UUID.randomUUID();
          if (installed)
            delivery.select(actor(GENERAL), choose(client, bad, "install"), key(), "\"0\"");
          delivery.select(actor(GENERAL), choose(client, good, "install"), key(), "\"0\"");
          assign(delivery, bad, "member", GENERAL, "required");
          assign(delivery, good, "member", GENERAL, "required");
          var owner = new JdbcTemplate(roles.owner().dataSource());
          String constraint = "fixture_" + UUID.randomUUID().toString().replace("-", "");
          owner.execute(
              "ALTER TABLE resource.catalog_installation ADD CONSTRAINT "
                  + constraint
                  + " CHECK(package_id <> '"
                  + bad.pkg
                  + "'::uuid OR revision < "
                  + (installed ? 2 : 1)
                  + ")");
          try {
            var items =
                delivery.installations(actor(GENERAL), client, "skill", 50, null).path("items");
            assertEquals(installed ? 2 : 1, items.size());
            for (var item : items) {
              boolean failed = item.path("resource").path("identity").equals(identity(bad));
              assertEquals("applying", item.path("state").asString());
              assertEquals(
                  failed ? "optional" : "required",
                  item.path("resource").path("installationMode").asString());
            }
            var failures =
                delivery
                    .installations(actor(GENERAL), client, "skill", 50, null)
                    .path("assignmentFailures");
            assertEquals(1, failures.size());
            assertEquals(bad.pkg.toString(), failures.get(0).path("packageId").asString());
            assertTrue(
                delivery
                    .installations(actor(AUTHOR), client, "skill", 50, null)
                    .path("assignmentFailures")
                    .isEmpty());
            assertTrue(
                TenantContext.call(
                        UUID.randomUUID(),
                        () ->
                            tx.read(
                                () ->
                                    db.rows(
                                        "SELECT package_id FROM resource.catalog_assignment_failure WHERE target_id=:target",
                                        db.args("target", client))))
                    .isEmpty());
          } finally {
            owner.execute(
                "ALTER TABLE resource.catalog_installation DROP CONSTRAINT " + constraint);
          }
          for (var item :
              delivery.installations(actor(GENERAL), client, "skill", 50, null).path("items")) {
            assertEquals("required", item.path("resource").path("installationMode").asString());
            assertEquals(
                !installed && item.path("resource").path("identity").equals(identity(bad)) ? 1 : 2,
                item.path("installationRevision").asLong());
            assertEquals("applying", item.path("state").asString());
          }
          assertTrue(
              delivery
                  .installations(actor(GENERAL), client, "skill", 50, null)
                  .path("assignmentFailures")
                  .isEmpty());
          return null;
        });
  }

  @Test
  void memberFailuresPreserveInstallationAndDoNotRewriteUnchangedFailure() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          var first = upload(false);
          approvePublish(first);
          var second = nextSkillVersion(first, "1.1.0");
          catalog = spy(catalog);
          doAnswer(
                  call -> {
                    Map<String, Object> version = call.getArgument(1);
                    if (second.version.equals(version.get("id")))
                      throw new AurigaException(CommonErrors.UNAVAILABLE);
                    return call.callRealMethod();
                  })
              .when(catalog)
              .envelope(any(), anyMap(), anyLong());
          var delivery = delivery(null);
          UUID client = UUID.randomUUID();
          assign(delivery, first, "member", GENERAL, "default");
          var original =
              delivery
                  .installations(actor(GENERAL), client, "skill", 50, null)
                  .path("items")
                  .get(0)
                  .deepCopy();
          var upgrade = assign(delivery, second, "member", GENERAL, "default");
          var failed = delivery.installations(actor(GENERAL), client, "skill", 50, null);
          assertEquals(
              original,
              failed.path("items").get(0),
              "a transient failure must not mutate installation state or revision");
          var physical =
              tx.read(
                  () ->
                      db.row(
                          "SELECT xmin::text AS xid,updated_at,error_code FROM resource.catalog_assignment_failure WHERE tenant_id=:tenant AND target_id=:target AND package_id=:pkg",
                          db.args("target", client, "pkg", first.pkg)));
          advance(10);
          delivery.installations(actor(GENERAL), client, "skill", 50, null);
          assertEquals(
              physical,
              tx.read(
                  () ->
                      db.row(
                          "SELECT xmin::text AS xid,updated_at,error_code FROM resource.catalog_assignment_failure WHERE tenant_id=:tenant AND target_id=:target AND package_id=:pkg",
                          db.args("target", client, "pkg", first.pkg))),
              "unchanged failures must not rewrite PostgreSQL rows");
          delivery.removeAssignment(actor(PUBLISHER), uuid(upgrade, "id"), key(), "\"1\"");
          var recovered = delivery.installations(actor(GENERAL), client, "skill", 50, null);
          assertEquals(
              original,
              recovered.path("items").get(0),
              "recovering the original policy must not reinstall unchanged bytes");
          assertTrue(recovered.path("assignmentFailures").isEmpty());
          return null;
        });
  }

  @Test
  void cloudFailureRetriesOnlyTheFailedPackageWhileHealthyResourcesAdvance() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          var bad = upload(false);
          var good = upload(false);
          approvePublish(bad);
          approvePublish(good);
          catalog = spy(catalog);
          var failing = new AtomicBoolean(true);
          doAnswer(
                  call -> {
                    Map<String, Object> version = call.getArgument(1);
                    if (bad.version.equals(version.get("id")) && failing.get())
                      throw new AurigaException(CommonErrors.UNAVAILABLE);
                    return call.callRealMethod();
                  })
              .when(catalog)
              .envelope(any(), anyMap(), anyLong());
          UUID instance = UUID.randomUUID(), node = UUID.randomUUID();
          var workspace = mock(UnifiedResourceWorkspace.class);
          when(workspace.snapshot(TENANT, instance))
              .thenReturn(
                  new UnifiedResourceWorkspace.Snapshot(
                      instance, GENERAL, node, 0, 1, 0, false, List.of(), List.of()));
          var delivery = delivery(workspace);
          assign(delivery, bad, "instance", instance, "required");
          assign(delivery, good, "instance", instance, "required");
          var request =
              auriga.contracts.resource.v1.ResolveUnifiedResourcesRequest.newBuilder()
                  .setTenantId(TENANT.toString())
                  .setOwnerId(GENERAL.toString())
                  .setInstanceId(instance.toString())
                  .setNodeId(node.toString())
                  .setSynchronizeAssignments(true)
                  .build();
          var plan = delivery.resolve(request);
          assertTrue(plan.getAssignmentRetryRequired());
          assertEquals(1, plan.getResourcesCount());
          assertEquals(identity(good), identityFromRef(plan.getResources(0)));
          when(workspace.snapshot(TENANT, instance))
              .thenReturn(
                  new UnifiedResourceWorkspace.Snapshot(
                      instance, GENERAL, node, 1, 2, 0, false, List.of(), plan.getResourcesList()));
          var retry = delivery.resolve(request.toBuilder().setSelectionRevision(1).build());
          assertTrue(retry.getAssignmentRetryRequired());
          assertEquals(1, retry.getSelectionRevision());
          failing.set(false);
          var recovered = delivery.resolve(request.toBuilder().setSelectionRevision(1).build());
          assertFalse(recovered.getAssignmentRetryRequired());
          assertEquals(2, recovered.getResourcesCount());
          assertEquals(1, recovered.getSelectionsCount());
          assertEquals(
              identity(bad),
              identityFromRef(
                  recovered.getResourcesList().stream()
                      .filter(ref -> ref.getIdentity().getPackageId().equals(bad.pkg.toString()))
                      .findFirst()
                      .orElseThrow()));
          assertTrue(
              tx.read(
                      () ->
                          db.rows(
                              "SELECT package_id FROM resource.catalog_assignment_failure WHERE tenant_id=:tenant AND target_id=:target",
                              db.args("target", instance)))
                  .isEmpty());
          return null;
        });
  }

  @Test
  void unchangedInstallationListDoesNotWaitForTenantOrPackageWriters() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          var upload = upload(false);
          approvePublish(upload);
          var delivery = delivery(null);
          UUID client = UUID.randomUUID();
          assign(delivery, upload, "member", GENERAL, "default");
          delivery.installations(actor(GENERAL), client, "skill", 50, null);
          var locked = new java.util.concurrent.CountDownLatch(1);
          var release = new java.util.concurrent.CountDownLatch(1);
          try (var workers = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
            var blocker =
                workers.submit(
                    () ->
                        TenantContext.call(
                            TENANT,
                            () ->
                                tx.write(
                                    () -> {
                                      db.lock("catalog-assignments");
                                      catalog.pkg(access.member(actor(GENERAL)), upload.pkg, false);
                                      locked.countDown();
                                      try {
                                        assertTrue(
                                            release.await(
                                                10, java.util.concurrent.TimeUnit.SECONDS));
                                      } catch (InterruptedException e) {
                                        Thread.currentThread().interrupt();
                                        throw new RuntimeException(e);
                                      }
                                      return true;
                                    })));
            try {
              assertTrue(locked.await(10, java.util.concurrent.TimeUnit.SECONDS));
              var read =
                  workers.submit(
                      () ->
                          TenantContext.call(
                              TENANT,
                              () ->
                                  delivery.installations(
                                      actor(GENERAL), client, "skill", 50, null)));
              assertEquals(
                  1, read.get(5, java.util.concurrent.TimeUnit.SECONDS).path("items").size());
            } finally {
              release.countDown();
              blocker.get(10, java.util.concurrent.TimeUnit.SECONDS);
            }
          }
          return null;
        });
  }

  @Test
  void olderRequiredRulePreservesDesktopHighWaterAndOptionalCannotRaiseIt() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          var first = upload(false);
          approvePublish(first);
          var second = nextSkillVersion(first, "1.1.0");
          var delivery = delivery(null);
          UUID client = UUID.randomUUID();
          delivery.select(actor(GENERAL), choose(client, second, "install"), key(), "\"0\"");
          advance(86401);
          var rule = assign(delivery, first, "member", GENERAL, "required");
          var third = nextSkillVersion(first, "1.2.0");
          assign(delivery, third, "member", GENERAL, "optional");
          var other = upload(false);
          approvePublish(other);
          assign(delivery, other, "member", GENERAL, "default");
          var items =
              delivery.installations(actor(GENERAL), client, "skill", 50, null).path("items");
          assertEquals(2, items.size());
          var installed =
              java.util.stream.StreamSupport.stream(items.spliterator(), false)
                  .filter(
                      n ->
                          n.path("resource")
                              .path("identity")
                              .path("packageId")
                              .asString()
                              .equals(first.pkg.toString()))
                  .findFirst()
                  .orElseThrow();
          assertEquals(identity(second), installed.path("resource").path("identity"));
          assertTrue(
              Instant.parse(installed.path("resource").path("lease").path("expiresAt").asString())
                  .isAfter(clock.instant()),
              "governance change refreshes the exact installed version lease");
          assertEquals("required", installed.path("resource").path("installationMode").asString());
          delivery.removeAssignment(actor(PUBLISHER), uuid(rule, "id"), key(), "\"1\"");
          var revision = installed.path("installationRevision").asLong();
          var restored =
              delivery.installations(actor(GENERAL), client, "skill", 50, null).path("items");
          var current =
              java.util.stream.StreamSupport.stream(restored.spliterator(), false)
                  .filter(n -> n.path("resource").path("identity").equals(identity(second)))
                  .findFirst()
                  .orElseThrow();
          assertEquals("optional", current.path("resource").path("installationMode").asString());
          assertEquals(revision + 1, current.path("installationRevision").asLong());
          long currentRevision = current.path("installationRevision").asLong();
          delivery.select(
              actor(GENERAL),
              choose(client, second, "disable"),
              key(),
              "\"" + currentRevision + "\"");
          var enabled =
              delivery.select(
                  actor(GENERAL),
                  choose(client, second, "enable"),
                  key(),
                  "\"" + (currentRevision + 1) + "\"");
          assertEquals(identity(second), enabled.path("resource").path("identity"));
          assertEquals("ENABLED", enabled.path("resource").path("desiredState").asString());
          return null;
        });
  }

  @ParameterizedTest
  @CsvSource({
    "false,required,malicious", "true,required,malicious",
    "false,default,malicious", "true,default,malicious",
    "false,default,withdrawn", "true,default,withdrawn",
    "false,required,removed", "true,required,removed",
    "false,default,removedWithdrawn", "true,default,removedWithdrawn"
  })
  void mandatoryRulesRecoverAfterUnavailableHighVersion(boolean cloud, String mode, String scenario)
      throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          var first = upload(false);
          approvePublish(first);
          nextSkillVersion(first, "1.1.0");
          var manifest = (ObjectNode) first.value.path("manifest").deepCopy();
          manifest.put("version", "1.2.0");
          byte[] payload =
              archive(
                  manifest.path("name").asString(),
                  "Example",
                  "Distinct third-version documentation.",
                  "SKILL.md");
          ((ObjectNode) manifest.path("artifacts").get(0))
              .put("digest", CatalogManifest.digest(payload))
              .put("sizeBytes", payload.length);
          byte[] raw = manifest.toString().getBytes(StandardCharsets.UTF_8);
          var uploaded =
              catalog.upload(
                  actor(AUTHOR),
                  first.pkg,
                  JSON.createObjectNode()
                      .put("version", "1.2.0")
                      .put("expectedDigest", CatalogManifest.digest(raw)),
                  raw,
                  List.of(new ByteArrayInputStream(payload)),
                  key(),
                  etag(catalog.detail(actor(AUTHOR), first.pkg, null, false)));
          var third = new Upload(first.pkg, uuid(uploaded, "id"), uploaded, raw, payload);
          approvePublish(third);
          UUID target = UUID.randomUUID(), node = UUID.randomUUID();
          var workspace = mock(UnifiedResourceWorkspace.class);
          when(workspace.snapshot(TENANT, target))
              .thenReturn(
                  new UnifiedResourceWorkspace.Snapshot(
                      target, GENERAL, node, 0, 1, 0, false, List.of(), List.of()));
          var delivery = delivery(cloud ? workspace : null);
          long selection = 0;
          var request =
              auriga.contracts.resource.v1.ResolveUnifiedResourcesRequest.newBuilder()
                  .setTenantId(TENANT.toString())
                  .setOwnerId(GENERAL.toString())
                  .setInstanceId(target.toString())
                  .setNodeId(node.toString());
          if (cloud) {
            delivery.distribute(
                actor(PUBLISHER),
                distributionBody(target, selection(third, "install")),
                key(),
                "\"0\"");
            var initial =
                delivery.resolve(
                    request
                        .setSelectionRevision(1)
                        .addSelections(CatalogProtocol.selection(selection(third, "install")))
                        .build());
            var installed = initial.getResources(0);
            selection = 1;
            when(workspace.snapshot(TENANT, target))
                .thenReturn(
                    new UnifiedResourceWorkspace.Snapshot(
                        target,
                        GENERAL,
                        node,
                        selection,
                        2,
                        0,
                        false,
                        List.of(),
                        List.of(installed)));
            if (scenario.startsWith("removed")) {
              delivery.distribute(
                  actor(PUBLISHER),
                  distributionBody(target, selection(third, "remove")),
                  key(),
                  "\"1\"");
              var removal =
                  delivery.resolve(
                      request
                          .clearSelections()
                          .setSelectionRevision(2)
                          .addSelections(CatalogProtocol.selection(selection(third, "remove")))
                          .build());
              selection = 2;
              when(workspace.snapshot(TENANT, target))
                  .thenReturn(
                      new UnifiedResourceWorkspace.Snapshot(
                          target,
                          GENERAL,
                          node,
                          selection,
                          3,
                          0,
                          false,
                          List.of(),
                          removal.getResourcesList()));
            }
          } else {
            delivery.select(actor(GENERAL), choose(target, third, "install"), key(), "\"0\"");
            if (scenario.startsWith("removed"))
              delivery.select(actor(GENERAL), choose(target, third, "remove"), key(), "\"1\"");
          }
          // Keep the package enabled independently of v3: withdrawing the current default disables
          // the entire package, in which case no version is available and recovery is forbidden.
          nextSkillVersion(first, "1.3.0");
          assign(delivery, first, cloud ? "instance" : "member", cloud ? target : GENERAL, mode);
          if (!scenario.equals("removed"))
            catalog.withdraw(
                actor(PUBLISHER),
                third.pkg,
                third.version,
                reason().put("mode", scenario.equals("malicious") ? "malicious" : "withdrawn"),
                key(),
                etag(catalog.detail(actor(AUTHOR), third.pkg, third.version, false)));
          if (cloud) {
            var restored =
                delivery.resolve(
                    request
                        .clearSelections()
                        .setSelectionRevision(selection)
                        .setSynchronizeAssignments(true)
                        .build());
            if (mode.equals("default") && scenario.startsWith("removed")) {
              assertFalse(restored.getAssignmentRetryRequired());
              assertTrue(
                  restored.getResourcesList().stream()
                      .allMatch(
                          ref ->
                              CatalogStatePolicy.desired(ref).equals("REMOVED")
                                  && !ref.hasDowngradeAuthorization()));
              return null;
            }
            if (!scenario.equals("malicious")) {
              assertTrue(restored.getAssignmentRetryRequired());
              assertTrue(
                  restored.getResourcesList().stream()
                      .noneMatch(ref -> ref.hasDowngradeAuthorization()));
              return null;
            }
            assertFalse(restored.getAssignmentRetryRequired());
            assertEquals(identity(first), identityFromRef(restored.getResources(0)));
            assertEquals(mode, restored.getResources(0).getInstallationMode());
            assertEquals("ENABLED", CatalogStatePolicy.desired(restored.getResources(0)));
            var authorization = restored.getResources(0).getDowngradeAuthorization();
            assertEquals("revocation", authorization.getReason());
            assertEquals(GENERAL.toString(), authorization.getOwnerId());
            assertEquals(target.toString(), authorization.getInstanceId());
            assertEquals(1, authorization.getToReleaseSequence());
            assertEquals(3, authorization.getFromReleaseSequence());
            when(workspace.snapshot(TENANT, target))
                .thenReturn(
                    new UnifiedResourceWorkspace.Snapshot(
                        target,
                        GENERAL,
                        node,
                        restored.getSelectionRevision(),
                        3,
                        0,
                        false,
                        List.of(),
                        restored.getResourcesList()));
            advance(601);
            signing =
                new ResourceSigning(
                    "release", releaseKey.getPrivate(), "revoke", revocationKey.getPrivate());
            catalog.withdraw(
                actor(PUBLISHER),
                first.pkg,
                first.version,
                reason().put("mode", "malicious"),
                key(),
                etag(catalog.detail(actor(AUTHOR), first.pkg, first.version, false)));
            var disabled =
                delivery.resolve(
                    request
                        .clearSelections()
                        .setSynchronizeAssignments(false)
                        .setSelectionRevision(restored.getSelectionRevision())
                        .addSelections(CatalogProtocol.selection(selection(first, "install")))
                        .build());
            assertFalse(
                disabled.getResources(0).getEnabled(),
                "missing downgrade root must never block safety disable");
          } else {
            var page = delivery.installations(actor(GENERAL), target, "skill", 50, null);
            if (mode.equals("default") && scenario.startsWith("removed")) {
              assertTrue(page.path("assignmentFailures").isEmpty());
              assertEquals(
                  "REMOVED",
                  page.path("items").get(0).path("resource").path("desiredState").asString());
              return null;
            }
            if (!scenario.equals("malicious")) {
              assertFalse(page.path("assignmentFailures").isEmpty());
              assertTrue(
                  java.util.stream.StreamSupport.stream(page.path("items").spliterator(), false)
                      .noneMatch(item -> item.path("resource").has("downgradeAuthorization")));
              return null;
            }
            assertTrue(page.path("assignmentFailures").isEmpty());
            var resource = page.path("items").get(0).path("resource");
            assertEquals(identity(first), resource.path("identity"));
            assertEquals(mode, resource.path("installationMode").asString());
            assertEquals("ENABLED", resource.path("desiredState").asString());
            var authorization = resource.path("downgradeAuthorization");
            assertEquals("revocation", authorization.path("reason").asString());
            assertEquals(GENERAL.toString(), authorization.path("ownerId").asString());
            assertEquals(target.toString(), authorization.path("instanceId").asString());
            var trust = JSON.createObjectNode();
            trust.set(
                "downgrade",
                JSON.createObjectNode()
                    .put("kid", "downgrade")
                    .put(
                        "publicKeySpkiBase64",
                        Base64.getEncoder().encodeToString(downgradeKey.getPublic().getEncoded())));
            var input = (ObjectNode) authorization.deepCopy();
            input.set("authorization", authorization);
            input.put("now", clock.instant().toString());
            input.putArray("usedAuthorizationIds");
            var verifier = new CatalogTrust(trust);
            verifier.verify("downgrade", input);
            input.put("ownerId", UUID.randomUUID().toString());
            assertEquals(
                "downgrade_scope",
                assertThrows(
                        IllegalArgumentException.class, () -> verifier.verify("downgrade", input))
                    .getMessage());
            input.put("ownerId", GENERAL.toString());
            input.withArray("usedAuthorizationIds").add(authorization.path("authorizationId"));
            assertEquals(
                "downgrade_replay",
                assertThrows(
                        IllegalArgumentException.class, () -> verifier.verify("downgrade", input))
                    .getMessage());
            if (mode.equals("default")) {
              long revision = page.path("items").get(0).path("installationRevision").asLong();
              delivery.select(
                  actor(GENERAL), choose(target, first, "disable"), key(), "\"" + revision + "\"");
              var enabled =
                  delivery.select(
                      actor(GENERAL),
                      choose(target, first, "enable"),
                      key(),
                      "\"" + (revision + 1) + "\"");
              assertNotEquals(
                  authorization.path("authorizationId"),
                  enabled.path("resource").path("downgradeAuthorization").path("authorizationId"));
            }
          }
          return null;
        });
  }

  @Test
  void cloudHighWaterAndPendingPlanFollowRuleReplacement() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          var first = upload(false);
          approvePublish(first);
          var second = nextSkillVersion(first, "1.1.0");
          UUID instance = UUID.randomUUID(), node = UUID.randomUUID();
          var workspace = mock(UnifiedResourceWorkspace.class);
          when(workspace.snapshot(TENANT, instance))
              .thenReturn(
                  new UnifiedResourceWorkspace.Snapshot(
                      instance, GENERAL, node, 0, 1, 0, false, List.of(), List.of()));
          var delivery = delivery(workspace);
          delivery.distribute(
              actor(PUBLISHER),
              distributionBody(instance, selection(second, "install")),
              key(),
              "\"0\"");
          var request =
              auriga.contracts.resource.v1.ResolveUnifiedResourcesRequest.newBuilder()
                  .setTenantId(TENANT.toString())
                  .setOwnerId(GENERAL.toString())
                  .setInstanceId(instance.toString())
                  .setNodeId(node.toString())
                  .setSelectionRevision(1)
                  .addSelections(CatalogProtocol.selection(selection(second, "install")))
                  .build();
          var initial = delivery.resolve(request);
          when(workspace.snapshot(TENANT, instance))
              .thenReturn(
                  new UnifiedResourceWorkspace.Snapshot(
                      instance,
                      GENERAL,
                      node,
                      1,
                      2,
                      0,
                      false,
                      List.of(),
                      initial.getResourcesList()));
          var rule = assign(delivery, first, "instance", instance, "required");
          var third = nextSkillVersion(first, "1.2.0");
          assign(delivery, third, "member", GENERAL, "optional");
          var sync = request.toBuilder().clearSelections().setSynchronizeAssignments(true).build();
          var plan = delivery.resolve(sync);
          assertEquals(identity(second), identityFromRef(plan.getResources(0)));
          assertEquals("required", plan.getResources(0).getInstallationMode());
          assertEquals(plan, delivery.resolve(sync), "unchanged pending plan is idempotent");
          delivery.removeAssignment(actor(PUBLISHER), uuid(rule, "id"), key(), "\"1\"");
          assertEquals(
              1,
              delivery.resolve(sync).getSelectionRevision(),
              "removed rule supersedes unapplied required intent");
          var def = assign(delivery, first, "instance", instance, "default");
          var replacement = delivery.resolve(sync);
          assertTrue(replacement.getSelectionRevision() > plan.getSelectionRevision());
          assertEquals(identity(second), identityFromRef(replacement.getResources(0)));
          assertEquals("default", replacement.getResources(0).getInstallationMode());
          when(workspace.snapshot(TENANT, instance))
              .thenReturn(
                  new UnifiedResourceWorkspace.Snapshot(
                      instance,
                      GENERAL,
                      node,
                      replacement.getSelectionRevision(),
                      3,
                      0,
                      false,
                      List.of(),
                      replacement.getResourcesList()));
          delivery.removeAssignment(actor(PUBLISHER), uuid(def, "id"), key(), "\"1\"");
          var restored =
              delivery.resolve(
                  sync.toBuilder()
                      .setSelectionRevision(replacement.getSelectionRevision())
                      .build());
          assertEquals("optional", restored.getResources(0).getInstallationMode());
          assertEquals(identity(second), identityFromRef(restored.getResources(0)));
          return null;
        });
  }

  @ParameterizedTest
  @ValueSource(strings = {"plugin", "skill"})
  void assignmentsMergeModeAndPinnedVersionAndPreserveDefaultDisable(String kind) {
    TenantContext.run(
        TENANT,
        () -> {
          try {
            var first = upload(false, kind);
            approvePublish(first);
            var delivery = delivery(null);
            UUID client = UUID.randomUUID();
            assign(delivery, first, "member", GENERAL, "default");
            delivery.installations(actor(GENERAL), client, kind, 50, null);
            String disableKey = key();
            delivery.select(actor(GENERAL), choose(client, first, "disable"), disableKey, "\"1\"");
            var manifest = (ObjectNode) first.value.path("manifest").deepCopy();
            manifest.put("version", "1.1.0");
            byte[] bytes =
                kind.equals("plugin")
                    ? pluginArchive(
                        manifest.path("plugin").path("packageName").asString(),
                        "1.1.0",
                        "package/package.json",
                        false,
                        false)
                    : first.payload;
            ((ObjectNode) manifest.path("artifacts").get(0))
                .put("digest", CatalogManifest.digest(bytes))
                .put("sizeBytes", bytes.length);
            byte[] raw = manifest.toString().getBytes(StandardCharsets.UTF_8);
            var value =
                catalog.upload(
                    actor(AUTHOR),
                    first.pkg,
                    JSON.createObjectNode()
                        .put("version", "1.1.0")
                        .put("expectedDigest", CatalogManifest.digest(raw)),
                    raw,
                    List.of(new ByteArrayInputStream(bytes)),
                    key(),
                    etag(catalog.detail(actor(AUTHOR), first.pkg, null, false)));
            var second = new Upload(first.pkg, uuid(value, "id"), value, raw, bytes);
            approvePublish(second);
            assign(delivery, second, "tenant", TENANT, "default");
            var item =
                delivery.installations(actor(GENERAL), client, kind, 50, null).path("items").get(0);
            assertEquals(identity(second), item.path("resource").path("identity"));
            assertEquals("DISABLED", item.path("resource").path("desiredState").asString());
            assign(delivery, first, "member", GENERAL, "required");
            item =
                delivery.installations(actor(GENERAL), client, kind, 50, null).path("items").get(0);
            assertEquals(identity(second), item.path("resource").path("identity"));
            assertEquals("required", item.path("resource").path("installationMode").asString());
            assertEquals("ENABLED", item.path("resource").path("desiredState").asString());
            assertEquals(
                "resource.installation_required",
                assertThrows(
                        AurigaException.class,
                        () ->
                            delivery.select(
                                actor(GENERAL),
                                choose(client, first, "disable"),
                                disableKey,
                                "\"1\""))
                    .errorCode()
                    .code());
            var revision = "\"" + item.path("installationRevision").asLong() + "\"";
            assertEquals(
                "resource.distribution_locked",
                assertThrows(
                        AurigaException.class,
                        () ->
                            delivery.select(
                                actor(GENERAL), choose(client, first, "install"), key(), revision))
                    .errorCode()
                    .code());
            var foreign =
                JSON.createObjectNode()
                    .put("scope", "member")
                    .put("targetId", GENERAL.toString())
                    .put("installationMode", "required");
            foreign.set("identity", identity(second).put("tenantId", UUID.randomUUID().toString()));
            assertError(
                CommonErrors.NOT_FOUND,
                () -> delivery.createAssignment(actor(PUBLISHER), foreign, key(), "\"0\""));
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @ParameterizedTest
  @CsvSource({
    "plugin,required",
    "plugin,default",
    "plugin,optional",
    "skill,required",
    "skill,default",
    "skill,optional"
  })
  void assignmentModesControlMemberInstallation(String kind, String mode) {
    TenantContext.run(
        TENANT,
        () -> {
          try {
            var upload = upload(false, kind);
            approvePublish(upload);
            var delivery = delivery(null);
            UUID client = UUID.randomUUID();
            assign(delivery, upload, "tenant", TENANT, mode);
            personalAllowed.set(false);
            var page = delivery.installations(actor(GENERAL), client, kind, 50, null);
            assertFalse(page.path("allowPersonalPlugins").asBoolean());
            JsonNode installed;
            if (mode.equals("optional")) {
              assertTrue(page.path("items").isEmpty());
              installed =
                  delivery.select(
                      actor(GENERAL), choose(client, upload, "install"), key(), "\"0\"");
            } else {
              installed = page.path("items").get(0);
              assertEquals(
                  installed,
                  delivery
                      .installations(actor(GENERAL), client, kind, 50, null)
                      .path("items")
                      .get(0));
            }
            assertEquals(mode, installed.path("resource").path("installationMode").asString());
            var renewal =
                JSON.createObjectNode()
                    .put("clientId", client.toString())
                    .put("installationRevision", 1)
                    .put("selectionRevision", 1)
                    .put(
                        "releaseSequence",
                        installed.path("resource").path("releaseSequence").asLong());
            renewal.set("identity", identity(upload));
            assertEquals(
                mode,
                delivery
                    .renew(actor(GENERAL), renewal, key(), "\"1\"")
                    .path("resource")
                    .path("installationMode")
                    .asString());
            assertFalse(catalog.detail(actor(GENERAL), upload.pkg, null, false).has("level"));
            if (mode.equals("required")) {
              assertEquals(
                  "resource.installation_required",
                  assertThrows(
                          AurigaException.class,
                          () ->
                              delivery.select(
                                  actor(GENERAL),
                                  choose(client, upload, "disable"),
                                  key(),
                                  "\"1\""))
                      .errorCode()
                      .code());
            } else {
              var disabled =
                  delivery.select(
                      actor(GENERAL), choose(client, upload, "disable"), key(), "\"1\"");
              assertEquals("DISABLED", disabled.path("resource").path("desiredState").asString());
              assertEquals(
                  "DISABLED",
                  delivery
                      .installations(actor(GENERAL), client, kind, 50, null)
                      .path("items")
                      .get(0)
                      .path("resource")
                      .path("desiredState")
                      .asString());
            }
            if (mode.equals("required")) {
              assertEquals(
                  "resource.installation_required",
                  assertThrows(
                          AurigaException.class,
                          () ->
                              delivery.select(
                                  actor(GENERAL),
                                  choose(client, upload, "remove"),
                                  key(),
                                  mode.equals("required") ? "\"1\"" : "\"2\""))
                      .errorCode()
                      .code());
            } else {
              var removed =
                  delivery.select(actor(GENERAL), choose(client, upload, "remove"), key(), "\"2\"");
              assertEquals("REMOVED", removed.path("resource").path("desiredState").asString());
              assertEquals(
                  ResourceSigning.canonical(removed),
                  ResourceSigning.canonical(
                      delivery
                          .installations(actor(GENERAL), client, kind, 50, null)
                          .path("items")
                          .get(0)));
              var reinstall = upload;
              if (mode.equals("default") && kind.equals("skill")) {
                reinstall = nextSkillVersion(upload, "1.1.0");
                assign(delivery, reinstall, "tenant", TENANT, mode);
                var retained =
                    delivery
                        .installations(actor(GENERAL), client, kind, 50, null)
                        .path("items")
                        .get(0);
                assertEquals("REMOVED", retained.path("resource").path("desiredState").asString());
                assertEquals(3, retained.path("installationRevision").asLong());
              }
              var restored =
                  delivery.select(
                      actor(GENERAL), choose(client, reinstall, "install"), key(), "\"3\"");
              assertEquals("ENABLED", restored.path("resource").path("desiredState").asString());
            }
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @ParameterizedTest
  @ValueSource(strings = {"plugin", "skill"})
  void overlappingScopesTakeStrictestAndRevocationDropsMandatorySet(String kind) {
    TenantContext.run(
        TENANT,
        () -> {
          try {
            var upload = upload(false, kind);
            var published = approvePublish(upload);
            var delivery = delivery(null);
            UUID group = UUID.randomUUID(), client = UUID.randomUUID();
            knownGroups.add(group);
            groups.put(GENERAL, Set.of(group));
            assign(delivery, upload, "tenant", TENANT, "optional");
            var def = assign(delivery, upload, "group", group, "default");
            var req = assign(delivery, upload, "member", GENERAL, "required");
            var item =
                delivery.installations(actor(GENERAL), client, kind, 50, null).path("items").get(0);
            assertEquals("required", item.path("resource").path("installationMode").asString());
            delivery.removeAssignment(actor(PUBLISHER), uuid(req, "id"), key(), etag(req));
            item =
                delivery.installations(actor(GENERAL), client, kind, 50, null).path("items").get(0);
            assertEquals("default", item.path("resource").path("installationMode").asString());
            groups.put(GENERAL, Set.of());
            item =
                delivery.installations(actor(GENERAL), client, kind, 50, null).path("items").get(0);
            assertEquals("optional", item.path("resource").path("installationMode").asString());
            groups.put(GENERAL, Set.of(group));
            item =
                delivery.installations(actor(GENERAL), client, kind, 50, null).path("items").get(0);
            assertEquals("default", item.path("resource").path("installationMode").asString());
            catalog.withdraw(
                actor(PUBLISHER),
                upload.pkg,
                upload.version,
                reason().put("mode", "malicious"),
                key(),
                etag(published));
            item =
                delivery.installations(actor(GENERAL), client, kind, 50, null).path("items").get(0);
            assertEquals("optional", item.path("resource").path("installationMode").asString());
            assertEquals("REMOVED", item.path("resource").path("desiredState").asString());
            assertEquals(
                "resource.catalog_assignment_changed",
                tx.read(
                        () ->
                            db.row(
                                "SELECT event_type FROM resource.outbox_event WHERE tenant_id=:tenant AND subject_key=:id AND event_type='resource.catalog_assignment_changed' LIMIT 1",
                                db.args("id", def.path("id").asString())))
                    .get("event_type"));
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void assignmentPagesFilterCurrentPackageVisibilityWithoutBlockingVisibleRules() {
    TenantContext.run(
        TENANT,
        () -> {
          try {
            var hidden = upload(false);
            var visible = upload(false);
            approvePublish(hidden);
            approvePublish(visible);
            var delivery = delivery(null);
            var hiddenRule = assign(delivery, hidden, "member", GENERAL, "optional");
            var visibleRule = assign(delivery, visible, "member", GENERAL, "default");
            tx.write(
                () ->
                    db.update(
                        "UPDATE resource.catalog_package SET visibility='groups',group_ids=CAST(:groups AS uuid[]) WHERE tenant_id=:tenant AND id=:p",
                        db.args("p", hidden.pkg, "groups", "{" + UUID.randomUUID() + "}")));
            var page = delivery.assignments(actor(PUBLISHER), 200, null);
            var ids = new HashSet<String>();
            page.path("items").forEach(item -> ids.add(item.path("id").asString()));
            assertTrue(ids.contains(visibleRule.path("id").asString()));
            assertFalse(ids.contains(hiddenRule.path("id").asString()));
            assertEquals(
                visibleRule, delivery.assignment(actor(PUBLISHER), uuid(visibleRule, "id")));
            assertError(
                CommonErrors.NOT_FOUND,
                () -> delivery.assignment(actor(PUBLISHER), uuid(hiddenRule, "id")));
            assertError(
                CommonErrors.NOT_FOUND,
                () ->
                    delivery.removeAssignment(
                        actor(PUBLISHER), uuid(hiddenRule, "id"), key(), etag(hiddenRule)));
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void assignmentTargetsRequireTenantAndVisibilityAndCloudBootstrapPinsModes() {
    TenantContext.run(
        TENANT,
        () -> {
          try {
            var upload = upload(false, "plugin");
            approvePublish(upload);
            UUID instance = UUID.randomUUID(), node = UUID.randomUUID();
            var workspace = mock(UnifiedResourceWorkspace.class);
            when(workspace.snapshot(TENANT, instance))
                .thenReturn(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance, GENERAL, node, 0, 1, 0, false, List.of(), List.of()));
            var delivery = delivery(workspace);
            assertError(
                CommonErrors.NOT_FOUND,
                () -> assign(delivery, upload, "tenant", UUID.randomUUID(), "required"));
            assertError(
                CommonErrors.NOT_FOUND,
                () -> assign(delivery, upload, "group", UUID.randomUUID(), "default"));
            tx.write(
                () ->
                    db.update(
                        "UPDATE resource.catalog_package SET visibility='members' WHERE tenant_id=:tenant AND id=:p",
                        db.args("p", upload.pkg)));
            externalMembers.add(SECURITY);
            assertError(
                CommonErrors.NOT_FOUND,
                () -> assign(delivery, upload, "member", SECURITY, "required"));
            tx.write(
                () ->
                    db.update(
                        "UPDATE resource.catalog_package SET visibility='tenant' WHERE tenant_id=:tenant AND id=:p",
                        db.args("p", upload.pkg)));
            var rule = assign(delivery, upload, "instance", instance, "required");
            for (String action : List.of("disable", "remove"))
              assertEquals(
                  "resource.installation_required",
                  assertThrows(
                          AurigaException.class,
                          () ->
                              delivery.distribute(
                                  actor(PUBLISHER),
                                  distributionBody(instance, selection(upload, action)),
                                  key(),
                                  "\"0\""))
                      .errorCode()
                      .code());
            assertTrue(
                TenantContext.call(
                        UUID.randomUUID(),
                        () ->
                            tx.read(
                                () ->
                                    db.rows(
                                        "SELECT id FROM resource.catalog_assignment WHERE id=:id",
                                        db.args("id", uuid(rule, "id")))))
                    .isEmpty());
            personalAllowed.set(false);
            var request =
                auriga.contracts.resource.v1.ResolveUnifiedResourcesRequest.newBuilder()
                    .setTenantId(TENANT.toString())
                    .setOwnerId(GENERAL.toString())
                    .setInstanceId(instance.toString())
                    .setNodeId(node.toString())
                    .setSelectionRevision(0)
                    .setSynchronizeAssignments(true)
                    .build();
            var plan = delivery.resolve(request);
            assertEquals(1, plan.getSelectionRevision());
            assertEquals(identity(upload), identityFromRef(plan.getResources(0)));
            assertEquals("required", plan.getResources(0).getInstallationMode());
            assertFalse(plan.getAllowPersonalPlugins());
            assertEquals(plan, delivery.resolve(request));
            assertError(
                CommonErrors.CONFLICT,
                () -> delivery.resolve(request.toBuilder().setOwnerId(AUTHOR.toString()).build()));
            assertTrue(
                delivery
                    .installations(actor(AUTHOR), UUID.randomUUID(), "plugin", 50, null)
                    .path("items")
                    .isEmpty());
            when(workspace.snapshot(TENANT, instance))
                .thenReturn(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance,
                        GENERAL,
                        node,
                        1,
                        2,
                        0,
                        false,
                        List.of(),
                        plan.getResourcesList()));
            assertEquals(
                1,
                delivery
                    .resolve(request.toBuilder().setSelectionRevision(1).build())
                    .getSelectionRevision());
            var current = catalog.detail(actor(AUTHOR), upload.pkg, upload.version, false);
            catalog.withdraw(
                actor(PUBLISHER),
                upload.pkg,
                upload.version,
                reason().put("mode", "malicious"),
                key(),
                etag(current));
            var disabled =
                delivery.resolve(
                    request.toBuilder()
                        .setSynchronizeAssignments(false)
                        .setSelectionRevision(1)
                        .addAllSelections(plan.getSelectionsList())
                        .build());
            assertEquals("DISABLED", CatalogStatePolicy.desired(disabled.getResources(0)));
            assertEquals("optional", disabled.getResources(0).getInstallationMode());
            when(workspace.snapshot(TENANT, instance))
                .thenReturn(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance,
                        GENERAL,
                        node,
                        1,
                        3,
                        0,
                        false,
                        List.of(),
                        disabled.getResourcesList()));
            var removal = delivery.resolve(request.toBuilder().setSelectionRevision(1).build());
            assertEquals(2, removal.getSelectionRevision());
            assertEquals("remove", removal.getSelections(0).getAction());
            assertEquals("optional", removal.getResources(0).getInstallationMode());
            var resolvedRemoval =
                delivery.resolve(
                    request.toBuilder()
                        .setSynchronizeAssignments(false)
                        .setSelectionRevision(2)
                        .addAllSelections(removal.getSelectionsList())
                        .build());
            assertEquals("REMOVED", CatalogStatePolicy.desired(resolvedRemoval.getResources(0)));
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  ObjectNode identityFromRef(auriga.contracts.resource.v1.UnifiedResourceRef ref) {
    var id = ref.getIdentity();
    return JSON.createObjectNode()
        .put("tenantId", id.getTenantId())
        .put("packageId", id.getPackageId())
        .put("versionId", id.getVersionId())
        .put("kind", id.getKind())
        .put("version", id.getVersion())
        .put("manifestDigest", id.getManifestDigest());
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

  @ParameterizedTest
  @ValueSource(strings = {"auriga-dsh", "@deepseek-ai/dsh", "@deepseek-ai/reserved-plugin"})
  void uploadRejectsDeploymentOwnedPluginNames(String packageName) throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          var upload = upload(false, "plugin");
          var manifest = (ObjectNode) upload.value.path("manifest").deepCopy();
          manifest.put("version", "1.1.0");
          ((ObjectNode) manifest.path("plugin")).put("packageName", packageName);
          var payload = pluginArchive(packageName, "1.1.0", "package/package.json", false, false);
          ((ObjectNode) manifest.path("artifacts").get(0))
              .put("digest", CatalogManifest.digest(payload))
              .put("sizeBytes", payload.length);
          byte[] raw = manifest.toString().getBytes(StandardCharsets.UTF_8);
          assertError(
              CommonErrors.VALIDATION_FAILED,
              () ->
                  catalog.upload(
                      actor(AUTHOR),
                      upload.pkg,
                      JSON.createObjectNode()
                          .put("version", "1.1.0")
                          .put("expectedDigest", CatalogManifest.digest(raw)),
                      raw,
                      List.of(new ByteArrayInputStream(payload)),
                      key(),
                      etag(catalog.detail(actor(AUTHOR), upload.pkg, null, false))));
          return null;
        });
  }

  record Upload(UUID pkg, UUID version, JsonNode value, byte[] manifest, byte[] payload) {}

  Upload upload(boolean command) throws Exception {
    return upload(command, "skill");
  }

  Upload upload(boolean command, String kind) throws Exception {
    JsonNode p =
        kind.equals("skill")
            ? create()
            : catalog.create(
                actor(AUTHOR),
                JSON.createObjectNode()
                    .put("kind", "plugin")
                    .put("name", "plugin-" + key())
                    .put("visibility", "tenant"),
                key(),
                "\"0\"");
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
    if (!kind.equals("skill")) {
      bytes =
          kind.equals("npm-plugin")
              ? getClass()
                  .getResourceAsStream("/plugin/example-dsh-plugin-1.0.0.tgz")
                  .readAllBytes()
              : pluginArchive("@example/" + name, "1.0.0", "package/package.json", false, false);
      manifest.put("kind", "plugin");
      manifest.remove("skill");
      manifest.set(
          "plugin",
          JSON.createObjectNode()
              .put(
                  "packageName",
                  kind.equals("npm-plugin") ? "@example/dsh-plugin" : "@example/" + name)
              .put("dshVersionRange", "*"));
      var artifact = (ObjectNode) manifest.path("artifacts").get(0);
      artifact.put("digest", CatalogManifest.digest(bytes)).put("sizeBytes", bytes.length);
    }
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

  static byte[] pluginArchive(
      String name, String version, String path, boolean link, boolean script) throws Exception {
    var out = new ByteArrayOutputStream();
    try (var gzip = new java.util.zip.GZIPOutputStream(out);
        var tar = new TarArchiveOutputStream(gzip)) {
      var pkg = JSON.createObjectNode().put("name", name).put("version", version);
      pkg.putObject("dsh").putObject("bundle").put("patch", "./patch.js");
      if (script) pkg.set("scripts", JSON.createObjectNode().put("postinstall", "node install.js"));
      byte[] content = pkg.toString().getBytes(StandardCharsets.UTF_8);
      var entry =
          new TarArchiveEntry(path, link ? TarConstants.LF_SYMLINK : TarConstants.LF_NORMAL);
      entry.setMode(0644);
      if (link) entry.setLinkName("../../outside");
      entry.setSize(link ? 0 : content.length);
      tar.putArchiveEntry(entry);
      if (!link) tar.write(content);
      tar.closeArchiveEntry();
      var patch = new TarArchiveEntry("package/patch.js");
      byte[] patchBytes = "export default {};".getBytes(StandardCharsets.UTF_8);
      patch.setMode(0644);
      patch.setSize(patchBytes.length);
      tar.putArchiveEntry(patch);
      tar.write(patchBytes);
      tar.closeArchiveEntry();
    }
    return out.toByteArray();
  }

  @Test
  void acceptsActualNpmPackOutput() throws Exception {
    TenantContext.run(
        TENANT,
        () -> {
          try {
            assertEquals(
                "r2",
                upload(false, "npm-plugin").value.path("risk").path("minimumLevel").asString());
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void pluginUploadRejectsIdentityMismatchPathsLinksScriptsAndCompressedLimit() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false, "plugin");
            assertEquals("r2", u.value.path("risk").path("minimumLevel").asString());
            assertEquals(2, u.value.path("risk").path("requiredReviewers").asInt());
            String npmName = u.value.path("manifest").path("plugin").path("packageName").asString();
            for (byte[] payload :
                List.of(
                    pluginArchive("other", "1.0.0", "package/package.json", false, false),
                    pluginArchive(npmName, "2.0.0", "package/package.json", false, false),
                    pluginArchive(npmName, "1.0.0", "package.json", false, false),
                    pluginArchive(npmName, "1.0.0", "../package/package.json", false, false),
                    pluginArchive(npmName, "1.0.0", "package/package.json", true, false),
                    pluginArchive(npmName, "1.0.0", "package/package.json", false, true))) {
              var m = (ObjectNode) JSON.readTree(u.manifest);
              ((ObjectNode) m.path("artifacts").get(0))
                  .put("digest", CatalogManifest.digest(payload))
                  .put("sizeBytes", payload.length);
              byte[] raw = m.toString().getBytes(StandardCharsets.UTF_8);
              assertError(
                  CommonErrors.VALIDATION_FAILED,
                  () ->
                      catalog.upload(
                          actor(AUTHOR),
                          u.pkg,
                          JSON.createObjectNode()
                              .put("version", "1.0.0")
                              .put("expectedDigest", CatalogManifest.digest(raw)),
                          raw,
                          List.of(new ByteArrayInputStream(payload)),
                          key(),
                          "\"2\""));
            }
            var m = (ObjectNode) JSON.readTree(u.manifest);
            ((ObjectNode) m.path("artifacts").get(0)).put("sizeBytes", 67108865);
            byte[] raw = m.toString().getBytes(StandardCharsets.UTF_8);
            assertError(
                CommonErrors.VALIDATION_FAILED,
                () ->
                    CatalogManifest.parse(
                        raw, m.path("name").asString(), "1.0.0", CatalogManifest.digest(raw)));
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  static byte[] pluginBudgetArchive(boolean manyEntries, String name) throws Exception {
    var out = new ByteArrayOutputStream();
    var params = new org.apache.commons.compress.compressors.gzip.GzipParameters();
    params.setCompressionLevel(manyEntries ? 0 : 1);
    try (var gzip =
            new org.apache.commons.compress.compressors.gzip.GzipCompressorOutputStream(
                out, params);
        var tar = new TarArchiveOutputStream(gzip)) {
      byte[] pkg =
          JSON.createObjectNode()
              .put("name", name)
              .put("version", "1.0.0")
              .toString()
              .getBytes(StandardCharsets.UTF_8);
      var json = new TarArchiveEntry("package/package.json");
      json.setSize(pkg.length);
      tar.putArchiveEntry(json);
      tar.write(pkg);
      tar.closeArchiveEntry();
      if (manyEntries) {
        for (int i = 0; i < 4096; i++) {
          var entry = new TarArchiveEntry("package/file-" + i);
          entry.setSize(0);
          tar.putArchiveEntry(entry);
          tar.closeArchiveEntry();
        }
      } else {
        var entry = new TarArchiveEntry("package/large.js");
        entry.setSize(268435456L);
        tar.putArchiveEntry(entry);
        byte[] noise = new byte[4 * 1024 * 1024];
        new Random(17).nextBytes(noise);
        tar.write(noise);
        byte[] zeros = new byte[65536];
        for (long remaining = entry.getSize() - noise.length;
            remaining > 0;
            remaining -= zeros.length) tar.write(zeros);
        tar.closeArchiveEntry();
      }
    }
    return out.toByteArray();
  }

  @Test
  void pluginArchiveBudgetsAreEnforcedBeforeVersionInsert() throws Exception {
    TenantContext.run(
        TENANT,
        () -> {
          try {
            var u = upload(false, "plugin");
            for (byte[] payload :
                List.of(
                    pluginBudgetArchive(
                        true,
                        u.value.path("manifest").path("plugin").path("packageName").asString()),
                    pluginBudgetArchive(
                        false,
                        u.value.path("manifest").path("plugin").path("packageName").asString()))) {
              var m = (ObjectNode) JSON.readTree(u.manifest);
              ((ObjectNode) m.path("artifacts").get(0))
                  .put("digest", CatalogManifest.digest(payload))
                  .put("sizeBytes", payload.length);
              byte[] raw = m.toString().getBytes(StandardCharsets.UTF_8);
              assertError(
                  CommonErrors.VALIDATION_FAILED,
                  () ->
                      catalog.upload(
                          actor(AUTHOR),
                          u.pkg,
                          JSON.createObjectNode()
                              .put("version", "1.0.0")
                              .put("expectedDigest", CatalogManifest.digest(raw)),
                          raw,
                          List.of(new ByteArrayInputStream(payload)),
                          key(),
                          "\"2\""));
            }
            var oversized =
                new InputStream() {
                  long remaining = 67108865;

                  public int read() {
                    return remaining-- > 0 ? 0 : -1;
                  }

                  public int read(byte[] bytes, int offset, int length) {
                    if (remaining == 0) return -1;
                    int n = (int) Math.min(remaining, length);
                    Arrays.fill(bytes, offset, offset + n, (byte) 0);
                    remaining -= n;
                    return n;
                  }
                };
            assertError(
                CommonErrors.VALIDATION_FAILED,
                () ->
                    catalog.upload(
                        actor(AUTHOR),
                        u.pkg,
                        JSON.createObjectNode()
                            .put("version", "1.0.0")
                            .put("expectedDigest", CatalogManifest.digest(u.manifest)),
                        u.manifest,
                        List.of(oversized),
                        key(),
                        "\"2\""));
            assertEquals(
                1,
                catalog.list(actor(AUTHOR), u.pkg, "plugin", 50, null, null).path("items").size());
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
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
    var published =
        catalog.publish(
            actor(PUBLISHER), u.pkg, u.version, reason(), key(), etag(review.path("version")));
    var envelope = (ObjectNode) published.path("resourceEnvelope").deepCopy();
    String encoded = envelope.path("signature").path("value").asString();
    envelope.remove("signature");
    assertTrue(
        assertDoesNotThrow(
            () -> {
              var verifier = Signature.getInstance("Ed25519");
              verifier.initVerify(releaseKey.getPublic());
              verifier.update(ResourceSigning.canonical(envelope).getBytes(StandardCharsets.UTF_8));
              return verifier.verify(Base64.getUrlDecoder().decode(encoded));
            }));
    assertEquals(2, envelope.path("schemaVersion").asInt());
    assertEquals(
        CatalogManifest.digest(u.manifest),
        envelope.path("statement").path("manifestDigest").asString());
    assertEquals(
        List.of(CatalogManifest.digest(u.payload)),
        JSON.convertValue(envelope.path("statement").path("artifactDigests"), List.class));
    return published;
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
            assertEquals(review.path("reviewId"), published.path("reviewId"));
            assertEquals(
                review.path("reviewId"),
                catalog.detail(actor(AUTHOR), u.pkg, u.version, false).path("reviewId"));
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

  @ParameterizedTest
  @ValueSource(strings = {"skill", "plugin"})
  void r2DistinctReviewersQualificationRevokedBeforePublication(String kind) throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(true, kind);
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
  void tenantIsolationIdempotencyAndLegacyIdSeparation() {
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
                      actor(AUTHOR), body.deepCopy().put("kind", "tool"), key(), "\"0\""));
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
    for (String path :
        List.of(
            "a//b",
            "a/../b",
            "a./b",
            "a /b",
            "CON.txt",
            "COM¹",
            "LPT².txt",
            "COM³",
            "/absolute",
            "a\\b")) assertError(CommonErrors.VALIDATION_FAILED, () -> CatalogManifest.path(path));
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
    return delivery(workspace, TENANT);
  }

  CatalogDelivery delivery(UnifiedResourceWorkspace workspace, UUID tenant) {
    var distributions = mock(ResourceDistributions.class);
    when(distributions.revocations())
        .thenAnswer(
            call -> {
              var existing = db.revocations();
              if (existing != null
                  && Instant.parse(existing.path("expiresAt").asString())
                      .isAfter(clock.instant().plusSeconds(300))) return existing;
              var n =
                  JSON.createObjectNode()
                      .put("schemaVersion", 1)
                      .put("tenantId", tenant.toString())
                      .put(
                          "revision", existing == null ? 1 : existing.path("revision").asLong() + 1)
                      .put("issuedAt", clock.instant().toString())
                      .put("expiresAt", clock.instant().plusSeconds(86400).toString());
              n.set(
                  "revokedKids",
                  existing == null ? JSON.createArrayNode() : existing.path("revokedKids"));
              n.set(
                  "revokedArtifacts",
                  existing == null ? JSON.createArrayNode() : existing.path("revokedArtifacts"));
              return signing.sign(n, true);
            });
    var directory = mock(ResourceWorkspace.class);
    when(directory.tenants()).thenReturn(List.of(tenant));
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
        .put("kind", u.value.path("kind").asString())
        .put("version", u.value.path("version").asString())
        .put("manifestDigest", CatalogManifest.digest(u.manifest));
  }

  @ParameterizedTest
  @ValueSource(strings = {"skill", "plugin"})
  void memberTicketsReportsAndMaliciousWithdrawal(String kind) throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false, kind);
            var review = decide(submit(u), GENERAL, "general");
            if (kind.equals("plugin")) review = decide(review, SECURITY, "security");
            publish(u, review);
            var d = delivery(null);
            UUID client = UUID.randomUUID();
            var selection = JSON.createObjectNode().put("clientId", client.toString());
            selection.set(
                "selection",
                JSON.createObjectNode().put("action", "install").set("identity", identity(u)));
            var installed = d.select(actor(GENERAL), selection, key(), "\"0\"");
            assertEquals("applying", installed.path("state").asString());
            assertEquals(
                identity(u),
                d.installations(actor(GENERAL), client, kind, 50, null)
                    .path("items")
                    .get(0)
                    .path("resource")
                    .path("identity"));
            assertTrue(
                java.util.stream.StreamSupport.stream(
                        catalog
                            .list(actor(GENERAL), null, kind, 200, null, null)
                            .path("items")
                            .spliterator(),
                        false)
                    .anyMatch(item -> item.path("id").asString().equals(u.pkg.toString())));
            var ticketBody =
                JSON.createObjectNode()
                    .put("clientId", client.toString())
                    .put("installationRevision", 1);
            ticketBody.set("identity", identity(u));
            String ticketKey = key();
            var ticket = d.ticket(actor(GENERAL), ticketBody, ticketKey, "\"1\"");
            assertError(
                CommonErrors.CONFLICT,
                () -> d.ticket(actor(GENERAL), ticketBody, ticketKey, "\"1\""));
            assertFalse(
                tx.read(
                        () ->
                            db.rows(
                                "SELECT response FROM resource.resource_operation WHERE tenant_id=:tenant AND operation LIKE 'catalog/ticket/%'",
                                db.args()))
                    .toString()
                    .contains(ticket.path("ticket").asString()));
            var download =
                JSON.createObjectNode()
                    .put("clientId", client.toString())
                    .put("ticket", ticket.path("ticket").asString())
                    .put("artifactId", "payload");
            assertArrayEquals(
                u.payload,
                d.download(actor(GENERAL), download, key(), "\"1\"", false).readAllBytes());
            assertError(
                CommonErrors.CONFLICT,
                () -> d.download(actor(GENERAL), download, key(), "\"1\"", false));
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
            String renewKey = key();
            var renewed = d.renew(actor(GENERAL), renew, renewKey, "\"1\"");
            assertError(
                CommonErrors.CONFLICT, () -> d.renew(actor(GENERAL), renew, renewKey, "\"1\""));
            assertFalse(
                tx.read(
                        () ->
                            db.rows(
                                "SELECT response FROM resource.resource_operation WHERE tenant_id=:tenant AND operation LIKE 'catalog/renew/%'",
                                db.args()))
                    .toString()
                    .contains(renewed.path("ticket").asString()));
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

  @ParameterizedTest
  @ValueSource(strings = {"skill", "plugin"})
  void distributionPinsBytesAndWaitsForTrustedNodeObservation(String kind) throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false, kind);
            var review = decide(submit(u), GENERAL, "general");
            if (kind.equals("plugin")) review = decide(review, SECURITY, "security");
            publish(u, review);
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

  @ParameterizedTest
  @ValueSource(strings = {"optional", "default"})
  void cloudTombstonesRequireExpectedRevisionAndRetireOnlyNextGeneration(String mode)
      throws Exception {
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
            if (mode.equals("default")) assign(d, u, "instance", instance, mode);
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
            assertEquals(mode, tombstone.getInstallationMode());
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
            if (mode.equals("default")) {
              var unchanged =
                  d.resolve(
                      resolveRequest(instance, node, 3, remove.path("selections")).toBuilder()
                          .setSynchronizeAssignments(true)
                          .build());
              assertEquals(3, unchanged.getSelectionRevision());
              assertEquals(0, unchanged.getResourcesCount());
            }
            assertEquals(
                "applying",
                d.distribution(
                        actor(PUBLISHER), UUID.fromString(intent.path("distributionId").asString()))
                    .path("state")
                    .asString());
            // Leave the next intent unapplied while the current tombstone is acknowledged.
            d.distribute(actor(PUBLISHER), distributionBody(instance), key(), "\"3\"");
            reconcile(d);
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
            reconcile(d);
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
            var installationKey =
                db.args(
                    "instance",
                    instance,
                    "packageId",
                    UUID.fromString(tombstone.getIdentity().getPackageId()));
            tx.write(
                () ->
                    db.update(
                        "UPDATE resource.catalog_instance_installation SET market_installed=true"
                            + " WHERE tenant_id=:tenant AND instance_id=:instance AND package_id=:packageId",
                        installationKey));
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
            reconcile(d);
            assertEquals(1, acknowledged.resources().size());
            assertEquals(
                Boolean.FALSE,
                tx.write(
                    () ->
                        db.row(
                                "SELECT market_installed FROM resource.catalog_instance_installation"
                                    + " WHERE tenant_id=:tenant AND instance_id=:instance AND package_id=:packageId",
                                installationKey)
                            .get("market_installed")));
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
            if (mode.equals("default")) {
              var reconciled =
                  d.resolve(
                      resolveRequest(instance, node, 3, remove.path("selections")).toBuilder()
                          .setSynchronizeAssignments(true)
                          .build());
              assertEquals(0, reconciled.getResourcesCount());
              assertEquals(3, reconciled.getSelectionRevision());
            }
            var reinstall = distributionBody(instance, selection(u, "install"));
            d.distribute(actor(PUBLISHER), reinstall, key(), "\"3\"");
            var again = d.resolve(resolveRequest(instance, node, 5, reinstall.path("selections")));
            assertEquals(3, again.getResources(0).getInstallationRevision());
            current.set(
                new UnifiedResourceWorkspace.Snapshot(
                    instance,
                    GENERAL,
                    node,
                    5,
                    6,
                    6,
                    true,
                    List.of(report),
                    again.getResourcesList()));
            var other = upload(false);
            publish(other, decide(submit(other), GENERAL, "general"));
            var batch =
                distributionBody(instance, selection(u, "enable"), selection(other, "install"));
            d.distribute(actor(PUBLISHER), batch, key(), "\"5\"");
            var resources =
                d.resolve(resolveRequest(instance, node, 6, batch.path("selections")))
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

  @Test
  void withdrawnReferenceDoesNotPoisonResolution() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var healthy = upload(false);
            publish(healthy, decide(submit(healthy), GENERAL, "general"));
            var withdrawn = upload(false);
            publish(withdrawn, decide(submit(withdrawn), GENERAL, "general"));
            UUID instance = UUID.randomUUID(), node = UUID.randomUUID();
            var workspace = mock(UnifiedResourceWorkspace.class);
            var current =
                new java.util.concurrent.atomic.AtomicReference<>(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance, GENERAL, node, 1, 2, 2, true, List.of(), List.of()));
            when(workspace.snapshot(TENANT, instance)).thenAnswer(call -> current.get());
            var d = delivery(workspace);
            var install =
                distributionBody(
                    instance, selection(healthy, "install"), selection(withdrawn, "install"));
            d.distribute(actor(PUBLISHER), install, key(), "\"1\"");
            var request = resolveRequest(instance, node, 2, install.path("selections"));
            var before = d.resolve(request);
            current.set(
                new UnifiedResourceWorkspace.Snapshot(
                    instance, GENERAL, node, 2, 3, 3, true, List.of(), before.getResourcesList()));
            var version = catalog.detail(actor(AUTHOR), withdrawn.pkg, withdrawn.version, false);
            catalog.withdraw(
                actor(PUBLISHER),
                withdrawn.pkg,
                withdrawn.version,
                reason().put("mode", "withdrawn"),
                key(),
                etag(version));
            advance(86200);
            var after = d.resolve(request);
            assertTrue(
                Instant.parse(
                        after.getResourcesList().stream()
                            .filter(
                                x -> x.getIdentity().getPackageId().equals(healthy.pkg.toString()))
                            .findFirst()
                            .orElseThrow()
                            .getLease()
                            .getExpiresAt())
                    .isAfter(clock.instant().plusSeconds(300)));
            assertEquals(2, after.getResourcesCount());
            assertEquals(
                "ENABLED",
                CatalogStatePolicy.desired(
                    after.getResourcesList().stream()
                        .filter(x -> x.getIdentity().getPackageId().equals(healthy.pkg.toString()))
                        .findFirst()
                        .orElseThrow()));
            assertEquals(
                "DISABLED",
                CatalogStatePolicy.desired(
                    after.getResourcesList().stream()
                        .filter(
                            x -> x.getIdentity().getPackageId().equals(withdrawn.pkg.toString()))
                        .findFirst()
                        .orElseThrow()));
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void pendingDistributionKeepsActiveResolutionAndCanBeReplaced() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var active = upload(false);
            publish(active, decide(submit(active), GENERAL, "general"));
            var pending = upload(false);
            publish(pending, decide(submit(pending), GENERAL, "general"));
            UUID instance = UUID.randomUUID(), node = UUID.randomUUID();
            var workspace = mock(UnifiedResourceWorkspace.class);
            var current =
                new java.util.concurrent.atomic.AtomicReference<>(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance, GENERAL, node, 1, 2, 2, true, List.of(), List.of()));
            when(workspace.snapshot(TENANT, instance)).thenAnswer(call -> current.get());
            var d = delivery(workspace);
            var install = distributionBody(instance, selection(active, "install"));
            d.distribute(actor(PUBLISHER), install, key(), "\"1\"");
            var request = resolveRequest(instance, node, 2, install.path("selections"));
            var applied = d.resolve(request);
            current.set(
                new UnifiedResourceWorkspace.Snapshot(
                    instance, GENERAL, node, 2, 3, 3, true, List.of(), applied.getResourcesList()));
            var next = distributionBody(instance, selection(pending, "install"));
            d.distribute(actor(PUBLISHER), next, key(), "\"2\"");
            assertEquals(
                1,
                tx.read(
                    () ->
                        db.rows(
                                "SELECT * FROM resource.catalog_instance_installation WHERE tenant_id=:tenant AND instance_id=:instance AND selection_revision=2",
                                db.args("instance", instance))
                            .size()));
            assertEquals(
                0,
                tx.read(
                    () ->
                        db.rows(
                                "SELECT * FROM resource.catalog_instance_installation WHERE tenant_id=:tenant AND instance_id=:instance AND package_id=:pkg",
                                db.args("instance", instance, "pkg", pending.pkg))
                            .size()));
            assertEquals(applied.getResourcesList(), d.resolve(request).getResourcesList());
            advance(86200);
            var renewedCurrent = d.resolve(request);
            assertEquals(
                applied.getResources(0).getInstallationRevision(),
                renewedCurrent.getResources(0).getInstallationRevision());
            assertTrue(
                Instant.parse(renewedCurrent.getResources(0).getLease().getExpiresAt())
                    .isAfter(clock.instant().plusSeconds(300)));
            tx.write(
                () ->
                    db.update(
                        "UPDATE resource.catalog_package SET visibility='groups',group_ids=CAST(:groups AS uuid[]) WHERE tenant_id=:tenant AND id=:pkg",
                        db.args("pkg", pending.pkg, "groups", "{" + UUID.randomUUID() + "}")));
            var denied = d.resolve(resolveRequest(instance, node, 3, next.path("selections")));
            var deniedItem =
                denied.getResourcesList().stream()
                    .filter(ref -> ref.getIdentity().getPackageId().equals(pending.pkg.toString()))
                    .findFirst()
                    .orElseThrow();
            assertEquals("DISABLED", CatalogStatePolicy.desired(deniedItem));
            assertEquals("optional", deniedItem.getInstallationMode());
            assertTrue(
                denied.getResourcesList().stream()
                    .anyMatch(
                        ref ->
                            ref.getIdentity().getPackageId().equals(active.pkg.toString())
                                && CatalogStatePolicy.desired(ref).equals("ENABLED")));
            var cancel = distributionBody(instance);
            var replacement = d.distribute(actor(PUBLISHER), cancel, key(), "\"2\"");
            assertEquals(4, replacement.path("selectionRevision").asLong());
            assertError(
                CommonErrors.CONFLICT,
                () -> d.resolve(resolveRequest(instance, node, 3, next.path("selections"))));
            var resolution =
                d.resolve(resolveRequest(instance, node, 4, cancel.path("selections")));
            assertEquals(1, resolution.getResourcesCount());
            assertEquals(
                active.pkg.toString(), resolution.getResources(0).getIdentity().getPackageId());
            tx.write(
                () ->
                    db.update(
                        "UPDATE resource.catalog_instance_counter SET revision=:maximum WHERE tenant_id=:tenant AND instance_id=:instance AND package_id=:pkg",
                        db.args(
                            "instance",
                            instance,
                            "pkg",
                            active.pkg,
                            "maximum",
                            ResourceOperations.MAX)));
            assertError(
                CatalogStatePolicy.conflict().errorCode(),
                () ->
                    d.distribute(
                        actor(PUBLISHER),
                        distributionBody(instance, selection(active, "enable")),
                        key(),
                        "\"2\""));
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  ObjectNode trustInput(Upload upload, JsonNode envelope) {
    var input =
        JSON.createObjectNode()
            .put("protocol", 2)
            .put("now", clock.instant().toString())
            .put("rawManifest", new String(upload.manifest, StandardCharsets.UTF_8));
    input.set("expected", identity(upload));
    input.set("envelope", envelope);
    input.set(
        "artifactBytes",
        JSON.createObjectNode().put("payload", Base64.getEncoder().encodeToString(upload.payload)));
    return input;
  }

  CatalogTrust verifier() {
    var trust = JSON.createObjectNode();
    trust.set(
        "release",
        JSON.createObjectNode()
            .put("kid", "release")
            .put(
                "publicKeySpkiBase64",
                Base64.getEncoder().encodeToString(releaseKey.getPublic().getEncoded())));
    trust.set(
        "revocation",
        JSON.createObjectNode()
            .put("kid", "revoke")
            .put(
                "publicKeySpkiBase64",
                Base64.getEncoder().encodeToString(revocationKey.getPublic().getEncoded())));
    return new CatalogTrust(trust);
  }

  @Test
  void actualReleaseAndRevocationsPassIndependentVerification() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var u = upload(false);
            var published = publish(u, decide(submit(u), GENERAL, "general"));
            var input = trustInput(u, published.path("resourceEnvelope"));
            var verifier = verifier();
            assertDoesNotThrow(() -> verifier.verify("release", input));
            var tampered = input.deepCopy();
            ((ObjectNode) tampered.path("envelope")).put("releaseSequence", 2);
            assertEquals(
                "signature",
                assertThrows(
                        IllegalArgumentException.class, () -> verifier.verify("release", tampered))
                    .getMessage());
            var current = catalog.detail(actor(AUTHOR), u.pkg, u.version, false);
            catalog.withdraw(
                actor(PUBLISHER),
                u.pkg,
                u.version,
                reason().put("mode", "malicious"),
                key(),
                etag(current));
            var revoked = JSON.createObjectNode();
            revoked.set("release", input);
            revoked.set("list", tx.read(() -> db.revocations()));
            assertEquals(
                "revoked",
                assertThrows(
                        IllegalArgumentException.class,
                        () -> verifier.verify("revocation", revoked))
                    .getMessage());
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void maliciousPeerDoesNotPoisonRetainedDistribution() throws Exception {
    TenantContext.call(
        TENANT,
        () -> {
          try {
            var healthy = upload(false);
            publish(healthy, decide(submit(healthy), GENERAL, "general"));
            var malicious = upload(false);
            publish(malicious, decide(submit(malicious), GENERAL, "general"));
            UUID instance = UUID.randomUUID(), node = UUID.randomUUID();
            var workspace = mock(UnifiedResourceWorkspace.class);
            var current =
                new java.util.concurrent.atomic.AtomicReference<>(
                    new UnifiedResourceWorkspace.Snapshot(
                        instance, GENERAL, node, 1, 2, 2, true, List.of(), List.of()));
            when(workspace.snapshot(TENANT, instance)).thenAnswer(call -> current.get());
            var d = delivery(workspace);
            var install =
                distributionBody(
                    instance, selection(healthy, "install"), selection(malicious, "install"));
            d.distribute(actor(PUBLISHER), install, key(), "\"1\"");
            var before = d.resolve(resolveRequest(instance, node, 2, install.path("selections")));
            current.set(
                new UnifiedResourceWorkspace.Snapshot(
                    instance, GENERAL, node, 2, 3, 3, true, List.of(), before.getResourcesList()));
            var version = catalog.detail(actor(AUTHOR), malicious.pkg, malicious.version, false);
            catalog.withdraw(
                actor(PUBLISHER),
                malicious.pkg,
                malicious.version,
                reason().put("mode", "malicious"),
                key(),
                etag(version));
            var retain = distributionBody(instance);
            d.distribute(actor(PUBLISHER), retain, key(), "\"2\"");
            var result = d.resolve(resolveRequest(instance, node, 3, retain.path("selections")));
            assertEquals(2, result.getResourcesCount());
            assertEquals(
                "ENABLED",
                CatalogStatePolicy.desired(
                    result.getResourcesList().stream()
                        .filter(x -> x.getIdentity().getPackageId().equals(healthy.pkg.toString()))
                        .findFirst()
                        .orElseThrow()));
            assertEquals(
                "DISABLED",
                CatalogStatePolicy.desired(
                    result.getResourcesList().stream()
                        .filter(
                            x -> x.getIdentity().getPackageId().equals(malicious.pkg.toString()))
                        .findFirst()
                        .orElseThrow()));
            assertTrue(result.getRevocations().getRevokedArtifactsCount() > 0);
            return null;
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
  }

  @Test
  void reconciliationIsBoundedAndSkipsHistoricalAndCompletedIntents() {
    UUID tenant = UUID.randomUUID(), owner = UUID.randomUUID(), node = UUID.randomUUID();
    var workspace = mock(UnifiedResourceWorkspace.class);
    Set<UUID> pending = new HashSet<>(), history = new HashSet<>(), visited = new HashSet<>();
    when(workspace.snapshot(eq(tenant), any()))
        .thenAnswer(
            call -> {
              UUID instance = call.getArgument(1);
              assertFalse(history.contains(instance));
              visited.add(instance);
              return new UnifiedResourceWorkspace.Snapshot(
                  instance, owner, node, 2, 3, 3, true, List.of(), List.of());
            });
    var d = delivery(workspace, tenant);
    TenantContext.run(
        tenant,
        () ->
            tx.write(
                () -> {
                  for (int i = 0; i < 500; i++) {
                    UUID instance = UUID.randomUUID();
                    boolean old = i < 250;
                    (old ? history : pending).add(instance);
                    db.update(
                        "INSERT INTO resource.catalog_distribution(id,tenant_id,actor_id,instance_id,owner_id,node_id,selection_revision,fingerprint,document,resolution,lifecycle,next_reconcile_at) VALUES(:id,:tenant,:owner,:instance,:owner,:node,2,'fixture','{\"selections\":[]}', '{\"resources\":[]}',:state,:due)",
                        db.args(
                            "id",
                            UUID.randomUUID(),
                            "owner",
                            owner,
                            "instance",
                            instance,
                            "node",
                            node,
                            "state",
                            old ? "superseded" : "pending",
                            "due",
                            java.sql.Timestamp.from(clock.instant())));
                  }
                  return true;
                }));
    for (int i = 0; i < 3; i++) {
      int before = visited.size();
      d.reconcile();
      assertTrue(visited.size() - before <= 100);
    }
    assertEquals(pending, visited);
    clearInvocations(workspace);
    advance(10);
    d.reconcile();
    verifyNoInteractions(workspace);
  }

  @Test
  void concurrentDownloadsConsumeExactlyOneTicket() throws Exception {
    var d = delivery(null);
    UUID client = UUID.randomUUID();
    var download =
        TenantContext.call(
            TENANT,
            () -> {
              try {
                var u = upload(false);
                publish(u, decide(submit(u), GENERAL, "general"));
                d.select(actor(GENERAL), memberSelection(u, client, "install"), key(), "\"0\"");
                var input =
                    JSON.createObjectNode()
                        .put("clientId", client.toString())
                        .put("installationRevision", 1);
                input.set("identity", identity(u));
                var ticket = d.ticket(actor(GENERAL), input, key(), "\"1\"");
                return JSON.createObjectNode()
                    .put("clientId", client.toString())
                    .put("ticket", ticket.path("ticket").asString())
                    .put("artifactId", "payload");
              } catch (Exception e) {
                throw new RuntimeException(e);
              }
            });
    try (var executor = java.util.concurrent.Executors.newFixedThreadPool(2)) {
      var start = new java.util.concurrent.CountDownLatch(1);
      var attempts = new ArrayList<java.util.concurrent.Future<Boolean>>();
      for (int i = 0; i < 2; i++)
        attempts.add(
            executor.submit(
                () -> {
                  start.await();
                  return TenantContext.call(
                      TENANT,
                      () -> {
                        try (var stream =
                            d.download(actor(GENERAL), download, key(), "\"1\"", false)) {
                          stream.readAllBytes();
                          return true;
                        } catch (AurigaException e) {
                          assertEquals(CommonErrors.CONFLICT, e.errorCode());
                          return false;
                        } catch (IOException e) {
                          throw new UncheckedIOException(e);
                        }
                      });
                }));
      start.countDown();
      int success = 0;
      for (var attempt : attempts)
        if (attempt.get(20, java.util.concurrent.TimeUnit.SECONDS)) success++;
      assertEquals(1, success);
    }
  }

  @Test
  void failedRevocationBatchDoesNotStarveOtherInstances() {
    UUID tenant = UUID.randomUUID();
    var workspace = mock(UnifiedResourceWorkspace.class);
    var failed = new HashSet<UUID>();
    doAnswer(
            call -> {
              UUID instance = call.getArgument(1);
              if (failed.contains(instance)) throw new IllegalStateException("offline fixture");
              return null;
            })
        .when(workspace)
        .revoke(eq(tenant), any(), any());
    var d = delivery(workspace, tenant);
    TenantContext.run(
        tenant,
        () ->
            tx.write(
                () -> {
                  for (int i = 0; i < 200; i++) {
                    UUID instance = UUID.randomUUID();
                    if (i < 100) failed.add(instance);
                    db.update(
                        "INSERT INTO resource.catalog_revocation_delivery(id,tenant_id,instance_id,revision) VALUES(:id,:tenant,:instance,1)",
                        db.args(
                            "id", auriga.common.persistence.UuidV7.next(), "instance", instance));
                  }
                  return true;
                }));
    d.reconcile();
    verify(workspace, times(100)).revoke(eq(tenant), any(), any());
    clearInvocations(workspace);
    d.reconcile();
    verify(workspace, times(100)).revoke(eq(tenant), any(), any());
    assertEquals(
        100,
        TenantContext.call(
            tenant,
            () ->
                tx.read(
                    () ->
                        db.rows(
                                "SELECT id FROM resource.catalog_revocation_delivery WHERE tenant_id=:tenant AND completed_revision=revision",
                                db.args())
                            .size())));
    clearInvocations(workspace);
    d.reconcile();
    verifyNoInteractions(workspace);
  }
}
