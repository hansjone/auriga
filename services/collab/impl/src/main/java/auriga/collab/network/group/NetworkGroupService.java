package auriga.collab.network.group;

import static auriga.collab.network.group.GroupRules.*;

import auriga.collab.api.network.group.*;
import auriga.collab.api.network.group.NetworkGroups.*;
import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import java.nio.charset.StandardCharsets;
import java.sql.Timestamp;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.json.JsonMapper;

/**
 * Independent transaction-owned authority; production adapters are mandatory constructor inputs.
 */
public final class NetworkGroupService implements NetworkGroups {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final TransactionTemplate denialTx;
  private final NetworkGroupAuthority authority;
  private final NetworkGroupMailbox mailbox;
  private final Clock clock;

  public NetworkGroupService(
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkGroupAuthority authority,
      NetworkGroupMailbox mailbox,
      Clock clock) {
    this.jdbc = Objects.requireNonNull(jdbc);
    this.tx = new TransactionTemplate(manager);
    this.denialTx = new TransactionTemplate(manager);
    this.denialTx.setPropagationBehavior(TransactionDefinition.PROPAGATION_REQUIRES_NEW);
    this.authority = Objects.requireNonNull(authority);
    this.mailbox = Objects.requireNonNull(mailbox);
    this.clock = Objects.requireNonNull(clock);
  }

  private UUID tenant() {
    return TenantContext.require();
  }

  private Instant now() {
    return clock.instant();
  }

  private Actor source() {
    var a = authority.current();
    actor(a);
    authority.checkSource(a);
    return a;
  }

  private Actor administrator() {
    authority.requireAdministrator();
    var a = source();
    require(a.sender() == Sender.HUMAN, "sender_mismatch");
    authority.requireHumanUi(a);
    return a;
  }

  private <T> T locked(Supplier<T> work) {
    tenant();
    try {
      return tx.execute(
          status -> {
            TenantTransactionManager.verifyTransactionTenant();
            // Short DB-only tenant fence also makes repeatable-read stale snapshots fail
            // serialization.
            jdbc.queryForObject(
                "INSERT INTO collab.network_group_fence(id,tenant_id,realm) VALUES (?,?,'0') ON CONFLICT(tenant_id,realm) DO UPDATE SET revision=collab.network_group_fence.revision+1 RETURNING revision",
                Long.class,
                UuidV7.next(),
                tenant());
            return work.get();
          });
    } catch (AurigaException e) {
      // Denial evidence survives the rejected business transaction, without body/intent handles.
      denialTx.executeWithoutResult(
          status ->
              jdbc.update(
                  "INSERT INTO collab.network_group_audit(id,tenant_id,realm,action,actor_user,subject,reason,created_at) VALUES (?,?,'0','denied','svc:collab','request',?,?)",
                  UuidV7.next(),
                  tenant(),
                  e.errorCode().code(),
                  Timestamp.from(now())));
      throw e;
    }
  }

  private List<Map<String, Object>> rows(String sql, Object... args) {
    return jdbc.queryForList(sql, args);
  }

  private Map<String, Object> one(String sql, String code, Object... args) {
    var r = rows(sql, args);
    require(r.size() == 1, code);
    return r.getFirst();
  }

  private static long number(Map<String, Object> r, String k) {
    return ((Number) r.get(k)).longValue();
  }

  private static Instant instant(Map<String, Object> r, String k) {
    return ((Timestamp) r.get(k)).toInstant();
  }

  private static UUID id(Map<String, Object> r, String k) {
    return (UUID) r.get(k);
  }

  private static String str(Map<String, Object> r, String k) {
    return (String) r.get(k);
  }

  private static String json(Object o) {
    return NetworkJson.canonical(JSON.valueToTree(o));
  }

  private static Actor actorJson(String value) {
    return JSON.readValue(value, Actor.class);
  }

  private Group groupResult(Map<String, Object> g) {
    return new Group(str(g, "realm"), str(g, "local"), number(g, "revision"));
  }

  private Map<String, Object> group(Actor a, String local) {
    decimal(local, false);
    return one(
        "SELECT * FROM collab.network_group WHERE tenant_id=? AND realm=? AND local=? AND NOT deleted",
        "group_not_available",
        tenant(),
        a.address().realm(),
        local);
  }

  private Map<String, Object> member(Actor a, String group) {
    return one(
        "SELECT * FROM collab.network_group_member WHERE tenant_id=? AND realm=? AND group_local=? AND node=? AND local=? AND active",
        "group_not_available",
        tenant(),
        a.address().realm(),
        group,
        a.address().node(),
        a.address().local());
  }

  private void publisher(Actor a, Map<String, Object> g) {
    var m = member(a, str(g, "local"));
    require(
        !(Boolean) g.get("admins_only") || (Boolean) m.get("administrator"),
        "group_publish_forbidden");
  }

  private void manageGroup(Actor a, Map<String, Object> g, long expected) {
    var m = member(a, str(g, "local"));
    require((Boolean) m.get("administrator"), "group_publish_forbidden");
    require(number(g, "revision") == expected, "conflict");
  }

  @Override
  public Group capacity(String local, long expected, int maxMembers, int maxNodes) {
    return locked(
        () -> {
          var a = administrator();
          var g = group(a, local);
          require(number(g, "revision") == expected, "conflict");
          input(maxMembers > 0 && maxMembers <= 1024 && maxNodes > 0 && maxNodes <= 256);
          var size =
              jdbc.queryForMap(
                  "SELECT count(*) AS members,count(DISTINCT node) AS nodes FROM collab.network_group_member WHERE tenant_id=? AND realm=? AND group_local=? AND active",
                  tenant(),
                  a.address().realm(),
                  local);
          require(
              number(size, "members") <= maxMembers && number(size, "nodes") <= maxNodes,
              "quota_exceeded");
          jdbc.update(
              "UPDATE collab.network_group SET max_members=?,max_nodes=? WHERE tenant_id=? AND id=?",
              maxMembers,
              maxNodes,
              tenant(),
              g.get("id"));
          audit(a, "capacity", local, "");
          return bump(g);
        });
  }

  @Override
  public Group delegate(String local, long expected, UUID membership, boolean administrator) {
    return locked(
        () -> {
          var a = source();
          var g = group(a, local);
          manageGroup(a, g, expected);
          require(
              g.get("owner_user").equals(a.user()) && a.sender() == Sender.HUMAN,
              "group_publish_forbidden");
          authority.requireHumanUi(a);
          require(
              jdbc.update(
                      "UPDATE collab.network_group_member SET administrator=? WHERE tenant_id=? AND realm=? AND group_local=? AND id=? AND active",
                      administrator,
                      tenant(),
                      a.address().realm(),
                      local,
                      membership)
                  == 1,
              "group_not_available");
          var result = bump(g);
          invalidateGroup(a.address().realm(), local);
          audit(a, "delegate", membership.toString(), "");
          return result;
        });
  }

  private Group bump(Map<String, Object> g) {
    jdbc.update(
        "UPDATE collab.network_group SET revision=revision+1 WHERE tenant_id=? AND id=?",
        tenant(),
        g.get("id"));
    return new Group(str(g, "realm"), str(g, "local"), number(g, "revision") + 1);
  }

