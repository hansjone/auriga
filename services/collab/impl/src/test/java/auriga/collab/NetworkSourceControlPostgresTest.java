package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.*;
import auriga.collab.grpc.*;
import auriga.collab.network.local.LocalRegistry;
import auriga.common.context.*;
import auriga.common.grpc.*;
import auriga.common.persistence.*;
import auriga.common.security.AccessTokenVerifier;
import auriga.contracts.collab.v1.*;
import auriga.contracts.iam.v1.*;
import auriga.testsupport.PostgresExtension;
import com.google.protobuf.ByteString;
import com.zaxxer.hikari.*;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.security.KeyPairGenerator;
import java.security.Signature;
import java.time.*;
import java.time.temporal.ChronoUnit;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.Consumer;
import java.util.function.Supplier;
import java.util.function.UnaryOperator;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.namedparam.NamedParameterJdbcTemplate;
import org.springframework.transaction.support.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Real PG, common authenticated mailbox transport and production IAM clients; IAM is a fixture. */
@ExtendWith(PostgresExtension.class)
class NetworkSourceControlPostgresTest {
  static final UUID TENANT = UUID.randomUUID();
  static final AtomicInteger NODES = new AtomicInteger(5000);
  static final String FP = "sha256:" + "a".repeat(64);
  static HikariDataSource pool;
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static NetworkMailboxWireService wire;
  static NetworkSourceGrpcAuthority source;
  static NetworkAuthorityClient client;
  static LocalRegistry locals;
  static NetworkSigningKeys keys;
  static ManagedChannel channel, mailboxChannel;
  static Server server;
  static GrpcServer mailboxServer;
  static final SourcePeer peer = new SourcePeer();
  static final Authority authority = new Authority();
  static final Clock CLOCK = Clock.systemUTC();
  static final Context.Key<String> IAM_TENANT = Context.key("source-fixture-tenant");
  static final Context.Key<String> IAM_CALLER = Context.key("source-fixture-caller");
  static final NetworkAuthorityConnection LEGACY_CONNECTION =
      NetworkConnectionAnchorGrpcTest.connection(TENANT, newIngress()).toBuilder()
          .setNode("4999")
          .build();
  static ObjectNode legacyRequest;

  static class Authority extends NetworkAuthorityGrpcTest.Authority {
    final Set<String> invalid = ConcurrentHashMap.newKeySet();
    final Set<NetworkAuthorityConnection> wrongOwner = ConcurrentHashMap.newKeySet();
    volatile Runnable beforeResolveReply = () -> {};
    final Map<NetworkAuthorityConnection, NetworkConnectionAnchor> anchors =
        new ConcurrentHashMap<>();
    final AtomicInteger anchorReads = new AtomicInteger();
    volatile boolean metadata = true, sameHistoryOwner = true;
    volatile Status anchorFailure;
    volatile ReadNetworkConnectionAnchorRequest lastAnchorRead;
    volatile Runnable beforeAnchorRead = () -> {}, afterAnchorRead = () -> {};
    volatile UnaryOperator<NetworkConnectionAnchor> anchorView = UnaryOperator.identity();
    volatile UnaryOperator<ReadNetworkConnectionAnchorResponse> anchorReply =
        UnaryOperator.identity();

    NetworkConnectionAnchor ref(NetworkAuthorityConnection c) {
      return anchors.computeIfAbsent(
          c,
          value ->
              NetworkConnectionAnchor.newBuilder()
                  .setAnchorId(UuidV7.next().toString())
                  .setConnection(value)
                  .build());
    }

    @Override
    public void resolveNetworkTarget(
        ResolveNetworkTargetRequest r, StreamObserver<ResolveNetworkTargetResponse> o) {
      if (invalid.contains(r.getConnection().getIngress())
          || wrongOwner.contains(r.getConnection())) {
        o.onNext(ResolveNetworkTargetResponse.newBuilder().setAllowed(false).build());
        o.onCompleted();
      } else {
        beforeResolveReply.run();
        var b =
            ResolveNetworkTargetResponse.newBuilder()
                .setAllowed(allowed)
                .setConnection(echo(r.getConnection()))
                .setLocal(r.getLocal())
                .setTargetType("system")
                .setValidUntil(expiry());
        if (metadata && r.getConnection().getIngress().matches("[a-z2-7]{26}"))
          b.setConnectionAnchor(anchorView.apply(ref(r.getConnection())));
        o.onNext(b.build());
        o.onCompleted();
      }
    }

    @Override
    public void readNetworkConnectionAnchor(
        ReadNetworkConnectionAnchorRequest r,
        StreamObserver<ReadNetworkConnectionAnchorResponse> o) {
      anchorReads.incrementAndGet();
      lastAnchorRead = r;
      beforeAnchorRead.run();
      if (anchorFailure != null) {
        o.onError(anchorFailure.asRuntimeException());
        return;
      }
      if (!"Bearer collab-test".equals(IAM_CALLER.get())
          || !r.getOriginal().getConnection().getTenantId().equals(IAM_TENANT.get())
          || !r.getCurrent().getConnection().getTenantId().equals(IAM_TENANT.get())
          || !Objects.equals(anchors.get(r.getOriginal().getConnection()), r.getOriginal())
          || !Objects.equals(anchors.get(r.getCurrent().getConnection()), r.getCurrent())
          || !r.getOriginal()
              .getConnection()
              .getRealm()
              .equals(r.getCurrent().getConnection().getRealm())
          || !r.getOriginal()
              .getConnection()
              .getNode()
              .equals(r.getCurrent().getConnection().getNode())
          || !sameHistoryOwner) {
        o.onError(Status.PERMISSION_DENIED.asRuntimeException());
        return;
      }
      var result =
          ReadNetworkConnectionAnchorResponse.newBuilder()
              .setOriginal(r.getOriginal())
              .setCurrent(r.getCurrent())
              .build();
      afterAnchorRead.run();
      o.onNext(anchorReply.apply(result));
      o.onCompleted();
    }
  }

  static class SourcePeer
      extends NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceImplBase {
    final Map<String, RegisterAgentSourceRequest> requests = new ConcurrentHashMap<>();
    final Map<String, RegisterAgentSourceResponse> receipts = new ConcurrentHashMap<>();
    final Map<String, NetworkAgentSource> sources = new ConcurrentHashMap<>();
    final Map<String, String> heads = new ConcurrentHashMap<>();
    final Set<String> stopped = ConcurrentHashMap.newKeySet();
    final AtomicInteger registerCalls = new AtomicInteger();
    final AtomicInteger getCalls = new AtomicInteger();
    final AtomicInteger readCalls = new AtomicInteger();
    volatile ReadAgentSourceReceiptRequest lastRead;
    volatile Status readFailure;
    volatile boolean dropRegisterBeforeCommit, receiptOwnerValid = true;
    volatile Runnable afterRead = () -> {};
    volatile UnaryOperator<RegisterAgentSourceResponse> readReply = UnaryOperator.identity();
    volatile Runnable afterRegister = () -> {}, afterGet = () -> {}, afterRevoke = () -> {};
    volatile boolean loseRegister, loseRevoke, missingPresence, wrongEcho, reject;
    volatile RevokeAgentSourceRequest lastRevoke;
    volatile GetAgentSourceRequest lastGet;

    @Override
    public void registerAgentSource(
        RegisterAgentSourceRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
      registerCalls.incrementAndGet();
      if (dropRegisterBeforeCommit) {
        o.onError(Status.UNAVAILABLE.asRuntimeException());
        return;
      }
      RegisterAgentSourceResponse result;
      synchronized (this) {
        var old = requests.putIfAbsent(key(r), r);
        if (old != null && !old.equals(r)) {
          o.onError(Status.ABORTED.asRuntimeException());
          return;
        }
        result = receipts.get(key(r));
        if (result == null) {
          String registrationId = r.getRegistration().getRegistrationId();
          String head = heads.get(registrationId);
          var predecessor = sources.get(r.getPredecessorBinding());
          String reason =
              reject
                  ? "revision_mismatch"
                  : authority.invalid.contains(r.getConnection().getIngress())
                      ? "original_connection_invalid_uncommitted"
                      : stopped.contains(registrationId)
                          ? "lineage_stopped"
                          : head != null && !Objects.equals(head, r.getPredecessorBinding())
                              ? "predecessor_not_head"
                              : predecessor != null
                                      && (predecessor.getRevision() != r.getExpectedRevision()
                                          || predecessor.getState()
                                              != NetworkAgentSourceState
                                                  .NETWORK_AGENT_SOURCE_STATE_EXPIRED)
                                  ? "revision_mismatch"
                                  : null;
          if (reason != null) {
            String digest =
                db(
                    () ->
                        jdbc.queryForObject(
                            "SELECT request_digest FROM collab.network_source_operation WHERE tenant_id=? AND operation_id=?",
                            String.class,
                            TENANT,
                            r.getOperationId()));
            result =
                RegisterAgentSourceResponse.newBuilder()
                    .setDefinitivelyUncommitted(true)
                    .setOperationId(r.getOperationId())
                    .setRequestDigest(digest)
                    .setRejectionReason(reason)
                    .build();
          } else {
            var fact =
                NetworkAgentSource.newBuilder()
                    .setSourceBinding("binding-" + UuidV7.next())
                    .setOriginalConnection(r.getConnection())
                    .setRegistration(r.getRegistration())
                    .setScope(r.getScope())
                    .setRevision(1)
                    .setState(NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_ACTIVE)
                    .setOwnerUserId("01900000-0000-7000-8000-000000000001")
                    .setNotBefore(now().minusSeconds(1).toString())
                    .setExpiresAt(now().plusSeconds(180).toString())
                    .setPredecessorBinding(r.getPredecessorBinding())
                    .build();
            sources.put(fact.getSourceBinding(), fact);
            heads.put(registrationId, fact.getSourceBinding());
            result = RegisterAgentSourceResponse.newBuilder().setSource(fact).build();
          }
          receipts.put(key(r), result);
        }
      }
      afterRegister.run();
      if (loseRegister) o.onError(Status.UNAVAILABLE.asRuntimeException());
      else {
        o.onNext(result);
        o.onCompleted();
      }
    }

    static String key(RegisterAgentSourceRequest r) {
      return r.getConnection().getTenantId() + "/" + r.getOperationId();
    }

    @Override
    public void readAgentSourceReceipt(
        ReadAgentSourceReceiptRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
      readCalls.incrementAndGet();
      lastRead = r;
      if (!r.hasOriginalRequest()
          || !"Bearer collab-test".equals(IAM_CALLER.get())
          || !r.getOriginalRequest().getConnection().getTenantId().equals(IAM_TENANT.get())
          || TenantContext.PLATFORM_TENANT.toString().equals(IAM_TENANT.get())
          || !receiptOwnerValid) {
        o.onError(Status.PERMISSION_DENIED.asRuntimeException());
        return;
      }
      if (readFailure != null) {
        o.onError(readFailure.asRuntimeException());
        return;
      }
      var request = r.getOriginalRequest();
      RegisterAgentSourceResponse receipt;
      synchronized (this) {
        var original = requests.get(key(request));
        receipt = receipts.get(key(request));
        if (original == null || receipt == null) {
          o.onError(Status.UNAVAILABLE.asRuntimeException());
          return;
        }
        if (!original.equals(request)) {
          o.onError(Status.ABORTED.asRuntimeException());
          return;
        }
      }
      afterRead.run();
      o.onNext(readReply.apply(receipt));
      o.onCompleted();
    }

    @Override
    public void getAgentSource(GetAgentSourceRequest r, StreamObserver<GetAgentSourceResponse> o) {
      getCalls.incrementAndGet();
      lastGet = r;
      var fact = sources.get(r.getSourceBinding());
      if (fact == null
          || !fact.getRegistration().equals(r.getRegistration())
          || !fact.getOriginalConnection().getNode().equals(r.getConnection().getNode())
          || !fact.getOriginalConnection().getTenantId().equals(r.getConnection().getTenantId())
          || !fact.getOriginalConnection().getRealm().equals(r.getConnection().getRealm())
          || r.getConnection().getClientAccount().equals("other-owner")) {
        o.onError(Status.PERMISSION_DENIED.asRuntimeException());
        return;
      }
      var result =
          GetAgentSourceResponse.newBuilder()
              .setSource(fact)
              .setConnection(
                  wrongEcho
                      ? r.getConnection().toBuilder().setClientId("wrong").build()
                      : r.getConnection())
              .setPolicyRevision(7)
              .setObservedAt(now().toString());
      if (!missingPresence)
        result.setLineageStopped(stopped.contains(fact.getRegistration().getRegistrationId()));
      if (fact.getState() == NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_ACTIVE)
        result.setValidUntil(now().plusSeconds(60).toString());
      afterGet.run(); // Snapshot deliberately may arrive after a committed Revoke.
      o.onNext(result.build());
      o.onCompleted();
    }

    @Override
    public void revokeAgentSource(
        RevokeAgentSourceRequest r, StreamObserver<RevokeAgentSourceResponse> o) {
      lastRevoke = r;
      assertEquals(0, r.getExpectedRevision());
      var original = sources.get(r.getSourceBinding());
      if (original == null) {
        o.onError(Status.PERMISSION_DENIED.asRuntimeException());
        return;
      }
      stopped.add(original.getRegistration().getRegistrationId());
      sources.replaceAll(
          (id, fact) ->
              fact.getRegistration().equals(original.getRegistration())
                      && fact.getState()
                          == NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_ACTIVE
                  ? fact.toBuilder()
                      .setState(NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_REVOKED)
                      .setRevision(2)
                      .build()
                  : fact);
      var result =
          RevokeAgentSourceResponse.newBuilder()
              .setSource(sources.get(r.getSourceBinding()))
              .build();
      afterRevoke.run();
      if (loseRevoke) o.onError(Status.UNAVAILABLE.asRuntimeException());
      else {
        o.onNext(result);
        o.onCompleted();
      }
    }
  }

