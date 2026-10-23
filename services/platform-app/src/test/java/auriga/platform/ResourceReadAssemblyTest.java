package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.org.OrgService;
import auriga.org.OrgStore;
import auriga.resource.ResourceModuleConfiguration;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.ECDSASigner;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jose.jwk.source.ImmutableJWKSet;
import com.nimbusds.jwt.*;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.time.*;
import java.util.*;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.beans.factory.annotation.*;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.annotation.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.oauth2.jwt.*;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/** Real HTTP + signed tokens + org/resource PostgreSQL with independent runtime/migration roles. */
@SpringBootTest(
    classes = ResourceReadAssemblyTest.App.class,
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = {
      "auriga.platform.combined=true",
      "auriga.services.resource.enabled=true",
      "server.address=127.0.0.1",
      "auriga.logging.json=false",
      "auriga.grpc.server.enabled=false",
      "spring.autoconfigure.exclude=org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration,org.springframework.boot.flyway.autoconfigure.FlywayAutoConfiguration,auriga.common.autoconfigure.AurigaPersistenceAutoConfiguration,auriga.common.autoconfigure.AurigaEventsAutoConfiguration"
    })
@ExtendWith(PostgresExtension.class)
@DirtiesContext
class ResourceReadAssemblyTest {
  static final UUID T1 = UUID.fromString("00000000-0000-0000-0000-000000000001");
  static final UUID T2 = UUID.fromString("00000000-0000-0000-0000-000000000002");
  static final UUID ALICE = id(10), BOB = id(11), EXTERNAL = id(12), GROUP = id(13), ROLE = id(14);
  static final ECKey KEY = key();
  static final JsonMapper JSON = JsonMapper.builder().build();
  static final String ISSUER = "https://auth.example.com";

  @Value("${local.server.port}")
  int port;

  @Autowired
  @Qualifier("resourceDataSource")
  DataSource resourceDataSource;

  @Autowired
  @Qualifier("orgDataSource")
  DataSource orgDataSource;

  @Autowired
  @Qualifier("resourceTransactionManager")
  TenantTransactionManager resourceTransactions;

  @Autowired org.springframework.context.ApplicationContext context;
  JdbcTemplate admin;
  org.springframework.jdbc.datasource.SingleConnectionDataSource adminConnection;
  HttpClient http;

  static UUID id(int number) {
    return UUID.fromString(String.format("01900000-0000-7000-8000-%012d", number));
  }

  static ECKey key() {
    try {
      return new ECKeyGenerator(Curve.P_256).keyID("resource-test").generate();
    } catch (JOSEException e) {
      throw new IllegalStateException(e);
    }
  }

  static String randomKey() {
    byte[] bytes = new byte[32];
    new SecureRandom().nextBytes(bytes);
    return Base64.getEncoder().encodeToString(bytes);
  }

  @DynamicPropertySource
  static void properties(DynamicPropertyRegistry registry) {
    var roles =
        PostgresExtension.serviceRoles(
            ResourceReadAssemblyTest.class, "resource_owner", "resource_app", "resource");
    registry.add(
        "auriga.platform.resource.jdbc-url",
        () -> PostgresExtension.database(ResourceReadAssemblyTest.class).jdbcUrl().split("\\?")[0]);
    registry.add("auriga.platform.resource.username", () -> roles.runtime().role());
    registry.add("auriga.platform.resource.password", () -> "");
    registry.add("auriga.platform.resource.migration-username", () -> roles.owner().role());
    registry.add("auriga.platform.resource.migration-password", () -> "");
    registry.add("auriga.resource.cursor-signing-key", ResourceReadAssemblyTest::randomKey);
  }

  // Explicit lite configuration: not a scanned component or a discoverable Boot application root.
  @EnableAutoConfiguration
  @Import({ResourceModuleConfiguration.class, PlatformResourceConfiguration.class})
  static class App {
    @Bean
    Clock clock() {
      return Clock.systemUTC();
    }

    @Bean(name = "orgDataSource")
    DataSource orgDataSource() {
      var roles =
          PostgresExtension.serviceRoles(
              ResourceReadAssemblyTest.class,
              "resource_test_org_owner",
              "resource_test_org_app",
              "org");
      CommonMigrations.migrate(roles.owner().dataSource(), "org");
      CommonMigrations.serviceFlyway(
              Flyway.configure()
                  .dataSource(roles.owner().dataSource())
                  .schemas("org")
                  .locations("classpath:db/org")
                  .load())
          .migrate();
      var config = new com.zaxxer.hikari.HikariConfig();
      config.setDataSource(roles.runtime().dataSource());
      config.setPoolName("resource-test-org");
      config.setMaximumPoolSize(2);
      config.setMinimumIdle(0);
      return new com.zaxxer.hikari.HikariDataSource(config);
    }

    @Bean
    OrgService orgService(@Qualifier("orgDataSource") DataSource source, Clock clock) {
      return new OrgService(
          new OrgStore(new JdbcTemplate(source), new TenantTransactionManager(source)),
          new OutboxEventPublisher(
              new JdbcTemplate(source), "org", "org", new EventSchemas(), JSON, clock, () -> {}),
          clock);
    }

    @Bean
    JwtDecoder decoder() {
      return auriga.common.security.AccessTokenDecoders.create(
          auriga.common.security.AccessTokenSettings.of(
              ISSUER, URI.create(ISSUER + "/oauth2/jwks")),
          new ImmutableJWKSet<>(new JWKSet(KEY.toPublicJWK())));
    }
  }

  @BeforeEach
  void setup() throws java.sql.SQLException {
    http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();
    // Reuse a real fixture connection instead of starting a Windows PG backend for each statement.
    adminConnection =
        new org.springframework.jdbc.datasource.SingleConnectionDataSource(
            PostgresExtension.database(getClass()).dataSource().getConnection(), true);
    admin = new JdbcTemplate(adminConnection);
    admin.execute(
        "TRUNCATE resource.resource_package_creation, resource.resource_package_group, resource.resource_package, resource.resource_version CASCADE");
    admin.execute(
        "DELETE FROM org.role_binding; DELETE FROM org.group_member; DELETE FROM org.member; DELETE FROM org.role WHERE builtin=false; DELETE FROM org.\"group\"");
    admin.update(
        "INSERT INTO org.tenant(id,tenant_id,slug,display_name) VALUES(?,?,?,?) ON CONFLICT DO NOTHING",
        T2,
        T2,
        "example",
        "Example tenant");
    for (UUID tenant : List.of(T1, T2)) {
      for (UUID account : List.of(ALICE, BOB, EXTERNAL)) {
        // id is globally unique, so second-tenant actors use a separate account below.
        if (tenant.equals(T2)) continue;
        admin.update(
            "INSERT INTO org.member(id,tenant_id,display_name,member_type,status) VALUES(?,?,?,?, 'active')",
            account,
            tenant,
            "Example member",
            account.equals(EXTERNAL) ? "external" : "member");
      }
    }
    admin.update(
        "INSERT INTO org.member(id,tenant_id,display_name,member_type,status) VALUES(?,?,?,'member','active')",
        id(15),
        T2,
        "Example member");
    admin.update(
        "INSERT INTO org.role(id,tenant_id,key,name_en,name_zh,permissions) VALUES(?,?,'resource-test','Example','示例',ARRAY['resource.packages.read'])",
        ROLE,
        T1);
    admin.update(
        "INSERT INTO org.role(id,tenant_id,key,name_en,name_zh,permissions) VALUES(?,?,'resource-test','Example','示例',ARRAY['resource.packages.read'])",
        id(16),
        T2);
    for (UUID account : List.of(ALICE, BOB, EXTERNAL))
      admin.update(
          "INSERT INTO org.role_binding(id,tenant_id,role_id,subject_type,subject_id) VALUES(?,?,?,'member',?)",
          UuidV7.next(),
          T1,
          ROLE,
          account);
    admin.update(
        "INSERT INTO org.role_binding(id,tenant_id,role_id,subject_type,subject_id) VALUES(?,?,?,'member',?)",
        UuidV7.next(),
        T2,
        id(16),
        id(15));
    admin.update(
        "INSERT INTO org.\"group\"(id,tenant_id,name) VALUES(?,?,'Example group')", GROUP, T1);
    admin.update(
        "INSERT INTO org.group_member(id,tenant_id,group_id,account_id) VALUES(?,?,?,?)",
        UuidV7.next(),
        T1,
        GROUP,
        ALICE);
    for (UUID tenant : List.of(T1, T2))
      admin.update(
          "INSERT INTO org.policy(id,tenant_id,key,value) VALUES(?,?,'workspace','{\"allowPersonalPlugins\":true}') ON CONFLICT(tenant_id,key) DO UPDATE SET value=EXCLUDED.value",
          tenant.equals(T1) ? id(18) : id(17),
          tenant);
  }

