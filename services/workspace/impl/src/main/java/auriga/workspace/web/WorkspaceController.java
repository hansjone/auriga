package auriga.workspace.web;

import auriga.common.error.*;
import auriga.common.security.*;
import auriga.workspace.app.*;
import auriga.workspace.grpc.NodeSessions;
import auriga.workspace.web.api.*;
import auriga.workspace.web.model.*;
import jakarta.servlet.http.HttpServletRequest;
import java.util.*;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

/** Uses generated request/response models; ownership checks live in the application service. */
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
@RestController
@RequestMapping("/api/workspace/v1")
public class WorkspaceController implements MeApi, AdminRuntimesApi {
  private final WorkspaceService service;
  private final WorkspaceQueries queries;
  private final WorkspaceAdmin admin;
  private final NodeEnrollment enrollment;
  private final NodeSessions sessions;
  private final HttpServletRequest request;

  public WorkspaceController(
      WorkspaceService service,
      WorkspaceQueries queries,
      WorkspaceAdmin admin,
      NodeEnrollment enrollment,
      NodeSessions sessions,
      HttpServletRequest request) {
    this.service = service;
    this.queries = queries;
    this.admin = admin;
    this.enrollment = enrollment;
    this.sessions = sessions;
    this.request = request;
  }

  static AurigaPrincipal actor() {
    return AuthorizationRules.currentPrincipal()
        .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
  }

  @Override
  @AuthenticatedOnly
  public ResponseEntity<ListMyInstances200Response> listMyInstances() {
    return ResponseEntity.ok(
        new ListMyInstances200Response(
            queries.instances(UUID.fromString(actor().subject()), null, null, 200, null)));
  }

  @Override
  @AuthenticatedOnly
  public ResponseEntity<OpenResult> openMyInstance() {
    var result = service.open(UUID.fromString(actor().subject()));
    return ResponseEntity.ok(
        service.store.tx(
            () -> new OpenResult(queries.views.instance(result.instance()), result.created())));
  }

  @GetMapping("/instances/{id}")
  @AuthenticatedOnly
  public ResponseEntity<Instance> get(@PathVariable UUID id) {
    var body = queries.owned(id, actor());
    return ResponseEntity.ok().eTag("\"" + body.getGeneration() + "\"").body(body);
  }

  @PostMapping("/instances/{id}/start")
  @AuthenticatedOnly
  public ResponseEntity<Instance> start(@PathVariable UUID id) {
    return operate(id, "start");
  }

  @PostMapping("/instances/{id}/stop")
  @AuthenticatedOnly
  public ResponseEntity<Instance> stop(@PathVariable UUID id) {
    return operate(id, "stop");
  }

  @PostMapping("/instances/{id}/restart")
  @AuthenticatedOnly
  public ResponseEntity<Instance> restart(@PathVariable UUID id) {
    return operate(id, "restart");
  }

  private ResponseEntity<Instance> operate(UUID id, String action) {
    var row = service.operate(id, actor(), action);
    sessions.dispatch();
    return ResponseEntity.accepted().body(service.store.tx(() -> queries.views.instance(row)));
  }

  @GetMapping("/admin/instances")
  @AdminOnly
  @RequiresPermission("workspace.instances.read")
  public ListInstances200Response list(
      @RequestParam(required = false) UUID ownerId,
      @RequestParam(required = false) UUID nodeId,
      @RequestParam(required = false) auriga.workspace.web.model.InstancePhase phase,
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    var items =
        queries.instances(ownerId, nodeId, phase == null ? null : phase.getValue(), limit, cursor);
    var result = new ListInstances200Response(items);
    if (items.size() == limit) result.setNextCursor(items.getLast().getId().toString());
    return result;
  }

  @PatchMapping(value = "/admin/instances/{id}", consumes = "application/merge-patch+json")
  @AdminOnly
  @RequiresPermission("workspace.instances.admin")
  public Instance patch(@PathVariable UUID id, @RequestBody ObjectNode body) {
    var row = admin.patch(id, body, request.getHeader("If-Match"));
    sessions.dispatch();
    return service.store.tx(() -> queries.views.instance(row));
  }

