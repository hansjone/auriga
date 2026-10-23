package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.node.v1.*;
import auriga.contracts.org.v1.*;
import auriga.org.web.model.WorkspacePolicy;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.app.*;
import auriga.workspace.domain.*;
import auriga.workspace.grpc.*;
import auriga.workspace.persistence.NodeSessionLease;
import auriga.workspace.persistence.WorkspaceStore;
import auriga.workspace.web.model.HeartbeatRequest;
import auriga.workspace.web.model.HeartbeatRequestActivity;
import auriga.workspace.web.model.HeartbeatRequestHealth;
import com.google.protobuf.ByteString;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.*;
import io.grpc.stub.StreamObserver;
import java.io.ByteArrayInputStream;
import java.nio.file.*;
import java.security.KeyPair;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real PostgreSQL + TLS gRPC fake node using only the generated protobuf protocol. */
@ExtendWith({PostgresExtension.class, auriga.testsupport.NatsExtension.class})
class WorkspaceIntegrationTest {
  static WorkspaceStore store;
  @TempDir Path directory;
  final UUID tenant = UuidV7.next(), owner = UuidV7.next();
  final MutableClock clock = new MutableClock();
  WorkspaceService service;
  NodeEnrollment enrollment;
  NodeSessions sessions;
  NodeCa ca;
  KeyPair caKey;
  WorkspaceInputs inputs;
  final WorkspacePolicy policy = new WorkspacePolicy().maxConcurrentStarts(1);
  InstanceQuota quota =
      InstanceQuota.newBuilder().setMaxInstances(1).setAlwaysOnAllowed(true).build();
  MemberStatus memberStatus = MemberStatus.MEMBER_STATUS_ACTIVE;

  @BeforeAll
  static void database() {
    var roles =
        PostgresExtension.serviceRoles(
            WorkspaceIntegrationTest.class, "workspace_owner", "workspace_app", "workspace");
    CommonMigrations.migrate(roles.owner().dataSource(), "workspace");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("workspace")
                .defaultSchema("workspace")
                .locations("classpath:db/workspace")
                .load())
        .migrate();
    store =
        new WorkspaceStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @BeforeEach
  void setup() throws Exception {
    // Independent tenants exercise RLS without resetting shared fixtures through a privileged role.
    caKey = TestPki.key();
    ca = new NodeCa(TestPki.ca(caKey, clock.instant()), caKey.getPrivate(), clock);
    var template =
        InstanceSpec.newBuilder()
            .setRuntime(RuntimeKind.RUNTIME_KIND_DOCKER)
            .setResources(
                Resources.newBuilder()
                    .setCpuMillis(1000)
                    .setMemoryMb(512)
                    .setDiskGb(1)
                    .setPids(128))
            .setDsh(
                DshRuntime.newBuilder()
                    .setVersion("1.0.0")
                    .setArtifactId("dsh-test")
                    .setImage("example.com/dsh@sha256:" + "0".repeat(64))
                    .setReleaseEnvelope(
                        RuntimeReleaseEnvelope.newBuilder()
                            .setSchemaVersion(1)
                            .setPurpose("dsh_runtime")
                            .setName("dsh")
                            .setArtifactId("dsh-test")
                            .setDshVersion("1.0.0")
                            .setOs("linux")
                            .setArch("amd64")
                            .setImageDigest("sha256:" + "0".repeat(64))
                            .setReleaseSequence(1)
                            .setIssuedAt(
                                com.google.protobuf.Timestamp.newBuilder().setSeconds(1792713600))
                            .setSignature(
                                RuntimeReleaseSignature.newBuilder()
                                    .setAlg("Ed25519")
                                    .setKid("test-root")
                                    .setValue(
                                        Base64.getUrlEncoder()
                                            .withoutPadding()
                                            .encodeToString(new byte[64])))))
            .setProfile(Profile.newBuilder().setListenPort(7860))
            .build();
    inputs =
        new WorkspaceInputs() {
          public Member member(UUID tid, UUID id) {
            return Member.newBuilder()
                .setAccountId(id.toString())
                .setTenantId(tid.toString())
                .setStatus(memberStatus)
                .setDisplayName("alice")
                .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                .build();
          }

          public InstanceQuota quota(UUID tid, UUID id) {
            return quota;
          }

          public WorkspacePolicy policy(UUID tid) {
            return policy;
          }
        };
    service =
        new WorkspaceService(
            store,
            inputs,
            new WorkspaceTemplates(List.of(template)),
            new WorkspaceCrypto(WorkspaceCrypto.random(32)),
            new OutboxEventPublisher(
                store.jdbc,
                "workspace",
                "workspace",
                new EventSchemas(),
                WorkspaceService.JSON,
                clock,
                () -> {}),
            clock);
    enrollment = new NodeEnrollment(store, ca, clock, "https://127.0.0.1:7443", List.of());
    sessions = new NodeSessions(enrollment, service);
  }

  @AfterEach
  void close() {
    sessions.close();
  }

  AurigaPrincipal actor() {
    return new AurigaPrincipal(
        owner.toString(),
        tenant,
        PrincipalType.USER,
        TokenContext.PORTAL,
        null,
        null,
        "member",
        List.of(),
        Set.of(),
        clock.instant(),
        null,
        UuidV7.next().toString());
  }

  NodeEnrollment.Token token() {
    return TenantContext.call(tenant, () -> enrollment.createToken("test", 5, owner));
  }

  JoinRequest joinRequest(String token, KeyPair key) throws Exception {
    return JoinRequest.newBuilder()
        .setToken(token)
        .setCsr(ByteString.copyFrom(TestPki.csr(key)))
        .setSealingPublicKey(
            ByteString.copyFrom(
                WorkspaceCrypto.hpke()
                    .serializePublicKey(WorkspaceCrypto.hpke().generatePrivateKey().getPublic())))
        .setOs("linux")
        .setArch("amd64")
        .setHostname("example-node")
        .setAgentVersion("1.0.0")
        .build();
  }

