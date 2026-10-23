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
import org.springframework.boot.SpringBootConfiguration;
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

  @SpringBootConfiguration
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
      return roles.runtime().dataSource();
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
  void setup() {
    http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();
    admin = new JdbcTemplate(PostgresExtension.database(getClass()).dataSource());
    admin.execute(
        "TRUNCATE resource.resource_package_group, resource.resource_package, resource.resource_version CASCADE");
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
  void resourceHttpIsExplicitlyEnabledAndHasNoWriteOrDownloadHandler() throws Exception {
    assertNotNull(context.getBean(auriga.resource.web.ResourceController.class));
    var response =
        http.send(
            HttpRequest.newBuilder(
                    URI.create("http://127.0.0.1:" + port + "/api/resource/v1/packages"))
                .header("Authorization", "Bearer " + token(T1, ALICE, true))
                .POST(HttpRequest.BodyPublishers.noBody())
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(405, response.statusCode(), response.body());
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
    for (String table : List.of("resource_package", "resource_version", "resource_package_group"))
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
}