  @Override
  public Group create(String name) {
    return locked(
        () -> {
          label(name);
          var a = source();
          authority.allowCreate(a);
          unfrozen(a);
          int count =
              jdbc.queryForObject(
                  "SELECT count(*) FROM collab.network_group WHERE tenant_id=? AND owner_user=? AND NOT deleted",
                  Integer.class,
                  tenant(),
                  a.user());
          require(count < 10, "quota_exceeded");
          String local =
              jdbc.queryForObject(
                  "INSERT INTO collab.network_group_fence(id,tenant_id,realm,next_group) VALUES (?,?,?,3) ON CONFLICT(tenant_id,realm) DO UPDATE SET next_group=collab.network_group_fence.next_group+1 RETURNING (next_group-1)::text",
                  String.class,
                  UuidV7.next(),
                  tenant(),
                  a.address().realm());
          jdbc.update(
              "INSERT INTO collab.network_group(id,tenant_id,realm,local,name,owner_user) VALUES (?,?,?,?,?,?)",
              UuidV7.next(),
              tenant(),
              a.address().realm(),
              local,
              name,
              a.user());
          addMember(a.address(), local, UuidV7.next(), true);
          audit(a, "create", local, "");
          return new Group(a.address().realm(), local, 1);
        });
  }

  private void addMember(Address a, String group, UUID membership, boolean admin) {
    jdbc.update(
        "INSERT INTO collab.network_group_member(id,tenant_id,realm,group_local,node,local,address_type,administrator,joined_at) VALUES (?,?,?,?,?,?,?,?,?)",
        membership,
        tenant(),
        a.realm(),
        group,
        a.node(),
        a.local(),
        a.type(),
        admin,
        Timestamp.from(now()));
  }

  @Override
  public Group policy(String local, long expected, boolean adminsOnly, boolean deleted) {
    return locked(
        () -> {
          var a = source();
          var g = group(a, local);
          manageGroup(a, g, expected);
          jdbc.update(
              "UPDATE collab.network_group SET admins_only=?,deleted=? WHERE tenant_id=? AND id=?",
              adminsOnly,
              deleted,
              tenant(),
              g.get("id"));
          if (deleted)
            jdbc.update(
                "UPDATE collab.network_group_member SET active=false,left_at=? WHERE tenant_id=? AND realm=? AND group_local=? AND active",
                Timestamp.from(now()),
                tenant(),
                a.address().realm(),
                local);
          var result = bump(g);
          invalidateGroup(a.address().realm(), local);
          audit(a, "policy", local, "");
          return result;
        });
  }

  @Override
  public Invitation invite(String local, long expected, Address target, Instant expiresAt) {
    return locked(
        () -> {
          var a = source();
          unfrozen(a);
          var g = group(a, local);
          manageGroup(a, g, expected);
          address(target);
          input(
              a.address().realm().equals(target.realm())
                  && expiresAt != null
                  && expiresAt.isAfter(now())
                  && !expiresAt.isAfter(now().plusSeconds(86400)));
          var invitation = UuidV7.next();
          var membership = UuidV7.next();
          var contact =
              Objects.requireNonNull(
                  authority.reserveInvitation(a, target, local, invitation, expiresAt));
          jdbc.update(
              "INSERT INTO collab.network_group_invitation(id,tenant_id,realm,group_local,group_revision,membership_id,contact_id,node,local,address_type,inviter_node,inviter_local,expires_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
              invitation,
              tenant(),
              target.realm(),
              local,
              expected,
              membership,
              contact,
              target.node(),
              target.local(),
              target.type(),
              a.address().node(),
              a.address().local(),
              Timestamp.from(expiresAt));
          audit(a, "invite", invitation.toString(), "");
          return new Invitation(invitation, membership, expected);
        });
  }

  @Override
  public Group join(UUID invitation, long expected) {
    return locked(
        () -> {
          var a = source();
          var i =
              one(
                  "SELECT * FROM collab.network_group_invitation WHERE tenant_id=? AND id=?",
                  "group_not_available",
                  tenant(),
                  invitation);
          require(
              a.address().realm().equals(i.get("realm"))
                  && a.address().node().equals(i.get("node"))
                  && a.address().local().equals(i.get("local")),
              "group_not_available");
          var g = group(a, str(i, "group_local"));
          require(number(i, "group_revision") == expected, "conflict");
          if (i.get("joined_revision") != null) {
            require(
                rows(
                            "SELECT id FROM collab.network_group_member WHERE tenant_id=? AND id=? AND active",
                            tenant(),
                            i.get("membership_id"))
                        .size()
                    == 1,
                "group_not_available");
            return new Group(
                a.address().realm(), str(i, "group_local"), number(i, "joined_revision"));
          }
          require(instant(i, "expires_at").isAfter(now()), "contact_expired");
          require(number(g, "revision") == expected, "conflict");
          require(
              !rows(
                      "SELECT id FROM collab.network_group_member WHERE tenant_id=? AND realm=? AND group_local=? AND node=? AND local=? AND administrator AND active",
                      tenant(),
                      a.address().realm(),
                      g.get("local"),
                      i.get("inviter_node"),
                      i.get("inviter_local"))
                  .isEmpty(),
              "group_not_available");
          authority.approveJoin(
              a, id(i, "contact_id"), invitation, id(i, "membership_id"), expected);
          var members =
              rows(
                  "SELECT node FROM collab.network_group_member WHERE tenant_id=? AND realm=? AND group_local=? AND active",
                  tenant(),
                  a.address().realm(),
                  g.get("local"));
          require(members.size() < number(g, "max_members"), "quota_exceeded");
          var nodes = new HashSet<String>();
          for (var m : members) nodes.add(str(m, "node"));
          nodes.add(a.address().node());
          require(nodes.size() <= number(g, "max_nodes"), "quota_exceeded");
          require(
              rows(
                      "SELECT id FROM collab.network_group_member WHERE tenant_id=? AND realm=? AND group_local=? AND node=? AND local=? AND active",
                      tenant(),
                      a.address().realm(),
                      g.get("local"),
                      a.address().node(),
                      a.address().local())
                  .isEmpty(),
              "conflict");
          addMember(a.address(), str(g, "local"), id(i, "membership_id"), false);
          var result = bump(g);
          jdbc.update(
              "UPDATE collab.network_group_invitation SET joined_revision=? WHERE tenant_id=? AND id=?",
              result.revision(),
              tenant(),
              invitation);
          audit(a, "join", invitation.toString(), "");
          return result;
        });
  }

  @Override
  public Group leave(String local, long expected) {
    return locked(
        () -> {
          var a = source();
          var g = group(a, local);
          require(number(g, "revision") == expected, "conflict");
          var m = member(a, local);
          jdbc.update(
              "UPDATE collab.network_group_member SET active=false,left_at=? WHERE tenant_id=? AND id=?",
              Timestamp.from(now()),
              tenant(),
              m.get("id"));
          jdbc.update(
              "UPDATE collab.network_group_recipient SET outcome='rejected',terminal_at=?,response=CASE WHEN response='pending' THEN 'unavailable' ELSE response END WHERE tenant_id=? AND membership_id=? AND outcome='pending'",
              Timestamp.from(now()),
              tenant(),
              m.get("id"));
          var result = bump(g);
          invalidateGroup(a.address().realm(), local);
          audit(a, "leave", local, "");
          return result;
        });
  }

