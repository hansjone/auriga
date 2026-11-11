package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.audit.*;
import auriga.common.context.*;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.org.*;
import auriga.testsupport.*;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.ECDSASigner;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jose.jwk.source.ImmutableJWKSet;
import com.nimbusds.jwt.*;
import io.nats.client.Connection;
import java.net.*;
import java.net.http.*;
import java.time.*;
import java.util.*;
import javax.sql.DataSource;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.beans.factory.annotation.*;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.annotation.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.*;
import tools.jackson.databind.json.JsonMapper;

@SpringBootTest(
    classes = AuditCombinedIntegrationTest.App.class,
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = {
      "auriga.platform.combined=true",
      "auriga.services.audit.enabled=true",
      "auriga.grpc.server.enabled=false",
      "server.address=127.0.0.1",
      "auriga.logging.json=false",
      "spring.autoconfigure.exclude=org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration,org.springframework.boot.flyway.autoconfigure.FlywayAutoConfiguration,auriga.common.autoconfigure.AurigaPersistenceAutoConfiguration,auriga.common.autoconfigure.AurigaEventsAutoConfiguration"
    })
@ActiveProfiles("audit-integration")
@ExtendWith({PostgresExtension.class, NatsExtension.class})
@DirtiesContext
class AuditCombinedIntegrationTest {
  static final String ISSUER = "https://auth.example.com";
  static final UUID TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001");
  static final ECKey KEY = key();
  static final JsonMapper JSON = JsonMapper.builder().build();

  static ECKey key() {
    try {
      return new ECKeyGenerator(Curve.P_256).keyID("audit-test").generate();
    } catch (JOSEException e) {
      throw new IllegalStateException(e);
    }
  }

  @Value("${local.server.port}")
  int port;

  @Autowired
  @Qualifier("orgDataSource")
  DataSource orgSource;

  @Autowired
  @Qualifier("orgEventPublisher")
  EventPublisher orgEvents;

  @Autowired
  @Qualifier("orgTransactionManager")
  TenantTransactionManager orgTx;

  @Autowired
  @Qualifier("auditDataSource")
  DataSource auditSource;

  @Autowired NatsEventConsumer auditEventConsumer;
  @Autowired org.springframework.context.ApplicationContext context;

  @DynamicPropertySource
  static void props(DynamicPropertyRegistry r) {
    for (String name : List.of("iam", "org")) {
      var role =
          PostgresExtension.serviceRole(
              AuditCombinedIntegrationTest.class, "audit_test_" + name, name);
      r.add("auriga.platform." + name + ".jdbc-url", role::jdbcUrl);
      r.add("auriga.platform." + name + ".username", role::role);
      r.add("auriga.platform." + name + ".password", () -> "");
    }
    var database = PostgresExtension.database(AuditCombinedIntegrationTest.class);
    try {
      var composeSql =
          java.nio.file.Files.readString(
              java.nio.file.Path.of("../../deploy/compose/postgres/audit.sql"));
      var nasSql =
          java.nio.file.Files.readString(
                  java.nio.file.Path.of("../../deploy/nas/postgres/audit.sql.tmpl"))
              .replace("__PG_AUDIT_OWNER_PASSWORD__", "e2e_audit_owner")
              .replace("__PG_AUDIT_APP_PASSWORD__", "e2e_audit_app");
      var bootstrap = new JdbcTemplate(database.dataSource());
      bootstrap.execute(composeSql);
      bootstrap.execute(nasSql); // Upgrade is idempotent, including across deployment recipes.
    } catch (java.io.IOException e) {
      throw new java.io.UncheckedIOException(e);
    }
    r.add(
        "auriga.audit.jdbc-url",
        () -> database.jdbcUrl().split(java.util.regex.Pattern.quote("?"))[0]);
    r.add("auriga.audit.username", () -> "auriga_audit_app");
    r.add("auriga.audit.password", () -> "e2e_audit_app");
    r.add("auriga.audit.migration-username", () -> "auriga_audit_owner");
    r.add("auriga.audit.migration-password", () -> "e2e_audit_owner");
    NatsExtension.properties(r);
  }

