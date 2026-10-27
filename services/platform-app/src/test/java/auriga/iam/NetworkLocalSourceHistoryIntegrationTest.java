package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.*;
import auriga.collab.grpc.*;
import auriga.collab.network.local.LocalRegistry;
import auriga.common.audit.AuditLogger;
import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.grpc.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.collab.v1.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority.*;
import auriga.testsupport.PostgresExtension;
import com.google.protobuf.ByteString;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import io.grpc.*;
import io.grpc.stub.StreamObserver;
import java.nio.charset.StandardCharsets;
import java.security.KeyPairGenerator;
import java.sql.Timestamp;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.function.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.dao.DataAccessException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.namedparam.NamedParameterJdbcTemplate;
import org.springframework.transaction.support.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Real IAM OAuth/device/session, redeem, Resolve/Read and collab source wire on restricted PG
 * roles. Assembly adapted from NetworkConnectionAnchorPostgresTest and
 * NetworkSourceControlPostgresTest; no other module's test classes are imported. This package
 * permits existing IAM constructors. Account/provider provisioning and SessionManager.create are
 * the trusted login-completion boundary; org permissions, keys, clock, audit/KV sinks and
 * explicitly named transport faults are fixtures. This does not exercise browser OAuth, gateway
 * TLS, NATS/WSS, plugins or SEND.
 */
@ExtendWith(PostgresExtension.class)
class NetworkLocalSourceHistoryIntegrationTest {
  static IamStore store;
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static PostgresExtension.ServiceRoles iamRoles, collabRoles;
  static Harness legacy;
  static ObjectNode legacyFrame;
  Harness h;

  static void migrate(PostgresExtension.ServiceRoles roles, String schema, String target) {
    var config =
        Flyway.configure()
            .dataSource(roles.owner().dataSource())
            .schemas(schema)
            .defaultSchema(schema)
            .locations("classpath:db/" + schema);
    if (target != null) config.target(target);
    CommonMigrations.serviceFlyway(config.load()).migrate();
  }

  @BeforeAll
  static void database() throws Exception {
    iamRoles =
        PostgresExtension.serviceRoles(
            NetworkLocalSourceHistoryIntegrationTest.class,
            "history_iam_owner",
            "history_iam_app",
            "iam");
    collabRoles =
        PostgresExtension.serviceRoles(
            NetworkLocalSourceHistoryIntegrationTest.class,
            "history_collab_owner",
            "history_collab_app",
            "collab");
    CommonMigrations.migrate(iamRoles.owner().dataSource(), "iam");
    migrate(iamRoles, "iam", null);
    CommonMigrations.migrate(collabRoles.owner().dataSource(), "collab");
    migrate(collabRoles, "collab", "14");
    store =
        new IamStore(
            new JdbcTemplate(iamRoles.runtime().dataSource()),
            new TenantTransactionManager(iamRoles.runtime().dataSource()));
    jdbc = new JdbcTemplate(collabRoles.runtime().dataSource());
    manager = new TenantTransactionManager(collabRoles.runtime().dataSource());
    legacy = new Harness();
    // Other tests run the real global ingress sweep at +3601s. Keep this independent legacy
    // fixture's injected clock ahead so its same-live-C1 assertion cannot depend on test order.
    legacy.clock.advance(7200);
    try {
      legacy.start();
      legacyFrame = legacy.failureFrame("reconcile");
      var body = business(legacyFrame);
      var result = failureResult(legacyFrame, NetworkJson.digest(body));
      legacy.db(
          () ->
              jdbc.update(
                  """
          INSERT INTO collab.network_source_operation
          (id,tenant_id,method,operation_id,realm,node_id,business_json,request_digest,
          original_frame,original_connection,epoch,boot_id,status,result_json)
          VALUES (?,?,'Register',?,?,?,?,?,?,?,1,?,'not_accepted',?)
          """,
                  UuidV7.next(),
                  legacy.tenant,
                  text(legacyFrame, "operation_id"),
                  legacy.c.getRealm(),
                  legacy.c.getNode(),
                  NetworkJson.canonical(body),
                  NetworkJson.digest(body),
                  bytes(legacyFrame),
                  legacy.c.toByteArray(),
                  legacy.boot,
                  NetworkJson.canonical(result)));
      migrate(collabRoles, "collab", null);
    } catch (Exception | Error failure) {
      legacy.close();
      throw failure;
    }
  }

  @BeforeEach
  void start() throws Exception {
    h = new Harness();
    try {
      h.start();
    } catch (Exception | Error failure) {
      h.close();
      throw failure;
    }
  }

  @AfterEach
  void stop() throws Exception {
    if (h != null) h.close();
  }

  @AfterAll
  static void stopLegacy() throws Exception {
    if (legacy != null) legacy.close();
  }

  static final class Time extends Clock {
    volatile Instant now = Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);

    void advance(long seconds) {
      now = now.plusSeconds(seconds);
    }

