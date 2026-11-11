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
}
