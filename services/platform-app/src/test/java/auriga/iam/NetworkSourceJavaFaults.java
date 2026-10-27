package auriga.iam;

import auriga.common.security.AuthenticatedOnly;
import auriga.contracts.iam.v1.*;
import com.google.protobuf.Message;
import io.grpc.*;
import io.grpc.stub.StreamObserver;
import java.lang.reflect.*;
import java.sql.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicLong;
import java.util.function.Consumer;
import javax.sql.DataSource;
import org.springframework.jdbc.datasource.DelegatingDataSource;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Opt-in test transport/commit boundaries; never creates a business response or DB fact. */
final class NetworkSourceJavaFaults implements AutoCloseable {
  private static final Set<String> METHODS =
      Set.of(
          "RegisterAgentSource",
          "ReadAgentSourceReceipt",
          "FenceAgentSourceReceipt",
          "GetAgentSource",
          "RevokeAgentSource");
  private final boolean enabled;
  private final Consumer<ObjectNode> output;
  private final Map<String, Rule> rules = new LinkedHashMap<>();
  private final List<ObjectNode> events = new ArrayList<>();
  private final AtomicLong sequence = new AtomicLong();
  private final ThreadLocal<Invocation> current = new ThreadLocal<>();

  private record Invocation(String method, String op, String local) {}

  private static final class Rule {
    final String name, method, phase, mode, op, local;
    final CountDownLatch release = new CountDownLatch(1);
    int remaining = 1;

    Rule(JsonNode value) {
      name = value.path("name").asString();
      method = value.path("method").asString();
      phase = value.path("phase").asString();
      mode = value.path("mode").asString();
      op = value.path("operation").asString("");
      local = value.path("local").asString("");
      remaining = value.path("maxHits").asInt(1);
    }
  }

  NetworkSourceJavaFaults(boolean enabled, Consumer<ObjectNode> output) {
    this.enabled = enabled;
    this.output = output;
  }

  synchronized ObjectNode control(JsonNode value) {
    if (!enabled) throw new IllegalStateException("Source faults are disabled");
    String id = value.path("id").asString(), operation = value.path("op").asString();
    if (!id.matches("[A-Za-z0-9._:-]{1,128}"))
      throw new IllegalArgumentException("Invalid control id");
    var reply = Json.object("stage", "fault-control", "id", id);
    switch (operation) {
      case "arm" -> {
        var rule = new Rule(value);
        if (!rule.name.matches("[A-Za-z0-9._:-]{1,128}")
            || !METHODS.contains(rule.method)
            || rules.containsKey(rule.name)
            || rules.size() >= 64
            || rule.remaining < 1
            || rule.remaining > 16
            || !(rule.local.isEmpty() || rule.local.matches("[1-9][0-9]{0,19}"))
            || !(Set.of("before", "after").contains(rule.phase)
                    && Set.of("hold", "unavailable", "unimplemented").contains(rule.mode)
                || rule.phase.equals("commit")
                    && rule.mode.equals("ack-lost")
                    && rule.method.equals("RegisterAgentSource")))
          throw new IllegalArgumentException("Invalid bounded source fault");
        rules.put(rule.name, rule);
      }
      case "release" -> {
        var rule = rules.get(value.path("name").asString());
        if (rule == null) throw new IllegalArgumentException("Unknown source gate");
        rule.release.countDown();
      }
      case "clear" -> {
        for (var rule : rules.values()) rule.release.countDown();
        rules.clear();
      }
      case "trace" -> reply.set("events", Json.MAPPER.valueToTree(trace()));
      default -> throw new IllegalArgumentException("Unknown source control operation");
    }
    return reply;
  }

  synchronized List<ObjectNode> trace() {
    return List.copyOf(events);
  }

  private synchronized Rule claim(String method, String op, String phase) {
    if (!enabled) return null;
    for (var rule : rules.values())
      if (rule.remaining > 0
          && rule.method.equals(method)
          && rule.phase.equals(phase)
          && (rule.op.isEmpty() || rule.op.equals(op))) {
        if (!rule.local.isEmpty()
            && (current.get() == null || !rule.local.equals(current.get().local()))) continue;
        rule.remaining--;
        return rule;
      }
    return null;
  }