    public Instant instant() {
      return now;
    }

    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(ZoneId zone) {
      return this;
    }
  }

  static final class Harness implements AutoCloseable {
    final UUID tenant = UuidV7.next();
    final Time clock = new Time();
    UUID owner;
    String sid, did, access, refresh, boot, epoch = "1";
    long clientId = 40;
    final AtomicBoolean dropReply = new AtomicBoolean();
    NetworkAuthorityConnection c;
    ObjectNode registration, readyFrame;
    TokenService tokens;
    SessionManager sessions;
    OAuthProvider oauth;
    IamNetworkAuthority network;
    AccessTokenVerifier verifier;
    ObservedAuthority observed;
    ObservedSources observedSources;
    volatile Runnable beforeSourceOrg = () -> {};
    GrpcServer iamServer, mailboxServer;
    ManagedChannel iamChannel, mailboxChannel;
    NetworkAuthorityClient client;
    NetworkSourceGrpcAuthority source;
    NetworkSigningKeys signing;
    LocalRegistry locals;
    NetworkMailboxWireService wire;
    String collabSecret = Secrets.random(), gatewaySecret = Secrets.random();

    <T> T tx(Supplier<T> work) {
      return store.tenant(tenant, work);
    }

    <T> T db(Supplier<T> work) {
      return TenantContext.call(
          tenant, () -> new TransactionTemplate(manager).execute(s -> work.get()));
    }

    <T> T gateway(Supplier<T> work) {
      // Verify an actually issued current service JWT at this explicit in-process gateway boundary.
      return RequestContext.start(null, null)
          .withPrincipal(verifier.verify(tokens.issue("ai-gateway", gatewaySecret)))
          .call(() -> TenantContext.call(tenant, work::get));
    }

    void security() throws Exception {
      var key = new ECKeyGenerator(Curve.P_256).keyID(UuidV7.next().toString()).generate();
      var keys = mock(SigningKeys.class);
      when(keys.current()).thenReturn(key);
      when(keys.jwks()).thenReturn(Json.object("keys", List.of(key.toPublicJWK().toJSONObject())));
      tokens =
          new TokenService(
              keys,
              new ContractSchemas(),
              (t, s, c) -> Set.of("auriga.connect", "auriga.connect.collab.use"),
              clock,
              "https://portal.example.com",
              Map.of(
                  "collab", Secrets.hash(collabSecret), "ai-gateway", Secrets.hash(gatewaySecret)));
      var settings =
          new AccessTokenSettings(
              tokens.issuer(),
              java.net.URI.create(tokens.issuer() + "/oauth2/jwks"),
              null,
              null,
              null,
              null,
              clock,
              false);
      verifier =
          new AccessTokenVerifier(
              AccessTokenDecoders.createInternal(
                  settings,
                  new com.nimbusds.jose.jwk.source.ImmutableJWKSet<
                      com.nimbusds.jose.proc.SecurityContext>(new JWKSet(key.toPublicJWK()))));
    }

    void start() throws Exception {
      security();
      sessions =
          new SessionManager(
              store,
              mock(SessionKv.class),
              tokens,
              clock,
              mock(EventPublisher.class),
              mock(AuditLogger.class));
      oauth =
          new OAuthProvider(
              store,
              sessions,
              tokens,
              clock,
              List.of(
                  new OAuthClient(
                      "history-device",
                      tenant,
                      null,
                      Set.of(),
                      Set.of("auriga.connect"),
                      Set.of(OAuthProvider.DEVICE, "refresh_token"),
                      true)));
      var devices = new DeviceAccessAuthority(store, sessions, tokens);
      network =
          new IamNetworkAuthority(
              store, devices, mock(AuditLogger.class), clock, Set.of("server-a"));
      var sourceAuthority =
          new IamNetworkSourceAuthority(
              store,
              network,
              devices,
              r -> {
                store.requireTransactionReleased();
                beforeSourceOrg.run();
                assertEquals(tenant.toString(), r.getTenantId());
                return auriga.contracts.org.v1.GetEffectivePermissionsResponse.newBuilder()
                    .setMemberTypeValue(1)
                    .setStatusValue(1)
                    .addPermissions("auriga.connect.collab.use")
                    .build();
              },
              clock,
              new IamNetworkSourceAuthority.Policy(180, 30));
      connectIam(network, sourceAuthority);
      source = new NetworkSourceGrpcAuthority(client, manager);
      owner = account();
      sid = session(owner);
      var credential = credential(sid);
      did = text(tokens.verify(text(credential, "access_token")), "did");
      useCredential(credential);
      var node = TenantContext.call(tenant, () -> network.register(access));
      c = redeem(node.node(), access, did);
      reference(c);
      signing = new NetworkSigningKeys(clock, 1);
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      signing.add(
          c.getRealm(),
          "protocol_response",
          "history-key",
          pair.getPublic(),
          pair.getPrivate(),
          clock.instant().minusSeconds(30),
          clock.instant().plusSeconds(20000),
          NetworkSigningKeys.State.ACTIVE);
      locals = new LocalRegistry(jdbc, manager, source, client, clock);
      assemble(jdbc, manager);
      ready(false);
      var local =
          object()
              .put("app_version", "1.0")
              .put("type", "local_register")
              .put("request_id", "local-" + UuidV7.next())
              .put("epoch", epoch)
              .put("boot_id", boot)
              .put("candidate_local", "1")
              .put("address_type", "session")
              .put("allocation_op_id", "allocation-" + UuidV7.next())
              .put("allocation_fingerprint", "sha256:" + "a".repeat(64))
              .put("expected_registration_revision", "0");
      var registered = NetworkJson.parse(gateway(() -> wire.localRegister(c, bytes(local))));
      assertEquals("registered", registered.path("result").asString(), registered.toString());
      registration = object();
      for (String field :
          List.of(
              "registration_id",
              "allocation_op_id",
              "registration_revision",
              "local",
              "address_type")) registration.set(field, registered.path(field));
      registration.put("allocation_fingerprint", "sha256:" + "a".repeat(64));
    }

    void connectIam(IamNetworkAuthority authority, IamNetworkSourceAuthority sources) {
      observed = new ObservedAuthority(authority, clock, sources);
      observedSources = new ObservedSources(sources);
      iamServer =
          new GrpcServer(
              "127.0.0.1",
              0,
              List.of(observed, observedSources),
              new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:collab")),
              null,
              false,
              Set.of());
      iamServer.start();
      iamChannel =
          GrpcChannels.open(
              "127.0.0.1:" + iamServer.port(),
              new AurigaClientInterceptor(
                  () -> tokens.issue("collab", collabSecret), Duration.ofSeconds(5)));
      client = new NetworkAuthorityClient(iamChannel, Duration.ofSeconds(5));
    }

    void assemble(JdbcTemplate reads, TenantTransactionManager transactions) {
      wire =
          new NetworkMailboxWireService(
              new NetworkMailboxService(reads, transactions, source, signing, clock, client),
              locals,
              reads,
              transactions,
              source,
              signing,
              clock);
    }

    void mailbox() {
      if (mailboxChannel != null) {
        mailboxChannel.shutdownNow();
        assertDoesNotThrow(() -> assertTrue(mailboxChannel.awaitTermination(5, TimeUnit.SECONDS)));
      }
      if (mailboxServer != null) mailboxServer.stop();
      var service =
          new NetworkMailboxGrpcService(wire) {
            @Override
            @AuthenticatedOnly
            public void sourceRegister(
                NetworkMailboxServiceSourceRegisterRequest request,
                StreamObserver<NetworkMailboxServiceSourceRegisterResponse> observer) {
              boolean lose = dropReply.getAndSet(false);
              super.sourceRegister(
                  request,
                  new StreamObserver<>() {
                    public void onNext(NetworkMailboxServiceSourceRegisterResponse result) {
                      if (!lose) observer.onNext(result);
                    }

                    public void onError(Throwable error) {
                      observer.onError(error);
                    }

                    public void onCompleted() {
                      if (lose) observer.onError(Status.UNAVAILABLE.asRuntimeException());
                      else observer.onCompleted();
                    }
                  });
            }
          };
      mailboxServer =
          new GrpcServer(
              "127.0.0.1",
              0,
              List.of(service),
              new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:ai-gateway")),
              null,
              false,
              Set.of());
      mailboxServer.start();
      mailboxChannel =
          GrpcChannels.open(
              "127.0.0.1:" + mailboxServer.port(),
              new AurigaClientInterceptor(
                  () -> tokens.issue("ai-gateway", gatewaySecret), Duration.ofSeconds(10)));
    }

    UUID account() {
      UUID id = UuidV7.next();
      tx(() -> store.insert("account", id.toString(), Json.object("status", "active"), null));
      return id;
    }

    String session(UUID account) {
      return tx(
          () -> {
            UUID provider = UuidV7.next();
            store.insert("idp_provider", provider.toString(), Json.object(), null);
            return sessions
                .create(account, provider, "portal", true, Duration.ofHours(4), Duration.ofHours(4))
                .session()
                .path("sessionId")
                .asString();
          });
    }

    ObjectNode credential(String session) {
      return TenantContext.call(
          tenant,
          () -> {
            var grant =
                oauth.deviceAuthorization(
                    Map.of("client_id", "history-device", "scope", "auriga.connect"));
            oauth.decideDevice(text(grant, "user_code"), session, true);
            var result =
                oauth.token(
                    Map.of(
                        "grant_type",
                        OAuthProvider.DEVICE,
                        "client_id",
                        "history-device",
                        "device_code",
                        text(grant, "device_code")));
            assertTrue(result.has("access_token"), result.toString());
            assertTrue(result.has("refresh_token"));
            return result;
          });
    }

    void useCredential(ObjectNode result) {
      access = text(result, "access_token");
      refresh = text(result, "refresh_token");
    }

    void refresh() {
      var result =
          TenantContext.call(
              tenant,
              () ->
                  oauth.token(
                      Map.of(
                          "grant_type",
                          "refresh_token",
                          "client_id",
                          "history-device",
                          "refresh_token",
                          refresh)));
      assertTrue(result.has("access_token"), result.toString());
      useCredential(result);
      assertEquals(did, text(tokens.verify(access), "did"));
    }

    NetworkAuthorityConnection redeem(String node, String token, String device) {
      return TenantContext.call(
          tenant,
          () -> {
            var ticket = network.prepare(token, node, "server-a");
            var binding =
                new IngressBinding(
                    "N" + ((++clientId % 2) == 0 ? "B" : "C").repeat(55),
                    Long.toString(clientId),
                    "U" + "A".repeat(55),
                    IamNetworkAuthority.ingressForTicket(ticket.ticket()),
                    "BROKER-" + clientId);
            gateway(() -> network.redeem(ticket.ticket(), node, device, "server-a", binding));
            return NetworkAuthorityConnection.newBuilder()
                .setTenantId(tenant.toString())
                .setRealm(ticket.identity().realm())
                .setNode(node)
                .setIngress(binding.ingress())
                .setServerId(binding.serverId())
                .setClientId(binding.clientId())
                .setClientAccount(binding.account())
                .build();
          });
    }

    NetworkConnectionAnchor reference(NetworkAuthorityConnection connection) {
      var reply =
          TenantContext.call(
              tenant,
              () ->
                  NetworkAuthorityServiceGrpc.newBlockingStub(iamChannel)
                      .withDeadlineAfter(5, TimeUnit.SECONDS)
                      .resolveNetworkTarget(
                          ResolveNetworkTargetRequest.newBuilder()
                              .setConnection(connection)
                              .setLocal("0")
                              .build()));
      assertTrue(reply.getAllowed(), "new C2 must really Resolve allowed=true");
      assertEquals(connection, reply.getConnection());
      assertTrue(reply.hasConnectionAnchor());
      var anchor = reply.getConnectionAnchor();
      assertEquals(connection, anchor.getConnection());
      tx(
          () -> {
            var row =
                store
                    .networkJdbc()
                    .queryForMap(
                        "SELECT * FROM iam.network_connection_anchor WHERE tenant_id=? AND id=?",
                        tenant,
                        UUID.fromString(anchor.getAnchorId()));
            assertArrayEquals(connection.toByteArray(), (byte[]) row.get("connection"));
            assertEquals(owner, row.get("owner_id"));
            assertEquals(UUID.fromString(did), row.get("device_id"));
            assertEquals(UUID.fromString(sid), row.get("session_id"));
            return null;
          });
      return anchor;
    }

    void reconnect() {
      refresh();
      c = redeem(c.getNode(), access, did);
      reference(c);
      ready(true);
    }

    void ready(boolean retained) {
      boot = "boot-" + UuidV7.next();
      readyFrame =
          object()
              .put("app_version", "1.0")
              .put("type", "ready")
              .put("request_id", "ready-" + UuidV7.next())
              .put("channel_nonce", "nonce-" + UuidV7.next())
              .put("boot_id", boot)
              .put("keyset_version", "1")
              .putNull("pending_registration")
              .put("resume_cursor", "0")
              .put("local_high_water", retained ? "1" : "0")
              .put("registration_revision", retained ? "1" : "0");
      for (String field : List.of("features", "adapter_capabilities"))
        readyFrame
            .putArray(field)
            .add("server_auth_v1")
            .add("source_attestation_v1")
            .add("inbox_only_v1");
      readyFrame
          .putObject("receipt_checkpoint")
          .put("checkpoint_seq", "0")
          .put("checkpoint_revision", "0")
          .put("summary_digest", "sha256:" + "a".repeat(64));
      var reply = NetworkJson.parse(gateway(() -> wire.ready(c, bytes(readyFrame))));
      assertEquals("ready", text(reply, "status"), reply.toString());
      epoch = text(reply, "epoch");
    }

    ObjectNode frame() {
      var frame =
          object()
              .put("app_version", "1.0")
              .put("type", "source_register")
              .put("request_id", "request-" + UuidV7.next())
              .put("operation_id", "op-" + UuidV7.next())
              .put("epoch", epoch)
              .put("boot_id", boot)
              .put("mode", "issue")
              .put("scope", "durable_unicast_send")
              .put("expected_revision", "0")
              .put("predecessor_binding", "");
      frame.set("registration", registration.deepCopy());
      frame
          .putObject("native_witness")
          .put("witness_id", "w-" + UuidV7.next())
          .put("binding_id", "native-" + c.getNode())
          .put("binding_revision", "1")
          .put("observed_at", clock.instant().toString())
          .put("valid_until", clock.instant().plusSeconds(30).toString());
      return frame;
    }

    ObjectNode failureFrame(String path) {
      var result = frame();
      if (path.equals("reconcile")) result.put("mode", "reconcile").putNull("native_witness");
      else
        ((ObjectNode) result.path("native_witness"))
            .put("valid_until", clock.instant().minusSeconds(1).toString());
      return result;
    }

    ObjectNode retry(ObjectNode original) {
      return original
          .deepCopy()
          .put("mode", "reconcile")
          .putNull("native_witness")
          .put("epoch", epoch)
          .put("boot_id", boot)
          .put("request_id", "retry-" + UuidV7.next());
    }

    JsonNode register(ObjectNode frame) {
      if (mailboxServer == null) mailbox();
      return TenantContext.call(
          tenant,
          () ->
              NetworkJson.parse(
                  NetworkMailboxServiceGrpc.newBlockingStub(mailboxChannel)
                      .withDeadlineAfter(10, TimeUnit.SECONDS)
                      .sourceRegister(
                          NetworkMailboxServiceSourceRegisterRequest.newBuilder()
                              .setConnection(
                                  NetworkMailboxConnection.newBuilder()
                                      .setTenantId(c.getTenantId())
                                      .setRealm(c.getRealm())
                                      .setNode(c.getNode())
                                      .setIngress(c.getIngress())
                                      .setServerId(c.getServerId())
                                      .setClientId(c.getClientId())
                                      .setClientAccount(c.getClientAccount()))
                              .setFrameJson(ByteString.copyFrom(bytes(frame)))
                              .build())
                      .getFrameJson()
                      .toByteArray()));
    }

    Map<String, Object> row(ObjectNode frame) {
      return db(
          () ->
              jdbc.queryForMap(
                  "SELECT * FROM collab.network_source_operation WHERE tenant_id=? AND operation_id=?",
                  tenant,
                  text(frame, "operation_id")));
    }

    ObjectNode getOrRevokeFrame(String binding, boolean revoke) {
      var request =
          object()
              .put("app_version", "1.0")
              .put("type", revoke ? "source_revoke" : "source_get")
              .put("request_id", "request-" + UuidV7.next())
              .put("epoch", epoch)
              .put("boot_id", boot)
              .put("source_binding", binding);
      if (revoke) request.put("operation_id", "revoke-" + UuidV7.next());
      else {
        request.set("registration", registration.deepCopy());
        request.set("native_witness", frame().path("native_witness"));
      }
      return request;
    }

    JsonNode getOrRevoke(String binding, boolean revoke) {
      if (mailboxServer == null) mailbox();
      var connection =
          NetworkMailboxConnection.newBuilder()
              .setTenantId(c.getTenantId())
              .setRealm(c.getRealm())
              .setNode(c.getNode())
              .setIngress(c.getIngress())
              .setServerId(c.getServerId())
              .setClientId(c.getClientId())
              .setClientAccount(c.getClientAccount());
      var request = getOrRevokeFrame(binding, revoke);
      LocalHistoryTestAccess.validateSourceFrame(revoke ? "SourceRevoke" : "SourceGet", request);
      return TenantContext.call(
          tenant,
          () -> {
            var stub =
                NetworkMailboxServiceGrpc.newBlockingStub(mailboxChannel)
                    .withDeadlineAfter(10, TimeUnit.SECONDS);
            var data = ByteString.copyFrom(bytes(request));
            return NetworkJson.parse(
                (revoke
                        ? stub.sourceRevoke(
                                NetworkMailboxServiceSourceRevokeRequest.newBuilder()
                                    .setConnection(connection)
                                    .setFrameJson(data)
                                    .build())
                            .getFrameJson()
                        : stub.sourceGet(
                                NetworkMailboxServiceSourceGetRequest.newBuilder()
                                    .setConnection(connection)
                                    .setFrameJson(data)
                                    .build())
                            .getFrameJson())
                    .toByteArray());
          });
    }

    void mutate(String table, String id, String field, Object value) {
      tx(
          () -> {
            var row = store.byId(table, id, true);
            assertNotNull(row);
            row.set(field, Json.MAPPER.valueToTree(value));
            store.save(table, row);
            return null;
          });
      assertEquals(
          Json.MAPPER.valueToTree(value), tx(() -> store.byId(table, id, false)).path(field));
    }

    Map<String, List<String>> facts() {
      var result = new TreeMap<String, List<String>>();
      tx(
          () -> {
            for (String table :
                List.of(
                    "network_connection_anchor",
                    "network_source_anchor",
                    "network_source",
                    "network_source_operation",
                    "network_source_chain"))
              result.put(
                  "iam." + table,
                  store
                      .networkJdbc()
                      .queryForList(
                          "SELECT jsonb_build_object('row',to_jsonb(t),'xmin',t.xmin::text)::text FROM iam."
                              + table
                              + " t ORDER BY to_jsonb(t)::text",
                          String.class));
            return null;
          });
      db(
          () -> {
            for (String table :
                List.of(
                    "network_source_operation",
                    "network_source_lineage",
                    "network_source_binding",
                    "network_source_outbox"))
              result.put(
                  "collab." + table,
                  jdbc.queryForList(
                      "SELECT jsonb_build_object('row',to_jsonb(t),'xmin',t.xmin::text)::text FROM collab."
                          + table
                          + " t ORDER BY to_jsonb(t)::text",
                      String.class));
            return null;
          });
      return result;
    }

    void noSourceRpc() {
      assertEquals(0, observedSources.register.get(), "RegisterAgentSource");
      assertEquals(0, observedSources.read.get(), "ReadAgentSourceReceipt");
      assertEquals(0, observedSources.get.get(), "GetAgentSource");
      assertEquals(0, observedSources.fence.get(), "FenceAgentSourceReceipt");
    }

    public void close() throws Exception {
      assertAll(
          "close every owned transport even if another close fails",
          () -> {
            if (mailboxChannel != null) {
              mailboxChannel.shutdownNow();
              assertTrue(mailboxChannel.awaitTermination(5, TimeUnit.SECONDS));
            }
          },
          () -> {
            if (iamChannel != null) {
              iamChannel.shutdownNow();
              assertTrue(iamChannel.awaitTermination(5, TimeUnit.SECONDS));
            }
          },
          () -> {
            if (mailboxServer != null) mailboxServer.stop();
          },
          () -> {
            if (iamServer != null) iamServer.stop();
          },
          () -> {
            if (signing != null) signing.close();
          });
    }
  }

  static class ObservedAuthority extends IamGrpcNetworkAuthority {
    final AtomicInteger reads = new AtomicInteger(), resolved = new AtomicInteger();
    volatile Runnable before = () -> {}, after = () -> {};
    volatile Status failure;
    volatile boolean holdUntilCancelled;
    volatile UnaryOperator<ReadNetworkConnectionAnchorResponse> echo = UnaryOperator.identity();
    volatile UnaryOperator<ResolveNetworkTargetResponse> resolveEcho = UnaryOperator.identity();
    volatile ReadNetworkConnectionAnchorRequest last;
    volatile Runnable beforeCheck = () -> {};
    volatile boolean dropCheckResponse;

    ObservedAuthority(IamNetworkAuthority network, Clock clock, IamNetworkSourceAuthority sources) {
      super(network, clock, sources);
    }

    @Override
    @AuthenticatedOnly
    public void checkNetwork(
        CheckNetworkRequest request, StreamObserver<CheckNetworkResponse> observer) {
      beforeCheck.run();
      if (!dropCheckResponse) super.checkNetwork(request, observer);
    }

    @Override
    @AuthenticatedOnly
    public void resolveNetworkTarget(
        ResolveNetworkTargetRequest request,
        StreamObserver<ResolveNetworkTargetResponse> observer) {
      super.resolveNetworkTarget(
          request,
          new StreamObserver<>() {
            public void onNext(ResolveNetworkTargetResponse value) {
              if (value.getAllowed()) resolved.incrementAndGet();
              observer.onNext(resolveEcho.apply(value));
            }

            public void onError(Throwable error) {
              observer.onError(error);
            }

            public void onCompleted() {
              observer.onCompleted();
            }
          });
    }

    @Override
    @AuthenticatedOnly
    public void readNetworkConnectionAnchor(
        ReadNetworkConnectionAnchorRequest request,
        StreamObserver<ReadNetworkConnectionAnchorResponse> observer) {
      reads.incrementAndGet();
      last = request;
      before.run();
      if (failure != null) {
        observer.onError(failure.asRuntimeException());
        return;
      }
      super.readNetworkConnectionAnchor(
          request,
          new StreamObserver<>() {
            public void onNext(ReadNetworkConnectionAnchorResponse value) {
              after.run();
              if (!holdUntilCancelled) observer.onNext(echo.apply(value));
            }

            public void onError(Throwable error) {
              observer.onError(error);
            }

            public void onCompleted() {
              if (!holdUntilCancelled) observer.onCompleted();
            }
          });
    }
  }

  static class ObservedSources extends IamGrpcNetworkSources {
    final AtomicInteger register = new AtomicInteger(),
        read = new AtomicInteger(),
        get = new AtomicInteger(),
        revoke = new AtomicInteger(),
        fence = new AtomicInteger();
    volatile boolean dropRegisterBeforeCommit, dropRegisterReply;
    volatile Status readFailure, fenceFailure;
    volatile UnaryOperator<RegisterAgentSourceResponse> readEcho = UnaryOperator.identity(),
        fenceEcho = UnaryOperator.identity();
    volatile Runnable afterRead = () -> {}, beforeFence = () -> {}, afterFence = () -> {};
    volatile RegisterAgentSourceRequest lastRegister, lastRead, lastFence;
    volatile RegisterAgentSourceResponse lastFenceReply;
    volatile GetAgentSourceRequest lastGet;
    volatile GetAgentSourceResponse lastGetReply;
    volatile Throwable lastGetError;

    ObservedSources(IamNetworkSourceAuthority authority) {
      super(authority);
    }

    @Override
    @AuthenticatedOnly
    public void registerAgentSource(
        RegisterAgentSourceRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
      register.incrementAndGet();
      lastRegister = r;
      if (dropRegisterBeforeCommit) {
        o.onError(Status.UNAVAILABLE.asRuntimeException());
        return;
      }
      super.registerAgentSource(
          r,
          new StreamObserver<>() {
            public void onNext(RegisterAgentSourceResponse value) {
              if (!dropRegisterReply) o.onNext(value);
            }

            public void onError(Throwable error) {
              o.onError(error);
            }

            public void onCompleted() {
              if (dropRegisterReply) o.onError(Status.UNAVAILABLE.asRuntimeException());
              else o.onCompleted();
            }
          });
    }

    @Override
    @AuthenticatedOnly
    public void readAgentSourceReceipt(
        ReadAgentSourceReceiptRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
      read.incrementAndGet();
      lastRead = r.getOriginalRequest();
      if (readFailure != null) {
        afterRead.run();
        o.onError(readFailure.asRuntimeException());
        return;
      }
      try {
        super.readAgentSourceReceipt(
            r,
            new StreamObserver<>() {
              public void onNext(RegisterAgentSourceResponse value) {
                afterRead.run();
                o.onNext(readEcho.apply(value));
              }

              public void onError(Throwable error) {
                afterRead.run();
                o.onError(error);
              }

              public void onCompleted() {
                o.onCompleted();
              }
            });
      } catch (RuntimeException error) {
        afterRead.run();
        throw error;
      }
    }

    @Override
    @AuthenticatedOnly
    public void fenceAgentSourceReceipt(
        FenceAgentSourceReceiptRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
      fence.incrementAndGet();
      lastFence = r.getOriginalRequest();
      beforeFence.run();
      if (fenceFailure != null) {
        o.onError(fenceFailure.asRuntimeException());
        return;
      }
      super.fenceAgentSourceReceipt(
          r,
          new StreamObserver<>() {
            public void onNext(RegisterAgentSourceResponse value) {
              lastFenceReply = value;
              afterFence.run();
              o.onNext(fenceEcho.apply(value));
            }

            public void onError(Throwable error) {
              o.onError(error);
            }

            public void onCompleted() {
              o.onCompleted();
            }
          });
    }

    @Override
    @AuthenticatedOnly
    public void getAgentSource(GetAgentSourceRequest r, StreamObserver<GetAgentSourceResponse> o) {
      get.incrementAndGet();
      lastGet = r;
      try {
        super.getAgentSource(
            r,
            new StreamObserver<>() {
              public void onNext(GetAgentSourceResponse value) {
                lastGetReply = value;
                o.onNext(value);
              }

              public void onError(Throwable error) {
                lastGetError = error;
                o.onError(error);
              }

              public void onCompleted() {
                o.onCompleted();
              }
            });
      } catch (RuntimeException error) {
        lastGetError = error;
        throw error;
      }
    }

    @Override
    @AuthenticatedOnly
    public void revokeAgentSource(
        RevokeAgentSourceRequest r, StreamObserver<RevokeAgentSourceResponse> o) {
      revoke.incrementAndGet();
      super.revokeAgentSource(r, o);
    }
  }

  static String text(JsonNode value, String field) {
    return value.path(field).asString();
  }

  static ObjectNode object() {
    return (ObjectNode) NetworkJson.parse("{}");
  }

  static byte[] bytes(JsonNode value) {
    return NetworkJson.canonical(value).getBytes(StandardCharsets.UTF_8);
  }

  static ObjectNode business(JsonNode frame) {
    var body = object();
    for (String field :
        List.of("registration", "scope", "expected_revision", "predecessor_binding"))
      body.set(field, frame.path(field));
    return body;
  }

  static ObjectNode failureResult(ObjectNode frame, String digest) {
    var result =
        object()
            .put("app_version", "1.0")
            .put("type", "source_register_result")
            .put("operation_id", text(frame, "operation_id"))
            .put("result", "not_accepted")
            .putNull("source");
    result
        .putObject("failure_evidence")
        .put("authority", "collab")
        .put("reason", "rejected_before_pending")
        .put("request_digest", digest);
    return result;
  }

  static void code(String code, JsonNode reply) {
    assertEquals("error", text(reply, "type"), reply.toString());
    assertEquals("network." + code, text(reply, "code"), reply.toString());
    assertFalse(reply.has("result"));
    assertFalse(reply.has("failure_evidence"));
    assertFalse(reply.has("source"));
  }

  static void sameRow(Map<String, Object> expected, Map<String, Object> actual) {
    assertEquals(expected.keySet(), actual.keySet());
    expected.forEach(
        (k, v) -> {
          if (v instanceof byte[] b) assertArrayEquals(b, (byte[]) actual.get(k), k);
          else assertEquals(v, actual.get(k), k);
        });
  }

  void recovered(Map<String, Object> saved, ObjectNode retry, JsonNode reply) {
    assertEquals("not_accepted", text(reply, "result"), reply.toString());
    var result = NetworkJson.parse(saved.get("result_json").toString());
    for (String key : List.of("operation_id", "result", "failure_evidence", "source"))
      assertEquals(result.path(key), reply.path(key), key);
    assertEquals(
        saved.get("request_digest"),
        reply.path("failure_evidence").path("request_digest").asString());
    assertEquals(
        text(retry, "request_id"), reply.path("server_auth").path("request_id").asString());
    assertFalse(reply.path("server_auth").path("signature").asString().isEmpty());
    assertEquals(h.epoch, reply.path("server_auth").path("audience").path("epoch").asString());
    assertEquals(
        h.readyFrame.path("channel_nonce"), reply.path("server_auth").path("channel_nonce"));
    assertDoesNotThrow(
        () -> {
          var unsigned = (ObjectNode) reply.deepCopy();
          String signature = unsigned.path("server_auth").path("signature").asString();
          ((ObjectNode) unsigned.path("server_auth")).remove("signature");
          var verifier = java.security.Signature.getInstance("Ed25519");
          verifier.initVerify(h.signing.publicKey("history-key"));
          verifier.update("Auriga.ServerEnvelope.v1\0".getBytes(StandardCharsets.UTF_8));
          verifier.update(bytes(unsigned));
          assertTrue(verifier.verify(Base64.getUrlDecoder().decode(signature)));
        });
  }

  @ParameterizedTest
  @ValueSource(strings = {"invalid", "reconcile"})
  void lostLocalReplyRecoversThroughRealC2AfterExpiredC1IsSwept(String path) throws Exception {
    var original = h.c;
    var request = h.failureFrame(path);
    h.dropReply.set(true);
    var lost = assertThrows(StatusRuntimeException.class, () -> h.register(request));
    assertEquals(Status.Code.UNAVAILABLE, lost.getStatus().getCode());
    var saved = h.row(request);
    assertEquals("not_accepted", saved.get("status"));
    assertNull(saved.get("iam_request"));
    assertNull(saved.get("lineage_id"));
    assertEquals(1, saved.get("local_history_version"));
    assertEquals(0, h.observed.reads.get());
    // The real session policy allows refresh; no old absolute deadline or stale service JWT is
    // reused.
    h.clock.advance(3500);
    h.reconnect();
    h.clock.advance(101);
    assertFalse(h.tx(() -> store.get("network_ingress", original.getIngress(), false)) == null);
    var cleanup = new NetworkIngressCleanup(store, h.clock);
    long tenants =
        store
            .networkJdbc()
            .queryForObject("SELECT count(*) FROM iam.network_ingress_tenant", Long.class);
    for (long i = 0; i < ((tenants + 31) / 32) + 1; i++) {
      cleanup.sweepOnce();
      if (h.tx(() -> store.get("network_ingress", original.getIngress(), false)) == null) break;
    }
    assertNull(h.tx(() -> store.get("network_ingress", original.getIngress(), false)));
    h.reference(h.c);
    var before = h.facts();
    var retry = h.retry(request);
    recovered(saved, retry, h.register(retry));
    assertEquals(1, h.observed.reads.get());
    assertEquals(original, h.observed.last.getOriginal().getConnection());
    assertEquals(h.c, h.observed.last.getCurrent().getConnection());
    sameRow(saved, h.row(request));
    assertEquals(before, h.facts());
    h.noSourceRpc();
  }

  @Test
  void sourceLifecyclePositiveControlUsesRealIamPersistenceAndAllThreeWireEntrypoints() {
    var receipt = h.register(h.frame());
    assertEquals("registered", text(receipt, "result"), receipt.toString());
    String binding = receipt.path("source").path("source_binding").asString();
    assertFalse(binding.isEmpty());
    var current = h.getOrRevoke(binding, false);
    // Run all three production entrances even if the captured Get result later fails assertion.
    var stopped = h.getOrRevoke(binding, true);
    assertEquals(1, h.observedSources.get.get(), "SourceGet must reach real IAM: " + current);
    assertNotNull(
        h.observedSources.lastGetReply,
        () ->
            "IAM Get did not reply: "
                + h.observedSources.lastGetError
                + "; request="
                + h.observedSources.lastGet
                + "; wire="
                + current);
    assertTrue(h.observedSources.lastGetReply.hasSource());
    assertEquals(h.c, h.observedSources.lastGetReply.getConnection());
    assertEquals("source_get_result", text(current, "type"), current.toString());
    assertEquals("active", current.path("source").path("state").asString());
    assertTrue(Instant.parse(text(current, "valid_until")).isAfter(h.clock.instant()));
    assertEquals("source_revoke_result", text(stopped, "type"), stopped.toString());
    assertEquals(binding, text(stopped, "source_binding"));
    assertEquals(1, h.observedSources.register.get());
    assertEquals(1, h.observedSources.get.get());
    assertEquals(1, h.observedSources.revoke.get());
    assertEquals(0, h.observedSources.read.get());
    h.tx(
        () -> {
          assertEquals(
              2,
              store
                  .networkJdbc()
                  .queryForObject("SELECT state FROM iam.network_source", Integer.class));
          assertEquals(
              1,
              store
                  .networkJdbc()
                  .queryForObject("SELECT count(*) FROM iam.network_source", Integer.class));
          assertEquals(
              1,
              store
                  .networkJdbc()
                  .queryForObject("SELECT count(*) FROM iam.network_source_anchor", Integer.class));
          assertEquals(
              1,
              store
                  .networkJdbc()
                  .queryForObject("SELECT count(*) FROM iam.network_source_chain", Integer.class));
          return null;
        });
  }

  ObjectNode durableMiss() {
    var request = h.frame();
    h.observedSources.dropRegisterBeforeCommit = true;
    code("outcome_unknown", h.register(request));
    h.observedSources.dropRegisterBeforeCommit = false;
    assertEquals("pending", h.row(request).get("status"));
    assertNotNull(h.row(request).get("iam_request"));
    assertNull(h.row(request).get("iam_response"));
    assertTrue(h.facts().get("iam.network_source_operation").isEmpty());
    return request;
  }

  void expireOriginal(boolean sweep) {
    var original = h.c;
    h.clock.advance(3500);
    h.reconnect();
    h.clock.advance(101);
    checkOriginalIngress(original, sweep);
    h.reference(h.c);
  }

  void checkOriginalIngress(NetworkAuthorityConnection original, boolean sweep) {
    if (sweep) {
      var cleanup = new NetworkIngressCleanup(store, h.clock);
      long tenants =
          store
              .networkJdbc()
              .queryForObject("SELECT count(*) FROM iam.network_ingress_tenant", Long.class);
      for (long i = 0; i < ((tenants + 31) / 32) + 1; i++) {
        cleanup.sweepOnce();
        if (h.tx(() -> store.get("network_ingress", original.getIngress(), false)) == null) break;
      }
      assertNull(h.tx(() -> store.get("network_ingress", original.getIngress(), false)));
    } else assertNotNull(h.tx(() -> store.get("network_ingress", original.getIngress(), false)));
  }

  RegisterAgentSourceRequest frozen(ObjectNode request) throws Exception {
    return RegisterAgentSourceRequest.parseFrom((byte[]) h.row(request).get("iam_request"));
  }

  void pendingWithoutIamFailure(ObjectNode request, Map<String, List<String>> before) {
    // These persistent facts are the mutation oracle; an ordinary RPC count is not sufficient.
    assertEquals(
        "pending", h.row(request).get("status"), "ordinary failure must retain durable pending");
    assertNull(
        h.row(request).get("iam_response"), "ordinary failure has no complete durable receipt");
    assertEquals(before, h.facts(), "no IAM durable failure or collab terminal may be invented");
  }

  void onlyFenceReceiptWrites(Map<String, List<String>> before) {
    var after = h.facts();
    for (String table : before.keySet()) {
      if (Set.of("iam.network_source_anchor", "iam.network_source_operation").contains(table)) {
        assertEquals(before.get(table).size() + 1, after.get(table).size(), table);
        assertTrue(after.get(table).containsAll(before.get(table)), table);
      } else if (!Set.of("collab.network_source_operation", "collab.network_source_outbox")
          .contains(table)) assertEquals(before.get(table), after.get(table), table);
    }
  }

  @ParameterizedTest
  @ValueSource(booleans = {false, true})
  void pendingMissExpiredOrSweptConvergesThroughExplicitFenceAndReplaysCompleteReceipt(
      boolean sweep) throws Exception {
    var request = durableMiss();
    var saved = h.row(request);
    var original = frozen(request);
    expireOriginal(sweep);
    var before = h.facts();
    // Pure Read miss alone must leave every business fact byte-for-byte unchanged.
    TenantContext.run(
        h.tenant,
        () -> assertThrows(AurigaException.class, () -> h.source.readSourceReceipt(original)));
    assertEquals(before, h.facts());
    var response = h.register(h.retry(request));
    assertEquals("not_committed", text(response, "result"), response.toString());
    var receipt =
        RegisterAgentSourceResponse.parseFrom((byte[]) h.row(request).get("iam_response"));
    assertEquals(
        RegisterAgentSourceResponse.newBuilder()
            .setDefinitivelyUncommitted(true)
            .setOperationId(original.getOperationId())
            .setRequestDigest(saved.get("request_digest").toString())
            .setRejectionReason("original_connection_invalid_uncommitted")
            .build(),
        receipt);
    assertEquals(receipt, h.observedSources.lastFenceReply);
    assertArrayEquals(original.toByteArray(), h.observedSources.lastFence.toByteArray());
    assertArrayEquals(
        (byte[]) saved.get("original_frame"), (byte[]) h.row(request).get("original_frame"));
    assertEquals(saved.get("business_json"), h.row(request).get("business_json"));
    assertEquals("not_committed", h.row(request).get("status"));
    var persisted =
        h.tx(
            () ->
                store
                    .networkJdbc()
                    .queryForMap(
                        "SELECT request,response FROM iam.network_source_operation WHERE tenant_id=? AND method='Register' AND operation_id=?",
                        h.tenant,
                        original.getOperationId()));
    assertArrayEquals(original.toByteArray(), (byte[]) persisted.get("request"));
    assertArrayEquals(receipt.toByteArray(), (byte[]) persisted.get("response"));
    onlyFenceReceiptWrites(before);
    var committed = h.facts();
    int fences = h.observedSources.fence.get();
    var replay = h.register(h.retry(request));
    assertEquals(response.path("failure_evidence"), replay.path("failure_evidence"));
    assertEquals("not_committed", text(replay, "result"));
    TenantContext.run(
        h.tenant,
        () -> {
          assertEquals(receipt, h.client.readSourceReceipt(original));
          assertEquals(receipt, h.client.registerSource(original)); // Late Register cannot issue.
          var differentBody =
              original.toBuilder()
                  .setRegistration(
                      original.getRegistration().toBuilder()
                          .setAllocationFingerprint("sha256:" + "b".repeat(64)))
                  .build();
          assertEquals(
              "network.conflict",
              assertThrows(AurigaException.class, () -> h.client.registerSource(differentBody))
                  .errorCode()
                  .code());
        });
    assertEquals(fences, h.observedSources.fence.get(), "completed op never fences again");
    assertEquals(committed, h.facts());
  }

  @Test
  void liveOriginalMissCannotCreateSourceOrDurableFence() {
    var request = durableMiss();
    var before = h.facts();
    var reply = h.register(h.retry(request));
    assertTrue(
        h.facts().get("iam.network_source").isEmpty(),
        "live original miss must not create an IAM source");
    pendingWithoutIamFailure(request, before);
    code("outcome_unknown", reply);
    assertEquals(1, h.observedSources.fence.get(), "explicit no-create RPC must reject live C");
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "PERMISSION_DENIED",
        "UNAUTHENTICATED",
        "INVALID_ARGUMENT",
        "ABORTED",
        "ALREADY_EXISTS",
        "CANCELLED",
        "DEADLINE_EXCEEDED",
        "UNIMPLEMENTED",
        "NOT_FOUND",
        "INTERNAL",
        "UNKNOWN",
        "RESOURCE_EXHAUSTED"
      })
  void readErrorsNeverEnterFenceOrCreateAnInventedNegativeReceipt(String status) {
    var request = durableMiss();
    h.observedSources.readFailure =
        Status.fromCode(Status.Code.valueOf(status))
            .withDescription("miss unavailable not committed");
    var before = h.facts();
    var reply = h.register(h.retry(request));
    pendingWithoutIamFailure(request, before);
    code("outcome_unknown", reply);
    assertEquals(0, h.observedSources.fence.get());
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "UNAVAILABLE",
        "PERMISSION_DENIED",
        "UNIMPLEMENTED",
        "CANCELLED",
        "DEADLINE_EXCEEDED",
        "INTERNAL",
        "RESOURCE_EXHAUSTED"
      })
  void everyFenceErrorKeepsTheOriginalDurablePending(String status) throws Exception {
    var request = durableMiss();
    expireOriginal(false);
    h.observedSources.fenceFailure = Status.fromCode(Status.Code.valueOf(status));
    var before = h.facts();
    var reply = h.register(h.retry(request));
    pendingWithoutIamFailure(request, before);
    code("outcome_unknown", reply);
    assertEquals(1, h.observedSources.fence.get());
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "empty",
        "partial",
        "wrong-op",
        "wrong-digest",
        "wrong-reason",
        "unknown",
        "source-and-failure",
        "lost-commit-reply"
      })
  void malformedOrLostFenceReplyCannotCompleteCollabPending(String fault) throws Exception {
    var request = durableMiss();
    expireOriginal(true);
    h.observedSources.fenceEcho =
        value ->
            switch (fault) {
              case "empty" -> RegisterAgentSourceResponse.getDefaultInstance();
              case "partial" -> value.toBuilder().clearDefinitivelyUncommitted().build();
              case "wrong-op" -> value.toBuilder().setOperationId("another-op").build();
              case "wrong-digest" ->
                  value.toBuilder().setRequestDigest("sha256:" + "b".repeat(64)).build();
              case "wrong-reason" -> value.toBuilder().setRejectionReason("unavailable").build();
              case "unknown" ->
                  value.toBuilder()
                      .setUnknownFields(
                          com.google.protobuf.UnknownFieldSet.newBuilder()
                              .addField(
                                  100,
                                  com.google.protobuf.UnknownFieldSet.Field.newBuilder()
                                      .addVarint(1)
                                      .build())
                              .build())
                      .build();
              case "source-and-failure" ->
                  value.toBuilder().setSource(NetworkAgentSource.getDefaultInstance()).build();
              default -> throw new AurigaException(auriga.common.error.CommonErrors.UNAVAILABLE);
            };
    code("outcome_unknown", h.register(h.retry(request)));
    assertEquals("pending", h.row(request).get("status"));
    assertNull(h.row(request).get("iam_response"));
    assertTrue(h.facts().get("iam.network_source").isEmpty());
    assertEquals(
        1,
        h.facts().get("iam.network_source_operation").size(),
        "IAM committed complete failure, only reply is faulty");
    h.observedSources.fenceEcho = UnaryOperator.identity();
    assertEquals("not_committed", text(h.register(h.retry(request)), "result"));
    assertEquals(
        1, h.observedSources.fence.get(), "recovery of durable IAM receipt uses Read only");
  }

  @ParameterizedTest
  @ValueSource(strings = {"node", "owner", "device", "session", "deviceOwner", "sessionOwner"})
  void pendingOriginalOwnershipChangesNeverCreateFenceOrTerminal(String change) throws Exception {
    var request = durableMiss();
    changeIdentity(change);
    var before = h.facts();
    var reply = h.register(h.retry(request));
    code(change.equals("node") ? "unauthorized" : "outcome_unknown", reply);
    pendingWithoutIamFailure(request, before);
    assertTrue(h.facts().get("iam.network_source_operation").isEmpty());
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "tenant_id",
        "realm",
        "node",
        "ingress",
        "server_id",
        "client_id",
        "client_account"
      })
  void explicitFenceRejectsEveryOriginalConnectionSubstitutionWithoutWriting(String field)
      throws Exception {
    var request = durableMiss();
    var original = frozen(request);
    expireOriginal(true);
    var descriptor = original.getConnection().getDescriptorForType().findFieldByName(field);
    String value =
        switch (field) {
          case "tenant_id" -> UuidV7.next().toString();
          case "realm", "node", "client_id" -> "18446744073709551615";
          case "ingress" -> "b".repeat(26);
          case "server_id" -> "N" + "Z".repeat(55);
          default -> "OTHER";
        };
    var substituted = original.getConnection().toBuilder().setField(descriptor, value).build();
    var changed =
        original.toBuilder()
            .setConnection(substituted)
            .setRegistration(
                original.getRegistration().toBuilder()
                    .setRealm(substituted.getRealm())
                    .setNode(substituted.getNode()))
            .build();
    var before = h.facts();
    TenantContext.run(
        h.tenant,
        () -> assertThrows(AurigaException.class, () -> h.source.fenceSourceReceipt(changed)));
    pendingWithoutIamFailure(request, before);
  }

  @ParameterizedTest
  @ValueSource(
      strings = {"empty", "partial", "wrong-op", "wrong-digest", "unknown", "source-and-failure"})
  void malformedPureReadDoesNotEscalateToFenceOrOverwritePending(String fault) throws Exception {
    var request = durableMiss();
    var original = frozen(request);
    expireOriginal(false);
    TenantContext.run(h.tenant, () -> h.source.fenceSourceReceipt(original));
    int fences = h.observedSources.fence.get();
    h.observedSources.readEcho =
        value ->
            switch (fault) {
              case "empty" -> RegisterAgentSourceResponse.getDefaultInstance();
              case "partial" -> value.toBuilder().clearRequestDigest().build();
              case "wrong-op" -> value.toBuilder().setOperationId("other-op").build();
              case "wrong-digest" ->
                  value.toBuilder().setRequestDigest("sha256:" + "b".repeat(64)).build();
              case "unknown" ->
                  value.toBuilder()
                      .setUnknownFields(
                          com.google.protobuf.UnknownFieldSet.newBuilder()
                              .addField(
                                  100,
                                  com.google.protobuf.UnknownFieldSet.Field.newBuilder()
                                      .addVarint(1)
                                      .build())
                              .build())
                      .build();
              default ->
                  value.toBuilder().setSource(NetworkAgentSource.getDefaultInstance()).build();
            };
    var before = h.facts();
    code("outcome_unknown", h.register(h.retry(request)));
    assertEquals(fences, h.observedSources.fence.get());
    assertEquals("pending", h.row(request).get("status"));
    assertNull(h.row(request).get("iam_response"));
    assertEquals(before, h.facts());
  }

  @Test
  void originalRegisterSuccessWinsOverAnIndeterminateReadAndFenceWithoutActivation()
      throws Exception {
    var request = h.frame();
    h.observedSources.dropRegisterReply = true;
    code("outcome_unknown", h.register(request));
    h.observedSources.dropRegisterReply = false;
    assertEquals("pending", h.row(request).get("status"));
    var original = frozen(request);
    var created = TenantContext.call(h.tenant, () -> h.client.readSourceReceipt(original));
    assertTrue(created.hasSource());
    expireOriginal(true);
    h.observedSources.readFailure = Status.UNAVAILABLE;
    var before = h.facts();
    var response = h.register(h.retry(request));
    assertEquals("registered", text(response, "result"));
    assertFalse(
        response.has("valid_until"), "historical ACTIVE cannot activate current permission");
    assertEquals(
        created,
        RegisterAgentSourceResponse.parseFrom((byte[]) h.row(request).get("iam_response")));
    var after = h.facts();
    for (String table : before.keySet())
      if (table.startsWith("iam.")) assertEquals(before.get(table), after.get(table), table);
    int fences = h.observedSources.fence.get();
    // A completed local operation cannot Fence even when the Read is now unavailable.
    code("outcome_unknown", h.register(h.retry(request)));
    assertEquals(fences, h.observedSources.fence.get());
    assertEquals(after, h.facts());
  }

  @Test
  void savedSuccessPureReadRecoversOnRealC2AfterOriginalIngressGcWithoutAnyWrites()
      throws Exception {
    var request = h.frame();
    var created = h.register(request);
    assertEquals("registered", text(created, "result"), created.toString());
    var saved = h.row(request);
    var original = frozen(request);
    expireOriginal(true);
    assertNotEquals(original.getConnection(), h.c);
    var currentRef = h.reference(h.c);
    assertEquals(h.c, currentRef.getConnection());
    var before = h.facts();
    int reads = h.observedSources.read.get(),
        registrations = h.observedSources.register.get(),
        gets = h.observedSources.get.get(),
        fences = h.observedSources.fence.get();
    var retry = h.retry(request);
    var recovered = h.register(retry);
    assertEquals("registered", text(recovered, "result"), recovered.toString());
    assertEquals(created.path("source"), recovered.path("source"));
    assertFalse(recovered.has("valid_until"), "historical ACTIVE is not a current Get observation");
    assertEquals(h.epoch, recovered.path("server_auth").path("audience").path("epoch").asString());
    assertEquals(
        h.readyFrame.path("channel_nonce"), recovered.path("server_auth").path("channel_nonce"));
    assertEquals(retry.path("request_id"), recovered.path("request_id"));
    assertEquals(original, h.observedSources.lastRead);
    assertEquals(reads + 1, h.observedSources.read.get());
    assertEquals(registrations, h.observedSources.register.get());
    assertEquals(gets, h.observedSources.get.get());
    assertEquals(fences, h.observedSources.fence.get());
    sameRow(saved, h.row(request));
    assertEquals(
        before,
        h.facts(),
        "pure historical replay must not change IAM/collab facts, including xmin");
  }

  @Test
  void fenceCommitPrecedesLateRegisterFinalTransactionForTheSameOriginalKey(TestReporter reporter)
      throws Exception {
    var request = durableMiss();
    var original = frozen(request);
    long began = System.nanoTime();
    var phases = new ConcurrentLinkedQueue<String>();
    Consumer<String> phase =
        name -> phases.add(name + "=" + TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - began));
    var lateCode = new AtomicReference<>("not_started");
    // Redeem preserves other live IAM ingresses. Finish OAuth, C2/ref and READY before the
    // original Register's five-second RPC budget starts; its C1 must still pass real initial
    // authorization. Only then advance past C1 expiry and run the real ingress GC.
    h.clock.advance(3500);
    h.reconnect();
    h.reference(original.getConnection());
    phase.accept("c2_ready_c1_still_live");
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    var orgReturned = new AtomicBoolean();
    h.beforeSourceOrg =
        () -> {
          phase.accept("original_initial_checks_passed_org_entered");
          entered.countDown();
          assertDoesNotThrow(
              () -> assertTrue(release.await(10, TimeUnit.SECONDS), phases::toString));
          assertFalse(Context.current().isCancelled(), phases::toString);
          phase.accept("org_released_to_final_transaction");
          orgReturned.set(true);
        };
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var late =
          executor.submit(
              () -> {
                phase.accept("original_rpc_started");
                lateCode.set("in_flight");
                try {
                  var receipt =
                      TenantContext.call(h.tenant, () -> h.client.registerSource(original));
                  lateCode.set(Status.Code.OK.name());
                  return receipt;
                } catch (NetworkAuthorityClient.SourceRpcFailure error) {
                  lateCode.set(error.grpcCode().name());
                  throw error;
                } finally {
                  phase.accept("original_rpc_finished");
                }
              });
      try {
        assertTrue(entered.await(5, TimeUnit.SECONDS), phases::toString);
        h.clock.advance(101);
        checkOriginalIngress(original.getConnection(), true);
        phase.accept("c1_expired_and_gc_complete");
        assertEquals("not_committed", text(h.register(h.retry(request)), "result"));
        phase.accept("fence_and_collab_receipt_committed");
        var committed = h.facts();
        phase.accept("committed_facts_captured_release");
        release.countDown();
        var lateReceipt = late.get(5, TimeUnit.SECONDS);
        assertTrue(orgReturned.get(), "late Register must return through its final transaction");
        assertEquals(Status.Code.OK.name(), lateCode.get(), phases::toString);
        assertEquals(h.observedSources.lastFenceReply, lateReceipt);
        assertEquals(
            RegisterAgentSourceResponse.parseFrom((byte[]) h.row(request).get("iam_response")),
            lateReceipt);
        assertEquals(original, h.observedSources.lastFence);
        assertTrue(lateReceipt.getDefinitivelyUncommitted());
        assertFalse(lateReceipt.hasSource());
        assertEquals(original.getOperationId(), lateReceipt.getOperationId());
        assertEquals(h.row(request).get("request_digest"), lateReceipt.getRequestDigest());
        assertEquals("original_connection_invalid_uncommitted", lateReceipt.getRejectionReason());
        var after = h.facts();
        assertEquals(committed, after);
        assertTrue(after.get("iam.network_source").isEmpty());
        assertTrue(after.get("iam.network_source_chain").isEmpty());
      } finally {
        release.countDown();
      }
    } finally {
      reporter.publishEntry(
          Map.of("phase_elapsed_ms", phases.toString(), "late_register_grpc_code", lateCode.get()));
    }
  }

  @ParameterizedTest
  @ValueSource(
      strings = {"health", "ready", "epoch", "boot", "slot", "slotExpiry", "deadline", "ref"})
  void gateChangesAfterUndeterminedReadPreventFence(String change) throws Exception {
    var request = durableMiss();
    expireOriginal(false);
    h.observedSources.readFailure = Status.UNAVAILABLE;
    var before = h.facts();
    h.observedSources.afterRead = () -> changePendingGate(change);
    noHistoricalResult(() -> h.register(h.retry(request)));
    pendingWithoutIamFailure(request, before);
    assertEquals(0, h.observedSources.fence.get());
  }

  void changePendingGate(String change) {
    switch (change) {
      case "health" ->
          h.db(
              () ->
                  jdbc.update(
                      "UPDATE collab.network_node SET local_identity_status='identity_rollback' WHERE tenant_id=? AND node_id=?",
                      h.tenant,
                      h.c.getNode()));
      case "ready" ->
          h.db(
              () ->
                  jdbc.update(
                      "UPDATE collab.network_node SET wire_status='reconciling' WHERE tenant_id=? AND node_id=?",
                      h.tenant,
                      h.c.getNode()));
      case "epoch" ->
          h.db(
              () ->
                  jdbc.update(
                      "UPDATE collab.network_node SET connection_epoch=connection_epoch+1 WHERE tenant_id=? AND node_id=?",
                      h.tenant,
                      h.c.getNode()));
      case "boot" ->
          h.db(
              () ->
                  jdbc.update(
                      "UPDATE collab.network_node SET local_boot_id='changed-boot' WHERE tenant_id=? AND node_id=?",
                      h.tenant,
                      h.c.getNode()));
      case "slot" ->
          h.db(
              () ->
                  jdbc.update(
                      "UPDATE collab.network_source_slot SET holder=? WHERE tenant_id=? AND node_id=?",
                      UuidV7.next(),
                      h.tenant,
                      h.c.getNode()));
      case "slotExpiry" ->
          h.db(
              () ->
                  jdbc.update(
                      "UPDATE collab.network_source_slot SET valid_until=? WHERE tenant_id=? AND node_id=?",
                      Timestamp.from(h.clock.instant().minusSeconds(1)),
                      h.tenant,
                      h.c.getNode()));
      case "deadline" -> h.clock.advance(31);
      case "ref" ->
          h.observed.resolveEcho =
              value ->
                  value.toBuilder()
                      .setConnectionAnchor(
                          value.getConnectionAnchor().toBuilder()
                              .setAnchorId(UuidV7.next().toString()))
                      .build();
      default -> throw new AssertionError(change);
    }
  }

  @ParameterizedTest
  @ValueSource(
      strings = {"health", "ready", "epoch", "boot", "slot", "slotExpiry", "deadline", "ref"})
  void completeFenceReceiptIsDurableEvenWhenCurrentGateChangesBeforeSigning(String change)
      throws Exception {
    var request = durableMiss();
    expireOriginal(true);
    h.observedSources.afterFence = () -> changePendingGate(change);
    noHistoricalResult(() -> h.register(h.retry(request)));
    assertEquals("not_committed", h.row(request).get("status"));
    assertArrayEquals(
        h.observedSources.lastFenceReply.toByteArray(),
        (byte[]) h.row(request).get("iam_response"));
    assertTrue(h.facts().get("iam.network_source").isEmpty());
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "original_frame",
        "original_connection",
        "iam_request",
        "request_digest",
        "business_json",
        "epoch",
        "boot_id",
        "witness_json"
      })
  void frozenPendingRowChangeAfterFenceRejectsLateCompletion(String field) throws Exception {
    var request = durableMiss();
    expireOriginal(false);
    h.observedSources.afterFence =
        () ->
            h.db(
                () -> {
                  Object altered =
                      switch (field) {
                        case "original_frame", "original_connection", "iam_request" ->
                            new byte[] {1};
                        case "epoch" -> 999L;
                        default -> "changed-original";
                      };
                  return jdbc.update(
                      "UPDATE collab.network_source_operation SET "
                          + field
                          + "=? WHERE tenant_id=? AND operation_id=?",
                      altered,
                      h.tenant,
                      text(request, "operation_id"));
                });
    code("outcome_unknown", h.register(h.retry(request)));
    assertEquals("pending", h.row(request).get("status"));
    assertNull(h.row(request).get("iam_response"));
    assertTrue(h.facts().get("iam.network_source").isEmpty());
    assertEquals(1, h.facts().get("iam.network_source_operation").size());
  }

  @Test
  void concurrentSameReceiptCompletionIsReplayedWithoutLateRewrite() throws Exception {
    var request = durableMiss();
    expireOriginal(false);
    var winner = new AtomicReference<Map<String, Object>>();
    var winnerXmin = new AtomicReference<String>();
    h.observedSources.afterFence =
        () ->
            h.db(
                () -> {
                  var receipt = h.observedSources.lastFenceReply;
                  var result =
                      object()
                          .put("app_version", "1.0")
                          .put("type", "source_register_result")
                          .put("operation_id", receipt.getOperationId())
                          .put("result", "not_committed")
                          .putNull("source");
                  result
                      .putObject("failure_evidence")
                      .put("authority", "iam")
                      .put("reason", receipt.getRejectionReason())
                      .put("request_digest", receipt.getRequestDigest());
                  jdbc.queryForList(
                      "SELECT pg_advisory_xact_lock(hashtextextended(?,0))",
                      h.tenant + ":source:Register:" + receipt.getOperationId());
                  jdbc.update(
                      "UPDATE collab.network_source_operation SET status='not_committed',iam_response=?,result_json=? WHERE tenant_id=? AND operation_id=? AND status='pending'",
                      receipt.toByteArray(),
                      NetworkJson.canonical(result),
                      h.tenant,
                      receipt.getOperationId());
                  jdbc.update(
                      "UPDATE collab.network_source_outbox SET completed=true WHERE tenant_id=? AND operation_id=?",
                      h.tenant,
                      h.row(request).get("id"));
                  winner.set(h.row(request));
                  winnerXmin.set(
                      jdbc.queryForObject(
                          "SELECT xmin::text FROM collab.network_source_operation WHERE tenant_id=? AND operation_id=?",
                          String.class,
                          h.tenant,
                          text(request, "operation_id")));
                  return null;
                });
    assertEquals("not_committed", text(h.register(h.retry(request)), "result"));
    sameRow(winner.get(), h.row(request));
    assertEquals(
        winnerXmin.get(),
        h.db(
            () ->
                jdbc.queryForObject(
                    "SELECT xmin::text FROM collab.network_source_operation WHERE tenant_id=? AND operation_id=?",
                    String.class,
                    h.tenant,
                    text(request, "operation_id"))),
        "late same receipt must not rewrite the winner, including xmin");
    assertTrue(h.facts().get("iam.network_source").isEmpty());
  }

  void changeIdentity(String change) {
    UUID other =
        h.account(); // Real same-tenant FK target, committed before any relationship update.
    String node = h.c.getNode();
    String originalNode = node;
    if (change.equals("owner")) {
      h.mutate("session", h.sid, "accountId", other.toString());
      h.mutate("device", h.did, "accountId", other.toString());
      h.tx(
          () ->
              store
                  .networkJdbc()
                  .update(
                      "UPDATE iam.network_node SET account_id=? WHERE tenant_id=? AND node=?",
                      other,
                      h.tenant,
                      new java.math.BigDecimal(originalNode)));
      h.owner = other;
    } else if (change.equals("session") || change.equals("sessionOwner")) {
      if (change.equals("sessionOwner")) h.mutate("session", h.sid, "accountId", other.toString());
      h.sid = h.session(h.owner);
      h.mutate("device", h.did, "sessionId", h.sid);
    } else {
      if (change.equals("deviceOwner")) h.mutate("device", h.did, "accountId", other.toString());
      var credential =
          h.credential(h.sid); // Each replacement device comes from the real OAuth exchange.
      h.useCredential(credential);
      h.did = text(h.tokens.verify(h.access), "did");
      if (change.equals("node"))
        node = TenantContext.call(h.tenant, () -> h.network.register(h.access)).node();
      else {
        String sameNode = node;
        h.tx(
            () ->
                store
                    .networkJdbc()
                    .update(
                        "UPDATE iam.network_node SET device_id=? WHERE tenant_id=? AND node=?",
                        UUID.fromString(h.did),
                        h.tenant,
                        new java.math.BigDecimal(sameNode)));
      }
    }
    // Explicit trusted issuer entry after administrative rebinding; every claim points at a real
    // OAuth-created device and SessionManager-created session. Prepare/redeem independently verify
    // it.
    String token =
        h.tokens.access(
            h.tenant,
            h.owner.toString(),
            "auriga",
            "device",
            "device",
            null,
            UUID.fromString(h.did),
            h.clock.instant(),
            h.clock.instant(),
            "auriga.connect");
    h.c = h.redeem(node, token, h.did);
    h.reference(h.c);
    h.ready(!change.equals("node"));
  }

  @ParameterizedTest
  @ValueSource(strings = {"node", "owner", "device", "session", "deviceOwner", "sessionOwner"})
  void changedOriginalAssociationCannotRecoverFromAnOtherwiseAllowedC2(String change)
      throws Exception {
    var request = h.failureFrame("reconcile");
    assertEquals("not_accepted", text(h.register(request), "result"));
    var saved = h.row(request);
    changeIdentity(change);
    // A same-C2 positive control traverses the current gate, READY/epoch/native registration and
    // real Read first. Same-node cases retain their real registration across READY.
    if (!change.equals("node")) {
      var baseline = h.failureFrame("reconcile");
      assertEquals("not_accepted", text(h.register(baseline), "result"));
      var baselineRetry = h.retry(baseline);
      recovered(h.row(baseline), baselineRetry, h.register(baselineRetry));
    }
    var before = h.facts();
    int reads = h.observed.reads.get();
    code(
        change.equals("node") ? "unauthorized" : "outcome_unknown",
        h.register(h.retry(request))); // Primary assertion: no old result/digest.
    if (!change.equals("node")) {
      assertEquals(
          reads + 1, h.observed.reads.get(), "must reach the actual new Read, not an earlier gate");
      assertEquals(
          NetworkConnectionAnchor.parseFrom(
              ByteString.copyFrom((byte[]) saved.get("original_connection_anchor"))),
          h.observed.last.getOriginal());
    } else assertEquals(reads, h.observed.reads.get(), "different node is rejected locally");
    sameRow(saved, h.row(request));
    assertEquals(before, h.facts());
    h.noSourceRpc();
  }

  ObjectNode variant(
      Map<String, Object> saved, Consumer<Map<String, Object>> change, boolean reseal) {
    var row = new LinkedHashMap<>(saved);
    String op = "variant-" + UuidV7.next();
    row.put("id", UuidV7.next());
    row.put("operation_id", op);
    var frame = (ObjectNode) NetworkJson.parse((byte[]) row.get("original_frame"));
    frame.put("operation_id", op);
    var result = (ObjectNode) NetworkJson.parse(row.get("result_json").toString());
    result.put("operation_id", op);
    row.put("original_frame", bytes(frame));
    row.put("result_json", NetworkJson.canonical(result));
    LocalHistoryTestAccess.seal(row);
    change.accept(row);
    if (reseal) LocalHistoryTestAccess.seal(row);
    h.db(
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
  void completeOriginalTupleMustMatchItsAuthoritativeReferenceEvenWithResealedDigest()
      throws Exception {
    var request = h.failureFrame("reconcile");
    h.register(request);
    var saved = h.row(request);
    var original = h.c;
    var ref = NetworkConnectionAnchor.parseFrom((byte[]) saved.get("original_connection_anchor"));
    h.reconnect();
    var currentRef = h.reference(h.c);
    for (String field : List.of("ingress", "server", "client", "account")) {
      var b = original.toBuilder();
      switch (field) {
        case "ingress" -> b.setIngress(h.c.getIngress());
        case "server" -> b.setServerId(h.c.getServerId());
        case "client" -> b.setClientId(h.c.getClientId());
        case "account" -> b.setClientAccount(h.c.getClientAccount());
        default -> throw new AssertionError(field);
      }
      for (boolean both : List.of(false, true)) {
        var bad =
            variant(
                saved,
                row -> {
                  row.put("original_connection", b.build().toByteArray());
                  if (both)
                    row.put(
                        "original_connection_anchor",
                        ref.toBuilder().setConnection(b).build().toByteArray());
                },
                true);
        var before = h.facts();
        code("outcome_unknown", h.register(h.retry(bad)));
        assertEquals(before, h.facts());
      }
    }
    for (String id : List.of(currentRef.getAnchorId(), UuidV7.next().toString())) {
      var bad =
          variant(
              saved,
              row ->
                  row.put(
                      "original_connection_anchor",
                      ref.toBuilder().setAnchorId(id).build().toByteArray()),
              true);
      var before = h.facts();
      code("outcome_unknown", h.register(h.retry(bad)));
      assertEquals(before, h.facts());
    }
    for (String field : List.of("tenant", "node", "realm")) {
      var b = original.toBuilder();
      switch (field) {
        case "tenant" -> b.setTenantId(UuidV7.next().toString());
        case "node" -> b.setNode("999999");
        case "realm" -> b.setRealm("999999");
        default -> throw new AssertionError(field);
      }
      var bad =
          variant(
              saved,
              row -> {
                row.put("original_connection", b.build().toByteArray());
                row.put(
                    "original_connection_anchor",
                    ref.toBuilder().setConnection(b).build().toByteArray());
              },
              true);
      var before = h.facts();
      code("outcome_unknown", h.register(h.retry(bad)));
      assertEquals(before, h.facts());
    }
    for (String field :
        List.of("original_frame", "original_connection_anchor", "local_history_digest")) {
      var bad =
          variant(
              saved,
              row ->
                  row.put(
                      field,
                      field.equals("local_history_digest")
                          ? "sha256:" + "b".repeat(64)
                          : new byte[] {1}),
              false);
      var before = h.facts();
      code("outcome_unknown", h.register(h.retry(bad)));
      assertEquals(before, h.facts());
    }
    h.observed.echo = r -> r.toBuilder().setOriginal(r.getCurrent()).build();
    var before = h.facts();
    code("outcome_unknown", h.register(h.retry(request)));
    assertEquals(before, h.facts());
    sameRow(saved, h.row(request));
    h.noSourceRpc();
  }

  @Test
  void legacyAbsentProofStaysUnknownOnSameLiveCAndC2WithoutRepair() {
    var saved = legacy.row(legacyFrame);
    var before = legacy.facts();
    code("outcome_unknown", legacy.register(legacy.retry(legacyFrame)));
    assertEquals(before, legacy.facts());
    assertNull(saved.get("local_history_version"));
    assertNull(saved.get("original_connection_anchor"));
    assertNull(saved.get("local_history_digest"));
    legacy.reconnect();
    before = legacy.facts();
    code("outcome_unknown", legacy.register(legacy.retry(legacyFrame)));
    assertEquals(before, legacy.facts());
    sameRow(saved, legacy.row(legacyFrame));
    assertEquals(0, legacy.observed.reads.get());
    legacy.noSourceRpc();
  }

  @Test
  void sameOperationDifferentBusinessIsConflictWithoutReadingOrWritingHistory() {
    var request = h.failureFrame("reconcile");
    h.register(request);
    var saved = h.row(request);
    h.reconnect();
    var retry = h.retry(request).put("expected_revision", "1");
    var before = h.facts();
    code("conflict", h.register(retry));
    assertEquals(0, h.observed.reads.get());
    assertEquals(before, h.facts());
    sameRow(saved, h.row(request));
    h.noSourceRpc();
  }

  @ParameterizedTest
  @ValueSource(strings = {"miss", "error", "unimplemented", "cancel", "timeout"})
  void indeterminateReadNeverFallsBackOrLeaksResults(String fault) {
    var request = h.failureFrame("reconcile");
    h.register(request);
    var saved = h.row(request);
    h.reconnect();
    switch (fault) {
      case "miss" -> h.observed.failure = Status.NOT_FOUND;
      case "error" -> h.observed.failure = Status.UNAVAILABLE;
      case "unimplemented" -> h.observed.failure = Status.UNIMPLEMENTED;
      case "cancel" -> h.observed.failure = Status.CANCELLED;
      case "timeout" ->
          h.observed.holdUntilCancelled =
              true; // Real Read succeeds, response never arrives; real deadline expires.
      default -> throw new AssertionError(fault);
    }
    var before = h.facts();
    code("outcome_unknown", h.register(h.retry(request)));
    assertEquals(1, h.observed.reads.get());
    sameRow(saved, h.row(request));
    assertEquals(before, h.facts());
    h.noSourceRpc();
  }

  static void await(CountDownLatch latch) {
    try {
      assertTrue(latch.await(5, TimeUnit.SECONDS), "latch timed out");
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new AssertionError(e);
    }
  }

  static void noHistoricalResult(Supplier<JsonNode> work) {
    try {
      var reply = work.get();
      assertEquals("error", text(reply, "type"), reply.toString());
      assertTrue(
          Set.of(
                  "network.outcome_unknown",
                  "network.stale_epoch",
                  "network.recovery_conflict",
                  "network.unauthorized",
                  "network.identity_rollback",
                  "network.expired")
              .contains(text(reply, "code")),
          reply.toString());
      assertFalse(reply.has("result"));
      assertFalse(reply.has("failure_evidence"));
      assertFalse(reply.has("source"));
    } catch (StatusRuntimeException e) {
      assertNotNull(e.getTrailers());
      String code = e.getTrailers().get(GrpcMetadata.ERROR_CODE);
      assertTrue(
          Set.of("network.stale_epoch", "network.recovery_conflict", "network.unauthorized")
              .contains(code),
          String.valueOf(code));
    }
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "health",
        "ready",
        "epoch",
        "boot",
        "connection",
        "ref",
        "slot",
        "slotExpiry",
        "deadline"
      })
  void committedFenceChangesAfterRealReadPreventSigning(String fence) throws Exception {
    var request = h.failureFrame("reconcile");
    h.register(request);
    var saved = h.row(request);
    h.reconnect();
    var retry = h.retry(request);
    var before = h.facts();
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    h.observed.after =
        () -> {
          entered.countDown();
          await(release);
        };
    try (var executor = Executors.newSingleThreadExecutor()) {
      var recovering =
          executor.submit(
              () -> {
                noHistoricalResult(() -> h.register(retry));
                return null;
              });
      try {
        await(entered);
        switch (fence) {
          case "health" ->
              h.db(
                  () ->
                      jdbc.update(
                          "UPDATE collab.network_node SET local_identity_status='identity_rollback' WHERE tenant_id=? AND node_id=?",
                          h.tenant,
                          h.c.getNode()));
          case "ready" ->
              h.db(
                  () ->
                      jdbc.update(
                          "UPDATE collab.network_node SET wire_status='reconciling' WHERE tenant_id=? AND node_id=?",
                          h.tenant,
                          h.c.getNode()));
          case "epoch" ->
              h.db(
                  () ->
                      jdbc.update(
                          "UPDATE collab.network_node SET connection_epoch=connection_epoch+1 WHERE tenant_id=? AND node_id=?",
                          h.tenant,
                          h.c.getNode()));
          case "boot" ->
              h.db(
                  () ->
                      jdbc.update(
                          "UPDATE collab.network_node SET local_boot_id='other-boot' WHERE tenant_id=? AND node_id=?",
                          h.tenant,
                          h.c.getNode()));
          case "connection" ->
              h.reconnect(); // Real third redemption + READY replaces the actual binding.
          case "ref" ->
              h.observed.resolveEcho =
                  r ->
                      r.toBuilder()
                          .setConnectionAnchor(
                              r.getConnectionAnchor().toBuilder()
                                  .setAnchorId(UuidV7.next().toString()))
                          .build();
          case "slot" ->
              h.db(
                  () ->
                      jdbc.update(
                          "UPDATE collab.network_source_slot SET holder=? WHERE tenant_id=? AND node_id=?",
                          UuidV7.next(),
                          h.tenant,
                          h.c.getNode()));
          case "slotExpiry" ->
              h.db(
                  () ->
                      jdbc.update(
                          "UPDATE collab.network_source_slot SET valid_until=? WHERE tenant_id=? AND node_id=?",
                          Timestamp.from(h.clock.instant().minusSeconds(1)),
                          h.tenant,
                          h.c.getNode()));
          case "deadline" -> h.clock.advance(31);
          default -> throw new AssertionError(fence);
        }
      } finally {
        release.countDown();
      }
      recovering.get(10, TimeUnit.SECONDS);
    } finally {
      release.countDown();
      h.observed.after = () -> {};
    }
    assertEquals(1, h.observed.reads.get());
    sameRow(saved, h.row(request));
    if (!fence.equals("connection")) assertEquals(before, h.facts());
    else {
      var after = h.facts();
      before.remove("iam.network_connection_anchor");
      after.remove("iam.network_connection_anchor");
      assertEquals(before, after); // The intentionally redeemed C3 is the only new authority fact.
    }
    h.noSourceRpc();
  }

  @Test
  void finalFrozenRowComparisonRejectsAnInjectedReadViewWhilePgRemainsImmutable() {
    var request = h.failureFrame("reconcile");
    h.register(request);
    var saved = h.row(request);
    h.reconnect();
    var afterRead = new AtomicBoolean();
    var injected = new AtomicBoolean();
    var reads =
        new JdbcTemplate(jdbc.getDataSource()) {
          @Override
          public List<Map<String, Object>> queryForList(String sql, Object... args) {
            var rows = super.queryForList(sql, args);
            if (afterRead.get()
                && sql.startsWith("SELECT * FROM collab.network_source_operation")
                && !rows.isEmpty()) {
              var row = new LinkedHashMap<>(rows.getFirst());
              row.put("request_digest", "sha256:" + "b".repeat(64));
              injected.set(true);
              return List.of(row);
            }
            return rows;
          }
        };
    h.observed.after = () -> afterRead.set(true);
    h.assemble(reads, manager);
    h.mailbox();
    var before = h.facts();
    code("outcome_unknown", h.register(h.retry(request)));
    assertTrue(injected.get());
    assertEquals(1, h.observed.reads.get());
    sameRow(saved, h.row(request));
    assertEquals(before, h.facts());
    h.noSourceRpc();
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "local_history_version",
        "original_connection_anchor",
        "local_history_digest",
        "original_frame"
      })
  void missingRetainedFieldInReadViewIsUnknownEvenWhileC1IsLive(String missing) {
    var request = h.failureFrame("reconcile");
    h.register(request);
    var saved = h.row(request);
    var touched = new AtomicBoolean();
    var reads =
        new JdbcTemplate(jdbc.getDataSource()) {
          @Override
          public List<Map<String, Object>> queryForList(String sql, Object... args) {
            var rows = super.queryForList(sql, args);
            if (sql.startsWith("SELECT * FROM collab.network_source_operation")
                && !rows.isEmpty()) {
              var row = new LinkedHashMap<>(rows.getFirst());
              row.put(missing, null);
              touched.set(true);
              return List.of(row);
            }
            return rows;
          }
        };
    // Explicit read-view fault: runtime cannot write these missing values into a V15 tombstone.
    // The separate V14->V15 fixture above supplies actual durable legacy NULL provenance.
    h.assemble(reads, manager);
    h.mailbox();
    var before = h.facts();
    code("outcome_unknown", h.register(h.retry(request)));
    assertTrue(touched.get());
    h.reconnect();
    before = h.facts();
    code("outcome_unknown", h.register(h.retry(request)));
    assertEquals(0, h.observed.reads.get());
    sameRow(saved, h.row(request));
    assertEquals(before, h.facts());
    h.noSourceRpc();
  }

  @Test
  void ordinaryConcurrentFrozenRowRewriteIsRejectedByPgBeforeApplicationCas() throws Exception {
    var request = h.failureFrame("reconcile");
    h.register(request);
    var saved = h.row(request);
    h.reconnect();
    var retry = h.retry(request);
    var before = h.facts();
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    h.observed.after =
        () -> {
          entered.countDown();
          await(release);
        };
    try (var executor = Executors.newSingleThreadExecutor()) {
      var recovering = executor.submit(() -> h.register(retry));
      try {
        await(entered);
        sqlState(
            "23514",
            () ->
                h.db(
                    () ->
                        jdbc.update(
                            "UPDATE collab.network_source_operation SET request_digest=? WHERE tenant_id=? AND id=?",
                            "sha256:" + "b".repeat(64),
                            h.tenant,
                            saved.get("id"))));
      } finally {
        release.countDown();
      }
      recovered(saved, retry, recovering.get(10, TimeUnit.SECONDS));
    } finally {
      release.countDown();
    }
    // This successful unchanged recovery is evidence of the PG guard, NOT of a changed row
    // hitting the final application CAS; the separate read-view fault exercises that CAS.
    sameRow(saved, h.row(request));
    assertEquals(before, h.facts());
    h.noSourceRpc();
  }

  @ParameterizedTest
  @ValueSource(strings = {"mutationBeforeRead", "readBeforeMutation"})
  void associationMutationAndReadHaveExplicitLockOrder(String order) throws Exception {
    var request = h.failureFrame("reconcile");
    h.register(request);
    var saved = h.row(request);
    h.reconnect();
    var retry = h.retry(request);
    UUID other = h.account();
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    Runnable pause =
        () -> {
          entered.countDown();
          await(release);
        };
    if (order.equals("mutationBeforeRead")) h.observed.before = pause;
    else h.observed.after = pause;
    try (var executor = Executors.newSingleThreadExecutor()) {
      var result =
          executor.submit(
              () -> {
                if (order.equals("mutationBeforeRead")) code("outcome_unknown", h.register(retry));
                else noHistoricalResult(() -> h.register(retry));
              });
      try {
        await(entered); // Current Resolve and collab admission have already passed.
        h.tx(
            () -> {
              store.lock("network-authority");
              var session =
                  store.byId("session", h.sid, true); // Same lock order as the actual Read.
              session.put("accountId", other.toString());
              store.save("session", session);
              if (order.equals("mutationBeforeRead"))
                release.countDown(); // Read cannot pass our advisory lock until COMMIT.
              return null;
            });
      } finally {
        release.countDown();
      }
      result.get(10, TimeUnit.SECONDS);
    } finally {
      release.countDown();
    }
    assertEquals(1, h.observed.reads.get());
    sameRow(saved, h.row(request));
    h.noSourceRpc();
    // A mutation after the remote Read is checked by current Resolve; no claim of a zero
    // revocation window after the final remote check is made.
  }

  static void sqlState(String state, Runnable work) {
    var error = assertThrows(DataAccessException.class, work::run);
    Throwable cause = error;
    while (cause != null && !(cause instanceof java.sql.SQLException)) cause = cause.getCause();
    assertInstanceOf(java.sql.SQLException.class, cause);
    assertEquals(state, ((java.sql.SQLException) cause).getSQLState());
  }

  @Test
  void restrictedRolesRlsAndImmutableTombstoneAreRealDatabaseConstraints() {
    var request = h.failureFrame("reconcile");
    h.register(request);
    var saved = h.row(request);
    for (var entry : Map.of("iam", store.networkJdbc(), "collab", jdbc).entrySet()) {
      var db = entry.getValue();
      String schema = entry.getKey();
      assertFalse(
          db.queryForObject(
              "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user",
              Boolean.class));
      assertFalse(
          db.queryForObject(
              "SELECT pg_has_role(current_user,?, 'MEMBER')",
              Boolean.class,
              schema.equals("iam") ? iamRoles.owner().role() : collabRoles.owner().role()));
      assertTrue(
          db.queryForObject(
              "SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid=?::regclass",
              Boolean.class,
              schema
                  + "."
                  + (schema.equals("iam")
                      ? "network_connection_anchor"
                      : "network_source_operation")));
    }
    assertEquals(
        0,
        jdbc.queryForObject("SELECT count(*) FROM collab.network_source_operation", Integer.class));
    assertEquals(
        Integer.valueOf(0),
        TenantContext.call(
            UuidV7.next(),
            () ->
                new TransactionTemplate(manager)
                    .execute(
                        s ->
                            jdbc.queryForObject(
                                "SELECT count(*) FROM collab.network_source_operation",
                                Integer.class))));
    sqlState(
        "23514",
        () ->
            h.db(
                () ->
                    jdbc.update(
                        "UPDATE collab.network_source_operation SET request_digest=request_digest WHERE tenant_id=?",
                        h.tenant)));
    sqlState(
        "23514",
        () ->
            h.db(
                () ->
                    jdbc.update(
                        "DELETE FROM collab.network_source_operation WHERE tenant_id=?",
                        h.tenant)));
    sqlState(
        "42501",
        () ->
            h.db(
                () -> {
                  jdbc.execute(
                      "ALTER TABLE collab.network_source_operation DISABLE ROW LEVEL SECURITY");
                  return null;
                }));
    sqlState("42501", () -> variant(saved, row -> row.put("tenant_id", UuidV7.next()), false));
    for (String field :
        List.of("local_history_version", "original_connection_anchor", "local_history_digest"))
      sqlState("23514", () -> variant(saved, row -> row.put(field, null), false));
    sqlState("23514", () -> variant(saved, row -> row.put("local_history_version", 2), false));
    sameRow(saved, h.row(request));
    h.noSourceRpc();
  }

  @ParameterizedTest
  @ValueSource(strings = {"rollback", "commitAckLost"})
  void failedOrUncertainLocalCommitIsNotReportedAsDefinitive(String fault) {
    var request = h.failureFrame("reconcile");
    var touched = new AtomicBoolean();
    var arm = new AtomicBoolean();
    var transactions =
        new TenantTransactionManager(jdbc.getDataSource()) {
          @Override
          protected void doCommit(DefaultTransactionStatus status) {
            super.doCommit(status);
            if (arm.getAndSet(false))
              throw new org.springframework.transaction.TransactionSystemException(
                  "test commit acknowledgement lost");
          }
        };
    var reads =
        new JdbcTemplate(jdbc.getDataSource()) {
          @Override
          public List<Map<String, Object>> queryForList(String sql, Object... args) {
            var rows = super.queryForList(sql, args);
            if (sql.startsWith("SELECT * FROM collab.network_source_operation")
                && !rows.isEmpty()
                && text(request, "operation_id").equals(rows.getFirst().get("operation_id"))
                && "not_accepted".equals(rows.getFirst().get("status"))
                && touched.compareAndSet(false, true)) {
              if (fault.equals("rollback"))
                throw new org.springframework.dao.DataIntegrityViolationException(
                    "test pre-commit failure");
              arm.set(true);
            }
            return rows;
          }
        };
    h.assemble(reads, transactions);
    h.mailbox();
    var before = h.facts();
    code("outcome_unknown", h.register(request));
    assertTrue(touched.get());
    h.assemble(jdbc, manager);
    h.mailbox();
    if (fault.equals("rollback")) assertEquals(before, h.facts());
    else {
      var saved = h.row(request);
      var retry = h.retry(request);
      var committed = h.facts();
      recovered(saved, retry, h.register(retry));
      assertEquals(committed, h.facts());
      sameRow(saved, h.row(request));
      assertEquals(1, h.observed.reads.get());
    }
    h.noSourceRpc();
  }
}

