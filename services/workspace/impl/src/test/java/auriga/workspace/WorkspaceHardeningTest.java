package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.*;
import auriga.common.grpc.GrpcErrors;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.node.v1.*;
import auriga.workspace.app.NodeEnrollment;
import auriga.workspace.domain.*;
import auriga.workspace.grpc.*;
import auriga.workspace.persistence.WorkspaceStore;
import auriga.workspace.web.SseLimits;
import io.grpc.Status;
import io.grpc.stub.ServerCallStreamObserver;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import java.io.IOException;
import java.nio.file.attribute.*;
import java.sql.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.atomic.*;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.datasource.AbstractDataSource;

class WorkspaceHardeningTest {
  static final class TestClock extends Clock {
    Instant now = Instant.now();

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

  static final class Transport {
    @SuppressWarnings("unchecked")
    final ServerCallStreamObserver<SessionResponse> observer = mock(ServerCallStreamObserver.class);

    final AtomicBoolean ready = new AtomicBoolean();
    final AtomicReference<Runnable> callback = new AtomicReference<>();
    final List<SessionResponse> received = new ArrayList<>();
    final AtomicReference<Throwable> failure = new AtomicReference<>();
    final NodeOutput output;

    Transport(TestClock clock) {
      when(observer.isReady()).thenAnswer(i -> ready.get());
      doAnswer(
              i -> {
                callback.set(i.getArgument(0));
                return null;
              })
          .when(observer)
          .setOnReadyHandler(any());
      doAnswer(
              i -> {
                received.add(i.getArgument(0));
                return null;
              })
          .when(observer)
          .onNext(any());
      var ref = new AtomicReference<NodeOutput>();
      output =
          new NodeOutput(
              observer,
              e -> {
                failure.set(e);
                ref.get().terminate(NodeGrpcServer.status(e));
              },
              () -> {},
              clock);
      ref.set(output);
    }
  }

  static SessionResponse frame(String id) {
    return SessionResponse.newBuilder().setWelcome(Welcome.newBuilder().setNodeId(id)).build();
  }

  @Test
  void readyCallbackDrainsInOrderWithoutBlockingSender() {
    var transport = new Transport(new TestClock());
    transport.output.send(frame("first"));
    transport.output.send(frame("second"));
    assertTrue(transport.received.isEmpty());
    transport.ready.set(true);
    transport.callback.get().run();
    assertEquals(List.of(frame("first"), frame("second")), transport.received);
    transport.output.send(frame("third"));
    assertEquals(3, transport.received.size());
  }

  @Test
  void nonReadingPeerHasBoundedFramesAndIsDisconnected() {
    var transport = new Transport(new TestClock());
    for (int i = 0; i < NodeOutput.MAX_FRAMES; i++)
      assertTrue(transport.output.send(frame("queued")));
    assertFalse(transport.output.send(frame("overflow")));
    assertEquals(
        "common.rate_limited", ((AurigaException) transport.failure.get()).errorCode().code());
    assertTrue(transport.received.isEmpty());
    verify(transport.observer)
        .onError(argThat(e -> Status.fromThrowable(e).getCode() == Status.Code.RESOURCE_EXHAUSTED));
  }

  @Test
  void nonReadingPeerTimesOutEvenBelowQueueLimit() {
    var clock = new TestClock();
    var transport = new Transport(clock);
    transport.output.send(frame("queued"));
    clock.now = clock.now.plusSeconds(30);
    transport.output.checkStalled();
    assertEquals(
        "common.unavailable", ((AurigaException) transport.failure.get()).errorCode().code());
  }

  @Test
  void oversizedFrameIsRejectedBeforeBuffering() {
    var transport = new Transport(new TestClock());
    var spec = InstanceSpec.newBuilder().setInstanceId("x".repeat(NodeOutput.MAX_BYTES + 1));
    assertFalse(
        transport.output.send(
            SessionResponse.newBuilder().setApply(ApplySpec.newBuilder().setSpec(spec)).build()));
    assertNotNull(transport.failure.get());
  }

