package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.grpc.*;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.WorkspaceModuleConfiguration;
import auriga.workspace.grpc.*;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.security.KeyPairGenerator;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.ApplicationContext;
import org.springframework.context.annotation.*;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.*;

@SpringBootTest(
    classes = StandaloneWorkspaceAssemblyTest.App.class,
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = {
      "auriga.platform.combined=false",
      "auriga.services.workspace.enabled=true",
      "auriga.service.name=workspace",
      "auriga.service.schema=workspace",
      "auriga.grpc.server.enabled=true",
      "auriga.grpc.server.port=0",
      "auriga.workspace.node-server.port=0",
      "auriga.remote.org.target=localhost:1",
      "spring.flyway.schemas=workspace",
      "spring.flyway.locations=classpath:db/workspace",
      "spring.datasource.hikari.maximum-pool-size=4"
    })
@ExtendWith(PostgresExtension.class)
@DirtiesContext
class StandaloneWorkspaceAssemblyTest {
  @TempDir static Path directory;
  @Autowired ApplicationContext context;

  @EnableAutoConfiguration
  @Import(WorkspaceModuleConfiguration.class)
  static class App {
    @Bean
    ServiceTokenProvider tokens() {
      return () -> {
        throw new IllegalStateException("Startup must not call remote services");
      };
    }
  }

  @DynamicPropertySource
  static void properties(DynamicPropertyRegistry registry) throws Exception {
    PostgresExtension.serviceRolesProperties(
        StandaloneWorkspaceAssemblyTest.class,
        "standalone_workspace_owner",
        "standalone_workspace_app",
        "workspace",
        registry);
    var generator = KeyPairGenerator.getInstance("EC");
    generator.initialize(new java.security.spec.ECGenParameterSpec("secp256r1"));
    var key = generator.generateKeyPair();
    Path certificate = directory.resolve("workspace.pem");
    Files.write(certificate, PlatformWorkspaceAssemblyTest.certificate(key));
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
    PlatformWorkspaceAssemblyTest.writePrivateKey(privateKey, key.getPrivate());
    String encryption = PlatformWorkspaceAssemblyTest.randomKey();
    registry.add("auriga.workspace.encryption-key", () -> encryption);
    registry.add("auriga.workspace.control-endpoint", () -> "https://workspace.example.com");
    registry.add("auriga.workspace.ca-certificate-file", certificate::toString);
    registry.add("auriga.workspace.ca-private-key-file", privateKey::toString);
    registry.add("auriga.workspace.tls-certificate-file", certificate::toString);
    registry.add("auriga.workspace.tls-private-key-file", privateKey::toString);
    registry.add("auriga.workspace.org-client-certificate-file", certificate::toString);
    registry.add("auriga.workspace.org-client-key-file", privateKey::toString);
    registry.add("auriga.workspace.org-ca-file", certificate::toString);
  }

  @Test
  void startsStandaloneWorkspaceAndBothGrpcServers() {
    assertFalse(context.containsBean("platformWorkspaceConfiguration"));
    assertTrue(context.getBean(GrpcServer.class).isRunning());
    assertTrue(context.getBean(NodeGrpcServer.class).isRunning());
    assertNotNull(context.getBean("workspaceOrgChannel"));
    for (String name :
        List.of(
            "workspaceUnifiedResources",
            "unifiedResourceDownloadAuthority",
            "unifiedResourceApplyGrpc",
            "workspaceResourceChannel")) assertFalse(context.containsBean(name), name);
  }
}
