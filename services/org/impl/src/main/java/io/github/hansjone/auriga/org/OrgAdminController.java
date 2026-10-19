package io.github.hansjone.auriga.org;

import io.github.hansjone.auriga.common.security.AdminOnly;
import io.github.hansjone.auriga.common.security.RequiresPermission;
import java.time.Instant;
import java.util.List;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@RestController
@ConditionalOnExpression("${auriga.services.org.enabled:true}")
@RequestMapping("/api/org/v1/admin")
@AdminOnly
public class OrgAdminController {
  private final OrgService org;

  public OrgAdminController(OrgService org) {
    this.org = org;
  }

  @PatchMapping("/tenant")
  @RequiresPermission("org.tenant.write")
  public ObjectNode updateTenant(@RequestBody ObjectNode body) {
    return org.tenantJson(
        org.updateTenant(
            text(body, "displayName"),
            text(body, "locale"),
            body.has("hosts")
                ? OrgJson.MAPPER.convertValue(body.path("hosts"), List.class)
                : null));
  }

  @GetMapping("/members")
  @RequiresPermission("org.members.read")
  public ObjectNode listMembers(
      @RequestParam(required = false) String q,
      @RequestParam(required = false) String status,
      @RequestParam(required = false) String memberType,
      @RequestParam(required = false) UUID groupId,
      @RequestParam(required = false, defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return org.listMembers(q, status, memberType, groupId, limit, cursor);
  }

  @GetMapping("/members/{accountId}")
  @RequiresPermission("org.members.read")
  public ResponseEntity<ObjectNode> getMember(@PathVariable UUID accountId) {
    ObjectNode m = org.memberJson(accountId);
    return ResponseEntity.ok().eTag("\"" + m.path("version").asLong() + "\"").body(m);
  }

  @PatchMapping("/members/{accountId}")
  @RequiresPermission("org.members.write")
  public ObjectNode updateMember(
      @PathVariable UUID accountId,
      @RequestBody ObjectNode body,
      @RequestHeader(value = "If-Match", required = false) String ifMatch) {
    return org.updateMember(
        accountId,
        text(body, "displayName"),
        text(body, "email"),
        text(body, "memberType"),
        parseEtag(ifMatch));
  }

  @PostMapping("/members/{accountId}/disable")
  @RequiresPermission("org.members.write")
  public ObjectNode disable(
      @PathVariable UUID accountId, @RequestBody(required = false) ObjectNode body) {
    return org.disableMember(accountId, body == null ? null : text(body, "reason"));
  }

  @PostMapping("/members/{accountId}/enable")
  @RequiresPermission("org.members.write")
  public ObjectNode enable(@PathVariable UUID accountId) {
    return org.enableMember(accountId);
  }

  @PostMapping("/members/{accountId}/offboard")
  @RequiresPermission("org.members.offboard")
  public ObjectNode offboard(@PathVariable UUID accountId, @RequestBody ObjectNode body) {
    UUID handover =
        body.hasNonNull("handoverTo") ? UUID.fromString(body.path("handoverTo").asString()) : null;
    return org.offboardMember(accountId, handover, text(body, "reason"));
  }

  @GetMapping("/members/{accountId}/role-bindings")
  @RequiresPermission("org.roles.read")
  public ObjectNode memberBindings(@PathVariable UUID accountId) {
    return org.listMemberRoleBindings(accountId);
  }

  @GetMapping("/groups")
  @RequiresPermission("org.groups.read")
  public ObjectNode listGroups(
      @RequestParam(required = false) String q, @RequestParam(required = false) UUID parentId) {
    return org.listGroups(q, parentId);
  }

  @PostMapping("/groups")
  @RequiresPermission("org.groups.write")
  @ResponseStatus(HttpStatus.CREATED)
  public ObjectNode createGroup(@RequestBody ObjectNode body) {
    UUID parent =
        body.hasNonNull("parentId") ? UUID.fromString(body.path("parentId").asString()) : null;
    return org.createGroup(body.path("name").asString(), text(body, "description"), parent);
  }

  @GetMapping("/groups/{groupId}")
  @RequiresPermission("org.groups.read")
  public ObjectNode getGroup(@PathVariable UUID groupId) {
    return org.getGroup(groupId);
  }

  @PatchMapping("/groups/{groupId}")
  @RequiresPermission("org.groups.write")
  public ObjectNode updateGroup(@PathVariable UUID groupId, @RequestBody ObjectNode body) {
    UUID parent =
        body.has("parentId")
            ? (body.path("parentId").isNull()
                ? null
                : UUID.fromString(body.path("parentId").asString()))
            : null;
    return org.updateGroup(
        groupId, text(body, "name"), text(body, "description"), body.has("parentId") ? parent : null);
  }

  @DeleteMapping("/groups/{groupId}")
  @RequiresPermission("org.groups.write")
  @ResponseStatus(HttpStatus.NO_CONTENT)
  public void deleteGroup(@PathVariable UUID groupId) {
    org.deleteGroup(groupId);
  }

  @GetMapping("/groups/{groupId}/members")
  @RequiresPermission("org.groups.read")
  public ObjectNode groupMembers(
      @PathVariable UUID groupId,
      @RequestParam(required = false, defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return org.listGroupMembers(groupId, limit, cursor);
  }

  @PutMapping("/groups/{groupId}/members/{accountId}")
  @RequiresPermission("org.groups.write")
  @ResponseStatus(HttpStatus.NO_CONTENT)
  public void addMember(@PathVariable UUID groupId, @PathVariable UUID accountId) {
    org.addGroupMember(groupId, accountId);
  }

  @DeleteMapping("/groups/{groupId}/members/{accountId}")
  @RequiresPermission("org.groups.write")
  @ResponseStatus(HttpStatus.NO_CONTENT)
  public void removeMember(@PathVariable UUID groupId, @PathVariable UUID accountId) {
    org.removeGroupMember(groupId, accountId);
  }

  @GetMapping("/permissions")
  @RequiresPermission("org.roles.read")
  public ObjectNode permissions() {
    return org.listPermissions();
  }

  @GetMapping("/roles")
  @RequiresPermission("org.roles.read")
  public ObjectNode roles() {
    return org.listRoles();
  }

  @PostMapping("/roles")
  @RequiresPermission("org.roles.write")
  @ResponseStatus(HttpStatus.CREATED)
  public ObjectNode createRole(@RequestBody ObjectNode body) {
    return org.createRole(
        body.path("key").asString(),
        body.path("name"),
        body.path("description"),
        OrgJson.MAPPER.convertValue(body.path("permissions"), List.class));
  }

  @GetMapping("/roles/{roleId}")
  @RequiresPermission("org.roles.read")
  public ObjectNode getRole(@PathVariable UUID roleId) {
    return org.getRole(roleId);
  }

  @PatchMapping("/roles/{roleId}")
  @RequiresPermission("org.roles.write")
  public ObjectNode updateRole(@PathVariable UUID roleId, @RequestBody ObjectNode body) {
    List<String> perms =
        body.has("permissions")
            ? OrgJson.MAPPER.convertValue(body.path("permissions"), List.class)
            : null;
    return org.updateRole(
        roleId,
        body.has("name") ? body.path("name") : null,
        body.has("description") ? body.path("description") : null,
        perms);
  }

  @DeleteMapping("/roles/{roleId}")
  @RequiresPermission("org.roles.write")
  @ResponseStatus(HttpStatus.NO_CONTENT)
  public void deleteRole(@PathVariable UUID roleId) {
    org.deleteRole(roleId);
  }

  @GetMapping("/role-bindings")
  @RequiresPermission("org.roles.read")
  public ObjectNode listBindings(
      @RequestParam(required = false) UUID roleId,
      @RequestParam(required = false, defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return org.listRoleBindings(roleId, limit, cursor);
  }

  @PostMapping("/role-bindings")
  @RequiresPermission("org.roles.grant")
  @ResponseStatus(HttpStatus.CREATED)
  public ObjectNode createBinding(@RequestBody ObjectNode body) {
    JsonNode subject = body.path("subject");
    return org.createRoleBinding(
        UUID.fromString(body.path("roleId").asString()),
        subject.path("type").asString(),
        UUID.fromString(subject.path("id").asString()));
  }

  @DeleteMapping("/role-bindings/{bindingId}")
  @RequiresPermission("org.roles.grant")
  @ResponseStatus(HttpStatus.NO_CONTENT)
  public void deleteBinding(@PathVariable UUID bindingId) {
    org.deleteRoleBinding(bindingId);
  }

  @GetMapping("/policies/{key}")
  @RequiresPermission("org.policies.read")
  public ResponseEntity<ObjectNode> getPolicy(@PathVariable String key) {
    ObjectNode p = org.getPolicy(key);
    return ResponseEntity.ok().eTag("\"" + p.path("version").asLong() + "\"").body(p);
  }

  @PutMapping("/policies/{key}")
  @RequiresPermission("org.policies.write")
  public ObjectNode putPolicy(
      @PathVariable String key,
      @RequestBody ObjectNode body,
      @RequestHeader(value = "If-Match", required = false) String ifMatch) {
    return org.putPolicy(key, body.path("value"), parseEtag(ifMatch));
  }

  @GetMapping("/quotas/default")
  @RequiresPermission("org.quotas.read")
  public ObjectNode defaultQuota() {
    return org.getQuota("default", null);
  }

  @PutMapping("/quotas/default")
  @RequiresPermission("org.quotas.write")
  public ObjectNode putDefaultQuota(@RequestBody ObjectNode body) {
    return org.putQuota("default", null, body);
  }

  @GetMapping("/groups/{groupId}/quota")
  @RequiresPermission("org.quotas.read")
  public ObjectNode groupQuota(@PathVariable UUID groupId) {
    return org.getQuota("group", groupId);
  }

  @PutMapping("/groups/{groupId}/quota")
  @RequiresPermission("org.quotas.write")
  public ObjectNode putGroupQuota(@PathVariable UUID groupId, @RequestBody ObjectNode body) {
    return org.putQuota("group", groupId, body);
  }

  @DeleteMapping("/groups/{groupId}/quota")
  @RequiresPermission("org.quotas.write")
  @ResponseStatus(HttpStatus.NO_CONTENT)
  public void deleteGroupQuota(@PathVariable UUID groupId) {
    org.deleteQuota("group", groupId);
  }

  @GetMapping("/members/{accountId}/quota")
  @RequiresPermission("org.quotas.read")
  public ObjectNode memberQuota(@PathVariable UUID accountId) {
    return org.getQuota("member", accountId);
  }

  @PutMapping("/members/{accountId}/quota")
  @RequiresPermission("org.quotas.write")
  public ObjectNode putMemberQuota(@PathVariable UUID accountId, @RequestBody ObjectNode body) {
    return org.putQuota("member", accountId, body);
  }

  @DeleteMapping("/members/{accountId}/quota")
  @RequiresPermission("org.quotas.write")
  @ResponseStatus(HttpStatus.NO_CONTENT)
  public void deleteMemberQuota(@PathVariable UUID accountId) {
    org.deleteQuota("member", accountId);
  }

  @GetMapping("/members/{accountId}/quota/effective")
  @RequiresPermission("org.quotas.read")
  public ObjectNode effectiveQuota(@PathVariable UUID accountId) {
    return org.getEffectiveQuota(accountId);
  }

  @GetMapping("/invitations")
  @RequiresPermission("org.invitations.read")
  public ObjectNode listInvitations(
      @RequestParam(required = false) String status,
      @RequestParam(required = false, defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return org.listInvitations(status, limit, cursor);
  }

  @PostMapping("/invitations")
  @RequiresPermission("org.invitations.write")
  @ResponseStatus(HttpStatus.CREATED)
  public ObjectNode createInvitation(@RequestBody ObjectNode body) {
    Instant expires =
        body.hasNonNull("expiresAt") ? Instant.parse(body.path("expiresAt").asString()) : null;
    int maxUses = body.path("maxUses").asInt(1);
    return org.createInvitation(
        text(body, "email"),
        text(body, "displayName"),
        body.path("memberType").asString("member"),
        uuids(body.path("roleIds")),
        uuids(body.path("groupIds")),
        uuids(body.path("providerIds")),
        expires,
        maxUses);
  }

  @DeleteMapping("/invitations/{invitationId}")
  @RequiresPermission("org.invitations.write")
  @ResponseStatus(HttpStatus.NO_CONTENT)
  public void revoke(@PathVariable UUID invitationId) {
    org.revokeInvitation(invitationId);
  }

  private static String text(ObjectNode body, String field) {
    return body.hasNonNull(field) ? body.path(field).asString() : null;
  }

  private static Long parseEtag(String ifMatch) {
    if (ifMatch == null || ifMatch.isBlank()) return null;
    String v = ifMatch.trim();
    if (v.startsWith("\"") && v.endsWith("\"")) v = v.substring(1, v.length() - 1);
    return Long.parseLong(v);
  }

  private static List<UUID> uuids(JsonNode n) {
    if (n == null || n.isMissingNode() || n.isNull()) return List.of();
    return OrgJson.MAPPER.convertValue(
        n, OrgJson.MAPPER.getTypeFactory().constructCollectionType(List.class, UUID.class));
  }
}