  @Configuration(proxyBeanMethods = false)
  @Profile("audit-integration")
  @EnableAutoConfiguration
  @Import({PlatformCombinedConfiguration.class, AuditModuleConfiguration.class})
  static class App {
    @Bean
    JwtDecoder decoder() {
      return AccessTokenDecoders.create(
          AccessTokenSettings.of(ISSUER, URI.create(ISSUER + "/oauth2/jwks")),
          new ImmutableJWKSet<>(new JWKSet(KEY.toPublicJWK())));
    }
  }

  String token(String ctx, boolean allowed) {
    try {
      var now = Instant.now();
      var claims =
          new JWTClaimsSet.Builder()
              .issuer(ISSUER)
              .audience("auriga")
              .subject("01900000-0000-7000-8000-000000000001")
              .claim("tid", TENANT.toString())
              .claim("ptyp", "user")
              .claim("ctx", ctx)
              .claim("sid", UUID.randomUUID().toString())
              .claim("mtyp", "member")
              .claim("perms", allowed ? List.of("audit.events.read") : List.of())
              .jwtID(UUID.randomUUID().toString())
              .issueTime(Date.from(now))
              .expirationTime(Date.from(now.plusSeconds(300)))
              .build();
      var jwt =
          new SignedJWT(
              new JWSHeader.Builder(JWSAlgorithm.ES256)
                  .type(new JOSEObjectType("at+jwt"))
                  .keyID(KEY.getKeyID())
                  .build(),
              claims);
      jwt.sign(new ECDSASigner(KEY));
      return jwt.serialize();
    } catch (JOSEException e) {
      throw new IllegalStateException(e);
    }
  }

