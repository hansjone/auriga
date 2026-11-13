package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.*;
import auriga.common.grpc.GrpcServer;
import auriga.contracts.node.v1.*;
import auriga.contracts.org.v1.*;
import auriga.contracts.resource.v1.*;
import auriga.resource.api.ResourceAccessInputs;
import auriga.resource.app.*;
import auriga.resource.persistence.*;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.app.*;
import auriga.workspace.domain.NodeCa;
import auriga.workspace.domain.WorkspaceCrypto;
import auriga.workspace.grpc.*;
import java.io.IOException;
import java.net.URI;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.security.*;
import java.time.*;
import java.util.*;
import org.bouncycastle.asn1.*;
import org.bouncycastle.asn1.x500.X500Name;
import org.bouncycastle.asn1.x509.*;
import org.bouncycastle.asn1.x9.X9ObjectIdentifiers;
import org.bouncycastle.crypto.params.X25519PrivateKeyParameters;
import org.bouncycastle.util.io.pem.PemObject;
import org.bouncycastle.util.io.pem.PemWriter;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.beans.factory.annotation.*;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.test.context.TestConfiguration;
import org.springframework.context.ApplicationContext;
import org.springframework.context.annotation.*;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.*;
import org.springframework.test.context.bean.override.convention.TestBean;
import org.springframework.test.context.bean.override.mockito.*;

/** The real platform root, including workspace wiring, security advisors and both gRPC servers. */
@SpringBootTest(
    classes = PlatformApplication.class,
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = {
      "auriga.platform.seed=false",
      "auriga.iam.session-kv-nats=false",
      "auriga.services.workspace.enabled=true",
      "auriga.services.resource.enabled=true",
      "auriga.resource.catalog-reconcile-ms=86400000",
      "auriga.services.knowledge.enabled=false",
      "auriga.services.collab.enabled=false",
      "auriga.services.audit.enabled=false",
      "auriga.services.ops.enabled=false",
      "auriga.grpc.server.enabled=true",
      "auriga.grpc.server.port=0",
      "auriga.workspace.node-server.port=0"
    })
@ActiveProfiles({"combined", "dev"})
@ExtendWith(PostgresExtension.class)
@DirtiesContext
@Import(PlatformWorkspaceAssemblyTest.RenewalConfiguration.class)
class PlatformWorkspaceAssemblyTest {
  @TempDir static Path directory;
  @Autowired ApplicationContext context;
  @Autowired WorkspaceService workspace;
  @Autowired WorkspaceUnifiedResources resources;
  @MockitoSpyBean ResourceStore resourceTransactions;

  @TestBean(name = "clock", methodName = "createRenewalClock")
  RenewalClock clock;

  @MockitoBean WorkspaceInputs inputs;
  @MockitoBean ResourceAccessInputs resourceInputs;
  @MockitoSpyBean CatalogDelivery delivery;
  @MockitoSpyBean ResourceWorkflowStore distributionStore;

  static RenewalClock createRenewalClock() {
    return new RenewalClock();
  }

  @TestConfiguration(proxyBeanMethods = false)
  static class RenewalConfiguration {
    @Bean
    ResourceSigning resourceSigning() throws Exception {
      var generator = KeyPairGenerator.getInstance("Ed25519");
      return new ResourceSigning(
          "release-test",
          generator.generateKeyPair().getPrivate(),
          "revoke-test",
          generator.generateKeyPair().getPrivate());
    }
  }

  static final class RenewalClock extends Clock {
    Instant now = Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);

    @Override
    public Instant instant() {
      return now;
    }

