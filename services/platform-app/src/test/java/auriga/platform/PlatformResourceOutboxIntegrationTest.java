package auriga.platform;

import static org.awaitility.Awaitility.await;
import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.events.NatsEventConsumer;
import auriga.common.events.OutboxForwarder;
import auriga.contracts.node.v1.DesiredState;
import auriga.contracts.node.v1.InstanceSpec;
import auriga.contracts.resource.v1.UnifiedDistributionInput;
import auriga.iam.IamStore;
import auriga.iam.SigningKeys;
import auriga.iam.TokenService;
import auriga.resource.app.CatalogManifest;
import auriga.resource.app.CatalogProtocol;
import auriga.testsupport.NatsExtension;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.app.WorkspaceService;
import auriga.workspace.app.WorkspaceUnifiedResources;
import auriga.workspace.domain.WorkspaceCrypto;
import io.nats.client.Connection;
import java.net.URI;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.SecureRandom;
import java.sql.Timestamp;
import java.time.*;
import java.util.UUID;
import java.util.concurrent.ScheduledExecutorService;
import javax.sql.DataSource;
import org.bouncycastle.crypto.params.X25519PrivateKeyParameters;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.beans.factory.annotation.*;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.ApplicationContext;
import org.springframework.context.annotation.Import;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.*;
import tools.jackson.databind.JsonNode;

/** Uses both production event beans and the combined platform's authenticated write endpoint. */
@SpringBootTest(
    classes = PlatformApplication.class,
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = {
      "auriga.platform.seed=false",
      "auriga.iam.session-kv-nats=false",
      "auriga.services.workspace.enabled=true",
      "auriga.services.resource.enabled=true",
      "auriga.resource.catalog-writes-enabled=true",
      "auriga.resource.catalog-reconcile-ms=86400000",
      "auriga.services.knowledge.enabled=false",
      "auriga.services.collab.enabled=false",
      "auriga.services.audit.enabled=false",
      "auriga.services.ops.enabled=false",
      "auriga.grpc.server.port=0",
      "auriga.workspace.node-server.port=0"
    })
@ActiveProfiles({"combined", "dev"})
@ExtendWith({PostgresExtension.class, NatsExtension.class})
@DirtiesContext
@Import(PlatformWorkspaceAssemblyTest.RenewalConfiguration.class)
class PlatformResourceOutboxIntegrationTest {
  private static final String EVENT = "resource.catalog_distribution_requested";

  @TempDir static Path directory;
  @Autowired ApplicationContext context;
  @Autowired TokenService tokens;
  @Autowired WorkspaceService workspace;
  @Autowired WorkspaceUnifiedResources resources;
  @Autowired WorkspaceCrypto crypto;
  @Autowired Clock clock;

  @Autowired
  @Qualifier("resourceDataSource")
  DataSource resourceSource;

  @Autowired
  @Qualifier("resourceOutboxForwarder")
  OutboxForwarder forwarder;

  @Autowired
  @Qualifier("workspaceUnifiedResourceConsumer")
  NatsEventConsumer consumer;

  @Autowired
  @Qualifier("workspaceTicker")
  ScheduledExecutorService workspaceTicker;

  @Value("${local.server.port}")
  int port;

  @DynamicPropertySource
  static void properties(DynamicPropertyRegistry registry) throws Exception {
    PlatformWorkspaceAssemblyTest.workspaceProperties(
        registry, PlatformResourceOutboxIntegrationTest.class, directory);
    NatsExtension.properties(registry);
  }

  private record Target(UUID tenant, UUID owner, UUID instance, JsonNode identity) {}

  @Test
  void committedResourceEventsReachWorkspaceInboxAndApplyToTheirOwnInstances() throws Exception {
    assertEquals(1, context.getBeansOfType(Connection.class).size());
    assertTrue(forwarder.isRunning());
    assertTrue(consumer.isRunning());
    // Keep reconciliation under the test's control so only the consumer can create the first
    // intent.
    resources.close();
    workspaceTicker.shutdownNow();
    forwarder.stop();
    activateTestSigningKey();

    Target queued = seed("queued"), live = seed("live");
    try (var http = HttpClient.newHttpClient()) {
      assertEquals(404, distribute(http, queued, live, "foreign-instance").statusCode());
      var receipt = accepted(distribute(http, queued, queued, "queued-install"));
      assertEquals(1, receipt.path("selectionRevision").asLong());
      assertEquals(receipt, accepted(distribute(http, queued, queued, "queued-install")));

      var outbox = new JdbcTemplate(resourceSource);
      var event = eventId(outbox, queued);
      var pending =
          outbox.queryForMap(
              "SELECT published_at,attempts FROM resource.outbox_event WHERE id=?", event);
      assertNull(pending.get("published_at"));
      assertEquals(0, pending.get("attempts"));
      assertEquals(0, inboxCount(queued, event));
      assertEquals(
          0L,
          resources
              .snapshot(queued.tenant(), queued.instance())
              .spec()
              .getResourceSelectionRevision());

      forwarder.start();
      verifyDelivered(outbox, queued, event);
      assertEquals(
          0L,
          resources.snapshot(live.tenant(), live.instance()).spec().getResourceSelectionRevision());
      assertEquals(0, inboxCount(live, event));

      var liveReceipt = accepted(distribute(http, live, live, "live-install"));
      assertEquals(1, liveReceipt.path("selectionRevision").asLong());
      var liveEvent = eventId(outbox, live);
      assertNotEquals(event, liveEvent);
      verifyDelivered(outbox, live, liveEvent);
      assertEquals(0, inboxCount(queued, liveEvent));
      assertEquals(
          1L,
          resources
              .snapshot(queued.tenant(), queued.instance())
              .spec()
              .getResourceSelectionRevision());
    }
  }