/** Frame/transport regression only; no PostgreSQL extension or identity/history claims. */
class NetworkLocalSourceHistoryTransportTest {
  private static Check check() {
    return new Check("2", "1", "", "0", Sender.HUMAN, Operation.CONNECT, 0, 0, 0);
  }

  @Test
  void sharedCheckTransportReauthenticatesAndSetsANewFiniteDeadlinePerCall() throws Exception {
    var h = new NetworkLocalSourceHistoryIntegrationTest.Harness();
    try {
      h.security();
      var authority = mock(IamNetworkAuthority.class);
      var limits = new EnumMap<Limit, Long>(Limit.class);
      for (var limit : Limit.values()) limits.put(limit, limit.defaultValue());
      when(authority.check(any())).thenReturn(new Decision(true, "allowed", limits));
      when(authority.effectiveLimits()).thenReturn(limits);
      h.connectIam(authority, mock(IamNetworkSourceAuthority.class));
      var deadlines = new CopyOnWriteArrayList<Deadline>();
      h.client =
          new NetworkAuthorityClient(
              ClientInterceptors.intercept(
                  h.iamChannel,
                  new ClientInterceptor() {
                    public <Q, R> ClientCall<Q, R> interceptCall(
                        io.grpc.MethodDescriptor<Q, R> method, CallOptions options, Channel next) {
                      assertNotNull(options.getDeadline());
                      assertTrue(options.getDeadline().timeRemaining(TimeUnit.MILLISECONDS) > 0);
                      assertTrue(
                          options.getDeadline().timeRemaining(TimeUnit.MILLISECONDS) <= 5000);
                      deadlines.add(options.getDeadline());
                      return next.newCall(method, options);
                    }
                  }),
              Duration.ofSeconds(5));
      h.observed.beforeCheck =
          () -> {
            var p = AuthorizationRules.currentPrincipal().orElseThrow();
            assertEquals("svc:collab", p.subject());
            assertEquals(TenantContext.PLATFORM_TENANT, p.tenantId());
            assertEquals(h.tenant, TenantContext.require());
          };
      var transportManager = new TenantTransactionManager(mock(javax.sql.DataSource.class));
      assertTrue(
          h.gateway(
                  () ->
                      LocalHistoryTestAccess.checkOutsideDatabase(
                          h.client, transportManager, check()))
              .allowed());
      h.clock.advance(3500);
      assertTrue(
          h.gateway(
                  () ->
                      LocalHistoryTestAccess.checkOutsideDatabase(
                          h.client, transportManager, check()))
              .allowed());
      assertEquals(4, deadlines.size()); // Each Check also gets the real generated Limits response.
      assertNotSame(deadlines.get(0), deadlines.get(2));
      verify(authority, times(2)).check(check());
    } finally {
      h.close();
    }
  }

