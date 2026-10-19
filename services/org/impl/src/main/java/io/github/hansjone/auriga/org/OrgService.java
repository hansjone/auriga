package io.github.hansjone.auriga.org;

import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.events.EventPublisher;
import io.github.hansjone.auriga.common.persistence.UuidV7;
import io.github.hansjone.auriga.common.security.AuthorizationRules;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.time.Clock;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.HexFormat;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.Set;
import java.util.UUID;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Org business facade: tenants, members, groups, roles, policies, quotas, invitations. */
public final class OrgService {
  private final OrgStore store;
  private final OrgUserEvents userEvents;
  private final Clock clock;
  private final SecureRandom random = new SecureRandom();

  public OrgService(OrgStore store, EventPublisher events, Clock clock) {
    this.store = store;
    this.userEvents = new OrgUserEvents(events);
    this.clock = clock;
  }

  public OrgStore store() {
    return store;
  }

  // ---- tenant ----

  public OrgStore.TenantRow resolveTenantByHost(String host) {
    Optional<OrgStore.TenantRow> hit =
        host == null || host.isBlank() ? Optional.empty() : store.resolveByHost(host.trim());
    return hit.or(store::defaultTenant).orElseThrow(() -> OrgError.error("not_found", 404));
  }

  public OrgStore.TenantRow getTenant() {
    return store
        .tx(() -> store.tenantById(TenantContext.require()))
        .orElseThrow(() -> OrgError.error("not_found", 404));
  }

  public OrgStore.TenantRow updateTenant(String displayName, String locale, List<String> hosts) {
    return store.tx(
        () -> {
          UUID id = TenantContext.require();
          store.updateTenant(id, displayName, locale, hosts);
          return store.tenantById(id).orElseThrow(() -> OrgError.error("not_found", 404));
        });
  }

  public ObjectNode tenantJson(OrgStore.TenantRow t) {
    return OrgJson.object(
        "id",
        t.id().toString(),
        "slug",
        t.slug(),
        "displayName",
        t.displayName(),
        "locale",
        t.locale(),
        "hosts",
        t.hosts(),
        "createdAt",
        t.createdAt().toString());
  }

  // ---- authorization ----

  public record Effective(
      String memberType, String status, List<String> roleKeys, List<String> permissions) {}

  public Effective getEffectivePermissions(UUID accountId) {
    return store.tx(
        () -> {
          var member = store.member(accountId).orElse(null);
          if (member == null) return new Effective("member", "active", List.of(), List.of());
          List<String> roles = roleKeysOf(accountId);
          List<String> perms = store.permissionsForRoles(roles);
          return new Effective(member.memberType(), member.status(), roles, perms);
        });
  }

  public boolean checkPermission(UUID accountId, String permission) {
    Effective e = getEffectivePermissions(accountId);
    return "active".equals(e.status()) && e.permissions().contains(permission);
  }

  public record GrantResult(boolean allowed, List<String> exceeding) {}

  public GrantResult checkGrant(UUID grantorId, List<UUID> roleIds, UUID groupId) {
    return store.tx(
        () -> {
          Set<String> grantor =
              new LinkedHashSet<>(getEffectivePermissions(grantorId).permissions());
          Set<String> target = new LinkedHashSet<>();
          if (roleIds != null && !roleIds.isEmpty())
            target.addAll(store.permissionsForRoleIds(roleIds));
          if (groupId != null) target.addAll(store.groupEffectivePermissions(groupId));
          List<String> exceeding =
              target.stream().filter(p -> !grantor.contains(p)).sorted().toList();
          return new GrantResult(exceeding.isEmpty(), exceeding);
        });
  }

  public void ensureGrantAllowed(UUID grantorId, List<UUID> roleIds, UUID groupId) {
    GrantResult r = checkGrant(grantorId, roleIds, groupId);
    OrgError.require(r.allowed(), "grant_exceeds_own", 403);
  }

  private List<String> roleKeysOf(UUID accountId) {
    Set<String> keys = new LinkedHashSet<>();
    keys.addAll(store.directRoleKeys(accountId));
    keys.addAll(store.groupRoleKeys(accountId));
    return List.copyOf(keys);
  }

  // ---- members ----

  public ObjectNode memberJson(UUID accountId) {
    return store.tx(
        () -> {
          var m = store.member(accountId).orElseThrow(() -> OrgError.error("not_found", 404));
          return toMemberJson(m);
        });
  }

  public ObjectNode toMemberJson(OrgStore.MemberRow m) {
    List<UUID> groups = store.groupIdsOf(m.id());
    List<String> roles = roleKeysOf(m.id());
    ObjectNode n =
        OrgJson.object(
            "id",
            m.id().toString(),
            "displayName",
            m.displayName(),
            "memberType",
            m.memberType(),
            "status",
            m.status(),
            "groupIds",
            groups.stream().map(UUID::toString).toList(),
            "roleKeys",
            roles,
            "createdAt",
            m.createdAt().toString());
    if (m.email() != null) n.put("email", m.email());
    if (m.disabledAt() != null) n.put("disabledAt", m.disabledAt().toString());
    if (m.offboardedAt() != null) n.put("offboardedAt", m.offboardedAt().toString());
    n.put("version", m.version());
    return n;
  }

  public ObjectNode listMembers(
      String q, String status, String memberType, UUID groupId, int limit, String cursor) {
    return store.tx(
        () -> {
          int lim = Math.min(Math.max(limit <= 0 ? 50 : limit, 1), 200);
          StringBuilder sql =
              new StringBuilder(
                  "SELECT id, display_name, email, member_type, status, version, created_at,"
                      + " disabled_at, offboarded_at FROM member WHERE tenant_id=?");
          List<Object> args = new ArrayList<>();
          args.add(store.tenantId());
          if (status != null && !status.isBlank()) {
            sql.append(" AND status=?");
            args.add(status);
          }
          if (memberType != null && !memberType.isBlank()) {
            sql.append(" AND member_type=?");
            args.add(memberType);
          }
          if (q != null && !q.isBlank()) {
            sql.append(" AND (display_name ILIKE ? OR email ILIKE ?)");
            String like = "%" + q.trim() + "%";
            args.add(like);
            args.add(like);
          }
          if (groupId != null) {
            sql.append(
                " AND id IN (SELECT account_id FROM group_member WHERE tenant_id=? AND group_id=?)");
            args.add(store.tenantId());
            args.add(groupId);
          }
          Cursor c = decodeCursor(cursor);
          if (c != null) {
            sql.append(" AND (created_at, id) > (?, ?)");
            args.add(java.sql.Timestamp.from(c.at()));
            args.add(c.id());
          }
          sql.append(" ORDER BY created_at, id LIMIT ?");
          args.add(lim + 1);
          List<OrgStore.MemberRow> rows =
              store
                  .jdbc()
                  .query(
                      sql.toString(),
                      (rs, i) ->
                          new OrgStore.MemberRow(
                              (UUID) rs.getObject("id"),
                              rs.getString("display_name"),
                              rs.getString("email"),
                              rs.getString("member_type"),
                              rs.getString("status"),
                              rs.getLong("version"),
                              rs.getTimestamp("created_at").toInstant(),
                              rs.getTimestamp("disabled_at") == null
                                  ? null
                                  : rs.getTimestamp("disabled_at").toInstant(),
                              rs.getTimestamp("offboarded_at") == null
                                  ? null
                                  : rs.getTimestamp("offboarded_at").toInstant()),
                      args.toArray());
          String next = null;
          if (rows.size() > lim) {
            var last = rows.get(lim - 1);
            next = encodeCursor(last.createdAt(), last.id());
            rows = rows.subList(0, lim);
          }
          ObjectNode out = OrgJson.object("items", rows.stream().map(this::toMemberJson).toList());
          if (next != null) out.put("nextCursor", next);
          return out;
        });
  }