  private void activateTestSigningKey() {
    var keys = context.getBean(SigningKeys.class);
    var store = context.getBean(IamStore.class);
    String kid = keys.publishNext();
    // The isolated fixture starts with no key because it disables dev seeding.
    store.isolated(
        SigningKeys.PLATFORM,
        () -> {
          var row = store.get("signing_key", kid, true);
          row.put("publishedAt", clock.instant().minus(Duration.ofMinutes(10)).toString());
          store.save("signing_key", row);
          return null;
        });
    keys.activate(kid);
  }

  private UUID eventId(JdbcTemplate outbox, Target target) {
    return outbox.queryForObject(
        "SELECT id FROM resource.outbox_event WHERE tenant_id=? AND event_type=? AND subject_key=?",
        UUID.class,
        target.tenant(),
        EVENT,
        target.instance().toString());
  }

  private int inboxCount(Target target, UUID event) {
    return workspace.store.in(
        target.tenant(),
        () ->
            workspace.store.jdbc.queryForObject(
                "SELECT count(*) FROM workspace.processed_event WHERE tenant_id=? AND event_id=? AND consumer=? AND event_type=?",
                Integer.class,
                target.tenant(),
                event,
                consumer.durableName(),
                EVENT));
  }

  private void verifyDelivered(JdbcTemplate outbox, Target target, UUID event) throws Exception {
    await()
        .atMost(Duration.ofSeconds(20))
        .untilAsserted(
            () -> {
              assertNotNull(
                  outbox.queryForObject(
                      "SELECT published_at FROM resource.outbox_event WHERE id=?",
                      Timestamp.class,
                      event));
              assertEquals(1, inboxCount(target, event));
            });
    var intent =
        workspace.store.in(
            target.tenant(),
            () ->
                workspace.store.jdbc.queryForMap(
                    "SELECT owner_id,request,applied FROM workspace.unified_resource_intent WHERE tenant_id=? AND instance_id=? AND selection_revision=1",
                    target.tenant(),
                    target.instance()));
    assertEquals(target.owner(), intent.get("owner_id"));
    assertEquals(false, intent.get("applied"));
    var input = UnifiedDistributionInput.parseFrom((byte[]) intent.get("request"));
    assertEquals(target.instance().toString(), input.getInstanceId());
    assertEquals(1, input.getSelectionsCount());
    assertEquals("install", input.getSelections(0).getAction());
    assertEquals(CatalogProtocol.identity(target.identity()), input.getSelections(0).getIdentity());

    resources.reconcile();
    var spec = resources.snapshot(target.tenant(), target.instance()).spec();
    assertEquals(1L, spec.getResourceSelectionRevision());
    assertEquals(3L, spec.getGeneration());
    assertEquals(1, spec.getUnifiedResourcesCount());
    assertEquals(input.getSelections(0).getIdentity(), spec.getUnifiedResources(0).getIdentity());
    assertEquals(
        true,
        workspace.store.in(
            target.tenant(),
            () ->
                workspace.store.jdbc.queryForObject(
                    "SELECT applied FROM workspace.unified_resource_intent WHERE tenant_id=? AND instance_id=? AND selection_revision=1",
                    Boolean.class,
                    target.tenant(),
                    target.instance())));
  }

  private HttpResponse<String> distribute(HttpClient http, Target caller, Target target, String key)
      throws Exception {
    var now = clock.instant();
    var token =
        TenantContext.call(
            caller.tenant(),
            () ->
                tokens.access(
                    caller.tenant(),
                    caller.owner().toString(),
                    "auriga",
                    "user",
                    "admin",
                    UUID.randomUUID(),
                    null,
                    now,
                    now,
                    "openid"));
    var body =
        WorkspaceService.JSON.createObjectNode().put("instanceId", target.instance().toString());
    body.putArray("selections")
        .addObject()
        .put("action", "install")
        .set("identity", target.identity());
    return http.send(
        HttpRequest.newBuilder(
                URI.create(
                    "http://127.0.0.1:" + port + "/api/resource/v1/admin/catalog/distributions"))
            .timeout(Duration.ofSeconds(15))
            .header("Authorization", "Bearer " + token)
            .header("Content-Type", "application/json")
            .header("Idempotency-Key", key)
            .header("If-Match", "\"0\"")
            .POST(HttpRequest.BodyPublishers.ofString(body.toString()))
            .build(),
        HttpResponse.BodyHandlers.ofString());
  }

