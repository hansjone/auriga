package auriga.workspace.app;

import static auriga.workspace.persistence.WorkspaceStore.*;

import auriga.contracts.node.v1.*;
import auriga.workspace.web.model.Instance;
import auriga.workspace.web.model.Node;
import java.util.*;

/**
 * Converts internal storage into generated contract models; secret columns never leave this class.
 */
public final class WorkspaceViews {
  private final WorkspaceService service;

  public WorkspaceViews(WorkspaceService service) {
    this.service = service;
  }

  public Instance instance(Map<String, Object> row) {
    var s = WorkspaceService.spec(row);
    Map<String, Object> out = new LinkedHashMap<>();
    for (String field : List.of("id", "name", "ready")) out.put(field, row.get(field));
    out.put("tenantId", row.get("tenant_id"));
    out.put("ownerId", row.get("owner_id"));
    out.put("runtime", WorkspaceService.runtime(s));
    out.put(
        "desired", s.getDesired() == DesiredState.DESIRED_STATE_RUNNING ? "running" : "stopped");
    out.put("phase", row.get("phase"));
    out.put("generation", s.getGeneration());
    out.put("observedGeneration", row.get("observed_generation"));
    out.put("alwaysOn", row.get("always_on"));
    out.put("dshVersion", s.getDsh().getVersion());
    out.put(
        "resources",
        Map.of(
            "cpuMillis",
            s.getResources().getCpuMillis(),
            "memoryMb",
            s.getResources().getMemoryMb(),
            "diskGb",
            s.getResources().getDiskGb()));
    out.put(
        "managedNetworkState",
        service.managedNetworkEnabled() ? row.get("managed_network_state") : "unconfigured");
    out.put(
        "managedNetworkReason",
        service.managedNetworkEnabled() ? row.get("managed_network_reason") : null);
    out.put("pendingRestart", row.get("restart_pending"));
    out.put("pendingRestartReasons", List.of());
    out.put("error", null);
    if (row.get("status") != null)
      try {
        var status = InstanceStatus.parseFrom((byte[]) row.get("status"));
        out.put(
            "pendingRestartReasons",
            status.getRestartReasonsList().stream()
                .filter(
                    r ->
                        r != RestartReason.UNRECOGNIZED
                            && r != RestartReason.RESTART_REASON_UNSPECIFIED)
                .map(r -> r.name().replace("RESTART_REASON_", "").toLowerCase(Locale.ROOT))
                .toList());
        if (status.hasError())
          out.put(
              "error",
              Map.of(
                  "code",
                  status.getError().getCode(),
                  "attempts",
                  status.getError().getAttempts()));
      } catch (Exception e) {
        throw new IllegalStateException(e);
      }
    out.put("node", null);
    if (row.get("node_id") != null) {
      var node = service.store.node((UUID) row.get("node_id"));
      out.put("node", Map.of("id", row.get("node_id"), "name", node.get("name")));
    }
    for (String field : List.of("created_at", "updated_at", "last_activity_at", "next_wake_at")) {
      var value = instant(row, field);
      if (value != null)
        out.put(
            switch (field) {
              case "created_at" -> "createdAt";
              case "updated_at" -> "updatedAt";
              case "last_activity_at" -> "lastActivityAt";
              default -> "nextWakeAt";
            },
            value.toString());
    }
    out.put("entryUrl", "/open/" + row.get("id"));
    return WorkspaceService.JSON.convertValue(out, Instance.class);
  }

  public Node node(Map<String, Object> row) {
    Map<String, Object> out = new LinkedHashMap<>();
    for (String field : List.of("id", "name", "hostname", "os", "arch", "schedulable"))
      out.put(field, row.get(field));
    out.put("agentVersion", row.get("agent_version"));
    out.put("labels", WorkspaceService.JSON.readTree(row.get("labels").toString()));
    out.put("capacity", Map.of());
    out.put("drivers", List.of());
    var seen = instant(row, "last_seen_at");
    out.put(
        "status",
        (boolean) row.get("revoked")
            ? "revoked"
            : (boolean) row.get("draining")
                ? "draining"
                : seen != null && seen.isAfter(service.clock.instant().minusSeconds(60))
                    ? "online"
                    : "offline");
    out.put("joinedAt", instant(row, "joined_at").toString());
    out.put("certificateExpiresAt", instant(row, "certificate_expires_at").toString());
    if (seen != null) out.put("lastSeenAt", seen.toString());
    if (row.get("hello") != null)
      try {
        var h = Hello.parseFrom((byte[]) row.get("hello"));
        var c = h.getCapacity();
        Map<String, Object> capacity = new HashMap<>();
        capacity.put("cpuMillis", c.getCpuMillis());
        capacity.put("memoryMb", c.getMemoryMb());
        capacity.put("diskGb", c.getDiskGb());
        if (c.hasMaxInstances()) capacity.put("maxInstances", c.getMaxInstances());
        out.put("capacity", capacity);
        out.put(
            "drivers",
            h.getDriversList().stream()
                .filter(
                    r -> r != RuntimeKind.UNRECOGNIZED && r != RuntimeKind.RUNTIME_KIND_UNSPECIFIED)
                .map(r -> r.name().replace("RUNTIME_KIND_", "").toLowerCase(Locale.ROOT))
                .toList());
        if (h.hasNodeTime())
          out.put(
              "clockSkewMs",
              (h.getNodeTime().getSeconds() * 1000 + h.getNodeTime().getNanos() / 1000000)
                  - seen.toEpochMilli());
      } catch (Exception e) {
        throw new IllegalStateException(e);
      }
    return WorkspaceService.JSON.convertValue(out, Node.class);
  }
}