  @Test
  void ingressHelloAndRouteRecoveryMigrationDeletion() throws Exception {
    var request = joinRequest(token().token(), TestPki.key());
    var joined = enrollment.join(request);
    UUID node = UUID.fromString(joined.getNodeId());
    var fake = new FakeNode();
    var context =
        Context.current()
            .withValue(NodeSessions.PEER, NodeCa.parse(joined.getCertificateChain().toByteArray()));
    var stream = context.call(() -> sessions.session(fake));
    stream.onNext(
        SessionRequest.newBuilder()
            .setHello(
                Hello.newBuilder()
                    .setNodeId(node.toString())
                    .setProtocolVersion(1)
                    .setOs("linux")
                    .setArch("amd64")
                    .setIngressEndpoint("https://node.example.com:7443")
                    .addDrivers(RuntimeKind.RUNTIME_KIND_DOCKER)
                    .setCapacity(
                        Capacity.newBuilder().setCpuMillis(10000).setMemoryMb(8192).setDiskGb(100))
                    .setSealingKeyId(
                        HexFormat.of()
                            .formatHex(
                                WorkspaceCrypto.hash(request.getSealingPublicKey().toByteArray()))
                            .substring(0, 16)))
            .build());
    assertTrue(fake.next().hasWelcome());
    assertEquals(
        "https://node.example.com:7443",
        store.in(tenant, () -> store.node(node).get("ingress_endpoint")));
    UUID instance =
        TenantContext.call(tenant, () -> (UUID) service.open(owner).instance().get("id"));
    assertEquals(node, store.in(tenant, () -> store.instance(instance).get("node_id")));
    var generation =
        store.in(tenant, () -> ((Number) store.instance(instance).get("generation")).longValue());
    TenantContext.run(
        tenant,
        () ->
            service.status(
                node,
                InstanceStatus.newBuilder()
                    .setInstanceId(instance.toString())
                    .setObservedGeneration(generation)
                    .setPhase(InstancePhase.INSTANCE_PHASE_RUNNING)
                    .setReady(true)
                    .build()));
    // Publishing is separate from business transactions. Simulate initial NATS outage.
    try (var unavailable =
        new WorkspaceRoutes(
            store,
            () -> {
              throw new IllegalStateException("offline");
            })) {
      assertDoesNotThrow(unavailable::refresh);
      assertEquals("running", store.in(tenant, () -> store.instance(instance).get("phase")));
    }
    String nats = auriga.testsupport.NatsExtension.url();
    try (var reader = io.nats.client.Nats.connect(nats);
        var routes = WorkspaceRoutes.configured(store, nats, "", "", false)) {
      routes.refresh();
      var kv = reader.keyValue(WorkspaceRoutes.BUCKET);
      var first = WorkspaceService.JSON.readTree(kv.get(instance.toString()).getValue());
      assertEquals(node.toString(), first.path("nodeId").asText());
      assertEquals(owner.toString(), first.path("ownerId").asText());
      assertEquals("running", first.path("state").asText());
      // A rolled back change must not leak into routing.
      assertThrows(
          IllegalStateException.class,
          () ->
              store.in(
                  tenant,
                  () -> {
                    store.jdbc.update(
                        "UPDATE node SET ingress_endpoint=? WHERE tenant_id=? AND id=?",
                        "https://rolled-back.example.com:7443",
                        tenant,
                        node);
                    throw new IllegalStateException("rollback");
                  }));
      routes.refresh();
      assertEquals(
          "https://node.example.com:7443",
          WorkspaceService.JSON
              .readTree(kv.get(instance.toString()).getValue())
              .path("ingress")
              .asText());
      // Changed node/endpoint is picked up from committed state, including after publisher restart.
      UUID replacement =
          UUID.fromString(enrollment.join(joinRequest(token().token(), TestPki.key())).getNodeId());
      store.in(
          tenant,
          () -> {
            store.jdbc.update(
                "UPDATE node SET ingress_endpoint=?,last_seen_at=? WHERE tenant_id=? AND id=?",
                "npipe:////./pipe/replacement",
                WorkspaceStore.time(clock.instant()),
                tenant,
                replacement);
            return store.jdbc.update(
                "UPDATE instance SET node_id=?,generation=generation+1 WHERE tenant_id=? AND id=?",
                replacement,
                tenant,
                instance);
          });
      try (var restarted = WorkspaceRoutes.configured(store, nats, "", "", false)) {
        restarted.refresh();
      }
      var migrated = WorkspaceService.JSON.readTree(kv.get(instance.toString()).getValue());
      assertEquals(replacement.toString(), migrated.path("nodeId").asText());
      assertEquals("npipe:////./pipe/replacement", migrated.path("ingress").asText());
      TenantContext.run(
          tenant, () -> new WorkspaceAdmin(service, inputs).delete(instance, false, owner));
      routes.refresh();
      assertTrue(
          kv.get(instance.toString()) == null
              || kv.get(instance.toString()).getOperation()
                  != io.nats.client.api.KeyValueOperation.PUT);
    }
    stream.onCompleted();
  }

  @Test
  void helloRejectsInvalidIngressScheme() throws Exception {
    var request = joinRequest(token().token(), TestPki.key());
    var joined = enrollment.join(request);
    var fake = new FakeNode();
    var context =
        Context.current()
            .withValue(NodeSessions.PEER, NodeCa.parse(joined.getCertificateChain().toByteArray()));
    var stream = context.call(() -> sessions.session(fake));
    stream.onNext(
        SessionRequest.newBuilder()
            .setHello(
                Hello.newBuilder()
                    .setNodeId(joined.getNodeId())
                    .setProtocolVersion(1)
                    .setIngressEndpoint("http://node.example.com:7443"))
            .build());
    assertEquals(Status.Code.INVALID_ARGUMENT, fake.error.get(5, TimeUnit.SECONDS).getCode());
  }