  private synchronized void record(
      String stage, String method, String op, String phase, String bytes, String code) {
    if (!enabled) return;
    if (events.size() >= 4096) throw new IllegalStateException("Source trace capacity exceeded");
    var event =
        Json.object(
            "stage",
            stage,
            "seq",
            sequence.incrementAndGet(),
            "method",
            method,
            "operation",
            op,
            "phase",
            phase,
            "bytes",
            bytes,
            "code",
            code);
    event.put("local", current.get() == null ? "" : current.get().local());
    events.add(event);
    output.accept(event);
  }

  private Status boundary(String method, String op, String phase) {
    var rule = claim(method, op, phase);
    if (rule == null) return null;
    record("fault-hit", method, op, phase, rule.name, rule.mode);
    if (rule.mode.equals("unavailable"))
      return Status.UNAVAILABLE.withDescription("common.unavailable");
    if (rule.mode.equals("unimplemented"))
      return Status.UNIMPLEMENTED.withDescription("test old source service");
    try {
      while (!rule.release.await(20, TimeUnit.MILLISECONDS)) {
        if (Context.current().isCancelled()) {
          var deadline = Context.current().getDeadline();
          String code =
              deadline != null && deadline.isExpired() ? "DEADLINE_EXCEEDED" : "CANCELLED";
          record("fault-cancelled", method, op, phase, rule.name, code);
          return code.equals("CANCELLED") ? Status.CANCELLED : Status.DEADLINE_EXCEEDED;
        }
      }
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      return Status.CANCELLED;
    }
    if (Context.current().isCancelled()) {
      record("fault-cancelled", method, op, phase, rule.name, "CANCELLED");
      return Status.CANCELLED;
    }
    record("fault-released", method, op, phase, rule.name, "OK");
    return null;
  }

  private static String operation(Message value) {
    return switch (value) {
      case RegisterAgentSourceRequest r -> r.getOperationId();
      case ReadAgentSourceReceiptRequest r -> r.getOriginalRequest().getOperationId();
      case FenceAgentSourceReceiptRequest r -> r.getOriginalRequest().getOperationId();
      case GetAgentSourceRequest r -> r.getSourceBinding();
      case RevokeAgentSourceRequest r -> r.getOperationId();
      default -> throw new IllegalArgumentException("Unsupported source request");
    };
  }

  private static String local(Message value) {
    return switch (value) {
      case RegisterAgentSourceRequest r -> r.getRegistration().getLocal();
      case ReadAgentSourceReceiptRequest r -> r.getOriginalRequest().getRegistration().getLocal();
      case FenceAgentSourceReceiptRequest r -> r.getOriginalRequest().getRegistration().getLocal();
      case GetAgentSourceRequest r -> r.getRegistration().getLocal();
      default -> "";
    };
  }

  <T extends Message> void invoke(
      String method,
      Message request,
      StreamObserver<T> observer,
      Consumer<StreamObserver<T>> delegate) {
    String op = operation(request);
    var previous = current.get();
    current.set(new Invocation(method, op, local(request)));
    try {
      record(
          "inner-rpc",
          method,
          op,
          "call",
          Base64.getEncoder().encodeToString(request.toByteArray()),
          "");
      var before = boundary(method, op, "before");
      if (before != null) {
        record("inner-rpc", method, op, "error", "", before.getCode().name());
        observer.onError(before.asRuntimeException());
        return;
      }
      record("inner-rpc", method, op, "delegate", "", "");
      delegate.accept(
          new StreamObserver<>() {
            boolean failed;

            public void onNext(T value) {
              record(
                  "inner-rpc",
                  method,
                  op,
                  "produced",
                  Base64.getEncoder().encodeToString(value.toByteArray()),
                  value instanceof GetAgentSourceResponse get
                      ? get.getSource().getState().name()
                      : "OK");
              var after = boundary(method, op, "after");
              if (after != null) {
                failed = true;
                record("inner-rpc", method, op, "error", "", after.getCode().name());
                observer.onError(after.asRuntimeException());
              } else {
                record("inner-rpc", method, op, "forwarded", "", "OK");
                observer.onNext(value);
              }
            }

            public void onError(Throwable error) {
              failed = true;
              record(
                  "inner-rpc",
                  method,
                  op,
                  "error",
                  "",
                  Status.fromThrowable(error).getCode().name());
              observer.onError(error);
            }

            public void onCompleted() {
              if (!failed) observer.onCompleted();
            }
          });
    } catch (RuntimeException failure) {
      record("inner-rpc", method, op, "thrown", "", failure.getClass().getSimpleName());
      throw failure;
    } finally {
      if (previous == null) current.remove();
      else current.set(previous);
    }
  }