  public ObjectNode updateMember(
      UUID accountId, String displayName, String email, String memberType, Long ifMatch) {
    return store.tx(
        () -> {
          var m = store.member(accountId).orElseThrow(() -> OrgError.error("not_found", 404));
          if (ifMatch != null) OrgError.require(m.version() == ifMatch, "precondition_failed", 412);
          List<String> changed = new ArrayList<>();
          String dn = displayName != null ? displayName : m.displayName();
          String em = email != null ? email : m.email();
          String mt = memberType != null ? memberType : m.memberType();
          if (displayName != null && !Objects.equals(displayName, m.displayName()))
            changed.add("displayName");
          if (email != null && !Objects.equals(email, m.email())) changed.add("email");
          if (memberType != null && !Objects.equals(memberType, m.memberType()))
            changed.add("memberType");
          OrgError.require(!changed.isEmpty(), "conflict", 409);
          var updated =
              new OrgStore.MemberRow(
                  m.id(),
                  dn,
                  em,
                  mt,
                  m.status(),
                  m.version() + 1,
                  m.createdAt(),
                  m.disabledAt(),
                  m.offboardedAt());
          store.saveMember(updated);
          ObjectNode json = toMemberJson(updated);
          userEvents.updated(json, changed, actorId());
          return json;
        });
  }

  public ObjectNode disableMember(UUID accountId, String reason) {
    return store.tx(
        () -> {
          UUID actor = actorId();
          OrgError.require(actor == null || !actor.equals(accountId), "conflict", 409);
          var m = store.member(accountId).orElseThrow(() -> OrgError.error("not_found", 404));
          OrgError.require("active".equals(m.status()), "conflict", 409);
          if (store.isOwner(accountId))
            OrgError.require(store.countOwners() > 1, "last_owner", 409);
          Instant now = clock.instant();
          var updated =
              new OrgStore.MemberRow(
                  m.id(),
                  m.displayName(),
                  m.email(),
                  m.memberType(),
                  "disabled",
                  m.version() + 1,
                  m.createdAt(),
                  now,
                  m.offboardedAt());
          store.saveMember(updated);
          userEvents.disabled(accountId, reason, actor);
          return toMemberJson(updated);
        });
  }

  public ObjectNode enableMember(UUID accountId) {
    return store.tx(
        () -> {
          var m = store.member(accountId).orElseThrow(() -> OrgError.error("not_found", 404));
          OrgError.require("disabled".equals(m.status()), "conflict", 409);
          var updated =
              new OrgStore.MemberRow(
                  m.id(),
                  m.displayName(),
                  m.email(),
                  m.memberType(),
                  "active",
                  m.version() + 1,
                  m.createdAt(),
                  null,
                  m.offboardedAt());
          store.saveMember(updated);
          userEvents.enabled(accountId, actorId());
          return toMemberJson(updated);
        });
  }

  public ObjectNode offboardMember(UUID accountId, UUID handoverTo, String reason) {
    return store.tx(
        () -> {
          UUID actor = actorId();
          OrgError.require(actor == null || !actor.equals(accountId), "conflict", 409);
          var m = store.member(accountId).orElseThrow(() -> OrgError.error("not_found", 404));
          OrgError.require(!"offboarded".equals(m.status()), "conflict", 409);
          if (store.isOwner(accountId))
            OrgError.require(store.countOwners() > 1, "last_owner", 409);
          Instant now = clock.instant();
          var updated =
              new OrgStore.MemberRow(
                  m.id(),
                  m.displayName(),
                  m.email(),
                  m.memberType(),
                  "offboarded",
                  m.version() + 1,
                  m.createdAt(),
                  m.disabledAt() == null ? now : m.disabledAt(),
                  now);
          store.saveMember(updated);
          userEvents.offboarded(accountId, handoverTo, reason, actor);
          return toMemberJson(updated);
        });
  }

  public ObjectNode listMemberRoleBindings(UUID accountId) {
    return store.tx(
        () -> {
          store.member(accountId).orElseThrow(() -> OrgError.error("not_found", 404));
          List<ObjectNode> items = new ArrayList<>();
          store
              .jdbc()
              .query(
                  "SELECT b.id, b.role_id, r.key, b.subject_type, b.subject_id, b.granted_by, b.created_at"
                      + " FROM role_binding b JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
                      + " WHERE b.tenant_id=? AND b.subject_type='member' AND b.subject_id=?",
                  (rs, i) -> {
                    items.add(bindingJson(rs, "direct"));
                    return null;
                  },
                  store.tenantId(),
                  accountId);
          store
              .jdbc()
              .query(
                  "SELECT b.id, b.role_id, r.key, b.subject_type, b.subject_id, b.granted_by, b.created_at"
                      + " FROM group_member gm"
                      + " JOIN role_binding b ON b.tenant_id=gm.tenant_id AND b.subject_type='group'"
                      + " AND b.subject_id=gm.group_id"
                      + " JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
                      + " WHERE gm.tenant_id=? AND gm.account_id=?",
                  (rs, i) -> {
                    items.add(bindingJson(rs, "group"));
                    return null;
                  },
                  store.tenantId(),
                  accountId);
          return OrgJson.object("items", items);
        });
  }

  private ObjectNode bindingJson(java.sql.ResultSet rs, String via) throws java.sql.SQLException {
    ObjectNode n =
        OrgJson.object(
            "id",
            rs.getObject("id").toString(),
            "roleId",
            rs.getObject("role_id").toString(),
            "roleKey",
            rs.getString("key"),
            "subject",
            OrgJson.object(
                "type", rs.getString("subject_type"), "id", rs.getObject("subject_id").toString()),
            "createdAt",
            rs.getTimestamp("created_at").toInstant().toString());
    if (via != null) n.put("via", via);
    UUID gb = (UUID) rs.getObject("granted_by");
    if (gb != null) n.put("grantedBy", gb.toString());
    return n;
  }

  // ---- groups ----

  public ObjectNode listGroups(String q, UUID parentId) {
    return store.tx(
        () -> {
          StringBuilder sql =
              new StringBuilder(
                  "SELECT g.id, g.name, g.description, g.parent_id, g.source, g.created_at,"
                      + " (SELECT count(*) FROM group_member gm WHERE gm.tenant_id=g.tenant_id AND gm.group_id=g.id) AS member_count"
                      + " FROM \"group\" g WHERE g.tenant_id=?");
          List<Object> args = new ArrayList<>();
          args.add(store.tenantId());
          if (q != null && !q.isBlank()) {
            sql.append(" AND g.name ILIKE ?");
            args.add("%" + q.trim() + "%");
          }
          if (parentId != null) {
            sql.append(" AND g.parent_id=?");
            args.add(parentId);
          }
          sql.append(" ORDER BY g.name");
          List<ObjectNode> items =
              store.jdbc().query(sql.toString(), (rs, i) -> groupJson(rs), args.toArray());
          return OrgJson.object("items", items);
        });
  }