  @Test
  void platformRegistryRequiresRegistrationBeforeTenantTransactionAndSeparatesResourceKinds() {
    UUID id = UuidV7.next();
    store.register(id, "instance", tenant);
    store.register(id, "instance", tenant);
    assertEquals(tenant, store.route(id, "instance"));
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> store.route(id, "token")).errorCode().code());
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  assertThrows(
                      IllegalStateException.class, () -> store.register(id, "instance", tenant));
                  assertEquals(
                      0L,
                      store.jdbc.queryForObject(
                          "SELECT count(*) FROM workspace_registry WHERE id=?", Long.class, id));
                  return null;
                }));
  }

  @Test
  void formalDockerReleaseSupportsPinsDefaultsAndRuntimeQueriesWithoutLegacyStatement() {
    var first = service.templates.all().getFirst();
    var second = first.toBuilder();
    second.getDshBuilder().setVersion("2.0.0");
    second
        .getDshBuilder()
        .getReleaseEnvelopeBuilder()
        .setDshVersion("2.0.0")
        .setReleaseSequence(2)
        .setIssuedAt(com.google.protobuf.Timestamp.newBuilder().setSeconds(0).setNanos(123456789));
    var app =
        new WorkspaceService(
            store,
            inputs,
            new WorkspaceTemplates(List.of(first, second.build())),
            new WorkspaceCrypto(WorkspaceCrypto.random(32)),
            new OutboxEventPublisher(
                store.jdbc,
                "workspace",
                "workspace",
                new EventSchemas(),
                WorkspaceService.JSON,
                clock,
                () -> {}),
            clock);
    TenantContext.run(
        tenant,
        () -> {
          var id = (UUID) app.open(owner).instance().get("id");
          var admin = new WorkspaceAdmin(app, inputs);
          admin.patch(
              id, WorkspaceService.JSON.createObjectNode().put("dshVersionPin", "2.0.0"), null);
          var pinned = WorkspaceService.spec(store.tx(() -> store.instance(id)));
          assertEquals("2.0.0", pinned.getDsh().getVersion());
          assertFalse(pinned.getDsh().hasSigned());
          admin.defaultRuntime("1.0.0");
          assertEquals(
              "2.0.0",
              WorkspaceService.spec(store.tx(() -> store.instance(id))).getDsh().getVersion());
          admin.patch(id, WorkspaceService.JSON.createObjectNode().putNull("dshVersionPin"), null);
          assertEquals(
              "1.0.0",
              WorkspaceService.spec(store.tx(() -> store.instance(id))).getDsh().getVersion());
          admin.defaultRuntime("2.0.0");
          assertEquals(
              "2.0.0",
              WorkspaceService.spec(store.tx(() -> store.instance(id))).getDsh().getVersion());
          app.tickTenant();
          var runtimes = new WorkspaceQueries(app).runtimes();
          assertEquals("2.0.0", runtimes.getDefaultVersion());
          var runtime =
              runtimes.getItems().stream()
                  .filter(r -> r.getVersion().equals("2.0.0"))
                  .findFirst()
                  .orElseThrow();
          var artifact = runtime.getArtifacts().getFirst();
          assertEquals("linux", artifact.getOs().toString());
          assertEquals("amd64", artifact.getArch().toString());
          assertEquals("", artifact.getDigest());
          assertEquals(Instant.ofEpochSecond(0, 123456789), runtime.getCreatedAt().toInstant());
        });
  }

  WorkspaceService serviceFor(WorkspaceStore target) {
    return new WorkspaceService(
        target,
        inputs,
        service.templates,
        new WorkspaceCrypto(WorkspaceCrypto.random(32)),
        new OutboxEventPublisher(
            target.jdbc,
            "workspace",
            "workspace",
            new EventSchemas(),
            WorkspaceService.JSON,
            clock,
            () -> {}),
        clock);
  }

  @ParameterizedTest
  @ValueSource(ints = {1, 2})
  void concurrentOpenJoinAndIdempotentTokensOutnumberPoolWithoutDeadlock(int poolSize)
      throws Exception {
    policy.setMaxConcurrentStarts(100);
    List<JoinRequest> requests = new ArrayList<>();
    for (int i = 0; i < 4; i++) requests.add(joinRequest(token().token(), TestPki.key()));
    try (var pool = new com.zaxxer.hikari.HikariDataSource()) {
      pool.setDataSource(store.jdbc.getDataSource());
      pool.setMaximumPoolSize(poolSize);
      pool.setMinimumIdle(0);
      pool.setConnectionTimeout(2000);
      var small = new WorkspaceStore(new JdbcTemplate(pool), new TenantTransactionManager(pool));
      var app = serviceFor(small);
      var nodes = new NodeEnrollment(small, ca, clock, "https://127.0.0.1:7443", List.of());
      try (var workers = Executors.newVirtualThreadPerTaskExecutor()) {
        CountDownLatch ready = new CountDownLatch(12), start = new CountDownLatch(1);
        List<Future<String>> futures = new ArrayList<>();
        for (int i = 0; i < 12; i++) {
          int index = i;
          futures.add(
              workers.submit(
                  () -> {
                    ready.countDown();
                    assertTrue(start.await(10, TimeUnit.SECONDS));
                    return TenantContext.call(
                        tenant,
                        () -> {
                          if (index % 3 == 0) {
                            UUID account = UuidV7.next();
                            var opened = app.open(account);
                            assertEquals(
                                opened.instance().get("id"),
                                app.open(account).instance().get("id"));
                            return opened.instance().get("id").toString();
                          }
                          if (index % 3 == 1)
                            return nodes.join(requests.get(index / 3)).getNodeId();
                          return app.joinToken(nodes, "concurrent", 5, owner, "concurrent-key")
                              .token();
                        });
                  }));
        }
        assertTrue(ready.await(10, TimeUnit.SECONDS));
        start.countDown();
        Set<String> credentials = new HashSet<>();
        for (int i = 0; i < futures.size(); i++) {
          String value = futures.get(i).get(20, TimeUnit.SECONDS);
          if (i % 3 == 2) credentials.add(value);
        }
        assertEquals(1, credentials.size());
      }
      assertEquals(
          4L,
          small.in(
              tenant,
              () ->
                  small.jdbc.queryForObject(
                      "SELECT count(*) FROM instance WHERE tenant_id=?", Long.class, tenant)));
      assertEquals(
          4L,
          small.in(
              tenant,
              () ->
                  small.jdbc.queryForObject(
                      "SELECT count(*) FROM node WHERE tenant_id=?", Long.class, tenant)));
      assertEquals(
          5L,
          small.in(
              tenant,
              () ->
                  small.jdbc.queryForObject(
                      "SELECT count(*) FROM join_token WHERE tenant_id=?", Long.class, tenant)));
      assertEquals(0, pool.getHikariPoolMXBean().getActiveConnections());
    }
  }

  @Test
  void leasesDoNotHoldPoolConnectionsAndFenceExpiredHolders() throws Exception {
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    // A two-connection pool must support more idle sessions than connections.
    try (var pool = new com.zaxxer.hikari.HikariDataSource()) {
      pool.setDataSource(store.jdbc.getDataSource());
      pool.setMaximumPoolSize(2);
      pool.setMinimumIdle(0);
      pool.setConnectionTimeout(1000);
      var small = new WorkspaceStore(new JdbcTemplate(pool), new TenantTransactionManager(pool));
      List<NodeSessionLease> leases = new ArrayList<>();
      for (int i = 0; i < 5; i++) {
        UUID n =
            i == 0
                ? node
                : UUID.fromString(
                    enrollment.join(joinRequest(token().token(), TestPki.key())).getNodeId());
        var lease = new NodeSessionLease(small, tenant, n, UuidV7.next());
        lease.acquire();
        leases.add(lease);
        assertEquals(0, pool.getHikariPoolMXBean().getActiveConnections());
      }
      var replacement = new NodeSessionLease(small, tenant, node, UuidV7.next());
      assertEquals(
          "common.conflict",
          assertThrows(AurigaException.class, replacement::acquire).errorCode().code());
      small.in(
          tenant,
          () ->
              small.jdbc.update(
                  "UPDATE node_session SET expires_at=CURRENT_TIMESTAMP-INTERVAL '1 second' WHERE tenant_id=? AND node_id=?",
                  tenant,
                  node));
      replacement.acquire();
      assertEquals(
          "common.conflict",
          assertThrows(AurigaException.class, () -> leases.getFirst().guard(() -> true))
              .errorCode()
              .code());
      leases.getFirst().release();
      assertTrue(replacement.guard(() -> true));
      replacement.release();
      for (var lease : leases) lease.release();
    }
  }

  @Test
  void durableRestartResultsBackoffAndDeleteAcknowledgement() throws Exception {
    UUID node =
        UUID.fromString(enrollment.join(joinRequest(token().token(), TestPki.key())).getNodeId());
    UUID instance =
        TenantContext.call(tenant, () -> (UUID) service.open(owner).instance().get("id"));
    store.in(
        tenant,
        () ->
            store.jdbc.update(
                "UPDATE instance SET node_id=? WHERE tenant_id=? AND id=?",
                node,
                tenant,
                instance));
    UUID holder = UuidV7.next();
    var lease = new NodeSessionLease(store, tenant, node, holder);
    lease.acquire();
    var delivery = new NodeDeliveries(store, clock);
    TenantContext.run(tenant, () -> service.operate(instance, actor(), "restart"));
    UUID job = null;
    for (int attempt = 1; attempt <= 5; attempt++) {
      var claimed = lease.guard(() -> delivery.claim(node, holder, Set.of()));
      assertEquals(1, claimed.size());
      var d = claimed.getFirst();
      if (job == null) job = d.id();
      else assertEquals(job, d.id());
      assertEquals(attempt, d.attempt());
      assertTrue(lease.guard(() -> delivery.claim(node, holder, Set.of(d.id()))).isEmpty());
      var failed =
          CommandResult.newBuilder()
              .setCommandId(d.id().toString())
              .setInstanceId(instance.toString())
              .setOk(false)
              .build();
      lease.guard(
          () -> {
            delivery.result(node, holder, d, failed);
            return null;
          });
      assertTrue(lease.guard(() -> delivery.claim(node, holder, Set.of())).isEmpty());
      var row =
          store.in(
              tenant,
              () ->
                  store.one(
                      "SELECT * FROM workspace_job WHERE tenant_id=? AND id=?", tenant, d.id()));
      assertArrayEquals(failed.toByteArray(), (byte[]) row.get("result"));
      clock.now = clock.now.plusSeconds(301);
    }
    assertTrue(lease.guard(() -> delivery.claim(node, holder, Set.of())).isEmpty());
    UUID exhausted = job;
    assertEquals(
        true,
        store.in(
            tenant,
            () ->
                store
                    .one(
                        "SELECT * FROM workspace_job WHERE tenant_id=? AND id=?", tenant, exhausted)
                    .get("failed")));
    TenantContext.run(
        tenant, () -> new WorkspaceAdmin(service, inputs).delete(instance, false, owner));
    var deletion = lease.guard(() -> delivery.claim(node, holder, Set.of())).getFirst();
    assertEquals("delete", deletion.kind());
    var absent =
        InstanceStatus.newBuilder()
            .setInstanceId(instance.toString())
            .setObservedGeneration(deletion.generation())
            .setPhase(InstancePhase.INSTANCE_PHASE_ABSENT)
            .build();
    lease.guard(
        () -> {
          delivery.deleted(node, holder, deletion, absent);
          return null;
        });
    assertTrue(lease.guard(() -> delivery.claim(node, holder, Set.of())).isEmpty());
    lease.release();
  }

  @Test
  @ExtendWith(org.springframework.boot.test.system.OutputCaptureExtension.class)
  void commandTimeoutKeepsSessionAndExhaustedDeleteLogsWithoutReleasingCapacity(
      org.springframework.boot.test.system.CapturedOutput logs) throws Exception {
    var request = joinRequest(token().token(), TestPki.key());
    var joined = enrollment.join(request);
    UUID node = UUID.fromString(joined.getNodeId());
    var fake = new FakeNode();
    var context =
        Context.current()
            .withValue(NodeSessions.PEER, NodeCa.parse(joined.getCertificateChain().toByteArray()));
    var stream = context.call(() -> sessions.session(fake));
    stream.onNext(
        SessionRequest.newBuilder()
            .setHello(
                Hello.newBuilder()
                    .setNodeId(node.toString())
                    .setProtocolVersion(1)
                    .setSealingKeyId(
                        HexFormat.of()
                            .formatHex(
                                WorkspaceCrypto.hash(request.getSealingPublicKey().toByteArray()))
                            .substring(0, 16)))
            .build());
    assertTrue(fake.next().hasWelcome());
    stream.onNext(SessionRequest.newBuilder().setInventory(Inventory.getDefaultInstance()).build());
    UUID instance =
        TenantContext.call(tenant, () -> (UUID) service.open(owner).instance().get("id"));
    store.in(
        tenant,
        () ->
            store.jdbc.update(
                "UPDATE instance SET node_id=? WHERE tenant_id=? AND id=?",
                node,
                tenant,
                instance));
    TenantContext.run(tenant, () -> service.operate(instance, actor(), "restart"));
    sessions.dispatch();
    SessionResponse frame;
    do {
      frame = fake.next();
    } while (!frame.hasCommand());
    String commandId = frame.getCommand().getCommandId();
    clock.now = clock.now.plusSeconds(121);
    stream.onNext(SessionRequest.newBuilder().setUsage(NodeUsage.getDefaultInstance()).build());
    sessions.expire();
    assertFalse(fake.error.isDone());
    var timedOut =
        store.in(
            tenant,
            () ->
                store.one(
                    "SELECT * FROM workspace_job WHERE tenant_id=? AND id=?",
                    tenant,
                    UUID.fromString(commandId)));
    assertEquals("common.unavailable", timedOut.get("last_error"));
    assertEquals(false, timedOut.get("done"));
    clock.now = clock.now.plusSeconds(6);
    sessions.dispatch();
    do {
      frame = fake.next();
    } while (!frame.hasCommand());
    assertEquals(commandId, frame.getCommand().getCommandId());
    stream.onNext(
        SessionRequest.newBuilder()
            .setResult(
                CommandResult.newBuilder()
                    .setCommandId(commandId)
                    .setInstanceId(instance.toString())
                    .setOk(true))
            .build());
    assertFalse(fake.error.isDone());
    TenantContext.run(
        tenant, () -> new WorkspaceAdmin(service, inputs).delete(instance, false, owner));
    UUID deletion =
        store.in(
            tenant,
            () ->
                store.jdbc.queryForObject(
                    "SELECT id FROM workspace_job WHERE tenant_id=? AND instance_id=? AND kind='delete'",
                    UUID.class,
                    tenant,
                    instance));
    for (int i = 0; i < 5; i++) {
      sessions.dispatch();
      do {
        frame = fake.next();
      } while (!frame.hasDelete());
      clock.now = clock.now.plusSeconds(121);
      stream.onNext(SessionRequest.newBuilder().setUsage(NodeUsage.getDefaultInstance()).build());
      sessions.expire();
      assertFalse(fake.error.isDone());
      clock.now = clock.now.plusSeconds(81);
      // Heartbeat before expiration checks: this test isolates command timeouts from liveness.
      stream.onNext(SessionRequest.newBuilder().setUsage(NodeUsage.getDefaultInstance()).build());
    }
    var failed =
        store.in(
            tenant,
            () ->
                store.one(
                    "SELECT * FROM workspace_job WHERE tenant_id=? AND id=?", tenant, deletion));
    assertEquals(true, failed.get("failed"));
    assertEquals(5, ((Number) failed.get("attempts")).intValue());
    assertTrue(logs.getAll().contains("Node deletion exhausted retries:"));
    assertTrue(logs.getAll().contains(deletion.toString()));
    assertEquals(
        "deleting",
        store.in(
            tenant,
            () ->
                store
                    .one("SELECT * FROM instance WHERE tenant_id=? AND id=?", tenant, instance)
                    .get("phase")));
    stream.onCompleted();
  }

  @Test
  void forceRevocationRejectsOrdinaryNonemptyRevokeAndInvalidatesStreamAndCertificate()
      throws Exception {
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    var leaf = NodeCa.parse(joined.getCertificateChain().toByteArray());
    var fake = new FakeNode();
    var context = Context.current().withValue(NodeSessions.PEER, leaf);
    context.call(() -> sessions.session(fake));
    UUID instance =
        TenantContext.call(tenant, () -> (UUID) service.open(owner).instance().get("id"));
    store.in(
        tenant,
        () ->
            store.jdbc.update(
                "UPDATE instance SET node_id=?,ready=true WHERE tenant_id=? AND id=?",
                node,
                tenant,
                instance));
    TenantContext.run(
        tenant,
        () -> {
          var admin = new WorkspaceAdmin(service, inputs);
          assertEquals(
              "workspace.node_not_empty",
              assertThrows(AurigaException.class, () -> admin.revoke(node)).errorCode().code());
          assertFalse(fake.error.isDone());
          admin.revoke(node, true);
        });
    assertEquals(Status.Code.UNAUTHENTICATED, fake.error.get(5, TimeUnit.SECONDS).getCode());
    assertEquals(true, store.in(tenant, () -> store.instance(instance).get("migration_pending")));
    assertEquals(false, store.in(tenant, () -> store.instance(instance).get("ready")));
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> enrollment.identity(leaf)).errorCode().code());
    var rejected = new FakeNode();
    context.call(() -> sessions.session(rejected));
    assertEquals(Status.Code.UNAUTHENTICATED, rejected.error.get(5, TimeUnit.SECONDS).getCode());
    assertEquals(
        1L,
        store.in(
            tenant,
            () ->
                store.jdbc.queryForObject(
                    "SELECT count(*) FROM outbox_event WHERE tenant_id=? AND event_type=? AND subject_key=?",
                    Long.class,
                    tenant,
                    EventEnvelope.AUDIT_TYPE,
                    node.toString())));
  }

  @Test
  void tokenReplayAndExpiryRejectedWithExactCode() throws Exception {
    var token = token();
    var request = joinRequest(token.token(), TestPki.key());
    enrollment.join(request);
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> enrollment.join(request)).errorCode().code());
    var expired = joinRequest(token().token(), TestPki.key());
    clock.now = clock.now.plusSeconds(301);
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> enrollment.join(expired)).errorCode().code());
  }

  @Test
  void quotaAdmissionAndCrossTenantIsolation() {
    TenantContext.run(
        tenant,
        () -> {
          var opened = service.open(owner);
          UUID id = (UUID) opened.instance().get("id");
          assertEquals(7, id.version());
          assertFalse(service.open(owner).created());
          assertEquals(
              "workspace.admission_full",
              assertThrows(AurigaException.class, () -> service.open(UuidV7.next()))
                  .errorCode()
                  .code());
          UUID other = UuidV7.next();
          assertEquals(
              "common.not_found",
              assertThrows(AurigaException.class, () -> store.in(other, () -> store.instance(id)))
                  .errorCode()
                  .code());
          assertEquals(
              0,
              store.in(
                  other,
                  () -> store.jdbc.update("UPDATE instance SET name='forbidden' WHERE id=?", id)));
          var original = opened.instance();
          var rls =
              assertThrows(
                  org.springframework.dao.DataAccessException.class,
                  () ->
                      store.in(
                          other,
                          () ->
                              store.jdbc.update(
                                  "INSERT INTO instance(id,tenant_id,owner_id,name,spec,generation,token_hash,token_encrypted,last_activity_at,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?,?,?,?)",
                                  UuidV7.next(),
                                  tenant,
                                  UuidV7.next(),
                                  "forbidden",
                                  original.get("spec"),
                                  original.get("token_hash"),
                                  original.get("token_encrypted"),
                                  original.get("last_activity_at"),
                                  original.get("created_at"),
                                  original.get("updated_at"))));
          assertEquals("42501", ((java.sql.SQLException) rls.getMostSpecificCause()).getSQLState());
          assertEquals(
              "common.not_found",
              assertThrows(
                      AurigaException.class,
                      () ->
                          store.tx(
                              () ->
                                  service.owned(
                                      id,
                                      new AurigaPrincipal(
                                          other.toString(),
                                          tenant,
                                          PrincipalType.USER,
                                          TokenContext.PORTAL,
                                          null,
                                          null,
                                          "member",
                                          List.of(),
                                          Set.of(),
                                          clock.instant(),
                                          null,
                                          "other"))))
                  .errorCode()
                  .code());
          quota = InstanceQuota.newBuilder().setMaxInstances(0).build();
          assertEquals(
              "workspace.not_allowed",
              assertThrows(AurigaException.class, () -> service.open(UuidV7.next()))
                  .errorCode()
                  .code());
        });
  }

  @Test
  void instanceSecretIsTenantBoundAndRevokedOnDelete() {
    TenantContext.run(
        tenant,
        () -> {
          var opened = service.open(owner);
          var row = opened.instance();
          assertFalse(
              Arrays.equals((byte[]) row.get("token_hash"), (byte[]) row.get("token_encrypted")));
          var admin =
              new WorkspaceAdmin(
                  service,
                  new WorkspaceInputs() {
                    public Member member(UUID t, UUID o) {
                      throw new UnsupportedOperationException();
                    }

                    public InstanceQuota quota(UUID t, UUID o) {
                      return quota;
                    }

                    public WorkspacePolicy policy(UUID t) {
                      return policy;
                    }
                  });
          admin.delete((UUID) row.get("id"), false, owner);
          assertEquals(
              "common.not_found",
              assertThrows(
                      AurigaException.class,
                      () -> store.tx(() -> store.instance((UUID) row.get("id"))))
                  .errorCode()
                  .code());
        });
  }

  @ParameterizedTest
  @ValueSource(ints = {0, 123456499, 123456500, 999999999})
  void persistedLifecycleUsesClockAndDoesNotExecuteRuntimeCommands(int nanos) throws Exception {
    // PostgreSQL stores microseconds and rounds finer timestamps. An exact timeout fixture
    // must use representable instants: Linux Instant.now() can have sub-microsecond precision.
    clock.now =
        clock
            .instant()
            .truncatedTo(java.time.temporal.ChronoUnit.SECONDS)
            .plusNanos(nanos)
            .truncatedTo(java.time.temporal.ChronoUnit.MICROS);
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    TenantContext.run(
        tenant,
        () -> {
          var opened = service.open(owner);
          UUID id = (UUID) opened.instance().get("id");
          store.tx(
              () ->
                  store.jdbc.update(
                      "UPDATE instance SET node_id=?,phase='running',ready=true,last_activity_at=?,plugin_seen_at=?,cpu_seen_at=?,plugin_active=false,cpu_active=false WHERE tenant_id=? AND id=?",
                      node,
                      auriga.workspace.persistence.WorkspaceStore.time(
                          clock.instant().minusSeconds(1800)),
                      auriga.workspace.persistence.WorkspaceStore.time(clock.instant()),
                      auriga.workspace.persistence.WorkspaceStore.time(clock.instant()),
                      tenant,
                      id));
          assertEquals(
              clock.instant().minusSeconds(1800),
              auriga.workspace.persistence.WorkspaceStore.instant(
                  store.tx(() -> store.instance(id)), "last_activity_at"));
          clock.now = clock.now.minusNanos(1000);
          service.tickTenant();
          assertEquals(
              DesiredState.DESIRED_STATE_RUNNING,
              WorkspaceService.spec(store.tx(() -> store.instance(id))).getDesired());
          clock.now = clock.now.plusNanos(1000);
          service.tickTenant();
          var stopped = store.tx(() -> store.instance(id));
          assertEquals(
              DesiredState.DESIRED_STATE_STOPPED, WorkspaceService.spec(stopped).getDesired());
          assertEquals("running", stopped.get("phase"));
          long generation = WorkspaceService.spec(stopped).getGeneration();
          service.wake(
              node,
              WakeSchedule.newBuilder()
                  .setInstanceId(id.toString())
                  .setNextWakeAt(NodeEnrollment.timestamp(clock.instant().plusSeconds(121)))
                  .build());
          service.tickTenant();
          assertEquals(
              generation,
              WorkspaceService.spec(store.tx(() -> store.instance(id))).getGeneration());
          clock.now = clock.now.plusSeconds(1);
          service.tickTenant();
          assertEquals(
              DesiredState.DESIRED_STATE_RUNNING,
              WorkspaceService.spec(store.tx(() -> store.instance(id))).getDesired());
          store.tx(
              () ->
                  store.jdbc.update(
                      "UPDATE instance SET always_on=true,last_activity_at=? WHERE tenant_id=? AND id=?",
                      auriga.workspace.persistence.WorkspaceStore.time(
                          clock.instant().minusSeconds(1800)),
                      tenant,
                      id));
          service.tickTenant();
          assertEquals(
              DesiredState.DESIRED_STATE_RUNNING,
              WorkspaceService.spec(store.tx(() -> store.instance(id))).getDesired());
        });
  }

  private void markRunningIdle(UUID node, UUID id, Instant activity) {
    store.tx(
        () ->
            store.jdbc.update(
                "UPDATE instance SET node_id=?,phase='running',ready=true,last_activity_at=?,plugin_seen_at=?,cpu_seen_at=?,plugin_active=false,cpu_active=false WHERE tenant_id=? AND id=?",
                node,
                WorkspaceStore.time(activity),
                WorkspaceStore.time(clock.instant()),
                WorkspaceStore.time(clock.instant()),
                tenant,
                id));
  }

  private List<Instant> openWakeJobs(UUID id) {
    return store.tx(
        () ->
            store
                .jdbc
                .queryForList(
                    "SELECT due_at FROM workspace_job WHERE tenant_id=? AND instance_id=? AND kind='wake' AND NOT done ORDER BY due_at",
                    tenant,
                    id)
                .stream()
                .map(r -> WorkspaceStore.instant(r, "due_at"))
                .toList());
  }

  @ParameterizedTest
  @ValueSource(
      strings = {"runningTurns", "openTerminals", "activeImBots", "connectedClients", "health"})
  void heartbeatUnknownOrUnhealthyProtectsWithoutInventingActivityAndRecovers(String failure)
      throws Exception {
    clock.now = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.MICROS);
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    TenantContext.run(
        tenant,
        () -> {
          UUID id = (UUID) service.open(owner).instance().get("id");
          Instant historical = clock.instant().minusSeconds(1801);
          markRunningIdle(node, id, historical);
          for (int step = 0; step < 3; step++) {
            clock.now = clock.now.plusSeconds(60);
            store.tx(
                () ->
                    store.jdbc.update(
                        "UPDATE instance SET cpu_seen_at=? WHERE tenant_id=? AND id=?",
                        WorkspaceStore.time(clock.instant()),
                        tenant,
                        id));
            var values = new LinkedHashMap<String, Object>();
            for (String name :
                List.of("runningTurns", "openTerminals", "activeImBots", "connectedClients"))
              values.put(name, name.equals(failure) ? null : 0);
            var request =
                WorkspaceService.JSON.convertValue(
                    Map.of(
                        "pluginVersion",
                        "test",
                        "dshVersion",
                        "test",
                        "activity",
                        values,
                        "health",
                        Map.of("ok", !failure.equals("health"))),
                    HeartbeatRequest.class);
            service.heartbeat(store.tx(() -> store.instance(id)), request);
            var row = store.tx(() -> store.instance(id));
            assertEquals(clock.instant(), WorkspaceStore.instant(row, "plugin_seen_at"));
            assertEquals(historical, WorkspaceStore.instant(row, "last_activity_at"));
            service.tickTenant();
            assertEquals(DesiredState.DESIRED_STATE_RUNNING, desired(id));
          }
          service.heartbeat(
              store.tx(() -> store.instance(id)),
              new HeartbeatRequest(
                  "test",
                  "test",
                  new HeartbeatRequestActivity(0, 0, 0, 0),
                  new HeartbeatRequestHealth(true)));
          assertEquals(
              historical,
              WorkspaceStore.instant(store.tx(() -> store.instance(id)), "last_activity_at"));
          service.tickTenant();
          assertEquals(DesiredState.DESIRED_STATE_STOPPED, desired(id));
        });
  }

  @Test
  void realHeartbeatCountsAndInteractionStillAdvanceActivity() throws Exception {
    clock.now = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.MICROS);
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    TenantContext.run(
        tenant,
        () -> {
          UUID id = (UUID) service.open(owner).instance().get("id");
          markRunningIdle(node, id, clock.instant().minusSeconds(1801));
          service.heartbeat(
              store.tx(() -> store.instance(id)),
              new HeartbeatRequest(
                  "test",
                  "test",
                  new HeartbeatRequestActivity(1, null, 0, 0),
                  new HeartbeatRequestHealth(false)));
          assertEquals(
              clock.instant(),
              WorkspaceStore.instant(store.tx(() -> store.instance(id)), "last_activity_at"));
          clock.now = clock.now.plusSeconds(20);
          Instant interaction = clock.instant().minusSeconds(3);
          service.heartbeat(
              store.tx(() -> store.instance(id)),
              new HeartbeatRequest(
                  "test",
                  "test",
                  new HeartbeatRequestActivity(0, 0, null, 0)
                      .lastInteractionAt(interaction.atOffset(ZoneOffset.UTC)),
                  new HeartbeatRequestHealth(true)));
          assertEquals(
              interaction,
              WorkspaceStore.instant(store.tx(() -> store.instance(id)), "last_activity_at"));
          service.tickTenant();
          assertEquals(DesiredState.DESIRED_STATE_RUNNING, desired(id));
        });
  }

  private DesiredState desired(UUID id) {
    return WorkspaceService.spec(store.tx(() -> store.instance(id))).getDesired();
  }

  @ParameterizedTest
  @ValueSource(booleans = {false, true})
  void rollingWakeReportsCannotOutliveTheAbsoluteIdleCapButStillWakeLater(boolean pendingAtDeadline)
      throws Exception {
    clock.now = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    policy.idleMinutes(5).wakeOnSchedule(true);
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    TenantContext.run(
        tenant,
        () -> {
          UUID id = (UUID) service.open(owner).instance().get("id");
          Instant activity = clock.instant().minusSeconds(300);
          // Idle deadline: activity + 5 min + 4 min = 240 s after the loop starts.
          int[] reportTimes =
              pendingAtDeadline
                  ? new int[] {0, 40, 80, 120, 160, 200, 230, 240}
                  : new int[] {0, 30, 60, 90, 120, 150, 180, 210, 240};
          for (int seconds : reportTimes) {
            clock.now = activity.plusSeconds(300 + seconds);
            markRunningIdle(node, id, activity);
            service.wake(
                node,
                WakeSchedule.newBuilder()
                    .setInstanceId(id.toString())
                    .setNextWakeAt(NodeEnrollment.timestamp(clock.instant().plusSeconds(120)))
                    .build());
            service.tickTenant();
            assertEquals(DesiredState.DESIRED_STATE_RUNNING, desired(id));
          }
          // A trailing report is applied at 260 s; a 100 s alarm cannot stop and pre-warm safely.
          clock.now = activity.plusSeconds(560);
          markRunningIdle(node, id, activity);
          service.tickTenant();
          assertEquals(DesiredState.DESIRED_STATE_RUNNING, desired(id));
          long generation =
              WorkspaceService.spec(store.tx(() -> store.instance(id))).getGeneration();
          // Even continually accepted near-term edits cannot move the activity + idle + 8 min cap.
          for (int seconds : new int[] {290, 320, 350, 380, 410, 440, 480}) {
            clock.now = activity.plusSeconds(300 + seconds);
            markRunningIdle(node, id, activity);
            // Every next report arrives before the previous alarm; only the final edit crosses
            // the cap. There is no expired-alarm gap that could authorize an earlier stop.
            Instant next =
                seconds == 480
                    ? clock.instant().plusSeconds(10)
                    : clock.instant().plusSeconds(Math.min(45, 480 - seconds));
            service.wake(
                node,
                WakeSchedule.newBuilder()
                    .setInstanceId(id.toString())
                    .setNextWakeAt(NodeEnrollment.timestamp(next))
                    .build());
            service.tickTenant();
            var state = store.tx(() -> store.instance(id));
            assertEquals(
                seconds < 480
                    ? DesiredState.DESIRED_STATE_RUNNING
                    : DesiredState.DESIRED_STATE_STOPPED,
                desired(id));
            assertEquals(
                seconds < 480 ? activity : clock.instant(),
                WorkspaceStore.instant(state, "last_activity_at"));
            assertEquals(
                generation + (seconds < 480 ? 0 : 1), WorkspaceService.spec(state).getGeneration());
          }
          Instant stoppedAt = clock.instant();
          assertEquals(false, store.tx(() -> store.instance(id)).get("wake_report_pending"));
          // The stop is not undone in the same loop, but the alarm is not lost either.
          assertEquals(List.of(stoppedAt.plusSeconds(30)), openWakeJobs(id));
          service.tickTenant();
          assertEquals(DesiredState.DESIRED_STATE_STOPPED, desired(id));
          clock.now = stoppedAt.plusSeconds(29);
          service.tickTenant();
          assertEquals(DesiredState.DESIRED_STATE_STOPPED, desired(id));
          clock.now = stoppedAt.plusSeconds(30);
          service.tickTenant();
          assertEquals(DesiredState.DESIRED_STATE_RUNNING, desired(id));
          assertEquals("wake", store.tx(() -> store.instance(id)).get("start_reason"));
        });
  }

  @Test
  void alarmJustAfterTheIdleDeadlineStillFiresOnTheRunningInstance() throws Exception {
    clock.now = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    policy.idleMinutes(5).wakeOnSchedule(true);
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    TenantContext.run(
        tenant,
        () -> {
          UUID id = (UUID) service.open(owner).instance().get("id");
          Instant activity = clock.instant().minusSeconds(301);
          // Review case: alarm A = last_activity + idle + 241 s, first idle loop at +1 s.
          Instant alarm = activity.plusSeconds(300 + 241);
          markRunningIdle(node, id, activity);
          service.wake(
              node,
              WakeSchedule.newBuilder()
                  .setInstanceId(id.toString())
                  .setNextWakeAt(NodeEnrollment.timestamp(alarm))
                  .build());
          for (int offset : new int[] {1, 121, 200, 240}) {
            clock.now = activity.plusSeconds(300 + offset);
            markRunningIdle(node, id, activity);
            service.tickTenant();
            assertEquals(DesiredState.DESIRED_STATE_RUNNING, desired(id), "at +" + offset);
          }
          // Its pre-warm (A - 2 min) was consumed while running; the alarm fires before stop.
          assertEquals(List.of(), openWakeJobs(id));
          clock.now = alarm;
          markRunningIdle(node, id, activity);
          service.tickTenant();
          assertEquals(DesiredState.DESIRED_STATE_STOPPED, desired(id));
          assertEquals(List.of(), openWakeJobs(id));
        });
  }

  @Test
  void alarmAdvancedAfterTheDeadlineFiresWithoutStoppingOrResettingActivity() throws Exception {
    clock.now = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    policy.idleMinutes(5).wakeOnSchedule(true);
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    TenantContext.run(
        tenant,
        () -> {
          UUID id = (UUID) service.open(owner).instance().get("id");
          Instant activity = clock.instant().minusSeconds(500);
          Instant originalAlarm = activity.plusSeconds(700);
          markRunningIdle(node, id, activity);
          service.wake(
              node,
              WakeSchedule.newBuilder()
                  .setInstanceId(id.toString())
                  .setNextWakeAt(NodeEnrollment.timestamp(originalAlarm))
                  .build());
          long generation =
              WorkspaceService.spec(store.tx(() -> store.instance(id))).getGeneration();
          // L+08:20 reports L+11:40; this known alarm keeps running past the L+09:00 deadline.
          for (int seconds : new int[] {500, 540}) {
            clock.now = activity.plusSeconds(seconds);
            markRunningIdle(node, id, activity);
            service.tickTenant();
            assertEquals(DesiredState.DESIRED_STATE_RUNNING, desired(id));
          }
          clock.now = activity.plusSeconds(541);
          Instant alarm = clock.instant().plusSeconds(10);
          service.wake(
              node,
              WakeSchedule.newBuilder()
                  .setInstanceId(id.toString())
                  .setNextWakeAt(NodeEnrollment.timestamp(alarm))
                  .build());
          var edited = store.tx(() -> store.instance(id));
          assertEquals(alarm, WorkspaceStore.instant(edited, "next_wake_at"));
          assertEquals(clock.instant(), WorkspaceStore.instant(edited, "wake_reported_at"));
          assertEquals(false, edited.get("wake_report_pending"));
          for (int seconds : new int[] {541, 550}) {
            clock.now = activity.plusSeconds(seconds);
            markRunningIdle(node, id, activity);
            service.tickTenant();
            var state = store.tx(() -> store.instance(id));
            assertEquals(DesiredState.DESIRED_STATE_RUNNING, desired(id));
            assertEquals(generation, WorkspaceService.spec(state).getGeneration());
            assertEquals(activity, WorkspaceStore.instant(state, "last_activity_at"));
            assertEquals(List.of(), openWakeJobs(id));
          }
          // It remains running through the alarm; afterward idle reclamation resumes normally.
          clock.now = alarm;
          markRunningIdle(node, id, activity);
          service.tickTenant();
          var stopped = store.tx(() -> store.instance(id));
          assertEquals(DesiredState.DESIRED_STATE_STOPPED, desired(id));
          assertEquals(generation + 1, WorkspaceService.spec(stopped).getGeneration());
          // Existing lifecycle changes stamp activity on stop; there was no intervening restart.
          assertEquals(clock.instant(), WorkspaceStore.instant(stopped, "last_activity_at"));
          assertEquals(List.of(), openWakeJobs(id));
        });
  }

  @Test
  void outOfRangeWakeReportsAndOldFinishedJobsAreDiscarded() throws Exception {
    clock.now = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    TenantContext.run(
        tenant,
        () -> {
          UUID id = (UUID) service.open(owner).instance().get("id");
          markRunningIdle(node, id, clock.instant());
          for (Instant next :
              List.of(
                  clock.instant().minusSeconds(121),
                  clock.instant().plus(Duration.ofDays(400)).plusSeconds(1))) {
            service.wake(
                node,
                WakeSchedule.newBuilder()
                    .setInstanceId(id.toString())
                    .setNextWakeAt(NodeEnrollment.timestamp(next))
                    .build());
            var state = store.tx(() -> store.instance(id));
            assertNull(WorkspaceStore.instant(state, "next_wake_at"));
            assertNull(WorkspaceStore.instant(state, "wake_reported_at"));
            assertEquals(List.of(), openWakeJobs(id));
          }
          Instant now = clock.instant();
          UUID old =
              UuidV7.next(
                  Clock.fixed(now.minus(Duration.ofDays(1)).minusMillis(1), ZoneOffset.UTC));
          UUID recent = UuidV7.next(Clock.fixed(now.minusSeconds(3600), ZoneOffset.UTC));
          UUID open = UuidV7.next(Clock.fixed(now.minus(Duration.ofDays(2)), ZoneOffset.UTC));
          for (UUID job : List.of(old, recent, open))
            store.tx(
                () ->
                    store.jdbc.update(
                        "INSERT INTO workspace_job(id,tenant_id,instance_id,kind,due_at,done) VALUES(?,?,?,'wake',?,?)",
                        job,
                        tenant,
                        id,
                        WorkspaceStore.time(now.plus(Duration.ofDays(30))),
                        !job.equals(open)));
          service.tickTenant();
          assertEquals(
              Set.of(open, recent),
              Set.copyOf(
                  store.tx(
                      () ->
                          store.jdbc.queryForList(
                              "SELECT id FROM workspace_job WHERE tenant_id=? AND instance_id=? AND kind='wake'",
                              UUID.class,
                              tenant,
                              id))));
        });
  }

  @Test
  void wakeReportLimitSurvivesAnotherServiceInstanceAndAppliesTrailingCancellation()
      throws Exception {
    clock.now = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    TenantContext.run(
        tenant,
        () -> {
          UUID id = (UUID) service.open(owner).instance().get("id");
          store.tx(
              () ->
                  store.jdbc.update(
                      "UPDATE instance SET node_id=? WHERE tenant_id=? AND id=?",
                      node,
                      tenant,
                      id));
          Instant reportedAt = clock.instant();
          Instant next = reportedAt.plusSeconds(3600);
          service.wake(
              node,
              WakeSchedule.newBuilder()
                  .setInstanceId(id.toString())
                  .setNextWakeAt(NodeEnrollment.timestamp(next))
                  .build());
          var another =
              new WorkspaceService(
                  store,
                  inputs,
                  service.templates,
                  null,
                  org.mockito.Mockito.mock(EventPublisher.class),
                  clock);
          clock.now = reportedAt.plusSeconds(29);
          another.wake(node, WakeSchedule.newBuilder().setInstanceId(id.toString()).build());
          var state = store.tx(() -> store.instance(id));
          assertEquals(
              next, auriga.workspace.persistence.WorkspaceStore.instant(state, "next_wake_at"));
          assertEquals(
              reportedAt,
              auriga.workspace.persistence.WorkspaceStore.instant(state, "wake_reported_at"));
          assertEquals(true, state.get("wake_report_pending"));
          clock.now = reportedAt.plusSeconds(30);
          service.tickTenant();
          assertNull(
              auriga.workspace.persistence.WorkspaceStore.instant(
                  store.tx(() -> store.instance(id)), "next_wake_at"));
          assertEquals(
              0,
              store.tx(
                  () ->
                      store.jdbc.queryForObject(
                          "SELECT count(*) FROM workspace_job WHERE tenant_id=? AND instance_id=? AND kind='wake' AND NOT done",
                          Integer.class,
                          tenant,
                          id)));
        });
  }

  @Test
  void duplicateAndForeignWakeReportsCannotCreateOrReplaceJobs() throws Exception {
    clock.now = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    var joined = enrollment.join(joinRequest(token().token(), TestPki.key()));
    UUID node = UUID.fromString(joined.getNodeId());
    TenantContext.run(
        tenant,
        () -> {
          UUID id = (UUID) service.open(owner).instance().get("id");
          store.tx(
              () ->
                  store.jdbc.update(
                      "UPDATE instance SET node_id=? WHERE tenant_id=? AND id=?",
                      node,
                      tenant,
                      id));
          Instant next = clock.instant().plusSeconds(120);
          WakeSchedule report =
              WakeSchedule.newBuilder()
                  .setInstanceId(id.toString())
                  .setNextWakeAt(NodeEnrollment.timestamp(next))
                  .build();
          service.wake(node, report);
          store.tx(
              () ->
                  store.jdbc.update(
                      "UPDATE workspace_job SET done=true WHERE tenant_id=? AND instance_id=? AND kind='wake'",
                      tenant,
                      id));
          clock.now = clock.now.plusSeconds(30);
          service.wake(node, report);
          service.wake(
              UUID.randomUUID(), WakeSchedule.newBuilder().setInstanceId(id.toString()).build());
          var state = store.tx(() -> store.instance(id));
          assertEquals(
              next, auriga.workspace.persistence.WorkspaceStore.instant(state, "next_wake_at"));
          assertEquals(false, state.get("wake_report_pending"));
          assertEquals(
              1,
              store.tx(
                  () ->
                      store.jdbc.queryForObject(
                          "SELECT count(*) FROM workspace_job WHERE tenant_id=? AND instance_id=? AND kind='wake'",
                          Integer.class,
                          tenant,
                          id)));
          assertEquals(
              0,
              store.tx(
                  () ->
                      store.jdbc.queryForObject(
                          "SELECT count(*) FROM workspace_job WHERE tenant_id=? AND instance_id=? AND kind='wake' AND NOT done",
                          Integer.class,
                          tenant,
                          id)));
        });
  }

  @Test
  void idempotentJoinTokenDoesNotMintAdditionalCredentials() {
    TenantContext.run(
        tenant,
        () -> {
          var first = service.joinToken(enrollment, "test", 5, owner, "same-key");
          var second = service.joinToken(enrollment, "test", 5, owner, "same-key");
          assertEquals(first.token(), second.token());
          assertEquals(
              "common.conflict",
              assertThrows(
                      AurigaException.class,
                      () -> service.joinToken(enrollment, "different", 5, owner, "same-key"))
                  .errorCode()
                  .code());
        });
  }

  @Test
  void fakeNodeJoinsReceivesSpecReportsStatusAndSecondSessionReplacesFirst() throws Exception {
    var serverKey = TestPki.key();
    var serverCert =
        TestPki.certificate(serverKey, caKey, ca.certificate(), clock.instant(), false);
    Path certificate = directory.resolve("server.pem"),
        privateKey = directory.resolve("server-key.pem");
    Files.write(certificate, ca.chain(serverCert));
    Files.writeString(privateKey, pemKey(serverKey));
    try (var server =
        new NodeGrpcServer(
            "127.0.0.1", 0, certificate.toFile(), privateKey.toFile(), enrollment, sessions)) {
      server.start();
      ManagedChannel joinChannel = channel(server.port(), null, null);
      try {
        var clientKey = TestPki.key();
        var request = joinRequest(token().token(), clientKey);
        var joined =
            NodeEnrollmentServiceGrpc.newBlockingStub(joinChannel)
                .withDeadlineAfter(5, TimeUnit.SECONDS)
                .join(request);
        assertEquals(
            Status.Code.UNAUTHENTICATED,
            assertThrows(
                    StatusRuntimeException.class,
                    () ->
                        NodeEnrollmentServiceGrpc.newBlockingStub(joinChannel)
                            .withDeadlineAfter(5, TimeUnit.SECONDS)
                            .join(request))
                .getStatus()
                .getCode());
        ManagedChannel nodeChannel =
            channel(server.port(), joined.getCertificateChain().toByteArray(), pemKey(clientKey));
        try {
          FakeNode first = new FakeNode();
          var stream = NodeControlServiceGrpc.newStub(nodeChannel).session(first);
          UUID node = UUID.fromString(joined.getNodeId());
          String keyId =
              HexFormat.of()
                  .formatHex(WorkspaceCrypto.hash(request.getSealingPublicKey().toByteArray()))
                  .substring(0, 16);
          Hello hello =
              Hello.newBuilder()
                  .setNodeId(joined.getNodeId())
                  .setProtocolVersion(1)
                  .setOs("linux")
                  .setArch("amd64")
                  .setSealingKeyId(keyId)
                  .addDrivers(RuntimeKind.RUNTIME_KIND_DOCKER)
                  .setCapacity(
                      Capacity.newBuilder()
                          .setCpuMillis(4000)
                          .setMemoryMb(4096)
                          .setDiskGb(20)
                          .setMaxInstances(2))
                  .build();
          stream.onNext(SessionRequest.newBuilder().setHello(hello).build());
          assertTrue(first.next().hasWelcome());
          stream.onNext(
              SessionRequest.newBuilder().setInventory(Inventory.getDefaultInstance()).build());
          var opened = TenantContext.call(tenant, () -> service.open(owner));
          var apply = first.next();
          assertTrue(apply.hasApply());
          assertEquals(node, opened.instance().get("node_id"));
          var spec = apply.getApply().getSpec();
          stream.onNext(
              SessionRequest.newBuilder()
                  .setStatus(
                      InstanceStatus.newBuilder()
                          .setInstanceId(spec.getInstanceId())
                          .setObservedGeneration(spec.getGeneration())
                          .setPhase(InstancePhase.INSTANCE_PHASE_RUNNING)
                          .setReady(true))
                  .build());
          org.awaitility.Awaitility.await()
              .atMost(java.time.Duration.ofSeconds(5))
              .untilAsserted(
                  () ->
                      assertEquals(
                          true,
                          store.in(
                              tenant,
                              () ->
                                  store
                                      .instance(UUID.fromString(spec.getInstanceId()))
                                      .get("ready"))));
          // Subsequent request/response on the same serialized stream is the synchronization
          // barrier.
          FakeNode second = new FakeNode();
          var replacement = NodeControlServiceGrpc.newStub(nodeChannel).session(second);
          replacement.onNext(SessionRequest.newBuilder().setHello(hello).build());
          assertTrue(second.next().hasWelcome());
          assertEquals(Status.Code.ABORTED, first.error.get(5, TimeUnit.SECONDS).getCode());
          replacement.onNext(
              SessionRequest.newBuilder().setInventory(Inventory.getDefaultInstance()).build());
          assertTrue(second.next().hasApply());
          replacement.onCompleted();
          var renewed =
              NodeEnrollmentServiceGrpc.newBlockingStub(nodeChannel)
                  .withDeadlineAfter(5, TimeUnit.SECONDS)
                  .renewCertificate(
                      RenewCertificateRequest.newBuilder()
                          .setCsr(ByteString.copyFrom(TestPki.csr(clientKey)))
                          .build());
          assertFalse(renewed.getCertificateChain().isEmpty());
          assertEquals(
              joined.getNodeId(),
              enrollment
                  .identity(NodeCa.parse(renewed.getCertificateChain().toByteArray()))
                  .toString());
        } finally {
          nodeChannel.shutdownNow();
          assertTrue(nodeChannel.awaitTermination(5, TimeUnit.SECONDS));
        }
      } finally {
        joinChannel.shutdownNow();
        assertTrue(joinChannel.awaitTermination(5, TimeUnit.SECONDS));
      }
    }
  }

  @Test
  void revokedNodeCannotRenew() throws Exception {
    var key = TestPki.key();
    var joined = enrollment.join(joinRequest(token().token(), key));
    UUID node = UUID.fromString(joined.getNodeId());
    var leaf = NodeCa.parse(joined.getCertificateChain().toByteArray());
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () ->
                    store.jdbc.update(
                        "UPDATE node SET revoked=true WHERE tenant_id=? AND id=?", tenant, node)));
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> enrollment.identity(leaf)).errorCode().code());
  }

  ManagedChannel channel(int port, byte[] certificate, String key) throws Exception {
    var ssl = GrpcSslContexts.forClient().trustManager(new ByteArrayInputStream(ca.bundle()));
    if (certificate != null)
      ssl.keyManager(
          new ByteArrayInputStream(certificate),
          new ByteArrayInputStream(key.getBytes(java.nio.charset.StandardCharsets.US_ASCII)));
    return NettyChannelBuilder.forAddress("127.0.0.1", port).sslContext(ssl.build()).build();
  }

  static String pemKey(KeyPair key) {
    return "-----BEGIN PRIVATE KEY-----\n" // gitleaks:allow
        + Base64.getMimeEncoder(64, new byte[] {10}).encodeToString(key.getPrivate().getEncoded())
        + "\n-----END PRIVATE KEY-----\n";
  }

  static final class FakeNode implements StreamObserver<SessionResponse> {
    final BlockingQueue<SessionResponse> responses = new LinkedBlockingQueue<>();
    final CompletableFuture<Status> error = new CompletableFuture<>();

    public void onNext(SessionResponse response) {
      responses.add(response);
    }

    public void onError(Throwable error) {
      this.error.complete(Status.fromThrowable(error));
    }

    public void onCompleted() {}

    SessionResponse next() throws Exception {
      var response = responses.poll(5, TimeUnit.SECONDS);
      assertNotNull(response);
      return response;
    }
  }

  static final class MutableClock extends Clock {
    Instant now = Instant.now();

    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(ZoneId zone) {
      return this;
    }

    public Instant instant() {
      return now;
    }
  }
}