  private String fingerprint(Send r) {
    input(
        r != null
            && r.thread() != null
            && r.key() != null
            && !r.key().isBlank()
            && r.key().length() <= 128
            && r.text() != null
            && r.intent() != null
            && r.mode() != null
            && r.expiresAt() != null);
    decimal(r.group(), false);
    input(r.text().getBytes(StandardCharsets.UTF_8).length <= 16384);
    var value =
        JSON.createObjectNode()
            .put("group", r.group())
            .put("thread", r.thread().toString())
            .put("key", r.key())
            .put("text", r.text())
            .put("intent", r.intent().name())
            .put("mode", r.mode().name())
            .put("expires", r.expiresAt().toString());
    return NetworkJson.digest(value);
  }

  @Override
  public UUID humanIntent(Send r) {
    return locked(() -> mintIntent(fingerprint(r)));
  }

  @Override
  public UUID humanEphemeralIntent(Send r, ProgressKind kind, long revision) {
    return locked(() -> mintIntent(progressFingerprint(r, kind, revision)));
  }

  private String progressFingerprint(Send r, ProgressKind kind, long revision) {
    input(kind != null && revision > 0);
    return NetworkJson.digest(
        JSON.createObjectNode()
            .put("operation", "ephemeral")
            .put("kind", kind.name())
            .put("revision", Long.toString(revision))
            .put("request", fingerprint(r)));
  }

  private UUID mintIntent(String fingerprint) {
    var a = source();
    authority.requireHumanUi(a);
    require(a.sender() == Sender.HUMAN, "sender_mismatch");
    var id = UuidV7.next();
    jdbc.update(
        "INSERT INTO collab.network_group_human_intent(id,tenant_id,realm,actor_json,fingerprint,expires_at) VALUES (?,?,?,?,?,?)",
        id,
        tenant(),
        a.address().realm(),
        json(a),
        fingerprint,
        Timestamp.from(now().plusSeconds(60)));
    return id;
  }

  private void consumeIntent(Actor a, Send r, String fingerprint, UUID message) {
    consumeIntent(a, r.humanIntent(), fingerprint, message);
  }

  private void consumeIntent(Actor a, UUID intent, String fingerprint, UUID message) {
    if (a.sender() != Sender.HUMAN) {
      require(intent == null, "sender_mismatch");
      return;
    }
    require(intent != null, "sender_mismatch");
    var i =
        one(
            "SELECT * FROM collab.network_group_human_intent WHERE tenant_id=? AND id=?",
            "sender_mismatch",
            tenant(),
            intent);
    require(
        json(a).equals(i.get("actor_json"))
            && fingerprint.equals(i.get("fingerprint"))
            && instant(i, "expires_at").isAfter(now())
            && i.get("consumed_message") == null,
        "sender_mismatch");
    jdbc.update(
        "UPDATE collab.network_group_human_intent SET consumed_message=? WHERE tenant_id=? AND id=?",
        message,
        tenant(),
        intent);
  }

  private String humanFingerprint(HumanSend r) {
    input(
        r != null
            && r.thread() != null
            && r.key() != null
            && !r.key().isBlank()
            && r.key().length() <= 128
            && r.text() != null
            && r.intent() != null
            && r.expiresAt() != null);
    address(r.target());
    input(r.text().getBytes(StandardCharsets.UTF_8).length <= 16384);
    return NetworkJson.digest(
        JSON.createObjectNode()
            .put("operation", "human_unicast")
            .put("target", json(r.target()))
            .put("thread", r.thread().toString())
            .put("key", r.key())
            .put("text", r.text())
            .put("intent", r.intent().name())
            .put("expires", r.expiresAt().toString()));
  }

  @Override
  public UUID humanUnicastIntent(HumanSend r) {
    return locked(() -> mintIntent(humanFingerprint(r)));
  }

  @Override
  public Stored sendHumanUnicast(HumanSend r) {
    return locked(
        () -> {
          var a = source();
          require(a.sender() == Sender.HUMAN, "sender_mismatch");
          String fp = humanFingerprint(r);
          var previous =
              rows(
                  "SELECT * FROM collab.network_human_submission WHERE tenant_id=? AND realm=? AND source_node=? AND idempotency_key=?",
                  tenant(),
                  a.address().realm(),
                  a.address().node(),
                  r.key());
          if (!previous.isEmpty()) {
            var p = previous.getFirst();
            require(
                fp.equals(p.get("fingerprint")) && json(a).equals(p.get("actor_json")), "conflict");
            return new Stored(id(p, "id"), 1, 1, str(p, "dispatch_status"));
          }
          unfrozen(a);
          input(
              a.address().realm().equals(r.target().realm())
                  && r.expiresAt().isAfter(now())
                  && !r.expiresAt().isAfter(now().plusSeconds(604800)));
          authority.checkHumanUnicast(
              a, r.target(), r.text().getBytes(StandardCharsets.UTF_8).length);
          UUID message = UuidV7.next();
          consumeIntent(a, r.humanIntent(), fp, message);
          var result = mailbox.humanUnicast(message, a, r);
          require(
              result != null
                  && message.equals(result.messageId())
                  && result.totalMembers() == 1
                  && result.totalNodes() == 1
                  && Set.of("pending", "complete").contains(result.dispatchStatus()),
              "protocol_error");
          jdbc.update(
              "INSERT INTO collab.network_human_submission(id,tenant_id,realm,source_node,idempotency_key,fingerprint,actor_json,dispatch_status) VALUES (?,?,?,?,?,?,?,?)",
              message,
              tenant(),
              a.address().realm(),
              a.address().node(),
              r.key(),
              fp,
              json(a),
              result.dispatchStatus());
          audit(a, "human_unicast", message.toString(), "");
          return result;
        });
  }

