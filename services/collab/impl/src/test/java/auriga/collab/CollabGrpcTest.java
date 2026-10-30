package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.CollabAdmission;
import auriga.collab.app.CollabService;
import auriga.collab.config.CollabRuntimeConfiguration;
import auriga.collab.grpc.CollabGrpcService;
import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.grpc.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.collab.v1.*;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.ECDSASigner;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.*;
import com.sun.net.httpserver.HttpServer;
import io.grpc.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.*;
import java.util.function.Consumer;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class CollabGrpcTest {
  static ECKey key;
  static HttpServer issuer;
  static GrpcServer server;
  static javax.sql.DataSource runtime;
  static AccessTokenVerifier verifier;
  static final List<ManagedChannel> channels = new ArrayList<>();
  static final UUID TENANT = UUID.randomUUID();
  static final String ISSUER = "https://auriga.example.com";

  @BeforeAll
  static void start() throws Exception {
    key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    issuer = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    issuer.createContext(
        "/jwks",
        exchange -> {
          byte[] data = new JWKSet(key.toPublicJWK()).toString().getBytes(StandardCharsets.UTF_8);
          exchange.getResponseHeaders().set("Content-Type", "application/json");
          exchange.sendResponseHeaders(200, data.length);
          exchange.getResponseBody().write(data);
          exchange.close();
        });
    issuer.start();
    verifier =
        new AccessTokenVerifier(
            AccessTokenDecoders.createInternal(
                AccessTokenSettings.of(
                    ISSUER,
                    URI.create("http://127.0.0.1:" + issuer.getAddress().getPort() + "/jwks"))));
    var roles =
        PostgresExtension.serviceRoles(
            CollabGrpcTest.class, "collab_grpc_owner", "collab_grpc_app", "collab");
    runtime = roles.runtime().dataSource();
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    // Exercise the actual production configuration's missing-admission fallback.
    CollabAdmission fallback;
    try (var context =
        new org.springframework.context.annotation.AnnotationConfigApplicationContext()) {
      context
          .getEnvironment()
          .getPropertySources()
          .addFirst(
              new org.springframework.core.env.MapPropertySource(
                  "collab-test", Map.of("auriga.service.schema", "collab")));
      context.registerBean(javax.sql.DataSource.class, roles.runtime()::dataSource);
      context.registerBean(
          TenantTransactionManager.class,
          () -> new TenantTransactionManager(roles.runtime().dataSource()));
      context.register(CollabRuntimeConfiguration.class);
      context.registerBean(
          auriga.collab.grpc.NetworkAuthorityClient.class,
          () -> org.mockito.Mockito.mock(auriga.collab.grpc.NetworkAuthorityClient.class));
      context.registerBean(
          auriga.collab.grpc.NetworkSourceGrpcAuthority.class,
          () -> org.mockito.Mockito.mock(auriga.collab.grpc.NetworkSourceGrpcAuthority.class));
      context.registerBean(
          auriga.collab.app.NetworkSigningKeys.class,
          () -> new auriga.collab.app.NetworkSigningKeys(java.time.Clock.systemUTC(), 1));
      context.refresh();
      fallback = context.getBean(CollabAdmission.class);
    }
    var service =
        new CollabService(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()),
            fallback);
    server =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(new CollabGrpcService(service)),
            new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:ai-gateway", "svc:other")),
            null,
            false,
            Set.of());
    server.start();
  }

  @AfterAll
  static void stop() throws Exception {
    for (var c : channels) {
      c.shutdownNow();
      c.awaitTermination(5, java.util.concurrent.TimeUnit.SECONDS);
    }
    if (server != null) server.stop();
    if (issuer != null) issuer.stop(0);
  }

  static String token(Consumer<JWTClaimsSet.Builder> changes) throws Exception {
    Instant now = Instant.now();
    var claims =
        new JWTClaimsSet.Builder()
            .issuer(ISSUER)
            .subject("svc:ai-gateway")
            .audience("auriga.internal")
            .issueTime(Date.from(now))
            .notBeforeTime(Date.from(now))
            .expirationTime(Date.from(now.plusSeconds(300)))
            .jwtID(UUID.randomUUID().toString())
            .claim("tid", TenantContext.PLATFORM_TENANT.toString())
            .claim("ctx", "service")
            .claim("ptyp", "internal_service")
            .claim("perms", List.of())
            .claim("roles", List.of());
    changes.accept(claims);
    var jwt =
        new SignedJWT(
            new JWSHeader.Builder(JWSAlgorithm.ES256)
                .keyID(key.getKeyID())
                .type(new JOSEObjectType("at+jwt"))
                .build(),
            claims.build());
    jwt.sign(new ECDSASigner(key));
    return jwt.serialize();
  }

  static CollabCommunicationServiceGrpc.CollabCommunicationServiceBlockingStub stub(String token) {
    var channel =
        token == null
            ? GrpcChannels.open("127.0.0.1:" + server.port())
            : GrpcChannels.open(
                "127.0.0.1:" + server.port(), new AurigaClientInterceptor(() -> token, null));
    channels.add(channel);
    return CollabCommunicationServiceGrpc.newBlockingStub(channel)
        .withDeadlineAfter(10, java.util.concurrent.TimeUnit.SECONDS);
  }

  static RegisterEndpointRequest request() {
    return RegisterEndpointRequest.newBuilder()
        .setActor(
            Actor.newBuilder()
                .setTenantId(TENANT.toString())
                .setAccountId(UUID.randomUUID().toString())
                .setCredentialKind("DEVICE")
                .setCredentialSubject(UUID.randomUUID().toString()))
        .setLabel("member")
        .setReceiveEnabled(true)
        .build();
  }

  static void reject(Status.Code status, String code, Runnable action) {
    var e = assertThrows(StatusRuntimeException.class, action::run);
    assertEquals(status, e.getStatus().getCode(), e.toString());
    assertNotNull(e.getTrailers());
    assertEquals(code, e.getTrailers().get(GrpcMetadata.ERROR_CODE));
  }

  @Test
  void realJwtAndDelegatedTenantReachDefaultDeny() throws Exception {
    var rpc = stub(token(c -> {}));
    var r = request();
    reject(
        Status.Code.UNAVAILABLE,
        "common.unavailable",
        () -> TenantContext.call(TENANT, () -> rpc.registerEndpoint(r)));
  }

  @Test
  void rejectsNoTokenWrongAudienceDeviceAndExternalServiceAccount() throws Exception {
    var r = request();
    reject(
        Status.Code.UNAUTHENTICATED,
        "common.unauthenticated",
        () -> stub(null).registerEndpoint(r));
    for (String invalid :
        List.of(
            "garbage",
            token(c -> c.audience("auriga.ai-gateway")),
            token(
                c ->
                    c.subject(UUID.randomUUID().toString())
                        .claim("ptyp", "device")
                        .claim("ctx", "device")
                        .claim("did", UUID.randomUUID().toString())),
            token(c -> c.subject(UUID.randomUUID().toString()).claim("ptyp", "service_account")))) {
      var rpc = stub(invalid);
      reject(
          Status.Code.UNAUTHENTICATED,
          "common.unauthenticated",
          () -> TenantContext.call(TENANT, () -> rpc.registerEndpoint(r)));
    }
  }

  @Test
  void rejectsOtherServiceMissingDelegationAndBodyMismatch() throws Exception {
    var r = request();
    var other = stub(token(c -> c.subject("svc:other")));
    reject(
        Status.Code.PERMISSION_DENIED,
        "common.forbidden",
        () -> TenantContext.call(TENANT, () -> other.registerEndpoint(r)));
    var unlisted = stub(token(c -> c.subject("svc:unlisted")));
    reject(
        Status.Code.PERMISSION_DENIED,
        "common.forbidden",
        () -> TenantContext.call(TENANT, () -> unlisted.registerEndpoint(r)));
    var valid = stub(token(c -> {}));
    reject(Status.Code.PERMISSION_DENIED, "common.forbidden", () -> valid.registerEndpoint(r));
    reject(
        Status.Code.PERMISSION_DENIED,
        "common.forbidden",
        () -> TenantContext.call(UUID.randomUUID(), () -> valid.registerEndpoint(r)));
  }

  @Test
  void internalTransportCannotBindNonLoopbackPlaintext() {
    var unsafe = new GrpcServer("0.0.0.0", 0, List.of(), null, null, false, Set.of());
    assertThrows(IllegalStateException.class, unsafe::start);
  }

  @Test
  void authenticatedInternalRpcRoundTripWithTestControlPlane() throws Exception {
    var core =
        new CollabService(
            new JdbcTemplate(runtime),
            new TenantTransactionManager(runtime),
            (actor, operation) -> {});
    var allowed =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(new CollabGrpcService(core)),
            new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:ai-gateway")),
            null,
            false,
            Set.of());
    allowed.start();
    ManagedChannel channel =
        GrpcChannels.open(
            "127.0.0.1:" + allowed.port(),
            new AurigaClientInterceptor(
                () -> {
                  try {
                    return token(c -> {});
                  } catch (Exception e) {
                    throw new IllegalStateException(e);
                  }
                },
                null));
    try {
      var rpc =
          CollabCommunicationServiceGrpc.newBlockingStub(channel)
              .withDeadlineAfter(10, java.util.concurrent.TimeUnit.SECONDS);
      TenantContext.call(
          TENANT,
          () -> {
            Actor a = request().getActor(), b = request().getActor();
            a =
                a.toBuilder()
                    .setEndpointId(
                        rpc.registerEndpoint(
                                RegisterEndpointRequest.newBuilder()
                                    .setActor(a)
                                    .setLabel("hq")
                                    .setReceiveEnabled(true)
                                    .build())
                            .getEndpoint()
                            .getId())
                    .setHostSessionId("hq-session")
                    .build();
            b =
                b.toBuilder()
                    .setEndpointId(
                        rpc.registerEndpoint(
                                RegisterEndpointRequest.newBuilder()
                                    .setActor(b)
                                    .setLabel("member")
                                    .setReceiveEnabled(true)
                                    .build())
                            .getEndpoint()
                            .getId())
                    .setHostSessionId("member-session")
                    .build();
            a =
                a.toBuilder()
                    .setBindingId(
                        rpc.bindSession(BindSessionRequest.newBuilder().setActor(a).build())
                            .getBinding()
                            .getId())
                    .build();
            assertTrue(
                rpc.listEndpoints(
                            ListEndpointsRequest.newBuilder().setActor(a).setLimit(100).build())
                        .getPage()
                        .getItemsCount()
                    >= 2);
            Message first =
                rpc.openThread(
                        OpenThreadRequest.newBuilder()
                            .setActor(a)
                            .setTargetEndpointId(b.getEndpointId())
                            .setText("hello")
                            .setIdempotencyKey("rpc-open")
                            .build())
                    .getMessage();
            b =
                b.toBuilder()
                    .setBindingId(
                        rpc.bindSession(
                                BindSessionRequest.newBuilder()
                                    .setActor(b)
                                    .setThreadId(first.getThreadId())
                                    .build())
                            .getBinding()
                            .getId())
                    .build();
            Delivery d =
                rpc.pullDeliveries(
                        PullDeliveriesRequest.newBuilder().setActor(b).setLimit(100).build())
                    .getPage()
                    .getItems(0);
            assertEquals(first, d.getMessage());
            assertEquals(
                "host_accepted",
                rpc.ackDelivery(
                        AckDeliveryRequest.newBuilder()
                            .setActor(b)
                            .setDeliveryId(d.getId())
                            .setLeaseToken(d.getLeaseToken())
                            .setHostRequestId(d.getId())
                            .build())
                    .getDelivery()
                    .getStatus());
            assertEquals(
                2,
                rpc.replyThread(
                        ReplyThreadRequest.newBuilder()
                            .setActor(b)
                            .setThreadId(first.getThreadId())
                            .setText("reply")
                            .setIdempotencyKey("rpc-reply")
                            .build())
                    .getMessage()
                    .getSequence());
            assertEquals(
                2,
                rpc.readThread(
                        ReadThreadRequest.newBuilder()
                            .setActor(a)
                            .setThreadId(first.getThreadId())
                            .setLimit(100)
                            .build())
                    .getPage()
                    .getItemsCount());
            assertTrue(
                rpc.revokeEndpoint(RevokeEndpointRequest.newBuilder().setActor(b).build())
                    .getEndpoint()
                    .getRevoked());
            return true;
          });
    } finally {
      channel.shutdownNow();
      channel.awaitTermination(5, java.util.concurrent.TimeUnit.SECONDS);
      allowed.stop();
    }
  }
}
