package auriga.collab.network.group;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.network.group.*;
import auriga.collab.api.network.group.NetworkGroups.*;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

@ExtendWith(PostgresExtension.class)
class NetworkGroupPostgresTest {
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  UUID tenant = UUID.randomUUID();
  Instant now = Instant.parse("2026-10-25T00:00:00Z");
  final Actor alice = actor("alice", "10", "0");
  Actor current = alice;
  boolean admin = true, approved = true, worker = true, available = true, full = false;
  boolean replyProof = true;
  boolean unicastAllowed = true;
  String deniedTarget;
  String unavailableTarget;
  boolean sourceDenied;
  int storeAttempts;
  int failStoreAt;
  final Map<Address, String> joinOwners = new HashMap<>();
  final Set<Address> closedJoinTargets = new HashSet<>();
  final Set<Address> unpermittedJoinTargets = new HashSet<>();
  Actor lastJoinApprover;
  Instant acceptedAt;
  List<Address> broadcast = List.of(address("20", "0"), address("21", "0"));
  NetworkGroupService service;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkGroupPostgresTest.class, "groups_owner", "groups_app", "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    var owner = new JdbcTemplate(roles.owner().dataSource());
    owner.execute(
        "CREATE TABLE collab.network_group_test_sink(tenant_id uuid NOT NULL,message_id uuid NOT NULL,node text NOT NULL,PRIMARY KEY(tenant_id,message_id,node))");
    owner.execute("SELECT auriga_enable_tenant_rls('network_group_test_sink')");
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    manager = new TenantTransactionManager(roles.runtime().dataSource());
  }

  @BeforeEach
  void setup() {
    var authority =
        new NetworkGroupAuthority() {
          public Actor current() {
            return current;
          }

          public void checkSource(Actor a) {
            if (!available) throw new AurigaException(auriga.common.error.CommonErrors.UNAVAILABLE);
            if (sourceDenied) throw new AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
          }

          public void checkSend(
              Actor a, String group, int nodes, int locals, int bytes, boolean ephemeral) {
            checkSource(a);
          }

          public void checkTarget(Actor s, Address a, boolean b) {
            checkSource(s);
            if (a.node().equals(unavailableTarget))
              throw new AurigaException(auriga.common.error.CommonErrors.UNAVAILABLE);
            if (a.node().equals(deniedTarget))
              throw new AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
          }

          public TargetAccess dispatchTarget(Actor s, Address a, boolean b) {
            checkSource(s);
            if (a.node().equals(deniedTarget)) return TargetAccess.REVOKED;
            checkTarget(s, a, b);
            return TargetAccess.ALLOWED;
          }

          public void checkHumanUnicast(Actor a, Address target, int bytes) {
            if (!unicastAllowed)
              throw new AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
          }

          public void checkHistoricalRead(Actor a, Item original) {
            checkSource(a);
          }

          public void requireHumanUi(Actor a) {
            if (a.sender() != Sender.HUMAN)
              throw new AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
          }

          public void requireAdministrator() {
            if (!admin) throw new AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
          }

          public void requireWorker() {
            if (!worker) throw new AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
          }

          public void allowCreate(Actor a) {
            checkSource(a);
          }

          public List<Address> broadcastSnapshot(String realm) {
            return broadcast;
          }

          public UUID reserveInvitation(Actor s, Address a, String g, UUID i, Instant e) {
            checkSource(s);
            return UUID.randomUUID();
          }

          public void approveJoin(
              Actor a,
              Address invited,
              Address selected,
              UUID c,
              UUID i,
              UUID m,
              long r,
              boolean committed) {
            lastJoinApprover = a;
            if (!approved
                || (joinOwners.containsKey(selected) && !a.user().equals(joinOwners.get(selected)))
                || (!selected.equals(a.address()) && !a.user().equals(joinOwners.get(selected)))
                || closedJoinTargets.contains(selected)
                || unpermittedJoinTargets.contains(selected))
              throw new AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
          }

          public Instant verifyReceipt(Actor a, UUID m, Recipient r, Outcome o) {
            checkSource(a);
            return o == Outcome.ACCEPTED ? acceptedAt == null ? now : acceptedAt : null;
          }

          public void verifyResponse(
              Actor a, Item original, Recipient r, String key, int revision, Response response) {
            if (!replyProof) throw new AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
          }
        };
    service =
        new NetworkGroupService(
            jdbc,
            manager,
            authority,
            new NetworkGroupMailbox() {
              public Stored humanUnicast(UUID message, Actor source, HumanSend request) {
                if (full) throw new AurigaException(auriga.common.error.CommonErrors.UNAVAILABLE);
                jdbc.update(
                    "INSERT INTO collab.network_group_test_sink(tenant_id,message_id,node) VALUES (?,?,?)",
                    tenant,
                    message,
                    request.target().node());
                return new Stored(message, 1, 1, "complete");
              }

              public void preflight(Item i) {
                assertTrue(i.inboxOnly());
              }

              public void store(Item i) {
                storeAttempts++;
                if (full || storeAttempts == failStoreAt)
                  throw new AurigaException(auriga.common.error.CommonErrors.UNAVAILABLE);
                // Real database mutation proves rollback of mailbox writes and plan progress
                // together.
                jdbc.update(
                    "INSERT INTO collab.network_group_test_sink(tenant_id,message_id,node) VALUES (?,?,?)",
                    tenant,
                    i.messageId(),
                    i.recipients().getFirst().address().node());
              }
            },
            new Clock() {
              public ZoneId getZone() {
                return ZoneOffset.UTC;
              }

              public Clock withZone(ZoneId z) {
                return this;
              }

              public Instant instant() {
                return now;
              }
            });
  }

  static Address address(String n, String l) {
    return new Address("2", n, l, l.equals("0") ? "system" : "session");
  }

  static Actor actor(String u, String n, String l) {
    return new Actor(
        u, address(n, l), l.equals("0") ? Sender.HUMAN : Sender.AGENT, "binding-" + n + "-" + l, u);
  }

  <T> T run(Supplier<T> work) {
    return TenantContext.call(
        tenant, () -> new TransactionTemplate(manager).execute(s -> work.get()));
  }

  <T> T call(Supplier<T> work) {
    return TenantContext.call(tenant, work::get);
  }

  void denied(String code, Runnable work) {
    assertEquals(
        code,
        assertThrows(
                AurigaException.class,
                () ->
                    call(
                        () -> {
                          work.run();
                          return null;
                        }))
            .errorCode()
            .code());
  }

  Send request(String group, String key) {
    return new Send(
        group,
        UUID.randomUUID(),
        key,
        "external text",
        Intent.REQUEST,
        Mode.ATOMIC,
        now.plusSeconds(3600),
        null);
  }

  Send human(Send r) {
    UUID i = call(() -> service.humanIntent(r));
    return new Send(
        r.group(), r.thread(), r.key(), r.text(), r.intent(), r.mode(), r.expiresAt(), i);
  }

  Group create() {
    return call(() -> service.create("team"));
  }

  Group add(Group g, Actor target) {
    current = alice;
    var invitation =
        call(() -> service.invite(g.local(), g.revision(), target.address(), now.plusSeconds(600)));
    current = target;
    return call(() -> service.join(invitation.id(), invitation.revision()));
  }

  @Test
  void acceptedRecipientCannotRestoreResponseAfterLeavingAndRejoining() {
    var bob = actor("bob", "20", "1");
    var g = add(create(), bob);
    current = alice;
    var sent = call(() -> service.send(human(request(g.local(), "before-leave"))));
    current = bob;
    var original = call(() -> service.fetch(sent.messageId())).recipients().getFirst();
    call(
        () -> {
          service.receipt(sent.messageId(), original.id(), Outcome.ACCEPTED);
          return null;
        });
    var left = call(() -> service.leave(g.local(), g.revision()));
    assertEquals(
        "unavailable",
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT response FROM collab.network_group_recipient WHERE tenant_id=? AND id=?",
                    String.class,
                    tenant,
                    original.id())));
    var rejoined = add(left, bob);
    assertNotEquals(
        original.membershipId(),
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT id FROM collab.network_group_member WHERE tenant_id=? AND realm='2' AND group_local=? AND node='20' AND local='1' AND active",
                    UUID.class,
                    tenant,
                    g.local())));
    denied(
        "network.target_unavailable",
        () ->
            service.respond(
                sent.messageId(), original.id(), "after-rejoin", 1, Response.RESPONDED));
    assertEquals(
        "accepted",
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT outcome FROM collab.network_group_recipient WHERE tenant_id=? AND id=?",
                    String.class,
                    tenant,
                    original.id())));
    assertEquals(
        "unavailable",
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT response FROM collab.network_group_recipient WHERE tenant_id=? AND id=?",
                    String.class,
                    tenant,
                    original.id())));
    assertEquals(
        0,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_response_operation WHERE tenant_id=? AND recipient_id=?",
                    Integer.class,
                    tenant,
                    original.id())));
    // A fresh snapshot for the current instance still grants its own narrow response permission.
    current = alice;
    var fresh = call(() -> service.send(human(request(rejoined.local(), "new-instance"))));
    current = bob;
    var recipient = call(() -> service.fetch(fresh.messageId())).recipients().getFirst();
    call(
        () -> {
          service.respond(fresh.messageId(), recipient.id(), "fresh", 1, Response.RESPONDED);
          service.respond(fresh.messageId(), recipient.id(), "fresh", 1, Response.RESPONDED);
          return null;
        });
  }

  @Test
  void committedResponseRetryAfterLeaveDoesNotGrantANewRevision() {
    var bob = actor("bob", "20", "1");
    var g = add(create(), bob);
    current = alice;
    var sent = call(() -> service.send(human(request(g.local(), "history"))));
    current = bob;
    var recipient = call(() -> service.fetch(sent.messageId())).recipients().getFirst();
    call(
        () -> {
          service.receipt(sent.messageId(), recipient.id(), Outcome.ACCEPTED);
          service.respond(sent.messageId(), recipient.id(), "committed", 1, Response.RESPONDED);
          return null;
        });
    var left = call(() -> service.leave(g.local(), g.revision()));
    add(left, bob);
    replyProof = false;
    denied(
        "common.forbidden",
        () ->
            service.respond(sent.messageId(), recipient.id(), "committed", 1, Response.RESPONDED));
    replyProof = true;
    call(
        () -> {
          service.respond(sent.messageId(), recipient.id(), "committed", 1, Response.RESPONDED);
          return null;
        });
    denied(
        "network.target_unavailable",
        () ->
            service.respond(
                sent.messageId(), recipient.id(), "new-revision", 2, Response.RESPONDED));
    assertEquals(
        1,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT response_revision FROM collab.network_group_recipient WHERE tenant_id=? AND id=?",
                    Integer.class,
                    tenant,
                    recipient.id())));
  }

  Stored pagedBroadcast(String key) {
    broadcast =
        java.util.stream.IntStream.range(100, 133).mapToObj(n -> address("" + n, "0")).toList();
    grant(Scope.USER, "alice", null, null, null);
    var r = request("1", key);
    return call(
        () ->
            service.send(
                human(
                    new Send(
                        r.group(),
                        r.thread(),
                        r.key(),
                        r.text(),
                        r.intent(),
                        Mode.PAGED,
                        r.expiresAt(),
                        null))));
  }

  @Test
  void firstRevokedPagedTargetDoesNotBlockOtherNodes() {
    revokedPagedTarget("100");
  }

  @Test
  void middleRevokedPagedTargetDoesNotBlockOtherNodes() {
    revokedPagedTarget("116");
  }

  private void revokedPagedTarget(String revoked) {
    var sent = pagedBroadcast("revoked-target");
    deniedTarget = revoked;
    assertEquals(31, call(() -> service.dispatch(sent.messageId())));
    assertEquals(1, call(() -> service.dispatch(sent.messageId())));
    assertEquals(0, call(() -> service.dispatch(sent.messageId())));
    assertEquals(
        32,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_test_sink WHERE tenant_id=? AND message_id=?",
                    Integer.class,
                    tenant,
                    sent.messageId())));
    assertEquals(
        0,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_test_sink WHERE tenant_id=? AND message_id=? AND node=?",
                    Integer.class,
                    tenant,
                    sent.messageId(),
                    revoked)));
    var before = call(() -> service.status(sent.messageId()));
    assertEquals(33, before.total());
    assertEquals(1L, before.receipts().get("rejected"));
    assertEquals(32L, before.receipts().get("pending"));
    for (var target : broadcast)
      if (!target.node().equals(revoked)) {
        current = actor("reader", target.node(), "0");
        var recipient = call(() -> service.fetch(sent.messageId())).recipients().getFirst();
        call(
            () -> {
              service.receipt(sent.messageId(), recipient.id(), Outcome.ACCEPTED);
              return null;
            });
      }
    current = alice;
    var after = call(() -> service.status(sent.messageId()));
    assertEquals(33, after.total());
    assertEquals(32L, after.receipts().get("accepted"));
    assertEquals(1L, after.receipts().get("rejected"));
  }

  @Test
  void temporaryTargetFailureAndSourceDenialDoNotBecomeRecipientRejection() {
    var sent = pagedBroadcast("temporary");
    unavailableTarget = "116";
    denied("common.unavailable", () -> service.dispatch(sent.messageId()));
    assertTrue(storeAttempts > 0);
    assertUntouchedPage(sent.messageId());
    unavailableTarget = null;
    sourceDenied = true;
    denied("common.forbidden", () -> service.dispatch(sent.messageId()));
    sourceDenied = false;
    assertUntouchedPage(sent.messageId());
    assertEquals(32, call(() -> service.dispatch(sent.messageId())));
    assertEquals(1, call(() -> service.dispatch(sent.messageId())));
  }

  @Test
  void mailboxFailureAfterPartialPageWritesRollsBackProgressAndTerminalDecisions() {
    var sent = pagedBroadcast("mid-page");
    deniedTarget = "100";
    failStoreAt = 3;
    denied("common.unavailable", () -> service.dispatch(sent.messageId()));
    assertEquals(3, storeAttempts);
    assertUntouchedPage(sent.messageId());
    failStoreAt = 0;
    assertEquals(31, call(() -> service.dispatch(sent.messageId())));
    assertEquals(1, call(() -> service.dispatch(sent.messageId())));
    assertEquals(0, call(() -> service.dispatch(sent.messageId())));
  }

  private void assertUntouchedPage(UUID message) {
    assertEquals(
        0,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_test_sink WHERE tenant_id=? AND message_id=?",
                    Integer.class,
                    tenant,
                    message)));
    assertEquals(
        0,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_recipient WHERE tenant_id=? AND message_id=? AND dispatched",
                    Integer.class,
                    tenant,
                    message)));
    var status = call(() -> service.status(message));
    assertEquals(33L, status.receipts().get("pending"));
    assertEquals(0L, status.receipts().getOrDefault("rejected", 0L));
  }

  @Test
  void sysInvitationCanSelectExistingLocalAndFixesItsTargetForRetries() {
    var g = create();
    var selected = address("20", "1");
    joinOwners.put(selected, "bob");
    var invitation =
        call(
            () ->
                service.invite(g.local(), g.revision(), address("20", "0"), now.plusSeconds(600)));
    var approver = actor("bob", "20", "0");
    current = approver;
    var joined = call(() -> service.join(invitation.id(), invitation.revision(), selected));
    assertEquals(approver, lastJoinApprover);
    assertEquals(approver, current);
    assertEquals(
        "1",
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT local FROM collab.network_group_member WHERE tenant_id=? AND id=?",
                    String.class,
                    tenant,
                    invitation.membershipId())));
    assertEquals(
        "0",
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT local FROM collab.network_group_invitation WHERE tenant_id=? AND id=?",
                    String.class,
                    tenant,
                    invitation.id())));
    assertEquals(
        joined, call(() -> service.join(invitation.id(), invitation.revision(), selected)));
    current = actor("mallory", "20", "0");
    denied(
        "common.forbidden", () -> service.join(invitation.id(), invitation.revision(), selected));
    current = approver;
    joinOwners.put(address("20", "2"), "bob");
    denied(
        "network.conflict",
        () -> service.join(invitation.id(), invitation.revision(), address("20", "2")));
    assertEquals(
        2,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_member WHERE tenant_id=? AND group_local=? AND active",
                    Integer.class,
                    tenant,
                    g.local())));
    current = alice;
    var sent = call(() -> service.send(human(request(g.local(), "selected-member"))));
    current = actor("bob", "20", "1");
    var actual = call(() -> service.fetch(sent.messageId())).recipients().getFirst();
    assertEquals(selected, actual.address());
    assertEquals(invitation.membershipId(), actual.membershipId());
  }

  @Test
  void selectedJoinTargetNeedsSameNodeOwnershipRegistrationLifecycleAndDurablePermission() {
    var g = create();
    var selected = address("20", "1");
    joinOwners.put(selected, "bob");
    joinOwners.put(address("20", "2"), "charlie");
    var invitation =
        call(
            () ->
                service.invite(g.local(), g.revision(), address("20", "0"), now.plusSeconds(600)));
    current = actor("bob", "20", "0");
    denied(
        "network.group_not_available",
        () ->
            service.join(
                invitation.id(), invitation.revision(), new Address("3", "20", "1", "session")));
    denied(
        "network.group_not_available",
        () -> service.join(invitation.id(), invitation.revision(), address("21", "1")));
    denied(
        "common.forbidden",
        () -> service.join(invitation.id(), invitation.revision(), address("20", "2")));
    denied(
        "common.forbidden",
        () -> service.join(invitation.id(), invitation.revision(), address("20", "3")));
    closedJoinTargets.add(selected);
    denied(
        "common.forbidden", () -> service.join(invitation.id(), invitation.revision(), selected));
    closedJoinTargets.clear();
    unpermittedJoinTargets.add(selected);
    denied(
        "common.forbidden", () -> service.join(invitation.id(), invitation.revision(), selected));
    unpermittedJoinTargets.clear();
    current = actor("bob", "21", "0");
    denied(
        "network.group_not_available",
        () -> service.join(invitation.id(), invitation.revision(), selected));
    current =
        new Actor(
            "bob", new Address("3", "20", "0", "system"), Sender.HUMAN, "other-realm-ui", "bob");
    denied(
        "network.group_not_available",
        () -> service.join(invitation.id(), invitation.revision(), selected));
    current = actor("mallory", "20", "0");
    denied(
        "common.forbidden", () -> service.join(invitation.id(), invitation.revision(), selected));
    current = actor("bob", "20", "2");
    denied(
        "network.group_not_available",
        () -> service.join(invitation.id(), invitation.revision(), selected));
    current = actor("bob", "20", "0");
    approved = false;
    denied(
        "common.forbidden", () -> service.join(invitation.id(), invitation.revision(), selected));
    approved = true;
    assertNotNull(call(() -> service.join(invitation.id(), invitation.revision(), selected)));
  }

  @Test
  void exactLocalInvitationCannotBeRetargetedByItsOwner() {
    var g = create();
    var target = address("20", "1");
    joinOwners.put(target, "bob");
    joinOwners.put(address("20", "2"), "bob");
    var invitation =
        call(() -> service.invite(g.local(), g.revision(), target, now.plusSeconds(600)));
    current = actor("bob", "20", "0");
    denied(
        "network.group_not_available",
        () -> service.join(invitation.id(), invitation.revision(), address("20", "2")));
    denied(
        "network.group_not_available",
        () -> service.join(invitation.id(), invitation.revision(), address("20", "0")));
    assertNotNull(call(() -> service.join(invitation.id(), invitation.revision(), target)));
  }

  @Test
  void membershipConsentCasAndRejoinDoNotReviveOldSnapshot() {
    var g = create();
    var bob = actor("bob", "20", "1");
    var invitation =
        call(() -> service.invite(g.local(), g.revision(), bob.address(), now.plusSeconds(600)));
    current = bob;
    approved = false;
    denied("common.forbidden", () -> service.join(invitation.id(), invitation.revision()));
    approved = true;
    var joined = call(() -> service.join(invitation.id(), invitation.revision()));
    assertEquals(joined, call(() -> service.join(invitation.id(), invitation.revision())));
    current = alice;
    var sent = call(() -> service.send(human(request(g.local(), "k"))));
    current = bob;
    call(() -> service.leave(g.local(), joined.revision()));
    current = alice;
    var newest = new Group("2", g.local(), joined.revision() + 1);
    add(newest, bob);
    denied("network.target_unavailable", () -> service.fetch(sent.messageId()));
  }

  @Test
  void humanIntentIsBoundAndOneUseButSameKeyRetries() {
    var g = create();
    var r = human(request(g.local(), "k"));
    var s = call(() -> service.send(r));
    assertEquals(s, call(() -> service.send(r)));
    var changed =
        new Send(
            r.group(),
            r.thread(),
            "other",
            r.text(),
            r.intent(),
            r.mode(),
            r.expiresAt(),
            r.humanIntent());
    denied("network.sender_mismatch", () -> service.send(changed));
    denied("network.sender_mismatch", () -> service.send(request(g.local(), "no-intent")));
  }

  @Test
  void humanIntentExpiryAndDifferentBodyFailClosed() {
    var g = create();
    var r = human(request(g.local(), "k"));
    now = now.plusSeconds(61);
    denied("network.sender_mismatch", () -> service.send(r));
    var fresh = human(request(g.local(), "fresh"));
    denied(
        "network.sender_mismatch",
        () ->
            service.send(
                new Send(
                    fresh.group(),
                    fresh.thread(),
                    fresh.key(),
                    "changed",
                    fresh.intent(),
                    fresh.mode(),
                    fresh.expiresAt(),
                    fresh.humanIntent())));
  }

  @Test
  void nonMembersAndAdminsOnlyCannotBorrowUserMembership() {
    var g = create();
    current = actor("alice", "10", "1");
    denied("network.group_not_available", () -> service.send(request(g.local(), "k")));
    current = alice;
    var joined = add(g, actor("bob", "20", "1"));
    current = alice;
    call(() -> service.policy(g.local(), joined.revision(), true, false));
    current = actor("bob", "20", "1");
    denied("network.group_publish_forbidden", () -> service.send(request(g.local(), "k")));
  }

  Grant grant(Scope scope, String u, String n, String l, String b) {
    return call(
        () ->
            service.grant(
                new GrantRequest(
                    scope, u, n, l, b, now.plusSeconds(3600), 10000, 16384, "test grant")));
  }

  @Test
  void adminHasNoImplicitBroadcastAndAgentCannotInheritHumanGrant() {
    denied("network.broadcast_forbidden", () -> service.send(human(request("1", "none"))));
    grant(Scope.USER, "alice", null, null, null);
    current = actor("alice", "10", "1");
    denied("network.broadcast_forbidden", () -> service.send(request("1", "agent")));
    var agent = current;
    current = alice;
    grant(Scope.AGENT, "alice", "10", "1", agent.binding());
    current = agent;
    assertEquals(2, call(() -> service.send(request("1", "exact"))).totalMembers());
    current = actor("alice", "10", "2");
    denied("network.broadcast_forbidden", () -> service.send(request("1", "other")));
  }

  @Test
  void revokeAndRecallPreserveAcceptedAndCreateIndependentUpdates() {
    var gr = grant(Scope.USER, "alice", null, null, null);
    var sent = call(() -> service.send(human(request("1", "k"))));
    current = actor("bob", "20", "0");
    var item = call(() -> service.fetch(sent.messageId()));
    call(
        () -> {
          service.receipt(sent.messageId(), item.recipients().getFirst().id(), Outcome.ACCEPTED);
          return null;
        });
    current = alice;
    call(() -> service.revoke(gr.id(), gr.revision(), "revoke", "reason"));
    assertNotNull(call(() -> service.status(sent.messageId())));
    current = actor("bob", "20", "0");
    assertNotNull(call(() -> service.fetch(sent.messageId())));
    current = alice;
    assertEquals(2L, call(() -> service.recall(sent.messageId(), 1, "recall", "reason")));
    assertEquals(2L, call(() -> service.recall(sent.messageId(), 1, "recall", "reason")));
    var stats = call(() -> service.status(sent.messageId()));
    assertEquals(1L, stats.receipts().get("accepted"));
    assertEquals(1L, stats.receipts().get("rejected"));
    assertEquals(2, call(() -> service.pendingUpdates(128)).size());
    current = actor("bob", "20", "0");
    denied("network.message_recalled", () -> service.fetch(sent.messageId()));
  }

  @Test
  void pagingRollbackRetryAndImmutableNodeSnapshot() {
    broadcast =
        java.util.stream.IntStream.range(100, 133).mapToObj(n -> address("" + n, "0")).toList();
    grant(Scope.USER, "alice", null, null, null);
    denied("network.requires_paged_dispatch", () -> service.send(human(request("1", "atomic"))));
    var r = request("1", "paged");
    var p =
        human(
            new Send(
                r.group(),
                r.thread(),
                r.key(),
                r.text(),
                r.intent(),
                Mode.PAGED,
                r.expiresAt(),
                null));
    var sent = call(() -> service.send(p));
    assertEquals(33, sent.totalNodes());
    broadcast = List.of(address("999", "0"));
    full = true;
    denied("common.unavailable", () -> service.dispatch(sent.messageId()));
    full = false;
    assertEquals(32, call(() -> service.dispatch(sent.messageId())));
    assertEquals(1, call(() -> service.dispatch(sent.messageId())));
    assertEquals(0, call(() -> service.dispatch(sent.messageId())));
    assertEquals(
        33,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_test_sink WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
  }

  @Test
  void freezeExpiryAndOrganizationFailureStopDispatchAndRead() {
    var g = create();
    var sent = call(() -> service.send(human(request(g.local(), "k"))));
    available = false;
    denied("common.unavailable", () -> service.fetch(sent.messageId()));
    available = true;
    call(() -> service.freeze(Scope.USER, "alice", 0, true, "freeze", "reason"));
    denied("network.target_unavailable", () -> service.fetch(sent.messageId()));
    call(() -> service.freeze(Scope.USER, "alice", 1, false, "unfreeze", "reason"));
    denied("network.target_unavailable", () -> service.fetch(sent.messageId()));
  }

  @Test
  void responseRevisionCountsAndRecipientIsolation() {
    grant(Scope.USER, "alice", null, null, null);
    var sent = call(() -> service.send(human(request("1", "k"))));
    current = actor("bob", "20", "0");
    var item = call(() -> service.fetch(sent.messageId()));
    var id = item.recipients().getFirst().id();
    call(
        () -> {
          service.respond(sent.messageId(), id, "r", 1, Response.RESPONDED);
          service.respond(sent.messageId(), id, "r", 1, Response.RESPONDED);
          return null;
        });
    for (int rev = 2; rev <= 4; rev++) {
      final int v = rev;
      call(
          () -> {
            service.respond(sent.messageId(), id, "r" + v, v, Response.RESPONDED);
            return null;
          });
    }
    call(
        () -> {
          service.respond(sent.messageId(), id, "r", 1, Response.RESPONDED);
          return null;
        });
    denied(
        "network.quota_exceeded",
        () -> service.respond(sent.messageId(), id, "r5", 5, Response.RESPONDED));
    current = actor("eve", "21", "0");
    denied(
        "network.target_unavailable",
        () -> service.receipt(sent.messageId(), id, Outcome.ACCEPTED));
    current = alice;
    assertEquals(1L, call(() -> service.status(sent.messageId())).responses().get("responded"));
  }

  @Test
  void tenantRlsAndUnauthorizedManagement() {
    var g = create();
    var original = tenant;
    tenant = UUID.randomUUID();
    denied(
        "network.group_not_available", () -> service.policy(g.local(), g.revision(), true, false));
    tenant = original;
    admin = false;
    denied("common.forbidden", () -> service.freeze(Scope.USER, "alice", 0, true, "k", "reason"));
    worker = false;
    denied("common.forbidden", () -> service.pendingUpdates(128));
  }

  @Test
  void activeBroadcastGrantCannotBeUsedAfterExpiry() {
    grant(Scope.USER, "alice", null, null, null);
    now = now.plusSeconds(3601);
    denied("network.broadcast_forbidden", () -> service.send(human(request("1", "expired-grant"))));
  }

  @Test
  void oneUnauthorizedAtomicTargetRollsBackTheWholeSnapshot() {
    var g = add(create(), actor("bob", "20", "1"));
    current = alice;
    deniedTarget = "20";
    var r = human(request(g.local(), "forbidden-target"));
    denied("common.forbidden", () -> service.send(r));
    assertEquals(
        0,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_plan WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
    deniedTarget = null;
    assertEquals(2, call(() -> service.send(r)).totalMembers());
  }

  @Test
  void humanSysUnicastBindsTargetAndCannotBypassRelationship() {
    var r =
        new HumanSend(
            address("20", "0"),
            UUID.randomUUID(),
            "human",
            "hello",
            Intent.INFORMATION,
            now.plusSeconds(60),
            null);
    var intent = call(() -> service.humanUnicastIntent(r));
    var bound =
        new HumanSend(r.target(), r.thread(), r.key(), r.text(), r.intent(), r.expiresAt(), intent);
    unicastAllowed = false;
    denied("common.forbidden", () -> service.sendHumanUnicast(bound));
    unicastAllowed = true;
    var result = call(() -> service.sendHumanUnicast(bound));
    assertEquals(result, call(() -> service.sendHumanUnicast(bound)));
    denied(
        "network.conflict",
        () ->
            service.sendHumanUnicast(
                new HumanSend(
                    address("21", "0"),
                    r.thread(),
                    r.key(),
                    r.text(),
                    r.intent(),
                    r.expiresAt(),
                    intent)));
    current = actor("alice", "10", "1");
    denied("network.sender_mismatch", () -> service.sendHumanUnicast(bound));
  }

  @Test
  void groupCapacityDelegationDeletionAndMonotonicIds() {
    var g = create();
    denied("common.validation_failed", () -> service.capacity(g.local(), g.revision(), 1025, 256));
    var expanded = call(() -> service.capacity(g.local(), g.revision(), 1024, 256));
    var invitation =
        call(
            () ->
                service.invite(
                    g.local(), expanded.revision(), address("20", "0"), now.plusSeconds(60)));
    current = actor("bob", "20", "0");
    var joined = call(() -> service.join(invitation.id(), invitation.revision()));
    denied(
        "network.group_publish_forbidden",
        () -> service.delegate(g.local(), joined.revision(), invitation.membershipId(), true));
    current = alice;
    var delegated =
        call(() -> service.delegate(g.local(), joined.revision(), invitation.membershipId(), true));
    var restricted = call(() -> service.policy(g.local(), delegated.revision(), true, false));
    current = actor("bob", "20", "0");
    assertEquals(
        2, call(() -> service.send(human(request(g.local(), "admin-speech")))).totalMembers());
    current = alice;
    call(() -> service.policy(g.local(), restricted.revision(), true, true));
    denied("network.group_not_available", () -> service.send(human(request(g.local(), "deleted"))));
    assertEquals(
        new java.math.BigInteger(g.local()).add(java.math.BigInteger.ONE).toString(),
        create().local());
  }

  @Test
  void cancelExpiryAndUpdateCommitAreBoundedAndIdempotent() {
    var g = create();
    var sent = call(() -> service.send(human(request(g.local(), "cancel"))));
    call(
        () -> {
          service.cancel(sent.messageId());
          service.cancel(sent.messageId());
          return null;
        });
    assertEquals(1L, call(() -> service.status(sent.messageId())).receipts().get("canceled"));
    var expiring = call(() -> service.send(human(request(g.local(), "expiry"))));
    now = now.plusSeconds(3601);
    assertEquals(0, call(() -> service.dispatch(expiring.messageId())));
    assertEquals(1L, call(() -> service.status(expiring.messageId())).receipts().get("expired"));
    call(() -> service.freeze(Scope.USER, "alice", 0, true, "freeze", "reason"));
    var updates = call(() -> service.pendingUpdates(1));
    assertEquals(1, updates.size());
    assertEquals(Scope.USER, updates.getFirst().scope());
    assertTrue(updates.getFirst().frozen());
    call(
        () -> {
          service.updateCommitted(updates.getFirst().id());
          service.updateCommitted(updates.getFirst().id());
          return null;
        });
    denied("common.validation_failed", () -> service.pendingUpdates(129));
  }

  @Test
  void ephemeralHasNoBodyPlanOrReceiptAndRechecksMembership() {
    var g = create();
    var r =
        new Send(
            g.local(),
            UUID.randomUUID(),
            "progress",
            "online",
            Intent.INFORMATION,
            Mode.ATOMIC,
            now.plusSeconds(30),
            null);
    var wrong = human(r);
    denied(
        "network.sender_mismatch", () -> service.prepareEphemeral(wrong, ProgressKind.ONLINE, 1));
    var intent = call(() -> service.humanEphemeralIntent(r, ProgressKind.ONLINE, 1));
    var bound =
        new Send(
            r.group(), r.thread(), r.key(), r.text(), r.intent(), r.mode(), r.expiresAt(), intent);
    assertEquals(
        1, call(() -> service.prepareEphemeral(bound, ProgressKind.ONLINE, 1)).recipients().size());
    assertEquals(
        0,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_plan WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
    call(() -> service.leave(g.local(), g.revision()));
    denied(
        "network.group_not_available",
        () -> service.prepareEphemeral(bound, ProgressKind.ONLINE, 1));
  }

  @Test
  void concurrentWorkersCommitEachNodeOnce() throws Exception {
    broadcast =
        java.util.stream.IntStream.range(100, 133).mapToObj(n -> address("" + n, "0")).toList();
    grant(Scope.USER, "alice", null, null, null);
    var r = request("1", "workers");
    var sent =
        call(
            () ->
                service.send(
                    human(
                        new Send(
                            r.group(),
                            r.thread(),
                            r.key(),
                            r.text(),
                            r.intent(),
                            Mode.PAGED,
                            r.expiresAt(),
                            null))));
    try (var pool = Executors.newFixedThreadPool(2)) {
      var tasks =
          List.<Callable<Integer>>of(
              () -> call(() -> service.dispatch(sent.messageId())),
              () -> call(() -> service.dispatch(sent.messageId())));
      var results = pool.invokeAll(tasks);
      assertEquals(33, results.get(0).get() + results.get(1).get());
    }
  }

  @Test
  void delayedReceiptPreservesProvenAcceptanceBeforeRecall() {
    grant(Scope.USER, "alice", null, null, null);
    var sent = call(() -> service.send(human(request("1", "late"))));
    current = actor("bob", "20", "0");
    var item = call(() -> service.fetch(sent.messageId()));
    acceptedAt = now;
    now = now.plusSeconds(1);
    current = alice;
    call(() -> service.recall(sent.messageId(), 1, "recall", "reason"));
    current = actor("bob", "20", "0");
    call(
        () -> {
          service.receipt(sent.messageId(), item.recipients().getFirst().id(), Outcome.ACCEPTED);
          return null;
        });
    current = alice;
    assertEquals(1L, call(() -> service.status(sent.messageId())).receipts().get("accepted"));
  }

  @Test
  void expiredInvitationGrantAndUnfreezeDoNotRestoreAuthority() {
    var g = create();
    var invitation =
        call(
            () -> service.invite(g.local(), g.revision(), address("20", "1"), now.plusSeconds(30)));
    now = now.plusSeconds(31);
    current = actor("bob", "20", "1");
    denied("network.contact_expired", () -> service.join(invitation.id(), invitation.revision()));
    current = alice;
    grant(Scope.USER, "alice", null, null, null);
    call(() -> service.freeze(Scope.USER, "alice", 0, true, "freeze", "reason"));
    call(() -> service.freeze(Scope.USER, "alice", 1, false, "unfreeze", "reason"));
    denied("network.broadcast_forbidden", () -> service.send(human(request("1", "after"))));
    now = now.plusSeconds(3601);
    denied("network.broadcast_forbidden", () -> service.send(human(request("1", "expired"))));
  }

  @Test
  void privateReplyNeedsEvidenceAndCannotCountAWholeNode() {
    grant(Scope.USER, "alice", null, null, null);
    var sent = call(() -> service.send(human(request("1", "k"))));
    current = actor("bob", "20", "0");
    var item = call(() -> service.fetch(sent.messageId()));
    replyProof = false;
    denied(
        "common.forbidden",
        () ->
            service.respond(
                sent.messageId(),
                item.recipients().getFirst().id(),
                "reply",
                1,
                Response.RESPONDED));
    current = alice;
    assertEquals(2L, call(() -> service.status(sent.messageId())).responses().get("pending"));
  }

  @Test
  void atomicSinkFailureRollsBackPlanIntentAndEveryRecipient() {
    var g = create();
    var r = human(request(g.local(), "rollback"));
    full = true;
    denied("common.unavailable", () -> service.send(r));
    assertEquals(
        0,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_group_plan WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
    full = false;
    assertEquals(1, call(() -> service.send(r)).totalMembers());
  }
}