  private JsonNode accepted(HttpResponse<String> response) {
    assertEquals(202, response.statusCode(), response.body());
    return WorkspaceService.JSON.readTree(response.body());
  }

  private Target seed(String name) {
    var admin = new JdbcTemplate(PostgresExtension.database(getClass()).dataSource());
    UUID tenant = UUID.randomUUID(), owner = UUID.randomUUID(), role = UUID.randomUUID();
    UUID node = UUID.randomUUID(), instance = UUID.randomUUID();
    admin.update(
        "INSERT INTO org.tenant(id,tenant_id,slug,display_name) VALUES(?,?,?,?)",
        tenant,
        tenant,
        name,
        name);
    admin.update(
        "INSERT INTO org.member(id,tenant_id,display_name,member_type,status) VALUES(?,?,?,'member','active')",
        owner,
        tenant,
        name);
    admin.update(
        """
        INSERT INTO org.role(id,tenant_id,key,name_en,name_zh,permissions)
        VALUES(?,?,'outbox-test','Outbox','Outbox',ARRAY['resource.packages.read','resource.distributions.manage'])
        """,
        role,
        tenant);
    admin.update(
        "INSERT INTO org.role_binding(id,tenant_id,role_id,subject_type,subject_id) VALUES(?,?,?,'member',?)",
        UUID.randomUUID(),
        tenant,
        role,
        owner);
    admin.update(
        "INSERT INTO org.policy(id,tenant_id,key,value) VALUES(?,?,'workspace','{}')",
        UUID.randomUUID(),
        tenant);

    var sealingKey =
        new X25519PrivateKeyParameters(new SecureRandom()).generatePublicKey().getEncoded();
    admin.update(
        """
        INSERT INTO workspace.node(id,tenant_id,name,hostname,os,arch,agent_version,sealing_key,
          certificate_serial,certificate_expires_at,joined_at)
        VALUES(?,?,?,'test','linux','amd64','test',?,'test',now()+interval '1 day',now())
        """,
        node,
        tenant,
        name,
        sealingKey);
    var spec =
        InstanceSpec.newBuilder()
            .setInstanceId(instance.toString())
            .setTenantId(tenant.toString())
            .setOwnerId(owner.toString())
            .setGeneration(2)
            .setDesired(DesiredState.DESIRED_STATE_RUNNING)
            .setAllowPersonalPlugins(true)
            .build();
    byte[] token = WorkspaceCrypto.random(32);
    admin.update(
        """
        INSERT INTO workspace.instance(id,tenant_id,owner_id,name,node_id,spec,generation,
          observed_generation,phase,ready,catalog_policy_checked,token_hash,token_encrypted,
          last_activity_at,created_at,updated_at)
        VALUES(?,?,?,?,?,?,2,2,'running',true,true,?,?,now(),now(),now())
        """,
        instance,
        tenant,
        owner,
        name,
        node,
        spec.toByteArray(),
        WorkspaceCrypto.hash(token),
        crypto.encrypt(token, tenant + "/" + instance));
    TenantContext.run(tenant, () -> workspace.store.register(tenant, "tenant", tenant));

    UUID pkg = UUID.randomUUID(), version = UUID.randomUUID();
    String manifest =
        """
        {"name":"%s","kind":"skill","version":"1.0.0",
         "artifacts":[{"id":"payload","digest":"sha256:%s","sizeBytes":42}]}
        """
            .formatted(name, "a".repeat(64));
    byte[] manifestBytes = manifest.getBytes(StandardCharsets.UTF_8);
    String digest = CatalogManifest.digest(manifestBytes);
    admin.update(
        """
        INSERT INTO resource.catalog_package(id,tenant_id,owner_id,name,kind,visibility,
          enabled,release_high_water,created_at)
        VALUES(?,?,?,?,'skill','tenant',true,1,now())
        """,
        pkg,
        tenant,
        owner,
        name);
    admin.update(
        """
        INSERT INTO resource.catalog_version(id,tenant_id,package_id,author_id,version,digest,
          manifest_bytes,manifest,state,minimum_risk,risk,release_sequence,created_at)
        VALUES(?,?,?,?,'1.0.0',?,?,?::jsonb,'published','r1','r1',1,now())
        """,
        version,
        tenant,
        pkg,
        owner,
        digest,
        manifestBytes,
        manifest);
    var identity =
        WorkspaceService.JSON
            .createObjectNode()
            .put("tenantId", tenant.toString())
            .put("packageId", pkg.toString())
            .put("versionId", version.toString())
            .put("kind", "skill")
            .put("version", "1.0.0")
            .put("manifestDigest", digest);
    return new Target(tenant, owner, instance, identity);
  }
}
