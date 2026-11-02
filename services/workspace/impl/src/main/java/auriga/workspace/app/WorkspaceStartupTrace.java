package auriga.workspace.app;

import auriga.common.context.RequestContext;
import auriga.common.context.TraceContext;
import auriga.common.observability.LogFields;
import auriga.contracts.node.v1.ApplySpec;
import auriga.contracts.node.v1.InstanceSpec;
import java.util.Map;

/** Only observation data crosses the durable startup boundary; no caller principal is retained. */
public final class WorkspaceStartupTrace {
  private WorkspaceStartupTrace() {}

  static String begin() {
    return RequestContext.current()
        .map(c -> c.trace().toTraceparent())
        .orElseGet(() -> TraceContext.start().toTraceparent());
  }

  public static String parent(Map<String, Object> row) {
    return TraceContext.parse((String) row.get("startup_traceparent"))
        .map(TraceContext::toTraceparent)
        .orElse("");
  }

  public static ApplySpec dispatch(Map<String, Object> row, InstanceSpec spec) {
    var result = ApplySpec.newBuilder().setSpec(spec);
    TraceContext.parse(parent(row))
        .ifPresent(
            parent -> {
              var child = parent.child();
              result.setTraceparent(child.toTraceparent());
              try (var fields =
                  LogFields.of(
                      Map.of(
                          LogFields.TRACE_ID,
                          child.traceId(),
                          LogFields.SPAN_ID,
                          child.spanId(),
                          "parent_span_id",
                          parent.spanId(),
                          "instance_id",
                          spec.getInstanceId(),
                          "generation",
                          Long.toString(spec.getGeneration())))) {
                org.slf4j.LoggerFactory.getLogger(WorkspaceStartupTrace.class)
                    .info("Dispatching startup spec");
              }
            });
    return result.build();
  }

  /** A managed-material step belongs to an unfinished startup, never to an old completed open. */
  static void reconcile(Map<String, Object> row, Runnable operation) {
    var parent = TraceContext.parse(parent(row));
    if (parent.isEmpty()) {
      operation.run();
      return;
    }
    var child = parent.get().child();
    var request = new RequestContext("startup-" + row.get("id"), child, null, null, null);
    try (var fields = LogFields.of(request);
        var selected =
            LogFields.of(
                Map.of(
                    "parent_span_id", parent.get().spanId(),
                    "instance_id", row.get("id").toString(),
                    "generation", row.get("generation").toString()))) {
      request.run(operation);
    }
  }
}
