package io.github.hansjone.auriga.common.security;

import static org.junit.jupiter.api.Assertions.*;

import com.nimbusds.jose.JOSEObjectType;
import com.nimbusds.jose.JWSAlgorithm;
import com.nimbusds.jose.JWSHeader;
import com.nimbusds.jose.crypto.MACSigner;
import com.nimbusds.jwt.PlainJWT;
import com.nimbusds.jwt.SignedJWT;
import io.github.hansjone.auriga.common.error.AurigaException;
import io.github.hansjone.auriga.common.error.CommonErrors;
import io.github.hansjone.auriga.common.testing.TestTokens;
import java.security.SecureRandom;
import java.time.Duration;
import java.time.Instant;
import java.util.Date;
import java.util.List;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.security.oauth2.jwt.JwtDecoder;

class AccessTokenValidationTest {
  private TestTokens tokens;
  private AccessTokenVerifier verifier;

  @BeforeEach
  void setUp() throws Exception {
    tokens = new TestTokens();
    verifier = new AccessTokenVerifier(decoder(Duration.ofSeconds(30)));
  }

  private JwtDecoder decoder(Duration minRefresh) {
    return AccessTokenDecoders.create(
        new AccessTokenSettings(
            TestTokens.ISSUER, tokens.jwksUri(), null, null, null, minRefresh, null, false));
  }

  @AfterEach
  void tearDown() {
    tokens.close();
  }

  private void assertRejected(String token) {
    AurigaException e = assertThrows(AurigaException.class, () -> verifier.verify(token));
    assertEquals(CommonErrors.UNAUTHENTICATED, e.errorCode());
  }

  @Test
  void validTokenMapsAllClaims() {
    Instant stepUp = Instant.now().minusSeconds(60);
    AurigaPrincipal p =
        verifier.verify(
            tokens.token(
                c ->
                    c.claim("ctx", "admin")
                        .claim("perms", List.of("org.members.write", "org.members.read"))
                        .claim("stepup_time", stepUp.getEpochSecond())
                        .claim("auth_time", stepUp.getEpochSecond() - 100)
                        .claim("mtyp", "member")));
    assertEquals(TestTokens.TENANT_A, p.tenantId());
    assertEquals(PrincipalType.USER, p.type());
    assertEquals(TokenContext.ADMIN, p.context());
    assertTrue(p.hasPermission("org.members.write"));
    assertEquals(stepUp.getEpochSecond(), p.stepUpTime().getEpochSecond());
    assertEquals("member", p.memberType());
    assertNotNull(p.sessionId());
  }

  @Test
  void audienceArrayContainingAurigaIsAccepted() {
    assertNotNull(verifier.verify(tokens.token(c -> c.audience(List.of("auriga", "other")))));
  }

  @Test
  void rejectsMissingOrWrongTyp() throws Exception {
    assertRejected(tokens.token(c -> {}, null, tokens.signingKey()));
    assertRejected(tokens.token(c -> {}, JOSEObjectType.JWT, tokens.signingKey()));
    assertRejected(tokens.token(c -> {}, new JOSEObjectType("id_token+jwt"), tokens.signingKey()));
  }

  @Test
  void acceptsApplicationAtJwtTyp() {
    assertNotNull(
        verifier.verify(
            tokens.token(c -> {}, new JOSEObjectType("application/at+jwt"), tokens.signingKey())));
  }

  @Test
  void rejectsExpiredBeyondSkewButAcceptsWithinSkew() {
    Instant now = Instant.now();
    Date issued = Date.from(now.minusSeconds(300));
    assertRejected(
        tokens.token(
            c ->
                c.issueTime(issued)
                    .notBeforeTime(issued)
                    .expirationTime(Date.from(now.minusSeconds(45)))));
    assertNotNull(
        verifier.verify(
            tokens.token(
                c ->
                    c.issueTime(issued)
                        .notBeforeTime(issued)
                        .expirationTime(Date.from(now.minusSeconds(20))))));
  }

  @Test
  void rejectsNotYetValid() {
    assertRejected(tokens.token(c -> c.notBeforeTime(Date.from(Instant.now().plusSeconds(120)))));
  }

  @Test
  void rejectsMissingExpiry() {
    assertRejected(tokens.token(c -> c.expirationTime(null)));
  }

  @Test
  void rejectsWrongIssuer() {
    assertRejected(tokens.token(c -> c.issuer("https://evil.example.com")));
  }

  @Test
  void rejectsThirdPartyAndGatewayAudiences() {
    assertRejected(tokens.token(c -> c.audience("third-party-client-id")));
    assertRejected(tokens.token(c -> c.audience("auriga.ai-gateway")));
    assertRejected(tokens.token(c -> c.audience((String) null)));
  }

  @Test
  void rejectsMissingOrInvalidPlatformClaims() {
    assertRejected(tokens.token(c -> c.claim("tid", null)));
    assertRejected(tokens.token(c -> c.claim("tid", "not-a-uuid")));
    assertRejected(tokens.token(c -> c.claim("ptyp", "robot")));
    assertRejected(tokens.token(c -> c.claim("ctx", null)));
    assertRejected(tokens.token(c -> c.subject(null)));
    assertRejected(tokens.token(c -> c.jwtID(null)));
  }

