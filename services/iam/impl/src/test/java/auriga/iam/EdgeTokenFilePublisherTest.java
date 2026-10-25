package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.common.security.*;
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

class EdgeTokenFilePublisherTest {
  @TempDir Path dir;

  String service() {
    return "edge";
  }

  Class<?> configuration() {
    return EdgeTokenFileConfiguration.class;
  }

  ServiceTokenFilePublisher publisher(
      auriga.iam.api.InternalTokenIssuer issuer, Path secret, Path token, int group) {
    return publisher(issuer, secret, token, group, -1);
  }

  ServiceTokenFilePublisher publisher(
      auriga.iam.api.InternalTokenIssuer issuer, Path secret, Path token, int group, int owner) {
    return new EdgeTokenFilePublisher(issuer, secret, token, group, owner);
  }

  TokenService serviceIssuer(Time time, String secret, ECKey key) {
    return issuer(time, secret, key, service());
  }

  static final class Time extends Clock {
    volatile Instant now = Instant.parse("2026-10-22T00:00:00Z");

    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(ZoneId zone) {
      return this;
    }

    public Instant instant() {
      return now;
    }
  }

  static TokenService issuer(Time time, String secret, ECKey key) {
    return issuer(time, secret, key, "edge");
  }

  static TokenService issuer(Time time, String secret, ECKey key, String service) {
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    return new TokenService(
        keys,
        new ContractSchemas(),
        (tid, sub, ctx) -> Set.of(),
        time,
        "https://portal.example.com",
        Map.of(service, Secrets.hash(secret)));
  }

  static AccessTokenVerifier verifier(Time time, ECKey key) {
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
    return new AccessTokenVerifier(
        AccessTokenDecoders.createInternal(
            settings, new ImmutableJWKSet<>(new JWKSet(key.toPublicJWK()))));
  }

  boolean posix() {
    return dir.getFileSystem().supportedFileAttributeViews().contains("posix");
  }

  static void awaitToken(Path token) throws Exception {
    long deadline = System.nanoTime() + Duration.ofSeconds(10).toNanos();
    try (var watcher = token.getFileSystem().newWatchService()) {
      token.getParent().register(watcher, StandardWatchEventKinds.ENTRY_CREATE);
      while (!Files.exists(token)) {
        assertTrue(System.nanoTime() < deadline, "initial edge token was not published");
        var event = watcher.poll(1, java.util.concurrent.TimeUnit.SECONDS);
        if (event != null) event.reset();
      }
    }
  }

  int gid() throws Exception {
    return (int) Files.getAttribute(dir, "unix:gid");
  }