  private ObjectNode groupJson(java.sql.ResultSet rs) throws java.sql.SQLException {
    ObjectNode n =
        OrgJson.object(
            "id",
            rs.getObject("id").toString(),
            "name",
            rs.getString("name"),
            "memberCount",
            rs.getInt("member_count"),
            "source",
            rs.getString("source"),
            "createdAt",
            rs.getTimestamp("created_at").toInstant().toString());
    String desc = rs.getString("description");
    if (desc != null) n.put("description", desc);
    UUID parent = (UUID) rs.getObject("parent_id");
    if (parent != null) n.put("parentId", parent.toString());
    return n;
  }

  public ObjectNode createGroup(String name, String description, UUID parentId) {
    return store.tx(
        () -> {
          if (parentId != null) {
            ensureGrantAllowed(requireActor(), null, parentId);
            Integer exists =
                store
                    .jdbc()
                    .queryForObject(
                        "SELECT count(*) FROM \"group\" WHERE tenant_id=? AND id=?",
                        Integer.class,
                        store.tenantId(),
                        parentId);
            OrgError.require(exists != null && exists > 0, "not_found", 404);
          }
          UUID id = UuidV7.next();
          Instant now = clock.instant();
          store
              .jdbc()
              .update(
                  "INSERT INTO \"group\" (id, tenant_id, name, description, parent_id, source, created_at)"
                      + " VALUES (?,?,?,?,?,'manual',?)",
                  id,
                  store.tenantId(),
                  name,
                  description,
                  parentId,
                  java.sql.Timestamp.from(now));
          return getGroup(id);
        });
  }

  public ObjectNode getGroup(UUID groupId) {
    return store.tx(
        () -> {
          List<ObjectNode> rows =
              store
                  .jdbc()
                  .query(
                      "SELECT g.id, g.name, g.description, g.parent_id, g.source, g.created_at,"
                          + " (SELECT count(*) FROM group_member gm WHERE gm.tenant_id=g.tenant_id AND gm.group_id=g.id) AS member_count"
                          + " FROM \"group\" g WHERE g.tenant_id=? AND g.id=?",
                      (rs, i) -> groupJson(rs),
                      store.tenantId(),
                      groupId);
          OrgError.require(!rows.isEmpty(), "not_found", 404);
          return rows.getFirst();
        });
  }

  public ObjectNode updateGroup(UUID groupId, String name, String description, UUID parentId) {
    return store.tx(
        () -> {
          ObjectNode g = getGroup(groupId);
          if (parentId != null) {
            OrgError.require(!parentId.equals(groupId), "conflict", 409);
            ensureGrantAllowed(requireActor(), null, parentId);
          }
          ensureGrantAllowed(requireActor(), null, groupId);
          String newName = name != null ? name : g.path("name").asString();
          String newDesc =
              description != null
                  ? description
                  : (g.hasNonNull("description") ? g.path("description").asString() : null);
          UUID newParent =
              parentId != null
                  ? parentId
                  : (g.hasNonNull("parentId")
                      ? UUID.fromString(g.path("parentId").asString())
                      : null);
          store
              .jdbc()
              .update(
                  "UPDATE \"group\" SET name=?, description=?, parent_id=? WHERE tenant_id=? AND id=?",
                  newName,
                  newDesc,
                  newParent,
                  store.tenantId(),
                  groupId);
          return getGroup(groupId);
        });
  }

  public void deleteGroup(UUID groupId) {
    store.tx(
        () -> {
          getGroup(groupId);
          Integer children =
              store
                  .jdbc()
                  .queryForObject(
                      "SELECT count(*) FROM \"group\" WHERE tenant_id=? AND parent_id=?",
                      Integer.class,
                      store.tenantId(),
                      groupId);
          OrgError.require(children == null || children == 0, "group_not_empty", 409);
          List<UUID> members =
              store
                  .jdbc()
                  .query(
                      "SELECT account_id FROM group_member WHERE tenant_id=? AND group_id=?",
                      (rs, i) -> (UUID) rs.getObject(1),
                      store.tenantId(),
                      groupId);
          store
              .jdbc()
              .update(
                  "DELETE FROM role_binding WHERE tenant_id=? AND subject_type='group' AND subject_id=?",
                  store.tenantId(),
                  groupId);
          store
              .jdbc()
              .update(
                  "DELETE FROM quota WHERE tenant_id=? AND scope='group' AND scope_id=?",
                  store.tenantId(),
                  groupId);
          store
              .jdbc()
              .update(
                  "DELETE FROM group_member WHERE tenant_id=? AND group_id=?",
                  store.tenantId(),
                  groupId);
          store
              .jdbc()
              .update(
                  "DELETE FROM \"group\" WHERE tenant_id=? AND id=?", store.tenantId(), groupId);
          UUID actor = actorId();
          for (UUID m : members) {
            ObjectNode json = toMemberJson(store.member(m).orElseThrow());
            userEvents.updated(json, List.of("groupIds"), actor);
            userEvents.permissionsChanged(m, roleKeysOf(m), actor);
          }
          return null;
        });
  }

  public ObjectNode listGroupMembers(UUID groupId, int limit, String cursor) {
    getGroup(groupId);
    return listMembers(null, null, null, groupId, limit, cursor);
  }

  public void addGroupMember(UUID groupId, UUID accountId) {
    store.tx(
        () -> {
          getGroup(groupId);
          store.member(accountId).orElseThrow(() -> OrgError.error("not_found", 404));
          ensureGrantAllowed(requireActor(), null, groupId);
          List<String> before = roleKeysOf(accountId);
          store
              .jdbc()
              .update(
                  "INSERT INTO group_member (id, tenant_id, group_id, account_id, created_at)"
                      + " VALUES (?,?,?,?,?) ON CONFLICT (tenant_id, group_id, account_id) DO NOTHING",
                  UuidV7.next(),
                  store.tenantId(),
                  groupId,
                  accountId,
                  java.sql.Timestamp.from(clock.instant()));
          ObjectNode json = toMemberJson(store.member(accountId).orElseThrow());
          UUID actor = actorId();
          userEvents.updated(json, List.of("groupIds"), actor);
          List<String> after = roleKeysOf(accountId);
          if (!before.equals(after)) userEvents.permissionsChanged(accountId, after, actor);
          return null;
        });
  }

  public void removeGroupMember(UUID groupId, UUID accountId) {
    store.tx(
        () -> {
          getGroup(groupId);
          List<String> before = roleKeysOf(accountId);
          int n =
              store
                  .jdbc()
                  .update(
                      "DELETE FROM group_member WHERE tenant_id=? AND group_id=? AND account_id=?",
                      store.tenantId(),
                      groupId,
                      accountId);
          OrgError.require(n > 0, "not_found", 404);
          UUID actor = actorId();
          ObjectNode json = toMemberJson(store.member(accountId).orElseThrow());
          userEvents.updated(json, List.of("groupIds"), actor);
          List<String> after = roleKeysOf(accountId);
          if (!before.equals(after)) userEvents.permissionsChanged(accountId, after, actor);
          return null;
        });
  }

