package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.grpc.AurigaClientInterceptor;
import auriga.common.grpc.GrpcChannels;
import auriga.contracts.iam.v1.NetworkAuthorityActiveRequest;
import auriga.contracts.iam.v1.NetworkAuthorityActiveResponse;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import auriga.iam.api.NetworkAuthority.*;
import ch.qos.logback.classic.Level;
import ch.qos.logback.classic.Logger;
import ch.qos.logback.classic.PatternLayout;
import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.core.read.ListAppender;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.InetSocketAddress;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;

class NetworkAuthorityDiagnosticsTest {
  private static final String SECRET = "never-log-authority-secret-marker";
  private static final Set<String> BUCKETS =
      Set.of("NONE", "EXPIRED", "LE_25_MS", "LE_100_MS", "LE_250_MS", "LE_1000_MS", "GT_1000_MS");

  @Test
  void everyPhaseAndStatusStayFailClosedAndLogOnlyAllowedFields() throws Exception {
    try (var f = new Fixture()) {
      Map<String, Runnable> phases = new LinkedHashMap<>();
      phases.put("ACTIVE", () -> f.client.active("2", "20"));
      phases.put(
          "CHECK",
          () ->
              f.client.check(
                  new Check("2", "20", "20", "0", Sender.HUMAN, Operation.RECEIVE, 0, 1, 1)));
      phases.put("LIMITS", () -> f.client.limits());
      phases.put("RESOLVE", () -> f.client.resolve(f.connection(), "0"));
      phases.put("OTHER", () -> f.client.authorize(f.connection(), SECRET, SECRET));
      for (Status.Code code :
          List.of(
              Status.Code.UNAVAILABLE,
              Status.Code.DEADLINE_EXCEEDED,
              Status.Code.CANCELLED,
              Status.Code.UNAUTHENTICATED,
              Status.Code.PERMISSION_DENIED,
              Status.Code.UNIMPLEMENTED,
              Status.Code.RESOURCE_EXHAUSTED,
              Status.Code.INTERNAL,
              Status.Code.UNKNOWN)) {
        f.failure.set(
            Status.fromCode(code)
                .withDescription(SECRET)
                .withCause(new IllegalStateException(SECRET)));
        for (var phase : phases.entrySet()) {
          f.events.list.clear();
          Map<String, String> ambient = Map.of("tenant", f.tenant.toString(), "credential", SECRET);
          MDC.setContextMap(ambient);
          TenantContext.run(f.tenant, () -> failed(phase.getValue()));
          assertEquals(ambient, MDC.getCopyOfContextMap(), "logging must restore ambient context");
          var fields = event(f);
          assertEquals(phase.getKey(), fields.get("phase"));
          assertEquals(code.name(), fields.get("grpc_code"));
          assertEquals("NONE", fields.get("inbound_remaining"));
          assertEquals(false, fields.get("cancelled"));
        }
      }
    }
  }

  @Test
  void upstreamDeadlineAndLateReplyCannotBecomeAnAllow() throws Exception {
    try (var f = new Fixture()) {
      f.warmAndHold();
      var scheduler = Executors.newSingleThreadScheduledExecutor();
      var inbound = Context.current().withDeadlineAfter(200, TimeUnit.MILLISECONDS, scheduler);
      try {
        inbound.run(
            () -> TenantContext.run(f.tenant, () -> failed(() -> f.client.active("2", "20"))));
        assertEquals(0, f.seen.getCount(), "the real fake RPC must be in flight");
        var fields = event(f);
        assertEquals("DEADLINE_EXCEEDED", fields.get("grpc_code"));
        assertTrue(
            Set.of("LE_25_MS", "LE_100_MS", "LE_250_MS").contains(fields.get("inbound_remaining")));
        assertEquals(true, fields.get("cancelled"));
        f.lateReply();
        assertEquals(1, f.events.list.size());
      } finally {
        inbound.cancel(null);
        scheduler.shutdownNow();
      }
    }
  }

