package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.contracts.node.v1.InstancePhase;
import auriga.contracts.node.v1.InstanceSpec;
import auriga.contracts.node.v1.InstanceStatus;
import auriga.contracts.resource.v1.*;
import auriga.iam.ResourceDeviceFixture.Grant;
import auriga.org.OrgService;
import auriga.org.OrgStore;
import auriga.resource.ResourceModuleConfiguration;
import auriga.resource.api.ResourceAccessInputs;
import auriga.resource.app.CatalogDelivery;
import auriga.resource.app.CatalogProtocol;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.app.WorkspaceService;
import auriga.workspace.app.WorkspaceUnifiedResources;
import auriga.workspace.domain.UnifiedSpec;
import auriga.workspace.persistence.WorkspaceStore;
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
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/** Real HTTP + signed tokens + org/resource PostgreSQL with independent runtime/migration roles. */
@SpringBootTest(
    classes = ResourceReadAssemblyTest.App.class,
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = {
      "auriga.platform.combined=true",
      "auriga.services.resource.enabled=true",
      "auriga.resource.catalog-writes-enabled=false",
      "auriga.services.workspace.enabled=false",
      "auriga.resource.reconcile-ms=3600000",
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
  static final Map<String, java.security.PublicKey> RESOURCE_KEYS = new HashMap<>();
  static final JsonMapper JSON = JsonMapper.builder().build();
  static final java.util.concurrent.atomic.AtomicBoolean CATALOG_WRITES =
      new java.util.concurrent.atomic.AtomicBoolean();
  final java.util.concurrent.atomic.AtomicInteger revokeFailures =
      new java.util.concurrent.atomic.AtomicInteger();
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
  @Autowired auriga.iam.ResourceDeviceFixture deviceCredentials;
  @Autowired MarketWorkspace marketWorkspace;
  @Autowired CatalogDelivery catalogDelivery;
  @MockitoSpyBean ResourceAccessInputs resourceAccessInputs;
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
    registry.add("auriga.resource.catalog-writes-enabled", CATALOG_WRITES::get);
    registry.add(
        "auriga.resource.download-base-url",
        () -> "https://resource.example.com/internal/resource/v1/downloads/");
  }

  // Explicit lite configuration: not a scanned component or a discoverable Boot application root.
  @EnableAutoConfiguration
  @Import({ResourceModuleConfiguration.class, PlatformResourceConfiguration.class})
  static class App {
    @Bean
    auriga.iam.ResourceDeviceFixture resourceDeviceFixture(OrgService org, Clock clock) {
      var roles =
          PostgresExtension.serviceRoles(
              ResourceReadAssemblyTest.class,
              "resource_test_iam_owner",
              "resource_test_iam_app",
              "iam");
      CommonMigrations.migrate(roles.owner().dataSource(), "iam");
      CommonMigrations.serviceFlyway(
              Flyway.configure()
                  .dataSource(roles.owner().dataSource())
                  .schemas("iam")
                  .locations("classpath:db/iam")
                  .load())
          .migrate();
      var permissions = new auriga.org.OrgPermissionResolver(org);
      return new auriga.iam.ResourceDeviceFixture(
          roles.runtime().dataSource(), KEY, ISSUER, clock, permissions, permissions);
    }

    @Bean
    auriga.iam.api.DevicePermissionAuthority devicePermissionAuthority(
        auriga.iam.ResourceDeviceFixture devices) {
      return devices.authority();
    }

    @Bean
    MarketWorkspace marketWorkspace() {
      return new MarketWorkspace();
    }

    @Bean(destroyMethod = "close")
    WorkspaceUnifiedResources marketWorkspaceResources(MarketWorkspace fixture, Clock clock) {
      return new WorkspaceUnifiedResources(
          new WorkspaceService(
              fixture.store, null, null, null, (a, b, c, v) -> UuidV7.next(), clock),
          null,
          null);
    }

    @Bean
    auriga.resource.api.UnifiedResourceWorkspace marketWorkspacePort(
        WorkspaceUnifiedResources fixture) {
      return new PlatformResourceWorkspace.Unified(fixture);
    }

    @Bean(destroyMethod = "close")
    auriga.resource.objectstore.ObjectStore resourceObjects() throws Exception {
      return new auriga.resource.objectstore.LocalObjectStore(
          java.nio.file.Files.createTempDirectory("resource-http-"));
    }

    @Bean
    auriga.resource.app.ResourceSigning resourceSigning() throws Exception {
      var generator = java.security.KeyPairGenerator.getInstance("Ed25519");
      var release = generator.generateKeyPair();
      var revoke = generator.generateKeyPair();
      RESOURCE_KEYS.put("release-test", release.getPublic());
      RESOURCE_KEYS.put("revoke-test", revoke.getPublic());
      return new auriga.resource.app.ResourceSigning(
          "release-test", release.getPrivate(), "revoke-test", revoke.getPrivate());
    }

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
    CATALOG_WRITES.set(false);
    marketWorkspace.rows.clear();
    http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();
    // Reuse a real fixture connection instead of starting a Windows PG backend for each statement.
    adminConnection =
        new org.springframework.jdbc.datasource.SingleConnectionDataSource(
            PostgresExtension.database(getClass()).dataSource().getConnection(), true);
    admin = new JdbcTemplate(adminConnection);
    admin.execute(
        "TRUNCATE resource.resource_revoked_artifact, resource.resource_revocation_delivery, resource.outbox_event, resource.resource_operation, resource.resource_distribution, resource.resource_download, resource.resource_revocations, resource.resource_package_creation, resource.resource_package_group, resource.resource_package, resource.resource_version CASCADE");
    admin.execute("TRUNCATE resource.catalog_package CASCADE");
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
  void disabledCatalogWritesKeepLegacyHttpAndPluginIsolation() throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['resource.packages.read','resource.packages.upload','resource.versions.submit','resource.versions.review','resource.versions.publish','resource.distributions.manage'] WHERE id=?",
        ROLE);
    var legacy = resource(189, "l4", "private", ALICE, T1, false);
    assertEquals(200, request("/" + legacy, token(T1, ALICE, true)).statusCode());
    String root = "http://127.0.0.1:" + port;
    var list =
        http.send(
            HttpRequest.newBuilder(URI.create(root + "/api/resource/v1/catalog/packages"))
                .header("Authorization", "Bearer " + token(T1, ALICE, true))
                .GET()
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(200, list.statusCode(), list.body());
    assertTrue(JSON.readTree(list.body()).path("items").isEmpty());
    var isolated =
        http.send(
            HttpRequest.newBuilder(URI.create(root + "/api/resource/v1/catalog/packages/" + legacy))
                .header("Authorization", "Bearer " + token(T1, ALICE, true))
                .GET()
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(404, isolated.statusCode(), isolated.body());
    var create =
        http.send(
            HttpRequest.newBuilder(URI.create(root + "/api/resource/v1/catalog/packages"))
                .header("Authorization", "Bearer " + workflowToken(ALICE))
                .header("Content-Type", "application/json")
                .header("If-Match", "\"0\"")
                .header("Idempotency-Key", UUID.randomUUID().toString())
                .POST(
                    HttpRequest.BodyPublishers.ofString(
                        "{\"name\":\"example-skill\",\"kind\":\"skill\",\"visibility\":\"tenant\"}"))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(503, create.statusCode(), create.body());
    var node =
        http.send(
            HttpRequest.newBuilder(
                    URI.create(
                        root
                            + "/internal/resource/v1/catalog/downloads/"
                            + UUID.randomUUID()
                            + "/manifest"))
                .GET()
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(401, node.statusCode(), node.body());
  }

  HttpResponse<byte[]> deviceRequest(String path, String token, String body) throws Exception {
    return deviceRequest(path, token, body, "application/json");
  }

  HttpResponse<byte[]> deviceRequest(String path, String token, String body, String contentType)
      throws Exception {
    var builder =
        HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/api/resource/v1" + path))
            .timeout(Duration.ofSeconds(10))
            .header("Authorization", "Bearer " + token);
    if (body == null) builder.GET();
    else
      builder
          .header("Content-Type", contentType)
          .header("If-Match", "\"0\"")
          .header("Idempotency-Key", UUID.randomUUID().toString())
          .POST(HttpRequest.BodyPublishers.ofString(body));
    return http.send(builder.build(), HttpResponse.BodyHandlers.ofByteArray());
  }

  void deviceStatus(int expected, HttpResponse<byte[]> response) {
    assertEquals(
        expected, response.statusCode(), new String(response.body(), StandardCharsets.UTF_8));
  }

  static final class MarketWorkspace {
    final Map<UUID, Map<String, Object>> rows = new java.util.concurrent.ConcurrentHashMap<>();
    final WorkspaceStore store = org.mockito.Mockito.mock(WorkspaceStore.class);

    MarketWorkspace() {
      org.mockito.Mockito.when(
              store.in(org.mockito.ArgumentMatchers.any(), org.mockito.ArgumentMatchers.any()))
          .thenAnswer(call -> call.<java.util.function.Supplier<?>>getArgument(1).get());
      org.mockito.Mockito.when(store.instance(org.mockito.ArgumentMatchers.any()))
          .thenAnswer(
              call -> {
                var row = rows.get(call.<UUID>getArgument(0));
                if (row == null)
                  throw new auriga.common.error.AurigaException(
                      auriga.common.error.CommonErrors.NOT_FOUND);
                return row;
              });
    }

    void seed(UUID tenant, UUID owner, UUID instance) {
      var spec =
          InstanceSpec.newBuilder()
              .setTenantId(tenant.toString())
              .setOwnerId(owner.toString())
              .setInstanceId(instance.toString())
              .setGeneration(37)
              .build();
      set(spec, UUID.randomUUID(), null);
    }

    InstanceSpec spec(UUID instance) {
      return WorkspaceService.spec(rows.get(instance));
    }

    UUID node(UUID instance) {
      return (UUID) rows.get(instance).get("node_id");
    }

    void set(InstanceSpec spec, UUID node, InstanceStatus status) {
      var row = new HashMap<String, Object>();
      row.put("id", UUID.fromString(spec.getInstanceId()));
      row.put("owner_id", UUID.fromString(spec.getOwnerId()));
      row.put("node_id", node);
      row.put("spec", spec.toByteArray());
      row.put("observed_generation", status == null ? 0L : status.getObservedGeneration());
      row.put("ready", status != null && status.getReady());
      if (status != null) row.put("status", status.toByteArray());
      rows.put(UUID.fromString(spec.getInstanceId()), row);
    }

    void report(UUID instance, long generation, long selection, long revOffset) {
      var spec = spec(instance);
      var status =
          InstanceStatus.newBuilder()
              .setInstanceId(spec.getInstanceId())
              .setPhase(InstancePhase.INSTANCE_PHASE_RUNNING)
              .setObservedGeneration(generation)
              .setReady(true)
              .setUnifiedGuardian(
                  UnifiedGuardianState.newBuilder()
                      .setTenantId(spec.getTenantId())
                      .setOwnerId(spec.getOwnerId())
                      .setHostId(instance.toString())
                      .setGeneration(generation)
                      .setSelectionRevision(selection)
                      .setState("active"));
      for (var ref : spec.getUnifiedResourcesList()) {
        var state =
            switch (ref.getDesiredState()) {
              case UNIFIED_RESOURCE_DESIRED_STATE_DISABLED -> "disabled";
              case UNIFIED_RESOURCE_DESIRED_STATE_REMOVED -> "removed";
              default -> "applied";
            };
        status.addUnifiedResourceReports(
            UnifiedInstallationReport.newBuilder()
                .setClientId(instance.toString())
                .setIdentity(ref.getIdentity())
                .setState(state)
                .setInstallationRevision(ref.getInstallationRevision() + revOffset)
                .setSelectionRevision(selection)
                .setGeneration(generation)
                .setObservedGeneration(generation)
                .setReportSource("node")
                .setObservedAt("2026-11-18T00:00:00Z"));
      }
      set(spec, node(instance), status.build());
    }
  }

  private JsonNode marketPackage(int number, long sequence, String name) {
    UUID pkg = id(number), versionId = id(number + 1);
    String manifest =
        """
        {"name":"%s","kind":"skill","version":"1.0.0",
        "artifacts":[{"id":"payload","digest":"sha256:%s","sizeBytes":42}]}
        """
            .formatted(name, "a".repeat(64));
    byte[] bytes = manifest.getBytes(StandardCharsets.UTF_8);
    String digest = auriga.resource.app.CatalogManifest.digest(bytes);
    admin.update(
        "INSERT INTO resource.catalog_package(id,tenant_id,owner_id,name,kind,visibility,enabled,release_high_water,created_at) VALUES(?,?,?,?,'skill','tenant',true,?,now())",
        pkg,
        T1,
        ALICE,
        name,
        sequence);
    admin.update(
        "INSERT INTO resource.catalog_version(id,tenant_id,package_id,author_id,version,digest,manifest_bytes,manifest,state,minimum_risk,risk,release_sequence,created_at) VALUES(?,?,?,?,'1.0.0',?,?,?::jsonb,'published','r1','r1',?,now())",
        versionId,
        T1,
        pkg,
        ALICE,
        digest,
        bytes,
        manifest,
        sequence);
    return JSON.createObjectNode()
        .put("tenantId", T1.toString())
        .put("packageId", pkg.toString())
        .put("versionId", versionId.toString())
        .put("kind", "skill")
        .put("version", "1.0.0")
        .put("manifestDigest", digest);
  }

  private void marketRule(Grant grant, JsonNode identity, String mode) {
    admin.update(
        "INSERT INTO resource.catalog_assignment(id,tenant_id,package_id,version_id,scope,target_id,installation_mode,created_at) VALUES(?,?,?,?,'instance',?,?,now())",
        UuidV7.next(),
        T1,
        UUID.fromString(identity.path("packageId").asString()),
        UUID.fromString(identity.path("versionId").asString()),
        grant.instance(),
        mode);
  }

  private static final String MARKET = "/catalog/member/installations";

  private JsonNode marketList(Grant grant) throws Exception {
    var response = deviceRequest(MARKET + "?clientId=" + grant.device(), grant.token(), null);
    deviceStatus(200, response);
    return JSON.readTree(response.body()).path("items");
  }

  private JsonNode marketItem(Grant grant, JsonNode identity) throws Exception {
    for (var row : marketList(grant))
      if (row.path("resource").path("identity").equals(identity)) return row;
    return JSON.missingNode();
  }

  private HttpResponse<String> marketSelect(
      Grant grant, UUID client, JsonNode identity, String action, long revision, String key)
      throws Exception {
    var body = JSON.createObjectNode().put("clientId", client.toString());
    body.putObject("selection").put("action", action).set("identity", identity);
    return http.send(
        HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/api/resource/v1" + MARKET))
            .header("Authorization", "Bearer " + grant.token())
            .header("Content-Type", "application/json")
            .header("Idempotency-Key", key)
            .header("If-Match", "\"" + revision + "\"")
            .POST(HttpRequest.BodyPublishers.ofString(body.toString()))
            .build(),
        HttpResponse.BodyHandlers.ofString());
  }

  private ResolveUnifiedResourcesRequest.Builder marketResolve(Grant grant) {
    return ResolveUnifiedResourcesRequest.newBuilder()
        .setTenantId(T1.toString())
        .setInstanceId(grant.instance().toString())
        .setOwnerId(marketWorkspace.spec(grant.instance()).getOwnerId())
        .setNodeId(marketWorkspace.node(grant.instance()).toString())
        .setSelectionRevision(
            marketWorkspace.spec(grant.instance()).getResourceSelectionRevision());
  }

  private ResolveUnifiedResourcesResponse marketPlan(Grant grant) {
    return TenantContext.call(
        T1,
        () ->
            catalogDelivery.resolve(marketResolve(grant).setSynchronizeAssignments(true).build()));
  }

  private ResolveUnifiedResourcesRequest marketEvent(Grant grant, long expectedRevision) {
    var envelope =
        JSON.readValue(
            admin.queryForObject(
                "SELECT envelope FROM resource.outbox_event WHERE tenant_id=? AND subject_key=? AND event_type='resource.catalog_distribution_requested' ORDER BY seq DESC LIMIT 1",
                String.class,
                T1,
                grant.instance().toString()),
            EventEnvelope.class);
    assertEquals(T1.toString(), envelope.tenantid());
    assertEquals("auriga/resource", envelope.source());
    assertEquals("resource.catalog_distribution_requested", envelope.type());
    assertEquals(grant.instance().toString(), envelope.subject());
    var event = envelope.data();
    assertEquals(envelope.subject(), event.path("instanceId").asString());
    assertEquals(
        marketWorkspace.spec(grant.instance()).getOwnerId(), event.path("ownerId").asString());
    long revision = event.path("selectionRevision").asLong();
    assertEquals(expectedRevision, revision);
    assertTrue(revision > marketWorkspace.spec(grant.instance()).getResourceSelectionRevision());
    var selections = event.path("selections");
    assertTrue(selections.isArray() && !selections.isEmpty());
    var request =
        ResolveUnifiedResourcesRequest.newBuilder()
            .setTenantId(envelope.tenantid())
            .setInstanceId(event.path("instanceId").asString())
            .setOwnerId(event.path("ownerId").asString())
            .setNodeId(marketWorkspace.node(grant.instance()).toString())
            .setSelectionRevision(revision);
    selections.forEach(s -> request.addSelections(CatalogProtocol.selection(s)));
    return request.build();
  }

  private void applyMarket(
      Grant grant, ResolveUnifiedResourcesRequest request, boolean assignment) {
    var resolution = TenantContext.call(T1, () -> catalogDelivery.resolve(request));
    var next =
        UnifiedSpec.apply(
            marketWorkspace.spec(grant.instance()),
            request.getSelectionRevision(),
            request.getSelectionsList(),
            resolution,
            false,
            assignment);
    marketWorkspace.set(UnifiedSpec.bump(next), marketWorkspace.node(grant.instance()), null);
  }

  @Test
  void managedMarketUsesItsOwnNodeAndOutboxWithLiveIamAndDesktopIsolation() throws Exception {
    CATALOG_WRITES.set(true);
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['auriga.connect','auriga.connect.collab.use','auriga.connect.resources.use','resource.packages.read'] WHERE id IN (?,?)",
        ROLE,
        id(16));
    var desktop = deviceCredentials.issue(T1, ALICE, "standard");
    var a = deviceCredentials.managed(T1, ALICE, UUID.randomUUID(), 1);
    var b = deviceCredentials.managed(T1, ALICE, UUID.randomUUID(), 1);
    var foreign = deviceCredentials.issue(T2, id(15), "managed");
    marketWorkspace.seed(T1, ALICE, a.instance());
    marketWorkspace.seed(T1, ALICE, b.instance());
    marketWorkspace.seed(T2, id(15), foreign.instance());
    assertNotEquals(a.device(), a.instance());
    assertNotEquals(b.device(), b.instance());
    var required = marketPackage(1500, 1, "market-required");
    var optional = marketPackage(1510, 2, "market-optional");
    var defaultInstall = marketPackage(1520, 3, "market-default");
    marketRule(a, required, "required");
    marketRule(a, defaultInstall, "default");
    var plan = marketPlan(a);
    assertEquals(2, plan.getSelectionsCount());
    applyMarket(a, marketEvent(a, plan.getSelectionRevision()), true);
    var spec = marketWorkspace.spec(a.instance());
    assertEquals("applying", marketItem(a, required).path("state").asString());
    marketWorkspace.report(
        a.instance(), spec.getGeneration() - 1, spec.getResourceSelectionRevision(), 0);
    assertEquals("applying", marketItem(a, required).path("state").asString());
    marketWorkspace.report(
        a.instance(), spec.getGeneration(), spec.getResourceSelectionRevision(), 1);
    assertEquals("applying", marketItem(a, required).path("state").asString());
    marketWorkspace.report(
        a.instance(), spec.getGeneration(), spec.getResourceSelectionRevision(), 0);
    var installed = marketItem(a, required);
    assertEquals("applied", installed.path("state").asString());
    assertEquals("required", installed.path("resource").path("installationMode").asString());
    assertEquals(a.device().toString(), installed.path("clientId").asString());
    assertEquals(a.instance().toString(), installed.path("report").path("clientId").asString());
    assertEquals(0, marketList(b).size());
    assertEquals(0, marketList(desktop).size());
    assertEquals(0, marketList(foreign).size());
    deviceStatus(404, deviceRequest(MARKET + "?clientId=" + b.device(), a.token(), null));
    deviceStatus(404, deviceRequest(MARKET + "?clientId=" + a.instance(), a.token(), null));
    failure(
        marketSelect(b, a.device(), optional, "install", 0, "other-did"), 404, "common.not_found");
    failure(
        marketSelect(foreign, foreign.device(), optional, "install", 0, "other-tenant"),
        404,
        "common.not_found");
    for (String action : List.of("disable", "remove"))
      failure(
          marketSelect(
              a,
              a.device(),
              required,
              action,
              installed.path("installationRevision").asLong(),
              "required-" + action),
          403,
          "resource.installation_required");
    var previous = 0L;
    for (String action : List.of("install", "disable", "enable", "remove")) {
      var selected =
          result(
              marketSelect(a, a.device(), optional, action, previous, "optional-" + action), 202);
      assertEquals(
          selected,
          result(
              marketSelect(a, a.device(), optional, action, previous, "optional-" + action), 202));
      if (action.equals("install"))
        failure(
            marketSelect(a, a.device(), optional, action, previous, "parallel-before-apply"),
            409,
            "common.conflict");
      assertEquals("applying", marketItem(a, optional).path("state").asString());
      previous = selected.path("installationRevision").asLong();
      var request = marketEvent(a, selected.path("selectionRevision").asLong());
      applyMarket(a, request, false);
      var current = marketWorkspace.spec(a.instance());
      marketWorkspace.report(
          a.instance(), current.getGeneration(), current.getResourceSelectionRevision(), 0);
      assertEquals(
          switch (action) {
            case "disable" -> "disabled";
            case "remove" -> "removed";
            default -> "applied";
          },
          marketItem(a, optional).path("state").asString());
    }
    assertEquals(0, marketPlan(a).getSelectionsCount());
    failure(
        marketSelect(a, a.device(), optional, "install", 0, "old-cas"),
        412,
        "common.precondition_failed");
    var defaultRow = marketItem(a, defaultInstall);
    var removedDefault =
        result(
            marketSelect(
                a,
                a.device(),
                defaultInstall,
                "remove",
                defaultRow.path("installationRevision").asLong(),
                "remove-default"),
            202);
    applyMarket(a, marketEvent(a, removedDefault.path("selectionRevision").asLong()), false);
    assertEquals(0, marketPlan(a).getSelectionsCount());
    var beforeCollection = marketWorkspace.spec(a.instance());
    marketWorkspace.report(
        a.instance(),
        beforeCollection.getGeneration(),
        beforeCollection.getResourceSelectionRevision(),
        0);
    assertEquals("removed", marketItem(a, defaultInstall).path("state").asString());
    // The resource worker drops the tombstone from the execution spec and retains its CAS record.
    String defaultPackageId = defaultInstall.path("packageId").asString();
    var collected =
        UnifiedSpec.bump(
            beforeCollection.toBuilder()
                .clearUnifiedResources()
                .addAllUnifiedResources(
                    beforeCollection.getUnifiedResourcesList().stream()
                        .filter(ref -> !ref.getIdentity().getPackageId().equals(defaultPackageId))
                        .toList())
                .build());
    marketWorkspace.set(collected, marketWorkspace.node(a.instance()), null);
    assertEquals(
        1,
        admin.update(
            "UPDATE resource.catalog_instance_installation SET collected_generation=? WHERE instance_id=? AND package_id=?",
            collected.getGeneration(),
            a.instance(),
            UUID.fromString(defaultPackageId)));
    assertEquals(0, marketPlan(a).getSelectionsCount());
    var tombstone = marketItem(a, defaultInstall);
    assertEquals("removed", tombstone.path("state").asString());
    assertFalse(tombstone.has("report"));
    assertEquals(
        defaultRow.path("installationRevision").asLong() + 1,
        tombstone.path("installationRevision").asLong());
    var restored =
        result(
            marketSelect(
                a,
                a.device(),
                defaultInstall,
                "enable",
                tombstone.path("installationRevision").asLong(),
                "explicit-restore"),
            202);
    applyMarket(a, marketEvent(a, restored.path("selectionRevision").asLong()), false);
    assertEquals(
        0,
        admin.queryForObject("SELECT count(*) FROM resource.catalog_installation", Integer.class));
    assertEquals(
        7,
        admin.queryForObject(
            "SELECT count(*) FROM resource.outbox_event WHERE event_type='resource.catalog_distribution_requested' AND subject_key=?",
            Integer.class,
            a.instance().toString()));
    assertEquals(
        0,
        admin.queryForObject(
            "SELECT count(*) FROM resource.catalog_distribution WHERE instance_id=?",
            Integer.class,
            b.instance()));

    var pendingPolicy =
        result(marketSelect(b, b.device(), optional, "install", 0, "pending-before-policy"), 202);
    var superseded = marketEvent(b, pendingPolicy.path("selectionRevision").asLong());
    marketRule(b, required, "required");
    var rejected =
        assertThrows(
            auriga.common.error.AurigaException.class,
            () -> TenantContext.call(T1, () -> catalogDelivery.resolve(superseded)));
    assertEquals(auriga.common.error.CommonErrors.PRECONDITION_FAILED, rejected.errorCode());
    assertTrue(marketItem(b, optional).isMissingNode());
    var replacementPlan = marketPlan(b);
    assertEquals(1, replacementPlan.getSelectionsCount());
    assertTrue(replacementPlan.getSelectionRevision() > superseded.getSelectionRevision());
    applyMarket(b, marketEvent(b, replacementPlan.getSelectionRevision()), true);
    assertTrue(marketItem(b, optional).isMissingNode());
    assertFalse(marketItem(b, required).isMissingNode());
    var bSpec = marketWorkspace.spec(b.instance());
    marketWorkspace.set(
        bSpec.toBuilder().setOwnerId(BOB.toString()).build(),
        marketWorkspace.node(b.instance()),
        null);
    deviceStatus(404, deviceRequest(MARKET + "?clientId=" + b.device(), b.token(), null));
    marketWorkspace.set(bSpec, null, null);
    deviceStatus(503, deviceRequest(MARKET + "?clientId=" + b.device(), b.token(), null));

    assertEquals(
        "member",
        result(marketSelect(desktop, desktop.device(), optional, "install", 0, "desktop"), 202)
            .path("reportSource")
            .asString());
    assertEquals("member", marketItem(desktop, optional).path("reportSource").asString());

    var pendingRotation =
        result(
            marketSelect(a, a.device(), optional, "install", previous, "queued-before-rotation"),
            202);
    var queuedBeforeRotation = marketEvent(a, pendingRotation.path("selectionRevision").asLong());
    var replacement = deviceCredentials.managed(T1, ALICE, a.instance(), 2);
    assertNotEquals(a.device(), replacement.device());
    deviceStatus(403, deviceRequest(MARKET + "?clientId=" + a.device(), a.token(), null));
    assertEquals(
        CommonErrors.PRECONDITION_FAILED,
        assertThrows(
                auriga.common.error.AurigaException.class,
                () -> TenantContext.call(T1, () -> catalogDelivery.resolve(queuedBeforeRotation)))
            .errorCode());
    failure(
        marketSelect(a, a.device(), optional, "install", previous, "replaced"),
        403,
        "common.forbidden");
    assertFalse(marketItem(replacement, required).isMissingNode());
    admin.update(
        "UPDATE org.role SET permissions=array_remove(permissions,'auriga.connect.resources.use') WHERE id=?",
        ROLE);
    deviceStatus(
        403,
        deviceRequest(MARKET + "?clientId=" + replacement.device(), replacement.token(), null));
    failure(
        marketSelect(
            replacement, replacement.device(), optional, "install", previous, "no-permission"),
        403,
        "common.forbidden");
    admin.update(
        "UPDATE org.role SET permissions=array_append(permissions,'auriga.connect.resources.use') WHERE id=?",
        ROLE);
    deviceCredentials.revoke(T1, replacement.device());
    deviceStatus(
        403,
        deviceRequest(MARKET + "?clientId=" + replacement.device(), replacement.token(), null));
    assertEquals(
        1,
        admin.queryForObject("SELECT count(*) FROM resource.catalog_installation", Integer.class));
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(booleans = {false, true})
  void pendingMarketSelectionIsCancelledOnlyAfterConfirmedReadLoss(boolean assignment)
      throws Exception {
    CATALOG_WRITES.set(true);
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['auriga.connect','auriga.connect.collab.use','auriga.connect.resources.use','resource.packages.read'] WHERE id=?",
        ROLE);
    UUID controlRole = id(1580);
    assertEquals(
        1,
        admin.update(
            "INSERT INTO org.role(id,tenant_id,key,name_en,name_zh,permissions) SELECT ?,tenant_id,'market-control',name_en,name_zh,permissions FROM org.role WHERE id=?",
            controlRole,
            ROLE));
    assertEquals(
        1,
        admin.update(
            "UPDATE org.role_binding SET role_id=? WHERE tenant_id=? AND subject_id=? AND role_id=?",
            controlRole,
            T1,
            BOB,
            ROLE));
    var revoked = deviceCredentials.issue(T1, ALICE, "managed");
    var authorized = deviceCredentials.issue(T1, BOB, "managed");
    marketWorkspace.seed(T1, ALICE, revoked.instance());
    marketWorkspace.seed(T1, BOB, authorized.instance());
    var identity = marketPackage(1590, 1, "market-read-loss");
    var selected =
        result(
            marketSelect(revoked, revoked.device(), identity, "install", 0, "before-revocation"),
            202);
    var allowed =
        result(
            marketSelect(
                authorized, authorized.device(), identity, "install", 0, "still-authorized"),
            202);
    var event = marketEvent(revoked, selected.path("selectionRevision").asLong());
    var request =
        assignment ? marketResolve(revoked).setSynchronizeAssignments(true).build() : event;
    assertEquals("pending", marketDistribution(event).get("lifecycle"));

    for (var error : List.of(CommonErrors.UNAVAILABLE, CommonErrors.FORBIDDEN)) {
      // A failed permission lookup gives no permissions snapshot for the target.
      org.mockito.Mockito.doThrow(new AurigaException(error))
          .when(resourceAccessInputs)
          .permissions(T1, ALICE);
      try {
        assertEquals(
            error,
            assertThrows(
                    AurigaException.class,
                    () -> TenantContext.call(T1, () -> catalogDelivery.resolve(request)))
                .errorCode());
      } finally {
        org.mockito.Mockito.doCallRealMethod().when(resourceAccessInputs).permissions(T1, ALICE);
      }
      assertEquals("pending", marketDistribution(event).get("lifecycle"));
    }

    admin.update(
        "UPDATE org.role SET permissions=array_remove(permissions,'resource.packages.read') WHERE id=?",
        ROLE);
    failure(
        marketSelect(revoked, revoked.device(), identity, "install", 0, "while-read-revoked"),
        403,
        "common.forbidden");
    assertThrows(
        AurigaException.class,
        () ->
            TenantContext.call(
                T1,
                () ->
                    catalogDelivery.resolve(
                        request.toBuilder().setOwnerId(BOB.toString()).build())));
    assertEquals("pending", marketDistribution(event).get("lifecycle"));
    assertEquals(
        CommonErrors.PRECONDITION_FAILED,
        assertThrows(
                AurigaException.class,
                () -> TenantContext.call(T1, () -> catalogDelivery.resolve(request)))
            .errorCode());
    var cancelled = marketDistribution(event);
    assertEquals("superseded", cancelled.get("lifecycle"));
    assertNull(cancelled.get("next_reconcile_at"));

    var authorizedEvent = marketEvent(authorized, allowed.path("selectionRevision").asLong());
    if (assignment)
      assertEquals(
          authorizedEvent.getSelectionRevision(), marketPlan(authorized).getSelectionRevision());
    applyMarket(authorized, authorizedEvent, assignment);
    assertEquals(1, marketWorkspace.spec(authorized.instance()).getUnifiedResourcesCount());

    admin.update(
        "UPDATE org.role SET permissions=array_append(permissions,'resource.packages.read') WHERE id=?",
        ROLE);
    assertEquals(
        CommonErrors.PRECONDITION_FAILED,
        assertThrows(
                AurigaException.class,
                () -> TenantContext.call(T1, () -> catalogDelivery.resolve(event)))
            .errorCode());
    assertEquals(0, marketPlan(revoked).getSelectionRevision());
    assertTrue(marketItem(revoked, identity).isMissingNode());
    assertEquals("superseded", marketDistribution(event).get("lifecycle"));

    var resubmitted =
        result(
            marketSelect(revoked, revoked.device(), identity, "install", 0, "after-restoration"),
            202);
    var newEvent = marketEvent(revoked, resubmitted.path("selectionRevision").asLong());
    assertTrue(newEvent.getSelectionRevision() > event.getSelectionRevision());
    assertEquals(newEvent.getSelectionRevision(), marketPlan(revoked).getSelectionRevision());
    applyMarket(revoked, newEvent, true);
    assertEquals(1, marketWorkspace.spec(revoked.instance()).getUnifiedResourcesCount());
  }

  private Map<String, Object> marketDistribution(ResolveUnifiedResourcesRequest event) {
    return admin.queryForMap(
        "SELECT lifecycle,next_reconcile_at FROM resource.catalog_distribution WHERE tenant_id=? AND instance_id=? AND selection_revision=?",
        UUID.fromString(event.getTenantId()),
        UUID.fromString(event.getInstanceId()),
        event.getSelectionRevision());
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(strings = {"standard", "native", "managed"})
  void deviceTokensReadInstallAndDownloadWhilePublicationOperationsRemainForbidden(String kind)
      throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['auriga.connect','auriga.connect.collab.use','auriga.connect.resources.use','resource.packages.read','resource.packages.upload','resource.versions.submit','resource.versions.review','resource.versions.publish','resource.distributions.manage'] WHERE id=?",
        ROLE);
    UUID pkg = resource(550, "l4", "private", ALICE, T1, false);
    admin.update("UPDATE resource.resource_package SET name='example-plugin' WHERE id=?", pkg);
    byte[] archive = workflowArchive("valid");
    uploadArchive(pkg, archive, "\"1\"", 201);
    UUID version =
        admin.queryForObject(
            "SELECT id FROM resource.resource_version WHERE package_id=?", UUID.class, pkg);
    String digest =
        admin.queryForObject(
            "SELECT digest FROM resource.resource_version WHERE id=?", String.class, version);
    workflowRequest(
        "/api/resource/v1/packages/" + pkg + "/versions/" + version + "/personal-signatures",
        ALICE,
        "{\"reason\":\"Device resource fixture\"}",
        "\"1\"",
        201);
    admin.update("UPDATE resource.resource_package SET enabled=true WHERE id=?", pkg);
    UUID skill = id(552), skillVersion = id(553);
    byte[] manifest =
        ("{\"kind\":\"skill\",\"name\":\"example-skill\",\"version\":\"1.0.0\",\"artifacts\":[]}")
            .getBytes(StandardCharsets.UTF_8);
    admin.update(
        "INSERT INTO resource.catalog_package(id,tenant_id,owner_id,name,kind,visibility,created_at) VALUES(?,?,?,'example-skill','skill','tenant',now())",
        skill,
        T1,
        ALICE);
    admin.update(
        "INSERT INTO resource.catalog_version(id,tenant_id,package_id,author_id,version,digest,manifest_bytes,manifest,state,minimum_risk,risk,created_at) VALUES(?,?,?,?,'1.0.0',?,?,?::jsonb,'draft','r1','r1',now())",
        skillVersion,
        T1,
        skill,
        ALICE,
        auriga.resource.app.CatalogManifest.digest(manifest),
        manifest,
        new String(manifest, StandardCharsets.UTF_8));
    var grant = deviceCredentials.issue(T1, ALICE, kind);
    if (grant.instance() != null) marketWorkspace.seed(T1, ALICE, grant.instance());
    for (String path :
        List.of(
            "/revocations",
            "/packages",
            "/packages/" + pkg,
            "/packages/" + pkg + "/versions",
            "/packages/" + pkg + "/versions/" + version,
            "/catalog/packages",
            "/catalog/packages/" + skill,
            "/catalog/packages/" + skill + "/versions",
            "/catalog/packages/" + skill + "/versions/" + skillVersion,
            "/member/installations?clientId=" + grant.device(),
            "/catalog/member/installations?clientId=" + grant.device()))
      deviceStatus(200, deviceRequest(path, grant.token(), null));
    var manifestDownload =
        deviceRequest(
            "/catalog/packages/" + skill + "/versions/" + skillVersion + "/manifest",
            grant.token(),
            null);
    deviceStatus(200, manifestDownload);
    assertArrayEquals(manifest, manifestDownload.body());
    var identity =
        JSON.createObjectNode()
            .put("clientId", grant.device().toString())
            .put("packageId", pkg.toString())
            .put("versionId", version.toString())
            .put("digest", digest);
    deviceStatus(
        202,
        deviceRequest(
            "/member/installations",
            grant.token(),
            identity.deepCopy().put("action", "install").toString()));
    var ticketResponse =
        deviceRequest("/member/download-tickets", grant.token(), identity.toString());
    deviceStatus(201, ticketResponse);
    String ticket = JSON.readTree(ticketResponse.body()).path("ticket").asString();
    var download =
        deviceRequest(
            "/member/downloads",
            grant.token(),
            JSON.createObjectNode()
                .put("clientId", grant.device().toString())
                .put("ticket", ticket)
                .toString());
    deviceStatus(200, download);
    assertArrayEquals(archive, download.body());
    deviceStatus(
        200,
        deviceRequest(
            "/packages/" + pkg + "/versions/" + version + "/content", grant.token(), null));
    for (String path :
        List.of(
            "/packages",
            "/catalog/packages",
            "/distributions",
            "/packages/" + pkg + "/versions/" + version + "/submissions",
            "/admin/reviews/" + UUID.randomUUID() + "/decisions",
            "/admin/packages/" + pkg + "/versions/" + version + "/publications")) {
      var denied = deviceRequest(path, grant.token(), "{}");
      deviceStatus(403, denied);
      assertEquals("common.forbidden", JSON.readTree(denied.body()).path("code").asString());
    }
    var distribution = deviceRequest("/distributions/" + UUID.randomUUID(), grant.token(), null);
    deviceStatus(403, distribution);
    assertEquals("common.forbidden", JSON.readTree(distribution.body()).path("code").asString());
    String multipart =
        "--device-resource\r\nContent-Disposition: form-data; name=\"metadata\"\r\n\r\n{}\r\n--device-resource--\r\n";
    for (String path :
        List.of("/packages/" + pkg + "/versions", "/catalog/packages/" + skill + "/versions")) {
      var denied =
          deviceRequest(
              path, grant.token(), multipart, "multipart/form-data; boundary=device-resource");
      deviceStatus(403, denied);
      assertEquals("common.forbidden", JSON.readTree(denied.body()).path("code").asString());
    }
    deviceCredentials.revoke(T1, grant.device());
    deviceStatus(403, deviceRequest("/revocations", grant.token(), null));
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(strings = {"standard", "native", "managed"})
  void deviceRequestsRecheckCurrentMembershipAndBothPermissions(String kind) throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['auriga.connect','auriga.connect.collab.use','auriga.connect.resources.use','resource.packages.read'] WHERE id=?",
        ROLE);
    var grant = deviceCredentials.issue(T1, ALICE, kind);
    deviceStatus(200, deviceRequest("/revocations", grant.token(), null));
    admin.update("UPDATE org.member SET status='disabled' WHERE id=?", ALICE);
    deviceStatus(403, deviceRequest("/revocations", grant.token(), null));
    admin.update("UPDATE org.member SET status='active' WHERE id=?", ALICE);
    for (String permission : List.of("auriga.connect.resources.use", "resource.packages.read")) {
      admin.update(
          "UPDATE org.role SET permissions=array_remove(permissions,?) WHERE id=?",
          permission,
          ROLE);
      deviceStatus(403, deviceRequest("/revocations", grant.token(), null));
      admin.update(
          "UPDATE org.role SET permissions=array_append(permissions,?) WHERE id=?",
          permission,
          ROLE);
    }
    deviceStatus(200, deviceRequest("/revocations", grant.token(), null));
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(strings = {"standard", "native", "managed"})
  void externalDevicesKeepCurrentVisibilityBoundaries(String kind) throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['auriga.connect','auriga.connect.collab.use','auriga.connect.resources.use','resource.packages.read'] WHERE id=?",
        ROLE);
    UUID privatePackage = resource(551, "l4", "private", ALICE, T1, false);
    var grant = deviceCredentials.issue(T1, EXTERNAL, kind);
    deviceStatus(200, deviceRequest("/revocations", grant.token(), null));
    deviceStatus(200, deviceRequest("/packages", grant.token(), null));
    deviceStatus(404, deviceRequest("/packages/" + privatePackage, grant.token(), null));
    deviceStatus(404, deviceRequest("/catalog/packages/" + privatePackage, grant.token(), null));
  }

  @Test
  void workflowUploadsReviewsSignsAndRevokesExactBytes() throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['resource.packages.read','resource.packages.upload','resource.versions.submit','resource.versions.review','resource.versions.publish','resource.distributions.manage'] WHERE id=?",
        ROLE);
    var pkg = resource(190, "l3", "tenant", ALICE, T1, false);
    admin.update("UPDATE resource.resource_package SET name='example-plugin' WHERE id=?", pkg);
    byte[] archive;
    try (var buffer = new java.io.ByteArrayOutputStream()) {
      try (var gzip =
              new org.apache.commons.compress.compressors.gzip.GzipCompressorOutputStream(buffer);
          var tar = new org.apache.commons.compress.archivers.tar.TarArchiveOutputStream(gzip)) {
        byte[] manifest =
            "{\"name\":\"example-plugin\",\"version\":\"1.0.0\",\"auriga\":{\"permissions\":[\"files.read\"]}}"
                .getBytes(StandardCharsets.UTF_8);
        var entry =
            new org.apache.commons.compress.archivers.tar.TarArchiveEntry("package/package.json");
        entry.setSize(manifest.length);
        tar.putArchiveEntry(entry);
        tar.write(manifest);
        tar.closeArchiveEntry();
      }
      archive = buffer.toByteArray();
    }
    String digest =
        "sha256:"
            + HexFormat.of()
                .formatHex(java.security.MessageDigest.getInstance("SHA-256").digest(archive));
    String metadata =
        "{\"version\":\"1.0.0\",\"expectedDigest\":\""
            + digest
            + "\",\"manifest\":{\"name\":\"example-plugin\",\"dshVersionRange\":\"*\",\"os\":\"any\",\"arch\":\"any\"}}";
    var multipart = new java.io.ByteArrayOutputStream();
    multipart.write(
        ("--market-boundary\r\nContent-Disposition: form-data; name=\"metadata\"\r\nContent-Type: application/json\r\n\r\n"
                + metadata
                + "\r\n--market-boundary\r\nContent-Disposition: form-data; name=\"archive\"; filename=\"plugin.tgz\"\r\nContent-Type: application/gzip\r\n\r\n")
            .getBytes(StandardCharsets.UTF_8));
    multipart.write(archive);
    multipart.write("\r\n--market-boundary--\r\n".getBytes(StandardCharsets.UTF_8));
    String base = "/api/resource/v1/packages/" + pkg + "/versions";
    var upload =
        http.send(
            HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + base))
                .header("Authorization", "Bearer " + workflowToken(ALICE))
                .header("Content-Type", "multipart/form-data; boundary=market-boundary")
                .header("Idempotency-Key", UUID.randomUUID().toString())
                .header("If-Match", "\"1\"")
                .POST(HttpRequest.BodyPublishers.ofByteArray(multipart.toByteArray()))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(201, upload.statusCode(), upload.body());
    var version = JSON.readTree(upload.body());
    String id = version.path("id").asString();
    workflowRequest(
        base + "/" + id + "/submissions",
        ALICE,
        "{\"reason\":\"Ready for review\"}",
        "\"99\"",
        412);
    String submitKey = UUID.randomUUID().toString();
    var submission =
        workflowRequest(
            base + "/" + id + "/submissions",
            ALICE,
            "{\"reason\":\"Ready for review\"}",
            "\"1\"",
            201,
            submitKey);
    assertEquals(
        submission,
        workflowRequest(
            base + "/" + id + "/submissions",
            ALICE,
            "{\"reason\":\"Ready for review\"}",
            "\"1\"",
            201,
            submitKey));
    workflowRequest(
        base + "/" + id + "/submissions",
        ALICE,
        "{\"reason\":\"Changed intent\"}",
        "\"1\"",
        409,
        submitKey);
    String review = submission.path("id").asString();
    String decision =
        "{\"decision\":\"approve\",\"reason\":\"Checked files\",\"versionRevision\":2,\"digest\":\""
            + digest
            + "\"}";
    workflowRequest(
        "/api/resource/v1/admin/reviews/" + review + "/decisions", ALICE, decision, "\"1\"", 403);
    workflowRequest(
        "/api/resource/v1/admin/reviews/" + review + "/decisions",
        BOB,
        decision.replace("\"versionRevision\":2", "\"versionRevision\":1"),
        "\"1\"",
        409);
    workflowRequest(
        "/api/resource/v1/admin/reviews/" + review + "/decisions",
        BOB,
        decision.replace("Checked files", " "),
        "\"1\"",
        400);
    String decisionKey = UUID.randomUUID().toString();
    var approved =
        workflowRequest(
            "/api/resource/v1/admin/reviews/" + review + "/decisions",
            BOB,
            decision,
            "\"1\"",
            200,
            decisionKey);
    assertEquals(
        approved,
        workflowRequest(
            "/api/resource/v1/admin/reviews/" + review + "/decisions",
            BOB,
            decision,
            "\"1\"",
            200,
            decisionKey));
    String adminBase = "/api/resource/v1/admin/packages/" + pkg + "/versions/" + id;
    workflowRequest(adminBase + "/publications", ALICE, "{\"reason\":\"Publish\"}", "\"3\"", 403);
    var published =
        workflowRequest(adminBase + "/publications", BOB, "{\"reason\":\"Publish\"}", "\"3\"", 201);
    assertEquals(1, published.path("releaseSequence").asLong());
    var envelope = published.path("resourceEnvelope");
    verifyResourceSignature(envelope);
    assertEquals(digest, envelope.path("statement").path("digest").asString());
    assertTrue(
        Duration.between(
                    Instant.parse(envelope.path("statement").path("issuedAt").asString()),
                    Instant.parse(envelope.path("notAfter").asString()))
                .compareTo(Duration.ofHours(24))
            <= 0);
    workflowMemberDownloadsAndReports(pkg, UUID.fromString(id), digest, archive);
    var workspace = workflowWorkspace();
    UUID instance = id(990), node = id(991);
    var distribution =
        workflowRequest(
            "/api/resource/v1/distributions",
            BOB,
            "{\"instanceId\":\""
                + instance
                + "\",\"selections\":[{\"packageId\":\""
                + pkg
                + "\",\"versionId\":\""
                + id
                + "\",\"action\":\"install\"}]}",
            "\"1\"",
            202);
    assertEquals("requested", distribution.path("state").asString());
    String distributionPath =
        "/api/resource/v1/distributions/" + distribution.path("id").asString();
    assertEquals("resolved", workflowGet(distributionPath).path("state").asString());
    assertEquals("applying", workflowGet(distributionPath).path("state").asString());
    var unauthenticated =
        http.send(
            HttpRequest.newBuilder(
                    URI.create(
                        "http://127.0.0.1:"
                            + port
                            + "/internal/resource/v1/downloads/"
                            + UUID.randomUUID()))
                .header("Authorization", "Bearer " + workflowToken(BOB))
                .header("X-Forwarded-Client-Cert", "forged")
                .GET()
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(401, unauthenticated.statusCode(), unauthenticated.body());
    var spec =
        workspace.store.in(
            T1,
            () -> auriga.workspace.app.WorkspaceService.spec(workspace.store.instance(instance)));
    assertEquals(2, spec.getGeneration());
    assertEquals(1, spec.getResourceSelectionRevision());
    assertEquals(id, spec.getPlugins(0).getResourceEnvelope().getVersionId());
    assertEquals(
        envelope.path("statement").path("issuedAt").asString(),
        spec.getPlugins(0).getResourceEnvelope().getStatement().getIssuedAt());
    var downloads = context.getBean(auriga.resource.app.ResourceDistributions.class);
    try (var content =
        downloads.download(T1, node, UUID.fromString(spec.getPlugins(0).getDownloadId()), false)) {
      assertArrayEquals(archive, content.readAllBytes());
    }
    assertThrows(
        auriga.common.error.AurigaException.class,
        () ->
            downloads.download(
                T1, id(992), UUID.fromString(spec.getPlugins(0).getDownloadId()), false));
    TenantContext.run(
        T1,
        () ->
            workspace.status(
                node,
                auriga.contracts.node.v1.InstanceStatus.newBuilder()
                    .setInstanceId(instance.toString())
                    .setPhase(auriga.contracts.node.v1.InstancePhase.INSTANCE_PHASE_RUNNING)
                    .setObservedGeneration(1)
                    .setReady(true)
                    .build()));
    assertEquals("applying", workflowGet(distributionPath).path("state").asString());
    TenantContext.run(
        T1,
        () ->
            workspace.inventory(
                node,
                auriga.contracts.node.v1.InstanceStatus.newBuilder()
                    .setInstanceId(instance.toString())
                    .setPhase(auriga.contracts.node.v1.InstancePhase.INSTANCE_PHASE_RUNNING)
                    .setObservedGeneration(2)
                    .setReady(true)
                    .build()));
    assertEquals("applied", workflowGet(distributionPath).path("state").asString());
    var renewed =
        downloads.renew(
            T1, node, UUID.fromString(spec.getPlugins(0).getDownloadId()), "renew-once");
    assertEquals(
        auriga.resource.app.ResourceSigning.canonical(renewed),
        auriga.resource.app.ResourceSigning.canonical(
            downloads.renew(
                T1, node, UUID.fromString(spec.getPlugins(0).getDownloadId()), "renew-once")));
    assertTrue(
        Duration.between(Instant.now(), Instant.parse(renewed.path("expiresAt").asString()))
                .compareTo(Duration.ofMinutes(5))
            <= 0);
    // Force the valid signed release into the renewal window; the selection and sequence stay
    // fixed.
    var shortened = (tools.jackson.databind.node.ObjectNode) envelope.deepCopy();
    shortened.put("notAfter", Instant.now().plusSeconds(3600).toString());
    var shortSigned =
        context.getBean(auriga.resource.app.ResourceSigning.class).sign(shortened, false);
    admin.update(
        "UPDATE resource.resource_version SET envelope=CAST(? AS jsonb) WHERE id=?",
        shortSigned.toString(),
        UUID.fromString(id));
    var grantExpiry =
        admin.queryForObject(
            "SELECT expires_at FROM resource.resource_download WHERE id=?",
            java.sql.Timestamp.class,
            UUID.fromString(spec.getPlugins(0).getDownloadId()));
    var refreshing = workflowGet(distributionPath);
    assertEquals(
        grantExpiry,
        admin.queryForObject(
            "SELECT expires_at FROM resource.resource_download WHERE id=?",
            java.sql.Timestamp.class,
            UUID.fromString(spec.getPlugins(0).getDownloadId())));
    assertEquals("applying", refreshing.path("state").asString());
    assertEquals(3, refreshing.path("generation").asLong());
    var refreshed =
        workspace.store.in(
            T1,
            () -> auriga.workspace.app.WorkspaceService.spec(workspace.store.instance(instance)));
    assertEquals(1, refreshed.getResourceSelectionRevision());
    assertEquals(1, refreshed.getPlugins(0).getResourceEnvelope().getReleaseSequence());
    assertEquals(id, refreshed.getPlugins(0).getResourceEnvelope().getVersionId());
    TenantContext.run(
        T1,
        () ->
            workspace.inventory(
                node,
                auriga.contracts.node.v1.InstanceStatus.newBuilder()
                    .setInstanceId(instance.toString())
                    .setPhase(auriga.contracts.node.v1.InstancePhase.INSTANCE_PHASE_RUNNING)
                    .setObservedGeneration(3)
                    .setReady(true)
                    .build()));
    assertEquals("applied", workflowGet(distributionPath).path("state").asString());
    var download = request("/" + pkg + "/versions/" + id + "/content", token(T1, BOB, true));
    assertEquals(200, download.statusCode(), download.body());
    workflowRequest(
        adminBase + "/withdrawals",
        BOB,
        "{\"mode\":\"withdrawn\",\"reason\":\"Retired\"}",
        "\"4\"",
        200);
    assertEquals(
        auriga.contracts.node.v1.DesiredState.DESIRED_STATE_RUNNING,
        workspace
            .store
            .in(
                T1,
                () ->
                    auriga.workspace.app.WorkspaceService.spec(workspace.store.instance(instance)))
            .getDesired());
    admin.update(
        "UPDATE resource.resource_version SET envelope=CAST(? AS jsonb) WHERE id=?",
        shortSigned.toString(),
        UUID.fromString(id));
    assertEquals("applying", workflowGet(distributionPath).path("state").asString());
    assertEquals(
        4,
        workspace
            .store
            .in(
                T1,
                () ->
                    auriga.workspace.app.WorkspaceService.spec(workspace.store.instance(instance)))
            .getGeneration());
    assertEquals(
        404,
        request("/" + pkg + "/versions/" + id + "/content", token(T1, BOB, true)).statusCode());
    TenantContext.run(
        T1,
        () ->
            workspace.inventory(
                node,
                auriga.contracts.node.v1.InstanceStatus.newBuilder()
                    .setInstanceId(instance.toString())
                    .setPhase(auriga.contracts.node.v1.InstancePhase.INSTANCE_PHASE_RUNNING)
                    .setObservedGeneration(4)
                    .setReady(true)
                    .build()));
    assertEquals("applied", workflowGet(distributionPath).path("state").asString());
    revokeFailures.set(1);
    workflowRequest(
        adminBase + "/withdrawals",
        BOB,
        "{\"mode\":\"malicious\",\"reason\":\"Unsafe archive\"}",
        "\"5\"",
        200);
    assertEquals(
        404,
        request("/" + pkg + "/versions/" + id + "/content", token(T1, BOB, true)).statusCode());
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_revocations WHERE tenant_id=?",
            Integer.class,
            T1));
    assertThrows(
        auriga.common.error.AurigaException.class,
        () ->
            downloads.download(
                T1, node, UUID.fromString(spec.getPlugins(0).getDownloadId()), false));
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_revocation_delivery WHERE tenant_id=? AND completed_revision<revision",
            Integer.class,
            T1));
    // Refresh may learn the list before the explicit retry. It must still stop the instance.
    workflowGet(distributionPath);
    var stoppedAfterRefresh =
        workspace.store.in(
            T1,
            () -> auriga.workspace.app.WorkspaceService.spec(workspace.store.instance(instance)));
    assertEquals(
        auriga.contracts.node.v1.DesiredState.DESIRED_STATE_STOPPED,
        stoppedAfterRefresh.getDesired());
    assertFalse(stoppedAfterRefresh.getPlugins(0).getEnabled());
    try (var pool = java.util.concurrent.Executors.newFixedThreadPool(2)) {
      var first = pool.submit(downloads::tick);
      var second = pool.submit(downloads::tick);
      first.get(20, java.util.concurrent.TimeUnit.SECONDS);
      second.get(20, java.util.concurrent.TimeUnit.SECONDS);
    }
    assertEquals(
        0,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_revocation_delivery WHERE tenant_id=? AND completed_revision<revision",
            Integer.class,
            T1));
    verifyResourceSignature(workflowGet("/api/resource/v1/revocations"));
    var beforeRepeat =
        admin.queryForObject(
            "SELECT revision FROM resource.resource_revocations WHERE tenant_id=?", Long.class, T1);
    workflowRequest(
        adminBase + "/withdrawals",
        BOB,
        "{\"mode\":\"malicious\",\"reason\":\"Already revoked\"}",
        "\"6\"",
        200);
    assertEquals(
        beforeRepeat,
        admin.queryForObject(
            "SELECT revision FROM resource.resource_revocations WHERE tenant_id=?",
            Long.class,
            T1));
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT jsonb_array_length(document->'revokedArtifacts') FROM resource.resource_revocations WHERE tenant_id=?",
            Integer.class,
            T1));
    // Withdrawal forbids new downloads but an administrator can still remove the installed version.
    var removal =
        workflowRequest(
            "/api/resource/v1/distributions",
            BOB,
            "{\"instanceId\":\""
                + instance
                + "\",\"selections\":[{\"packageId\":\""
                + pkg
                + "\",\"versionId\":\""
                + id
                + "\",\"action\":\"remove\"}]}",
            "\"1\"",
            202);
    assertEquals(
        "resolved",
        workflowGet("/api/resource/v1/distributions/" + removal.path("id").asString())
            .path("state")
            .asString());
    assertEquals("superseded", workflowGet(distributionPath).path("state").asString());
    workflowSupersededHistory(
        distributionPath, UUID.fromString(distribution.path("id").asString()));
    var stopped =
        workspace.store.in(
            T1,
            () -> auriga.workspace.app.WorkspaceService.spec(workspace.store.instance(instance)));
    assertEquals(auriga.contracts.node.v1.DesiredState.DESIRED_STATE_STOPPED, stopped.getDesired());
    assertTrue(
        stopped.getResourceRevocations(0).getRevokedArtifactsList().stream()
            .anyMatch(a -> a.getVersionId().equals(id)));
  }

  void workflowSupersededHistory(String path, UUID distribution) throws Exception {
    for (String previous : List.of("requested", "resolved", "applying", "applied", "failed")) {
      boolean applied = previous.equals("applied"), failed = previous.equals("failed");
      admin.update(
          "UPDATE resource.resource_distribution SET state=?,generation=?,observed_generation=?,ready=?,error_code=?,applied_generation=4 WHERE id=?",
          previous,
          applied ? 4 : 5,
          applied ? 4 : 5,
          applied,
          failed ? "workspace.apply_failed" : null,
          distribution);
      var observed = workflowGet(path);
      String expected = applied || failed ? previous : "superseded";
      assertEquals(expected, observed.path("state").asString(), previous);
      assertEquals(applied ? 4 : 5, observed.path("generation").asLong());
      assertEquals(applied ? 4 : 5, observed.path("observedGeneration").asLong());
      assertEquals(4, observed.path("appliedGeneration").asLong());
      if (failed) assertEquals("workspace.apply_failed", observed.path("errorCode").asString());
      else assertFalse(observed.has("errorCode"));
      assertEquals(observed, workflowGet(path));
    }
  }

  void verifyResourceSignature(JsonNode signed) throws Exception {
    var body = (tools.jackson.databind.node.ObjectNode) signed.deepCopy();
    body.remove("signature");
    var verifier = java.security.Signature.getInstance("Ed25519");
    verifier.initVerify(RESOURCE_KEYS.get(signed.path("signature").path("kid").asString()));
    verifier.update(
        auriga.resource.app.ResourceSigning.canonical(body).getBytes(StandardCharsets.UTF_8));
    assertTrue(
        verifier.verify(
            Base64.getUrlDecoder().decode(signed.path("signature").path("value").asString())));
  }

  @Test
  void workflowUploadRejectsUnsafeArchivesAndChecksConflictsBeforeObjectCommit() throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['resource.packages.read','resource.packages.upload'] WHERE id=?",
        ROLE);
    UUID pkg = resource(198, "l3", "tenant", ALICE, T1, false);
    admin.update("UPDATE resource.resource_package SET name='example-plugin' WHERE id=?", pkg);
    var store = context.getBean(auriga.resource.objectstore.ObjectStore.class);
    int before = store.expiredObjects(Instant.now().plusSeconds(10)).size();
    for (String mode : List.of("escape", "link", "duplicate", "bomb"))
      uploadArchive(pkg, workflowArchive(mode), "\"1\"", 400);
    assertEquals(before, store.expiredObjects(Instant.now().plusSeconds(10)).size());
    uploadArchive(pkg, workflowArchive("valid"), "\"1\"", 201);
    int committed = store.expiredObjects(Instant.now().plusSeconds(10)).size();
    uploadArchive(pkg, workflowArchive("variant"), "\"1\"", 412);
    uploadArchive(pkg, workflowArchive("variant"), "\"2\"", 409);
    assertEquals(committed, store.expiredObjects(Instant.now().plusSeconds(10)).size());
  }

  byte[] workflowArchive(String mode) throws Exception {
    var bytes = new java.io.ByteArrayOutputStream();
    try (var gzip =
            new org.apache.commons.compress.compressors.gzip.GzipCompressorOutputStream(bytes);
        var tar = new org.apache.commons.compress.archivers.tar.TarArchiveOutputStream(gzip)) {
      byte[] manifest =
          "{\"name\":\"example-plugin\",\"version\":\"1.0.0\"}".getBytes(StandardCharsets.UTF_8);
      var entry =
          new org.apache.commons.compress.archivers.tar.TarArchiveEntry("package/package.json");
      entry.setSize(manifest.length);
      tar.putArchiveEntry(entry);
      tar.write(manifest);
      tar.closeArchiveEntry();
      if (!mode.equals("valid")) {
        String name =
            mode.equals("escape")
                ? "../escape"
                : mode.equals("duplicate") ? "package/package.json" : "package/extra";
        byte[] data = mode.equals("bomb") ? new byte[4 * 1024 * 1024] : manifest;
        var extra =
            mode.equals("link")
                ? new org.apache.commons.compress.archivers.tar.TarArchiveEntry(
                    name, org.apache.commons.compress.archivers.tar.TarConstants.LF_SYMLINK)
                : new org.apache.commons.compress.archivers.tar.TarArchiveEntry(name);
        if (mode.equals("link")) extra.setLinkName("../escape");
        else extra.setSize(data.length);
        tar.putArchiveEntry(extra);
        if (!mode.equals("link")) tar.write(data);
        tar.closeArchiveEntry();
      }
    }
    return bytes.toByteArray();
  }

  void uploadArchive(UUID pkg, byte[] archive, String etag, int expected) throws Exception {
    String digest =
        "sha256:"
            + HexFormat.of()
                .formatHex(java.security.MessageDigest.getInstance("SHA-256").digest(archive));
    String metadata =
        "{\"version\":\"1.0.0\",\"expectedDigest\":\""
            + digest
            + "\",\"manifest\":{\"name\":\"example-plugin\",\"dshVersionRange\":\"*\",\"os\":\"any\",\"arch\":\"any\"}}";
    var body = new java.io.ByteArrayOutputStream();
    body.write(
        ("--upload-test\r\nContent-Disposition: form-data; name=\"metadata\"\r\nContent-Type: application/json\r\n\r\n"
                + metadata
                + "\r\n--upload-test\r\nContent-Disposition: form-data; name=\"archive\"; filename=\"plugin.tgz\"\r\nContent-Type: application/gzip\r\n\r\n")
            .getBytes(StandardCharsets.UTF_8));
    body.write(archive);
    body.write("\r\n--upload-test--\r\n".getBytes(StandardCharsets.UTF_8));
    var response =
        http.send(
            HttpRequest.newBuilder(
                    URI.create(
                        "http://127.0.0.1:"
                            + port
                            + "/api/resource/v1/packages/"
                            + pkg
                            + "/versions"))
                .header("Authorization", "Bearer " + workflowToken(ALICE))
                .header("If-Match", etag)
                .header("Idempotency-Key", UUID.randomUUID().toString())
                .header("Content-Type", "multipart/form-data; boundary=upload-test")
                .POST(HttpRequest.BodyPublishers.ofByteArray(body.toByteArray()))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(expected, response.statusCode(), response.body());
  }

  void workflowMemberDownloadsAndReports(UUID pkg, UUID version, String digest, byte[] archive)
      throws Exception {
    UUID client = id(995);
    var identity =
        JSON.createObjectNode()
            .put("clientId", client.toString())
            .put("packageId", pkg.toString())
            .put("versionId", version.toString())
            .put("digest", digest);
    String tickets = "/api/resource/v1/member/download-tickets";
    String ticketKey = UUID.randomUUID().toString();
    var ticket = workflowRequest(tickets, ALICE, identity.toString(), "\"0\"", 201, ticketKey);
    workflowRequest(tickets, ALICE, identity.toString(), "\"0\"", 409, ticketKey);
    String plaintext = ticket.path("ticket").asString();
    assertEquals(
        0,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_operation WHERE response::text LIKE ?",
            Integer.class,
            "%" + plaintext + "%"));
    assertEquals(
        0,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_member_ticket t WHERE to_jsonb(t)::text LIKE ?",
            Integer.class,
            "%" + plaintext + "%"));
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_member_ticket WHERE ticket_hash=?",
            Integer.class,
            auriga.resource.app.ResourceWorkflow.hash(plaintext)));
    assertEquals(version.toString(), ticket.path("resourceEnvelope").path("versionId").asString());
    assertTrue(
        Duration.between(Instant.now(), Instant.parse(ticket.path("expiresAt").asString()))
                .compareTo(Duration.ofSeconds(60))
            <= 0);
    var consume =
        JSON.createObjectNode()
            .put("clientId", client.toString())
            .put("ticket", ticket.path("ticket").asString());
    assertEquals(404, memberDownload(BOB, consume, false).statusCode());
    var bytes = memberDownload(ALICE, consume, false);
    assertEquals(200, bytes.statusCode(), new String(bytes.body(), StandardCharsets.UTF_8));
    assertArrayEquals(archive, bytes.body());
    assertEquals(409, memberDownload(ALICE, consume, false).statusCode());
    var concurrent = workflowRequest(tickets, ALICE, identity.toString(), "\"0\"", 201);
    consume.put("ticket", concurrent.path("ticket").asString());
    assertEquals(400, memberDownload(ALICE, consume, true).statusCode());
    try (var pool = java.util.concurrent.Executors.newFixedThreadPool(2)) {
      var first = pool.submit(() -> memberDownload(ALICE, consume, false).statusCode());
      var second = pool.submit(() -> memberDownload(ALICE, consume, false).statusCode());
      assertEquals(
          Set.of(200, 409),
          Set.of(
              first.get(20, java.util.concurrent.TimeUnit.SECONDS),
              second.get(20, java.util.concurrent.TimeUnit.SECONDS)));
    }
    var expired = workflowRequest(tickets, ALICE, identity.toString(), "\"0\"", 201);
    consume.put("ticket", expired.path("ticket").asString());
    admin.update(
        "UPDATE resource.resource_member_ticket SET expires_at=now()-interval '1 second' WHERE consumed_at IS NULL");
    assertEquals(404, memberDownload(ALICE, consume, false).statusCode());
    // Use IAM-issued credentials with live lineage and current resource capability.
    admin.update(
        "UPDATE org.role SET permissions=array_append(permissions,'auriga.connect.resources.use') WHERE id=?",
        ROLE);
    var deviceGrant = deviceCredentials.issue(T1, ALICE, "native");
    var deviceIdentity = identity.deepCopy().put("clientId", deviceGrant.device().toString());
    var device =
        http.send(
            HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + tickets))
                .header("Authorization", "Bearer " + deviceGrant.token())
                .header("Content-Type", "application/json")
                .header("Idempotency-Key", UUID.randomUUID().toString())
                .POST(HttpRequest.BodyPublishers.ofString(deviceIdentity.toString()))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(201, device.statusCode(), device.body());
    var wrongClient =
        http.send(
            HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + tickets))
                .header(
                    "Authorization",
                    "Bearer " + deviceCredentials.issue(T1, ALICE, "native").token())
                .header("Content-Type", "application/json")
                .header("Idempotency-Key", UUID.randomUUID().toString())
                .POST(HttpRequest.BodyPublishers.ofString(identity.toString()))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(404, wrongClient.statusCode());
    var select = identity.deepCopy().put("action", "install");
    String path = "/api/resource/v1/member/installations";
    String key = UUID.randomUUID().toString();
    var installation = workflowRequest(path, ALICE, select.toString(), "\"0\"", 202, key);
    assertEquals("applying", installation.path("state").asString());
    assertEquals("member", installation.path("reportSource").asString());
    assertEquals(installation, workflowRequest(path, ALICE, select.toString(), "\"0\"", 202, key));
    workflowRequest(path, ALICE, select.toString(), "\"0\"", 412);
    var report = identity.deepCopy().put("installationRevision", 1).put("state", "applied");
    workflowRequest(
        "/api/resource/v1/member/installation-reports", ALICE, report.toString(), "\"1\"", 200);
    select.put("action", "disable");
    var pendingDisable = workflowRequest(path, ALICE, select.toString(), "\"1\"", 202);
    assertFalse(pendingDisable.has("reportedRevision"));
    workflowRequest(
        "/api/resource/v1/member/installation-reports", ALICE, report.toString(), "\"1\"", 409);
    admin.update("UPDATE resource.resource_package SET level='l1' WHERE id=?", pkg);
    workflowRequest(path, ALICE, select.toString(), "\"2\"", 403);
    select.put("action", "remove");
    workflowRequest(path, ALICE, select.toString(), "\"2\"", 403);
    admin.update("UPDATE resource.resource_package SET level='l2' WHERE id=?", pkg);
    workflowRequest(path, ALICE, select.toString(), "\"2\"", 403);
    select.put("action", "enable");
    workflowRequest(path, ALICE, select.toString(), "\"2\"", 202);
    select.put("action", "disable");
    workflowRequest(path, ALICE, select.toString(), "\"3\"", 202);
    admin.update("UPDATE resource.resource_package SET level='l3' WHERE id=?", pkg);
    select.put("action", "remove");
    workflowRequest(path, ALICE, select.toString(), "\"4\"", 202);
    assertEquals(
        1L,
        admin.queryForObject(
            "SELECT high_water FROM resource.resource_member_installation WHERE tenant_id=? AND member_id=? AND client_id=? AND package_id=?",
            Long.class,
            T1,
            ALICE,
            client,
            pkg));
    assertTrue(workflowGet(path + "?clientId=" + client).path("items").isEmpty());
  }

  HttpResponse<byte[]> memberDownload(UUID account, JsonNode body, boolean range) throws Exception {
    var request =
        HttpRequest.newBuilder(
                URI.create("http://127.0.0.1:" + port + "/api/resource/v1/member/downloads"))
            .header("Authorization", "Bearer " + workflowToken(account))
            .header("Content-Type", "application/json")
            .header("Idempotency-Key", UUID.randomUUID().toString())
            .POST(HttpRequest.BodyPublishers.ofString(body.toString()));
    if (range) request.header("Range", "bytes=0-1");
    return http.send(request.build(), HttpResponse.BodyHandlers.ofByteArray());
  }

  JsonNode workflowGet(String path) throws Exception {
    var response =
        http.send(
            HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + path))
                .header("Authorization", "Bearer " + workflowToken(BOB))
                .GET()
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(200, response.statusCode(), response.body());
    return JSON.readTree(response.body());
  }

  @Test
  void workflowGovernanceRequiresExplicitOwnerAndProtectsLevelsAndDefault() throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['resource.packages.read','resource.levels.admin'] WHERE id=?",
        ROLE);
    UUID pkg = resource(194, "l3", "tenant", ALICE, T1, false);
    String path = "/api/resource/v1/admin/packages/" + pkg;
    workflowPatch(path, "{\"level\":\"l2\",\"reason\":\"Policy\"}", "\"1\"", 403);
    UUID ownerRole =
        admin.queryForObject(
            "SELECT id FROM org.role WHERE tenant_id=? AND key='owner' AND builtin=true",
            UUID.class,
            T1);
    admin.update(
        "INSERT INTO org.role_binding(id,tenant_id,role_id,subject_type,subject_id) VALUES(?,?,?,'member',?)",
        UuidV7.next(),
        T1,
        ownerRole,
        BOB);
    workflowPatch(path, "{\"level\":\"l0\",\"reason\":\"Policy\"}", "\"1\"", 400);
    workflowPatch(path, "{\"visibility\":\"private\",\"reason\":\"Policy\"}", "\"1\"", 400);
    workflowPatch(path, "{\"enabled\":true,\"reason\":\"Policy\"}", "\"1\"", 409);
    workflowPatch(path, "{\"level\":\"l2\",\"reason\":\"Policy\"}", "\"9\"", 412);
    var result = workflowPatch(path, "{\"level\":\"l2\",\"reason\":\"Policy\"}", "\"1\"", 200);
    assertEquals("l2", result.path("level").asString());
    assertEquals(2, result.path("revision").asLong());
    UUID fixed = resource(195, "l0", "tenant", ALICE, T1, false);
    workflowPatch(
        "/api/resource/v1/admin/packages/" + fixed,
        "{\"level\":\"l3\",\"reason\":\"Policy\"}",
        "\"1\"",
        403);
  }

  JsonNode workflowPatch(String path, String body, String etag, int status) throws Exception {
    var response =
        http.send(
            HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + path))
                .header("Authorization", "Bearer " + workflowToken(BOB))
                .header("Content-Type", "application/json")
                .header("If-Match", etag)
                .method("PATCH", HttpRequest.BodyPublishers.ofString(body))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(status, response.statusCode(), response.body());
    return JSON.readTree(response.body());
  }

  @Test
  void workflowResetsRejectedVersionsAndSignsOnlyPermittedOwnerPrivateDrafts() throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['resource.packages.read','resource.packages.upload','resource.versions.submit'] WHERE id=?",
        ROLE);
    UUID shared = resource(196, "l3", "tenant", ALICE, T1, false),
        rejected = version(197, shared, ALICE, "rejected");
    admin.update("UPDATE resource.resource_version SET archive_verified=true WHERE id=?", rejected);
    String path = "/api/resource/v1/packages/" + shared + "/versions/" + rejected;
    workflowRequest(path + "/submissions", ALICE, "{\"reason\":\"Retry\"}", "\"1\"", 409);
    workflowRequest(path + "/drafts", BOB, "{\"reason\":\"Retry\"}", "\"1\"", 403);
    assertEquals(
        "draft",
        workflowRequest(path + "/drafts", ALICE, "{\"reason\":\"Retry\"}", "\"1\"", 200)
            .path("state")
            .asString());
    assertEquals(
        3,
        workflowRequest(path + "/submissions", ALICE, "{\"reason\":\"Retry\"}", "\"2\"", 201)
            .path("versionRevision")
            .asLong());
    UUID personal = resource(198, "l4", "private", ALICE, T1, false),
        draft = version(199, personal, ALICE, "draft");
    admin.update(
        "UPDATE resource.resource_version SET archive_verified=true,manifest=CAST(? AS jsonb) WHERE id=?",
        "{\"manifest\":{\"name\":\"example-personal\",\"os\":\"any\",\"arch\":\"any\"}}",
        draft);
    String sign =
        "/api/resource/v1/packages/" + personal + "/versions/" + draft + "/personal-signatures";
    workflowRequest(sign, BOB, "{\"reason\":\"Personal\"}", "\"1\"", 404);
    admin.update(
        "UPDATE org.policy SET value='{\"allowPersonalPlugins\":false}' WHERE tenant_id=? AND key='workspace'",
        T1);
    workflowRequest(sign, ALICE, "{\"reason\":\"Personal\"}", "\"1\"", 404);
    admin.update(
        "UPDATE org.policy SET value='{\"allowPersonalPlugins\":true}' WHERE tenant_id=? AND key='workspace'",
        T1);
    var result = workflowRequest(sign, ALICE, "{\"reason\":\"Personal\"}", "\"1\"", 201);
    assertEquals("published", result.path("state").asString());
    assertEquals(1, result.path("releaseSequence").asLong());
    assertEquals(
        "release-test", result.path("resourceEnvelope").path("signature").path("kid").asString());
  }

  auriga.workspace.app.WorkspaceService workflowWorkspace() {
    var roles =
        PostgresExtension.serviceRoles(
            ResourceReadAssemblyTest.class, "resource_ws_owner", "resource_ws_app", "workspace");
    CommonMigrations.migrate(roles.owner().dataSource(), "workspace");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("workspace")
                .defaultSchema("workspace")
                .locations("classpath:db/workspace")
                .load())
        .migrate();
    var store =
        new auriga.workspace.persistence.WorkspaceStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
    store.register(T1, "tenant", T1);
    var inputs =
        new auriga.workspace.api.WorkspaceInputs() {
          public auriga.contracts.org.v1.Member member(UUID tenant, UUID owner) {
            return context
                .getBean(auriga.resource.api.ResourceAccessInputs.class)
                .member(tenant, owner);
          }

          public auriga.contracts.org.v1.InstanceQuota quota(UUID tenant, UUID owner) {
            return auriga.contracts.org.v1.InstanceQuota.getDefaultInstance();
          }

          public auriga.org.web.model.WorkspacePolicy policy(UUID tenant) {
            return new auriga.org.web.model.WorkspacePolicy().allowPersonalPlugins(true);
          }
        };
    var crypto =
        new auriga.workspace.domain.WorkspaceCrypto(
            auriga.workspace.domain.WorkspaceCrypto.random(32));
    var service =
        new auriga.workspace.app.WorkspaceService(
            store,
            inputs,
            new auriga.workspace.app.WorkspaceTemplates(List.of()),
            crypto,
            new OutboxEventPublisher(
                store.jdbc,
                "workspace",
                "workspace",
                new EventSchemas(),
                JSON,
                Clock.systemUTC(),
                () -> {}),
            Clock.systemUTC());
    var port = new PlatformResourceWorkspace(service, inputs, null);
    ((org.springframework.context.ConfigurableApplicationContext) context)
        .getBeanFactory()
        .registerSingleton(
            "workflowResourceWorkspace",
            java.lang.reflect.Proxy.newProxyInstance(
                getClass().getClassLoader(),
                new Class<?>[] {auriga.resource.api.ResourceWorkspace.class},
                (proxy, method, args) -> {
                  if (method.getName().equals("revoke")
                      && revokeFailures.getAndUpdate(n -> Math.max(0, n - 1)) > 0)
                    throw new auriga.common.error.AurigaException(
                        auriga.common.error.CommonErrors.UNAVAILABLE);
                  try {
                    return method.invoke(port, args);
                  } catch (java.lang.reflect.InvocationTargetException e) {
                    throw e.getCause();
                  }
                }));
    byte[] sealing =
        new org.bouncycastle.crypto.params.X25519PrivateKeyParameters(new SecureRandom())
            .generatePublicKey()
            .getEncoded();
    UUID instance = id(990), node = id(991);
    var spec =
        auriga.contracts.node.v1.InstanceSpec.newBuilder()
            .setInstanceId(instance.toString())
            .setTenantId(T1.toString())
            .setOwnerId(ALICE.toString())
            .setGeneration(1)
            .setDesired(auriga.contracts.node.v1.DesiredState.DESIRED_STATE_RUNNING)
            .build();
    admin.update(
        "INSERT INTO workspace.node(id,tenant_id,name,hostname,os,arch,agent_version,sealing_key,certificate_serial,certificate_expires_at,joined_at) VALUES(?,?,'example','example','linux','amd64','1.0.0',?,'test',now()+interval '1 day',now())",
        node,
        T1,
        sealing);
    admin.update(
        "INSERT INTO workspace.instance(id,tenant_id,owner_id,name,node_id,spec,generation,token_hash,token_encrypted,last_activity_at,created_at,updated_at) VALUES(?,?,?,'example',?,?,1,?,?,now(),now(),now())",
        instance,
        T1,
        ALICE,
        node,
        spec.toByteArray(),
        new byte[32],
        crypto.encrypt(new byte[32], T1 + "/" + instance));
    return service;
  }

  String workflowToken(UUID account) throws Exception {
    return workflowToken(account, null);
  }

  String workflowToken(UUID account, UUID device) throws Exception {
    Instant now = Instant.now();
    var claims =
        new JWTClaimsSet.Builder()
            .issuer(ISSUER)
            .audience("auriga")
            .subject(account.toString())
            .claim("tid", T1.toString())
            .claim("ptyp", device == null ? "user" : "device")
            .claim("ctx", device == null ? "admin" : "device")
            .claim("did", device == null ? null : device.toString())
            .claim("sid", device == null ? UUID.randomUUID().toString() : null)
            .claim("mtyp", "member")
            .claim("stepup_time", now.getEpochSecond())
            .claim(
                "perms",
                List.of(
                    "resource.packages.read",
                    "resource.packages.upload",
                    "resource.versions.submit",
                    "resource.versions.review",
                    "resource.versions.publish",
                    "resource.levels.admin",
                    "resource.distributions.manage"))
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
  }

  JsonNode workflowRequest(String path, UUID actor, String body, String etag, int status)
      throws Exception {
    return workflowRequest(path, actor, body, etag, status, UUID.randomUUID().toString());
  }

  JsonNode workflowRequest(
      String path, UUID actor, String body, String etag, int status, String key) throws Exception {
    var response =
        http.send(
            HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + path))
                .header("Authorization", "Bearer " + workflowToken(actor))
                .header("Content-Type", "application/json")
                .header("If-Match", etag)
                .header("Idempotency-Key", key)
                .POST(HttpRequest.BodyPublishers.ofString(body))
                .build(),
            HttpResponse.BodyHandlers.ofString());
    assertEquals(status, response.statusCode(), response.body());
    return JSON.readTree(response.body());
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

  String governanceToken(String ctx) throws Exception {
    var claims =
        new JWTClaimsSet.Builder(SignedJWT.parse(token(T1, ALICE, true)).getJWTClaimsSet())
            .claim("ctx", ctx)
            .claim("stepup_time", Instant.now().getEpochSecond())
            .claim("perms", List.of("resource.versions.review", "resource.versions.publish"))
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
  }

  HttpResponse<String> governanceRequest(String path, String bearer) throws Exception {
    return http.send(
        HttpRequest.newBuilder(
                URI.create("http://127.0.0.1:" + port + "/api/resource/v1/admin/" + path))
            .timeout(Duration.ofSeconds(30))
            .header("Authorization", "Bearer " + bearer)
            .GET()
            .build(),
        HttpResponse.BodyHandlers.ofString());
  }

  @Test
  void governanceReadsSharedPendingVersionsWithoutLeakingPrivateOrOtherTenants() throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['resource.versions.review','resource.versions.publish'] WHERE id=?",
        ROLE);
    var shared = resource(800, "l3", "tenant", BOB, T1, false);
    var pending = version(801, shared, BOB, "pending_review");
    version(802, shared, BOB, "draft");
    var published = version(803, shared, BOB, "published");
    version(804, resource(805, "l4", "private", ALICE, T1, false), ALICE, "pending_review");
    version(806, resource(807, "l3", "private", BOB, T1, false), BOB, "pending_review");
    version(808, resource(809, "l3", "tenant", id(15), T2, false), id(15), "pending_review");
    String bearer = governanceToken("admin");
    var unverified =
        new SignedJWT(
            new JWSHeader.Builder(JWSAlgorithm.ES256)
                .type(new JOSEObjectType("at+jwt"))
                .keyID(KEY.getKeyID())
                .build(),
            new JWTClaimsSet.Builder(SignedJWT.parse(bearer).getJWTClaimsSet())
                .claim("stepup_time", null)
                .build());
    unverified.sign(new ECDSASigner(KEY));
    failure(governanceRequest("review", unverified.serialize()), 403, "iam.step_up_required");
    var review = governanceRequest("review", bearer);
    assertEquals(List.of(pending.toString()), ids(result(review, 200)));
    assertEquals("no-store", review.headers().firstValue("cache-control").orElse(""));
    assertEquals(
        List.of(published.toString()), ids(result(governanceRequest("published", bearer), 200)));
    failure(governanceRequest("review", governanceToken("portal")), 403, "common.forbidden");
    failure(governanceRequest("review", token(T1, ALICE, true)), 403, "common.forbidden");
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['resource.packages.read'] WHERE id=?", ROLE);
    failure(governanceRequest("review", bearer), 403, "common.forbidden");
  }

  @Test
  void governanceCursorIsScopedAndRechecksVisibility() throws Exception {
    admin.update(
        "UPDATE org.role SET permissions=ARRAY['resource.versions.review','resource.versions.publish'] WHERE id=?",
        ROLE);
    var shared = resource(820, "l3", "tenant", BOB, T1, false);
    var firstId = version(821, shared, BOB, "pending_review");
    version(822, shared, BOB, "pending_review");
    String bearer = governanceToken("admin");
    var first = result(governanceRequest("review?limit=1", bearer), 200);
    assertEquals(List.of(firstId.toString()), ids(first));
    String next = cursor(first);
    assertEquals(
        1, ids(result(governanceRequest("review?limit=1&cursor=" + next, bearer), 200)).size());
    failure(
        governanceRequest("published?limit=1&cursor=" + next, bearer),
        400,
        "common.validation_failed");
    admin.update("UPDATE resource.resource_package SET visibility='private' WHERE id=?", shared);
    assertTrue(
        ids(result(governanceRequest("review?limit=1&cursor=" + next, bearer), 200)).isEmpty());
  }
}
