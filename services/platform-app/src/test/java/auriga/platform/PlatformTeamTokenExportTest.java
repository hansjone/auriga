package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.iam.*;
import auriga.iam.api.InternalTokenIssuer;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jose.jwk.source.ImmutableJWKSet;
import com.nimbusds.jwt.SignedJWT;
import java.nio.file.*;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.core.env.MapPropertySource;
import org.springframework.mock.env.MockEnvironment;

class PlatformTeamTokenExportTest {
  @TempDir Path root;

  static final class Time extends Clock {
    Instant now = Instant.parse("2026-10-24T00:00:00Z");

    @Override
    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    @Override
    public Clock withZone(ZoneId zone) {
      return this;
    }

    @Override
    public Instant instant() {
      return now;
    }
  }

  @Test
  void fixedAdaptersUseRealIssuerIndependentSubjectsAndCredentialsAndRotate() throws Exception {
    var time = new Time();
    var key = new ECKeyGenerator(Curve.P_256).keyID("team-test").generate();
    String gatewaySecret = Secrets.random(), collabSecret = Secrets.random();
    var issuer = issuer(time, key, gatewaySecret, collabSecret);
    var gateway = PlatformTeamTokenExportConfiguration.fixedIssuer(issuer, "ai-gateway");
    var collab = PlatformTeamTokenExportConfiguration.fixedIssuer(issuer, "collab");
    var settings =
        new AccessTokenSettings(
            "https://portal.example.com",
            java.net.URI.create("https://portal.example.com/jwks"),
            null,
            Duration.ZERO,
            null,
            null,
            time,
            false);
    var verifier =
        new AccessTokenVerifier(
            AccessTokenDecoders.createInternal(
                settings, new ImmutableJWKSet<>(new JWKSet(key.toPublicJWK()))));
    String first = gateway.issue("edge", gatewaySecret);
    String other = collab.issue("edge", collabSecret);
    assertEquals("svc:ai-gateway", verifier.verify(first).subject());
    assertEquals("svc:collab", verifier.verify(other).subject());
    assertNotEquals(first, other);
    assertEquals(
        List.of("auriga.internal"), SignedJWT.parse(first).getJWTClaimsSet().getAudience());
    assertThrows(AurigaException.class, () -> gateway.issue("edge", collabSecret));
    assertThrows(AurigaException.class, () -> collab.issue("edge", gatewaySecret));
    assertThrows(IllegalStateException.class, () -> gateway.issue("collab", gatewaySecret));
    assertThrows(
        IllegalStateException.class,
        () -> PlatformTeamTokenExportConfiguration.fixedIssuer(issuer, "edge"));
    time.now = time.now.plusSeconds(240);
    String rotated = gateway.issue("edge", gatewaySecret);
    assertNotEquals(first, rotated);
    time.now = time.now.plusSeconds(61);
    assertThrows(AurigaException.class, () -> verifier.verify(first));
    assertEquals("svc:ai-gateway", verifier.verify(rotated).subject());
  }

  @Test
  void disabledExportRequiresNoSecretsAndRegistersNoPublishers() {
    try (var context = new AnnotationConfigApplicationContext()) {
      context
          .getEnvironment()
          .getPropertySources()
          .addFirst(
              new MapPropertySource(
                  "off", Map.of("auriga.platform.team-token-export.enabled", "false")));
      context.register(PlatformTeamTokenExportConfiguration.class);
      context.refresh();
      assertFalse(context.containsBean("platformTeamTokenFilePublisher"));
    }
  }

  @Test
  void installationRejectsSharedHashesPathsGroupsAndNonCombinedBeforeReadingFiles() {
    var configuration = new PlatformTeamTokenExportConfiguration();
    var env = environment();
    InternalTokenIssuer never =
        (service, secret) -> {
          fail("must not read or issue before installation validation");
          return null;
        };
    env.withProperty("auriga.platform.combined", "false");
    assertThrows(
        IllegalStateException.class,
        () -> configuration.platformTeamTokenFilePublisher(never, env));
    env.withProperty("auriga.platform.combined", "true")
        .withProperty("auriga.iam.internal-secret-hashes.collab", "a".repeat(64));
    assertThrows(
        IllegalStateException.class,
        () -> configuration.platformTeamTokenFilePublisher(never, env));
    env.withProperty("auriga.iam.internal-secret-hashes.collab", "b".repeat(64))
        .withProperty(
            "auriga.platform.team-token-export.collab.installation-secret-file",
            root.resolve("gateway.secret").toString());
    assertThrows(
        IllegalStateException.class,
        () -> configuration.platformTeamTokenFilePublisher(never, env));
    env.withProperty(
            "auriga.platform.team-token-export.collab.installation-secret-file",
            root.resolve("collab.secret").toString())
        .withProperty("auriga.platform.team-token-export.collab.reader-group", "0");
    assertThrows(
        IllegalStateException.class,
        () -> configuration.platformTeamTokenFilePublisher(never, env));
    env.withProperty("auriga.platform.team-token-export.collab.reader-group", "1");
    try (var publisher = configuration.platformTeamTokenFilePublisher(never, env)) {
      assertFalse(publisher.isRunning());
      if (!root.getFileSystem().supportedFileAttributeViews().contains("posix")) {
        assertThrows(IllegalStateException.class, publisher::start);
        assertFalse(publisher.isRunning());
        assertFalse(Files.exists(root.resolve("gateway/token.jwt")));
      }
    }
  }

