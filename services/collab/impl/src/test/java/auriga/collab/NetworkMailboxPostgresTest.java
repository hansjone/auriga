package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.NetworkSourceAuthority;
import auriga.collab.app.NetworkErrors;
import auriga.collab.app.NetworkJson;
import auriga.collab.app.NetworkMailboxService;
import auriga.collab.app.NetworkMailboxService.AttemptBeginRequest;
import auriga.collab.app.NetworkMailboxService.ContactDecideRequest;
import auriga.collab.app.NetworkMailboxService.DeliveryStored;
import auriga.collab.app.NetworkMailboxService.ExecutionAdmitRequest;
import auriga.collab.app.NetworkMailboxService.NodeTarget;
import auriga.collab.app.NetworkMailboxService.ReceiptItem;
import auriga.collab.app.NetworkMailboxService.ReceiptRequest;
import auriga.collab.app.NetworkMailboxService.StoreRequest;
import auriga.collab.app.NetworkMailboxService.StoreResult;
import auriga.collab.app.NetworkSigningKeys;
import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.security.*;
import auriga.testsupport.PostgresExtension;
import java.security.KeyPairGenerator;
import java.time.Clock;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

@ExtendWith(PostgresExtension.class)
class NetworkMailboxPostgresTest {
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static NetworkMailboxService mailbox;
  static NetworkSigningKeys keys;
  static PostgresExtension.ServiceRoles roles;
  static final AurigaPrincipal GATEWAY =
      new AurigaPrincipal(
          "svc:ai-gateway",
          TenantContext.PLATFORM_TENANT,
          PrincipalType.INTERNAL_SERVICE,
          TokenContext.SERVICE,
          null,
          null,
          null,
          List.of(),
          Set.of(),
          null,
          null,
          "test");

