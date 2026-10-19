package auriga.workspace.web;

import auriga.common.context.TenantContext;
import auriga.common.security.AuthenticatedOnly;
import auriga.workspace.app.WorkspaceQueries;
import java.util.*;
import java.util.concurrent.*;
import org.springframework.web.bind.annotation.*;
import org.springframework.web.servlet.mvc.method.annotation.SseEmitter;

@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
@RestController
@RequestMapping("/api/workspace/v1")
public class WorkspaceSseController implements AutoCloseable {
  private final WorkspaceQueries queries;
  private final ScheduledExecutorService executor =
      Executors.newSingleThreadScheduledExecutor(
          Thread.ofVirtual().name("workspace-sse").factory());

  public WorkspaceSseController(WorkspaceQueries queries) {
    this.queries = queries;
  }

  @GetMapping(value = "/instances/{id}/events", produces = "text/event-stream")
  @AuthenticatedOnly
  public SseEmitter events(@PathVariable UUID id) {
    var actor = WorkspaceController.actor();
    var initial = queries.owned(id, actor);
    UUID tenant = TenantContext.require();
    SseEmitter emitter = new SseEmitter(60000L);
    java.util.concurrent.atomic.AtomicReference<ScheduledFuture<?>> future =
        new java.util.concurrent.atomic.AtomicReference<>();
    Runnable cancel =
        () -> {
          var f = future.get();
          if (f != null) f.cancel(false);
        };
    emitter.onCompletion(cancel);
    emitter.onTimeout(
        () -> {
          cancel.run();
          emitter.complete();
        });
    emitter.onError(e -> cancel.run());
    java.util.concurrent.atomic.AtomicReference<String> previous =
        new java.util.concurrent.atomic.AtomicReference<>("");
    Runnable send =
        () ->
            TenantContext.run(
                tenant,
                () -> {
                  try {
                    var value = queries.owned(id, actor);
                    String json =
                        auriga.workspace.app.WorkspaceService.JSON.writeValueAsString(value);
                    if (!json.equals(previous.getAndSet(json)))
                      emitter.send(SseEmitter.event().name("instance").data(value));
                  } catch (Exception e) {
                    cancel.run();
                    emitter.completeWithError(e);
                  }
                });
    // Send the authorized initial state before scheduling; then poll persisted revisions across
    // replicas.
    try {
      emitter.send(SseEmitter.event().name("instance").data(initial));
      previous.set(auriga.workspace.app.WorkspaceService.JSON.writeValueAsString(initial));
    } catch (Exception e) {
      emitter.completeWithError(e);
      return emitter;
    }
    future.set(executor.scheduleWithFixedDelay(send, 1, 1, TimeUnit.SECONDS));
    return emitter;
  }

  @Override
  public void close() {
    executor.shutdownNow();
  }
}