  static class Client extends NetworkAuthorityClient {
    Client(ManagedChannel c) {
      super(c, Duration.ofSeconds(5));
    }

    void outside() {
      assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
      assertFalse(TransactionSynchronizationManager.isSynchronizationActive());
      assertFalse(TransactionSynchronizationManager.hasResource(pool));
    }

    @Override
    public RegisterAgentSourceResponse registerSource(RegisterAgentSourceRequest r) {
      outside();
      return super.registerSource(r);
    }

    @Override
    public RegisterAgentSourceResponse readSourceReceipt(RegisterAgentSourceRequest r) {
      outside();
      return super.readSourceReceipt(r);
    }

    @Override
    public GetAgentSourceResponse getSource(GetAgentSourceRequest r) {
      outside();
      return super.getSource(r);
    }

    @Override
    public RevokeAgentSourceResponse revokeSource(RevokeAgentSourceRequest r) {
      outside();
      return super.revokeSource(r);
    }

    @Override
    public ResolveNetworkTargetResponse resolve(NetworkAuthorityConnection c, String local) {
      outside();
      return super.resolve(c, local);
    }

    @Override
    public ReadNetworkConnectionAnchorResponse readConnectionAnchor(
        NetworkConnectionAnchor original, NetworkConnectionAnchor current) {
      outside();
      return super.readConnectionAnchor(original, current);
    }
  }