  @BeforeAll
  static void migrate() throws Exception {
    roles =
        PostgresExtension.serviceRoles(
            NetworkMailboxPostgresTest.class, "collab_owner", "collab_app", "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    manager = new TenantTransactionManager(roles.runtime().dataSource());
    keys = new NetworkSigningKeys(Clock.systemUTC(), 1);
    for (String purpose : List.of("business_delivery", "protocol_response", "management_state")) {
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      keys.add(
          "2",
          purpose,
          purpose,
          pair.getPublic(),
          pair.getPrivate(),
          Instant.now().minusSeconds(3600),
          Instant.now().plusSeconds(86400),
          NetworkSigningKeys.State.ACTIVE);
    }
    mailbox = new NetworkMailboxService(jdbc, manager, authority(null), keys, Clock.systemUTC());
  }

  static NetworkSourceAuthority authority(NetworkSourceAuthority.Dispatch dispatch) {
    return new NetworkSourceAuthority() {
      public Source currentSource(UUID tenant) {
        return new Source(
            "2",
            "10",
            "1",
            "session",
            "agent",
            "user-alice",
            "协作会话",
            "1",
            Instant.now(),
            "binding-test",
            "1",
            "1",
            Instant.now().plusSeconds(600));
      }

      public Instant authorizeDelivery(UUID tenant, String message, String source, String node) {
        return Instant.now().plusSeconds(600);
      }

      public Target currentTarget(UUID tenant) {
        long epoch =
            jdbc.queryForObject(
                "SELECT connection_epoch FROM collab.network_node WHERE tenant_id=? AND realm='2' AND node_id='20'",
                Long.class,
                tenant);
        return new Target(
            "2",
            "20",
            "inc-20",
            epoch,
            "nonce-test",
            "request-test",
            Instant.now().plusSeconds(600));
      }

      public Dispatch currentDispatch(UUID tenant) {
        return dispatch;
      }
    };
  }

  static <T> T call(UUID tenant, Supplier<T> action) {
    return RequestContext.start(null, null)
        .withPrincipal(GATEWAY)
        .call(() -> TenantContext.call(tenant, action::get));
  }

  static void error(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  static <T> T sql(UUID tenant, Supplier<T> work) {
    return call(tenant, () -> new TransactionTemplate(manager).execute(s -> work.get()));
  }

  @Test
  void forgedSourceNodeAndBroadcastSourceAreRejectedBeforeWriting() {
    UUID tenant = UUID.randomUUID();
    for (String node : List.of("0", "999")) {
      error(
          "network.sender_mismatch",
          () ->
              call(
                  tenant,
                  () ->
                      mailbox.store(
                          StoreRequest.unicast(tenant, "2", node, "1", "forged", "20", "3"))));
    }
    assertEquals(
        0,
        sql(
            tenant,
            () ->
                jdbc.queryForObject("SELECT count(*) FROM collab.network_message", Integer.class)));
  }

  @Test
  void storeSameTransactionBodyMailAndRecipients() {
    UUID tenant = UUID.randomUUID();
    var stored =
        call(
            tenant,
            () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "hello", "20", "3")));
    assertEquals("platform_stored", stored.durabilityStatus());
    assertEquals("platform_stored", stored.visibleStatus());
    DeliveryStored d = stored.deliveries().getFirst();
    assertEquals(1L, d.mailSeq());
    assertEquals(1, d.recipientIds().size());
    assertEquals(
        1,
        sql(
            tenant,
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_message WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
    assertEquals(
        1,
        sql(
            tenant,
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_mail_item WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
    assertEquals(
        1,
        sql(
            tenant,
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_recipient WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
    assertEquals(
        1L,
        sql(
            tenant,
            () ->
                jdbc.queryForObject(
                    "SELECT head_seq FROM collab.network_node WHERE tenant_id=? AND node_id=?",
                    Long.class,
                    tenant,
                    "20")));
  }

  @Test
  void receiptIdempotentAndAdvancesGaplessCursor() {
    UUID tenant = UUID.randomUUID();
    StoreResult first =
        call(
            tenant,
            () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "one", "20", "1")));
    StoreResult second =
        call(
            tenant,
            () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "two", "20", "1")));
    var route = call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    DeliveryStored d1 = first.deliveries().getFirst();
    DeliveryStored d2 = second.deliveries().getFirst();

    var ack1 =
        call(
            tenant,
            () ->
                mailbox.receipt(
                    new ReceiptRequest(
                        tenant,
                        "2",
                        "20",
                        route.connectionEpoch(),
                        d1.deliveryId(),
                        d1.mailSeq(),
                        List.of(new ReceiptItem(d1.recipientIds().getFirst(), "host_accepted")))));
    assertTrue(ack1.mailItemClosed());
    assertEquals(1L, ack1.committedCursor());
    var again =
        call(
            tenant,
            () ->
                mailbox.receipt(
                    new ReceiptRequest(
                        tenant,
                        "2",
                        "20",
                        route.connectionEpoch(),
                        d1.deliveryId(),
                        d1.mailSeq(),
                        List.of(new ReceiptItem(d1.recipientIds().getFirst(), "host_accepted")))));
    assertEquals(1L, again.committedCursor());
    assertEquals("host_accepted", again.results().getFirst().transportStatus());

    // Hole at seq=2 keeps cursor at 1 until closed.
    assertEquals(
        1L, call(tenant, () -> mailbox.fetch(tenant, "2", "20", 0, 10)).route().committedCursor());

    var ack2 =
        call(
            tenant,
            () ->
                mailbox.receipt(
                    new ReceiptRequest(
                        tenant,
                        "2",
                        "20",
                        route.connectionEpoch(),
                        d2.deliveryId(),
                        d2.mailSeq(),
                        List.of(new ReceiptItem(d2.recipientIds().getFirst(), "host_accepted")))));
    assertEquals(2L, ack2.committedCursor());
  }

  @Test
  void staleEpochRejectedOnReceiptAttemptAndAdmit() {
    UUID tenant = UUID.randomUUID();
    StoreResult stored =
        call(
            tenant,
            () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "body", "20", "1")));
    var route = call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    long oldEpoch = route.connectionEpoch();
    var newer = call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    assertTrue(newer.connectionEpoch() > oldEpoch);
    DeliveryStored d = stored.deliveries().getFirst();
    UUID recipient = d.recipientIds().getFirst();

    error(
        NetworkErrors.STALE_EPOCH.code(),
        () ->
            call(
                tenant,
                () ->
                    mailbox.receipt(
                        new ReceiptRequest(
                            tenant,
                            "2",
                            "20",
                            oldEpoch,
                            d.deliveryId(),
                            d.mailSeq(),
                            List.of(new ReceiptItem(recipient, "host_accepted"))))));
    error(
        NetworkErrors.STALE_EPOCH.code(),
        () ->
            call(
                tenant,
                () ->
                    mailbox.attemptBegin(
                        new AttemptBeginRequest(
                            tenant, "2", "20", oldEpoch, d.deliveryId(), recipient, "process"))));
    error(
        NetworkErrors.STALE_EPOCH.code(),
        () ->
            call(
                tenant,
                () ->
                    mailbox.executionAdmit(
                        new ExecutionAdmitRequest(
                            tenant, "2", "20", oldEpoch, recipient, "boot-1", "host-req-1"))));

    var admit =
        call(
            tenant,
            () ->
                mailbox.executionAdmit(
                    new ExecutionAdmitRequest(
                        tenant,
                        "2",
                        "20",
                        newer.connectionEpoch(),
                        recipient,
                        "boot-1",
                        "host-req-1")));
    assertEquals("admitted", admit.result());
    var again =
        call(
            tenant,
            () ->
                mailbox.executionAdmit(
                    new ExecutionAdmitRequest(
                        tenant,
                        "2",
                        "20",
                        newer.connectionEpoch(),
                        recipient,
                        "boot-1",
                        "host-req-1")));
    assertEquals("already_admitted", again.result());
    assertEquals(admit.admissionId(), again.admissionId());
  }