  @Test
  void droppedCheckReplyTerminatesAtTheExistingFiveSecondRpcDeadline() throws Exception {
    var h = new NetworkLocalSourceHistoryIntegrationTest.Harness();
    var executor = Executors.newSingleThreadExecutor();
    try {
      h.security();
      h.connectIam(mock(IamNetworkAuthority.class), mock(IamNetworkSourceAuthority.class));
      var entered = new CountDownLatch(1);
      h.observed.beforeCheck = entered::countDown;
      h.observed.dropCheckResponse = true;
      var transportManager = new TenantTransactionManager(mock(javax.sql.DataSource.class));
      var call =
          executor.submit(
              () ->
                  assertThrows(
                      AurigaException.class,
                      () ->
                          h.gateway(
                              () ->
                                  LocalHistoryTestAccess.checkOutsideDatabase(
                                      h.client, transportManager, check()))));
      NetworkLocalSourceHistoryIntegrationTest.await(entered);
      assertEquals("common.unavailable", call.get(8, TimeUnit.SECONDS).errorCode().code());
    } finally {
      try {
        h.close();
      } finally {
        executor.shutdownNow();
        assertTrue(executor.awaitTermination(5, TimeUnit.SECONDS));
      }
    }
  }

  @Test
  void getAndRevokeUseTheirActualSourceControlSchemas() {
    var h = new NetworkLocalSourceHistoryIntegrationTest.Harness();
    h.c =
        NetworkAuthorityConnection.newBuilder()
            .setTenantId(h.tenant.toString())
            .setRealm("2")
            .setNode("1")
            .setIngress("a".repeat(26))
            .setServerId("N" + "A".repeat(55))
            .setClientId("41")
            .setClientAccount("BROKER-41")
            .build();
    h.boot = "boot-fixture";
    h.registration =
        NetworkLocalSourceHistoryIntegrationTest.object()
            .put("registration_id", UuidV7.next().toString())
            .put("allocation_op_id", "allocation-fixture")
            .put("allocation_fingerprint", "sha256:" + "a".repeat(64))
            .put("registration_revision", "1")
            .put("local", "1")
            .put("address_type", "session");
    LocalHistoryTestAccess.validateSourceFrame("SourceRegister", h.frame());
    LocalHistoryTestAccess.validateSourceFrame(
        "SourceGet", h.getOrRevokeFrame(UuidV7.next().toString(), false));
    LocalHistoryTestAccess.validateSourceFrame(
        "SourceRevoke", h.getOrRevokeFrame(UuidV7.next().toString(), true));
  }
}
