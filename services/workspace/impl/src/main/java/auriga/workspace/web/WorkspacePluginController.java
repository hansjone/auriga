package auriga.workspace.web;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.app.*;
import auriga.workspace.domain.WorkspaceErrors;
import auriga.workspace.web.api.ConnectApi;
import auriga.workspace.web.api.InstanceApiApi;
import auriga.workspace.web.model.*;
import jakarta.servlet.http.HttpServletRequest;
import java.util.*;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
@RestController
@RequestMapping("/api/workspace/v1")
public class WorkspacePluginController implements InstanceApiApi, ConnectApi {
  private final WorkspaceService service;
  private final WorkspaceInputs inputs;
  private final HttpServletRequest request;
  private final ConnectManifest manifest;

  public WorkspacePluginController(
      WorkspaceService service,
      WorkspaceInputs inputs,
      HttpServletRequest request,
      ConnectManifest manifest) {
    this.service = service;
    this.inputs = inputs;
    this.request = request;
    this.manifest = manifest;
  }

  private static void protocol(int version) {
    if (version != 1)
      throw new AurigaException(
          WorkspaceErrors.PROTOCOL_UNSUPPORTED, Map.of("current", 1, "minSupported", 1));
  }

  private Map<String, Object> instance() {
    String auth = request.getHeader("Authorization");
    if (auth == null || !auth.startsWith("Bearer "))
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    return service.authenticateInstance(auth.substring(7));
  }

  @Override
  @PublicEndpoint
  public ResponseEntity<GetInstanceSelf200Response> getInstanceSelf(Integer version) {
    var row = instance();
    protocol(version);
    return ResponseEntity.ok(
        WorkspaceService.JSON.convertValue(
            Map.of(
                "instanceId",
                row.get("id"),
                "ownerId",
                row.get("owner_id"),
                "tenantId",
                row.get("tenant_id"),
                "generation",
                row.get("generation"),
                "protocol",
                Map.of("current", 1, "minSupported", 1)),
            GetInstanceSelf200Response.class));
  }

  @Override
  @PublicEndpoint
  public ResponseEntity<HeartbeatResponse> postInstanceHeartbeat(
      Integer version, HeartbeatRequest body) {
    var row = instance();
    protocol(version);
    TenantContext.run((UUID) row.get("tenant_id"), () -> service.heartbeat(row, body));
    return ResponseEntity.ok(
        WorkspaceService.JSON.convertValue(
            Map.of(
                "serverTime",
                service.clock.instant().toString(),
                "protocol",
                Map.of("current", 1, "minSupported", 1),
                "nextHeartbeatSeconds",
                30,
                "pendingRestart",
                "pending_restart".equals(row.get("phase"))),
            HeartbeatResponse.class));
  }

  @Override
  @AuthenticatedOnly
  public ResponseEntity<ConnectManifest> getConnectManifest(Integer version) {
    var actor = WorkspaceController.actor();
    if (actor.type() != PrincipalType.DEVICE)
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    protocol(version);
    var member = inputs.member(actor.tenantId(), UUID.fromString(actor.subject()));
    if (member.getStatus() != auriga.contracts.org.v1.MemberStatus.MEMBER_STATUS_ACTIVE)
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (manifest.getPanel() == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    var copy =
        WorkspaceService.JSON.readValue(
            WorkspaceService.JSON.writeValueAsBytes(manifest), ConnectManifest.class);
    copy.setProtocol(new ProtocolInfo(1, 1));
    copy.setAccount(
        new ConnectManifestAccount(
            UUID.fromString(actor.subject()),
            member.getDisplayName(),
            actor.tenantId().toString()));
    return ResponseEntity.ok(copy);
  }
}
