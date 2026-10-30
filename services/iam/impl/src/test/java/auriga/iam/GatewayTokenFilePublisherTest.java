package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.iam.api.InternalTokenIssuer;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.nio.file.Path;
import java.util.Map;
import java.util.Set;
import org.junit.jupiter.api.Test;

/** Run the same file safety, expiry, rotation and lifecycle suite for the fixed gateway subject. */
class GatewayTokenFilePublisherTest extends EdgeTokenFilePublisherTest {
  @Override
  String service() {
    return "ai-gateway";
  }

  @Override
  Class<?> configuration() {
    return GatewayTokenFileConfiguration.class;
  }

  @Override
  ServiceTokenFilePublisher publisher(
      InternalTokenIssuer issuer, Path secret, Path token, int group, int owner) {
    return new GatewayTokenFilePublisher(issuer, secret, token, group, owner);
  }

  @Test
  void gatewayCredentialCannotIssueAnotherSubjectAndInternalVerifierEnforcesExpiry()
      throws Exception {
    var time = new Time();
    String secret = Secrets.random();
    String edgeSecret = Secrets.random();
    var key = new ECKeyGenerator(Curve.P_256).keyID("gateway-test").generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    var issuer =
        new TokenService(
            keys,
            new ContractSchemas(),
            (tid, sub, ctx) -> Set.of(),
            time,
            "https://portal.example.com",
            Map.of("ai-gateway", Secrets.hash(secret), "edge", Secrets.hash(edgeSecret)));
    var verifier = verifier(time, key);
    String jwt = issuer.issue("ai-gateway", secret);
    assertEquals("svc:ai-gateway", verifier.verify(jwt).subject());
    assertEquals("svc:edge", verifier.verify(issuer.issue("edge", edgeSecret)).subject());
    assertEquals(
        "iam.invalid_service_credentials",
        assertThrows(AurigaException.class, () -> issuer.issue("ai-gateway", edgeSecret))
            .errorCode()
            .code());
    for (String wrong : new String[] {"edge", "collab", "svc:ai-gateway", "workspace"}) {
      assertEquals(
          "iam.invalid_service_credentials",
          assertThrows(AurigaException.class, () -> issuer.issue(wrong, secret))
              .errorCode()
              .code());
    }
    assertEquals(
        "iam.invalid_service_credentials",
        assertThrows(AurigaException.class, () -> issuer.issue("ai-gateway", Secrets.random()))
            .errorCode()
            .code());
    time.now = time.now.plusSeconds(301);
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> verifier.verify(jwt)).errorCode().code());
  }
}