  @Test
  void firstContactExpiresAndPrivacyAppearance() {
    UUID tenant = UUID.randomUUID();
    Instant past = Instant.now().minusSeconds(60);
    StoreResult stored =
        call(
            tenant,
            () ->
                mailbox.store(
                    StoreRequest.firstContact(tenant, "2", "10", "1", "hi", "20", past, false)));
    assertEquals("platform_stored", stored.durabilityStatus());
    assertEquals("request_submitted", stored.visibleStatus());
    assertTrue(stored.restrictedAppearance());
    DeliveryStored delivery = stored.deliveries().getFirst();
    assertEquals(
        0L, call(tenant, () -> mailbox.fetch(tenant, "2", "20", 0, 10)).route().committedCursor());

    var expired = call(tenant, () -> mailbox.expireContactIfDue(tenant, stored.contactId(), false));
    assertEquals("expired", expired.status());
    assertEquals("request_submitted", expired.visibleStatus());
    assertTrue(expired.restrictedAppearance());
    assertEquals("request_submitted", expired.presentationCode());

    var afterExpire = call(tenant, () -> mailbox.fetch(tenant, "2", "20", 0, 10));
    assertEquals(
        "expired", afterExpire.items().getFirst().recipients().getFirst().transportStatus());
    assertEquals(delivery.mailSeq(), afterExpire.route().committedCursor());

    var viewerExpired =
        call(tenant, () -> mailbox.expireContactIfDue(tenant, stored.contactId(), true));
    assertEquals("contact_expired", viewerExpired.visibleStatus());
    assertEquals("network.contact_expired", viewerExpired.presentationCode());

    error(
        NetworkErrors.CONTACT_EXPIRED.code(),
        () ->
            call(
                tenant,
                () ->
                    mailbox.contactDecide(
                        new ContactDecideRequest(tenant, stored.contactId(), "allowed", true))));
  }

  @Test
  void firstContactDenyWritesRevokedTombstoneAndAdvancesCursor() {
    UUID tenant = UUID.randomUUID();
    StoreResult stored =
        call(
            tenant,
            () ->
                mailbox.store(
                    StoreRequest.firstContact(
                        tenant,
                        "2",
                        "10",
                        "1",
                        "nope",
                        "20",
                        Instant.now().plusSeconds(3600),
                        true)));
    DeliveryStored delivery = stored.deliveries().getFirst();
    var denied =
        call(
            tenant,
            () ->
                mailbox.contactDecide(
                    new ContactDecideRequest(tenant, stored.contactId(), "denied", true)));
    assertEquals("denied", denied.status());
    assertEquals("contact_denied", denied.visibleStatus());
    var afterDeny = call(tenant, () -> mailbox.fetch(tenant, "2", "20", 0, 10));
    assertEquals("revoked", afterDeny.items().getFirst().recipients().getFirst().transportStatus());
    assertEquals(delivery.mailSeq(), afterDeny.route().committedCursor());
  }

