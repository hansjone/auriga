package io.github.hansjone.auriga.common.testing;

import com.nimbusds.jose.JOSEObjectType;
import com.nimbusds.jose.JWSAlgorithm;
import com.nimbusds.jose.JWSHeader;
import com.nimbusds.jose.JWSSigner;
import com.nimbusds.jose.crypto.ECDSASigner;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.ECKey;
import com.nimbusds.jose.jwk.JWKSet;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.JWTClaimsSet;
import com.nimbusds.jwt.SignedJWT;
import com.sun.net.httpserver.HttpServer;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Date;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.Consumer;

/**
 * Test issuer: freshly generated ES256 keys (never stored anywhere), a JWKS endpoint on a random
 * loopback port, and a token builder that defaults to a valid platform access token.
 */
public final class TestTokens implements AutoCloseable {
  public static final String ISSUER = "https://auriga.example.com";
  public static final UUID TENANT_A = UUID.fromString("0192a1b2-0000-7000-8000-00000000000a");
  public static final UUID TENANT_B = UUID.fromString("0192a1b2-0000-7000-8000-00000000000b");

  private final HttpServer server;
  private final List<ECKey> published = new CopyOnWriteArrayList<>();
  private final AtomicInteger fetches = new AtomicInteger();
  private volatile ECKey signingKey;

  public TestTokens() throws Exception {
    signingKey = newKey();
    published.add(signingKey);
    server = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
    server.createContext(
        "/oauth2/jwks",
        exchange -> {
          fetches.incrementAndGet();
          byte[] body =
              new JWKSet(
                      published.stream()
                          .map(k -> (com.nimbusds.jose.jwk.JWK) k.toPublicJWK())
                          .toList())
                  .toString()
                  .getBytes(StandardCharsets.UTF_8);
          exchange.getResponseHeaders().add("Content-Type", "application/json");
          exchange.sendResponseHeaders(200, body.length);
          exchange.getResponseBody().write(body);
          exchange.close();
        });
    server.start();
  }

  public static ECKey newKey() throws Exception {
    return new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
  }

  public URI jwksUri() {
    return URI.create("http://127.0.0.1:" + server.getAddress().getPort() + "/oauth2/jwks");
  }

  public int fetchCount() {
    return fetches.get();
  }

  /** Publishes a new key and signs with it from now on (old key stays published). */
  public void rotate() throws Exception {
    signingKey = newKey();
    published.add(signingKey);
  }

  public ECKey signingKey() {
    return signingKey;
  }

  /** Claims of a valid portal token for tenant A. */
  public static JWTClaimsSet.Builder validClaims() {
    Instant now = Instant.now();
    return new JWTClaimsSet.Builder()
        .issuer(ISSUER)
        .subject("0192a1b2-0000-7000-8000-0000000000a1")
        .audience("auriga")
        .issueTime(Date.from(now))
        .notBeforeTime(Date.from(now))
        .expirationTime(Date.from(now.plusSeconds(300)))
        .jwtID(UUID.randomUUID().toString())
        .claim("tid", TENANT_A.toString())
        .claim("ptyp", "user")
        .claim("ctx", "portal")
        .claim("sid", UUID.randomUUID().toString())
        .claim("perms", List.of("org.members.read"))
        .claim("roles", List.of("member"));
  }

  /** Turns the claims into an internal service token ({@code svc:org}, aud auriga.internal). */
  public static JWTClaimsSet.Builder internalService(JWTClaimsSet.Builder c) {
    return c.subject("svc:org")
        .audience("auriga.internal")
        .claim("tid", "00000000-0000-0000-0000-000000000000")
        .claim("ptyp", "internal_service")
        .claim("ctx", "service")
        .claim("sid", null)
        .claim("perms", List.of())
        .claim("roles", List.of());
  }

  public String token() {
    return token(c -> {});
  }

  public String token(Consumer<JWTClaimsSet.Builder> claims) {
    return token(claims, new JOSEObjectType("at+jwt"), signingKey);
  }

  public String token(Consumer<JWTClaimsSet.Builder> claims, JOSEObjectType type, ECKey key) {
    try {
      JWTClaimsSet.Builder builder = validClaims();
      claims.accept(builder);
      JWSHeader.Builder header = new JWSHeader.Builder(JWSAlgorithm.ES256).keyID(key.getKeyID());
      if (type != null) header.type(type);
      SignedJWT jwt = new SignedJWT(header.build(), builder.build());
      JWSSigner signer = new ECDSASigner(key);
      jwt.sign(signer);
      return jwt.serialize();
    } catch (Exception e) {
      throw new IllegalStateException(e);
    }
  }

  @Override
  public void close() {
    server.stop(0);
  }
}