  HttpResponse<String> get(String suffix, String bearer) throws Exception {
    var b =
        HttpRequest.newBuilder(
                URI.create("http://127.0.0.1:" + port + "/api/audit/v1/admin/events" + suffix))
            .timeout(Duration.ofSeconds(15))
            .GET();
    if (bearer != null) b.header("Authorization", "Bearer " + bearer);
    return HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(5))
        .build()
        .send(b.build(), HttpResponse.BodyHandlers.ofString());
  }

  @Test
  void sharedNatsOutboxToHttpAndDenialsArePersisted() throws Exception {
    assertEquals(1, context.getBeansOfType(Connection.class).size());
    assertEquals("audit-records", auditEventConsumer.durableName());
    assertTrue(auditEventConsumer.isRunning());
    TenantContext.run(
        TENANT,
        () ->
            new org.springframework.transaction.support.TransactionTemplate(orgTx)
                .executeWithoutResult(
                    s ->
                        orgEvents.publish(
                            "audit.recorded",
                            "target",
                            Map.of(
                                "actor",
                                Map.of("type", "user", "id", "alice"),
                                "action",
                                "org.member.update",
                                "result",
                                "success"))));
    String body = "";
    long end = System.nanoTime() + Duration.ofSeconds(20).toNanos();
    do {
      var page = get("?actor=alice", token("admin", true));
      assertEquals(200, page.statusCode(), page.body());
      body = page.body();
      if (body.contains("org.member.update")) break;
      Thread.sleep(100);
    } while (System.nanoTime() < end);
    assertTrue(body.contains("org.member.update"), body);
    String id = JSON.readTree(body).path("items").get(0).path("id").asString();
    assertEquals(200, get("/" + id, token("admin", true)).statusCode());
    assertTrue(
        get("/export?actor=alice", token("admin", true)).body().contains("org.member.update"));
    assertEquals(401, get("", null).statusCode());
    assertEquals(401, get("", "invalid").statusCode());
    assertEquals(403, get("", token("admin", false)).statusCode());
    assertEquals(403, get("", token("portal", true)).statusCode());
    var denied = get("?result=denied", token("admin", true));
    assertEquals(200, denied.statusCode(), denied.body());
    assertEquals(4, JSON.readTree(denied.body()).path("items").size(), denied.body());
    assertEquals(
        0,
        new JdbcTemplate(auditSource)
            .queryForObject("SELECT count(*) FROM audit.event", Integer.class));
  }

  @Test
  void onlyPlatformBuiltinAdministratorsReceivePermission() {
    var svc =
        new OrgService(
            new OrgStore(new JdbcTemplate(orgSource), orgTx), orgEvents, Clock.systemUTC());
    TenantContext.run(
        TENANT,
        () -> {
          var roles = svc.listRoles().toString();
          assertTrue(roles.contains("audit.events.read"));
          assertEquals(
              2,
              new org.springframework.transaction.support.TransactionTemplate(orgTx)
                  .execute(
                      status ->
                          new JdbcTemplate(orgSource)
                              .queryForObject(
                                  "SELECT count(*) FROM org.role WHERE builtin AND permissions @> ARRAY['audit.events.read']",
                                  Integer.class))
                  .intValue());
        });
    var other = UUID.fromString("00000000-0000-0000-0000-000000000002");
    TenantContext.run(
        other,
        () -> {
          assertFalse(svc.listPermissions().toString().contains("audit.events.read"));
        });
  }

  @Test
  void relayBusinessAudit() throws Exception {
    var database = PostgresExtension.database(AuditCombinedIntegrationTest.class);
    var dsn =
        database
                .jdbcUrl()
                .split(java.util.regex.Pattern.quote("?"))[0]
                .replace("jdbc:", "")
                .replace("postgresql://", "postgresql://postgres@")
            + "?sslmode=disable";
    var log = java.nio.file.Path.of("target/relay-audit-go.log");
    var command =
        new ProcessBuilder(
                "go",
                "test",
                "-v",
                "-count=1",
                "-timeout=180s",
                "-run",
                "TestRelayAudit",
                "./internal/bifrostbackend")
            .directory(java.nio.file.Path.of("../../infra/ai-gateway").toFile())
            .redirectErrorStream(true)
            .redirectOutput(log.toFile());
    command.environment().put("AURIGA_AI_TEST_PG_DSN", dsn);
    command
        .environment()
        .put("AURIGA_AUDIT_TEST_NATS_URL", context.getBean(Connection.class).getConnectedUrl());
    command
        .environment()
        .put("AURIGA_AUDIT_TEST_URL", "http://127.0.0.1:" + port + "/api/audit/v1/admin/events");
    command.environment().put("AURIGA_AUDIT_TEST_BEARER", token("admin", true));
    command.environment().put("GOMAXPROCS", "2");
    var process = command.start();
    try {
      assertTrue(
          process.waitFor(240, java.util.concurrent.TimeUnit.SECONDS),
          "Go audit integration timeout");
      assertEquals(0, process.exitValue(), java.nio.file.Files.readString(log));
    } finally {
      if (process.isAlive()) {
        process.descendants().forEach(p -> p.destroyForcibly());
        process.destroyForcibly();
      }
    }
  }

  static final UUID BUSINESS_ACTOR = UUID.fromString("01900000-0000-7000-8000-000000000001");

  <T> T business(java.util.function.Supplier<T> operation) {
    var principal =
        new AurigaPrincipal(
            BUSINESS_ACTOR.toString(),
            TENANT,
            PrincipalType.USER,
            TokenContext.ADMIN,
            UUID.randomUUID().toString(),
            null,
            "member",
            List.of("owner"),
            Set.of("audit.events.read"),
            Instant.now(),
            Instant.now(),
            "business-audit");
    return TenantContext.call(
        TENANT,
        () ->
            RequestContext.start(
                    "business-audit", "00-12345678901234567890123456789012-1234567890123456-01")
                .withClient("192.0.2.20", "fixture")
                .withPrincipal(principal)
                .call(operation::get));
  }

  OrgService businessOrg(UUID target) {
    var orgStore = new OrgStore(new JdbcTemplate(orgSource), orgTx);
    orgStore.tx(
        () -> {
          for (UUID id : List.of(BUSINESS_ACTOR, target))
            if (orgStore.member(id).isEmpty())
              orgStore.insertMember(
                  new OrgStore.MemberRow(
                      id,
                      "fixture",
                      "alice@example.com",
                      "member",
                      "active",
                      1,
                      Instant.now(),
                      null,
                      null));
          orgStore
              .jdbc()
              .update(
                  "INSERT INTO org.role_binding(id,tenant_id,role_id,subject_type,subject_id,created_at) SELECT ?,?,id,'member',?,now() FROM org.role WHERE tenant_id=? AND key='owner' ON CONFLICT DO NOTHING",
                  UuidV7.next(),
                  TENANT,
                  BUSINESS_ACTOR,
                  TENANT);
          return null;
        });
    try {
      var access =
          auriga.contracts.deploymentconfig.DeploymentAccess.load(
              java.nio.file.Files.readString(
                      java.nio.file.Path.of(
                          "../../contracts/deployment/fixtures/domain-proxy.json"))
                  .replace("019a0000-0000-7000-8000-000000000001", TENANT.toString())
                  .getBytes(java.nio.charset.StandardCharsets.UTF_8));
      return new OrgService(orgStore, orgEvents, Clock.systemUTC(), access);
    } catch (java.io.IOException e) {
      throw new java.io.UncheckedIOException(e);
    }
  }

  void awaitBusinessAudit(String action, String target) throws Exception {
    long deadline = System.nanoTime() + Duration.ofSeconds(20).toNanos();
    String response = "";
    do {
      var result = get("?action=" + action + "&q=" + target, token("admin", true));
      assertEquals(200, result.statusCode(), result.body());
      response = result.body();
      var items = JSON.readTree(response).path("items");
      if (!items.isEmpty()) {
        assertEquals(1, items.size(), response);
        var record = items.get(0);
        assertEquals(target, record.path("target").path("id").asString());
        assertEquals(BUSINESS_ACTOR.toString(), record.path("actor").path("id").asString());
        assertEquals("192.0.2.20", record.path("actor").path("ip").asString());
        assertEquals("12345678901234567890123456789012", record.path("traceId").asString());
        assertEquals("success", record.path("result").asString());
        assertFalse(response.contains("must-not-appear"));
        return;
      }
      Thread.sleep(50);
    } while (System.nanoTime() < deadline);
    fail("Business audit missing for " + action + ": " + response);
  }

  @Test
  void memberBusinessOperationBecomesQueryable() throws Exception {
    UUID target = UuidV7.next();
    business(
        () -> {
          businessOrg(target).updateMember(target, "password=must-not-appear", null, null, 1L);
          return null;
        });
    awaitBusinessAudit("org.member.update", target.toString());
  }

  @Test
  void invitationBusinessOperationsBecomeQueryableWithoutInvitationCode() throws Exception {
    UUID newcomer = UuidV7.next();
    var invitation =
        business(
            () -> {
              var org = businessOrg(UuidV7.next());
              var created =
                  org.createInvitation(
                      null,
                      "secret=must-not-appear",
                      "member",
                      List.of(),
                      List.of(),
                      List.of(),
                      Instant.now().plusSeconds(600),
                      3);
              var code = created.path("code").asString();
              org.redeemInvitation(code, newcomer, "alice", "alice@example.com", null);
              org.redeemInvitation(code, newcomer, "alice", "alice@example.com", null);
              org.revokeInvitation(UUID.fromString(created.path("id").asString()));
              return created;
            });
    for (String action : List.of("create", "redeem", "revoke"))
      awaitBusinessAudit("org.invitation." + action, invitation.path("id").asString());
    awaitBusinessAudit("org.member.create", newcomer.toString());
    var exported = get("/export?q=" + invitation.path("id").asString(), token("admin", true));
    assertFalse(exported.body().contains(invitation.path("code").asString()));
  }

  @Test
  void roleAndBindingBusinessOperationsBecomeQueryable() throws Exception {
    var ids =
        business(
            () -> {
              UUID member = UuidV7.next();
              var org = businessOrg(member);
              var name = JSON.createObjectNode().put("en", "Fixture").put("zh", "Fixture");
              UUID role =
                  UUID.fromString(
                      org.createRole("audit-" + member, name, null, List.of())
                          .path("id")
                          .asString());
              org.updateRole(role, name.deepCopy().put("en", "Updated"), null, null);
              UUID binding =
                  UUID.fromString(
                      org.createRoleBinding(role, "member", member).path("id").asString());
              org.deleteRoleBinding(binding);
              org.deleteRole(role);
              return List.of(role, binding);
            });
    for (String action : List.of("create", "update", "delete"))
      awaitBusinessAudit("org.role." + action, ids.get(0).toString());
    for (String action : List.of("create", "delete"))
      awaitBusinessAudit("org.role_binding." + action, ids.get(1).toString());
  }

  @Test
  void iamSecurityBusinessOperationsBecomeQueryable() throws Exception {
    var ids =
        business(
            () -> {
              var source = context.getBean("iamDataSource", DataSource.class);
              var iam =
                  new auriga.iam.IamStore(
                      new JdbcTemplate(source),
                      context.getBean("iamTransactionManager", TenantTransactionManager.class));
              var events = context.getBean("iamEventPublisher", EventPublisher.class);
              byte[] encryption = new byte[32];
              new java.security.SecureRandom().nextBytes(encryption);
              var accounts =
                  new auriga.iam.TeamAccounts(
                      iam,
                      new auriga.iam.Secrets(Base64.getEncoder().encodeToString(encryption)),
                      Clock.systemUTC(),
                      events);
              iam.tx(
                  () -> {
                    if (iam.get("account", BUSINESS_ACTOR.toString(), false) == null)
                      iam.insert(
                          "account",
                          BUSINESS_ACTOR.toString(),
                          JSON.createObjectNode()
                              .put("status", "active")
                              .put("memberType", "member"),
                          null);
                    return null;
                  });
              var enrollment =
                  iam.tx(() -> accounts.beginTotpEnrollment(BUSINESS_ACTOR, "example.com"));
              String secret = enrollment.path("secret").asString();
              String code = auriga.iam.Totp.code(secret, Instant.now().getEpochSecond() / 30);
              iam.tx(
                  () -> {
                    accounts.confirmTotpEnrollment(
                        BUSINESS_ACTOR, enrollment.path("enrollmentId").asString(), code);
                    accounts.deleteTotp(BUSINESS_ACTOR, false);
                    return null;
                  });
              var sessions =
                  new auriga.iam.SessionManager(
                      iam,
                      org.mockito.Mockito.mock(auriga.iam.SessionKv.class),
                      org.mockito.Mockito.mock(auriga.iam.TokenService.class),
                      Clock.systemUTC(),
                      events,
                      new auriga.common.audit.OutboxAuditLogger(events));
              UUID provider = UuidV7.next();
              iam.tx(
                  () ->
                      iam.insert(
                          "idp_provider",
                          provider.toString(),
                          JSON.createObjectNode()
                              .put("enabled", true)
                              .put("connectorType", "team-account"),
                          null));
              var issued =
                  iam.tx(
                      () ->
                          sessions.create(
                              BUSINESS_ACTOR,
                              provider,
                              "admin",
                              false,
                              Duration.ofHours(1),
                              Duration.ofMinutes(30)));
              sessions.stepUp(issued.session());
              UUID device = UuidV7.next();
              iam.tx(
                  () ->
                      iam.insert(
                          "device",
                          device.toString(),
                          JSON.createObjectNode()
                              .put("accountId", BUSINESS_ACTOR.toString())
                              .put("revoked", false),
                          null));
              var revoke =
                  auriga.contracts.iam.v1.RevokeSessionsRequest.newBuilder()
                      .setTenantId(TENANT.toString())
                      .setAccountId(BUSINESS_ACTOR.toString())
                      .setIncludeDevices(true)
                      .setReason(auriga.contracts.iam.v1.RevokeReason.REVOKE_REASON_ADMIN)
                      .build();
              sessions.revoke(revoke);
              sessions.revoke(revoke);
              return List.of(
                  issued.session().path("sessionId").asString(), device.toString(), secret);
            });
    awaitBusinessAudit("iam.mfa.totp.add", BUSINESS_ACTOR.toString());
    awaitBusinessAudit("iam.mfa.totp.remove", BUSINESS_ACTOR.toString());
    awaitBusinessAudit("iam.session.step_up", ids.get(0));
    awaitBusinessAudit("iam.device.revoke", ids.get(1));
    assertFalse(
        get("/export?action=iam.mfa.totp.add", token("admin", true)).body().contains(ids.get(2)));
  }
}