  @Test
  void publisherPairReusesSecureFilesAndStopsBothWhenOneStartupFails() throws Exception {
    if (!root.getFileSystem().supportedFileAttributeViews().contains("posix")) {
      var env = environment();
      try (var pair =
          new PlatformTeamTokenExportConfiguration()
              .platformTeamTokenFilePublisher(
                  (service, secret) -> {
                    fail("Windows must refuse before issuing");
                    return null;
                  },
                  env)) {
        assertThrows(IllegalStateException.class, pair::start);
        assertFalse(pair.isRunning());
        pair.stop();
        pair.stop();
      }
      return;
    }
    int gid = (int) Files.getAttribute(root, "unix:gid");
    String gatewaySecret = Secrets.random(), collabSecret = Secrets.random();
    var time = new Time();
    var key = new ECKeyGenerator(Curve.P_256).keyID("file-test").generate();
    var authority = issuer(time, key, gatewaySecret, collabSecret);
    Path privateDir =
        Files.createDirectory(
            root.resolve("private"),
            PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")));
    Path secretA = Files.writeString(privateDir.resolve("gateway.secret"), gatewaySecret);
    Path secretB = Files.writeString(privateDir.resolve("collab.secret"), collabSecret);
    Files.setPosixFilePermissions(secretA, PosixFilePermissions.fromString("rw-------"));
    Files.setPosixFilePermissions(secretB, PosixFilePermissions.fromString("rw-------"));
    Path tokenA = root.resolve("a/token.jwt"), tokenB = root.resolve("b/token.jwt");
    var a =
        new EdgeTokenFilePublisher(
            PlatformTeamTokenExportConfiguration.fixedIssuer(authority, "ai-gateway"),
            secretA,
            tokenA,
            gid);
    var b =
        new EdgeTokenFilePublisher(
            PlatformTeamTokenExportConfiguration.fixedIssuer(authority, "collab"),
            secretB,
            tokenB,
            gid);
    try (var pair = new PlatformTeamTokenExportConfiguration.TeamPublishers(a, b)) {
      pair.start();
      awaitToken(tokenA);
      awaitToken(tokenB);
      String first = Files.readString(tokenA), other = Files.readString(tokenB);
      assertEquals("svc:ai-gateway", SignedJWT.parse(first).getJWTClaimsSet().getSubject());
      assertEquals("svc:collab", SignedJWT.parse(other).getJWTClaimsSet().getSubject());
      time.now = time.now.plusSeconds(240);
      assertTrue(refresh(a));
      assertNotEquals(first, Files.readString(tokenA));
      String previous = Files.readString(tokenB);
      Files.writeString(secretB, gatewaySecret);
      assertFalse(refresh(b));
      assertEquals(previous, Files.readString(tokenB));
      Files.writeString(secretB, collabSecret);
      Files.delete(tokenB);
      Files.createDirectory(tokenB);
      assertFalse(refresh(b));
      Files.delete(tokenB);
      assertTrue(refresh(b));
      pair.stop();
      assertFalse(Files.exists(tokenA));
      assertFalse(Files.exists(tokenB));
    }
    Files.delete(root.resolve("b"));
    Files.createSymbolicLink(root.resolve("b"), root.resolve("a"));
    try (var pair = new PlatformTeamTokenExportConfiguration.TeamPublishers(a, b)) {
      assertThrows(IllegalStateException.class, pair::start);
      assertFalse(a.isRunning());
      assertFalse(b.isRunning());
    }
  }

  @Test
  void edgeDeclaredRefreshRemainsCallableOnEveryPlatform() throws Exception {
    try (var publisher =
        new EdgeTokenFilePublisher(
            (service, secret) -> {
              fail("stopped publisher must not issue a token");
              return null;
            },
            root.resolve("private/secret"),
            root.resolve("export/token"),
            0)) {
      assertFalse(refresh(publisher));
      assertFalse(publisher.isRunning());
    }
  }

  private MockEnvironment environment() {
    return new MockEnvironment()
        .withProperty("auriga.platform.combined", "true")
        .withProperty("auriga.iam.internal-secret-hashes.ai-gateway", "a".repeat(64))
        .withProperty("auriga.iam.internal-secret-hashes.collab", "b".repeat(64))
        .withProperty(
            "auriga.platform.team-token-export.ai-gateway.installation-secret-file",
            root.resolve("gateway.secret").toString())
        .withProperty(
            "auriga.platform.team-token-export.ai-gateway.token-file",
            root.resolve("gateway/token.jwt").toString())
        .withProperty("auriga.platform.team-token-export.ai-gateway.reader-group", "0")
        .withProperty(
            "auriga.platform.team-token-export.collab.installation-secret-file",
            root.resolve("collab.secret").toString())
        .withProperty(
            "auriga.platform.team-token-export.collab.token-file",
            root.resolve("collab/token.jwt").toString())
        .withProperty("auriga.platform.team-token-export.collab.reader-group", "1");
  }

  private static TokenService issuer(Clock time, ECKey key, String gateway, String collab) {
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    return new TokenService(
        keys,
        new ContractSchemas(),
        (tenant, subject, context) -> Set.of(),
        time,
        "https://portal.example.com",
        Map.of("ai-gateway", Secrets.hash(gateway), "collab", Secrets.hash(collab)));
  }

  private static boolean refresh(EdgeTokenFilePublisher publisher) throws Exception {
    var method = EdgeTokenFilePublisher.class.getDeclaredMethod("refresh");
    method.setAccessible(true);
    return (boolean) method.invoke(publisher);
  }

  private static void awaitToken(Path token) throws Exception {
    long deadline = System.nanoTime() + Duration.ofSeconds(10).toNanos();
    try (var watch = token.getFileSystem().newWatchService()) {
      token.getParent().register(watch, StandardWatchEventKinds.ENTRY_CREATE);
      while (!Files.exists(token)) {
        assertTrue(System.nanoTime() < deadline, "Team token was not published");
        var event = watch.poll(1, java.util.concurrent.TimeUnit.SECONDS);
        if (event != null) event.reset();
      }
    }
  }
}