  @Override
  public Stored send(Send r) {
    return locked(
        () -> {
          var a = source();
          var fingerprint = fingerprint(r);
          var prior =
              rows(
                  "SELECT * FROM collab.network_group_plan WHERE tenant_id=? AND realm=? AND source_node=? AND source_local=? AND idempotency_key=?",
                  tenant(),
                  a.address().realm(),
                  a.address().node(),
                  a.address().local(),
                  r.key());
          if (!prior.isEmpty()) {
            var p = prior.getFirst();
            require(
                fingerprint.equals(p.get("fingerprint")) && json(a).equals(p.get("actor_json")),
                "conflict");
            return stored(p);
          }
          unfrozen(a);
          input(r.expiresAt().isAfter(now()) && !r.expiresAt().isAfter(now().plusSeconds(604800)));
          var targets = new ArrayList<Recipient>();
          Map<String, Object> grant = null;
          boolean broadcast = r.group().equals("1");
          if (broadcast) {
            grant = findGrant(a);
            var addresses = authority.broadcastSnapshot(a.address().realm());
            require(addresses.size() <= 10000, "quota_exceeded");
            for (var target : addresses) {
              address(target);
              input(a.address().realm().equals(target.realm()) && target.local().equals("0"));
              targets.add(new Recipient(UuidV7.next(), UuidV7.next(), target));
            }
          } else {
            var g = group(a, r.group());
            publisher(a, g);
            for (var m :
                rows(
                    "SELECT * FROM collab.network_group_member WHERE tenant_id=? AND realm=? AND group_local=? AND active ORDER BY node,local",
                    tenant(),
                    a.address().realm(),
                    r.group()))
              targets.add(
                  new Recipient(
                      UuidV7.next(),
                      id(m, "id"),
                      new Address(
                          a.address().realm(),
                          str(m, "node"),
                          str(m, "local"),
                          str(m, "address_type"))));
          }
          var byNode = nodes(a.address().realm(), targets, broadcast);
          authority.checkSend(
              a,
              r.group(),
              byNode.size(),
              targets.size(),
              r.text().getBytes(StandardCharsets.UTF_8).length,
              false);
          for (var target : targets) {
            authority.checkTarget(a, target.address(), broadcast);
          }
          atomic(r.mode(), byNode.size(), targets.size());
          if (grant != null)
            require(
                byNode.size() <= number(grant, "max_nodes")
                    && r.text().getBytes(StandardCharsets.UTF_8).length
                        <= number(grant, "max_bytes"),
                "broadcast_forbidden");
          int pending =
              jdbc.queryForObject(
                  "SELECT count(*) FROM collab.network_group_recipient WHERE tenant_id=? AND outcome='pending'",
                  Integer.class,
                  tenant());
          require(pending + targets.size() <= 100000, "quota_exceeded");
          long plans =
              jdbc.queryForObject(
                  "SELECT count(*) FROM collab.network_group_plan p WHERE p.tenant_id=? AND EXISTS(SELECT 1 FROM collab.network_group_recipient r WHERE r.tenant_id=p.tenant_id AND r.message_id=p.id AND r.outcome='pending')",
                  Long.class,
                  tenant());
          require(plans < 100, "quota_exceeded");
          var userBudget =
              jdbc.queryForMap(
                  "SELECT count(*) AS plans,coalesce(sum(total_nodes),0) AS nodes,coalesce(sum(octet_length(body)::bigint*total_nodes),0) AS bytes FROM collab.network_group_plan p WHERE tenant_id=? AND source_user=? AND EXISTS(SELECT 1 FROM collab.network_group_recipient r WHERE r.tenant_id=p.tenant_id AND r.message_id=p.id AND r.outcome='pending')",
                  tenant(),
                  a.user());
          require(
              number(userBudget, "plans") < 10
                  && number(userBudget, "nodes") + byNode.size() <= 10000
                  && number(userBudget, "bytes")
                          + (long) r.text().getBytes(StandardCharsets.UTF_8).length * byNode.size()
                      <= 67108864L,
              "quota_exceeded");
          long pendingBytes =
              jdbc.queryForObject(
                  "SELECT coalesce(sum(octet_length(body)::bigint*total_nodes),0) FROM collab.network_group_plan p WHERE tenant_id=? AND EXISTS(SELECT 1 FROM collab.network_group_recipient r WHERE r.tenant_id=p.tenant_id AND r.message_id=p.id AND r.outcome='pending')",
                  Long.class,
                  tenant());
          require(
              pendingBytes + (long) r.text().getBytes(StandardCharsets.UTF_8).length * byNode.size()
                  <= 1073741824L,
              "quota_exceeded");
          if (broadcast) {
            rate(a.address().realm(), "broadcast_source", a.user(), 1, 1);
            rate(a.address().realm(), "broadcast_tenant", "all", 5, 5);
          } else {
            rate(a.address().realm(), "group_source", a.address().node(), 60, 10);
            rate(a.address().realm(), "group", r.group(), 300, 300);
          }
          var message = UuidV7.next();
          consumeIntent(a, r, fingerprint, message);
          var deadline =
              r.expiresAt().isBefore(now().plusSeconds(86400))
                  ? r.expiresAt()
                  : now().plusSeconds(86400);
          jdbc.update(
              "INSERT INTO collab.network_group_plan(id,tenant_id,realm,group_local,source_node,source_local,source_user,actor_json,thread_id,idempotency_key,fingerprint,body,intent,dispatch_mode,total_members,total_nodes,grant_id,grant_revision,created_at,expires_at,response_deadline) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
              message,
              tenant(),
              a.address().realm(),
              r.group(),
              a.address().node(),
              a.address().local(),
              a.user(),
              json(a),
              r.thread(),
              r.key(),
              fingerprint,
              r.text(),
              r.intent().name(),
              r.mode().name(),
              targets.size(),
              byNode.size(),
              grant == null ? null : grant.get("id"),
              grant == null ? 0 : number(grant, "revision"),
              Timestamp.from(now()),
              Timestamp.from(r.expiresAt()),
              Timestamp.from(deadline));
          for (var t : targets)
            jdbc.update(
                "INSERT INTO collab.network_group_recipient(id,tenant_id,message_id,membership_id,realm,node,local,address_type,response) VALUES (?,?,?,?,?,?,?,?,?)",
                t.id(),
                tenant(),
                message,
                t.membershipId(),
                t.address().realm(),
                t.address().node(),
                t.address().local(),
                t.address().type(),
                r.intent() == Intent.REQUEST ? "pending" : "unavailable");
          var p = plan(message);
          for (var list : byNode.values()) {
            require(list.size() <= 256, "quota_exceeded");
            mailbox.preflight(item(p, list));
          }
          if (r.mode() == Mode.ATOMIC) dispatchPage(p);
          audit(a, "send", message.toString(), "");
          return stored(p);
        });
  }

  private void rate(String realm, String scope, String subject, int perMinute, int burst) {
    var previous =
        rows(
            "SELECT * FROM collab.network_group_rate WHERE tenant_id=? AND realm=? AND scope=? AND subject=?",
            tenant(),
            realm,
            scope,
            subject);
    double tokens = burst;
    if (!previous.isEmpty()) {
      var r = previous.getFirst();
      tokens =
          Math.min(
              burst,
              ((Number) r.get("tokens")).doubleValue()
                  + Math.max(0, Duration.between(instant(r, "refilled_at"), now()).toMillis())
                      * perMinute
                      / 60000.0);
    }
    require(tokens >= 1, "quota_exceeded");
    jdbc.update(
        "INSERT INTO collab.network_group_rate(id,tenant_id,realm,scope,subject,tokens,refilled_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT(tenant_id,realm,scope,subject) DO UPDATE SET tokens=excluded.tokens,refilled_at=excluded.refilled_at",
        UuidV7.next(),
        tenant(),
        realm,
        scope,
        subject,
        tokens - 1,
        Timestamp.from(now()));
  }

  @Override
  public Ephemeral prepareEphemeral(Send r, ProgressKind kind, long revision) {
    return locked(
        () -> {
          var a = source();
          unfrozen(a);
          var fp = progressFingerprint(r, kind, revision);
          input(
              r.intent() == Intent.INFORMATION
                  && !r.group().equals("1")
                  && r.expiresAt().isAfter(now())
                  && !r.expiresAt().isAfter(now().plusSeconds(30)));
          var g = group(a, r.group());
          publisher(a, g);
          var recipients = new ArrayList<Recipient>();
          for (var m :
              rows(
                  "SELECT * FROM collab.network_group_member WHERE tenant_id=? AND realm=? AND group_local=? AND active ORDER BY node,local",
                  tenant(),
                  a.address().realm(),
                  r.group())) {
            var address =
                new Address(
                    a.address().realm(), str(m, "node"), str(m, "local"), str(m, "address_type"));
            authority.checkTarget(a, address, false);
            recipients.add(new Recipient(UuidV7.next(), id(m, "id"), address));
          }
          var targetNodes = nodes(a.address().realm(), recipients, false);
          authority.checkSend(
              a,
              r.group(),
              targetNodes.size(),
              recipients.size(),
              r.text().getBytes(StandardCharsets.UTF_8).length,
              true);
          rate(a.address().realm(), "group_source", a.address().node(), 60, 10);
          rate(a.address().realm(), "group", r.group(), 300, 300);
          var event = UuidV7.next();
          consumeIntent(a, r, fp, event);
          // No body/message/receipt/dispatch plan is persisted. PUB success is not platform_stored.
          return new Ephemeral(
              event, a, groupResult(g), kind, revision, r.text(), r.expiresAt(), recipients);
        });
  }

