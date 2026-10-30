package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.autoconfigure.AurigaProperties;
import auriga.common.security.AccessTokenVerifier;
import auriga.common.web.LinkOrigins;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.net.URI;
import java.time.Clock;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.springframework.mock.env.MockEnvironment;
import org.springframework.mock.web.MockHttpServletRequest;

class IssuerOriginsConfigurationTest {
  @Test
  void iamFactorySharesApprovedIssuersWhileKeepingItsLocalPublicKeySource() throws Exception {
    String base = "http://192.0.2.10:8080", alias = "https://portal.example.com";
    var access = mock(DeploymentAccess.class);
    when(access.issuer()).thenReturn(base);
    var env =
        new MockEnvironment()
            .withProperty("auriga.iam.issuer", base)
            .withProperty("auriga.links.allowed-origins", alias);
    var key = new ECKeyGenerator(Curve.P_256).keyID("issuer-alias-test").generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    var factory = new IamRuntimeConfiguration();
    var settings =
        factory.iamLocalTokenSettings(access, new AurigaProperties(), Clock.systemUTC(), env);
    assertEquals(URI.create(base + "/oauth2/jwks"), settings.jwkSetUri());
    assertEquals(Set.of(base, alias), settings.acceptedIssuers());
    var signer =
        factory.iamTokens(
            keys, new ContractSchemas(), (t, s, c) -> Set.of(), Clock.systemUTC(), access, env);
    var source = factory.iamLocalJwkSource(keys, settings);
    try {
      var verifier = new AccessTokenVerifier(factory.iamAccessTokenDecoder(settings, source));
      var request = new MockHttpServletRequest();
      request.setScheme("https");
      request.setRemoteAddr("192.0.2.30");
      request.addHeader("Host", "portal.example.com");
      var links = new LinkOrigins(List.of(alias), List.of());
      var issued =
          links.call(
              request,
              () ->
                  signer.managedDevice(
                      UUID.randomUUID(),
                      UUID.randomUUID().toString(),
                      UUID.randomUUID(),
                      java.time.Instant.now().plusSeconds(300),
                      "member"));
      assertNotNull(verifier.verify(issued.accessToken()));
      assertEquals(alias, signer.verify(issued.accessToken()).path("iss").asString());
    } finally {
      if (source instanceof AutoCloseable closeable) closeable.close();
    }
    env.withProperty("auriga.iam.issuer", alias);
    assertThrows(
        IllegalStateException.class,
        () ->
            factory.iamTokens(
                keys,
                new ContractSchemas(),
                (t, s, c) -> Set.of(),
                Clock.systemUTC(),
                access,
                env));
  }
}