  // ---- roles ----

  public ObjectNode listPermissions() {
    return store.tx(
        () ->
            OrgJson.object(
                "items",
                store
                    .jdbc()
                    .query(
                        "SELECT key, service, description_en, description_zh, sensitive FROM permission"
                            + " WHERE tenant_id=? ORDER BY key",
                        (rs, i) ->
                            OrgJson.object(
                                "key",
                                rs.getString("key"),
                                "service",
                                rs.getString("service"),
                                "description",
                                OrgJson.object(
                                    "en",
                                    rs.getString("description_en"),
                                    "zh",
                                    rs.getString("description_zh")),
                                "sensitive",
                                rs.getBoolean("sensitive")),
                        store.tenantId())));
  }

  public ObjectNode listRoles() {
    return store.tx(
        () ->
            OrgJson.object(
                "items",
                store
                    .jdbc()
                    .query(
                        "SELECT r.id, r.key, r.name_en, r.name_zh, r.description_en, r.description_zh,"
                            + " r.builtin, r.permissions,"
                            + " (SELECT count(*) FROM role_binding b WHERE b.tenant_id=r.tenant_id AND b.role_id=r.id) AS binding_count"
                            + " FROM role r WHERE r.tenant_id=? ORDER BY r.builtin DESC, r.key",
                        (rs, i) -> roleJson(rs),
                        store.tenantId())));
  }

  private ObjectNode roleJson(java.sql.ResultSet rs) throws java.sql.SQLException {
    ObjectNode n =
        OrgJson.object(
            "id",
            rs.getObject("id").toString(),
            "key",
            rs.getString("key"),
            "name",
            OrgJson.object("en", rs.getString("name_en"), "zh", rs.getString("name_zh")),
            "builtin",
            rs.getBoolean("builtin"),
            "permissions",
            List.of(OrgStore.stringArray(rs, "permissions")),
            "bindingCount",
            rs.getInt("binding_count"));
    String de = rs.getString("description_en");
    String dz = rs.getString("description_zh");
    if (de != null || dz != null)
      n.set("description", OrgJson.object("en", de == null ? "" : de, "zh", dz == null ? "" : dz));
    return n;
  }

  public ObjectNode getRole(UUID roleId) {
    return store.tx(
        () -> {
          List<ObjectNode> rows =
              store
                  .jdbc()
                  .query(
                      "SELECT r.id, r.key, r.name_en, r.name_zh, r.description_en, r.description_zh,"
                          + " r.builtin, r.permissions,"
                          + " (SELECT count(*) FROM role_binding b WHERE b.tenant_id=r.tenant_id AND b.role_id=r.id) AS binding_count"
                          + " FROM role r WHERE r.tenant_id=? AND r.id=?",
                      (rs, i) -> roleJson(rs),
                      store.tenantId(),
                      roleId);
          OrgError.require(!rows.isEmpty(), "not_found", 404);
          return rows.getFirst();
        });
  }

  public ObjectNode createRole(
      String key, JsonNode name, JsonNode description, List<String> permissions) {
    return store.tx(
        () -> {
          Set<String> grantor =
              new LinkedHashSet<>(getEffectivePermissions(requireActor()).permissions());
          List<String> exceeding =
              permissions.stream().filter(p -> !grantor.contains(p)).sorted().toList();
          OrgError.require(exceeding.isEmpty(), "grant_exceeds_own", 403);
          UUID id = UuidV7.next();
          OrgArrays.update(
              store.jdbc(),
              "INSERT INTO role (id, tenant_id, key, name_en, name_zh, description_en, description_zh,"
                  + " builtin, permissions, created_at) VALUES (?,?,?,?,?,?,?,false,?,?)",
              id,
              store.tenantId(),
              key,
              text(name, "en"),
              text(name, "zh"),
              description == null ? null : text(description, "en"),
              description == null ? null : text(description, "zh"),
              permissions.toArray(String[]::new),
              java.sql.Timestamp.from(clock.instant()));
          return getRole(id);
        });
  }

  public ObjectNode updateRole(
      UUID roleId, JsonNode name, JsonNode description, List<String> permissions) {
    return store.tx(
        () -> {
          ObjectNode role = getRole(roleId);
          OrgError.require(!role.path("builtin").asBoolean(), "role_builtin", 409);
          if (permissions != null) {
            Set<String> grantor =
                new LinkedHashSet<>(getEffectivePermissions(requireActor()).permissions());
            List<String> exceeding =
                permissions.stream().filter(p -> !grantor.contains(p)).sorted().toList();
            OrgError.require(exceeding.isEmpty(), "grant_exceeds_own", 403);
          }
          List<String> perms =
              permissions != null
                  ? permissions
                  : OrgJson.MAPPER.convertValue(role.path("permissions"), List.class);
          OrgArrays.update(
              store.jdbc(),
              "UPDATE role SET name_en=COALESCE(?, name_en), name_zh=COALESCE(?, name_zh),"
                  + " description_en=COALESCE(?, description_en), description_zh=COALESCE(?, description_zh),"
                  + " permissions=? WHERE tenant_id=? AND id=?",
              name == null ? null : text(name, "en"),
              name == null ? null : text(name, "zh"),
              description == null ? null : text(description, "en"),
              description == null ? null : text(description, "zh"),
              perms.toArray(String[]::new),
              store.tenantId(),
              roleId);
          List<UUID> affected =
              store
                  .jdbc()
                  .query(
                      "SELECT DISTINCT CASE WHEN subject_type='member' THEN subject_id ELSE gm.account_id END"
                          + " FROM role_binding b"
                          + " LEFT JOIN group_member gm ON b.subject_type='group' AND gm.group_id=b.subject_id"
                          + " AND gm.tenant_id=b.tenant_id"
                          + " WHERE b.tenant_id=? AND b.role_id=?",
                      (rs, i) -> (UUID) rs.getObject(1),
                      store.tenantId(),
                      roleId);
          UUID actor = actorId();
          for (UUID a : affected) {
            if (a != null) userEvents.permissionsChanged(a, roleKeysOf(a), actor);
          }
          return getRole(roleId);
        });
  }

  public void deleteRole(UUID roleId) {
    store.tx(
        () -> {
          ObjectNode role = getRole(roleId);
          OrgError.require(!role.path("builtin").asBoolean(), "role_builtin", 409);
          List<UUID> affected =
              store
                  .jdbc()
                  .query(
                      "SELECT DISTINCT CASE WHEN subject_type='member' THEN subject_id ELSE gm.account_id END"
                          + " FROM role_binding b"
                          + " LEFT JOIN group_member gm ON b.subject_type='group' AND gm.group_id=b.subject_id"
                          + " AND gm.tenant_id=b.tenant_id"
                          + " WHERE b.tenant_id=? AND b.role_id=?",
                      (rs, i) -> (UUID) rs.getObject(1),
                      store.tenantId(),
                      roleId);
          store
              .jdbc()
              .update(
                  "DELETE FROM role_binding WHERE tenant_id=? AND role_id=?",
                  store.tenantId(),
                  roleId);
          store
              .jdbc()
              .update("DELETE FROM role WHERE tenant_id=? AND id=?", store.tenantId(), roleId);
          UUID actor = actorId();
          for (UUID a : affected) {
            if (a != null) userEvents.permissionsChanged(a, roleKeysOf(a), actor);
          }
          return null;
        });
  }