  @AfterEach
  void close() {
    http.close();
    if (adminConnection != null) adminConnection.destroy();
  }

  UUID resource(int n, String level, String visibility, UUID owner, UUID tenant, boolean enabled) {
    UUID packageId = id(n);
    admin.update(
        "INSERT INTO resource.resource_package(id,tenant_id,owner_id,name,level,visibility,enabled,revision,created_at) VALUES(?,?,?,?,?,?,?,1,now())",
        packageId,
        tenant,
        owner,
        "Example plugin " + n,
        level,
        visibility,
        enabled);
    return packageId;
  }

  UUID version(int n, UUID packageId, UUID author, String state) {
    UUID versionId = id(n);
    UUID tenant =
        admin.queryForObject(
            "SELECT tenant_id FROM resource.resource_package WHERE id=?", UUID.class, packageId);
    admin.update(
        "INSERT INTO resource.resource_version(id,tenant_id,package_id,author_id,version,digest,size_bytes,state,revision,created_at) VALUES(?,?,?,?,?,?,100,?,1,now())",
        versionId,
        tenant,
        packageId,
        author,
        "v" + n,
        "sha256:" + "a".repeat(64),
        state);
    return versionId;
  }

  String token(UUID tenant, UUID account, boolean permission) {
    try {
      Instant now = Instant.now();
      var claims =
          new JWTClaimsSet.Builder()
              .issuer(ISSUER)
              .audience("auriga")
              .subject(account.toString())
              .claim("tid", tenant.toString())
              .claim("ptyp", "user")
              .claim("ctx", "portal")
              .claim("sid", UUID.randomUUID().toString())
              .claim("mtyp", "member")
              .claim("perms", permission ? List.of("resource.packages.read") : List.of())
              .jwtID(UUID.randomUUID().toString())
              .issueTime(Date.from(now))
              .expirationTime(Date.from(now.plusSeconds(300)))
              .build();
      var signed =
          new SignedJWT(
              new JWSHeader.Builder(JWSAlgorithm.ES256)
                  .type(new JOSEObjectType("at+jwt"))
                  .keyID(KEY.getKeyID())
                  .build(),
              claims);
      signed.sign(new ECDSASigner(KEY));
      return signed.serialize();
    } catch (JOSEException e) {
      throw new IllegalStateException(e);
    }
  }

  HttpResponse<String> request(String path, String token) throws Exception {
    var builder =
        HttpRequest.newBuilder(
                URI.create("http://127.0.0.1:" + port + "/api/resource/v1/packages" + path))
            .timeout(Duration.ofSeconds(10))
            .GET();
    if (token != null) builder.header("Authorization", "Bearer " + token);
    return http.send(builder.build(), HttpResponse.BodyHandlers.ofString());
  }

  JsonNode get(String path, UUID account) throws Exception {
    var response = request(path, token(T1, account, true));
    assertEquals(200, response.statusCode(), response.body());
    assertEquals("no-store", response.headers().firstValue("cache-control").orElse(""));
    return JSON.readTree(response.body());
  }

  void denied(String path, String bearer, int status, String code) throws Exception {
    var response = request(path, bearer);
    assertEquals(status, response.statusCode(), response.body());
    assertEquals(code, JSON.readTree(response.body()).path("code").asString());
  }

  List<String> ids(JsonNode page) {
    var result = new ArrayList<String>();
    page.path("items").forEach(item -> result.add(item.path("id").asString()));
    return result;
  }

  String cursor(JsonNode page) {
    return URLEncoder.encode(page.path("nextCursor").asString(), StandardCharsets.UTF_8);
  }

  @Test
  void fourGetsReturnPersistedMetadataWithEtagsAndNullableDefault() throws Exception {
    var packageId = resource(100, "l3", "tenant", ALICE, T1, true);
    var versionId = version(200, packageId, ALICE, "published");
    var first = get("/" + packageId, BOB);
    assertTrue(first.has("defaultVersionId"), first.toString());
    assertTrue(first.get("defaultVersionId").isNull());
    admin.update(
        "UPDATE resource.resource_package SET default_version_id=? WHERE id=?",
        versionId,
        packageId);
    assertEquals(packageId.toString(), get("", BOB).path("items").get(0).path("id").asString());
    assertEquals(
        versionId.toString(), get("/" + packageId, BOB).path("defaultVersionId").asString());
    assertEquals(
        versionId.toString(),
        get("/" + packageId + "/versions", BOB).path("items").get(0).path("id").asString());
    var response = request("/" + packageId + "/versions/" + versionId, token(T1, BOB, true));
    assertEquals(200, response.statusCode(), response.body());
    assertEquals("\"1\"", response.headers().firstValue("etag").orElse(""));
    assertEquals(
        "sha256:" + "a".repeat(64), JSON.readTree(response.body()).path("digest").asString());
    assertFalse(JSON.readTree(response.body()).has("resourceEnvelope"));
    assertFalse(JSON.readTree(response.body()).has("releaseSequence"));
    assertFalse(get("", BOB).has("nextCursor"));
    assertFalse(get("/" + packageId + "/versions", BOB).has("nextCursor"));
  }

  @Test
  void visibilityFiltersBeforePaginationAndDetailsUseTheSameAcl() throws Exception {
    var tenant = resource(100, "l0", "tenant", BOB, T1, true);
    var member = resource(101, "l1", "members", BOB, T1, true);
    var group = resource(102, "l2", "groups", BOB, T1, false);
    var privateId = resource(103, "l3", "private", BOB, T1, true);
    admin.update(
        "INSERT INTO resource.resource_package_group(id,tenant_id,package_id,group_id) VALUES(?,?,?,?)",
        UuidV7.next(),
        T1,
        group,
        GROUP);
    assertEquals(
        List.of(tenant.toString(), member.toString(), group.toString()), ids(get("", ALICE)));
    assertEquals(List.of(tenant.toString()), ids(get("", EXTERNAL)));
    assertEquals(
        List.of(tenant.toString(), member.toString(), privateId.toString()), ids(get("", BOB)));
    assertFalse(get("/" + group, ALICE).path("enabled").asBoolean());
    denied("/" + privateId, token(T1, ALICE, true), 404, "common.not_found");
    denied("/" + group, token(T1, BOB, true), 404, "common.not_found");
    denied("/" + member, token(T1, EXTERNAL, true), 404, "common.not_found");
  }