    @Override
    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    @Override
    public Clock withZone(ZoneId zone) {
      return Clock.fixed(now, zone);
    }
  }

  @Value("${local.server.port}")
  int port;

  @DynamicPropertySource
  static void properties(DynamicPropertyRegistry registry) throws Exception {
    var iam =
        PostgresExtension.serviceRole(PlatformWorkspaceAssemblyTest.class, "auriga_iam", "iam");
    var org =
        PostgresExtension.serviceRole(PlatformWorkspaceAssemblyTest.class, "auriga_org", "org");
    registry.add("auriga.platform.iam.jdbc-url", iam::jdbcUrl);
    registry.add("auriga.platform.org.jdbc-url", org::jdbcUrl);
    var roles =
        PostgresExtension.serviceRoles(
            PlatformWorkspaceAssemblyTest.class, "workspace_owner", "workspace_app", "workspace");
    registry.add(
        "auriga.platform.workspace.jdbc-url",
        () ->
            PostgresExtension.database(PlatformWorkspaceAssemblyTest.class)
                .jdbcUrl()
                .split("\\?")[0]);
    registry.add("auriga.platform.workspace.username", () -> roles.runtime().role());
    registry.add("auriga.platform.workspace.migration-username", () -> roles.owner().role());
    var resource =
        PostgresExtension.serviceRoles(
            PlatformWorkspaceAssemblyTest.class, "resource_owner", "resource_app", "resource");
    registry.add(
        "auriga.platform.resource.jdbc-url", () -> resource.runtime().jdbcUrl().split("\\?")[0]);
    registry.add("auriga.platform.resource.username", () -> resource.runtime().role());
    registry.add("auriga.platform.resource.password", () -> "");
    registry.add("auriga.platform.resource.migration-username", () -> resource.owner().role());
    registry.add("auriga.platform.resource.migration-password", () -> "");
    registry.add("auriga.resource.cursor-signing-key", PlatformWorkspaceAssemblyTest::randomKey);
    registry.add("auriga.iam.encryption-key", PlatformWorkspaceAssemblyTest::randomKey);
    registry.add("auriga.workspace.encryption-key", PlatformWorkspaceAssemblyTest::randomKey);
    registry.add("auriga.iam.issuer", () -> "https://portal.example.com");
    registry.add("auriga.security.jwt.issuer", () -> "https://portal.example.com");
    registry.add("auriga.security.jwt.jwk-set-uri", () -> "http://127.0.0.1:1/unused-jwks");
    Path access = directory.resolve("deployment-access.json");
    Files.copy(Path.of("../../contracts/deployment/fixtures/domain-proxy.json"), access);
    registry.add("auriga.iam.deployment-access-file", access::toString);

    var generator = KeyPairGenerator.getInstance("EC");
    generator.initialize(new java.security.spec.ECGenParameterSpec("secp256r1"));
    var key = generator.generateKeyPair();
    Path certificate = directory.resolve("workspace-ca.pem");
    Files.write(certificate, certificate(key));
    Path privateKey = Files.createFile(directory.resolve("workspace-key.pem"));
    var posix = Files.getFileAttributeView(privateKey, PosixFileAttributeView.class);
    if (posix != null) posix.setPermissions(PosixFilePermissions.fromString("rw-------"));
    else {
      var acl = Files.getFileAttributeView(privateKey, AclFileAttributeView.class);
      acl.setAcl(
          List.of(
              AclEntry.newBuilder()
                  .setType(AclEntryType.ALLOW)
                  .setPrincipal(acl.getOwner())
                  .setPermissions(EnumSet.allOf(AclEntryPermission.class))
                  .build()));
    }
    writePrivateKey(privateKey, key.getPrivate());
    registry.add("auriga.workspace.ca-certificate-file", certificate::toString);
    registry.add("auriga.workspace.ca-private-key-file", privateKey::toString);
    registry.add("auriga.workspace.tls-certificate-file", certificate::toString);
    registry.add("auriga.workspace.tls-private-key-file", privateKey::toString);
    registry.add("auriga.workspace.control-endpoint", () -> "https://workspace.example.com");
  }

  static String randomKey() {
    byte[] key = new byte[32];
    new SecureRandom().nextBytes(key);
    return Base64.getEncoder().encodeToString(key);
  }

  static void writePrivateKey(Path file, PrivateKey key) throws IOException {
    try (var writer = new PemWriter(Files.newBufferedWriter(file, StandardCharsets.US_ASCII))) {
      writer.writeObject(new PemObject("PRIVATE KEY", key.getEncoded()));
    }
  }

  static byte[] certificate(KeyPair key) throws Exception {
    var algorithm = new AlgorithmIdentifier(X9ObjectIdentifiers.ecdsa_with_SHA256);
    var body = new V3TBSCertificateGenerator();
    body.setSerialNumber(
        new ASN1Integer(new java.math.BigInteger(120, new SecureRandom()).setBit(0)));
    body.setSignature(algorithm);
    body.setIssuer(new X500Name("CN=workspace-test"));
    body.setSubject(new X500Name("CN=workspace-test"));
    body.setStartDate(new Time(Date.from(Instant.now().minusSeconds(60))));
    body.setEndDate(new Time(Date.from(Instant.now().plusSeconds(86400))));
    body.setSubjectPublicKeyInfo(SubjectPublicKeyInfo.getInstance(key.getPublic().getEncoded()));
    var extensions = new ExtensionsGenerator();
    extensions.addExtension(Extension.basicConstraints, true, new BasicConstraints(true));
    body.setExtensions(extensions.generate());
    var tbs = body.generateTBSCertificate();
    var signer = Signature.getInstance("SHA256withECDSA");
    signer.initSign(key.getPrivate());
    signer.update(tbs.getEncoded("DER"));
    return NodeCa.pem(
        new DERSequence(new ASN1Encodable[] {tbs, algorithm, new DERBitString(signer.sign())})
            .getEncoded("DER"));
  }

  @Test
  void resourceOwnsRenewalAndWorkspaceDoesNotPollResolverDuringLeaseWindow() throws Exception {
    resources.close();
    context
        .getBean("workspaceTicker", java.util.concurrent.ScheduledExecutorService.class)
        .shutdownNow();
    UUID tenant = UUID.randomUUID(), owner = UUID.randomUUID();
    UUID instance = UUID.randomUUID(), node = UUID.randomUUID();
    UUID pkg = UUID.randomUUID(), version = UUID.randomUUID();
    var member =
        Member.newBuilder()
            .setTenantId(tenant.toString())
            .setAccountId(owner.toString())
            .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
            .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
            .build();
    when(inputs.member(tenant, owner)).thenReturn(member);
    when(resourceInputs.member(tenant, owner)).thenReturn(member);
    when(resourceInputs.permissions(tenant, owner))
        .thenReturn(
            GetEffectivePermissionsResponse.newBuilder()
                .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                .addPermissions(ResourceAccess.READ)
                .build());
    Instant start = clock.instant(), expiry = start.plusSeconds(3600);
    var json = WorkspaceService.JSON;
    var manifest =
        json.createObjectNode()
            .put("schemaVersion", 2)
            .put("kind", "skill")
            .put("name", "renewal-fixture")
            .put("version", "1.0.0");
    String artifactDigest = "sha256:" + "b".repeat(64);
    manifest
        .putArray("artifacts")
        .addObject()
        .put("id", "payload")
        .put("digest", artifactDigest)
        .put("role", "payload")
        .put("os", "any")
        .put("arch", "any")
        .put("sizeBytes", 1);
    manifest.putArray("runtimes");
    byte[] manifestBytes = manifest.toString().getBytes(StandardCharsets.UTF_8);
    String digest =
        "sha256:"
            + HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(manifestBytes));
    var identity =
        json.createObjectNode()
            .put("tenantId", tenant.toString())
            .put("packageId", pkg.toString())
            .put("versionId", version.toString())
            .put("kind", "skill")
            .put("version", "1.0.0")
            .put("manifestDigest", digest);
    var envelope =
        json.createObjectNode()
            .put("schemaVersion", 2)
            .put("tenantId", tenant.toString())
            .put("packageId", pkg.toString())
            .put("versionId", version.toString())
            .put("releaseSequence", 1)
            .put("notAfter", expiry.toString());
    var statement =
        envelope
            .putObject("statement")
            .put("purpose", "resource")
            .put("kind", "skill")
            .put("name", "renewal-fixture")
            .put("version", "1.0.0")
            .put("manifestDigest", digest)
            .put("issuedAt", start.toString());
    statement.putArray("artifactDigests").add(artifactDigest);
    var selected =
        json.createObjectNode()
            .put("releaseSequence", 1)
            .put("installationRevision", 1)
            .put("enabled", true)
            .put("desiredState", "ENABLED");
    selected.set("identity", identity);
    selected.set("envelope", context.getBean(ResourceSigning.class).sign(envelope, false));
    selected
        .putObject("lease")
        .put("envelopeNotAfter", expiry.toString())
        .put("revocationsExpiresAt", expiry.toString())
        .put("expiresAt", expiry.toString())
        .put("revocationRevision", 1);
    String base = "/api/resource/v1/catalog/packages/" + pkg + "/versions/" + version;
    selected.put("manifestDownloadPath", base + "/manifest");
    selected
        .putArray("artifacts")
        .addObject()
        .put("artifactId", "payload")
        .put("digest", artifactDigest)
        .put("sizeBytes", 1)
        .put("downloadPath", base + "/artifacts/payload");
    var ref = CatalogProtocol.ref(selected);
    var selection =
        UnifiedSelection.newBuilder().setIdentity(ref.getIdentity()).setAction("install").build();
    var document = json.createObjectNode();
    document.putArray("selections").addObject().put("action", "install").set("identity", identity);
    var resolution = json.createObjectNode().put("selectionRevision", 1);
    resolution.putArray("resources").add(selected);
    byte[] sealingKey =
        new X25519PrivateKeyParameters(new SecureRandom()).generatePublicKey().getEncoded();
    byte[] token = WorkspaceCrypto.random(32);
    byte[] encrypted =
        context.getBean(WorkspaceCrypto.class).encrypt(token, tenant + "/" + instance);

    workspace.store.in(
        tenant,
        () -> {
          workspace.store.jdbc.update(
              """
          INSERT INTO node(id,tenant_id,name,hostname,os,arch,agent_version,sealing_key,
            certificate_serial,certificate_expires_at,joined_at)
          VALUES(?,?,'renewal-node','test','linux','amd64','test',?,'test',?,?)
          """,
              node,
              tenant,
              sealingKey,
              java.sql.Timestamp.from(start.plusSeconds(86400)),
              java.sql.Timestamp.from(start));
          var spec =
              InstanceSpec.newBuilder()
                  .setInstanceId(instance.toString())
                  .setTenantId(tenant.toString())
                  .setOwnerId(owner.toString())
                  .setGeneration(1)
                  .setResourceSelectionRevision(1)
                  .setDesired(DesiredState.DESIRED_STATE_RUNNING)
                  .addUnifiedResources(ref)
                  .build();
          workspace.store.jdbc.update(
              """
          INSERT INTO instance(id,tenant_id,owner_id,name,node_id,spec,generation,
            token_hash,token_encrypted,last_activity_at,created_at,updated_at)
          VALUES(?,?,?,'renewal',?,?,1,?,?,?, ?,?)
          """,
              instance,
              tenant,
              owner,
              node,
              spec.toByteArray(),
              WorkspaceCrypto.hash(token),
              encrypted,
              java.sql.Timestamp.from(start),
              java.sql.Timestamp.from(start),
              java.sql.Timestamp.from(start));
          return null;
        });
    workspace.store.in(
        TenantContext.PLATFORM_TENANT,
        () ->
            workspace.store.jdbc.update(
                "INSERT INTO workspace_registry(id,tenant_id,resource_tenant,kind) VALUES(?,?,?,'tenant')",
                tenant,
                TenantContext.PLATFORM_TENANT,
                tenant));
    resources.record(tenant, instance, owner, 1, List.of(selection));
    acknowledgeRenewal(tenant, instance);
    TenantContext.run(
        tenant,
        () ->
            resourceTransactions.write(
                () -> {
                  distributionStore.update(
                      """
          INSERT INTO resource.catalog_package(id,tenant_id,owner_id,name,kind,visibility,enabled,release_high_water,created_at)
          VALUES(:pkg,:tenant,:owner,'renewal-fixture','skill','tenant',true,1,:now)
          """,
                      distributionStore.args(
                          "pkg", pkg, "owner", owner, "now", java.sql.Timestamp.from(start)));
                  distributionStore.update(
                      """
          INSERT INTO resource.catalog_version(id,tenant_id,package_id,author_id,version,digest,
            manifest_bytes,manifest,state,minimum_risk,risk,release_sequence,created_at)
          VALUES(:version,:tenant,:pkg,:owner,'1.0.0',:digest,:bytes,CAST(:manifest AS jsonb),'published','r1','r1',1,:now)
          """,
                      distributionStore.args(
                          "version",
                          version,
                          "pkg",
                          pkg,
                          "owner",
                          owner,
                          "digest",
                          digest,
                          "bytes",
                          manifestBytes,
                          "manifest",
                          manifest.toString(),
                          "now",
                          java.sql.Timestamp.from(start)));
                  distributionStore.update(
                      """
          INSERT INTO resource.catalog_distribution(id,tenant_id,actor_id,instance_id,owner_id,node_id,
            selection_revision,fingerprint,document,resolution,lifecycle,next_reconcile_at)
          VALUES(:id,:tenant,:owner,:instance,:owner,:node,1,'fixture',CAST(:document AS jsonb),CAST(:resolution AS jsonb),'active',:due)
          """,
                      distributionStore.args(
                          "id",
                          UUID.randomUUID(),
                          "owner",
                          owner,
                          "instance",
                          instance,
                          "node",
                          node,
                          "document",
                          document.toString(),
                          "resolution",
                          resolution.toString(),
                          "due",
                          java.sql.Timestamp.from(expiry.minusSeconds(295))));
                  return null;
                }));
    assertEquals(List.of(tenant), workspace.store.tenants());
    TenantContext.call(
        tenant,
        () ->
            delivery.resolve(
                ResolveUnifiedResourcesRequest.newBuilder()
                    .setTenantId(tenant.toString())
                    .setOwnerId(owner.toString())
                    .setInstanceId(instance.toString())
                    .setNodeId(node.toString())
                    .setSelectionRevision(1)
                    .addSelections(selection)
                    .build()));
    org.mockito.stubbing.Answer<Object> failFast =
        invocation -> {
          try {
            return invocation.callRealMethod();
          } catch (RuntimeException failure) {
            throw new AssertionError("Healthy renewal reconciliation failed", failure);
          }
        };
    doAnswer(failFast).when(resourceTransactions).read(any());
    doAnswer(failFast).when(resourceTransactions).write(any());
    clearInvocations(delivery, distributionStore);
    for (int elapsed = 1805; elapsed <= 3505; elapsed += 5) {
      clock.now = start.plusSeconds(elapsed);
      resources.reconcile();
      delivery.reconcile();
      if (elapsed == 3305) acknowledgeRenewal(tenant, instance);
    }
    var actual = resources.snapshot(tenant, instance).spec();
    assertTrue(
        Instant.parse(actual.getUnifiedResources(0).getLease().getExpiresAt()).isAfter(expiry));
    assertFalse(actual.getUnifiedResources(0).getEnvelope().getSignature().getValue().isEmpty());
    assertEquals(ref.getIdentity(), actual.getUnifiedResources(0).getIdentity());
    assertEquals(1, actual.getUnifiedResources(0).getInstallationRevision());
    verify(delivery, never()).resolve(any());
    long calls =
        mockingDetails(distributionStore).getInvocations().stream()
            .filter(
                i ->
                    i.getMethod().getName().equals("row")
                        && i.getArgument(0)
                            .toString()
                            .contains("instance_id=:instance AND selection_revision=:rev"))
            .count();
    assertTrue(calls >= 1 && calls <= 3, "resource resolution calls per renewal cycle: " + calls);
  }

  void acknowledgeRenewal(UUID tenant, UUID instance) {
    var spec = resources.snapshot(tenant, instance).spec();
    var ref = spec.getUnifiedResources(0);
    var status =
        InstanceStatus.newBuilder()
            .setInstanceId(instance.toString())
            .setReady(true)
            .setObservedGeneration(spec.getGeneration())
            .setUnifiedGuardian(
                UnifiedGuardianState.newBuilder()
                    .setState("active")
                    .setTenantId(tenant.toString())
                    .setOwnerId(spec.getOwnerId())
                    .setHostId(instance.toString())
                    .setGeneration(spec.getGeneration())
                    .setSelectionRevision(1))
            .addUnifiedResourceReports(
                UnifiedInstallationReport.newBuilder()
                    .setClientId(instance.toString())
                    .setIdentity(ref.getIdentity())
                    .setInstallationRevision(ref.getInstallationRevision())
                    .setGeneration(spec.getGeneration())
                    .setObservedGeneration(spec.getGeneration())
                    .setSelectionRevision(1)
                    .setState("applied")
                    .setReportSource("node"))
            .build();
    workspace.store.in(
        tenant,
        () ->
            workspace.store.jdbc.update(
                "UPDATE instance SET observed_generation=?,phase='running',ready=true,status=? WHERE tenant_id=? AND id=?",
                spec.getGeneration(),
                status.toByteArray(),
                tenant,
                instance));
  }

  @Test
  void startsWorkspaceBeansAndBothGrpcServersAndServesHealth() throws Exception {
    for (String name :
        List.of("workspaceUnifiedResources", "unifiedResourceWorkspace", "catalogDelivery"))
      assertNotNull(context.getBean(name), name);
    assertFalse(context.containsBean("unifiedResourceApplyGrpc"));
    assertFalse(context.containsBean("unifiedResourceDownloadAuthority"));
    assertTrue(context.getBean(GrpcServer.class).isRunning());
    assertTrue(context.getBean(NodeGrpcServer.class).isRunning());
    try (var client = HttpClient.newHttpClient()) {
      var response =
          client.send(
              HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/actuator/health"))
                  .GET()
                  .build(),
              HttpResponse.BodyHandlers.ofString());
      assertEquals(200, response.statusCode(), response.body());
      assertTrue(response.body().contains("\"status\":\"UP\""), response.body());
    }
  }
}
