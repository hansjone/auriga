package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.contracts.iam.v1.FinishSharedTaskRequest;
import auriga.iam.api.CredentialInstallationScope;
import auriga.iam.api.InternalTokenIssuer;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.SignedJWT;
import io.grpc.stub.StreamObserver;
import java.nio.file.*;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.mock.env.MockEnvironment;

class AgentTokenFilePublisherTest {
  @TempDir Path dir;

  boolean posix() {
    return dir.getFileSystem().supportedFileAttributeViews().contains("posix");
  }

  boolean rejectUnsupported() {
    if (posix()) return false;
    try (var publisher =
        new AgentTokenFilePublisher(
            (s, c) -> fail("unsupported platform must not issue"),
            dir.resolve("private/secret"),
            dir.resolve("output/token"),
            -1)) {
      assertThrows(IllegalStateException.class, publisher::start);
      assertFalse(publisher.isRunning());
      assertFalse(Files.exists(dir.resolve("output/token")));
    }
    return true;
  }

  Path secret(String value) throws Exception {
    Path parent =
        Files.createDirectory(
            dir.resolve("private"),
            PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")));
    Path file = Files.writeString(parent.resolve("agent.secret"), value);
    Files.setPosixFilePermissions(file, PosixFilePermissions.fromString("rw-------"));
    return file;
  }

  AgentTokenFilePublisher publisher(InternalTokenIssuer issuer, Path secret, Path token) {
    return new AgentTokenFilePublisher(issuer, secret, token, -1);
  }

  @Test
  void realIssuerKeepsSubjectAudienceAndInstallationTenantAuthority() throws Exception {
    var time = new EdgeTokenFilePublisherTest.Time();
    var key = new ECKeyGenerator(Curve.P_256).keyID("agent-installation").generate();
    String credential = Secrets.random();
    var issuer = EdgeTokenFilePublisherTest.issuer(time, credential, key, "agent");
    var verifier = EdgeTokenFilePublisherTest.verifier(time, key);
    String jwt = issuer.issue("agent", credential);
    var claims = SignedJWT.parse(jwt).getJWTClaimsSet();
    assertEquals(List.of("auriga.internal"), claims.getAudience());
    assertNull(claims.getClaim("perms"));
    var principal = verifier.verify(jwt);
    assertEquals("svc:agent", principal.subject());
    assertEquals(TenantContext.PLATFORM_TENANT, principal.tenantId());
    assertTrue(principal.isInternalService());
    for (String service : List.of("edge", "ai-gateway", "svc:agent", "workspace"))
      assertThrows(AurigaException.class, () -> issuer.issue(service, credential));
    assertThrows(AurigaException.class, () -> issuer.issue("agent", Secrets.random()));

    UUID installation = UUID.randomUUID();
    var tasks = mock(SharedTasks.class);
    var rpc =
        new IamGrpcSharedTasks(
            tasks, new CredentialInstallationScope(installation), mock(SharedOutbound.class));
    var request = FinishSharedTaskRequest.getDefaultInstance();
    var observer = mock(StreamObserver.class);
    RequestContext.start("agent-export-test", null)
        .withPrincipal(principal)
        .run(() -> TenantContext.run(installation, () -> rpc.finishSharedTask(request, observer)));
    verify(tasks).finish(request);
    for (UUID wrong : List.of(TenantContext.PLATFORM_TENANT, UUID.randomUUID()))
      RequestContext.start("agent-export-test", null)
          .withPrincipal(principal)
          .run(
              () ->
                  TenantContext.run(
                      wrong,
                      () ->
                          assertThrows(
                              AurigaException.class,
                              () -> rpc.finishSharedTask(request, observer))));
    verifyNoMoreInteractions(tasks);
  }

  @Test
  void privateAtomicRefreshFailureAndStopRetainOnlyTheOriginalExpiry() throws Exception {
    if (rejectUnsupported()) return;
    var time = new EdgeTokenFilePublisherTest.Time();
    var key = new ECKeyGenerator(Curve.P_256).keyID("agent-private").generate();
    String credential = Secrets.random();
    var authority = EdgeTokenFilePublisherTest.issuer(time, credential, key, "agent");
    var verifier = EdgeTokenFilePublisherTest.verifier(time, key);
    Path secret = secret(credential);
    Path token = dir.resolve("output/service.jwt");
    try (var publisher = publisher(authority, secret, token)) {
      publisher.start();
      EdgeTokenFilePublisherTest.awaitToken(token);
      String first = Files.readString(token);
      Object firstInode = Files.getAttribute(token, "unix:ino");
      assertEquals(
          PosixFilePermissions.fromString("rw-------"), Files.getPosixFilePermissions(token));
      assertEquals(
          PosixFilePermissions.fromString("rwx------"),
          Files.getPosixFilePermissions(token.getParent()));
      assertEquals("svc:agent", verifier.verify(first).subject());
      assertEquals(
          300,
          SignedJWT.parse(first).getJWTClaimsSet().getExpirationTime().toInstant().getEpochSecond()
              - time.instant().getEpochSecond());
      time.now = time.now.plusSeconds(240);
      assertTrue(publisher.refresh());
      String second = Files.readString(token);
      assertNotEquals(first, second);
      assertNotEquals(firstInode, Files.getAttribute(token, "unix:ino"));
      Files.writeString(secret, Secrets.random());
      assertFalse(publisher.refresh());
      assertEquals(second, Files.readString(token));
      publisher.stop();
      assertFalse(publisher.isRunning());
      assertFalse(publisher.refresh());
      assertEquals(second, Files.readString(token));
      time.now = time.now.plusSeconds(301);
      assertThrows(AurigaException.class, () -> verifier.verify(Files.readString(token)));
      assertEquals(second, Files.readString(token));
      Files.writeString(secret, credential);
      publisher.start();
      assertTrue(publisher.refresh());
      assertEquals("svc:agent", verifier.verify(Files.readString(token)).subject());
    }
    assertTrue(Files.isRegularFile(token), "Agent close must preserve the signed old file");
  }

  @Test
  void groupReadableFilesWritableParentsAndLinksCannotReachTheIssuer() throws Exception {
    if (rejectUnsupported()) return;
    var time = new EdgeTokenFilePublisherTest.Time();
    var key = new ECKeyGenerator(Curve.P_256).keyID("agent-file-safety").generate();
    String credential = Secrets.random();
    Path secret = secret(credential);
    Path token = dir.resolve("output/token");
    try (var publisher =
        publisher(
            EdgeTokenFilePublisherTest.issuer(time, credential, key, "agent"), secret, token)) {
      publisher.start();
      EdgeTokenFilePublisherTest.awaitToken(token);
      String previous = Files.readString(token);
      Files.setPosixFilePermissions(token, PosixFilePermissions.fromString("rw-r-----"));
      assertFalse(publisher.refresh(), "Node would reject a group-readable service token");
      assertEquals(previous, Files.readString(token));
      Files.setPosixFilePermissions(token, PosixFilePermissions.fromString("rw-------"));
      Files.setPosixFilePermissions(
          token.getParent(), PosixFilePermissions.fromString("rwxr-x---"));
      assertFalse(publisher.refresh());
      Files.setPosixFilePermissions(
          token.getParent(), PosixFilePermissions.fromString("rwx------"));
      Path hard = Files.createLink(dir.resolve("hard-token"), token);
      assertFalse(publisher.refresh());
      Files.delete(hard);
      Files.move(token, token.resolveSibling("saved"));
      Files.createSymbolicLink(token, Path.of("saved"));
      assertFalse(publisher.refresh());
      assertEquals(previous, Files.readString(token.resolveSibling("saved")));
      Files.delete(token);
      Files.move(token.resolveSibling("saved"), token);
      Path secretHard = Files.createLink(dir.resolve("hard-secret"), secret);
      assertFalse(publisher.refresh());
      Files.delete(secretHard);
      Files.setPosixFilePermissions(secret, PosixFilePermissions.fromString("rw-r-----"));
      assertFalse(publisher.refresh());
      Files.setPosixFilePermissions(secret, PosixFilePermissions.fromString("rw-------"));
      assertTrue(publisher.refresh());
      try (var files = Files.list(token.getParent())) {
        assertEquals(1, files.count(), "failed attempts must not publish a second token path");
      }
    }
    Path linkedParent = dir.resolve("linked-output");
    Files.createSymbolicLink(linkedParent, Path.of("output"));
    try (var publisher =
        publisher(
            (s, c) -> fail("linked output must not issue"),
            secret,
            linkedParent.resolve("token"))) {
      assertThrows(IllegalStateException.class, publisher::start);
    }
  }

  @Test
  void oldGenerationCannotPublishAfterStopOrRestartEvenWhenIssuerIgnoresCancellation()
      throws Exception {
    if (rejectUnsupported()) return;
    var time = new EdgeTokenFilePublisherTest.Time();
    var key = new ECKeyGenerator(Curve.P_256).keyID("agent-stop").generate();
    String credential = Secrets.random();
    Path secret = secret(credential);
    var authority = EdgeTokenFilePublisherTest.issuer(time, credential, key, "agent");
    Path token = dir.resolve("output/token");
    var block = new AtomicBoolean();
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    var issuer =
        (InternalTokenIssuer)
            (service, value) -> {
              String signed = authority.issue(service, value);
              if (block.compareAndSet(true, false)) {
                entered.countDown();
                boolean done = false;
                while (!done) {
                  try {
                    release.await();
                    done = true;
                  } catch (InterruptedException ignored) {
                    // Deliberately uncooperative issuer to verify the publication fence.
                  }
                }
              }
              return signed;
            };
    try (var publisher = publisher(issuer, secret, token);
        var worker = Executors.newSingleThreadExecutor()) {
      publisher.start();
      EdgeTokenFilePublisherTest.awaitToken(token);
      String original = Files.readString(token);
      block.set(true);
      Future<Boolean> late = worker.submit((Callable<Boolean>) publisher::refresh);
      try {
        assertTrue(entered.await(5, TimeUnit.SECONDS));
        assertTimeoutPreemptively(
            Duration.ofSeconds(2),
            () -> {
              publisher.stop();
            });
        assertEquals(original, Files.readString(token));
        time.now = time.now.plusSeconds(1);
        publisher.start();
        assertTrue(publisher.refresh());
        String fresh = Files.readString(token);
        assertNotEquals(original, fresh);
        release.countDown();
        assertFalse(late.get(5, TimeUnit.SECONDS));
        assertEquals(
            time.instant(),
            SignedJWT.parse(Files.readString(token)).getJWTClaimsSet().getIssueTime().toInstant());
      } finally {
        release.countDown();
      }
    }
  }

  @Test
  void explicitCombinedInstallationAndDisjointCredentialDirectoriesAreRequired() {
    try (var context = new AnnotationConfigApplicationContext()) {
      context.register(AgentTokenFileConfiguration.class);
      context.refresh();
      assertTrue(context.getBeansOfType(AgentTokenFilePublisher.class).isEmpty());
    }
    var issuer = mock(InternalTokenIssuer.class);
    var env = new MockEnvironment();
    env.setProperty("auriga.iam.agent-token-export.enabled", "true");
    env.setProperty(
        "auriga.iam.agent-token-export.installation-secret-file",
        dir.resolve("agent-secret/key").toString());
    env.setProperty(
        "auriga.iam.agent-token-export.token-file", dir.resolve("agent-output/token").toString());
    assertThrows(
        IllegalStateException.class,
        () -> new AgentTokenFileConfiguration().iamAgentTokenFilePublisher(issuer, env));
    env.setProperty("auriga.platform.combined", "true");
    assertInstanceOf(
        AgentTokenFilePublisher.class,
        new AgentTokenFileConfiguration().iamAgentTokenFilePublisher(issuer, env));
    for (String prefix : List.of("edge", "ai-gateway")) {
      env.setProperty("auriga.iam." + prefix + "-token-export.enabled", "true");
      env.setProperty(
          "auriga.iam." + prefix + "-token-export.installation-secret-file",
          dir.resolve(prefix + "-secret/key").toString());
      env.setProperty(
          "auriga.iam." + prefix + "-token-export.token-file",
          dir.resolve("agent-output/child/token").toString());
      if (prefix.equals("ai-gateway"))
        env.setProperty("auriga.iam." + prefix + "-token-export.reader-group", "65531");
      assertThrows(
          IllegalStateException.class,
          () -> new AgentTokenFileConfiguration().iamAgentTokenFilePublisher(issuer, env));
      env.setProperty("auriga.iam." + prefix + "-token-export.enabled", "false");
    }
    assertThrows(
        IllegalStateException.class,
        () ->
            new AgentTokenFilePublisher(
                issuer, dir.resolve("same/secret"), dir.resolve("same/token"), -1));
    assertThrows(
        IllegalStateException.class,
        () ->
            new AgentTokenFilePublisher(
                issuer, Path.of("relative"), dir.resolve("output/token"), -1));
    verifyNoInteractions(issuer);
  }
}