  @Test
  void joinLimitsArePerSourceAndHaveGlobalBudget() {
    var clock = new TestClock();
    var registry = new SimpleMeterRegistry();
    var limiter = new JoinRateLimiter(clock, registry);
    for (int i = 0; i < 10; i++) limiter.admit("192.0.2.10");
    assertEquals(
        "common.rate_limited",
        assertThrows(AurigaException.class, () -> limiter.admit("192.0.2.10")).errorCode().code());
    limiter.admit("192.0.2.11");
    clock.now = clock.now.plusSeconds(10);
    limiter.admit("192.0.2.10");
    var global = new JoinRateLimiter(clock, registry);
    for (int i = 0; i < 100; i++) global.admit("source-" + i);
    assertEquals(
        "common.rate_limited",
        assertThrows(AurigaException.class, () -> global.admit("different-source"))
            .errorCode()
            .code());
    assertEquals(2, registry.counter("auriga.workspace.join.rejected").count());
  }

  @Test
  void joinFailuresTriggerCooldownAndMetrics() {
    var clock = new TestClock();
    var registry = new SimpleMeterRegistry();
    var limiter = new JoinRateLimiter(clock, registry);
    for (int i = 0; i < 5; i++) {
      limiter.admit("192.0.2.10");
      limiter.failed("192.0.2.10");
    }
    assertThrows(AurigaException.class, () -> limiter.admit("192.0.2.10"));
    limiter.admit("192.0.2.11");
    clock.now = clock.now.plusSeconds(31);
    limiter.admit("192.0.2.10");
    assertEquals(5, registry.counter("auriga.workspace.join.failures").count());
  }

  @Test
  void malformedJoinFailsBeforeAnyDatabaseOrCryptographicDependency() {
    var enrollment =
        new NodeEnrollment(null, null, Clock.systemUTC(), "https://example.com", List.of());
    for (String token :
        List.of("", "invalid", "x".repeat(100000), UUID.randomUUID() + "." + "!".repeat(43)))
      assertEquals(
          "common.unauthenticated",
          assertThrows(
                  AurigaException.class,
                  () -> enrollment.join(JoinRequest.newBuilder().setToken(token).build()))
              .errorCode()
              .code());
  }

  @Test
  void certificateSerialFitsTwentyDerOctets() throws Exception {
    var clock = new TestClock();
    var key = TestPki.key();
    var ca = new NodeCa(TestPki.ca(key, clock.instant()), key.getPrivate(), clock);
    byte[] csr = TestPki.csr(TestPki.key());
    for (int i = 0; i < 8; i++) {
      var serial = ca.issue(csr, UUID.randomUUID()).getSerialNumber();
      assertTrue(serial.signum() > 0);
      assertTrue(serial.toByteArray().length <= 20);
    }
  }

  @Test
  void posixAndWindowsAclPoliciesRejectSharedSecrets() throws Exception {
    assertFalse(
        SecretFilePermissions.exposedPosix(
            EnumSet.of(PosixFilePermission.OWNER_READ, PosixFilePermission.OWNER_WRITE)));
    assertTrue(SecretFilePermissions.exposedPosix(EnumSet.of(PosixFilePermission.GROUP_READ)));
    assertTrue(SecretFilePermissions.exposedPosix(EnumSet.of(PosixFilePermission.OTHERS_WRITE)));
    UserPrincipal owner = () -> "owner", other = () -> "other";
    var own =
        AclEntry.newBuilder()
            .setType(AclEntryType.ALLOW)
            .setPrincipal(owner)
            .setPermissions(AclEntryPermission.READ_DATA)
            .build();
    var shared =
        AclEntry.newBuilder()
            .setType(AclEntryType.ALLOW)
            .setPrincipal(other)
            .setPermissions(AclEntryPermission.READ_DATA)
            .build();
    SecretFilePermissions.checkAcl(List.of(own), Set.of(owner));
    assertThrows(
        IOException.class,
        () -> SecretFilePermissions.checkAcl(List.of(own, shared), Set.of(owner)));
  }

