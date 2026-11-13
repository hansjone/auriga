package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.*;
import auriga.common.error.*;
import auriga.common.grpc.GrpcServer;
import auriga.common.security.*;
import auriga.contracts.workspace.v1.*;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.domain.NodeCa;
import auriga.workspace.grpc.*;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.net.URI;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.security.*;
import java.time.Instant;
import java.util.*;
import org.bouncycastle.asn1.*;
import org.bouncycastle.asn1.x500.X500Name;
import org.bouncycastle.asn1.x509.*;
import org.bouncycastle.asn1.x9.X9ObjectIdentifiers;
import org.bouncycastle.util.io.pem.PemObject;
import org.bouncycastle.util.io.pem.PemWriter;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.aop.support.AopUtils;
import org.springframework.beans.factory.annotation.*;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.ApplicationContext;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.*;

/** The real platform root, including workspace wiring, security advisors and both gRPC servers. */
@SpringBootTest(
    classes = PlatformApplication.class,
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = {
      "auriga.platform.seed=false",
      "auriga.iam.session-kv-nats=false",
      "auriga.services.workspace.enabled=true",
      "auriga.services.resource.enabled=false",
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
class PlatformWorkspaceAssemblyTest {
  @TempDir static Path directory;
  @Autowired ApplicationContext context;
  @Autowired UnifiedResourceApplyGrpc endpoint;

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
  void startsWorkspaceBeansAndBothGrpcServersAndServesHealth() throws Exception {
    assertTrue(AopUtils.isCglibProxy(endpoint));
    for (String name :
        List.of(
            "workspaceUnifiedResources",
            "unifiedResourceWorkspace",
            "unifiedResourceDownloadAuthority",
            "unifiedResourceApplyGrpc")) assertNotNull(context.getBean(name), name);
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

  @Test
  void proxyKeepsAuthenticationAndServiceTenantChecks() {
    UUID tenant = UUID.randomUUID();
    var request =
        CheckUnifiedResourceDownloadRequest.newBuilder().setTenantId(tenant.toString()).build();
    var observer = new RecordingObserver();
    assertEquals(
        CommonErrors.UNAUTHENTICATED,
        assertThrows(
                AurigaException.class,
                () -> endpoint.checkUnifiedResourceDownload(request, observer))
            .errorCode());
    assertEquals(
        CommonErrors.FORBIDDEN,
        assertThrows(
                AurigaException.class,
                () -> call("svc:iam", TenantContext.PLATFORM_TENANT, tenant, request, observer))
            .errorCode());
    assertEquals(
        CommonErrors.FORBIDDEN,
        assertThrows(
                AurigaException.class,
                () -> call("svc:resource", tenant, tenant, request, observer))
            .errorCode());
    assertEquals(
        CommonErrors.FORBIDDEN,
        assertThrows(
                AurigaException.class,
                () ->
                    call(
                        "svc:resource",
                        TenantContext.PLATFORM_TENANT,
                        UUID.randomUUID(),
                        request,
                        observer))
            .errorCode());
    assertFalse(observer.called);
  }

  @Test
  void authenticatedResourceCallReachesRealWorkspaceAuthority() {
    UUID tenant = UUID.randomUUID();
    var request =
        CheckUnifiedResourceDownloadRequest.newBuilder()
            .setTenantId(tenant.toString())
            .setInstanceId(UUID.randomUUID().toString())
            .build();
    var observer = new RecordingObserver();
    // The authorized caller reaches the actual tenant store, which rejects the absent instance.
    assertEquals(
        CommonErrors.NOT_FOUND,
        assertThrows(
                AurigaException.class,
                () ->
                    call("svc:resource", TenantContext.PLATFORM_TENANT, tenant, request, observer))
            .errorCode());
    assertFalse(observer.called);
  }

  void call(
      String subject,
      UUID principalTenant,
      UUID tenant,
      CheckUnifiedResourceDownloadRequest request,
      RecordingObserver observer) {
    var principal =
        new AurigaPrincipal(
            subject,
            principalTenant,
            PrincipalType.INTERNAL_SERVICE,
            TokenContext.SERVICE,
            null,
            null,
            null,
            List.of(),
            Set.of(),
            null,
            null,
            "test");
    RequestContext.start("workspace-assembly", null)
        .withPrincipal(principal)
        .run(
            () ->
                TenantContext.run(
                    tenant, () -> endpoint.checkUnifiedResourceDownload(request, observer)));
  }

  static final class RecordingObserver
      implements StreamObserver<CheckUnifiedResourceDownloadResponse> {
    boolean called;

    @Override
    public void onNext(CheckUnifiedResourceDownloadResponse value) {
      called = true;
    }

    @Override
    public void onError(Throwable error) {
      fail(error);
    }

    @Override
    public void onCompleted() {
      fail("Rejected calls must not complete successfully");
    }
  }
}