  @Test
  void upstreamCancellationAndLateReplyCannotBecomeAnAllow() throws Exception {
    try (var f = new Fixture();
        var executor = Executors.newSingleThreadExecutor()) {
      f.warmAndHold();
      var scheduler = Executors.newSingleThreadScheduledExecutor();
      var inbound = Context.current().withDeadlineAfter(5, TimeUnit.SECONDS, scheduler);
      try {
        Future<?> call =
            executor.submit(
                () ->
                    inbound.call(
                        () -> TenantContext.call(f.tenant, () -> f.client.active("2", "20"))));
        assertTrue(f.seen.await(3, TimeUnit.SECONDS));
        inbound.cancel(new IllegalStateException(SECRET));
        var failure = assertThrows(ExecutionException.class, () -> call.get(3, TimeUnit.SECONDS));
        assertPublicFailure(assertInstanceOf(AurigaException.class, failure.getCause()));
        var fields = event(f);
        assertEquals("CANCELLED", fields.get("grpc_code"));
        assertEquals("GT_1000_MS", fields.get("inbound_remaining"));
        assertEquals(true, fields.get("cancelled"));
        f.lateReply();
        assertTrue(call.isDone());
        assertEquals(1, f.events.list.size());
      } finally {
        inbound.cancel(null);
        scheduler.shutdownNow();
      }
    }
  }

  @Test
  void plainConsoleKeepsClassificationWithoutChangingLogConfiguration() throws Exception {
    try (var f = new Fixture()) {
      TenantContext.run(f.tenant, () -> failed(() -> f.client.active("2", "20")));
      var layout = new PatternLayout();
      layout.setContext(f.logger.getLoggerContext());
      // Boot's default console pattern renders %m, without %kvp.
      layout.setPattern("%m%n");
      layout.start();
      try {
        String line = layout.doLayout(f.events.list.getFirst()).trim();
        assertTrue(
            line.matches(
                "collab_iam_rpc_failure phase=ACTIVE grpc_code=UNAVAILABLE elapsed_ms=[0-9]+ inbound_remaining=NONE cancelled=false"));
        assertFalse(line.contains(SECRET));
      } finally {
        layout.stop();
      }
    }
  }

  @Test
  void successfulAuthorityCallDoesNotEmitFailureClassification() throws Exception {
    try (var f = new Fixture()) {
      f.failure.set(null);
      TenantContext.run(f.tenant, () -> assertTrue(f.client.active("2", "20")));
      assertTrue(f.events.list.isEmpty());
    }
  }

  private static void failed(Runnable call) {
    assertPublicFailure(assertThrows(AurigaException.class, call::run));
  }

  private static void assertPublicFailure(AurigaException failure) {
    assertEquals("common.unavailable", failure.errorCode().code());
    assertNull(failure.getCause());
  }

  private static Map<String, Object> event(Fixture f) {
    assertEquals(1, f.events.list.size());
    var event = f.events.list.getFirst();
    assertEquals(
        "collab_iam_rpc_failure phase={} grpc_code={} elapsed_ms={} inbound_remaining={} cancelled={}",
        event.getMessage());
    assertNull(event.getThrowableProxy());
    assertTrue(
        event.getMDCPropertyMap().isEmpty(),
        "ambient identity and credentials must not enter event MDC");
    var fields = new HashMap<String, Object>();
    for (var pair : event.getKeyValuePairs()) assertNull(fields.put(pair.key, pair.value));
    assertEquals(
        Set.of("phase", "grpc_code", "elapsed_ms", "inbound_remaining", "cancelled"),
        fields.keySet());
    assertTrue(
        Set.of("RESOLVE", "ACTIVE", "CHECK", "LIMITS", "OTHER").contains(fields.get("phase")));
    assertTrue(BUCKETS.contains(fields.get("inbound_remaining")));
    assertTrue(fields.get("cancelled") instanceof Boolean);
    assertTrue(fields.get("elapsed_ms") instanceof Long);
    assertTrue((Long) fields.get("elapsed_ms") >= 0);
    assertArrayEquals(
        new Object[] {
          fields.get("phase"),
          fields.get("grpc_code"),
          fields.get("elapsed_ms"),
          fields.get("inbound_remaining"),
          fields.get("cancelled")
        },
        event.getArgumentArray());
    assertEquals(
        "collab_iam_rpc_failure phase="
            + fields.get("phase")
            + " grpc_code="
            + fields.get("grpc_code")
            + " elapsed_ms="
            + fields.get("elapsed_ms")
            + " inbound_remaining="
            + fields.get("inbound_remaining")
            + " cancelled="
            + fields.get("cancelled"),
        event.getFormattedMessage());
    assertFalse(fields.toString().contains(SECRET));
    assertFalse(fields.toString().contains(f.tenant.toString()));
    return fields;
  }