  @DeleteMapping("/admin/instances/{id}")
  @AdminOnly
  @RequiresPermission("workspace.instances.admin")
  public ResponseEntity<Void> delete(
      @PathVariable UUID id, @RequestParam(defaultValue = "false") boolean purgeData) {
    admin.delete(id, purgeData, UUID.fromString(actor().subject()));
    sessions.dispatch();
    return ResponseEntity.accepted().build();
  }

  @AdminOnly
  @RequiresPermission("workspace.nodes.read")
  @GetMapping("/admin/nodes")
  public ResponseEntity<ListNodes200Response> listNodes() {
    return ResponseEntity.ok(new ListNodes200Response(queries.nodes()));
  }

  @AdminOnly
  @RequiresPermission("workspace.nodes.read")
  @GetMapping("/admin/nodes/{id}")
  public ResponseEntity<Node> getNode(@PathVariable UUID id) {
    return ResponseEntity.ok(queries.node(id));
  }

  @AdminOnly
  @RequiresPermission("workspace.nodes.admin")
  @PatchMapping(value = "/admin/nodes/{id}", consumes = "application/merge-patch+json")
  public ResponseEntity<Node> updateNode(@PathVariable UUID id, @RequestBody ObjectNode body) {
    var patch = WorkspaceService.JSON.treeToValue(body, UpdateNodeRequest.class);
    if (!body.has("labels")) patch.setLabels(null);
    var row = admin.node(id, patch, false);
    return ResponseEntity.ok(service.store.tx(() -> queries.views.node(row)));
  }

  @AdminOnly
  @RequiresPermission("workspace.nodes.admin")
  @PostMapping("/admin/nodes/{id}/drain")
  public ResponseEntity<Node> drainNode(@PathVariable UUID id) {
    var row = admin.node(id, null, true);
    return ResponseEntity.accepted().body(service.store.tx(() -> queries.views.node(row)));
  }

  @AdminOnly
  @RequiresPermission("workspace.nodes.admin")
  @DeleteMapping("/admin/nodes/{id}")
  public ResponseEntity<Void> revokeNode(
      @PathVariable UUID id, @RequestParam(defaultValue = "false") boolean force) {
    admin.revoke(id, force);
    sessions.revoke(id);
    return ResponseEntity.noContent().build();
  }

  @AdminOnly
  @RequiresPermission("workspace.nodes.admin")
  @GetMapping("/admin/join-tokens")
  public ResponseEntity<ListJoinTokens200Response> listJoinTokens() {
    return ResponseEntity.ok(new ListJoinTokens200Response(queries.tokens()));
  }

  @AdminOnly
  @RequiresPermission("workspace.nodes.admin")
  @PostMapping("/admin/join-tokens")
  public ResponseEntity<CreateJoinToken201Response> createJoinToken(
      @jakarta.validation.Valid @RequestBody(required = false) CreateJoinTokenRequest body) {
    var token =
        service.joinToken(
            enrollment,
            body == null ? null : body.getName(),
            body == null || body.getTtlMinutes() == null ? 60 : body.getTtlMinutes(),
            UUID.fromString(actor().subject()),
            request.getHeader("Idempotency-Key"));
    return ResponseEntity.status(201)
        .body(WorkspaceService.JSON.convertValue(token, CreateJoinToken201Response.class));
  }

  @AdminOnly
  @RequiresPermission("workspace.nodes.admin")
  @DeleteMapping("/admin/join-tokens/{id}")
  public ResponseEntity<Void> deleteJoinToken(@PathVariable UUID id) {
    queries.deleteToken(id);
    return ResponseEntity.noContent().build();
  }

  @Override
  @AdminOnly
  @RequiresPermission("workspace.runtimes.read")
  public ResponseEntity<ListRuntimes200Response> listRuntimes() {
    return ResponseEntity.ok(queries.runtimes());
  }

  @Override
  @AdminOnly
  @RequiresPermission("workspace.runtimes.write")
  public ResponseEntity<Void> setDefaultRuntime(SetDefaultRuntimeRequest body) {
    admin.defaultRuntime(body.getVersion());
    sessions.dispatch();
    return ResponseEntity.noContent().build();
  }
}