  @BeforeAll
  static void setup() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkSourceControlPostgresTest.class, "source_owner", "source_app", "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .target("14")
                .load())
        .migrate();
    var config = new HikariConfig();
    config.setDataSource(roles.runtime().dataSource());
    config.setMaximumPoolSize(3);
    config.setMinimumIdle(0);
    pool = new HikariDataSource(config);
    jdbc = new JdbcTemplate(pool);
    manager = new TenantTransactionManager(pool);
    seedPreV15Tombstone();
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .intercept(
                new ServerInterceptor() {
                  @Override
                  public <Q, R> ServerCall.Listener<Q> interceptCall(
                      ServerCall<Q, R> call, Metadata headers, ServerCallHandler<Q, R> next) {
                    return Contexts.interceptCall(
                        Context.current()
                            .withValue(IAM_TENANT, headers.get(GrpcMetadata.TENANT_ID))
                            .withValue(IAM_CALLER, headers.get(GrpcMetadata.AUTHORIZATION)),
                        call,
                        headers,
                        next);
                  }
                })
            .addService(authority)
            .addService(peer)
            .build()
            .start();
    channel =
        GrpcChannels.open(
            "127.0.0.1:" + server.getPort(),
            new AurigaClientInterceptor(() -> "collab-test", Duration.ofSeconds(5)));
    client = new Client(channel);
    source = new NetworkSourceGrpcAuthority(client, manager);
    keys = new NetworkSigningKeys(CLOCK, 1);
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    keys.add(
        "2",
        "protocol_response",
        "source-test",
        pair.getPublic(),
        pair.getPrivate(),
        now().minusSeconds(30),
        now().plusSeconds(3600),
        NetworkSigningKeys.State.ACTIVE);
    locals = new LocalRegistry(jdbc, manager, source, client, CLOCK);
    wire = newWire();
    var verifier = mock(AccessTokenVerifier.class);
    when(verifier.verify(anyString()))
        .thenReturn(NetworkMailboxGrpcTest.principal("svc:ai-gateway"));
    mailboxServer =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(new NetworkMailboxGrpcService(wire)),
            new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:ai-gateway")),
            null,
            false,
            Set.of());
    mailboxServer.start();
    mailboxChannel =
        GrpcChannels.open(
            "127.0.0.1:" + mailboxServer.port(),
            new AurigaClientInterceptor(() -> "gateway-test", Duration.ofSeconds(10)));
  }

  static NetworkMailboxWireService newWire() {
    return new NetworkMailboxWireService(
        new NetworkMailboxService(jdbc, manager, source, keys, CLOCK, client),
        locals,
        jdbc,
        manager,
        source,
        keys,
        CLOCK);
  }

  @AfterEach
  void reset() {
    authority.beforeResolveReply = () -> {};
    authority.metadata = true;
    authority.sameHistoryOwner = true;
    authority.anchorFailure = null;
    authority.beforeAnchorRead = () -> {};
    authority.afterAnchorRead = () -> {};
    authority.anchorView = UnaryOperator.identity();
    authority.anchorReply = UnaryOperator.identity();
    peer.afterRegister = () -> {};
    peer.afterGet = () -> {};
    peer.afterRevoke = () -> {};
    peer.loseRegister = false;
    peer.loseRevoke = false;
    peer.missingPresence = false;
    peer.wrongEcho = false;
    peer.reject = false;
    peer.readFailure = null;
    peer.receiptOwnerValid = true;
    peer.dropRegisterBeforeCommit = false;
    peer.afterRead = () -> {};
    peer.readReply = UnaryOperator.identity();
  }

  @AfterAll
  static void close() throws Exception {
    if (mailboxChannel != null) mailboxChannel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    if (channel != null) channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    if (mailboxServer != null) mailboxServer.stop();
    if (server != null) server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    if (keys != null) keys.close();
    if (pool != null) pool.close();
  }

  record Fixture(NetworkAuthorityConnection c, ObjectNode ready, ObjectNode registration) {}

  static Fixture fresh() {
    String node = Integer.toString(NODES.incrementAndGet());
    var c =
        NetworkAuthorityGrpcTest.connection(TENANT).toBuilder()
            .setNode(node)
            .setIngress(newIngress())
            .setServerId("N" + "A".repeat(55))
            .build();
    return fresh(c);
  }

  static Fixture fresh(NetworkAuthorityConnection c) {
    String node = c.getNode();
    var ready =
        (ObjectNode)
            NetworkJson.parse(
                NetworkMailboxWirePostgresTest.ready(
                    "boot-" + node, "nonce-" + node, "ready-" + node));
    call(() -> wire.ready(c, bytes(ready)));
    var local =
        ((ObjectNode) NetworkJson.parse("{}"))
            .put("app_version", "1.0")
            .put("type", "local_register")
            .put("request_id", "local-" + node)
            .put("epoch", "1")
            .put("boot_id", "boot-" + node)
            .put("candidate_local", "1")
            .put("address_type", "session")
            .put("allocation_op_id", "allocation-" + node)
            .put("allocation_fingerprint", FP)
            .put("expected_registration_revision", "0");
    var registered = NetworkJson.parse(call(() -> wire.localRegister(c, bytes(local))));
    var registration = (ObjectNode) NetworkJson.parse("{}");
    for (String field :
        List.of(
            "registration_id",
            "allocation_op_id",
            "registration_revision",
            "local",
            "address_type")) registration.set(field, registered.path(field));
    registration.put("allocation_fingerprint", FP);
    return new Fixture(c, ready, registration);
  }

  static void seedPreV15Tombstone() {
    var ready =
        (ObjectNode)
            NetworkJson.parse(
                NetworkMailboxWirePostgresTest.ready("boot-4999", "nonce-4999", "ready-4999"));
    var registration = (ObjectNode) NetworkJson.parse("{}");
    registration
        .put("registration_id", UuidV7.next().toString())
        .put("allocation_op_id", "legacy-allocation")
        .put("allocation_fingerprint", FP)
        .put("registration_revision", "1")
        .put("local", "1")
        .put("address_type", "session");
    legacyRequest =
        frame(new Fixture(LEGACY_CONNECTION, ready, registration), "source_register")
            .put("operation_id", "pre-v15-operation")
            .put("mode", "reconcile")
            .putNull("native_witness");
    var business = (ObjectNode) NetworkJson.parse("{}");
    for (String field :
        List.of("registration", "scope", "expected_revision", "predecessor_binding"))
      business.set(field, legacyRequest.path(field));
    String digest = NetworkJson.digest(business);
    var result = (ObjectNode) NetworkJson.parse("{}");
    result
        .put("app_version", "1.0")
        .put("type", "source_register_result")
        .put("operation_id", "pre-v15-operation")
        .put("result", "not_accepted")
        .putNull("source");
    result
        .putObject("failure_evidence")
        .put("authority", "collab")
        .put("reason", "rejected_before_pending")
        .put("request_digest", digest);
    db(
        () ->
            jdbc.update(
                """
        INSERT INTO collab.network_source_operation(id,tenant_id,method,operation_id,realm,node_id,
        business_json,request_digest,original_frame,original_connection,epoch,boot_id,status,result_json)
        VALUES (?,?,'Register','pre-v15-operation','2','4999',?,?,?,?,1,'boot-4999','not_accepted',?)
        """,
                UuidV7.next(),
                TENANT,
                NetworkJson.canonical(business),
                digest,
                bytes(legacyRequest),
                LEGACY_CONNECTION.toByteArray(),
                NetworkJson.canonical(result)));
  }

  static ObjectNode frame(Fixture f, String type) {
    var frame =
        ((ObjectNode) NetworkJson.parse("{}"))
            .put("app_version", "1.0")
            .put("type", type)
            .put("request_id", "request-" + UUID.randomUUID())
            .put("epoch", "1")
            .put("boot_id", f.ready().path("boot_id").asString());
    if (!"source_revoke".equals(type)) {
      frame.set("registration", f.registration().deepCopy());
      frame.set("native_witness", witness(f));
    }
    if ("source_register".equals(type))
      frame
          .put("operation_id", "op-" + UUID.randomUUID())
          .put("mode", "issue")
          .put("scope", "durable_unicast_send")
          .put("expected_revision", "0")
          .put("predecessor_binding", "");
    return frame;
  }

  static ObjectNode witness(Fixture f) {
    return ((ObjectNode) NetworkJson.parse("{}"))
        .put("witness_id", "w-" + UUID.randomUUID())
        .put("binding_id", "native-" + f.c().getNode())
        .put("binding_revision", "18446744073709551615")
        .put("observed_at", now().toString())
        .put("valid_until", now().plusSeconds(30).toString());
  }

  static Instant now() {
    return Instant.now().truncatedTo(ChronoUnit.SECONDS);
  }

  static byte[] bytes(JsonNode node) {
    return NetworkJson.canonical(node).getBytes(java.nio.charset.StandardCharsets.UTF_8);
  }

  static <T> T call(Supplier<T> work) {
    return RequestContext.start(null, null)
        .withPrincipal(NetworkMailboxGrpcTest.principal("svc:ai-gateway"))
        .call(() -> TenantContext.call(TENANT, work::get));
  }

  static <T> T db(Supplier<T> work) {
    return TenantContext.call(
        TENANT, () -> new TransactionTemplate(manager).execute(s -> work.get()));
  }

  static JsonNode register(Fixture f, ObjectNode frame) {
    return NetworkJson.parse(call(() -> wire.sourceRegister(f.c(), bytes(frame))));
  }

  static JsonNode issue(Fixture f) {
    return register(f, frame(f, "source_register"));
  }

  static JsonNode get(Fixture f, String binding) {
    return get(f, frame(f, "source_get").put("source_binding", binding));
  }

  static JsonNode get(Fixture f, ObjectNode frame) {
    return NetworkJson.parse(call(() -> wire.sourceGet(f.c(), bytes(frame))));
  }

  static JsonNode revoke(Fixture f, String binding) {
    return NetworkJson.parse(
        call(
            () ->
                wire.sourceRevoke(
                    f.c(),
                    bytes(
                        frame(f, "source_revoke")
                            .put("source_binding", binding)
                            .put("operation_id", "revoke-" + UUID.randomUUID())))));
  }

  static String binding(JsonNode receipt) {
    assertEquals("registered", receipt.path("result").asString(), receipt.toString());
    return receipt.path("source").path("source_binding").asString();
  }

  static void code(String code, JsonNode response) {
    assertEquals("error", response.path("type").asString(), response.toString());
    assertEquals("network." + code, response.path("code").asString());
  }

  static Fixture reconnect(Fixture f) {
    authority.invalid.add(f.c().getIngress());
    var next = f.c().toBuilder().setIngress(newIngress()).build();
    var ready =
        f.ready()
            .deepCopy()
            .put("boot_id", f.ready().path("boot_id").asString() + "-next")
            .put("channel_nonce", f.ready().path("channel_nonce").asString() + "-next")
            .put("request_id", "ready-" + UUID.randomUUID())
            .put("local_high_water", "1")
            .put("registration_revision", "1");
    call(() -> wire.ready(next, bytes(ready)));
    return new Fixture(next, ready, f.registration());
  }

  static String newIngress() {
    return UUID.randomUUID()
        .toString()
        .replace("-", "")
        .substring(0, 26)
        .replace('0', 'a')
        .replace('1', 'b')
        .replace('8', 'c')
        .replace('9', 'd');
  }

  static ObjectNode reconcile(Fixture current, ObjectNode original) {
    return original
        .deepCopy()
        .put("mode", "reconcile")
        .putNull("native_witness")
        .put("epoch", current.ready().path("boot_id").equals(original.path("boot_id")) ? "1" : "2")
        .put("boot_id", current.ready().path("boot_id").asString())
        .put("request_id", "read-" + UUID.randomUUID());
  }

  static Map<String, Object> operationRow(ObjectNode request) {
    return db(
        () ->
            jdbc.queryForMap(
                "SELECT * FROM collab.network_source_operation WHERE tenant_id=? AND method='Register' AND operation_id=?",
                TENANT,
                request.path("operation_id").asString()));
  }

  @Test
  void preV15HistoryRemainsUnknownEvenOnTheSameLiveConnectionAndNeverGetsBackfilled() {
    var current = fresh(LEGACY_CONNECTION);
    var saved = operationRow(legacyRequest);
    int sourceReads = peer.readCalls.get(),
        sourceRegisters = peer.registerCalls.get(),
        sourceGets = peer.getCalls.get(),
        anchorReads = authority.anchorReads.get();
    code("outcome_unknown", register(current, legacyRequest));
    var next = reconnect(current);
    code("outcome_unknown", register(next, reconcile(next, legacyRequest)));
    assertNull(saved.get("local_history_version"));
    assertNull(saved.get("original_connection_anchor"));
    assertNull(saved.get("local_history_digest"));
    assertTombstoneUnchanged(saved, operationRow(legacyRequest));
    assertEquals(anchorReads, authority.anchorReads.get());
    assertEquals(sourceReads, peer.readCalls.get());
    assertEquals(sourceRegisters, peer.registerCalls.get());
    assertEquals(sourceGets, peer.getCalls.get());
  }

  static ObjectNode localFailure(Fixture first) {
    var request =
        frame(first, "source_register").put("mode", "reconcile").putNull("native_witness");
    assertEquals("not_accepted", register(first, request).path("result").asString());
    return request;
  }

  static Map<String, Object> sourceFacts() {
    return db(
        () ->
            jdbc.queryForMap(
                """
        SELECT (SELECT count(*) FROM collab.network_source_lineage) AS lineages,
        (SELECT count(*) FROM collab.network_source_binding) AS bindings,
        (SELECT count(*) FROM collab.network_source_outbox) AS outbox
        """));
  }

  static void assertNoHistoricalResult(Runnable call) {
    try {
      call.run();
    } catch (auriga.common.error.AurigaException e) {
      assertTrue(
          Set.of("network.stale_epoch", "network.recovery_conflict", "network.unauthorized")
              .contains(e.errorCode().code()),
          e.errorCode().code());
    }
  }

  static void notHistorical(JsonNode response) {
    assertEquals("error", response.path("type").asString(), response.toString());
    assertFalse(response.has("failure_evidence"));
    assertFalse(response.has("result"));
  }

  @Test
  void pureAnchorReadRejectsOldOwnershipEvenWhenNewConnectionIsFullyAllowed() throws Exception {
    var first = fresh();
    var request = localFailure(first);
    var saved = operationRow(request);
    var current = reconnect(first);
    var retry = reconcile(current, request);
    assertTrue(call(() -> source.verify(current.c())).isAfter(now()));
    var facts = sourceFacts();
    int registers = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    var originalRef =
        NetworkConnectionAnchor.parseFrom((byte[]) saved.get("original_connection_anchor"));
    int anchorReads = authority.anchorReads.get();
    int anchors = authority.anchors.size();
    authority.sameHistoryOwner = false;
    code("outcome_unknown", register(current, retry));
    assertEquals(anchorReads + 1, authority.anchorReads.get());
    assertEquals(originalRef, authority.lastAnchorRead.getOriginal());
    assertEquals(authority.ref(current.c()), authority.lastAnchorRead.getCurrent());
    authority.sameHistoryOwner = true;
    for (var failure :
        List.of(
            Status.UNAVAILABLE,
            Status.PERMISSION_DENIED,
            Status.NOT_FOUND,
            Status.UNIMPLEMENTED,
            Status.CANCELLED,
            Status.DEADLINE_EXCEEDED)) {
      authority.anchorFailure = failure;
      code("outcome_unknown", register(current, retry));
    }
    authority.anchorFailure = null;
    authority.anchorReply = r -> r.toBuilder().setOriginal(r.getCurrent()).build();
    code("outcome_unknown", register(current, retry));
    authority.anchorReply = UnaryOperator.identity();
    assertRecoveredTombstone(saved, current, retry, register(current, retry));
    authority.sameHistoryOwner = false;
    code(
        "outcome_unknown",
        register(current, retry)); // A successful old read is never cached as authority.
    authority.sameHistoryOwner = true;
    assertEquals(anchors, authority.anchors.size());
    assertEquals(facts, sourceFacts());
    assertTombstoneUnchanged(saved, operationRow(request));
    assertEquals(registers, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  @Test
  void missingResolveProofDoesNotCreateLocalHistoryOrDisableEmergencySourceRevocation() {
    var f = fresh();
    String id = binding(issue(f));
    int registers = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    authority.metadata = false;
    for (var absent :
        List.of(
            frame(f, "source_register").put("mode", "reconcile").putNull("native_witness"),
            frame(f, "source_register").put("scope", "invalid"))) {
      code("outcome_unknown", register(f, absent));
      assertEquals(
          Integer.valueOf(0),
          db(
              () ->
                  jdbc.queryForObject(
                      "SELECT count(*) FROM collab.network_source_operation WHERE tenant_id=? AND operation_id=?",
                      Integer.class,
                      TENANT,
                      absent.path("operation_id").asString())));
    }
    assertEquals("source_revoke_result", revoke(f, id).path("type").asString());
    assertEquals(registers, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  static ObjectNode insertHistoryVariant(
      Map<String, Object> saved, Consumer<Map<String, Object>> change) {
    var row = new LinkedHashMap<>(saved);
    String op = "forged-history-" + UUID.randomUUID();
    row.put("id", UuidV7.next());
    row.put("operation_id", op);
    var frame = (ObjectNode) NetworkJson.parse((byte[]) saved.get("original_frame"));
    frame.put("operation_id", op);
    var result = (ObjectNode) NetworkJson.parse(saved.get("result_json").toString());
    result.put("operation_id", op);
    row.put("original_frame", bytes(frame));
    row.put("result_json", NetworkJson.canonical(result));
    LocalSourceHistoryFixtures.seal(row);
    change.accept(row);
    db(
        () ->
            new NamedParameterJdbcTemplate(jdbc)
                .update(
                    "INSERT INTO collab.network_source_operation ("
                        + String.join(",", row.keySet())
                        + ") VALUES (:"
                        + String.join(",:", row.keySet())
                        + ")",
                    row));
    return frame;
  }

  @Test
  void recomputedDigestsCannotRebindNonemptyOriginalFieldsOrAuthoritativeAnchorIds()
      throws Exception {
    var first = fresh();
    var request = localFailure(first);
    var saved = operationRow(request);
    var current = reconnect(first);
    var original = first.c();
    var originalRef =
        NetworkConnectionAnchor.parseFrom((byte[]) saved.get("original_connection_anchor"));
    int registers = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    for (String field : List.of("ingress", "server", "client", "account")) {
      var changed = original.toBuilder();
      switch (field) {
        case "ingress" -> changed.setIngress(current.c().getIngress());
        case "server" -> changed.setServerId("N" + "B".repeat(55));
        case "client" -> changed.setClientId("999");
        case "account" -> changed.setClientAccount("another-account");
        default -> throw new AssertionError(field);
      }
      var bad =
          insertHistoryVariant(
              saved,
              row -> {
                row.put("original_connection", changed.build().toByteArray());
                LocalSourceHistoryFixtures.seal(row);
              });
      code("outcome_unknown", register(current, reconcile(current, bad)));
      // Even changing both references and recomputing the digest cannot change an IAM anchor's C.
      var forged =
          insertHistoryVariant(
              saved,
              row -> {
                row.put("original_connection", changed.build().toByteArray());
                row.put(
                    "original_connection_anchor",
                    originalRef.toBuilder().setConnection(changed).build().toByteArray());
                LocalSourceHistoryFixtures.seal(row);
              });
      code("outcome_unknown", register(current, reconcile(current, forged)));
    }
    var wrongId =
        insertHistoryVariant(
            saved,
            row -> {
              row.put(
                  "original_connection_anchor",
                  originalRef.toBuilder()
                      .setAnchorId(UuidV7.next().toString())
                      .build()
                      .toByteArray());
              LocalSourceHistoryFixtures.seal(row);
            });
    code("outcome_unknown", register(current, reconcile(current, wrongId)));
    for (String field : List.of("original_connection_anchor", "original_frame")) {
      var bad = insertHistoryVariant(saved, row -> row.put(field, new byte[] {1}));
      code("outcome_unknown", register(current, reconcile(current, bad)));
    }
    for (String missing :
        List.of("local_history_version", "local_history_digest", "original_connection_anchor"))
      immutable(() -> insertHistoryVariant(saved, row -> row.put(missing, null)));
    var foreign = original.toBuilder().setTenantId(UUID.randomUUID().toString()).build();
    var wrongTenant =
        insertHistoryVariant(
            saved,
            row -> {
              row.put("original_connection", foreign.toByteArray());
              row.put(
                  "original_connection_anchor",
                  originalRef.toBuilder().setConnection(foreign).build().toByteArray());
              LocalSourceHistoryFixtures.seal(row);
            });
    code("outcome_unknown", register(current, reconcile(current, wrongTenant)));
    assertTombstoneUnchanged(saved, operationRow(request));
    assertEquals(registers, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  @Test
  void afterPureReadAllCurrentAssociationAndAdmissionFencesStillRejectHistoricalResults() {
    int registers = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    for (String phase : List.of("before", "after")) {
      for (String fence :
          List.of(
              "health",
              "ready",
              "epoch",
              "boot",
              "connection",
              "ref",
              "missing_ref",
              "slot",
              "expired_slot",
              "expired_identity")) {
        var first = fresh();
        var request = localFailure(first);
        var saved = operationRow(request);
        var current = reconnect(first);
        var retry = reconcile(current, request);
        var facts = sourceFacts();
        String changedId = UuidV7.next().toString();
        Runnable changeFence =
            () -> {
              switch (fence) {
                case "health" ->
                    db(
                        () ->
                            jdbc.update(
                                "UPDATE collab.network_node SET local_identity_status='identity_rollback' WHERE tenant_id=? AND node_id=?",
                                TENANT,
                                current.c().getNode()));
                case "ready" ->
                    db(
                        () ->
                            jdbc.update(
                                "UPDATE collab.network_node SET wire_status='reconciling' WHERE tenant_id=? AND node_id=?",
                                TENANT,
                                current.c().getNode()));
                case "epoch" ->
                    db(
                        () ->
                            jdbc.update(
                                "UPDATE collab.network_node SET connection_epoch=connection_epoch+1 WHERE tenant_id=? AND node_id=?",
                                TENANT,
                                current.c().getNode()));
                case "boot" ->
                    db(
                        () ->
                            jdbc.update(
                                "UPDATE collab.network_node SET local_boot_id='another-boot' WHERE tenant_id=? AND node_id=?",
                                TENANT,
                                current.c().getNode()));
                case "connection" ->
                    db(
                        () ->
                            jdbc.update(
                                "UPDATE collab.network_node SET wire_binding_json='{}' WHERE tenant_id=? AND node_id=?",
                                TENANT,
                                current.c().getNode()));
                case "ref" ->
                    authority.anchorView = a -> a.toBuilder().setAnchorId(changedId).build();
                case "missing_ref" -> authority.metadata = false;
                case "slot" ->
                    db(
                        () ->
                            jdbc.update(
                                "UPDATE collab.network_source_slot SET holder=? WHERE tenant_id=? AND node_id=? AND slot='regular'",
                                UuidV7.next(),
                                TENANT,
                                current.c().getNode()));
                case "expired_slot" ->
                    db(
                        () ->
                            jdbc.update(
                                "UPDATE collab.network_source_slot SET valid_until=now()-interval '1 second' WHERE tenant_id=? AND node_id=? AND slot='regular'",
                                TENANT,
                                current.c().getNode()));
                case "expired_identity" -> authority.expired = true;
                default -> throw new AssertionError(fence);
              }
            };
        if ("before".equals(phase)) authority.beforeAnchorRead = changeFence;
        else authority.afterAnchorRead = changeFence;
        try {
          assertNoHistoricalResult(() -> notHistorical(register(current, retry)));
        } finally {
          authority.afterAnchorRead = () -> {};
          authority.beforeAnchorRead = () -> {};
          authority.anchorView = UnaryOperator.identity();
          authority.metadata = true;
          authority.expired = false;
          db(
              () ->
                  jdbc.update(
                      "DELETE FROM collab.network_source_slot WHERE tenant_id=? AND node_id=?",
                      TENANT,
                      current.c().getNode()));
        }
        assertTombstoneUnchanged(saved, operationRow(request));
        assertEquals(facts, sourceFacts());
      }
    }
    assertEquals(registers, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  @Test
  void concurrentReconcileAndLateIssueKeepTheSamePermanentResultWithBoundedAdmission()
      throws Exception {
    var first = fresh();
    var request = localFailure(first);
    var saved = operationRow(request);
    var current = reconnect(first);
    var retry = reconcile(current, request);
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    var executor = Executors.newSingleThreadExecutor();
    int registers = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    int anchorReads = authority.anchorReads.get();
    authority.afterAnchorRead =
        () -> {
          entered.countDown();
          await(release);
        };
    try {
      var recovering = executor.submit(() -> register(current, retry));
      await(entered);
      var late = retry.deepCopy().put("mode", "issue");
      late.set("native_witness", witness(current));
      code("overloaded", register(current, late));
      release.countDown();
      assertRecoveredTombstone(saved, current, retry, recovering.get(5, TimeUnit.SECONDS));
      authority.afterAnchorRead = () -> {};
      assertRecoveredTombstone(saved, current, late, register(current, late));
      assertTombstoneUnchanged(saved, operationRow(request));
      assertEquals(anchorReads + 2, authority.anchorReads.get());
    } finally {
      release.countDown();
      executor.shutdownNow();
      assertTrue(executor.awaitTermination(5, TimeUnit.SECONDS));
    }
    assertEquals(registers, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  static final class UnsafeAnchorReadOmission extends NetworkSourceGrpcAuthority {
    UnsafeAnchorReadOmission() {
      super(client, manager);
    }

    @Override
    public ReadNetworkConnectionAnchorResponse readConnectionAnchor(
        NetworkConnectionAnchor original, NetworkConnectionAnchor current) {
      // Deliberately bad compiled variant: local structural matches are not an IAM ownership read.
      return ReadNetworkConnectionAnchorResponse.newBuilder()
          .setOriginal(original)
          .setCurrent(current)
          .build();
    }
  }

  @Test
  void compiledPureReadOmissionLeaksHistoryUnderTheSameWrongOwnershipInvariant() {
    var first = fresh();
    var request = localFailure(first);
    var current = reconnect(first);
    var retry = reconcile(current, request);
    var saved = operationRow(request);
    var facts = sourceFacts();
    int registers = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    authority.sameHistoryOwner = false;
    code("outcome_unknown", register(current, retry));
    var productionSource = source;
    try {
      source = new UnsafeAnchorReadOmission();
      wire = newWire();
      assertThrows(AssertionError.class, () -> code("outcome_unknown", register(current, retry)));
    } finally {
      source = productionSource;
      wire = newWire();
    }
    assertTombstoneUnchanged(saved, operationRow(request));
    assertEquals(facts, sourceFacts());
    assertEquals(registers, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  static NetworkMailboxWireService assembled(
      JdbcTemplate reads, TenantTransactionManager transactions) {
    return new NetworkMailboxWireService(
        new NetworkMailboxService(reads, transactions, source, keys, CLOCK, client),
        locals,
        reads,
        transactions,
        source,
        keys,
        CLOCK);
  }

  @Test
  void finalCasRejectsChangedFrozenRowsFromARestrictedReaderWithoutWeakeningDatabaseGuards() {
    var first = fresh();
    var request = localFailure(first);
    var saved = operationRow(request);
    var current = reconnect(first);
    var retry = reconcile(current, request);
    var afterRead = new AtomicBoolean();
    var changedRead = new AtomicBoolean();
    var restricted =
        new JdbcTemplate(pool) {
          @Override
          public List<Map<String, Object>> queryForList(String sql, Object... args) {
            var rows = super.queryForList(sql, args);
            if (afterRead.get()
                && sql.startsWith("SELECT * FROM collab.network_source_operation")
                && !rows.isEmpty()) {
              var changed = new LinkedHashMap<>(rows.getFirst());
              changed.put("request_digest", "sha256:" + "b".repeat(64));
              changedRead.set(true);
              return List.of(changed);
            }
            return rows;
          }
        };
    authority.afterAnchorRead = () -> afterRead.set(true);
    int registers = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    try {
      // Real PG reads/transactions; only the final returned row is fault-injected in this assembly.
      wire = assembled(restricted, manager);
      code("outcome_unknown", register(current, retry));
      assertTrue(changedRead.get());
    } finally {
      wire = newWire();
    }
    assertTombstoneUnchanged(saved, operationRow(request));
    assertEquals(registers, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  @Test
  void failedOrUncertainLocalCommitNeverSignsANewFactAndCommittedRetryUsesPureHistoryRead() {
    int registers = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    for (boolean committed : List.of(false, true)) {
      var first = fresh();
      var request =
          frame(first, "source_register").put("mode", "reconcile").putNull("native_witness");
      var faulted = new AtomicBoolean();
      var armCommitFailure = new AtomicBoolean();
      var facts = sourceFacts();
      var transactions =
          new TenantTransactionManager(pool) {
            @Override
            protected void doCommit(DefaultTransactionStatus status) {
              super.doCommit(status);
              if (armCommitFailure.getAndSet(false))
                throw new org.springframework.transaction.TransactionSystemException(
                    "test commit acknowledgement lost");
            }
          };
      var readsWithFault =
          new JdbcTemplate(pool) {
            @Override
            public List<Map<String, Object>> queryForList(String sql, Object... args) {
              var rows = super.queryForList(sql, args);
              if (sql.startsWith("SELECT * FROM collab.network_source_operation")
                  && !rows.isEmpty()
                  && request
                      .path("operation_id")
                      .asString()
                      .equals(rows.getFirst().get("operation_id"))
                  && "not_accepted".equals(rows.getFirst().get("status"))
                  && faulted.compareAndSet(false, true)) {
                if (!committed)
                  throw new org.springframework.dao.DataIntegrityViolationException(
                      "test before commit failure");
                armCommitFailure.set(true);
              }
              return rows;
            }
          };
      int anchorReads = authority.anchorReads.get();
      try {
        wire = assembled(readsWithFault, transactions);
        code("outcome_unknown", register(first, request));
      } finally {
        wire = newWire();
      }
      assertTrue(faulted.get());
      assertEquals(anchorReads, authority.anchorReads.get());
      assertEquals(facts, sourceFacts());
      if (committed) {
        var saved = operationRow(request);
        assertEquals("not_accepted", register(first, request).path("result").asString());
        assertEquals(anchorReads + 1, authority.anchorReads.get());
        assertTombstoneUnchanged(saved, operationRow(request));
      } else
        assertEquals(
            Integer.valueOf(0),
            db(
                () ->
                    jdbc.queryForObject(
                        "SELECT count(*) FROM collab.network_source_operation WHERE tenant_id=? AND operation_id=?",
                        Integer.class,
                        TENANT,
                        request.path("operation_id").asString())));
    }
    assertEquals(registers, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  @Test
  void localNotAcceptedLostReplyRecoversAcrossConnectionsWithoutIamSourceCalls() throws Exception {
    int registrations = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    for (String cause :
        List.of("expired_native", "invalid_field", "stale_epoch", "absent_reconcile")) {
      var first = fresh();
      var request = frame(first, "source_register");
      switch (cause) {
        case "expired_native" ->
            ((ObjectNode) request.path("native_witness"))
                .put("observed_at", now().minusSeconds(40).toString())
                .put("valid_until", now().minusSeconds(10).toString());
        case "invalid_field" -> request.put("scope", "invalid");
        case "stale_epoch" -> request.put("epoch", "99");
        case "absent_reconcile" -> request.put("mode", "reconcile").putNull("native_witness");
        default -> throw new AssertionError(cause);
      }
      // The client loses this response; only the committed PG tombstone survives a new wire
      // instance.
      var lost = register(first, request);
      assertEquals("not_accepted", lost.path("result").asString(), lost.toString());
      var saved = operationRow(request);
      assertNull(saved.get("iam_request"));
      var current = reconnect(first);
      wire = newWire();
      var retry = reconcile(current, request);
      for (int attempt = 0; attempt < 3; attempt++) {
        retry.put("request_id", "tombstone-" + UUID.randomUUID());
        var recovered = register(current, retry);
        assertRecoveredTombstone(saved, current, retry, recovered);
        assertTombstoneUnchanged(saved, operationRow(request));
      }
      // A late issue with fresh native still replays the original permanent failure.
      retry.put("mode", "issue").set("native_witness", witness(current));
      assertRecoveredTombstone(saved, current, retry, register(current, retry));
      assertTombstoneUnchanged(saved, operationRow(request));
      NetworkAuthorityGrpcTest.denied(() -> register(first, request), "network.unauthorized");
      assertEquals(0, sourceCount(current));
      assertEquals(
          Integer.valueOf(0),
          db(
              () ->
                  jdbc.queryForObject(
                      "SELECT count(*) FROM collab.network_source_outbox WHERE tenant_id=? AND operation_id=?",
                      Integer.class,
                      TENANT,
                      saved.get("id"))));
    }
    assertEquals(registrations, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  static void assertRecoveredTombstone(
      Map<String, Object> saved, Fixture current, ObjectNode request, JsonNode response)
      throws Exception {
    assertEquals("not_accepted", response.path("result").asString(), response.toString());
    var businessResult = (ObjectNode) response.deepCopy();
    businessResult.remove("request_id");
    businessResult.remove("server_auth");
    assertEquals(NetworkJson.parse(saved.get("result_json").toString()), businessResult);
    assertEquals(request.path("request_id"), response.path("request_id"));
    assertEquals("2", response.path("server_auth").path("audience").path("epoch").asString());
    assertEquals(
        current.ready().path("channel_nonce"), response.path("server_auth").path("channel_nonce"));
    assertEquals(request.path("request_id"), response.path("server_auth").path("request_id"));
    var unsigned = (ObjectNode) response.deepCopy();
    String signature = unsigned.path("server_auth").path("signature").asString();
    ((ObjectNode) unsigned.path("server_auth")).remove("signature");
    var verifier = Signature.getInstance("Ed25519");
    verifier.initVerify(keys.publicKey("source-test"));
    verifier.update("Auriga.ServerEnvelope.v1\0".getBytes(StandardCharsets.UTF_8));
    verifier.update(bytes(unsigned));
    assertTrue(verifier.verify(Base64.getUrlDecoder().decode(signature)));
  }

  static void assertTombstoneUnchanged(Map<String, Object> before, Map<String, Object> after) {
    assertEquals(before.keySet(), after.keySet());
    before.forEach(
        (key, value) -> {
          if (value instanceof byte[] original)
            assertArrayEquals(original, (byte[]) after.get(key), key);
          else assertEquals(value, after.get(key), key);
        });
  }

  @Test
  void localTombstoneRecoveryRejectsDifferentBodiesNodesTenantsAndOriginalAssociations()
      throws Exception {
    var first = fresh();
    var request =
        frame(first, "source_register").put("mode", "reconcile").putNull("native_witness");
    assertEquals("not_accepted", register(first, request).path("result").asString());
    var saved = operationRow(request);
    var current = reconnect(first);
    var retry = reconcile(current, request);
    int registrations = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    code("conflict", register(current, retry.deepCopy().put("expected_revision", "1")));
    var other = fresh();
    code("unauthorized", register(other, reconcile(other, request)));
    var foreign = current.c().toBuilder().setTenantId(UUID.randomUUID().toString()).build();
    NetworkAuthorityGrpcTest.denied(
        () -> call(() -> wire.sourceRegister(foreign, bytes(retry))), "common.forbidden");
    var original = NetworkAuthorityConnection.parseFrom((byte[]) saved.get("original_connection"));
    for (var corrupted :
        List.of(
            original.toBuilder().setTenantId(UUID.randomUUID().toString()).build(),
            original.toBuilder().setNode(other.c().getNode()).build(),
            original.toBuilder().setRealm("999").build())) {
      immutable(
          () ->
              db(
                  () ->
                      jdbc.update(
                          "UPDATE collab.network_source_operation SET original_connection=? WHERE tenant_id=? AND id=?",
                          corrupted.toByteArray(),
                          TENANT,
                          saved.get("id"))));
    }
    assertRecoveredTombstone(saved, current, retry, register(current, retry));
    assertTombstoneUnchanged(saved, operationRow(request));
    assertEquals(registrations, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  @Test
  void localTombstonesCannotBeReboundDeletedOrDowngradedByRuntime() {
    var first = fresh();
    var request =
        frame(first, "source_register").put("mode", "reconcile").putNull("native_witness");
    assertEquals("not_accepted", register(first, request).path("result").asString());
    var saved = operationRow(request);
    int registrations = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    var wrongFrame = request.deepCopy().put("operation_id", "another-op");
    for (String column :
        List.of(
            "request_digest",
            "result_json",
            "original_frame",
            "boot_id",
            "epoch",
            "business_json",
            "local_history_version",
            "original_connection_anchor",
            "local_history_digest")) {
      Object bad =
          switch (column) {
            case "request_digest" -> "sha256:" + "b".repeat(64);
            case "result_json" ->
                saved
                    .get("result_json")
                    .toString()
                    .replace("rejected_before_pending", "another_reason");
            case "original_frame" -> bytes(wrongFrame);
            case "boot_id" -> "another-boot";
            case "epoch" -> 0L;
            case "business_json" -> "{}";
            case "local_history_version" -> null;
            case "original_connection_anchor" -> new byte[] {7};
            case "local_history_digest" -> null;
            default -> throw new AssertionError(column);
          };
      // Column names are a fixed test list, never input from a wire request.
      immutable(
          () ->
              db(
                  () ->
                      jdbc.update(
                          "UPDATE collab.network_source_operation SET "
                              + column
                              + "=? WHERE tenant_id=? AND id=?",
                          bad,
                          TENANT,
                          saved.get("id"))));
    }
    immutable(
        () ->
            db(
                () ->
                    jdbc.update(
                        "DELETE FROM collab.network_source_operation WHERE tenant_id=? AND id=?",
                        TENANT,
                        saved.get("id"))));
    immutable(
        () ->
            db(
                () ->
                    jdbc.update(
                        "UPDATE collab.network_source_operation SET status='pending' WHERE tenant_id=? AND id=?",
                        TENANT,
                        saved.get("id"))));
    assertTombstoneUnchanged(saved, operationRow(request));
    assertEquals(registrations, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  static void immutable(Runnable work) {
    var failure = assertThrows(org.springframework.dao.DataAccessException.class, work::run);
    assertEquals(
        "23514",
        assertInstanceOf(java.sql.SQLException.class, failure.getMostSpecificCause())
            .getSQLState());
  }

  @Test
  void localTombstoneRecoveryRequiresLiveReadyHealthyCurrentConnectionAndFrameFences()
      throws Exception {
    var first = fresh();
    var request =
        frame(first, "source_register").put("mode", "reconcile").putNull("native_witness");
    assertEquals("not_accepted", register(first, request).path("result").asString());
    var saved = operationRow(request);
    var current = reconnect(first);
    var retry = reconcile(current, request);
    int registrations = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    code("stale_epoch", register(current, retry.deepCopy().put("epoch", "1")));
    code("stale_epoch", register(current, retry.deepCopy().put("boot_id", "old-boot")));
    db(
        () ->
            jdbc.update(
                "UPDATE collab.network_node SET local_identity_status='identity_rollback' WHERE tenant_id=? AND node_id=?",
                TENANT,
                current.c().getNode()));
    code("identity_rollback", register(current, retry));
    db(
        () ->
            jdbc.update(
                "UPDATE collab.network_node SET local_identity_status='healthy',wire_status='reconciling' WHERE tenant_id=? AND node_id=?",
                TENANT,
                current.c().getNode()));
    NetworkAuthorityGrpcTest.denied(() -> register(current, retry), "network.recovery_conflict");
    db(
        () ->
            jdbc.update(
                "UPDATE collab.network_node SET wire_status='ready' WHERE tenant_id=? AND node_id=?",
                TENANT,
                current.c().getNode()));
    authority.expired = true;
    try {
      NetworkAuthorityGrpcTest.denied(() -> register(current, retry), "network.unauthorized");
    } finally {
      authority.expired = false;
    }
    authority.invalid.add(current.c().getIngress());
    NetworkAuthorityGrpcTest.denied(() -> register(current, retry), "network.unauthorized");
    authority.invalid.remove(current.c().getIngress());
    // IAM's authoritative fixture rejects an exact current association with a changed owner.
    authority.wrongOwner.add(current.c());
    try {
      NetworkAuthorityGrpcTest.denied(() -> register(current, retry), "network.unauthorized");
    } finally {
      authority.wrongOwner.remove(current.c());
    }
    assertRecoveredTombstone(saved, current, retry, register(current, retry));
    assertTombstoneUnchanged(saved, operationRow(request));
    assertEquals(registrations, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  @Test
  void localTombstoneRecoveryRechecksHealthInFinalSigningTransaction() {
    var first = fresh();
    var request =
        frame(first, "source_register").put("mode", "reconcile").putNull("native_witness");
    assertEquals("not_accepted", register(first, request).path("result").asString());
    var saved = operationRow(request);
    var current = reconnect(first);
    var retry = reconcile(current, request);
    int registrations = peer.registerCalls.get(),
        reads = peer.readCalls.get(),
        gets = peer.getCalls.get();
    var resolves = new AtomicInteger();
    authority.beforeResolveReply =
        () -> {
          if (resolves.incrementAndGet() == 2)
            db(
                () ->
                    jdbc.update(
                        "UPDATE collab.network_node SET local_identity_status='identity_rollback' WHERE tenant_id=? AND node_id=?",
                        TENANT,
                        current.c().getNode()));
        };
    // First gate and history checks pass; health changes during the final outside-transaction IAM
    // refresh.
    code("identity_rollback", register(current, retry));
    assertEquals(2, resolves.get());
    assertTombstoneUnchanged(saved, operationRow(request));
    assertEquals(registrations, peer.registerCalls.get());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(gets, peer.getCalls.get());
  }

  @Test
  void committedIamReceiptLostByCollabRecoversAfterReconnectWithFrozenOriginalRequest()
      throws Exception {
    var first = fresh();
    var request = frame(first, "source_register");
    peer.loseRegister = true;
    code("outcome_unknown", register(first, request));
    peer.loseRegister = false;
    var before = operationRow(request);
    var original = RegisterAgentSourceRequest.parseFrom((byte[]) before.get("iam_request"));
    int registrations = peer.registerCalls.get(),
        gets = peer.getCalls.get(),
        reads = peer.readCalls.get();
    var current = reconnect(first);
    var read = reconcile(current, request);
    var recovered = register(current, read);
    String id = binding(recovered);
    assertEquals(original, peer.lastRead.getOriginalRequest());
    assertEquals(reads + 1, peer.readCalls.get());
    assertEquals(registrations, peer.registerCalls.get());
    assertEquals(gets, peer.getCalls.get());
    var saved = operationRow(request);
    assertEquals("registered", saved.get("status"));
    assertArrayEquals((byte[]) before.get("iam_request"), (byte[]) saved.get("iam_request"));
    assertArrayEquals((byte[]) before.get("original_frame"), (byte[]) saved.get("original_frame"));
    assertEquals(before.get("request_digest"), saved.get("request_digest"));
    assertEquals(
        peer.receipts.get(SourcePeer.key(original)),
        RegisterAgentSourceResponse.parseFrom((byte[]) saved.get("iam_response")));
    assertEquals("2", recovered.path("server_auth").path("audience").path("epoch").asString());
    assertEquals(
        current.ready().path("channel_nonce"), recovered.path("server_auth").path("channel_nonce"));
    assertEquals(read.path("request_id"), recovered.path("request_id"));
    assertNoActiveObservation(id);
    var missingWitness = frame(current, "source_get").put("epoch", "2").put("source_binding", id);
    missingWitness.remove("native_witness");
    NetworkAuthorityGrpcTest.denied(() -> get(current, missingWitness), "network.protocol_error");
    assertEquals(gets, peer.getCalls.get());
    assertEquals(
        "active",
        get(current, frame(current, "source_get").put("epoch", "2").put("source_binding", id))
            .path("source")
            .path("state")
            .asString());
  }

  @Test
  void readMissAndAllIndeterminateStatusesRetainPendingWithoutRegistrationFallback() {
    var f = fresh();
    var request = frame(f, "source_register");
    peer.dropRegisterBeforeCommit = true;
    code("outcome_unknown", register(f, request));
    peer.dropRegisterBeforeCommit = false;
    int registrations = peer.registerCalls.get();
    var read = reconcile(f, request);
    for (Status status :
        List.of(
            Status.UNAVAILABLE,
            Status.PERMISSION_DENIED,
            Status.UNIMPLEMENTED,
            Status.NOT_FOUND,
            Status.CANCELLED,
            Status.DEADLINE_EXCEEDED)) {
      peer.readFailure = status;
      code("outcome_unknown", register(f, read));
      assertEquals("pending", operationRow(request).get("status"));
      assertNull(operationRow(request).get("iam_response"));
    }
    peer.readFailure = null;
    code("outcome_unknown", register(f, read));
    assertEquals(registrations, peer.registerCalls.get());
    assertEquals(0, sourceCount(f));
    int reads = peer.readCalls.get();
    var absent = frame(f, "source_register").put("mode", "reconcile").putNull("native_witness");
    assertEquals("not_accepted", register(f, absent).path("result").asString());
    assertEquals(reads, peer.readCalls.get());
    assertEquals(registrations, peer.registerCalls.get());
  }

  @Test
  void readFailureReceiptRequiresExactSavedDigestBeforeDurableProjection() throws Exception {
    var f = fresh();
    var request = frame(f, "source_register");
    peer.reject = true;
    peer.loseRegister = true;
    code("outcome_unknown", register(f, request));
    peer.loseRegister = false;
    var current = reconnect(f);
    var read = reconcile(current, request);
    peer.readReply = r -> r.toBuilder().setRequestDigest("sha256:" + "b".repeat(64)).build();
    code("outcome_unknown", register(current, read));
    assertNull(operationRow(request).get("iam_response"));
    assertEquals("pending", operationRow(request).get("status"));
    peer.readReply = UnaryOperator.identity();
    var response = register(current, read);
    assertEquals("not_committed", response.path("result").asString(), response.toString());
    var saved = operationRow(request);
    assertEquals(
        saved.get("request_digest"),
        response.path("failure_evidence").path("request_digest").asString());
    var receipt = RegisterAgentSourceResponse.parseFrom((byte[]) saved.get("iam_response"));
    assertEquals(peer.receipts.get(SourcePeer.key(peer.lastRead.getOriginalRequest())), receipt);
    assertEquals("revision_mismatch", receipt.getRejectionReason());
    assertEquals(0, sourceCount(f));
  }

  @Test
  void badReadTuplesFrozenBodiesAndOwnerFailuresNeverReplaceSavedTruth() throws Exception {
    var f = fresh();
    var request = frame(f, "source_register");
    peer.loseRegister = true;
    code("outcome_unknown", register(f, request));
    peer.loseRegister = false;
    var originalRow = operationRow(request);
    var original = RegisterAgentSourceRequest.parseFrom((byte[]) originalRow.get("iam_request"));
    var current = reconnect(f);
    var read = reconcile(current, request);
    List<UnaryOperator<RegisterAgentSourceResponse>> invalid =
        List.of(
            r ->
                r.toBuilder()
                    .setSource(
                        r.getSource().toBuilder()
                            .setOriginalConnection(
                                original.getConnection().toBuilder()
                                    .setTenantId(UUID.randomUUID().toString())))
                    .build(),
            r ->
                r.toBuilder()
                    .setSource(
                        r.getSource().toBuilder()
                            .setRegistration(
                                original.getRegistration().toBuilder().setNode("99999")))
                    .build(),
            r -> r.toBuilder().setSource(r.getSource().toBuilder().setScopeValue(0)).build(),
            r ->
                r.toBuilder()
                    .setSource(r.getSource().toBuilder().setPredecessorBinding("wrong-head"))
                    .build(),
            r -> r.toBuilder().setSource(r.getSource().toBuilder().setRevision(2)).build());
    for (var change : invalid) {
      peer.readReply = change;
      code("outcome_unknown", register(current, read));
      assertNull(operationRow(request).get("iam_response"));
    }
    peer.readReply = UnaryOperator.identity();
    int reads = peer.readCalls.get();
    var alteredFrame = read.deepCopy();
    ((ObjectNode) alteredFrame.path("registration"))
        .put("allocation_fingerprint", "sha256:" + "b".repeat(64));
    code("conflict", register(current, alteredFrame));
    assertEquals(reads, peer.readCalls.get());
    var changed = original.toBuilder().setExpectedRevision(1).build();
    db(
        () ->
            jdbc.update(
                "UPDATE collab.network_source_operation SET iam_request=? WHERE tenant_id=? AND id=?",
                changed.toByteArray(),
                TENANT,
                originalRow.get("id")));
    code("outcome_unknown", register(current, read));
    assertEquals(reads, peer.readCalls.get());
    db(
        () ->
            jdbc.update(
                "UPDATE collab.network_source_operation SET iam_request=? WHERE tenant_id=? AND id=?",
                original.toByteArray(),
                TENANT,
                originalRow.get("id")));
    peer.receiptOwnerValid = false;
    code("outcome_unknown", register(current, read));
    peer.receiptOwnerValid = true;
    String id = binding(register(current, read));
    assertNoActiveObservation(id);
    var savedReceipt = (byte[]) operationRow(request).get("iam_response");
    peer.readReply =
        r ->
            r.toBuilder()
                .setSource(r.getSource().toBuilder().setSourceBinding("other-valid-binding"))
                .build();
    code("outcome_unknown", register(current, read));
    assertArrayEquals(savedReceipt, (byte[]) operationRow(request).get("iam_response"));
    peer.readReply = UnaryOperator.identity();
    peer.receiptOwnerValid = false;
    code("outcome_unknown", register(current, read));
    peer.receiptOwnerValid = true;
    var other = fresh();
    code(
        "unauthorized",
        register(other, reconcile(other, request))); // Same op is not enough for another node.
    UUID foreign = UUID.randomUUID();
    reads = peer.readCalls.get();
    TenantContext.run(
        foreign,
        () ->
            NetworkAuthorityGrpcTest.denied(
                () -> client.readSourceReceipt(original), "common.forbidden"));
    assertEquals(reads, peer.readCalls.get());
    assertNoActiveObservation(id);
  }

  @Test
  void lateHistoricalReadCannotUndoReservedSlotRevocation() throws Exception {
    var first = fresh();
    var request = frame(first, "source_register");
    String id = binding(register(first, request));
    get(first, id);
    var current = reconnect(first);
    var read = reconcile(current, request);
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    peer.afterRead =
        () -> {
          entered.countDown();
          await(release);
        };
    int gets = peer.getCalls.get();
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var pending = executor.submit(() -> register(current, read));
      assertTrue(entered.await(3, TimeUnit.SECONDS));
      var revoke =
          frame(current, "source_revoke")
              .put("epoch", "2")
              .put("source_binding", id)
              .put("operation_id", "stop-" + UUID.randomUUID());
      var result = NetworkJson.parse(call(() -> wire.sourceRevoke(current.c(), bytes(revoke))));
      assertTrue(result.path("lineage_stopped").asBoolean(), result.toString());
      release.countDown();
      var receipt = pending.get(5, TimeUnit.SECONDS);
      assertEquals("registered", receipt.path("result").asString());
      assertEquals(
          "active",
          receipt.path("source").path("state").asString(),
          "The historical receipt stays immutable");
      assertEquals(gets, peer.getCalls.get());
      assertNoActiveObservation(id);
      assertTrue(
          db(
              () ->
                  jdbc.queryForObject(
                      "SELECT stopped FROM collab.network_source_lineage WHERE tenant_id=? AND registration_id=?",
                      Boolean.class,
                      TENANT,
                      UUID.fromString(first.registration().path("registration_id").asString()))));
    } finally {
      release.countDown();
    }
  }

  @Test
  void compiledReadMissRegisterFallbackViolatesTheSameNoCreationInvariant() throws Exception {
    var f = fresh();
    var request = frame(f, "source_register");
    peer.dropRegisterBeforeCommit = true;
    code("outcome_unknown", register(f, request));
    peer.dropRegisterBeforeCommit = false;
    var original =
        RegisterAgentSourceRequest.parseFrom((byte[]) operationRow(request).get("iam_request"));
    int registrations = peer.registerCalls.get();
    code("outcome_unknown", register(f, reconcile(f, request)));
    assertEquals(registrations, peer.registerCalls.get());
    assertNoReadCreation(f);
    var bad = call(() -> UnsafeReadFallback.read(source, original));
    assertTrue(bad.hasSource());
    assertThrows(AssertionError.class, () -> assertNoReadCreation(f));
    assertEquals(registrations + 1, peer.registerCalls.get());
  }

  static final class UnsafeReadFallback {
    static RegisterAgentSourceResponse read(
        NetworkSourceGrpcAuthority source, RegisterAgentSourceRequest original) {
      try {
        return source.readSourceReceipt(original);
      } catch (auriga.common.error.AurigaException e) {
        return source.registerSource(original);
      }
    }
  }

  static int sourceCount(Fixture f) {
    return (int)
        peer.sources.values().stream()
            .filter(
                s ->
                    s.getRegistration()
                        .getRegistrationId()
                        .equals(f.registration().path("registration_id").asString()))
            .count();
  }

  static void assertNoReadCreation(Fixture f) {
    assertEquals(0, sourceCount(f), "A receipt read must never create an IAM source");
  }

  @Test
  void realGrpcUsesFullTypedWitnessAndStoresOriginalRequestAndObservation() {
    var f = fresh();
    var frame = frame(f, "source_register");
    var c = f.c();
    var mailboxConnection =
        NetworkMailboxConnection.newBuilder()
            .setTenantId(c.getTenantId())
            .setRealm(c.getRealm())
            .setNode(c.getNode())
            .setIngress(c.getIngress())
            .setServerId(c.getServerId())
            .setClientId(c.getClientId())
            .setClientAccount(c.getClientAccount())
            .build();
    var result =
        NetworkJson.parse(
            call(
                () ->
                    NetworkMailboxServiceGrpc.newBlockingStub(mailboxChannel)
                        .sourceRegister(
                            NetworkMailboxServiceSourceRegisterRequest.newBuilder()
                                .setConnection(mailboxConnection)
                                .setFrameJson(ByteString.copyFrom(bytes(frame)))
                                .build())
                        .getFrameJson()
                        .toStringUtf8()));
    String binding = binding(result);
    var current = get(f, binding);
    assertEquals("active", current.path("source").path("state").asString(), current.toString());
    assertTrue(Instant.parse(current.path("valid_until").asString()).isAfter(Instant.now()));
    assertEquals("protocol_response", current.path("server_auth").path("purpose").asString());
    assertFalse(current.path("source").has("owner_user_id"));
    assertFalse(current.path("source").has("original_connection"));
    assertEquals(
        "18446744073709551615",
        current.path("source").path("native_binding").path("binding_revision").asString());
    assertEquals(f.c(), peer.lastGet.getConnection());
    var row =
        db(
            () ->
                jdbc.queryForMap(
                    "SELECT * FROM collab.network_source_operation WHERE tenant_id=? AND operation_id=?",
                    TENANT,
                    frame.path("operation_id").asString()));
    assertArrayEquals(bytes(frame), (byte[]) row.get("original_frame"));
    assertEquals(
        NetworkJson.digest(NetworkJson.parse(row.get("iam_request_json").toString())),
        row.get("request_digest"));
    assertNotNull(row.get("iam_response"));
    assertNotNull(
        db(
            () ->
                jdbc.queryForObject(
                    "SELECT valid_until FROM collab.network_source_binding WHERE tenant_id=? AND source_binding=?",
                    java.sql.Timestamp.class,
                    TENANT,
                    binding)));
  }

  @Test
  void pendingAndSavedReceiptsUsePureReadWithoutCurrentGetOrRegisterReplay() {
    var f = fresh();
    var request = frame(f, "source_register");
    peer.loseRegister = true;
    code("outcome_unknown", register(f, request));
    peer.loseRegister = false;
    var reconcile = request.deepCopy().put("mode", "reconcile").putNull("native_witness");
    int before = peer.registerCalls.get();
    String binding = binding(register(f, reconcile));
    assertEquals(before, peer.registerCalls.get());
    request.set("native_witness", witness(f));
    int getsBeforeIssue = peer.getCalls.get();
    assertEquals(binding, binding(register(f, request)));
    assertEquals(
        getsBeforeIssue,
        peer.getCalls.get(),
        "Register must not substitute current Get for a historical receipt read");
    authority.invalid.add(f.c().getIngress());
    var next = receiptReconnectConnection(f.c());
    var ready =
        f.ready()
            .deepCopy()
            .put("boot_id", "next-boot")
            .put("channel_nonce", "next-nonce")
            .put("request_id", "next-ready")
            .put("local_high_water", "1")
            .put("registration_revision", "1");
    call(() -> wire.ready(next, bytes(ready)));
    var nextFixture = new Fixture(next, ready, f.registration());
    var verified = call(() -> source.verifiedConnection(next));
    assertNotNull(verified.anchor(), "canonical C2 must retain its authoritative reference");
    assertEquals(next, verified.anchor().getConnection());
    var proof = call(() -> source.readConnectionAnchor(authority.ref(f.c()), verified.anchor()));
    assertEquals(authority.ref(f.c()), proof.getOriginal());
    assertEquals(verified.anchor(), proof.getCurrent());
    reconcile.put("epoch", "2").put("boot_id", "next-boot");
    int getsBeforeReplay = peer.getCalls.get();
    assertEquals(binding, binding(register(nextFixture, reconcile)));
    assertEquals(
        getsBeforeReplay,
        peer.getCalls.get(),
        "Cross-connection receipt reads need the dedicated IAM RPC");
    assertEquals(f.c(), peer.lastRead.getOriginalRequest().getConnection());
    assertEquals(
        f.c(),
        peer.requests.get(TENANT + "/" + request.path("operation_id").asString()).getConnection());
    assertNoActiveObservation(binding);
    assertEquals(
        "2",
        NetworkJson.parse(call(() -> wire.sourceRegister(nextFixture.c(), bytes(reconcile))))
            .path("server_auth")
            .path("audience")
            .path("epoch")
            .asString());
    var get = frame(nextFixture, "source_get").put("source_binding", binding).put("epoch", "2");
    assertEquals("active", get(nextFixture, get).path("source").path("state").asString());
    assertEquals(next, peer.lastGet.getConnection());
  }

  static NetworkAuthorityConnection receiptReconnectConnection(
      NetworkAuthorityConnection original) {
    // Resolve can return a retained reference only for a canonical redeemed C.
    return original.toBuilder().setIngress(newIngress()).build();
  }

  @Test
  void definitiveTombstonesFenceLateIssueAndRecoveryGapsRemainUnknown() {
    var f = fresh();
    var request = frame(f, "source_register");
    ((ObjectNode) request.path("native_witness"))
        .put("observed_at", now().minusSeconds(40).toString())
        .put("valid_until", now().minusSeconds(10).toString());
    var failed = register(f, request);
    assertEquals("not_accepted", failed.path("result").asString(), failed.toString());
    request.set("native_witness", witness(f));
    assertEquals(failed.path("failure_evidence"), register(f, request).path("failure_evidence"));
    assertFalse(peer.requests.containsKey(TENANT + "/" + request.path("operation_id").asString()));
    var missing = frame(f, "source_register").put("mode", "reconcile").putNull("native_witness");
    assertEquals("not_accepted", register(f, missing).path("result").asString());
    db(
        () ->
            jdbc.update(
                "UPDATE collab.network_node SET source_operation_recovery_gap=true WHERE tenant_id=? AND node_id=?",
                TENANT,
                f.c().getNode()));
    code(
        "outcome_unknown",
        register(
            f, frame(f, "source_register").put("mode", "reconcile").putNull("native_witness")));
  }

  @Test
  void unknownAfterConnectionReplacementNeverUsesResolveDenialAsANoCreateProof() {
    var f = fresh();
    var request = frame(f, "source_register");
    peer.dropRegisterBeforeCommit = true;
    code("outcome_unknown", register(f, request));
    peer.dropRegisterBeforeCommit = false;
    int calls = peer.registerCalls.get();
    authority.invalid.add(f.c().getIngress());
    var next = f.c().toBuilder().setIngress(f.c().getIngress() + "-next").build();
    var ready =
        f.ready()
            .deepCopy()
            .put("boot_id", "next-unknown-boot")
            .put("channel_nonce", "next-unknown-nonce")
            .put("request_id", "next-unknown-ready")
            .put("local_high_water", "1")
            .put("registration_revision", "1");
    call(() -> wire.ready(next, bytes(ready)));
    var reconnect = new Fixture(next, ready, f.registration());
    var reconcile =
        request
            .deepCopy()
            .put("mode", "reconcile")
            .putNull("native_witness")
            .put("epoch", "2")
            .put("boot_id", "next-unknown-boot");
    code("outcome_unknown", register(reconnect, reconcile));
    assertEquals(calls, peer.registerCalls.get());
    assertEquals(
        "pending",
        db(
            () ->
                jdbc.queryForObject(
                    "SELECT status FROM collab.network_source_operation WHERE tenant_id=? AND operation_id=?",
                    String.class,
                    TENANT,
                    request.path("operation_id").asString())));
  }

  @Test
  void committedIamRejectionIsStoredAndOrdinaryRpcFailureIsNotDefinitive() {
    var f = fresh();
    peer.reject = true;
    var request = frame(f, "source_register");
    var response = register(f, request);
    assertEquals("not_committed", response.path("result").asString(), response.toString());
    assertEquals("iam", response.path("failure_evidence").path("authority").asString());
    assertEquals("revision_mismatch", response.path("failure_evidence").path("reason").asString());
    assertNotNull(
        db(
            () ->
                jdbc.queryForObject(
                    "SELECT iam_response FROM collab.network_source_operation WHERE tenant_id=? AND operation_id=?",
                    byte[].class,
                    TENANT,
                    request.path("operation_id").asString())));
  }

  @Test
  void terminalFactsKeepNamedRevisionAndOmitValidityAndMissingPresenceFailsClosed() {
    var f = fresh();
    String id = binding(issue(f));
    peer.missingPresence = true;
    code("overloaded", get(f, id));
    peer.missingPresence = false;
    peer.wrongEcho = true;
    code("overloaded", get(f, id));
    peer.wrongEcho = false;
    peer.sources.compute(
        id,
        (k, fact) ->
            fact.toBuilder()
                .setState(NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_EXPIRED)
                .setRevision(2)
                .build());
    var expired = get(f, id);
    assertEquals("expired", expired.path("source").path("state").asString());
    assertEquals("2", expired.path("source").path("revision").asString());
    assertFalse(expired.has("valid_until"));
    peer.stopped.add(f.registration().path("registration_id").asString());
    var stopped = get(f, id);
    assertEquals("stopped", stopped.path("source").path("state").asString());
    assertFalse(stopped.has("valid_until"));
    peer.sources.compute(
        id,
        (k, fact) ->
            fact.toBuilder()
                .setState(NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_REVOKED)
                .setRevision(3)
                .build());
    var revoked = get(f, id);
    assertEquals("revoked", revoked.path("source").path("state").asString());
    assertFalse(revoked.has("valid_until"));
  }

  @Test
  void fullTypedLocalAndSavedNativeMappingAreComparedAndWitnessIdsCannotBeReassigned() {
    var f = fresh();
    String id = binding(issue(f));
    for (String field :
        List.of(
            "registration_id",
            "allocation_op_id",
            "allocation_fingerprint",
            "registration_revision",
            "local")) {
      var request = frame(f, "source_get").put("source_binding", id);
      ((ObjectNode) request.path("registration"))
          .put(
              field,
              switch (field) {
                case "allocation_fingerprint" -> "sha256:" + "b".repeat(64);
                case "registration_revision", "local" -> "2";
                default -> "other";
              });
      code("local".equals(field) ? "local_registration_required" : "conflict", get(f, request));
    }
    var request = frame(f, "source_get").put("source_binding", id);
    assertEquals("active", get(f, request).path("source").path("state").asString());
    ((ObjectNode) request.path("native_witness"))
        .put("valid_until", now().plusSeconds(29).toString());
    code("conflict", get(f, request));
    var mismatch = frame(f, "source_get").put("source_binding", id);
    ((ObjectNode) mismatch.path("native_witness")).put("binding_revision", "1");
    code("conflict", get(f, mismatch));
  }

  @Test
  void revokeOutboxSurvivesRestartAndOldTransportAndRevokesActiveSuccessorOfExpiredPredecessor() {
    var f = fresh();
    String first = binding(issue(f));
    peer.sources.compute(
        first,
        (k, fact) ->
            fact.toBuilder()
                .setState(NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_EXPIRED)
                .setRevision(2)
                .build());
    var successor =
        frame(f, "source_register").put("predecessor_binding", first).put("expected_revision", "2");
    String second = binding(register(f, successor));
    peer.loseRevoke = true;
    code("outcome_unknown", revoke(f, first));
    assertNull(
        db(
            () ->
                jdbc.queryForObject(
                    "SELECT valid_until FROM collab.network_source_binding WHERE tenant_id=? AND source_binding=?",
                    java.sql.Timestamp.class,
                    TENANT,
                    second)));
    authority.invalid.add(f.c().getIngress());
    peer.loseRevoke = false;
    var restarted = newWire();
    assertTrue(call(restarted::retrySourceRevocations) >= 1);
    assertEquals(f.c(), peer.lastRevoke.getConnection());
    assertEquals(
        NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_EXPIRED,
        peer.sources.get(first).getState());
    assertEquals(
        NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_REVOKED,
        peer.sources.get(second).getState());
  }

  @Test
  void revokeReservedSlotDoesNotWaitForGetAndLateResponseCannotCacheActive() throws Exception {
    var f = fresh();
    String id = binding(issue(f));
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    peer.afterGet =
        () -> {
          entered.countDown();
          await(release);
        };
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var pending = executor.submit(() -> get(f, id));
      assertTrue(entered.await(3, TimeUnit.SECONDS));
      code("overloaded", get(f, id));
      var revoked = revoke(f, id);
      assertTrue(revoked.path("lineage_stopped").asBoolean(), revoked.toString());
      release.countDown();
      var late = pending.get(5, TimeUnit.SECONDS);
      assertEquals("stopped", late.path("source").path("state").asString(), late.toString());
      assertFalse(late.has("valid_until"));
      assertNoActiveObservation(id);
      // A compiled broken implementation that omits the stop CAS demonstrably violates
      // the SAME PG invariant; this is a counterexample, not a production test switch.
      db(
          () -> {
            UnsafeActivation.activate(jdbc, TENANT, id);
            return null;
          });
      assertThrows(AssertionError.class, () -> assertNoActiveObservation(id));
      db(
          () ->
              jdbc.update(
                  "UPDATE collab.network_source_binding SET valid_until=NULL WHERE tenant_id=? AND source_binding=?",
                  TENANT,
                  id));
    } finally {
      release.countDown();
    }
  }

  static final class UnsafeActivation {
    static void activate(JdbcTemplate jdbc, UUID tenant, String binding) {
      jdbc.update(
          "UPDATE collab.network_source_binding SET valid_until=now()+interval '30 seconds' WHERE tenant_id=? AND source_binding=?",
          tenant,
          binding);
    }
  }

  static void assertNoActiveObservation(String id) {
    assertNull(
        db(
            () ->
                jdbc.queryForObject(
                    "SELECT valid_until FROM collab.network_source_binding WHERE tenant_id=? AND source_binding=?",
                    java.sql.Timestamp.class,
                    TENANT,
                    id)));
  }

  static void await(CountDownLatch latch) {
    try {
      assertTrue(latch.await(5, TimeUnit.SECONDS));
    } catch (InterruptedException e) {
      throw new AssertionError(e);
    }
  }

  @Test
  void crossNodeAndCrossTenantCannotReadOrRevokeKnownBindingOrOperation() {
    var f = fresh();
    var request = frame(f, "source_register");
    String id = binding(register(f, request));
    var other = fresh();
    code("unauthorized", get(other, id));
    code("unauthorized", revoke(other, id));
    var replay = frame(other, "source_register");
    replay.put("operation_id", request.path("operation_id").asString());
    code("unauthorized", register(other, replay));
    UUID foreignTenant = UUID.randomUUID();
    assertEquals(
        Integer.valueOf(0),
        TenantContext.call(
            foreignTenant,
            () ->
                new TransactionTemplate(manager)
                    .execute(
                        s ->
                            jdbc.queryForObject(
                                "SELECT count(*) FROM collab.network_source_binding WHERE source_binding=?",
                                Integer.class,
                                id))));
  }

  @Test
  void getNormalizesFractionalRequestBudgetBeforePersistingAndSigning() throws Exception {
    var f = fresh();
    String id = binding(issue(f));
    var original =
        db(
            () ->
                jdbc.queryForObject(
                    "SELECT creation_receipt FROM collab.network_source_binding WHERE tenant_id=? AND source_binding=?",
                    byte[].class,
                    TENANT,
                    id));
    int gets = peer.getCalls.get(),
        registers = peer.registerCalls.get(),
        reads = peer.readCalls.get();
    // As in the real-IAM integration harness, a fixed whole-second clock leaves the nano budget
    // cap just below the witness's whole-second +30s limit. The IAM fixture still returns UTC
    // seconds.
    var clock = Clock.fixed(now().plusSeconds(5), ZoneOffset.UTC);
    var ordinary = wire;
    var request = frame(f, "source_get").put("source_binding", id);
    ((ObjectNode) request.path("native_witness"))
        .put("observed_at", clock.instant().toString())
        .put("valid_until", clock.instant().plusSeconds(30).toString());
    try {
      wire =
          new NetworkMailboxWireService(
              new NetworkMailboxService(jdbc, manager, source, keys, clock, client),
              locals,
              jdbc,
              manager,
              source,
              keys,
              clock);
      var reply = get(f, request);
      assertEquals("source_get_result", reply.path("type").asString(), reply.toString());
      assertEquals("active", reply.path("source").path("state").asString());
      var until = Instant.parse(reply.path("valid_until").asString());
      assertEquals(0, until.getNano());
      assertTrue(until.isAfter(clock.instant()));
      assertFalse(until.isAfter(clock.instant().plusSeconds(30)));
      var saved =
          db(
              () ->
                  jdbc.queryForMap(
                      "SELECT * FROM collab.network_source_binding WHERE tenant_id=? AND source_binding=?",
                      TENANT,
                      id));
      assertEquals(java.sql.Timestamp.from(until), saved.get("valid_until"));
      assertArrayEquals(original, (byte[]) saved.get("creation_receipt"));
      var observation = GetAgentSourceResponse.parseFrom((byte[]) saved.get("observation"));
      assertEquals(f.c(), observation.getConnection());
      assertEquals(peer.sources.get(id), observation.getSource());
      assertEquals(gets + 1, peer.getCalls.get());
      assertEquals(registers, peer.registerCalls.get());
      assertEquals(reads, peer.readCalls.get());
    } finally {
      wire = ordinary;
    }
  }

  @Test
  void postRpcTypedLocalMutationAndWitnessExpiryCannotActivate() {
    var f = fresh();
    String id = binding(issue(f));
    peer.afterGet =
        () ->
            db(
                () ->
                    jdbc.update(
                        "UPDATE collab.network_local_registration SET status='retired' WHERE tenant_id=? AND id=?",
                        TENANT,
                        UUID.fromString(f.registration().path("registration_id").asString())));
    code("local_registration_required", get(f, id));
    assertNoActiveObservation(id);
    peer.afterGet = () -> {};
    var second = fresh();
    String secondId = binding(issue(second));
    var request = frame(second, "source_get").put("source_binding", secondId);
    ((ObjectNode) request.path("native_witness"))
        .put("observed_at", now().minusSeconds(31).toString())
        .put("valid_until", now().minusSeconds(1).toString());
    code("expired", get(second, request));
    assertNoActiveObservation(secondId);
  }
}