  public ObjectNode listRoleBindings(UUID roleId, int limit, String cursor) {
    return store.tx(
        () -> {
          int lim = Math.min(Math.max(limit <= 0 ? 50 : limit, 1), 200);
          StringBuilder sql =
              new StringBuilder(
                  "SELECT b.id, b.role_id, r.key, b.subject_type, b.subject_id, b.granted_by, b.created_at"
                      + " FROM role_binding b JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
                      + " WHERE b.tenant_id=?");
          List<Object> args = new ArrayList<>();
          args.add(store.tenantId());
          if (roleId != null) {
            sql.append(" AND b.role_id=?");
            args.add(roleId);
          }
          Cursor c = decodeCursor(cursor);
          if (c != null) {
            sql.append(" AND (b.created_at, b.id) > (?, ?)");
            args.add(java.sql.Timestamp.from(c.at()));
            args.add(c.id());
          }
          sql.append(" ORDER BY b.created_at, b.id LIMIT ?");
          args.add(lim + 1);
          List<ObjectNode> items = new ArrayList<>();
          store
              .jdbc()
              .query(
                  sql.toString(),
                  (rs, i) -> {
                    items.add(bindingJson(rs, null));
                    return null;
                  },
                  args.toArray());
          String next = null;
          List<ObjectNode> page = items;
          if (items.size() > lim) {
            var last = items.get(lim - 1);
            next =
                encodeCursor(
                    Instant.parse(last.path("createdAt").asString()),
                    UUID.fromString(last.path("id").asString()));
            page = items.subList(0, lim);
          }
          ObjectNode out = OrgJson.object("items", page);
          if (next != null) out.put("nextCursor", next);
          return out;
        });
  }

  public ObjectNode createRoleBinding(UUID roleId, String subjectType, UUID subjectId) {
    return store.tx(
        () -> {
          getRole(roleId);
          ensureGrantAllowed(requireActor(), List.of(roleId), null);
          if ("group".equals(subjectType)) ensureGrantAllowed(requireActor(), null, subjectId);
          UUID id = UuidV7.next();
          UUID actor = requireActor();
          Instant now = clock.instant();
          store
              .jdbc()
              .update(
                  "INSERT INTO role_binding (id, tenant_id, role_id, subject_type, subject_id, granted_by, created_at)"
                      + " VALUES (?,?,?,?,?,?,?)",
                  id,
                  store.tenantId(),
                  roleId,
                  subjectType,
                  subjectId,
                  actor,
                  java.sql.Timestamp.from(now));
          if ("member".equals(subjectType))
            userEvents.permissionsChanged(subjectId, roleKeysOf(subjectId), actor);
          else if ("group".equals(subjectType)) {
            List<UUID> members =
                store
                    .jdbc()
                    .query(
                        "SELECT account_id FROM group_member WHERE tenant_id=? AND group_id=?",
                        (rs, i) -> (UUID) rs.getObject(1),
                        store.tenantId(),
                        subjectId);
            for (UUID m : members) userEvents.permissionsChanged(m, roleKeysOf(m), actor);
          }
          List<ObjectNode> rows =
              store
                  .jdbc()
                  .query(
                      "SELECT b.id, b.role_id, r.key, b.subject_type, b.subject_id, b.granted_by, b.created_at"
                          + " FROM role_binding b JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
                          + " WHERE b.tenant_id=? AND b.id=?",
                      (rs, i) -> bindingJson(rs, null),
                      store.tenantId(),
                      id);
          return rows.getFirst();
        });
  }

  public void deleteRoleBinding(UUID bindingId) {
    store.tx(
        () -> {
          List<Map<String, Object>> rows =
              store
                  .jdbc()
                  .queryForList(
                      "SELECT b.role_id, b.subject_type, b.subject_id, r.key FROM role_binding b"
                          + " JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
                          + " WHERE b.tenant_id=? AND b.id=?",
                      store.tenantId(),
                      bindingId);
          OrgError.require(!rows.isEmpty(), "not_found", 404);
          var row = rows.getFirst();
          if ("owner".equals(row.get("key")) && "member".equals(row.get("subject_type"))) {
            UUID subject = (UUID) row.get("subject_id");
            if (store.isOwner(subject))
              OrgError.require(store.countOwners() > 1, "last_owner", 409);
          }
          store
              .jdbc()
              .update(
                  "DELETE FROM role_binding WHERE tenant_id=? AND id=?",
                  store.tenantId(),
                  bindingId);
          UUID actor = actorId();
          String st = (String) row.get("subject_type");
          UUID sid = (UUID) row.get("subject_id");
          if ("member".equals(st)) userEvents.permissionsChanged(sid, roleKeysOf(sid), actor);
          else if ("group".equals(st)) {
            List<UUID> members =
                store
                    .jdbc()
                    .query(
                        "SELECT account_id FROM group_member WHERE tenant_id=? AND group_id=?",
                        (rs, i) -> (UUID) rs.getObject(1),
                        store.tenantId(),
                        sid);
            for (UUID m : members) userEvents.permissionsChanged(m, roleKeysOf(m), actor);
          }
          return null;
        });
  }

  // ---- policies ----

  public ObjectNode getPolicy(String key) {
    return store.tx(
        () -> {
          List<ObjectNode> rows =
              store
                  .jdbc()
                  .query(
                      "SELECT key, version, value::text, updated_at, updated_by FROM policy"
                          + " WHERE tenant_id=? AND key=?",
                      (rs, i) -> {
                        ObjectNode n =
                            OrgJson.object(
                                "key",
                                rs.getString("key"),
                                "version",
                                rs.getLong("version"),
                                "value",
                                OrgJson.read(rs.getString("value")),
                                "updatedAt",
                                rs.getTimestamp("updated_at").toInstant().toString());
                        UUID by = (UUID) rs.getObject("updated_by");
                        if (by != null) n.put("updatedBy", by.toString());
                        return n;
                      },
                      store.tenantId(),
                      key);
          OrgError.require(!rows.isEmpty(), "not_found", 404);
          return rows.getFirst();
        });
  }

  public ObjectNode putPolicy(String key, JsonNode value, Long ifMatch) {
    return store.tx(
        () -> {
          OrgError.require(Set.of("workspace", "egress", "login").contains(key), "conflict", 409);
          ObjectNode existing = getPolicy(key);
          if (ifMatch != null)
            OrgError.require(
                existing.path("version").asLong() == ifMatch, "precondition_failed", 412);
          long version = existing.path("version").asLong() + 1;
          store
              .jdbc()
              .update(
                  "UPDATE policy SET version=?, value=?::jsonb, updated_at=?, updated_by=?"
                      + " WHERE tenant_id=? AND key=?",
                  version,
                  value.toString(),
                  java.sql.Timestamp.from(clock.instant()),
                  actorId(),
                  store.tenantId(),
                  key);
          return getPolicy(key);
        });
  }

  // ---- quotas ----