  @Test
  void l4IsOwnerOnlyAndPolicyChangesApplyOnTheNextRead() throws Exception {
    var personal = resource(100, "l4", "private", ALICE, T1, true);
    assertEquals(List.of(personal.toString()), ids(get("", ALICE)));
    denied("/" + personal, token(T1, BOB, true), 404, "common.not_found");
    admin.update(
        "UPDATE org.policy SET value=jsonb_set(value,'{allowPersonalPlugins}','false') WHERE tenant_id=? AND key='workspace'",
        T1);
    assertTrue(ids(get("", ALICE)).isEmpty());
    denied("/" + personal, token(T1, ALICE, true), 404, "common.not_found");
  }

  @Test
  void groupRemovalRevokesExistingTokensAndVersionAccess() throws Exception {
    var group = resource(100, "l2", "groups", BOB, T1, true);
    var versionId = version(200, group, BOB, "published");
    admin.update(
        "INSERT INTO resource.resource_package_group(id,tenant_id,package_id,group_id) VALUES(?,?,?,?)",
        UuidV7.next(),
        T1,
        group,
        GROUP);
    String bearer = token(T1, ALICE, true);
    assertEquals(200, request("/" + group, bearer).statusCode());
    admin.update("DELETE FROM org.group_member WHERE tenant_id=? AND account_id=?", T1, ALICE);
    for (String path :
        List.of("/" + group, "/" + group + "/versions", "/" + group + "/versions/" + versionId))
      denied(path, bearer, 404, "common.not_found");
    assertTrue(ids(get("", ALICE)).isEmpty());
  }

  @Test
  void livePermissionAndMemberStatusOverrideAStillValidJwt() throws Exception {
    String bearer = token(T1, ALICE, true);
    assertEquals(200, request("", bearer).statusCode());
    admin.update("DELETE FROM org.role_binding WHERE tenant_id=? AND subject_id=?", T1, ALICE);
    denied("", bearer, 403, "common.forbidden");
    admin.update("UPDATE org.member SET status='disabled' WHERE id=?", BOB);
    denied("", token(T1, BOB, true), 403, "common.forbidden");
    admin.update("DELETE FROM org.member WHERE id=?", EXTERNAL);
    denied("", token(T1, EXTERNAL, true), 403, "common.forbidden");
  }

  @Test
  void anonymousInvalidTokenAndMissingPermissionFailClosed() throws Exception {
    denied("", null, 401, "common.unauthenticated");
    denied("", "invalid", 401, "common.unauthenticated");
    denied("", token(T1, ALICE, false), 403, "common.forbidden");
  }

  @Test
  void tenantsCannotReadEachOthersObjectsOrCrossPackageVersions() throws Exception {
    var mine = resource(100, "l3", "tenant", ALICE, T1, true);
    var other = resource(101, "l3", "tenant", id(15), T2, true);
    var otherVersion = version(200, other, id(15), "published");
    denied("/" + other, token(T1, ALICE, true), 404, "common.not_found");
    denied("/" + other + "/versions", token(T1, ALICE, true), 404, "common.not_found");
    denied(
        "/" + mine + "/versions/" + otherVersion, token(T1, ALICE, true), 404, "common.not_found");
    denied("/" + mine, token(T2, id(15), true), 404, "common.not_found");
    assertEquals(List.of(mine.toString()), ids(get("", ALICE)));
  }

  @Test
  void unpublishedVersionsAreAuthorOnlyAndDefaultDoesNotLeakTheirIds() throws Exception {
    var packageId = resource(100, "l3", "tenant", ALICE, T1, true);
    var published = version(200, packageId, ALICE, "published");
    var deprecated = version(201, packageId, ALICE, "deprecated");
    var withdrawn = version(202, packageId, ALICE, "withdrawn");
    var draft = version(203, packageId, ALICE, "draft");
    version(204, packageId, ALICE, "pending_review");
    version(205, packageId, ALICE, "approved");
    version(206, packageId, ALICE, "rejected");
    admin.update(
        "UPDATE resource.resource_package SET default_version_id=? WHERE id=?", draft, packageId);
    assertEquals(
        List.of(published.toString(), deprecated.toString(), withdrawn.toString()),
        ids(get("/" + packageId + "/versions", BOB)));
    assertEquals(7, ids(get("/" + packageId + "/versions", ALICE)).size());
    assertTrue(get("/" + packageId, BOB).path("defaultVersionId").isNull());
    denied("/" + packageId + "/versions/" + draft, token(T1, BOB, true), 404, "common.not_found");
  }

  @Test
  void packagePaginationIsDeterministicScopedAndDoesNotIncludeLaterInserts() throws Exception {
    resource(99, "l3", "private", BOB, T1, true);
    var a = resource(100, "l3", "tenant", ALICE, T1, true);
    var b = resource(101, "l3", "tenant", ALICE, T1, true);
    var c = resource(102, "l3", "tenant", ALICE, T1, true);
    var first = get("?limit=1", ALICE);
    assertEquals(List.of(a.toString()), ids(first));
    resource(103, "l3", "tenant", ALICE, T1, true);
    var second = get("?limit=1&cursor=" + cursor(first), ALICE);
    var last = get("?limit=1&cursor=" + cursor(second), ALICE);
    assertEquals(List.of(b.toString()), ids(second));
    assertEquals(List.of(c.toString()), ids(last));
    assertFalse(last.has("nextCursor"));
    assertEquals(ids(second), ids(get("?limit=1&cursor=" + cursor(first), ALICE)));
    denied(
        "?limit=1&cursor=" + cursor(first), token(T1, BOB, true), 400, "common.validation_failed");
    denied(
        "?limit=1&cursor=" + cursor(first),
        token(T2, id(15), true),
        400,
        "common.validation_failed");
    denied(
        "?limit=2&cursor=" + cursor(first),
        token(T1, ALICE, true),
        400,
        "common.validation_failed");
  }

  @Test
  void versionCursorsAreBoundToTheirPackageAndEndpoint() throws Exception {
    var a = resource(100, "l3", "tenant", ALICE, T1, true);
    var b = resource(101, "l3", "tenant", ALICE, T1, true);
    var v1 = version(200, a, ALICE, "published");
    var v2 = version(201, a, ALICE, "published");
    var first = get("/" + a + "/versions?limit=1", BOB);
    assertEquals(List.of(v1.toString()), ids(first));
    assertEquals(
        List.of(v2.toString()),
        ids(get("/" + a + "/versions?limit=1&cursor=" + cursor(first), BOB)));
    denied(
        "/" + b + "/versions?limit=1&cursor=" + cursor(first),
        token(T1, BOB, true),
        400,
        "common.validation_failed");
    denied(
        "?limit=1&cursor=" + cursor(first), token(T1, BOB, true), 400, "common.validation_failed");
  }