  private Map<String, Object> findGrant(Actor a) {
    for (var g :
        rows(
            "SELECT * FROM collab.network_broadcast_grant WHERE tenant_id=? AND realm=? AND source_user=? AND NOT revoked AND expires_at>? ORDER BY id",
            tenant(),
            a.address().realm(),
            a.user(),
            Timestamp.from(now()))) {
      boolean match =
          a.sender() == Sender.HUMAN
              ? g.get("scope").equals("USER")
                  || g.get("scope").equals("NODE") && a.address().node().equals(g.get("node"))
              : g.get("scope").equals("AGENT")
                  && a.address().node().equals(g.get("node"))
                  && a.address().local().equals(g.get("local"))
                  && a.binding().equals(g.get("binding"));
      if (match) return g;
    }
    throw error("broadcast_forbidden");
  }

  @Override
  public Grant grant(GrantRequest r) {
    return locked(
        () -> {
          var a = administrator();
          input(
              r != null
                  && r.scope() != null
                  && r.user() != null
                  && !r.user().isBlank()
                  && r.expiresAt() != null
                  && r.expiresAt().isAfter(now())
                  && !r.expiresAt().isAfter(now().plusSeconds(86400))
                  && r.maxNodes() > 0
                  && r.maxNodes() <= 10000
                  && r.maxBytes() > 0
                  && r.maxBytes() <= 16384);
          reason(r.reason());
          if (r.scope() != Scope.USER) decimal(r.node(), false);
          if (r.scope() == Scope.AGENT) {
            decimal(r.local(), false);
            input(r.binding() != null && !r.binding().isBlank());
          }
          input(
              r.scope() != Scope.USER
                  || r.node() == null && r.local() == null && r.binding() == null);
          input(r.scope() != Scope.NODE || r.local() == null && r.binding() == null);
          UUID id = UuidV7.next();
          jdbc.update(
              "INSERT INTO collab.network_broadcast_grant(id,tenant_id,realm,scope,source_user,node,local,binding,expires_at,max_nodes,max_bytes) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
              id,
              tenant(),
              a.address().realm(),
              r.scope().name(),
              r.user(),
              r.node(),
              r.local(),
              r.binding(),
              Timestamp.from(r.expiresAt()),
              r.maxNodes(),
              r.maxBytes());
          audit(a, "grant", id.toString(), r.reason());
          return new Grant(id, 1);
        });
  }

  private void reason(String reason) {
    input(reason != null && !reason.isBlank() && reason.length() <= 512);
  }

  private Long priorManagement(String realm, String key, String fingerprint) {
    input(key != null && !key.isBlank() && key.length() <= 128);
    var rows =
        rows(
            "SELECT * FROM collab.network_group_management WHERE tenant_id=? AND realm=? AND idempotency_key=?",
            tenant(),
            realm,
            key);
    if (rows.isEmpty()) return null;
    require(fingerprint.equals(rows.getFirst().get("fingerprint")), "conflict");
    return number(rows.getFirst(), "result_revision");
  }

  private void management(String realm, String key, String fp, long revision) {
    jdbc.update(
        "INSERT INTO collab.network_group_management(id,tenant_id,realm,idempotency_key,fingerprint,result_revision) VALUES (?,?,?,?,?,?)",
        UuidV7.next(),
        tenant(),
        realm,
        key,
        fp,
        revision);
  }

  @Override
  public Grant revoke(UUID grant, long expected, String key, String reason) {
    return locked(
        () -> {
          var a = administrator();
          reason(reason);
          String fp = json(List.of("revoke", grant, expected, reason));
          var prior = priorManagement(a.address().realm(), key, fp);
          if (prior != null) return new Grant(grant, prior);
          var g =
              one(
                  "SELECT * FROM collab.network_broadcast_grant WHERE tenant_id=? AND realm=? AND id=?",
                  "broadcast_forbidden",
                  tenant(),
                  a.address().realm(),
                  grant);
          require(number(g, "revision") == expected, "conflict");
          jdbc.update(
              "UPDATE collab.network_broadcast_grant SET revoked=true,revision=revision+1 WHERE tenant_id=? AND id=?",
              tenant(),
              grant);
          for (var p :
              rows(
                  "SELECT * FROM collab.network_group_plan WHERE tenant_id=? AND grant_id=?",
                  tenant(),
                  grant)) terminate(p, "rejected");
          management(a.address().realm(), key, fp, expected + 1);
          audit(a, "revoke", grant.toString(), reason);
          return new Grant(grant, expected + 1);
        });
  }

  private void unfrozen(Actor a) {
    require(!isFrozen(a), "broadcast_forbidden");
  }

  private boolean isFrozen(Actor a) {
    return !rows(
            "SELECT id FROM collab.network_sender_freeze WHERE tenant_id=? AND realm=? AND frozen AND ((scope='USER' AND subject=?) OR (scope='NODE' AND subject=?) OR (scope='AGENT' AND subject=?))",
            tenant(),
            a.address().realm(),
            a.user(),
            a.address().node(),
            a.address().node() + "." + a.address().local())
        .isEmpty();
  }

  @Override
  public long freeze(
      Scope scope, String subject, long expected, boolean frozen, String key, String reason) {
    return locked(
        () -> {
          var a = administrator();
          input(scope != null && subject != null && !subject.isBlank() && subject.length() <= 128);
          reason(reason);
          if (scope == Scope.NODE) decimal(subject, false);
          if (scope == Scope.AGENT) {
            var parts = subject.split("\\.", -1);
            input(parts.length == 2);
            decimal(parts[0], false);
            decimal(parts[1], false);
          }
          String fp = json(List.of("freeze", scope, subject, expected, frozen, reason));
          var prior = priorManagement(a.address().realm(), key, fp);
          if (prior != null) return prior;
          var old =
              rows(
                  "SELECT * FROM collab.network_sender_freeze WHERE tenant_id=? AND realm=? AND scope=? AND subject=?",
                  tenant(),
                  a.address().realm(),
                  scope.name(),
                  subject);
          require((old.isEmpty() ? 0 : number(old.getFirst(), "revision")) == expected, "conflict");
          jdbc.update(
              "INSERT INTO collab.network_sender_freeze(id,tenant_id,realm,scope,subject,revision,frozen) VALUES (?,?,?,?,?,?,?) ON CONFLICT(tenant_id,realm,scope,subject) DO UPDATE SET revision=excluded.revision,frozen=excluded.frozen",
              UuidV7.next(),
              tenant(),
              a.address().realm(),
              scope.name(),
              subject,
              expected + 1,
              frozen);
          if (frozen) {
            String grantPredicate =
                switch (scope) {
                  case USER -> "source_user=?";
                  case NODE -> "node=?";
                  case AGENT -> "scope='AGENT' AND node || '.' || local=?";
                };
            jdbc.update(
                "UPDATE collab.network_broadcast_grant SET revoked=true,revision=revision+1 WHERE tenant_id=? AND realm=? AND NOT revoked AND "
                    + grantPredicate,
                tenant(),
                a.address().realm(),
                subject);
          }
          if (frozen)
            jdbc.update(
                "UPDATE collab.network_group_recipient r SET outcome='rejected',terminal_at=?,response=CASE WHEN response='pending' THEN 'unavailable' ELSE response END FROM collab.network_group_plan p WHERE r.tenant_id=p.tenant_id AND r.message_id=p.id AND p.tenant_id=? AND p.realm=? AND r.outcome='pending' AND "
                    + sourcePredicate(scope),
                Timestamp.from(now()),
                tenant(),
                a.address().realm(),
                subject);
          long boundary =
              jdbc.queryForObject(
                  "SELECT coalesce(max(position),0) FROM collab.network_group_plan WHERE tenant_id=?",
                  Long.class,
                  tenant());
          // Publish scope revisions lazily in bounded pages; freeze itself never walks old bodies.
          jdbc.update(
              "INSERT INTO collab.network_group_freeze_dispatch(id,tenant_id,realm,scope,subject,revision,frozen,boundary) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id,realm,scope,subject) DO UPDATE SET revision=excluded.revision,frozen=excluded.frozen,boundary=excluded.boundary,after_position=0,after_node='',complete=false",
              UuidV7.next(),
              tenant(),
              a.address().realm(),
              scope.name(),
              subject,
              expected + 1,
              frozen,
              boundary);
          management(a.address().realm(), key, fp, expected + 1);
          audit(a, frozen ? "freeze" : "unfreeze", subject, reason);
          return expected + 1;
        });
  }