  public ObjectNode getQuota(String scope, UUID scopeId) {
    return store.tx(
        () -> {
          List<String> rows =
              scopeId == null
                  ? store
                      .jdbc()
                      .query(
                          "SELECT value::text FROM quota WHERE tenant_id=? AND scope=? AND scope_id IS NULL",
                          (rs, i) -> rs.getString(1),
                          store.tenantId(),
                          scope)
                  : store
                      .jdbc()
                      .query(
                          "SELECT value::text FROM quota WHERE tenant_id=? AND scope=? AND scope_id=?",
                          (rs, i) -> rs.getString(1),
                          store.tenantId(),
                          scope,
                          scopeId);
          if (rows.isEmpty()) {
            if ("default".equals(scope)) return OrgJson.object();
            throw OrgError.error("not_found", 404);
          }
          return OrgJson.read(rows.getFirst());
        });
  }

  public ObjectNode putQuota(String scope, UUID scopeId, JsonNode value) {
    return store.tx(
        () -> {
          int updated =
              scopeId == null
                  ? store
                      .jdbc()
                      .update(
                          "UPDATE quota SET value=?::jsonb, updated_at=? WHERE tenant_id=? AND scope=? AND scope_id IS NULL",
                          value.toString(),
                          java.sql.Timestamp.from(clock.instant()),
                          store.tenantId(),
                          scope)
                  : store
                      .jdbc()
                      .update(
                          "UPDATE quota SET value=?::jsonb, updated_at=? WHERE tenant_id=? AND scope=? AND scope_id=?",
                          value.toString(),
                          java.sql.Timestamp.from(clock.instant()),
                          store.tenantId(),
                          scope,
                          scopeId);
          if (updated == 0) {
            store
                .jdbc()
                .update(
                    "INSERT INTO quota (id, tenant_id, scope, scope_id, value, updated_at) VALUES (?,?,?,?,?::jsonb,?)",
                    UuidV7.next(),
                    store.tenantId(),
                    scope,
                    scopeId,
                    value.toString(),
                    java.sql.Timestamp.from(clock.instant()));
          }
          return getQuota(scope, scopeId);
        });
  }

  public void deleteQuota(String scope, UUID scopeId) {
    store.tx(
        () -> {
          OrgError.require(!"default".equals(scope), "conflict", 409);
          int n =
              store
                  .jdbc()
                  .update(
                      "DELETE FROM quota WHERE tenant_id=? AND scope=? AND scope_id=?",
                      store.tenantId(),
                      scope,
                      scopeId);
          OrgError.require(n > 0, "not_found", 404);
          return null;
        });
  }

  public ObjectNode getEffectiveQuota(UUID accountId) {
    return store.tx(
        () -> {
          store.member(accountId).orElseThrow(() -> OrgError.error("not_found", 404));
          ObjectNode result = OrgJson.object();
          ObjectNode def = softQuota("default", null);
          mergeQuota(result, def);
          for (UUID g : store.groupIdsOf(accountId)) {
            ObjectNode gq = softQuota("group", g);
            if (gq != null) mergeQuotaLoosest(result, gq);
          }
          ObjectNode mq = softQuota("member", accountId);
          if (mq != null) mergeQuota(result, mq); // member override wins field-by-field when set
          // Re-apply correctly: member → groups → default per OpenAPI
          result = OrgJson.object();
          ObjectNode memberQ = softQuota("member", accountId);
          ObjectNode groupsMerged = OrgJson.object();
          boolean anyGroup = false;
          for (UUID g : store.groupIdsOf(accountId)) {
            ObjectNode gq = softQuota("group", g);
            if (gq != null) {
              anyGroup = true;
              mergeQuotaLoosest(groupsMerged, gq);
            }
          }
          ObjectNode defaultQ = softQuota("default", null);
          mergeLayer(result, memberQ);
          if (anyGroup) mergeLayer(result, groupsMerged);
          mergeLayer(result, defaultQ);
          return result;
        });
  }

  private ObjectNode softQuota(String scope, UUID scopeId) {
    try {
      return getQuota(scope, scopeId);
    } catch (RuntimeException e) {
      return null;
    }
  }

  /** Fill only unset fields from layer. */
  private void mergeLayer(ObjectNode target, ObjectNode layer) {
    if (layer == null) return;
    mergeSection(target, layer, "instance");
    mergeSection(target, layer, "model");
  }

  private void mergeSection(ObjectNode target, ObjectNode layer, String section) {
    if (!layer.has(section) || layer.path(section).isNull()) return;
    ObjectNode src = (ObjectNode) layer.path(section);
    ObjectNode dst =
        target.has(section) && target.path(section).isObject()
            ? (ObjectNode) target.path(section)
            : target.putObject(section);
    src.properties()
        .forEach(
            e -> {
              if (!dst.has(e.getKey())) dst.set(e.getKey(), e.getValue());
            });
  }

  private void mergeQuota(ObjectNode target, ObjectNode layer) {
    mergeLayer(target, layer);
  }

  /** For multiple groups: take loosest (max numeric, true for bool, null unlimited wins). */
  private void mergeQuotaLoosest(ObjectNode target, ObjectNode layer) {
    if (layer == null) return;
    for (String section : List.of("instance", "model")) {
      if (!layer.has(section)) continue;
      ObjectNode src = (ObjectNode) layer.path(section);
      ObjectNode dst =
          target.has(section) && target.path(section).isObject()
              ? (ObjectNode) target.path(section)
              : target.putObject(section);
      src.properties()
          .forEach(
              e -> {
                String k = e.getKey();
                JsonNode v = e.getValue();
                if (!dst.has(k)) {
                  dst.set(k, v);
                  return;
                }
                JsonNode cur = dst.get(k);
                if (cur.isNull() || v.isNull()) {
                  dst.putNull(k);
                  return;
                }
                if (v.isBoolean()) dst.put(k, cur.asBoolean() || v.asBoolean());
                else if (v.isNumber()) dst.put(k, Math.max(cur.asLong(), v.asLong()));
                else dst.set(k, v);
              });
    }
  }

  // ---- invitations ----

  public ObjectNode createInvitation(
      String email,
      String displayName,
      String memberType,
      List<UUID> roleIds,
      List<UUID> groupIds,
      List<UUID> providerIds,
      Instant expiresAt,
      int maxUses) {
    return store.tx(
        () -> {
          List<UUID> roles = roleIds == null ? List.of() : roleIds;
          List<UUID> groups = groupIds == null ? List.of() : groupIds;
          ensureGrantAllowed(requireActor(), roles, null);
          for (UUID g : groups) ensureGrantAllowed(requireActor(), null, g);
          String code = newCode();
          UUID id = UuidV7.next();
          Instant exp = expiresAt != null ? expiresAt : clock.instant().plusSeconds(7 * 24 * 3600);
          Instant now = clock.instant();
          UUID actor = requireActor();
          OrgArrays.update(
              store.jdbc(),
              "INSERT INTO invitation (id, tenant_id, code_hash, status, email, display_name,"
                  + " member_type, role_ids, group_ids, provider_ids, max_uses, uses, expires_at,"
                  + " created_by, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,0,?,?,?)",
              id,
              store.tenantId(),
              hashCode(code),
              "active",
              email,
              displayName,
              memberType,
              roles.toArray(UUID[]::new),
              groups.toArray(UUID[]::new),
              (providerIds == null ? List.<UUID>of() : providerIds).toArray(UUID[]::new),
              maxUses <= 0 ? 1 : maxUses,
              java.sql.Timestamp.from(exp),
              actor,
              java.sql.Timestamp.from(now));
          ObjectNode inv = invitationJson(id);
          inv.put("code", code);
          inv.put("url", "https://portal.example.com/invite/" + code);
          return inv;
        });
  }

