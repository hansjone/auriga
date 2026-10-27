package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.grpc.GrpcAuthorizationCheck;
import auriga.contracts.iam.v1.*;
import io.grpc.*;
import io.grpc.stub.StreamObserver;
import java.sql.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;

/** Transport/JDBC-hook guards only. Response fixtures here are NOT source-chain acceptance. */
class NetworkSourceJavaFaultsTest {
  static final RegisterAgentSourceRequest REQUEST =
      RegisterAgentSourceRequest.newBuilder().setOperationId("guard-op").build();

  static class Observer<T> implements StreamObserver<T> {
    T value;
    Throwable error;
    boolean completed;

    public void onNext(T next) {
      value = next;
    }

    public void onError(Throwable failure) {
      error = failure;
    }

    public void onCompleted() {
      completed = true;
    }
  }

  static void arm(
      NetworkSourceJavaFaults faults, String name, String method, String phase, String mode) {
    faults.control(
        Json.object(
            "id", name, "op", "arm", "name", name, "method", method, "phase", phase, "mode", mode));
  }

  @Test
  void defaultOffAndInvalidRulesCannotAlterAuthentication() {
    var disabled = new NetworkSourceJavaFaults(false, v -> {});
    var original = mock(DataSource.class);
    assertSame(original, disabled.dataSource(original));
    assertThrows(
        IllegalStateException.class,
        () -> arm(disabled, "x", "ReadAgentSourceReceipt", "before", "hold"));
    var enabled = new NetworkSourceJavaFaults(true, v -> {});
    assertThrows(
        IllegalArgumentException.class,
        () -> arm(enabled, "x", "RedeemNetworkTicket", "before", "unavailable"));
    assertThrows(
        IllegalArgumentException.class,
        () -> arm(enabled, "x", "RegisterAgentSource", "before", "allow"));
    var authority = mock(IamNetworkSourceAuthority.class);
    GrpcAuthorizationCheck.verify(List.of(enabled.service(authority)), Set.of());
  }

  @Test
  void ruleLocalScopesAvoidFaultingAnotherSessionsRenewal() {
    var faults = new NetworkSourceJavaFaults(true, v -> {});
    faults.control(
        Json.object(
            "id",
            "local",
            "op",
            "arm",
            "name",
            "scoped",
            "method",
            "RegisterAgentSource",
            "phase",
            "before",
            "mode",
            "unavailable",
            "local",
            "3"));
    var unrelated =
        REQUEST.toBuilder()
            .setRegistration(NetworkAgentSourceRegistration.newBuilder().setLocal("2"))
            .build();
    var allowed = new Observer<RegisterAgentSourceResponse>();
    faults.invoke(
        "RegisterAgentSource",
        unrelated,
        allowed,
        o -> {
          o.onNext(RegisterAgentSourceResponse.getDefaultInstance());
          o.onCompleted();
        });
    assertTrue(allowed.completed);
    assertNull(allowed.error);
    var targeted = new Observer<RegisterAgentSourceResponse>();
    faults.invoke(
        "RegisterAgentSource",
        unrelated.toBuilder()
            .setRegistration(NetworkAgentSourceRegistration.newBuilder().setLocal("3"))
            .build(),
        targeted,
        o -> fail("targeted call escaped fault"));
    assertEquals(Status.Code.UNAVAILABLE, Status.fromThrowable(targeted.error).getCode());
  }

  @Test
  void unavailableBeforeDelegateCannotReturnBusinessFixture() {
    var faults = new NetworkSourceJavaFaults(true, v -> {});
    arm(faults, "before", "RegisterAgentSource", "before", "unavailable");
    var observer = new Observer<RegisterAgentSourceResponse>();
    var calls = new AtomicInteger();
    faults.invoke(
        "RegisterAgentSource",
        REQUEST,
        observer,
        o -> {
          calls.incrementAndGet();
          o.onNext(RegisterAgentSourceResponse.getDefaultInstance());
          o.onCompleted();
        });
    assertEquals(0, calls.get());
    assertNull(observer.value);
    assertEquals(Status.Code.UNAVAILABLE, Status.fromThrowable(observer.error).getCode());
  }

