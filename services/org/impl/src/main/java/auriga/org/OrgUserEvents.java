package auriga.org;

import auriga.common.events.EventPublisher;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import tools.jackson.databind.node.ObjectNode;

final class OrgUserEvents {
  private final EventPublisher events;
  private final OrgAudit audit;

  OrgUserEvents(EventPublisher events) {
    this.events = events;
    this.audit = new OrgAudit(events);
  }

  void created(ObjectNode member, String via, UUID invitationId, UUID actorId) {
    Map<String, Object> data = new LinkedHashMap<>();
    data.put("member", snapshot(member));
    data.put("via", via);
    if (invitationId != null) data.put("invitationId", invitationId.toString());
    data.put("actorId", actorId == null ? null : actorId.toString());
    events.publish("user.created", member.path("id").asString(), data);
    audit.record(
        "org.member.create",
        "member",
        member.path("id").asString(),
        "invitation".equals(via) ? UUID.fromString(member.path("id").asString()) : actorId);
  }

  void updated(ObjectNode member, List<String> changed, UUID actorId) {
    Map<String, Object> data = new LinkedHashMap<>();
    data.put("member", snapshot(member));
    data.put("changed", changed);
    data.put("actorId", actorId == null ? null : actorId.toString());
    events.publish("user.updated", member.path("id").asString(), data);
    audit.record("org.member.update", "member", member.path("id").asString(), actorId);
  }

  void disabled(UUID userId, String reason, UUID actorId) {
    Map<String, Object> data = new LinkedHashMap<>();
    data.put("userId", userId.toString());
    if (reason != null) data.put("reason", reason);
    data.put("actorId", actorId == null ? null : actorId.toString());
    events.publish("user.disabled", userId.toString(), data);
    audit.record("org.member.disable", "member", userId, actorId);
  }

  void enabled(UUID userId, UUID actorId) {
    Map<String, Object> data = new LinkedHashMap<>();
    data.put("userId", userId.toString());
    data.put("actorId", actorId == null ? null : actorId.toString());
    events.publish("user.enabled", userId.toString(), data);
    audit.record("org.member.enable", "member", userId, actorId);
  }

  void offboarded(UUID userId, UUID handoverTo, String reason, UUID actorId) {
    Map<String, Object> data = new LinkedHashMap<>();
    data.put("userId", userId.toString());
    data.put("handoverTo", handoverTo == null ? null : handoverTo.toString());
    if (reason != null) data.put("reason", reason);
    data.put("actorId", actorId == null ? null : actorId.toString());
    events.publish("user.offboarded", userId.toString(), data);
    audit.record("org.member.offboard", "member", userId, actorId);
  }

  void permissionsChanged(UUID userId, List<String> roleKeys, UUID actorId) {
    Map<String, Object> data = new LinkedHashMap<>();
    data.put("userId", userId.toString());
    data.put("roleKeys", roleKeys);
    data.put("actorId", actorId == null ? null : actorId.toString());
    events.publish("user.permissions_changed", userId.toString(), data);
  }

  private static Map<String, Object> snapshot(ObjectNode member) {
    Map<String, Object> snap = new LinkedHashMap<>();
    snap.put("userId", member.path("id").asString());
    snap.put("displayName", member.path("displayName").asString());
    if (member.hasNonNull("email")) snap.put("email", member.path("email").asString());
    snap.put("memberType", member.path("memberType").asString());
    snap.put("status", member.path("status").asString());
    snap.put("groupIds", OrgJson.MAPPER.convertValue(member.path("groupIds"), List.class));
    snap.put("roleKeys", OrgJson.MAPPER.convertValue(member.path("roleKeys"), List.class));
    return snap;
  }
}