  @Override
  public long recall(UUID message, long expected, String key, String reason) {
    return locked(
        () -> {
          var a = administrator();
          reason(reason);
          String fp = json(List.of("recall", message, expected, reason));
          var prior = priorManagement(a.address().realm(), key, fp);
          if (prior != null) return prior;
          var p = plan(message);
          require(
              p.get("realm").equals(a.address().realm()) && p.get("group_local").equals("1"),
              "broadcast_forbidden");
          require(number(p, "state_version") == expected, "conflict");
          jdbc.update(
              "UPDATE collab.network_group_plan SET recalled=true,state_version=state_version+1 WHERE tenant_id=? AND id=?",
              tenant(),
              message);
          terminate(p, "rejected");
          jdbc.update(
              "UPDATE collab.network_group_recipient SET response='unavailable' WHERE tenant_id=? AND message_id=? AND response='pending'",
              tenant(),
              message);
          updates(p, expected + 1, "recall");
          management(a.address().realm(), key, fp, expected + 1);
          audit(a, "recall", message.toString(), reason);
          return expected + 1;
        });
  }

  private void updates(Map<String, Object> p, long revision, String kind) {
    for (var n :
        rows(
            "SELECT DISTINCT node FROM collab.network_group_recipient WHERE tenant_id=? AND message_id=?",
            tenant(),
            p.get("id")))
      jdbc.update(
          "INSERT INTO collab.network_group_state_update(id,tenant_id,message_id,node,revision,kind) VALUES (?,?,?,?,?,?) ON CONFLICT DO NOTHING",
          UuidV7.next(),
          tenant(),
          p.get("id"),
          n.get("node"),
          revision,
          kind);
  }

  private Map<String, Object> plan(UUID message) {
    return one(
        "SELECT * FROM collab.network_group_plan WHERE tenant_id=? AND id=?",
        "target_unavailable",
        tenant(),
        message);
  }

  private void terminate(Map<String, Object> p, String outcome) {
    jdbc.update(
        "UPDATE collab.network_group_recipient SET outcome=?,terminal_at=?,response=CASE WHEN response='pending' THEN 'unavailable' ELSE response END WHERE tenant_id=? AND message_id=? AND outcome='pending'",
        outcome,
        Timestamp.from(now()),
        tenant(),
        p.get("id"));
  }

  private boolean live(Map<String, Object> p) {
    if ((Boolean) p.get("recalled")) return false;
    if (!instant(p, "expires_at").isAfter(now())) {
      terminate(p, "expired");
      return false;
    }
    if (p.get("grant_id") != null) {
      var gs =
          rows(
              "SELECT * FROM collab.network_broadcast_grant WHERE tenant_id=? AND id=? AND NOT revoked AND expires_at>? AND revision=?",
              tenant(),
              p.get("grant_id"),
              Timestamp.from(now()),
              p.get("grant_revision"));
      if (gs.isEmpty()) {
        terminate(p, "rejected");
        return false;
      }
    }
    var s = actorJson(str(p, "actor_json"));
    if (isFrozen(s)) {
      terminate(p, "rejected");
      return false;
    }
    if (!p.get("group_local").equals("1")) {
      var groups =
          rows(
              "SELECT * FROM collab.network_group WHERE tenant_id=? AND realm=? AND local=? AND NOT deleted",
              tenant(),
              p.get("realm"),
              p.get("group_local"));
      var members =
          rows(
              "SELECT * FROM collab.network_group_member WHERE tenant_id=? AND realm=? AND group_local=? AND node=? AND local=? AND active",
              tenant(),
              p.get("realm"),
              p.get("group_local"),
              s.address().node(),
              s.address().local());
      if (groups.isEmpty()
          || members.isEmpty()
          || (Boolean) groups.getFirst().get("admins_only")
              && !(Boolean) members.getFirst().get("administrator")) {
        terminate(p, "rejected");
        return false;
      }
      jdbc.update(
          "UPDATE collab.network_group_recipient r SET outcome='rejected',terminal_at=?,response=CASE WHEN response='pending' THEN 'unavailable' ELSE response END WHERE tenant_id=? AND message_id=? AND outcome='pending' AND NOT EXISTS(SELECT 1 FROM collab.network_group_member m WHERE m.tenant_id=r.tenant_id AND m.id=r.membership_id AND m.active)",
          Timestamp.from(now()),
          tenant(),
          p.get("id"));
    }
    return true;
  }

  private void invalidateGroup(String realm, String local) {
    // A local exit/policy revocation must not depend on a different sender's IAM availability.
    jdbc.update(
        "UPDATE collab.network_group_recipient r SET outcome='rejected',terminal_at=?,response=CASE WHEN response='pending' THEN 'unavailable' ELSE response END FROM collab.network_group_plan p WHERE r.tenant_id=p.tenant_id AND r.message_id=p.id AND p.tenant_id=? AND p.realm=? AND p.group_local=? AND r.outcome='pending' AND (NOT EXISTS(SELECT 1 FROM collab.network_group g JOIN collab.network_group_member m ON m.tenant_id=g.tenant_id AND m.realm=g.realm AND m.group_local=g.local WHERE g.tenant_id=p.tenant_id AND g.realm=p.realm AND g.local=p.group_local AND NOT g.deleted AND m.active AND m.node=p.source_node AND m.local=p.source_local AND (NOT g.admins_only OR m.administrator)) OR NOT EXISTS(SELECT 1 FROM collab.network_group_member m WHERE m.tenant_id=r.tenant_id AND m.id=r.membership_id AND m.active))",
        Timestamp.from(now()),
        tenant(),
        realm,
        local);
    jdbc.update(
        "UPDATE collab.network_group_recipient r SET response='unavailable' FROM collab.network_group_plan p WHERE r.tenant_id=p.tenant_id AND r.message_id=p.id AND p.tenant_id=? AND p.realm=? AND p.group_local=? AND r.response='pending' AND NOT EXISTS(SELECT 1 FROM collab.network_group_member m WHERE m.tenant_id=r.tenant_id AND m.id=r.membership_id AND m.active)",
        tenant(),
        realm,
        local);
  }

  private Recipient recipient(Map<String, Object> r) {
    return new Recipient(
        id(r, "id"),
        id(r, "membership_id"),
        new Address(str(r, "realm"), str(r, "node"), str(r, "local"), str(r, "address_type")));
  }