  @Test
  void malformedLimitsIdsAndCursorsAreRejected() throws Exception {
    for (String path :
        List.of(
            "?limit=0",
            "?limit=201",
            "?limit=abc",
            "/not-a-uuid",
            "?cursor=bad",
            "?cursor=" + "a".repeat(2049)))
      denied(path, token(T1, ALICE, true), 400, "common.validation_failed");
    assertTrue(ids(get("?cursor=", ALICE)).isEmpty());
  }

  @Test
  void missingOrgPolicyFailsClosedWithoutReturningResourceMetadata() throws Exception {
    resource(100, "l3", "tenant", ALICE, T1, true);
    admin.update("DELETE FROM org.policy WHERE tenant_id=? AND key='workspace'", T1);
    denied("", token(T1, ALICE, true), 503, "common.unavailable");
  }

  @Test
  void runtimeRoleCannotBypassRlsOrAccessOrgSchema() {
    resource(100, "l3", "tenant", ALICE, T1, true);
    resource(101, "l3", "tenant", id(15), T2, true);
    var runtime = new JdbcTemplate(resourceDataSource);
    assertEquals(
        0, runtime.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
    TenantContext.run(
        T1,
        () ->
            new org.springframework.transaction.support.TransactionTemplate(resourceTransactions)
                .execute(
                    status -> {
                      assertEquals(
                          1,
                          runtime.queryForObject(
                              "SELECT count(*) FROM resource.resource_package", Integer.class));
                      assertThrows(
                          org.springframework.dao.DataAccessException.class,
                          () ->
                              runtime.queryForObject(
                                  "SELECT count(*) FROM org.member", Integer.class));
                      return null;
                    }));
    assertEquals(
        0, runtime.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
    assertFalse(
        runtime.queryForObject(
            "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user",
            Boolean.class));
    assertThrows(
        org.springframework.dao.DataAccessException.class,
        () -> runtime.execute("ALTER TABLE resource.resource_package DISABLE ROW LEVEL SECURITY"));
    assertNotSame(resourceDataSource, orgDataSource);
  }

  @Test
  void resourceHttpIsExplicitlyEnabledAndReadPermissionCannotWriteOrDownload() throws Exception {
    assertNotNull(context.getBean(auriga.resource.web.ResourceController.class));
    var response =
        http.send(
            HttpRequest.newBuilder(
                    URI.create("http://127.0.0.1:" + port + "/api/resource/v1/packages"))
                .header("Authorization", "Bearer " + token(T1, ALICE, true))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString("{}"))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(403, response.statusCode(), response.body());
    denied(
        "/" + id(100) + "/versions/" + id(200) + "/content",
        token(T1, ALICE, true),
        404,
        "common.not_found");
  }

  @Test
  void clientSuppliedTenantHeadersCannotOverrideTheSignedTenant() throws Exception {
    var other = resource(100, "l3", "tenant", id(15), T2, true);
    var response =
        http.send(
            HttpRequest.newBuilder(
                    URI.create("http://127.0.0.1:" + port + "/api/resource/v1/packages/" + other))
                .header("Authorization", "Bearer " + token(T1, ALICE, true))
                .header("Auriga-Tenant-Id", T2.toString())
                .header("X-Tenant-Id", T2.toString())
                .GET()
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(404, response.statusCode(), response.body());
    assertEquals("common.not_found", JSON.readTree(response.body()).path("code").asString());
  }

  @Test
  void aclIsRecheckedWhenReusingAnExistingCursor() throws Exception {
    resource(100, "l3", "tenant", BOB, T1, true);
    var group = resource(101, "l2", "groups", BOB, T1, true);
    admin.update(
        "INSERT INTO resource.resource_package_group(id,tenant_id,package_id,group_id) VALUES(?,?,?,?)",
        UuidV7.next(),
        T1,
        group,
        GROUP);
    var first = get("?limit=1", ALICE);
    assertTrue(first.hasNonNull("nextCursor"));
    admin.update("DELETE FROM org.group_member WHERE tenant_id=? AND account_id=?", T1, ALICE);
    assertTrue(ids(get("?limit=1&cursor=" + cursor(first), ALICE)).isEmpty());
  }

  @Test
  void databaseConstraintsRejectL4SharingAndCrossTenantVersionReferences() {
    assertThrows(
        org.springframework.dao.DataIntegrityViolationException.class,
        () -> resource(100, "l4", "tenant", ALICE, T1, true));
    var mine = resource(101, "l3", "tenant", ALICE, T1, true);
    var other = resource(102, "l3", "tenant", id(15), T2, true);
    var otherVersion = version(200, other, id(15), "published");
    assertThrows(
        org.springframework.dao.DataIntegrityViolationException.class,
        () ->
            admin.update(
                "UPDATE resource.resource_package SET default_version_id=? WHERE id=?",
                otherVersion,
                mine));
    assertThrows(
        org.springframework.dao.DataIntegrityViolationException.class,
        () ->
            admin.update(
                "INSERT INTO resource.resource_package_group(id,tenant_id,package_id,group_id) VALUES(?,?,?,?)",
                UuidV7.next(),
                T2,
                mine,
                GROUP));
    for (String table :
        List.of(
            "resource_package",
            "resource_version",
            "resource_package_group",
            "resource_package_creation"))
      assertTrue(
          admin.queryForObject(
              "SELECT relrowsecurity AND relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='resource' AND c.relname=?",
              Boolean.class,
              table));
  }

  @Test
  void startupRejectsOwnerOrSuperuserAsTheRuntimeRole() {
    var env =
        new org.springframework.mock.env.MockEnvironment()
            .withProperty(
                "auriga.platform.resource.jdbc-url",
                PostgresExtension.database(getClass()).jdbcUrl().split("\\?")[0])
            .withProperty("auriga.platform.resource.migration-username", "resource_owner")
            .withProperty("auriga.platform.resource.migration-password", "")
            .withProperty("auriga.platform.resource.password", "");
    for (String role : List.of("resource_owner", "postgres")) {
      env.withProperty("auriga.platform.resource.username", role);
      assertThrows(
          IllegalStateException.class,
          () -> new PlatformResourceConfiguration().resourceDataSource(env));
    }
    admin.execute("GRANT resource_owner TO resource_app");
    try {
      env.withProperty("auriga.platform.resource.username", "resource_app");
      assertThrows(
          IllegalStateException.class,
          () -> new PlatformResourceConfiguration().resourceDataSource(env));
    } finally {
      admin.execute("REVOKE resource_owner FROM resource_app");
    }
  }

  void grantWrites() {
    admin.execute(
        "UPDATE org.role SET permissions=ARRAY['resource.packages.read','resource.packages.upload'] WHERE key='resource-test'");
  }

  String writer(UUID tenant, UUID account) throws Exception {
    return writer(tenant, account, "user", "portal", Instant.now().getEpochSecond());
  }

  String writer(UUID tenant, UUID account, String type, String ctx, Long stepUp) throws Exception {
    var claims =
        new JWTClaimsSet.Builder(SignedJWT.parse(token(tenant, account, true)).getJWTClaimsSet())
            .claim("perms", List.of("resource.packages.read", "resource.packages.upload"))
            .claim("ptyp", type)
            .claim("ctx", ctx)
            .claim("stepup_time", stepUp);
    if (ctx.equals("device")) claims.claim("did", id(900).toString());
    var signed =
        new SignedJWT(
            new JWSHeader.Builder(JWSAlgorithm.ES256)
                .type(new JOSEObjectType("at+jwt"))
                .keyID(KEY.getKeyID())
                .build(),
            claims.build());
    signed.sign(new ECDSASigner(KEY));
    return signed.serialize();
  }

  HttpResponse<String> write(
      String method, String path, String bearer, String body, String key, String match)
      throws Exception {
    var builder =
        HttpRequest.newBuilder(
                URI.create("http://127.0.0.1:" + port + "/api/resource/v1/packages" + path))
            .timeout(Duration.ofSeconds(30))
            .header("Content-Type", "application/json")
            .method(method, HttpRequest.BodyPublishers.ofString(body));
    if (bearer != null) builder.header("Authorization", "Bearer " + bearer);
    if (key != null) builder.header("Idempotency-Key", key);
    if (match != null) builder.header("If-Match", match);
    return http.send(builder.build(), HttpResponse.BodyHandlers.ofString());
  }

  String input(String name, String visibility, UUID... groups) {
    return JSON.writeValueAsString(
        Map.of(
            "name", name, "kind", "plugin", "visibility", visibility, "groupIds", List.of(groups)));
  }

  JsonNode result(HttpResponse<String> response, int status) {
    assertEquals(status, response.statusCode(), response.body());
    return JSON.readTree(response.body());
  }

  void failure(HttpResponse<String> response, int status, String code) {
    assertEquals(code, result(response, status).path("code").asString());
  }

  JsonNode create(String name, String visibility, UUID... groups) throws Exception {
    return result(
        write(
            "POST",
            "",
            writer(T1, ALICE),
            input(name, visibility, groups),
            UUID.randomUUID().toString(),
            null),
        201);
  }

  @Test
  void realCreateReadPatchReadPreservesServerFieldsAndUpdatesAclAtomically() throws Exception {
    grantWrites();
    var created = create("Example plugin", "tenant");
    String packageId = created.path("id").asString();
    assertEquals(T1.toString(), created.path("tenantId").asString());
    assertEquals(ALICE.toString(), created.path("ownerId").asString());
    assertEquals("l3", created.path("level").asString());
    assertFalse(created.path("enabled").asBoolean());
    assertEquals(1, created.path("revision").asLong());
    assertTrue(created.has("defaultVersionId") && created.get("defaultVersionId").isNull());
    assertEquals(created, get("/" + packageId, BOB));
    assertTrue(get("/" + packageId + "/versions", ALICE).path("items").isEmpty());
    var patched =
        write(
            "PATCH",
            "/" + packageId,
            writer(T1, ALICE),
            JSON.writeValueAsString(
                Map.of(
                    "name", "Renamed plugin", "visibility", "groups", "groupIds", List.of(GROUP))),
            null,
            "\"1\"");
    var changed = result(patched, 200);
    assertEquals("\"2\"", patched.headers().firstValue("etag").orElse(""));
    assertEquals("no-store", patched.headers().firstValue("cache-control").orElse(""));
    assertEquals(2, changed.path("revision").asLong());
    assertEquals("Renamed plugin", get("/" + packageId, ALICE).path("name").asString());
    denied("/" + packageId, token(T1, BOB, true), 404, "common.not_found");
    result(
        write(
            "PATCH",
            "/" + packageId,
            writer(T1, ALICE),
            "{\"visibility\":\"tenant\"}",
            null,
            "\"2\""),
        200);
    assertTrue(get("/" + packageId, BOB).path("groupIds").isEmpty());
    assertEquals(
        0,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_package_group", Integer.class));
    assertEquals(
        0, admin.queryForObject("SELECT count(*) FROM resource.resource_version", Integer.class));
    assertEquals(
        0, admin.queryForObject("SELECT count(*) FROM resource.outbox_event", Integer.class));
  }

  @Test
  void metadataWritesRequireLiveUploadAndRecentStepUpEvenOnReplay() throws Exception {
    grantWrites();
    String body = input("Example", "tenant"), bearer = writer(T1, ALICE);
    failure(write("POST", "", null, body, "auth", null), 401, "common.unauthenticated");
    failure(write("POST", "", token(T1, ALICE, true), body, "auth", null), 403, "common.forbidden");
    for (Long time :
        new Long[] {
          null,
          Instant.now().minusSeconds(601).getEpochSecond(),
          Instant.now().plusSeconds(60).getEpochSecond()
        })
      failure(
          write("POST", "", writer(T1, ALICE, "user", "portal", time), body, "auth", null),
          403,
          "iam.step_up_required");
    var created = result(write("POST", "", bearer, body, "auth", null), 201);
    admin.update("DELETE FROM org.role_binding WHERE tenant_id=? AND subject_id=?", T1, ALICE);
    failure(write("POST", "", bearer, body, "auth", null), 403, "common.forbidden");
    failure(
        write(
            "PATCH",
            "/" + created.path("id").asString(),
            bearer,
            "{\"name\":\"Blocked\"}",
            null,
            "\"1\""),
        403,
        "common.forbidden");
    String bob = writer(T1, BOB);
    result(write("POST", "", bob, body, "disabled", null), 201);
    admin.update("UPDATE org.member SET status='disabled' WHERE id=?", BOB);
    failure(write("POST", "", bob, body, "disabled", null), 403, "common.forbidden");
    assertEquals(
        2, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
  }

  @Test
  void uploadPermissionDoesNotImplicitlyRequireTheSeparateReadPermission() throws Exception {
    grantWrites();
    admin.execute(
        "UPDATE org.role SET permissions=ARRAY['resource.packages.upload'] WHERE key='resource-test'");
    var claims =
        new JWTClaimsSet.Builder(SignedJWT.parse(writer(T1, ALICE)).getJWTClaimsSet())
            .claim("perms", List.of("resource.packages.upload"))
            .build();
    var signed =
        new SignedJWT(
            new JWSHeader.Builder(JWSAlgorithm.ES256)
                .type(new JOSEObjectType("at+jwt"))
                .keyID(KEY.getKeyID())
                .build(),
            claims);
    signed.sign(new ECDSASigner(KEY));
    result(
        write("POST", "", signed.serialize(), input("Upload only", "tenant"), "upload-only", null),
        201);
    denied("", signed.serialize(), 403, "common.forbidden");
  }

  @Test
  void nonMemberServiceDeviceAndAiCredentialsCannotWrite() throws Exception {
    grantWrites();
    String body = input("Example", "tenant");
    for (String[] claims :
        List.of(new String[] {"device", "device"}, new String[] {"service_account", "service"})) {
      var response =
          write(
              "POST",
              "",
              writer(T1, ALICE, claims[0], claims[1], Instant.now().getEpochSecond()),
              body,
              "subject",
              null);
      assertTrue(response.statusCode() == 401 || response.statusCode() == 403, response.body());
    }
    failure(
        write("POST", "", "ait_example_redacted", body, "subject", null),
        401,
        "common.unauthenticated");
    failure(write("POST", "", writer(T1, id(999)), body, "subject", null), 403, "common.forbidden");
    assertEquals(
        0, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
  }

  @Test
  void personalCreationPolicyAndL4SharingRemainEnforced() throws Exception {
    grantWrites();
    String body = input("Personal plugin", "private"), bearer = writer(T1, ALICE);
    var personal = result(write("POST", "", bearer, body, "personal", null), 201);
    String packageId = personal.path("id").asString();
    assertEquals("l4", personal.path("level").asString());
    denied("/" + packageId, token(T1, BOB, true), 404, "common.not_found");
    failure(
        write(
            "PATCH",
            "/" + packageId,
            writer(T1, ALICE),
            "{\"visibility\":\"tenant\"}",
            null,
            "\"1\""),
        403,
        "resource.level_locked");
    result(
        write(
            "PATCH",
            "/" + packageId,
            writer(T1, ALICE),
            "{\"name\":\"Personal renamed\"}",
            null,
            "\"1\""),
        200);
    admin.update(
        "UPDATE org.policy SET value=jsonb_set(value,'{allowPersonalPlugins}','false') WHERE tenant_id=? AND key='workspace'",
        T1);
    failure(
        write("POST", "", writer(T1, ALICE), input("Disabled", "private"), "policy", null),
        403,
        "common.forbidden");
    failure(write("POST", "", bearer, body, "personal", null), 403, "common.forbidden");
    failure(
        write("PATCH", "/" + packageId, writer(T1, ALICE), "{\"name\":\"Blocked\"}", null, "\"2\""),
        404,
        "common.not_found");
    var shared = create("Shared draft", "tenant");
    result(
        write(
            "PATCH",
            "/" + shared.path("id").asString(),
            writer(T1, ALICE),
            "{\"visibility\":\"private\"}",
            null,
            "\"1\""),
        200);
    assertEquals("l3", get("/" + shared.path("id").asString(), ALICE).path("level").asString());
  }

  @Test
  void groupWritesUseOnlyCurrentTrustedMembershipAndReplayCannotRestoreIt() throws Exception {
    grantWrites();
    admin.update(
        "INSERT INTO org.\"group\"(id,tenant_id,name) VALUES(?,?,'Other tenant group')",
        id(21),
        T2);
    String bearer = writer(T1, ALICE), body = input("Group plugin", "groups", GROUP);
    for (UUID group : List.of(id(999), id(21)))
      failure(
          write("POST", "", bearer, input("Blocked", "groups", group), "group-" + group, null),
          403,
          "common.forbidden");
    failure(write("POST", "", writer(T1, BOB), body, "group", null), 403, "common.forbidden");
    var created = result(write("POST", "", bearer, body, "group", null), 201);
    admin.update("DELETE FROM org.group_member WHERE tenant_id=? AND account_id=?", T1, ALICE);
    failure(write("POST", "", bearer, body, "group", null), 403, "common.forbidden");
    failure(
        write(
            "PATCH",
            "/" + created.path("id").asString(),
            bearer,
            "{\"name\":\"Blocked\"}",
            null,
            "\"1\""),
        404,
        "common.not_found");
    assertEquals(
        1, admin.queryForObject("SELECT revision FROM resource.resource_package", Long.class));
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_package_group", Integer.class));
  }

  @Test
  void patchNeverGrantsOwnerOrTenantOrLevelOverrides() throws Exception {
    grantWrites();
    var created = create("Owned", "tenant");
    String packageId = created.path("id").asString();
    failure(
        write("PATCH", "/" + packageId, writer(T1, BOB), "{\"name\":\"Stolen\"}", null, "\"1\""),
        403,
        "common.forbidden");
    failure(
        write(
            "PATCH",
            "/" + packageId,
            writer(T1, BOB, "user", "admin", Instant.now().getEpochSecond()),
            "{\"name\":\"Admin override\"}",
            null,
            "\"1\""),
        403,
        "common.forbidden");
    failure(
        write("PATCH", "/" + packageId, writer(T2, id(15)), "{\"name\":\"Stolen\"}", null, "\"1\""),
        404,
        "common.not_found");
    for (String level : List.of("l0", "l1", "l2")) {
      UUID id =
          resource(100 + Integer.parseInt(level.substring(1)), level, "tenant", ALICE, T1, false);
      failure(
          write("PATCH", "/" + id, writer(T1, ALICE), "{\"name\":\"Locked\"}", null, "\"1\""),
          403,
          "resource.level_locked");
    }
    var privateOther =
        result(
            write("POST", "", writer(T1, BOB), input("Other private", "private"), "other", null),
            201);
    failure(
        write(
            "PATCH",
            "/" + privateOther.path("id").asString(),
            writer(T1, ALICE),
            "{\"name\":\"Hidden\"}",
            null,
            "\"1\""),
        404,
        "common.not_found");
    assertEquals(created, get("/" + packageId, ALICE));
  }

  @Test
  void strictPackageBodiesRejectProtectedFieldsDuplicatesNullsAndOversizedInput() throws Exception {
    grantWrites();
    String bearer = writer(T1, ALICE), valid = input("Valid", "tenant");
    var created = create("Existing", "tenant");
    String path = "/" + created.path("id").asString();
    for (String field :
        List.of(
            "tenantId",
            "ownerId",
            "level",
            "enabled",
            "defaultVersionId",
            "revision",
            "id",
            "createdAt",
            "extra")) {
      failure(
          write(
              "POST",
              "",
              bearer,
              valid.substring(0, valid.length() - 1) + ",\"" + field + "\":null}",
              "inject-" + field,
              null),
          400,
          "common.validation_failed");
      failure(
          write("PATCH", path, bearer, "{\"" + field + "\":null}", null, "\"1\""),
          400,
          "common.validation_failed");
    }
    failure(
        write("PATCH", path, bearer, "{\"kind\":\"plugin\"}", null, "\"1\""),
        400,
        "common.validation_failed");
    for (String body :
        List.of(
            "{}",
            "   ",
            "[]",
            "null",
            "{",
            valid + " {}",
            valid.replace("plugin", "unknown"),
            valid.replace("\"Valid\"", "null"),
            valid.replace("\"Valid\"", "\"\""),
            valid.replace("\"Valid\"", "\"\\u0000\""),
            valid.replace("\"Valid\"", "\"\\uD800\""),
            valid.replace("\"Valid\"", "\"\\uDC00\""),
            input("a".repeat(215), "tenant"),
            valid.replace("\"name\":", "\"name\":\"duplicate\",\"name\":"),
            input("Invalid", "groups", GROUP, GROUP),
            input("Invalid", "groups"),
            input("Invalid", "tenant", GROUP),
            valid.replace("[]", "null"),
            valid.replace("[]", "[\"not-a-uuid\"]"),
            input("a".repeat(33000), "tenant")))
      failure(write("POST", "", bearer, body, "invalid", null), 400, "common.validation_failed");
    for (String body :
        List.of("{}", "{\"name\":null}", "{\"visibility\":null}", "{\"groupIds\":null}"))
      failure(write("PATCH", path, bearer, body, null, "\"1\""), 400, "common.validation_failed");
    assertEquals(
        1, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_package_creation", Integer.class));
  }

  @Test
  void requestLimitsAlsoApplyToChunkedBodiesAndTooManyGroups() throws Exception {
    grantWrites();
    String bearer = writer(T1, ALICE), oversized = input("a".repeat(33000), "tenant");
    var response =
        http.send(
            HttpRequest.newBuilder(
                    URI.create("http://127.0.0.1:" + port + "/api/resource/v1/packages"))
                .header("Authorization", "Bearer " + bearer)
                .header("Idempotency-Key", "chunked")
                .header("Content-Type", "application/json")
                .POST(
                    HttpRequest.BodyPublishers.ofInputStream(
                        () ->
                            new java.io.ByteArrayInputStream(
                                oversized.getBytes(StandardCharsets.UTF_8))))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    failure(response, 400, "common.validation_failed");
    UUID[] groups =
        java.util.stream.IntStream.range(1000, 1201)
            .mapToObj(ResourceReadAssemblyTest::id)
            .toArray(UUID[]::new);
    failure(
        write("POST", "", bearer, input("Many groups", "groups", groups), "groups", null),
        400,
        "common.validation_failed");
    failure(
        write(
            "POST",
            "",
            bearer,
            input("Bad UUID", "groups", GROUP).replace(GROUP.toString(), "1-1-1-1-1"),
            "uuid",
            null),
        400,
        "common.validation_failed");
    assertEquals(
        0, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
  }

  @Test
  void nameLengthUsesUnicodeCodePointsWithoutDatabaseReplacement() throws Exception {
    grantWrites();
    String name = "\uD83D\uDD0C".repeat(214);
    var created = create(name, "tenant");
    assertEquals(name, get("/" + created.path("id").asString(), ALICE).path("name").asString());
    failure(
        write("POST", "", writer(T1, ALICE), input(name + "x", "tenant"), "too-long", null),
        400,
        "common.validation_failed");
  }

  @Test
  void ifMatchIsAnExactStrongSingleRevisionAndCannotOverflow() throws Exception {
    grantWrites();
    var created = create("Versioned", "tenant");
    String path = "/" + created.path("id").asString(), bearer = writer(T1, ALICE);
    failure(
        write("PATCH", path, bearer, "{\"name\":\"No match\"}", null, null),
        412,
        "common.precondition_failed");
    for (String match :
        List.of("1", "W/\"1\"", "*", "\"01\"", "\"1\", \"2\"", "\"0\"", "\"9007199254740992\""))
      failure(
          write("PATCH", path, bearer, "{\"name\":\"No match\"}", null, match),
          412,
          "common.precondition_failed");
    var duplicate =
        http.send(
            HttpRequest.newBuilder(
                    URI.create("http://127.0.0.1:" + port + "/api/resource/v1/packages" + path))
                .header("Authorization", "Bearer " + bearer)
                .header("Content-Type", "application/json")
                .headers("If-Match", "\"1\"", "If-Match", "\"1\"")
                .method("PATCH", HttpRequest.BodyPublishers.ofString("{\"name\":\"No match\"}"))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    failure(duplicate, 412, "common.precondition_failed");
    result(write("PATCH", path, bearer, "{\"name\":\"Changed\"}", null, "\"1\""), 200);
    failure(
        write("PATCH", path, bearer, "{\"name\":\"Lost update\"}", null, "\"1\""),
        412,
        "common.precondition_failed");
    admin.update(
        "UPDATE resource.resource_package SET revision=9007199254740991 WHERE id=?",
        UUID.fromString(created.path("id").asString()));
    failure(
        write("PATCH", path, bearer, "{\"name\":\"Overflow\"}", null, "\"9007199254740991\""),
        409,
        "resource.state_conflict");
    assertEquals("Changed", get(path, ALICE).path("name").asString());
    assertEquals(9007199254740991L, get(path, ALICE).path("revision").asLong());
  }

  @Test
  void idempotencyReplaysOriginalResultAfterPatchAndRejectsDifferentBodies() throws Exception {
    grantWrites();
    String bearer = writer(T1, ALICE), body = input("Initial", "tenant");
    var first = write("POST", "", bearer, body, "stable", null);
    var original = result(first, 201);
    assertEquals("\"1\"", first.headers().firstValue("etag").orElse(""));
    assertEquals("no-store", first.headers().firstValue("cache-control").orElse(""));
    String path = "/" + original.path("id").asString();
    result(write("PATCH", path, bearer, "{\"name\":\"Current\"}", null, "\"1\""), 200);
    var replay =
        write(
            "POST",
            "",
            writer(T1, ALICE),
            "{\"visibility\":\"tenant\",\"kind\":\"plugin\",\"name\":\"Initial\"}",
            "stable",
            null);
    assertEquals(original, result(replay, 201));
    assertEquals("\"1\"", replay.headers().firstValue("etag").orElse(""));
    assertEquals("Current", get(path, ALICE).path("name").asString());
    failure(
        write("POST", "", bearer, input("Different", "tenant"), "stable", null),
        409,
        "common.conflict");
    for (String key : List.of("", "bad key", "k".repeat(129)))
      failure(write("POST", "", bearer, body, key, null), 400, "common.validation_failed");
    assertEquals(
        1, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_package_creation", Integer.class));
  }

  @Test
  void omittedIdempotencyKeyCreatesIndependentPackagesWithoutReplayRecords() throws Exception {
    grantWrites();
    String body = input("Without a key", "tenant"), bearer = writer(T1, ALICE);
    var first = result(write("POST", "", bearer, body, null, null), 201);
    var second = result(write("POST", "", bearer, body, null, null), 201);
    assertNotEquals(first.path("id"), second.path("id"));
    assertEquals(first, get("/" + first.path("id").asString(), ALICE));
    assertEquals(second, get("/" + second.path("id").asString(), ALICE));
    assertEquals(
        2, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
    assertEquals(
        0,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_package_creation", Integer.class));
    admin.update("DELETE FROM org.role_binding WHERE tenant_id=? AND subject_id=?", T1, ALICE);
    failure(write("POST", "", bearer, body, null, null), 403, "common.forbidden");
  }

  @Test
  void groupFingerprintNormalizesUuidCaseAndGroupOrderAndRejectsDuplicateHeaders()
      throws Exception {
    grantWrites();
    UUID group = UUID.fromString("01900000-0000-7000-8000-00000000abcd");
    admin.update(
        "INSERT INTO org.\"group\"(id,tenant_id,name) VALUES(?,?,'Second example group')",
        group,
        T1);
    admin.update(
        "INSERT INTO org.group_member(id,tenant_id,group_id,account_id) VALUES(?,?,?,?)",
        UuidV7.next(),
        T1,
        group,
        ALICE);
    String bearer = writer(T1, ALICE), body = input("Canonical", "groups", GROUP, group);
    var original = result(write("POST", "", bearer, body, "canonical", null), 201);
    String reordered =
        input("Canonical", "groups", group, GROUP)
            .replace(group.toString(), group.toString().toUpperCase(Locale.ROOT));
    assertEquals(original, result(write("POST", "", bearer, reordered, "canonical", null), 201));
    failure(
        write(
            "POST",
            "",
            bearer,
            input("Duplicate", "groups", group, group)
                .replaceFirst(group.toString(), group.toString().toUpperCase(Locale.ROOT)),
            "duplicate",
            null),
        400,
        "common.validation_failed");
    var response =
        http.send(
            HttpRequest.newBuilder(
                    URI.create("http://127.0.0.1:" + port + "/api/resource/v1/packages"))
                .header("Authorization", "Bearer " + bearer)
                .header("Content-Type", "application/json")
                .headers("Idempotency-Key", "canonical", "Idempotency-Key", "canonical")
                .POST(HttpRequest.BodyPublishers.ofString(body))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    failure(response, 400, "common.validation_failed");
  }

  @Test
  void idempotencyScopeSeparatesActorsTenantsAndExpiredOperations() throws Exception {
    grantWrites();
    String body = input("Scoped", "tenant"), alice = writer(T1, ALICE);
    var first = result(write("POST", "", alice, body, "scope", null), 201);
    var bob = result(write("POST", "", writer(T1, BOB), body, "scope", null), 201);
    var other = result(write("POST", "", writer(T2, id(15)), body, "scope", null), 201);
    assertEquals(
        3,
        Set.of(first.path("id").asString(), bob.path("id").asString(), other.path("id").asString())
            .size());
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_package_creation WHERE expires_at BETWEEN now()+interval '23 hours 59 minutes' AND now()+interval '24 hours 1 minute' AND actor_id=?",
            Integer.class,
            ALICE));
    admin.update(
        "UPDATE resource.resource_package_creation SET expires_at=now()-interval '1 second' WHERE tenant_id=? AND actor_id=?",
        T1,
        ALICE);
    var next =
        result(write("POST", "", alice, input("After expiry", "tenant"), "scope", null), 201);
    assertNotEquals(first.path("id"), next.path("id"));
    assertEquals(
        next,
        result(write("POST", "", alice, input("After expiry", "tenant"), "scope", null), 201));
    assertEquals(
        4, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
    assertEquals(
        3,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_package_creation", Integer.class));
    var runtime = new JdbcTemplate(resourceDataSource);
    assertEquals(
        0,
        runtime.queryForObject(
            "SELECT count(*) FROM resource.resource_package_creation", Integer.class));
    TenantContext.run(
        T2,
        () ->
            new org.springframework.transaction.support.TransactionTemplate(resourceTransactions)
                .execute(
                    status -> {
                      assertEquals(
                          1,
                          runtime.queryForObject(
                              "SELECT count(*) FROM resource.resource_package_creation",
                              Integer.class));
                      return null;
                    }));
  }

  @Test
  void concurrentSameKeyCreatesOnePackageAndDifferentBodiesConflict() throws Exception {
    grantWrites();
    String bearer = writer(T1, ALICE), body = input("Concurrent", "tenant");
    var responses =
        concurrent(
            () -> write("POST", "", bearer, body, "concurrent", null),
            () -> write("POST", "", bearer, body, "concurrent", null));
    assertEquals(result(responses.get(0), 201), result(responses.get(1), 201));
    assertEquals(
        1, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
    responses =
        concurrent(
            () -> write("POST", "", bearer, input("One", "tenant"), "different", null),
            () -> write("POST", "", bearer, input("Two", "tenant"), "different", null));
    assertEquals(
        List.of(201, 409), responses.stream().map(HttpResponse::statusCode).sorted().toList());
    assertEquals(
        2, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
  }

  @Test
  void concurrentPatchesWithTheSameEtagCannotLoseUpdates() throws Exception {
    grantWrites();
    var created = create("Before concurrent patch", "tenant");
    String bearer = writer(T1, ALICE), path = "/" + created.path("id").asString();
    var responses =
        concurrent(
            () -> write("PATCH", path, bearer, "{\"name\":\"Name winner\"}", null, "\"1\""),
            () ->
                write(
                    "PATCH",
                    path,
                    bearer,
                    JSON.writeValueAsString(
                        Map.of("visibility", "groups", "groupIds", List.of(GROUP))),
                    null,
                    "\"1\""));
    assertEquals(
        List.of(200, 412), responses.stream().map(HttpResponse::statusCode).sorted().toList());
    var winner = responses.stream().filter(r -> r.statusCode() == 200).findFirst().orElseThrow();
    assertEquals(JSON.readTree(winner.body()), get(path, ALICE));
    assertEquals(2, get(path, ALICE).path("revision").asLong());
  }

  @Test
  void databaseFailureRollsBackPackageAclRevisionAndCreationReplayRecord() throws Exception {
    grantWrites();
    var created = create("Before rollback", "groups", GROUP);
    UUID packageId = UUID.fromString(created.path("id").asString());
    UUID originalRelation =
        admin.queryForObject(
            "SELECT id FROM resource.resource_package_group WHERE package_id=?",
            UUID.class,
            packageId);
    admin.execute(
        "ALTER TABLE resource.resource_package_group ADD CONSTRAINT resource_test_reject_group CHECK (group_id<>'"
            + GROUP
            + "'::uuid) NOT VALID");
    try {
      failure(
          write(
              "PATCH",
              "/" + packageId,
              writer(T1, ALICE),
              "{\"name\":\"Must roll back\"}",
              null,
              "\"1\""),
          500,
          "common.internal");
      failure(
          write(
              "POST",
              "",
              writer(T1, ALICE),
              input("Must roll back", "groups", GROUP),
              "rollback",
              null),
          500,
          "common.internal");
      assertEquals(created, get("/" + packageId, ALICE));
      assertEquals(
          originalRelation,
          admin.queryForObject(
              "SELECT id FROM resource.resource_package_group WHERE package_id=?",
              UUID.class,
              packageId));
      assertEquals(
          1, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
      assertEquals(
          1,
          admin.queryForObject(
              "SELECT count(*) FROM resource.resource_package_creation", Integer.class));
    } finally {
      admin.execute(
          "ALTER TABLE resource.resource_package_group DROP CONSTRAINT resource_test_reject_group");
    }
    result(
        write(
            "POST",
            "",
            writer(T1, ALICE),
            input("Must roll back", "groups", GROUP),
            "rollback",
            null),
        201);
  }

  @Test
  void missingPolicyFailsClosedForNewWritesAndCachedResults() throws Exception {
    grantWrites();
    String bearer = writer(T1, ALICE), body = input("Example", "tenant");
    var created = result(write("POST", "", bearer, body, "dependency", null), 201);
    admin.update("DELETE FROM org.policy WHERE tenant_id=? AND key='workspace'", T1);
    failure(write("POST", "", bearer, body, "dependency", null), 503, "common.unavailable");
    failure(write("POST", "", bearer, body, "new", null), 503, "common.unavailable");
    failure(
        write(
            "PATCH",
            "/" + created.path("id").asString(),
            bearer,
            "{\"name\":\"Blocked\"}",
            null,
            "\"1\""),
        503,
        "common.unavailable");
    assertEquals(
        1, admin.queryForObject("SELECT count(*) FROM resource.resource_package", Integer.class));
  }

  List<HttpResponse<String>> concurrent(
      java.util.concurrent.Callable<HttpResponse<String>> first,
      java.util.concurrent.Callable<HttpResponse<String>> second)
      throws Exception {
    try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var ready = new java.util.concurrent.CountDownLatch(2);
      var start = new java.util.concurrent.CountDownLatch(1);
      var futures = new ArrayList<java.util.concurrent.Future<HttpResponse<String>>>();
      for (var action : List.of(first, second))
        futures.add(
            executor.submit(
                () -> {
                  ready.countDown();
                  assertTrue(start.await(10, java.util.concurrent.TimeUnit.SECONDS));
                  return action.call();
                }));
      assertTrue(ready.await(10, java.util.concurrent.TimeUnit.SECONDS));
      start.countDown();
      return List.of(
          futures.get(0).get(30, java.util.concurrent.TimeUnit.SECONDS),
          futures.get(1).get(30, java.util.concurrent.TimeUnit.SECONDS));
    }
  }
}
