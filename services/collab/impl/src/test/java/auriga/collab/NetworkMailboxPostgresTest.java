package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkErrors;
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
import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.security.*;
import auriga.testsupport.PostgresExtension;
import java.time.Instant;
import java.util.List;
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
  static void migrate() {
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
    mailbox = new NetworkMailboxService(jdbc, manager);
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

    var expired = call(tenant, () -> mailbox.expireContactIfDue(tenant, stored.contactId(), false));
    assertEquals("expired", expired.status());
    assertEquals("request_submitted", expired.visibleStatus());
    assertTrue(expired.restrictedAppearance());
    assertEquals("request_submitted", expired.presentationCode());

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
    StoreResult stored =
        call(
            tenant,
            () ->
                mailbox.store(
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