  @Test
  void contactDecidePendingToAllowedWithViewer() {
    UUID tenant = UUID.randomUUID();
    StoreResult stored =
        call(
            tenant,
            () ->
                mailbox.store(
                    StoreRequest.firstContact(
                        tenant,
                        "2",
                        "10",
                        "1",
                        "please",
                        "20",
                        Instant.now().plusSeconds(3600),
                        true)));
    assertEquals("contact_pending", stored.visibleStatus());
    var decided =
        call(
            tenant,
            () ->
                mailbox.contactDecide(
                    new ContactDecideRequest(tenant, stored.contactId(), "allowed", true)));
    assertEquals("allowed", decided.status());
    assertEquals("contact_allowed", decided.visibleStatus());
    assertFalse(decided.restrictedAppearance());
    var again =
        call(
            tenant,
            () ->
                mailbox.contactDecide(
                    new ContactDecideRequest(tenant, stored.contactId(), "allowed", true)));
    assertEquals("allowed", again.status());
  }

  @Test
  void crossTenantRlsSeesZeroRows() {
    UUID a = UUID.randomUUID();
    UUID b = UUID.randomUUID();
    call(a, () -> mailbox.store(StoreRequest.unicast(a, "2", "10", "1", "secret", "20", "1")));
    assertEquals(
        0,
        sql(
            b,
            () ->
                jdbc.queryForObject("SELECT count(*) FROM collab.network_message", Integer.class)));
    assertEquals(
        0,
        sql(
            b,
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_mail_item", Integer.class)));
    assertEquals(
        0,
        sql(
            b,
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_recipient", Integer.class)));
    assertEquals(
        0,
        sql(
            b,
            () -> jdbc.queryForObject("SELECT count(*) FROM collab.network_node", Integer.class)));
    assertEquals(
        0,
        sql(
            b,
            () ->
                jdbc.queryForObject("SELECT count(*) FROM collab.network_contact", Integer.class)));
    assertEquals(0, call(b, () -> mailbox.fetch(b, "2", "20", 0, 10)).items().size());
  }

  @Test
  void fetchDoesNotChangeRecipientStatus() {
    UUID tenant = UUID.randomUUID();
    var dispatch =
        new NetworkSourceAuthority.Dispatch(
            "{\"version\":1,\"realm\":\"2\",\"node\":\"0\",\"local\":\"2\",\"type\":\"group\"}",
            "snapshot-test",
            Map.of("20.1", "member-1", "20.2", "member-2"),
            Map.of("20.1", "session", "20.2", "session"),
            null,
            null,
            null);
    var grouped =
        new NetworkMailboxService(jdbc, manager, authority(dispatch), keys, Clock.systemUTC());
    StoreResult stored =
        call(
            tenant,
            () ->
                grouped.store(
                    new StoreRequest(
                        tenant,
                        "2",
                        "10",
                        "1",
                        "message",
                        "batch",
                        null,
                        List.of(new NodeTarget("20", List.of("1", "2"))),
                        "conversation",
                        null,
                        true)));
    var page = call(tenant, () -> mailbox.fetch(tenant, "2", "20", 0, 10));
    assertEquals(1, page.items().size());
    assertEquals(2, page.items().getFirst().recipients().size());
    assertTrue(
        page.items().getFirst().recipients().stream()
            .allMatch(r -> "pending".equals(r.transportStatus())));
    assertEquals(
        2,
        sql(
            tenant,
            () ->
                jdbc.queryForObject(
                    """
                    SELECT count(*) FROM collab.network_recipient
                     WHERE tenant_id=? AND transport_status='pending'
                    """,
                    Integer.class,
                    tenant)));
    assertEquals(stored.messageId(), page.items().getFirst().messageId());
    call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    var signed =
        NetworkJson.parse(
            call(tenant, () -> mailbox.fetchSigned(tenant, "2", "20", 0, 10)).getFirst());
    assertEquals("multicast", signed.path("message").path("message_class").asString());
    assertEquals(2, signed.path("recipients").size());
  }

  @Test
  void signedFetchUsesPersistedStampAndBindsCurrentConnection() {
    UUID tenant = UUID.randomUUID();
    var stored =
        call(
            tenant,
            () ->
                mailbox.store(
                    StoreRequest.unicast(tenant, "2", "10", "1", "hello", "20", "3"),
                    "{\"ext\":{\"e2e_signature\":null,\"com.example.trace\":{\"preserved\":true}}}"));
    call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    var first =
        NetworkJson.parse(
            call(tenant, () -> mailbox.fetchSigned(tenant, "2", "20", 0, 10)).getFirst());
    assertEquals("10", first.path("source_context").path("node").asString());
    assertEquals("user-alice", first.path("source_context").path("user").asString());
    assertEquals("agent", first.path("message").path("sender_kind").asString());
    assertEquals(
        NetworkJson.digest(first.path("message")),
        first.path("source_context").path("message_digest").asString());
    assertTrue(
        first.path("message").path("ext").path("com.example.trace").path("preserved").asBoolean());
    call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    var second =
        NetworkJson.parse(
            call(tenant, () -> mailbox.fetchSigned(tenant, "2", "20", 0, 10)).getFirst());
    assertEquals(first.path("message"), second.path("message"));
    assertEquals(first.path("delivery_id"), second.path("delivery_id"));
    assertNotEquals(
        first.path("server_auth").path("audience").path("epoch"),
        second.path("server_auth").path("audience").path("epoch"));
    assertEquals(
        stored.messageId().toString(), second.path("message").path("message_id").asString());
  }

  @Test
  void clientCannotSupplyStampedFieldsAndUnwiredAuthorityFailsClosed() {
    UUID tenant = UUID.randomUUID();
    var request = StoreRequest.unicast(tenant, "2", "10", "1", "hello", "20", "3");
    for (String field :
        List.of(
            "from",
            "realm",
            "node",
            "sender_kind",
            "source_context",
            "server_auth",
            "reply_to",
            "authorization_ref")) {
      error(
          "network.sender_mismatch",
          () -> call(tenant, () -> mailbox.store(request, "{\"" + field + "\":\"forged\"}")));
    }
    var unwired = new NetworkMailboxService(jdbc, manager);
    error("common.unavailable", () -> call(tenant, () -> unwired.store(request)));
    assertEquals(
        0,
        sql(
            tenant,
            () ->
                jdbc.queryForObject("SELECT count(*) FROM collab.network_message", Integer.class)));
  }

  @Test
  void failedStoreRollsBackMessageAndMailbox() {
    UUID tenant = UUID.randomUUID();
    JdbcTemplate owner = new JdbcTemplate(roles.owner().dataSource());
    owner.execute(
        "ALTER TABLE collab.network_message ADD CONSTRAINT mailbox_fault CHECK(body <> 'boom')");
    try {
      assertThrows(
          org.springframework.dao.DataAccessException.class,
          () ->
              call(
                  tenant,
                  () ->
                      mailbox.store(
                          StoreRequest.unicast(tenant, "2", "10", "1", "boom", "20", "1"))));
      assertEquals(
          0,
          sql(
              tenant,
              () ->
                  jdbc.queryForObject(
                      "SELECT count(*) FROM collab.network_message", Integer.class)));
      assertEquals(
          0,
          sql(
              tenant,
              () ->
                  jdbc.queryForObject(
                      "SELECT count(*) FROM collab.network_mail_item", Integer.class)));
      assertEquals(
          0,
          sql(
              tenant,
              () ->
                  jdbc.queryForObject(
                      "SELECT count(*) FROM collab.network_recipient", Integer.class)));
    } finally {
      owner.execute("ALTER TABLE collab.network_message DROP CONSTRAINT mailbox_fault");
    }
  }
}