  public ObjectNode listInvitations(String status, int limit, String cursor) {
    return store.tx(
        () -> {
          refreshInvitationStatuses();
          int lim = Math.min(Math.max(limit <= 0 ? 50 : limit, 1), 200);
          StringBuilder sql = new StringBuilder("SELECT id FROM invitation WHERE tenant_id=?");
          List<Object> args = new ArrayList<>();
          args.add(store.tenantId());
          if (status != null && !status.isBlank()) {
            sql.append(" AND status=?");
            args.add(status);
          }
          Cursor c = decodeCursor(cursor);
          if (c != null) {
            sql.append(" AND (created_at, id) > (?, ?)");
            args.add(java.sql.Timestamp.from(c.at()));
            args.add(c.id());
          }
          sql.append(" ORDER BY created_at, id LIMIT ?");
          args.add(lim + 1);
          List<UUID> ids =
              store.jdbc().query(sql.toString(), (rs, i) -> (UUID) rs.getObject(1), args.toArray());
          String next = null;
          if (ids.size() > lim) {
            UUID last = ids.get(lim - 1);
            Instant at =
                store
                    .jdbc()
                    .queryForObject(
                        "SELECT created_at FROM invitation WHERE tenant_id=? AND id=?",
                        (rs, i) -> rs.getTimestamp(1).toInstant(),
                        store.tenantId(),
                        last);
            next = encodeCursor(at, last);
            ids = ids.subList(0, lim);
          }
          ObjectNode out = OrgJson.object("items", ids.stream().map(this::invitationJson).toList());
          if (next != null) out.put("nextCursor", next);
          return out;
        });
  }

  public void revokeInvitation(UUID invitationId) {
    store.tx(
        () -> {
          int n =
              store
                  .jdbc()
                  .update(
                      "UPDATE invitation SET status='revoked' WHERE tenant_id=? AND id=? AND status='active'",
                      store.tenantId(),
                      invitationId);
          OrgError.require(n > 0, "not_found", 404);
          return null;
        });
  }

  public record ResolvedInvitation(ObjectNode invitation, String reason) {}

  public ResolvedInvitation resolveInvitation(String code) {
    // May be called without tenant (gRPC); try hash lookup via system-less path using default
    // tenant
    // after resolving — invitations are tenant-scoped, so search default tenant first then require
    // tenant context when available.
    UUID tenant =
        TenantContext.current()
            .orElseGet(
                () ->
                    store
                        .defaultTenant()
                        .map(OrgStore.TenantRow::id)
                        .orElse(OrgIds.DEFAULT_TENANT));
    return TenantContext.call(
        tenant,
        () ->
            store.tx(
                () -> {
                  refreshInvitationStatuses();
                  List<Map<String, Object>> rows =
                      store
                          .jdbc()
                          .queryForList(
                              "SELECT id, status FROM invitation WHERE tenant_id=? AND code_hash=?",
                              store.tenantId(),
                              hashCode(code));
                  if (rows.isEmpty()) return new ResolvedInvitation(null, "not_found");
                  String status = (String) rows.getFirst().get("status");
                  if (!"active".equals(status))
                    return new ResolvedInvitation(null, status.equals("used") ? "used" : status);
                  return new ResolvedInvitation(
                      invitationJson((UUID) rows.getFirst().get("id")), null);
                }));
  }

  public ObjectNode redeemInvitation(
      String code, UUID accountId, String displayName, String email, UUID providerId) {
    UUID tenant =
        TenantContext.current()
            .orElseGet(
                () ->
                    store
                        .defaultTenant()
                        .map(OrgStore.TenantRow::id)
                        .orElse(OrgIds.DEFAULT_TENANT));
    return TenantContext.call(
        tenant,
        () ->
            store.tx(
                () -> {
                  refreshInvitationStatuses();
                  List<Map<String, Object>> rows =
                      store
                          .jdbc()
                          .queryForList(
                              "SELECT * FROM invitation WHERE tenant_id=? AND code_hash=? FOR UPDATE",
                              store.tenantId(),
                              hashCode(code));
                  OrgError.require(!rows.isEmpty(), "not_found", 404);
                  var inv = rows.getFirst();
                  UUID invitationId = (UUID) inv.get("id");
                  Integer existing =
                      store
                          .jdbc()
                          .queryForObject(
                              "SELECT count(*) FROM invitation_redemption WHERE tenant_id=? AND invitation_id=? AND account_id=?",
                              Integer.class,
                              store.tenantId(),
                              invitationId,
                              accountId);
                  if (existing != null && existing > 0) {
                    return toMemberJson(
                        store
                            .member(accountId)
                            .orElseThrow(() -> OrgError.error("not_found", 404)));
                  }
                  OrgError.require("active".equals(inv.get("status")), "conflict", 409);
                  Instant exp = ((java.sql.Timestamp) inv.get("expires_at")).toInstant();
                  OrgError.require(!exp.isBefore(clock.instant()), "conflict", 409);
                  UUID[] providers;
                  UUID[] roleIds;
                  UUID[] groupIds;
                  try {
                    providers = OrgStore.asUuidArray(inv.get("provider_ids"));
                    roleIds = OrgStore.asUuidArray(inv.get("role_ids"));
                    groupIds = OrgStore.asUuidArray(inv.get("group_ids"));
                  } catch (java.sql.SQLException e) {
                    throw new IllegalStateException(e);
                  }
                  if (providers.length > 0 && providerId != null) {
                    boolean ok = false;
                    for (UUID p : providers) if (p.equals(providerId)) ok = true;
                    OrgError.require(ok, "conflict", 409);
                  }
                  String invEmail = (String) inv.get("email");
                  if (invEmail != null && !invEmail.isBlank() && email != null && !email.isBlank())
                    OrgError.require(invEmail.equalsIgnoreCase(email), "conflict", 409);

                  boolean created = store.member(accountId).isEmpty();
                  if (created) {
                    String dn =
                        displayName != null && !displayName.isBlank()
                            ? displayName
                            : (inv.get("display_name") != null
                                ? (String) inv.get("display_name")
                                : "member");
                    String em = email != null ? email : (String) inv.get("email");
                    store.insertMember(
                        new OrgStore.MemberRow(
                            accountId,
                            dn,
                            em,
                            (String) inv.get("member_type"),
                            "active",
                            1,
                            clock.instant(),
                            null,
                            null));
                  }

                  for (UUID roleId : roleIds) {
                    store
                        .jdbc()
                        .update(
                            "INSERT INTO role_binding (id, tenant_id, role_id, subject_type, subject_id, granted_by, created_at)"
                                + " VALUES (?,?,?,'member',?,?,?) ON CONFLICT DO NOTHING",
                            UuidV7.next(),
                            store.tenantId(),
                            roleId,
                            accountId,
                            inv.get("created_by"),
                            java.sql.Timestamp.from(clock.instant()));
                  }
                  for (UUID groupId : groupIds) {
                    store
                        .jdbc()
                        .update(
                            "INSERT INTO group_member (id, tenant_id, group_id, account_id, created_at)"
                                + " VALUES (?,?,?,?,?) ON CONFLICT DO NOTHING",
                            UuidV7.next(),
                            store.tenantId(),
                            groupId,
                            accountId,
                            java.sql.Timestamp.from(clock.instant()));
                  }

                  store
                      .jdbc()
                      .update(
                          "INSERT INTO invitation_redemption (id, tenant_id, invitation_id, account_id, created_at)"
                              + " VALUES (?,?,?,?,?)",
                          UuidV7.next(),
                          store.tenantId(),
                          invitationId,
                          accountId,
                          java.sql.Timestamp.from(clock.instant()));
                  int uses = ((Number) inv.get("uses")).intValue() + 1;
                  int maxUses = ((Number) inv.get("max_uses")).intValue();
                  store
                      .jdbc()
                      .update(
                          "UPDATE invitation SET uses=?, status=? WHERE tenant_id=? AND id=?",
                          uses,
                          uses >= maxUses ? "used" : "active",
                          store.tenantId(),
                          invitationId);

                  ObjectNode member = toMemberJson(store.member(accountId).orElseThrow());
                  if (created)
                    userEvents.created(
                        member, "invitation", invitationId, (UUID) inv.get("created_by"));
                  return member;
                }));
  }

