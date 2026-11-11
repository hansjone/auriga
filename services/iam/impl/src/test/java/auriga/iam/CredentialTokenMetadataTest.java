package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class CredentialTokenMetadataTest {
  @Test
  void onlySuccessfullyIssuedServiceTokensPublishMetadataWithoutJwtOrSecrets() throws Exception {
    var keys = mock(SigningKeys.class);
    when(keys.current())
        .thenReturn(new ECKeyGenerator(Curve.P_256).keyID("public-version").generate());
    String secret = Secrets.random();
    var tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            Clock.systemUTC(),
            "https://portal.example.com",
            Map.of("edge", Secrets.hash(secret)));
    assertTrue(tokens.snapshot().entries().isEmpty());
    assertEquals("not_reported", tokens.snapshot().gaps().getFirst().reason());
    assertThrows(
        auriga.common.error.AurigaException.class, () -> tokens.issue("edge", Secrets.random()));
    assertTrue(tokens.snapshot().entries().isEmpty());
    String jwt = tokens.issue("edge", secret);
    var entry = tokens.snapshot().entries().getFirst();
    assertEquals(
        com.nimbusds.jwt.SignedJWT.parse(jwt).getJWTClaimsSet().getJWTID(), entry.versionLabel());
    assertEquals(
        com.nimbusds.jwt.SignedJWT.parse(jwt).getJWTClaimsSet().getExpirationTime().toInstant(),
        entry.expiresAt());
    assertEquals("service:edge", entry.id());
    assertNotNull(entry.issuedAt());
    assertTrue(entry.expiresAt().isAfter(entry.issuedAt()));
    assertFalse(tokens.snapshot().toString().contains(jwt));
    assertFalse(tokens.snapshot().toString().contains(secret));
    assertFalse(tokens.snapshot().toString().contains(Secrets.hash(secret)));
  }
}