  private Item item(Map<String, Object> p, List<Recipient> recipients) {
    return new Item(
        id(p, "id"),
        str(p, "realm"),
        str(p, "group_local"),
        id(p, "thread_id"),
        actorJson(str(p, "actor_json")),
        str(p, "body"),
        Intent.valueOf(str(p, "intent")),
        instant(p, "expires_at"),
        number(p, "state_version"),
        id(p, "grant_id"),
        number(p, "grant_revision"),
        p.get("grant_id") == null
            ? null
            : Scope.valueOf(
                jdbc.queryForObject(
                    "SELECT scope FROM collab.network_broadcast_grant WHERE tenant_id=? AND id=?",
                    String.class,
                    tenant(),
                    p.get("grant_id"))),
        recipients);
  }

  private Stored stored(Map<String, Object> p) {
    long pending =
        jdbc.queryForObject(
            "SELECT count(*) FROM collab.network_group_recipient WHERE tenant_id=? AND message_id=? AND NOT dispatched AND outcome='pending'",
            Long.class,
            tenant(),
            p.get("id"));
    return new Stored(
        id(p, "id"),
        (int) number(p, "total_members"),
        (int) number(p, "total_nodes"),
        pending == 0 ? "complete" : "pending");
  }

  @Override
  public int dispatch(UUID message) {
    return locked(
        () -> {
          authority.requireWorker();
          return dispatchPage(plan(message));
        });
  }

  @Override
  public List<UUID> pendingDispatches(UUID after, int limit) {
    return locked(
        () -> {
          authority.requireWorker();
          input(limit > 0 && limit <= 128);
          return rows(
                  "SELECT id FROM collab.network_group_plan p WHERE tenant_id=? AND id>? AND EXISTS(SELECT 1 FROM collab.network_group_recipient r WHERE r.tenant_id=p.tenant_id AND r.message_id=p.id AND r.outcome='pending') ORDER BY id LIMIT ?",
                  tenant(),
                  after == null ? new UUID(0, 0) : after,
                  limit)
              .stream()
              .map(r -> id(r, "id"))
              .toList();
        });
  }

  @Override
  public void cancel(UUID message) {
    locked(
        () -> {
          var a = source();
          var p = plan(message);
          require(json(a).equals(p.get("actor_json")), "target_unavailable");
          terminate(p, "canceled");
          audit(a, "cancel", message.toString(), "");
          return null;
        });
  }

  private int dispatchPage(Map<String, Object> p) {
    if (!live(p)) return 0;
    authority.checkSource(actorJson(str(p, "actor_json")));
    var nodes =
        rows(
            "SELECT DISTINCT node FROM collab.network_group_recipient WHERE tenant_id=? AND message_id=? AND NOT dispatched AND outcome='pending' ORDER BY node LIMIT 32",
            tenant(),
            p.get("id"));
    int count = 0;
    for (var n : nodes) {
      var rs =
          rows(
              "SELECT * FROM collab.network_group_recipient WHERE tenant_id=? AND message_id=? AND node=? AND NOT dispatched AND outcome='pending' ORDER BY local",
              tenant(),
              p.get("id"),
              n.get("node"));
      // A whole node item may be created early; fetch below gates each logical local separately.
      var recipients = rs.stream().map(this::recipient).toList();
      var source = actorJson(str(p, "actor_json"));
      for (var r : recipients)
        authority.checkTarget(source, r.address(), p.get("group_local").equals("1"));
      var item = item(p, recipients);
      mailbox.preflight(item);
      mailbox.store(item);
      jdbc.update(
          "UPDATE collab.network_group_recipient SET dispatched=true WHERE tenant_id=? AND message_id=? AND node=? AND outcome='pending'",
          tenant(),
          p.get("id"),
          n.get("node"));
      count++;
    }
    return count;
  }

  private Map<String, Object> targetRecipient(Actor a, UUID message, UUID recipient) {
    var r =
        one(
            "SELECT * FROM collab.network_group_recipient WHERE tenant_id=? AND message_id=? AND id=?",
            "target_unavailable",
            tenant(),
            message,
            recipient);
    require(a.address().equals(recipient(r).address()), "target_unavailable");
    return r;
  }

  @Override
  public Item fetch(UUID message) {
    return locked(
        () -> {
          var a = source();
          var p = plan(message);
          require(!(Boolean) p.get("recalled"), "message_recalled");
          var rs =
              rows(
                  "SELECT * FROM collab.network_group_recipient WHERE tenant_id=? AND message_id=? AND realm=? AND node=? AND local=? AND dispatched",
                  tenant(),
                  message,
                  a.address().realm(),
                  a.address().node(),
                  a.address().local());
          require(!rs.isEmpty(), "target_unavailable");
          boolean accepted = rs.stream().allMatch(r -> r.get("outcome").equals("accepted"));
          // Accepted historical evidence survives sender grant expiry/revocation, but target
          // permission is current.
          if (!accepted) {
            require(live(p), "target_unavailable");
            authority.checkSource(actorJson(str(p, "actor_json")));
          }
          var recipients = new ArrayList<Recipient>();
          for (var r : rs) {
            require(
                r.get("outcome").equals("pending") || r.get("outcome").equals("accepted"),
                "target_unavailable");
            require(
                r.get("outcome").equals("accepted")
                    || rows(
                            "SELECT r.id FROM collab.network_group_recipient r JOIN collab.network_group_plan q ON q.tenant_id=r.tenant_id AND q.id=r.message_id WHERE r.tenant_id=? AND q.realm=? AND q.group_local=? AND q.thread_id=? AND r.node=? AND r.local=? AND q.position<? AND r.outcome='pending' LIMIT 1",
                            tenant(),
                            p.get("realm"),
                            p.get("group_local"),
                            p.get("thread_id"),
                            r.get("node"),
                            r.get("local"),
                            p.get("position"))
                        .isEmpty(),
                "target_unavailable");
            var t = recipient(r);
            if (!accepted)
              authority.checkTarget(
                  actorJson(str(p, "actor_json")), t.address(), p.get("group_local").equals("1"));
            recipients.add(t);
          }
          var result = item(p, recipients);
          if (accepted) authority.checkHistoricalRead(a, result);
          return result;
        });
  }

  @Override
  public void receipt(UUID message, UUID recipient, Outcome outcome) {
    locked(
        () -> {
          input(outcome != null);
          var a = source();
          var p = plan(message);
          var r = targetRecipient(a, message, recipient);
          require((Boolean) r.get("dispatched"), "target_unavailable");
          var acceptedAt = authority.verifyReceipt(a, message, recipient(r), outcome);
          String value = outcome.name().toLowerCase(Locale.ROOT);
          if (outcome == Outcome.ACCEPTED)
            require(
                acceptedAt != null
                    && !acceptedAt.isAfter(now())
                    && !acceptedAt.isBefore(instant(p, "created_at"))
                    && acceptedAt.isBefore(instant(p, "expires_at")),
                "conflict");
          if (!r.get("outcome").equals("pending")) {
            if (r.get("outcome").equals(value)) return null;
            require(
                outcome == Outcome.ACCEPTED
                    && r.get("terminal_at") != null
                    && !acceptedAt.isAfter(instant(r, "terminal_at")),
                "conflict");
            jdbc.update(
                "UPDATE collab.network_group_recipient SET outcome='accepted' WHERE tenant_id=? AND id=?",
                tenant(),
                recipient);
            return null;
          }
          // Historical evidence reconciliation is independent of current source grants/recall.
          jdbc.update(
              "UPDATE collab.network_group_recipient SET outcome=? WHERE tenant_id=? AND id=? AND outcome='pending'",
              value,
              tenant(),
              recipient);
          if (!value.equals("accepted"))
            jdbc.update(
                "UPDATE collab.network_group_recipient SET response='unavailable' WHERE tenant_id=? AND id=? AND response='pending'",
                tenant(),
                recipient);
          audit(a, "receipt", p.get("id").toString(), "");
          return null;
        });
  }