  public ObjectNode previewInvitation(String code) {
    ResolvedInvitation r = resolveInvitation(code);
    if (r.invitation() == null) return OrgJson.object("valid", false, "reason", r.reason());
    ObjectNode inv = r.invitation();
    OrgStore.TenantRow tenant = resolveTenantByHost(""); // default
    UUID tenantId =
        TenantContext.current()
            .orElseGet(
                () ->
                    store
                        .defaultTenant()
                        .map(OrgStore.TenantRow::id)
                        .orElse(OrgIds.DEFAULT_TENANT));
    return TenantContext.call(
        tenantId,
        () -> {
          OrgStore.TenantRow t = store.defaultTenant().orElse(tenant);
          ObjectNode out =
              OrgJson.object(
                  "valid",
                  true,
                  "tenantName",
                  t.displayName(),
                  "memberType",
                  inv.path("memberType").asString(),
                  "providerIds",
                  OrgJson.MAPPER.convertValue(inv.path("providerIds"), List.class),
                  "expiresAt",
                  inv.path("expiresAt").asString());
          if (inv.hasNonNull("email")) {
            String email = inv.path("email").asString();
            out.put("email", maskEmail(email));
          }
          UUID createdBy = UUID.fromString(inv.path("createdBy").asString());
          store.member(createdBy).ifPresent(m -> out.put("inviterName", m.displayName()));
          return out;
        });
  }

  private ObjectNode invitationJson(UUID id) {
    List<ObjectNode> rows =
        store
            .jdbc()
            .query(
                "SELECT id, status, email, display_name, member_type, role_ids, group_ids, provider_ids,"
                    + " max_uses, uses, expires_at, created_by, created_at FROM invitation"
                    + " WHERE tenant_id=? AND id=?",
                (rs, i) -> {
                  ObjectNode n =
                      OrgJson.object(
                          "id",
                          rs.getObject("id").toString(),
                          "status",
                          rs.getString("status"),
                          "memberType",
                          rs.getString("member_type"),
                          "roleIds",
                          java.util.Arrays.stream(OrgStore.uuidArray(rs, "role_ids"))
                              .map(UUID::toString)
                              .toList(),
                          "groupIds",
                          java.util.Arrays.stream(OrgStore.uuidArray(rs, "group_ids"))
                              .map(UUID::toString)
                              .toList(),
                          "providerIds",
                          java.util.Arrays.stream(OrgStore.uuidArray(rs, "provider_ids"))
                              .map(UUID::toString)
                              .toList(),
                          "maxUses",
                          rs.getInt("max_uses"),
                          "uses",
                          rs.getInt("uses"),
                          "expiresAt",
                          rs.getTimestamp("expires_at").toInstant().toString(),
                          "createdBy",
                          rs.getObject("created_by").toString(),
                          "createdAt",
                          rs.getTimestamp("created_at").toInstant().toString());
                  String email = rs.getString("email");
                  if (email != null) n.put("email", email);
                  String dn = rs.getString("display_name");
                  if (dn != null) n.put("displayName", dn);
                  return n;
                },
                store.tenantId(),
                id);
    OrgError.require(!rows.isEmpty(), "not_found", 404);
    return rows.getFirst();
  }

  private void refreshInvitationStatuses() {
    store
        .jdbc()
        .update(
            "UPDATE invitation SET status='expired' WHERE tenant_id=? AND status='active' AND expires_at < ?",
            store.tenantId(),
            java.sql.Timestamp.from(clock.instant()));
    store
        .jdbc()
        .update(
            "UPDATE invitation SET status='used' WHERE tenant_id=? AND status='active' AND uses >= max_uses",
            store.tenantId());
  }

  // ---- me ----

  public ObjectNode me() {
    return store.tx(
        () -> {
          UUID account = requireActor();
          ObjectNode member = memberJson(account);
          OrgStore.TenantRow tenant = getTenant();
          Effective eff = getEffectivePermissions(account);
          return OrgJson.object(
              "member", member, "tenant", tenantJson(tenant), "permissions", eff.permissions());
        });
  }

  public ObjectNode myQuota() {
    return getEffectiveQuota(requireActor());
  }

  // ---- helpers ----

  private UUID actorId() {
    return AuthorizationRules.currentPrincipal()
        .map(
            p -> {
              try {
                return UUID.fromString(p.subject());
              } catch (Exception e) {
                return null;
              }
            })
        .orElse(null);
  }

  private UUID requireActor() {
    UUID id = actorId();
    OrgError.require(id != null, "not_found", 404);
    return id;
  }

  private static String text(JsonNode n, String field) {
    return n == null ? null : n.path(field).asString("");
  }

  private String newCode() {
    byte[] buf = new byte[24];
    random.nextBytes(buf);
    return Base64.getUrlEncoder().withoutPadding().encodeToString(buf);
  }

  static String hashCode(String code) {
    try {
      return HexFormat.of()
          .formatHex(
              MessageDigest.getInstance("SHA-256").digest(code.getBytes(StandardCharsets.UTF_8)));
    } catch (Exception e) {
      throw new IllegalStateException(e);
    }
  }

  private static String maskEmail(String email) {
    int at = email.indexOf('@');
    if (at <= 1) return "***";
    return email.charAt(0) + "***" + email.substring(at);
  }

  private record Cursor(Instant at, UUID id) {}

  private static String encodeCursor(Instant at, UUID id) {
    return Base64.getUrlEncoder()
        .withoutPadding()
        .encodeToString((at.toString() + "|" + id).getBytes(StandardCharsets.UTF_8));
  }

  private static Cursor decodeCursor(String cursor) {
    if (cursor == null || cursor.isBlank()) return null;
    try {
      String raw = new String(Base64.getUrlDecoder().decode(cursor), StandardCharsets.UTF_8);
      int i = raw.lastIndexOf('|');
      return new Cursor(Instant.parse(raw.substring(0, i)), UUID.fromString(raw.substring(i + 1)));
    } catch (Exception e) {
      throw OrgError.error("conflict", 409);
    }
  }
}