  @Test
  void afterDelegateLossRetainsExactProducedBytesInTrace() {
    var faults = new NetworkSourceJavaFaults(true, v -> {});
    arm(faults, "lost", "RegisterAgentSource", "after", "unavailable");
    var observer = new Observer<RegisterAgentSourceResponse>();
    var fixture =
        RegisterAgentSourceResponse.newBuilder()
            .setOperationId("fixture-not-a-real-receipt")
            .build();
    var calls = new AtomicInteger();
    faults.invoke(
        "RegisterAgentSource",
        REQUEST,
        observer,
        o -> {
          calls.incrementAndGet();
          o.onNext(fixture);
          o.onCompleted();
        });
    assertEquals(1, calls.get());
    assertNull(observer.value);
    assertFalse(observer.completed);
    assertEquals(Status.Code.UNAVAILABLE, Status.fromThrowable(observer.error).getCode());
    assertTrue(
        faults.trace().stream()
            .anyMatch(
                v ->
                    v.path("phase").asString().equals("produced")
                        && v.path("bytes")
                            .asString()
                            .equals(Base64.getEncoder().encodeToString(fixture.toByteArray()))));
  }

  @Test
  void heldCallObservesActualContextCancellationAndCloseReleasesGates() throws Exception {
    var entered = new CountDownLatch(1);
    var faults =
        new NetworkSourceJavaFaults(
            true,
            v -> {
              if (v.path("stage").asString().equals("fault-hit")) entered.countDown();
            });
    arm(faults, "hold", "ReadAgentSourceReceipt", "before", "hold");
    var observer = new Observer<RegisterAgentSourceResponse>();
    var cancelled = Context.current().withCancellation();
    var thread =
        Thread.ofVirtual()
            .start(
                () ->
                    cancelled.run(
                        () ->
                            faults.invoke(
                                "ReadAgentSourceReceipt",
                                ReadAgentSourceReceiptRequest.newBuilder()
                                    .setOriginalRequest(REQUEST)
                                    .build(),
                                observer,
                                o -> fail("cancelled request reached business"))));
    assertTrue(entered.await(5, TimeUnit.SECONDS));
    cancelled.cancel(new CancellationException());
    thread.join(5000);
    assertFalse(thread.isAlive());
    assertEquals(Status.Code.CANCELLED, Status.fromThrowable(observer.error).getCode());
    faults.close();
  }

  @Test
  void jdbcAckLossHappensOnlyAfterSuccessfulSourceOperationCommit() throws Exception {
    var faults = new NetworkSourceJavaFaults(true, v -> {});
    arm(faults, "commit", "RegisterAgentSource", "commit", "ack-lost");
    var source = mock(DataSource.class);
    var connection = mock(Connection.class);
    var statement = mock(PreparedStatement.class);
    when(source.getConnection()).thenReturn(connection);
    when(connection.prepareStatement(anyString())).thenReturn(statement);
    when(statement.executeUpdate()).thenReturn(1);
    var wrapped = faults.dataSource(source);
    var observer = new Observer<RegisterAgentSourceResponse>();
    faults.invoke(
        "RegisterAgentSource",
        REQUEST,
        observer,
        o -> {
          try (var c = wrapped.getConnection()) {
            assertEquals(c, c);
            try (var select = c.prepareStatement("SELECT 1")) {
              select.executeUpdate();
            }
            c.commit();
            try (var insert =
                c.prepareStatement("INSERT INTO iam.network_source_operation VALUES (?)")) {
              insert.executeUpdate();
            }
            var failure = assertThrows(SQLException.class, c::commit);
            assertEquals("08006", failure.getSQLState());
            o.onError(Status.UNAVAILABLE.asRuntimeException());
          } catch (SQLException e) {
            throw new AssertionError(e);
          }
        });
    verify(connection, times(2)).commit();
    assertNull(observer.value);
    assertTrue(
        faults.trace().stream()
            .anyMatch(v -> v.path("phase").asString().equals("commit-ack-lost")));
  }
}