  @Override
  public void respond(UUID message, UUID recipient, String key, int revision, Response response) {
    locked(
        () -> {
          input(response != null && key != null && !key.isBlank() && key.length() <= 128);
          var a = source();
          var p = plan(message);
          var r = targetRecipient(a, message, recipient);
          require(!(Boolean) p.get("recalled"), "message_recalled");
          require(
              (Boolean) r.get("dispatched")
                  && p.get("intent").equals("REQUEST")
                  && instant(p, "response_deadline").isAfter(now())
                  && !Set.of("rejected", "expired", "canceled", "skipped", "unresolved")
                      .contains(r.get("outcome")),
              "target_unavailable");
          if (!p.get("group_local").equals("1")) {
            member(a, str(p, "group_local"));
          }
          authority.checkTarget(
              actorJson(str(p, "actor_json")), a.address(), p.get("group_local").equals("1"));
          authority.verifyResponse(
              a, item(p, List.of(recipient(r))), recipient(r), key, revision, response);
          var operations =
              rows(
                  "SELECT * FROM collab.network_group_response_operation WHERE tenant_id=? AND recipient_id=? AND idempotency_key=?",
                  tenant(),
                  recipient,
                  key);
          if (!operations.isEmpty()) {
            var op = operations.getFirst();
            require(
                number(op, "revision") == revision && response.name().equals(op.get("response")),
                "conflict");
            return null;
          }
          require(revision <= 4, "quota_exceeded");
          require(revision == number(r, "response_revision") + 1, "conflict");
          jdbc.update(
              "UPDATE collab.network_group_recipient SET response=?,response_revision=?,response_key=?,response_kind=? WHERE tenant_id=? AND id=?",
              response.name().toLowerCase(Locale.ROOT),
              revision,
              key,
              response.name(),
              tenant(),
              recipient);
          jdbc.update(
              "INSERT INTO collab.network_group_response_operation(id,tenant_id,recipient_id,idempotency_key,revision,response) VALUES (?,?,?,?,?,?)",
              UuidV7.next(),
              tenant(),
              recipient,
              key,
              revision,
              response.name());
          return null;
        });
  }

  @Override
  public Statistics status(UUID message) {
    return locked(
        () -> {
          var a = source();
          var p = plan(message);
          require(json(a).equals(p.get("actor_json")), "target_unavailable");
          live(p);
          jdbc.update(
              "UPDATE collab.network_group_recipient SET response='timed_out' WHERE tenant_id=? AND message_id=? AND response='pending' AND ? >= ?",
              tenant(),
              message,
              Timestamp.from(now()),
              p.get("response_deadline"));
          var receipts = new HashMap<String, Long>();
          var responses = new HashMap<String, Long>();
          for (var r :
              rows(
                  "SELECT outcome,response,count(*) AS n FROM collab.network_group_recipient WHERE tenant_id=? AND message_id=? GROUP BY outcome,response",
                  tenant(),
                  message)) {
            receipts.merge(str(r, "outcome"), number(r, "n"), Long::sum);
            responses.merge(str(r, "response"), number(r, "n"), Long::sum);
          }
          return new Statistics(
              (int) number(p, "total_members"),
              receipts,
              responses,
              (Boolean) p.get("recalled"),
              number(p, "state_version"));
        });
  }

  @Override
  public List<StateUpdate> pendingUpdates(int limit) {
    return locked(
        () -> {
          authority.requireWorker();
          input(limit > 0 && limit <= 128);
          expandFreeze(Math.min(limit, 32));
          return rows(
                  "SELECT * FROM collab.network_group_state_update WHERE tenant_id=? AND NOT committed ORDER BY id LIMIT ?",
                  tenant(),
                  limit)
              .stream()
              .map(
                  r ->
                      new StateUpdate(
                          id(r, "id"),
                          id(r, "message_id"),
                          str(r, "node"),
                          number(r, "revision"),
                          str(r, "kind"),
                          r.get("scope") == null ? null : Scope.valueOf(str(r, "scope")),
                          str(r, "subject"),
                          (Boolean) r.get("frozen")))
              .toList();
        });
  }

  private static String sourcePredicate(Scope scope) {
    return switch (scope) {
      case USER -> "p.source_user=?";
      case NODE -> "p.source_node=?";
      case AGENT -> "p.source_local<>'0' AND p.source_node || '.' || p.source_local=?";
    };
  }

  private void expandFreeze(int limit) {
    var plans =
        rows(
            "SELECT * FROM collab.network_group_freeze_dispatch WHERE tenant_id=? AND NOT complete ORDER BY id LIMIT 1",
            tenant());
    if (plans.isEmpty()) return;
    var f = plans.getFirst();
    var page =
        rows(
            "SELECT DISTINCT p.id,p.position,r.node FROM collab.network_group_plan p JOIN collab.network_group_recipient r ON r.tenant_id=p.tenant_id AND r.message_id=p.id WHERE p.tenant_id=? AND p.realm=? AND "
                + sourcePredicate(Scope.valueOf(str(f, "scope")))
                + " AND p.position<=? AND (p.position,r.node)>(?,?) ORDER BY p.position,r.node LIMIT ?",
            tenant(),
            f.get("realm"),
            f.get("subject"),
            f.get("boundary"),
            f.get("after_position"),
            f.get("after_node"),
            limit);
    for (var r : page)
      jdbc.update(
          "INSERT INTO collab.network_group_state_update(id,tenant_id,message_id,node,revision,kind,scope,subject,frozen) VALUES (?,?,?,?,?,'freeze',?,?,?) ON CONFLICT DO NOTHING",
          UuidV7.next(),
          tenant(),
          r.get("id"),
          r.get("node"),
          f.get("revision"),
          f.get("scope"),
          f.get("subject"),
          f.get("frozen"));
    if (page.isEmpty())
      jdbc.update(
          "UPDATE collab.network_group_freeze_dispatch SET complete=true WHERE tenant_id=? AND id=?",
          tenant(),
          f.get("id"));
    else {
      var last = page.getLast();
      jdbc.update(
          "UPDATE collab.network_group_freeze_dispatch SET after_position=?,after_node=?,complete=? WHERE tenant_id=? AND id=?",
          last.get("position"),
          last.get("node"),
          page.size() < limit,
          tenant(),
          f.get("id"));
    }
  }

  @Override
  public void updateCommitted(UUID update) {
    locked(
        () -> {
          authority.requireWorker();
          jdbc.update(
              "UPDATE collab.network_group_state_update SET committed=true WHERE tenant_id=? AND id=?",
              tenant(),
              update);
          return null;
        });
  }

  private void audit(Actor a, String action, String subject, String reason) {
    jdbc.update(
        "INSERT INTO collab.network_group_audit(id,tenant_id,realm,action,actor_user,subject,reason,created_at) VALUES (?,?,?,?,?,?,?,?)",
        UuidV7.next(),
        tenant(),
        a.address().realm(),
        action,
        a.user(),
        subject,
        reason,
        Timestamp.from(now()));
  }
}