  private static final class Fixture implements AutoCloseable {
    final UUID tenant = UUID.randomUUID();
    final AtomicReference<Status> failure = new AtomicReference<>(Status.UNAVAILABLE);
    final AtomicReference<StreamObserver<NetworkAuthorityActiveResponse>> held =
        new AtomicReference<>();
    final CountDownLatch seen = new CountDownLatch(1);
    volatile boolean hold;
    final Logger logger = (Logger) LoggerFactory.getLogger(NetworkAuthorityClient.class);
    final Level oldLevel = logger.getLevel();
    final boolean oldAdditive = logger.isAdditive();
    final Map<String, String> oldMdc = MDC.getCopyOfContextMap();
    final ListAppender<ILoggingEvent> events =
        new ListAppender<>() {
          @Override
          protected void append(ILoggingEvent event) {
            event.prepareForDeferredProcessing();
            super.append(event);
          }
        };
    final Server server;
    final ManagedChannel channel;
    final NetworkAuthorityClient client;

    Fixture() throws Exception {
      events.setContext(logger.getLoggerContext());
      events.start();
      logger.addAppender(events);
      logger.setLevel(Level.WARN);
      logger.setAdditive(false);
      server =
          NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
              .intercept(
                  new ServerInterceptor() {
                    @Override
                    public <Q, R> ServerCall.Listener<Q> interceptCall(
                        ServerCall<Q, R> call, Metadata headers, ServerCallHandler<Q, R> next) {
                      Status status = failure.get();
                      if (status == null) return next.startCall(call, headers);
                      var trailers = new Metadata();
                      trailers.put(
                          Metadata.Key.of("test-secret", Metadata.ASCII_STRING_MARSHALLER), SECRET);
                      call.close(status, trailers);
                      return new ServerCall.Listener<>() {};
                    }
                  })
              .addService(
                  new NetworkAuthorityGrpcTest.Authority() {
                    @Override
                    public void networkAuthorityActive(
                        NetworkAuthorityActiveRequest request,
                        StreamObserver<NetworkAuthorityActiveResponse> observer) {
                      if (!hold) {
                        super.networkAuthorityActive(request, observer);
                        return;
                      }
                      held.set(observer);
                      seen.countDown();
                    }
                  })
              .build()
              .start();
      channel =
          GrpcChannels.open(
              "127.0.0.1:" + server.getPort(),
              new AurigaClientInterceptor(() -> SECRET, Duration.ofSeconds(5)));
      client = new NetworkAuthorityClient(channel, Duration.ofSeconds(5));
    }

    NetworkAuthorityConnection connection() {
      return NetworkAuthorityGrpcTest.connection(tenant).toBuilder()
          .setIngress(SECRET)
          .setClientAccount(SECRET)
          .build();
    }

    void warmAndHold() {
      failure.set(null);
      TenantContext.run(tenant, () -> assertTrue(client.active("2", "20")));
      hold = true;
    }

    void lateReply() {
      try {
        held.get().onNext(NetworkAuthorityActiveResponse.newBuilder().setActive(true).build());
        held.get().onCompleted();
      } catch (IllegalStateException alreadyClosed) {
        // A peer may reject a late write after cancellation; it still cannot revive the call.
      }
    }

    @Override
    public void close() throws Exception {
      channel.shutdownNow().awaitTermination(3, TimeUnit.SECONDS);
      server.shutdownNow().awaitTermination(3, TimeUnit.SECONDS);
      logger.detachAppender(events);
      events.stop();
      logger.setLevel(oldLevel);
      logger.setAdditive(oldAdditive);
      if (oldMdc == null) MDC.clear();
      else MDC.setContextMap(oldMdc);
    }
  }
}
