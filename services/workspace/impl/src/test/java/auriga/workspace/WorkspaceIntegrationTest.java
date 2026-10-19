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
import org.springframework.jdbc.core.JdbcTemplate;

/** Real PostgreSQL + TLS gRPC fake node using only the generated protobuf protocol. */
@ExtendWith(PostgresExtension.class)
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
                    .setDigest("sha256:" + "0".repeat(64))
                    .setImage("example.com/dsh@sha256:" + "0".repeat(64))
                    .setSigned(
                        SignedArtifact.newBuilder()
                            .setSignature(ByteString.copyFrom(new byte[] {1}))
                            .setStatement(
                                ArtifactStatement.newBuilder()
                                    .setOs("linux")
                                    .setArch("amd64")
                                    .setVersion("1.0.0"))))
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
  void platformRegistryWorksInsideTenantTransactionAndSeparatesResourceKinds() {
    UUID id = UuidV7.next();
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  store.register(id, "instance", tenant);
                  store.register(id, "instance", tenant);
                  assertEquals(
                      0L,
                      store.jdbc.queryForObject(
                          "SELECT count(*) FROM workspace_registry WHERE id=?", Long.class, id));
                  assertEquals(tenant, store.route(id, "instance"));
                  assertEquals(
                      "common.unauthenticated",
                      assertThrows(AurigaException.class, () -> store.route(id, "token"))
                          .errorCode()
                          .code());
                  return null;
                }));
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

  @Test
  void persistedLifecycleUsesClockAndDoesNotExecuteRuntimeCommands() throws Exception {
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