  @Test
  void rejectsSignatureByUnpublishedKey() throws Exception {
    assertRejected(tokens.token(c -> {}, new JOSEObjectType("at+jwt"), TestTokens.newKey()));
  }

  @Test
  void rejectsTamperedPayload() {
    String[] parts = tokens.token().split("\\.");
    String forged =
        tokens.token(c -> c.claim("tid", TestTokens.TENANT_B.toString())).split("\\.")[1];
    assertRejected(parts[0] + "." + forged + "." + parts[2]);
  }

  @Test
  void rejectsHmacAndUnsignedTokens() throws Exception {
    byte[] secret = new byte[32];
    new SecureRandom().nextBytes(secret);
    SignedJWT hmac =
        new SignedJWT(
            new JWSHeader.Builder(JWSAlgorithm.HS256)
                .type(new JOSEObjectType("at+jwt"))
                .keyID(tokens.signingKey().getKeyID())
                .build(),
            TestTokens.validClaims().build());
    hmac.sign(new MACSigner(secret));
    assertRejected(hmac.serialize());
    assertRejected(new PlainJWT(TestTokens.validClaims().build()).serialize());
    assertRejected("garbage");
    assertRejected("");
  }

  @Test
  void cachesJwksAndRefetchesOnRotation() throws Exception {
    verifier = new AccessTokenVerifier(decoder(Duration.ofMillis(1)));
    for (int i = 0; i < 5; i++) verifier.verify(tokens.token());
    assertEquals(1, tokens.fetchCount());
    tokens.rotate();
    Thread.sleep(5);
    assertNotNull(verifier.verify(tokens.token()));
    assertEquals(2, tokens.fetchCount());
  }

  @Test
  void unknownKidRefetchIsRateLimited() throws Exception {
    verifier.verify(tokens.token());
    for (int i = 0; i < 5; i++) {
      assertRejected(tokens.token(c -> {}, new JOSEObjectType("at+jwt"), TestTokens.newKey()));
    }
    assertTrue(tokens.fetchCount() <= 2, "fetches: " + tokens.fetchCount());
  }

  @Test
  void nonCanonicalTenantIdIsRejected() {
    assertThrows(
        AurigaException.class,
        () -> verifier.verify(tokens.token(c -> c.claim("tid", "1-1-1-1-1"))));
  }

  @Test
  void platformDecoderRejectsInternalServiceTokens() {
    assertRejected(tokens.token(TestTokens::internalService));
    // aud mixing both still is an internal token: refused by the HTTP API
    assertRejected(
        tokens.token(
            c -> TestTokens.internalService(c).audience(List.of("auriga", "auriga.internal"))));
  }

  @Test
  void internalDecoderAcceptsOnlyInternalServiceTokens() {
    AccessTokenVerifier internal =
        new AccessTokenVerifier(
            AccessTokenDecoders.createInternal(
                AccessTokenSettings.of(TestTokens.ISSUER, tokens.jwksUri())));
    AurigaPrincipal p = internal.verify(tokens.token(TestTokens::internalService));
    assertTrue(p.isInternalService());
    assertEquals("svc:org", p.subject());
    for (String token :
        List.of(
            // external client credentials token (service account), platform audience
            tokens.token(
                c -> c.subject("sa-1").claim("ptyp", "service_account").claim("ctx", "service")),
            // service account that somehow carries the internal audience
            tokens.token(
                c ->
                    TestTokens.internalService(c).subject("sa-1").claim("ptyp", "service_account")),
            // user token
            tokens.token(),
            // internal token whose aud is not only auriga.internal
            tokens.token(
                c -> TestTokens.internalService(c).audience(List.of("auriga.internal", "auriga"))),
            // wrong ctx / subject shape / tenant
            tokens.token(c -> TestTokens.internalService(c).claim("ctx", "admin")),
            tokens.token(c -> TestTokens.internalService(c).subject("org")),
            tokens.token(
                c -> TestTokens.internalService(c).claim("tid", TestTokens.TENANT_A.toString())))) {
      AurigaException e = assertThrows(AurigaException.class, () -> internal.verify(token));
      assertEquals(CommonErrors.UNAUTHENTICATED, e.errorCode());
    }
  }

  @Test
  void jwksUriMustBeHttpsOffLoopback() {
    assertThrows(
        IllegalArgumentException.class,
        () ->
            AccessTokenSettings.of(
                TestTokens.ISSUER, java.net.URI.create("http://iam.internal/oauth2/jwks")));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            AccessTokenSettings.of(
                TestTokens.ISSUER, java.net.URI.create("http://192.0.2.5/jwks")));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            AccessTokenSettings.of(TestTokens.ISSUER, java.net.URI.create("ftp://127.0.0.1/jwks")));
    assertNotNull(
        AccessTokenSettings.of(
            TestTokens.ISSUER, java.net.URI.create("https://iam.internal/jwks")));
    assertNotNull(
        AccessTokenSettings.of(
            TestTokens.ISSUER, java.net.URI.create("http://localhost:8080/jwks")));
    assertNotNull(
        AccessTokenSettings.of(TestTokens.ISSUER, java.net.URI.create("http://[::1]:8080/jwks")));
    assertNotNull(
        new AccessTokenSettings(
            TestTokens.ISSUER,
            java.net.URI.create("http://iam.internal/jwks"),
            null,
            null,
            null,
            null,
            null,
            true));
  }
}