  @Test
  void databaseOutageIsUnavailableRatherThanAuthenticationFailure() throws Exception {
    var source =
        new AbstractDataSource() {
          public Connection getConnection() throws SQLException {
            throw new SQLException("offline", "08006");
          }

          public Connection getConnection(String user, String password) throws SQLException {
            return getConnection();
          }
        };
    var store = new WorkspaceStore(new JdbcTemplate(source), new TenantTransactionManager(source));
    var clock = new TestClock();
    var key = TestPki.key();
    var ca = new NodeCa(TestPki.ca(key, clock.instant()), key.getPrivate(), clock);
    var leaf = ca.issue(TestPki.csr(TestPki.key()), UUID.randomUUID());
    var enrollment = new NodeEnrollment(store, ca, clock, "https://example.com", List.of());
    assertEquals(
        "common.unavailable",
        assertThrows(AurigaException.class, () -> enrollment.identity(leaf)).errorCode().code());
    var status =
        NodeGrpcServer.status(
            new org.springframework.dao.DataAccessResourceFailureException("offline"));
    assertEquals(Status.Code.UNAVAILABLE, status.getStatus().getCode());
    assertEquals("common.unavailable", GrpcErrors.toAurigaException(status).errorCode().code());
  }

  @Test
  void registryRejectsNestedRegistrationBeforeBorrowingConnection() {
    var connections = new AtomicInteger();
    var dataSource =
        new AbstractDataSource() {
          public Connection getConnection() throws SQLException {
            connections.incrementAndGet();
            throw new SQLException("No connection expected");
          }

          public Connection getConnection(String user, String password) throws SQLException {
            return getConnection();
          }
        };
    var store =
        new WorkspaceStore(new JdbcTemplate(dataSource), new TenantTransactionManager(dataSource));
    boolean previous =
        org.springframework.transaction.support.TransactionSynchronizationManager
            .isActualTransactionActive();
    org.springframework.transaction.support.TransactionSynchronizationManager
        .setActualTransactionActive(true);
    try {
      assertThrows(
          IllegalStateException.class,
          () -> store.register(UUID.randomUUID(), "instance", UUID.randomUUID()));
      assertEquals(0, connections.get());
    } finally {
      org.springframework.transaction.support.TransactionSynchronizationManager
          .setActualTransactionActive(previous);
    }
  }

  @Test
  void sseLimitsArePerUserGlobalAndReleasedOnlyOnce() {
    var slots = new SseLimits();
    List<Runnable> releases = new ArrayList<>();
    for (int i = 0; i < SseLimits.PER_USER; i++) releases.add(slots.acquire("tenant/alice"));
    assertEquals(
        "common.rate_limited",
        assertThrows(AurigaException.class, () -> slots.acquire("tenant/alice"))
            .errorCode()
            .code());
    releases.getFirst().run();
    releases.getFirst().run();
    slots.acquire("tenant/alice");
    assertThrows(AurigaException.class, () -> slots.acquire("tenant/alice"));
    var global = new SseLimits();
    List<Runnable> all = new ArrayList<>();
    for (int i = 0; i < SseLimits.TOTAL; i++) all.add(global.acquire("tenant/user-" + i));
    assertThrows(AurigaException.class, () -> global.acquire("another-user"));
    all.getFirst().run();
    global.acquire("another-user");
  }

  @Test
  void deliveryRetriesHaveFiniteAttemptsAndCappedBackoff() {
    assertFalse(NodeDeliveryPolicy.exhausted(4));
    assertTrue(NodeDeliveryPolicy.exhausted(5));
    assertEquals(Duration.ofSeconds(5), NodeDeliveryPolicy.backoff(1));
    assertEquals(Duration.ofSeconds(80), NodeDeliveryPolicy.backoff(5));
    assertEquals(Duration.ofSeconds(300), NodeDeliveryPolicy.backoff(100));
  }
}
