package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.*;
import org.junit.jupiter.api.Test;

class CredentialInstallationScopeTest {
  @Test
  void usesDeploymentAccessTenantInsteadOfInternalZeroTenant() {
    var access = mock(auriga.contracts.deploymentconfig.DeploymentAccess.class);
    UUID configured = UUID.randomUUID();
    when(access.tenantId()).thenReturn(configured.toString());
    var scope = new IamRuntimeConfiguration().credentialInstallationScope(access);
    assertTrue(scope.permits(configured));
    assertFalse(scope.permits(UUID.randomUUID()));
    assertFalse(scope.permits(SigningKeys.PLATFORM));
  }

  @Test
  void runtimeTokenBeanUsesRealDeploymentTenant() throws Exception {
    var access = mock(auriga.contracts.deploymentconfig.DeploymentAccess.class);
    UUID configured = UUID.randomUUID();
    when(access.tenantId()).thenReturn(configured.toString());
    when(access.issuer()).thenReturn("https://portal.example.com");
    String secret = Secrets.random();
    var env =
        new org.springframework.mock.env.MockEnvironment()
            .withProperty("auriga.iam.issuer", access.issuer())
            .withProperty("auriga.iam.internal-secret-hashes.edge", Secrets.hash(secret));
    var keys = mock(SigningKeys.class);
    when(keys.current())
        .thenReturn(
            new com.nimbusds.jose.jwk.gen.ECKeyGenerator(com.nimbusds.jose.jwk.Curve.P_256)
                .keyID("installed")
                .generate());
    var tokens =
        new IamRuntimeConfiguration()
            .iamTokens(
                keys,
                new ContractSchemas(),
                (t, x, c) -> Set.of(),
                java.time.Clock.systemUTC(),
                access,
                env);
    tokens.issue("edge", secret);
    assertEquals(
        1, auriga.common.context.TenantContext.call(configured, tokens::snapshot).entries().size());
    var foreign = auriga.common.context.TenantContext.call(UUID.randomUUID(), tokens::snapshot);
    assertTrue(foreign.entries().isEmpty());
    assertEquals("installation_scope", foreign.gaps().getFirst().reason());
  }
}