  Path secret(String value) throws Exception {
    Path parent =
        Files.createDirectory(
            dir.resolve("installation"),
            PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")));
    return Files.writeString(parent.resolve("edge.secret"), value, StandardOpenOption.CREATE_NEW);
  }

  @Test
  void realIssuerRefreshIsAtomicAndFailureNeverExtendsExpiryAndRestartRecovers() throws Exception {
    var time = new Time();
    var key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    String credential = Secrets.random();
    var issuer = serviceIssuer(time, credential, key);
    var verifier = verifier(time, key);
    Path token = dir.resolve("export/service.jwt");
    if (!posix()) {
      try (var publisher = publisher(issuer, dir.resolve("edge.secret"), token, 65532)) {
        assertThrows(IllegalStateException.class, publisher::start);
        assertFalse(publisher.isRunning());
        assertFalse(Files.exists(token));
      }
      return; // This platform's supported behavior is rejection; Linux executes the full chain.
    }
    Path secret = secret(credential);
    Files.setPosixFilePermissions(secret, PosixFilePermissions.fromString("rw-------"));
    try (var publisher = publisher(issuer, secret, token, gid())) {
      assertFalse(Files.exists(token));
      publisher.start();
      awaitToken(token);
      assertTrue(publisher.isRunning());
      String first = Files.readString(token);
      Object inode = Files.getAttribute(token, "unix:ino");
      assertEquals("svc:" + service(), verifier.verify(first).subject());
      assertEquals(
          300,
          SignedJWT.parse(first).getJWTClaimsSet().getExpirationTime().toInstant().getEpochSecond()
              - time.instant().getEpochSecond());
      time.now = time.now.plusSeconds(240);
      assertTrue(publisher.refresh());
      String second = Files.readString(token);
      assertNotEquals(first, second);
      assertNotEquals(inode, Files.getAttribute(token, "unix:ino"));
      assertEquals(
          PosixFilePermissions.fromString("rw-r-----"), Files.getPosixFilePermissions(token));
      assertEquals(gid(), Files.getAttribute(token, "unix:gid"));
      time.now = time.now.plusSeconds(61);
      assertEquals(
          "common.unauthenticated",
          assertThrows(AurigaException.class, () -> verifier.verify(first)).errorCode().code());
      assertEquals("svc:" + service(), verifier.verify(second).subject());
      Files.writeString(secret, Secrets.random());
      assertFalse(publisher.refresh());
      assertEquals(second, Files.readString(token));
      time.now = time.now.plusSeconds(240);
      assertEquals(
          "common.unauthenticated",
          assertThrows(AurigaException.class, () -> verifier.verify(Files.readString(token)))
              .errorCode()
              .code());
      Files.writeString(secret, credential);
      assertTrue(publisher.refresh());
      assertEquals("svc:" + service(), verifier.verify(Files.readString(token)).subject());
      var workerField = ServiceTokenFilePublisher.class.getDeclaredField("scheduler");
      workerField.setAccessible(true);
      var worker = (java.util.concurrent.ExecutorService) workerField.get(publisher);
      publisher.stop();
      assertTrue(worker.isShutdown());
      assertTrue(worker.awaitTermination(5, java.util.concurrent.TimeUnit.SECONDS));
      assertFalse(publisher.isRunning());
      assertFalse(Files.exists(token));
      assertFalse(publisher.refresh());
      publisher.start();
      awaitToken(token);
      assertEquals("svc:" + service(), verifier.verify(Files.readString(token)).subject());
      assertTrue(publisher.isRunning());
    }
    assertFalse(Files.exists(token));
  }

  @Test
  void unsafeFilesDirectoriesLinksAndWriteFailureAreRejectedWithoutTouchingOldToken()
      throws Exception {
    if (!posix()) {
      var publisher =
          publisher(
              (service, secret) -> "never",
              dir.resolve("secret"),
              dir.resolve("export/token"),
              65532);
      assertThrows(IllegalStateException.class, publisher::start);
      return;
    }
    String credential = Secrets.random();
    Path secret = secret(credential);
    Files.setPosixFilePermissions(secret, PosixFilePermissions.fromString("rw-------"));
    Path token = dir.resolve("export/token");
    var time = new Time();
    var key = new ECKeyGenerator(Curve.P_256).keyID("test").generate();
    try (var publisher = publisher(serviceIssuer(time, credential, key), secret, token, gid())) {
      publisher.start();
      awaitToken(token);
      String original = Files.readString(token);
      for (String unsafe : List.of("", " ", "x".repeat(129))) {
        Files.writeString(secret, unsafe);
        assertFalse(publisher.refresh());
        assertEquals(original, Files.readString(token));
      }
      Files.writeString(secret, credential);
      Files.setPosixFilePermissions(secret, PosixFilePermissions.fromString("rw-r--r--"));
      assertFalse(publisher.refresh());
      Files.setPosixFilePermissions(secret, PosixFilePermissions.fromString("rw-------"));
      Path hard = Files.createLink(dir.resolve("leaked-secret"), secret);
      assertFalse(publisher.refresh());
      Files.delete(hard);
      Files.move(secret, secret.resolveSibling("saved"));
      Files.createSymbolicLink(secret, Path.of("saved"));
      assertFalse(publisher.refresh());
      Files.delete(secret);
      Files.move(secret.resolveSibling("saved"), secret);
      Files.setPosixFilePermissions(
          token.getParent(), PosixFilePermissions.fromString("rwxrwx---"));
      assertFalse(publisher.refresh());
      Files.setPosixFilePermissions(
          token.getParent(), PosixFilePermissions.fromString("rwxr-x---"));
      Files.delete(token);
      Files.createDirectory(token);
      assertFalse(publisher.refresh());
      Files.delete(token);
      Files.createSymbolicLink(token, secret);
      assertFalse(publisher.refresh());
      assertEquals(credential, Files.readString(secret));
      Files.delete(token);
      assertTrue(publisher.refresh());
      Files.writeString(token, "x".repeat(16 * 1024 + 1));
      assertFalse(publisher.refresh());
      Files.delete(token);
      assertTrue(publisher.refresh());
      Files.setPosixFilePermissions(token, PosixFilePermissions.fromString("rw-r--r--"));
      assertFalse(publisher.refresh());
      Files.setPosixFilePermissions(token, PosixFilePermissions.fromString("rw-r-----"));
      assertTrue(publisher.refresh());
      try (var files = Files.list(token.getParent())) {
        assertEquals(1, files.count());
      }
    }
  }

  @Test
  void signingFailureAndUnreadableDestinationLeaveThePreviousExpiryUnchanged() throws Exception {
    if (!posix()) {
      assertThrows(
          IllegalStateException.class,
          () ->
              publisher(
                      (s, c) -> "never", dir.resolve("secret"), dir.resolve("export/token"), 65532)
                  .start());
      return;
    }
    var time = new Time();
    String credential = Secrets.random();
    var key = new ECKeyGenerator(Curve.P_256).keyID("failure-test").generate();
    var issuer = serviceIssuer(time, credential, key);
    var unavailable = new java.util.concurrent.atomic.AtomicBoolean(true);
    var writeFailure = new java.util.concurrent.atomic.AtomicBoolean();
    var firstAttempt = new java.util.concurrent.CountDownLatch(1);
    Path secret = secret(credential);
    Files.setPosixFilePermissions(secret, PosixFilePermissions.fromString("rw-------"));
    Path token = dir.resolve("export/token");
    try (var publisher =
        publisher(
            (service, value) -> {
              if (unavailable.get()) {
                firstAttempt.countDown();
                throw new IllegalStateException("signing authority unavailable");
              }
              if (writeFailure.get()) {
                try {
                  Files.setPosixFilePermissions(
                      dir.resolve("export"), PosixFilePermissions.fromString("r-xr-x---"));
                } catch (java.io.IOException e) {
                  throw new java.io.UncheckedIOException(e);
                }
              }
              return issuer.issue(service, value);
            },
            secret,
            token,
            gid())) {
      publisher.start();
      assertTrue(firstAttempt.await(5, java.util.concurrent.TimeUnit.SECONDS));
      assertFalse(Files.exists(token));
      unavailable.set(false);
      assertTrue(publisher.refresh());
      String previous = Files.readString(token);
      unavailable.set(true);
      time.now = time.now.plusSeconds(301);
      assertFalse(publisher.refresh());
      assertEquals(previous, Files.readString(token));
      assertEquals(
          "common.unauthenticated",
          assertThrows(AurigaException.class, () -> verifier(time, key).verify(previous))
              .errorCode()
              .code());
      unavailable.set(false);
      Files.createLink(dir.resolve("unsafe-output-link"), token);
      assertFalse(publisher.refresh());
      assertEquals(previous, Files.readString(token));
      Files.delete(dir.resolve("unsafe-output-link"));
      writeFailure.set(true);
      assertFalse(publisher.refresh());
      assertEquals(previous, Files.readString(token));
      Files.setPosixFilePermissions(
          token.getParent(), PosixFilePermissions.fromString("rwxr-x---"));
      writeFailure.set(false);
      assertTrue(publisher.refresh());
      assertNotEquals(previous, Files.readString(token));
    }
  }

  @Test
  void blockedIssuerCannotBlockStopOrPublishAcrossStopAndNewGeneration() throws Exception {
    if (!posix()) {
      assertThrows(
          IllegalStateException.class,
          () ->
              publisher(
                      (s, c) -> "never", dir.resolve("secret"), dir.resolve("export/token"), 65532)
                  .start());
      return;
    }
    for (boolean restart : List.of(false, true)) {
      Path root = Files.createDirectory(dir.resolve("case-" + restart));
      Path installation =
          Files.createDirectory(
              root.resolve("installation"),
              PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")));
      String credential = Secrets.random();
      Path secret = Files.writeString(installation.resolve("edge.secret"), credential);
      Files.setPosixFilePermissions(secret, PosixFilePermissions.fromString("rw-------"));
      Path token = root.resolve("export/token");
      var time = new Time();
      var key = new ECKeyGenerator(Curve.P_256).keyID("blocked-test").generate();
      var authority = serviceIssuer(time, credential, key);
      var entered = new java.util.concurrent.CountDownLatch(1);
      var release = new java.util.concurrent.CountDownLatch(1);
      var attempts = new java.util.concurrent.atomic.AtomicInteger();
      try (var publisher =
          publisher(
              (service, value) -> {
                if (attempts.incrementAndGet() == 1) {
                  entered.countDown();
                  boolean released = false;
                  while (!released) {
                    try {
                      release.await();
                      released = true;
                    } catch (InterruptedException ignored) {
                      /* Deliberately non-cooperative test authority. */
                    }
                  }
                }
                return authority.issue(service, value);
              },
              secret,
              token,
              gid())) {
        publisher.start();
        assertTrue(entered.await(5, java.util.concurrent.TimeUnit.SECONDS));
        var field = ServiceTokenFilePublisher.class.getDeclaredField("scheduler");
        field.setAccessible(true);
        var oldWorker = (java.util.concurrent.ExecutorService) field.get(publisher);
        assertTimeoutPreemptively(
            Duration.ofSeconds(2),
            () -> {
              publisher.stop();
            });
        assertTrue(oldWorker.isShutdown());
        assertFalse(Files.exists(token));
        String current = null;
        if (restart) {
          publisher.start();
          awaitToken(token);
          current = Files.readString(token);
        }
        release.countDown();
        assertTrue(oldWorker.awaitTermination(5, java.util.concurrent.TimeUnit.SECONDS));
        if (restart) assertEquals(current, Files.readString(token));
        else assertFalse(Files.exists(token));
      } finally {
        release.countDown();
      }
    }
  }

  @Test
  void configuredInstallationOwnerIsRequiredAndWrongOwnerFailsBeforeIssuance() throws Exception {
    if (!posix()) {
      assertThrows(
          IllegalStateException.class,
          () ->
              publisher(
                      (s, c) -> "never", dir.resolve("secret"), dir.resolve("export/token"), 65532)
                  .start());
      return;
    }
    String value = Secrets.random();
    Path secret = secret(value);
    Files.setPosixFilePermissions(secret, PosixFilePermissions.fromString("rw-------"));
    int owner = (int) Files.getAttribute(secret, "unix:uid");
    try (var bad =
        publisher(
            (s, c) -> {
              fail("wrong owner must never reach issuer");
              return "never";
            },
            secret,
            dir.resolve("bad/token"),
            gid(),
            owner + 1)) {
      assertThrows(IllegalStateException.class, bad::start);
      assertFalse(bad.isRunning());
    }
    var time = new Time();
    var key = new ECKeyGenerator(Curve.P_256).keyID("owner-test").generate();
    try (var good =
        publisher(
            serviceIssuer(time, value, key), secret, dir.resolve("good/token"), gid(), owner)) {
      good.start();
      awaitToken(dir.resolve("good/token"));
      assertEquals(
          "svc:" + service(),
          verifier(time, key).verify(Files.readString(dir.resolve("good/token"))).subject());
    }
  }

  @Test
  void exportIsAbsentByDefaultAndCannotBeEnabledOutsideCombinedOrWithoutIam() {
    try (var context = new AnnotationConfigApplicationContext()) {
      context.register(configuration());
      context.refresh();
      assertTrue(context.getBeansOfType(ServiceTokenFilePublisher.class).isEmpty());
    }
    try (var context = new AnnotationConfigApplicationContext()) {
      context
          .getEnvironment()
          .getPropertySources()
          .addFirst(
              new MapPropertySource(
                  "installation",
                  Map.of(
                      "auriga.iam." + service() + "-token-export.enabled",
                      "true",
                      "auriga.services.iam.enabled",
                      "false")));
      context.register(configuration());
      context.refresh();
      assertTrue(context.getBeansOfType(ServiceTokenFilePublisher.class).isEmpty());
    }
    try (var context = new AnnotationConfigApplicationContext()) {
      context
          .getEnvironment()
          .getPropertySources()
          .addFirst(
              new MapPropertySource(
                  "installation",
                  Map.of("auriga.iam." + service() + "-token-export.enabled", "true")));
      context.registerBean(
          auriga.iam.api.InternalTokenIssuer.class, () -> (service, secret) -> "never");
      context.register(configuration());
      assertThrows(org.springframework.beans.BeansException.class, context::refresh);
    }
    assertThrows(
        IllegalStateException.class,
        () ->
            publisher((s, c) -> "never", Path.of("relative"), dir.resolve("export/token"), 65532));
  }
}