  private void committed() throws SQLException {
    var invocation = current.get();
    if (invocation == null) return;
    var rule = claim(invocation.method(), invocation.op(), "commit");
    if (rule == null) return;
    record(
        "fault-hit", invocation.method(), invocation.op(), "commit-ack-lost", rule.name, "08006");
    // The delegate Connection.commit already succeeded. Only its acknowledgement is lost.
    throw new SQLException("Test source commit acknowledgement lost", "08006");
  }

  DataSource dataSource(DataSource target) {
    if (!enabled) return target;
    return new DelegatingDataSource(target) {
      @Override
      public Connection getConnection() throws SQLException {
        return connection(super.getConnection());
      }

      @Override
      public Connection getConnection(String user, String password) throws SQLException {
        return connection(super.getConnection(user, password));
      }
    };
  }

  private static Object call(Method method, Object target, Object[] args) throws Throwable {
    try {
      return method.invoke(target, args);
    } catch (InvocationTargetException error) {
      throw error.getCause();
    }
  }

  private static Object identity(Object proxy, Method method, Object[] args) {
    return switch (method.getName()) {
      case "equals" -> proxy == args[0];
      case "hashCode" -> System.identityHashCode(proxy);
      case "toString" -> "isolated-source-jdbc-boundary";
      default -> throw new IllegalArgumentException("Unexpected Object method");
    };
  }

  private Connection connection(Connection target) {
    boolean[] written = {false};
    return (Connection)
        Proxy.newProxyInstance(
            Connection.class.getClassLoader(),
            new Class<?>[] {Connection.class},
            (proxy, method, args) -> {
              if (method.getDeclaringClass() == Object.class) return identity(proxy, method, args);
              Object result = call(method, target, args);
              if (method.getName().equals("prepareStatement")
                  && args[0] instanceof String sql
                  && sql.contains("INSERT INTO iam.network_source_operation")
                  && result instanceof PreparedStatement statement)
                return Proxy.newProxyInstance(
                    PreparedStatement.class.getClassLoader(),
                    new Class<?>[] {PreparedStatement.class},
                    (p, m, a) -> {
                      if (m.getDeclaringClass() == Object.class) return identity(p, m, a);
                      Object value = call(m, statement, a);
                      if (m.getName().startsWith("execute")
                          && value instanceof Number count
                          && count.longValue() > 0) written[0] = true;
                      return value;
                    });
              if (method.getName().equals("rollback")) written[0] = false;
              if (method.getName().equals("commit")) {
                boolean source = written[0];
                written[0] = false;
                if (source) committed();
              }
              return result;
            });
  }

  IamGrpcNetworkSources service(IamNetworkSourceAuthority authority) {
    return enabled ? new Service(authority) : new IamGrpcNetworkSources(authority);
  }

  private final class Service extends IamGrpcNetworkSources {
    Service(IamNetworkSourceAuthority authority) {
      super(authority);
    }

    @Override
    @AuthenticatedOnly
    public void registerAgentSource(
        RegisterAgentSourceRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
      invoke("RegisterAgentSource", r, o, p -> super.registerAgentSource(r, p));
    }

    @Override
    @AuthenticatedOnly
    public void readAgentSourceReceipt(
        ReadAgentSourceReceiptRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
      invoke("ReadAgentSourceReceipt", r, o, p -> super.readAgentSourceReceipt(r, p));
    }

    @Override
    @AuthenticatedOnly
    public void fenceAgentSourceReceipt(
        FenceAgentSourceReceiptRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
      invoke("FenceAgentSourceReceipt", r, o, p -> super.fenceAgentSourceReceipt(r, p));
    }

    @Override
    @AuthenticatedOnly
    public void getAgentSource(GetAgentSourceRequest r, StreamObserver<GetAgentSourceResponse> o) {
      invoke("GetAgentSource", r, o, p -> super.getAgentSource(r, p));
    }

    @Override
    @AuthenticatedOnly
    public void revokeAgentSource(
        RevokeAgentSourceRequest r, StreamObserver<RevokeAgentSourceResponse> o) {
      invoke("RevokeAgentSource", r, o, p -> super.revokeAgentSource(r, p));
    }
  }

  @Override
  public synchronized void close() {
    for (var rule : rules.values()) rule.release.countDown();
    rules.clear();
  }
}
