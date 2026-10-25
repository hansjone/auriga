package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

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
import auriga.iam.api.NetworkAuthority;
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
import org.springframework.transaction.support.TransactionSynchronizationManager;
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
    mailbox =
        new NetworkMailboxService(
            jdbc, manager, authority(null), keys, Clock.systemUTC(), liveIam());
  }

  static NetworkAuthority liveIam() {
    var iam = mock(NetworkAuthority.class);
    when(iam.active(anyString(), anyString()))
        .thenAnswer(
            invocation -> {
              assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
              assertFalse(TransactionSynchronizationManager.isSynchronizationActive());
              assertFalse(TransactionSynchronizationManager.hasResource(jdbc.getDataSource()));
              return "2".equals(invocation.getArgument(0));
            });
    when(iam.check(any()))
        .thenAnswer(
            invocation -> {
              assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
              assertFalse(TransactionSynchronizationManager.isSynchronizationActive());
              return new NetworkAuthority.Decision(true, "allowed", Map.of());
            });
    return iam;
  }

  @Test
  void everyIngressRejectsUnknownRevokedAndUnreachableIamWithoutCreatingRoutes() {
    UUID tenant = UUID.randomUUID();
    var iam = liveIam();
    var guarded =
        new NetworkMailboxService(jdbc, manager, authority(null), keys, Clock.systemUTC(), iam);
    when(iam.active("2", "20")).thenReturn(false);
    error("network.unauthorized", () -> call(tenant, () -> guarded.ready(tenant, "2", "20")));
    assertEquals(
        0,
        sql(
            tenant,
            () -> jdbc.queryForObject("SELECT count(*) FROM collab.network_node", Integer.class)));
    when(iam.active("2", "20")).thenReturn(true);
    var stored =
        call(
            tenant,
            () -> guarded.store(StoreRequest.unicast(tenant, "2", "10", "1", "hello", "20", "1")));
    var route = call(tenant, () -> guarded.ready(tenant, "2", "20"));
    var delivery = stored.deliveries().getFirst();
    for (boolean offline : List.of(false, true)) {
      if (offline) when(iam.active("2", "20")).thenThrow(new IllegalStateException("offline"));
      else when(iam.active("2", "20")).thenReturn(false);
      String code = offline ? "common.unavailable" : "network.unauthorized";
      error(code, () -> call(tenant, () -> guarded.ready(tenant, "2", "20")));
      error(code, () -> call(tenant, () -> guarded.fetch(tenant, "2", "20", 0, 10)));
      error(code, () -> call(tenant, () -> guarded.fetchPage(tenant, "2", "20", 0, 10, 65536)));
      error(
          code,
          () ->
              call(
                  tenant,
                  () ->
                      guarded.receipt(
                          new ReceiptRequest(
                              tenant,
                              "2",
                              "20",
                              route.connectionEpoch(),
                              delivery.deliveryId(),
                              delivery.mailSeq(),
                              List.of(
                                  new ReceiptItem(
                                      delivery.recipientIds().getFirst(), "host_accepted"))))));
      error(
          code,
          () ->
              call(
                  tenant,
                  () ->
                      guarded.store(
                          StoreRequest.unicast(tenant, "2", "10", "1", "again", "20", "1"))));
    }
    when(iam.active("2", "10")).thenReturn(false);
    error(
        "network.unauthorized",
        () ->
            call(
                tenant,
                () ->
                    guarded.store(
                        StoreRequest.unicast(tenant, "2", "10", "1", "again", "20", "1"))));
    assertEquals(
        1,
        sql(
            tenant,
            () ->
                jdbc.queryForObject("SELECT count(*) FROM collab.network_message", Integer.class)));
  }

  @Test
  void privateUnknownAndKnownTargetsConsumeIdenticalBudgetsAndRetryDoesNotMaterializeTwice() {
    UUID knownTenant = UUID.randomUUID(), missingTenant = UUID.randomUUID();
    var iam = liveIam();
    var reserved = new java.util.concurrent.atomic.AtomicBoolean();
    var observedJdbc =
        new JdbcTemplate(jdbc.getDataSource()) {
          @Override
          public int update(String sql, Object... args) {
            int count = super.update(sql, args);
            if (sql.contains("INSERT INTO collab.network_contact_submission")) reserved.set(true);
            return count;
          }
        };
    var guarded =
        new NetworkMailboxService(
            observedJdbc, manager, authority(null), keys, Clock.systemUTC(), iam);
    var expiry = Instant.now().plusSeconds(600);
    var known =
        StoreRequest.firstContact(knownTenant, "2", "10", "1", "hello", "20", expiry, false);
    var missing =
        StoreRequest.firstContact(missingTenant, "2", "10", "1", "hello", "999", expiry, false);
    when(iam.active("2", "999"))
        .thenAnswer(
            i -> {
              assertTrue(reserved.get());
              return false;
            });
    var a = call(knownTenant, () -> guarded.store(known, null, "1", "same"));
    reserved.set(false);
    var b = call(missingTenant, () -> guarded.store(missing, null, "1", "same"));
    assertEquals("request_submitted", a.durabilityStatus());
    assertEquals(a.visibleStatus(), b.visibleStatus());
    assertEquals(a.deliveries(), b.deliveries());
    assertNull(a.contactId());
    assertNull(b.contactId());
    assertNull(a.messageId());
    assertNull(b.messageId());
    assertNotNull(a.submissionId());
    assertNotNull(b.submissionId());
    assertEquals(a.waitUntil(), b.waitUntil());
    assertEquals(a, call(knownTenant, () -> guarded.store(known, null, "1", "same")));
    assertEquals(b, call(missingTenant, () -> guarded.store(missing, null, "1", "same")));
    for (UUID tenant : List.of(knownTenant, missingTenant)) {
      assertEquals(
          1,
          sql(
              tenant,
              () ->
                  jdbc.queryForObject(
                      "SELECT count(*) FROM collab.network_contact_submission", Integer.class)));
      assertEquals(
          List.of(2.0, 4.0, 9.0),
          sql(
              tenant,
              () ->
                  jdbc.queryForList(
                      "SELECT tokens FROM collab.network_contact_budget ORDER BY tokens",
                      Double.class)));
    }
    assertEquals(
        1,
        sql(
            knownTenant,
            () ->
                jdbc.queryForObject("SELECT count(*) FROM collab.network_message", Integer.class)));
    assertEquals(
        0,
        sql(
            missingTenant,
            () ->
                jdbc.queryForObject("SELECT count(*) FROM collab.network_message", Integer.class)));
    assertEquals(
        1,
        sql(
            knownTenant,
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_contact_submission WHERE message_id IS NOT NULL",
                    Integer.class)));
    assertEquals(
        0,
        sql(
            missingTenant,
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_contact_submission WHERE message_id IS NOT NULL",
                    Integer.class)));
    error(
        "network.conflict",
        () ->
            call(
                knownTenant,
                () ->
                    guarded.store(
                        StoreRequest.firstContact(
                            knownTenant, "2", "10", "1", "changed", "20", expiry, false),
                        null,
                        "1",
                        "same")));
    assertTrue(
        sql(
                missingTenant,
                () ->
                    jdbc.queryForObject(
                        "SELECT count(*) FROM collab.outbox_event WHERE event_type='audit.recorded'",
                        Integer.class))
            > 0);
  }

  @Test
  void damagedDateAndNullRowsBecomeAuditedGapsWithoutBlockingNextItem() {
    UUID tenant = UUID.randomUUID();
    var one =
        call(
            tenant,
            () ->
                mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "bad-date", "20", "1")));
    var two =
        call(
            tenant,
            () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "null", "20", "1")));
    call(
        tenant,
        () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "healthy", "20", "1")));
    sql(
        tenant,
        () ->
            jdbc.update(
                "UPDATE collab.network_message SET message_json=jsonb_set(message_json::jsonb,'{expires_at}','\"damaged\"')::text WHERE id=?",
                one.messageId()));
    sql(
        tenant,
        () ->
            jdbc.update(
                "UPDATE collab.network_message SET message_json='null' WHERE id=?",
                two.messageId()));
    call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    assertThrows(
        IllegalStateException.class,
        () ->
            sql(
                tenant,
                () -> {
                  mailbox.fetchPage(tenant, "2", "20", 0, 10, 65536);
                  throw new IllegalStateException("rollback fetch");
                }));
    assertEquals(0, auditCount(tenant, "collab.network.dispatch"));
    var frame =
        NetworkJson.parse(call(tenant, () -> mailbox.fetchPage(tenant, "2", "20", 0, 10, 65536)));
    assertEquals("gap", frame.path("items").get(0).path("type").asString());
    assertEquals("gap", frame.path("items").get(1).path("type").asString());
    assertEquals("delivery", frame.path("items").get(2).path("type").asString());
    assertEquals(2, auditCount(tenant, "collab.network.dispatch"));
    for (var reader :
        List.of(
            mailbox,
            new NetworkMailboxService(
                jdbc, manager, authority(null), keys, Clock.systemUTC(), liveIam()))) {
      var replay =
          NetworkJson.parse(call(tenant, () -> reader.fetchPage(tenant, "2", "20", 0, 10, 65536)));
      assertEquals("gap", replay.path("items").get(0).path("type").asString());
      assertEquals("gap", replay.path("items").get(1).path("type").asString());
      assertEquals("delivery", replay.path("items").get(2).path("type").asString());
      assertEquals(2, auditCount(tenant, "collab.network.dispatch"));
    }
  }

  @Test
  void targetTypeRequiresBothLiveIamNodeAndAuthoritativeLocalDirectory() {
    UUID tenant = UUID.randomUUID();
    var delegate = authority(null);
    var noDirectory =
        new NetworkSourceAuthority() {
          public Source currentSource(UUID t) {
            return delegate.currentSource(t);
          }

          public Target currentTarget(UUID t) {
            return delegate.currentTarget(t);
          }

          public Instant authorizeDelivery(UUID t, String m, String s, String n) {
            return delegate.authorizeDelivery(t, m, s, n);
          }
        };
    var iam = liveIam();
    var guarded =
        new NetworkMailboxService(jdbc, manager, noDirectory, keys, Clock.systemUTC(), iam);
    error(
        "common.unavailable",
        () ->
            call(
                tenant,
                () ->
                    guarded.store(
                        StoreRequest.unicast(tenant, "2", "10", "1", "hello", "20", "3"))));
    when(iam.active("2", "20")).thenReturn(false);
    error(
        "network.unauthorized",
        () ->
            call(
                tenant,
                () ->
                    guarded.store(
                        StoreRequest.unicast(tenant, "2", "10", "1", "hello", "20", "0"))));
    assertEquals(
        0,
        sql(
            tenant,
            () ->
                jdbc.queryForObject("SELECT count(*) FROM collab.network_message", Integer.class)));
  }

  @Test
  void missingSourceFirstContactGrantIsRejectedBeforeReservationOrTargetLookup() {
    UUID tenant = UUID.randomUUID();
    var iam = liveIam();
    when(iam.check(
            argThat(
                r ->
                    r != null
                        && r.operation() == NetworkAuthority.Operation.FIRST_CONTACT
                        && "10".equals(r.targetNode()))))
        .thenReturn(new NetworkAuthority.Decision(false, "network_denied", Map.of()));
    var guarded =
        new NetworkMailboxService(jdbc, manager, authority(null), keys, Clock.systemUTC(), iam);
    error(
        "network.unauthorized",
        () ->
            call(
                tenant,
                () ->
                    guarded.store(
                        StoreRequest.firstContact(
                            tenant, "2", "10", "1", "hello", "999", null, false),
                        null,
                        "1",
                        "key")));
    verify(iam, never()).active("2", "999");
    assertEquals(
        0,
        sql(
            tenant,
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_contact_submission", Integer.class)));
  }

  @Test
  void storeRejectsOversizeSignedItemBeforeAllocatingMailboxSequence() {
    UUID tenant = UUID.randomUUID();
    var locals = new java.util.ArrayList<String>();
    var members = new java.util.HashMap<String, String>();
    var types = new java.util.HashMap<String, String>();
    for (int i = 0; i < 256; i++) {
      String local = Long.toString(Long.MAX_VALUE - i);
      locals.add(local);
      members.put("20." + local, "m".repeat(80));
      types.put("20." + local, "session");
    }
    var dispatch =
        new NetworkSourceAuthority.Dispatch(
            "{\"version\":1,\"realm\":\"2\",\"node\":\"0\",\"local\":\"2\",\"type\":\"group\"}",
            "snapshot",
            members,
            types,
            null,
            null,
            null);
    var grouped =
        new NetworkMailboxService(
            jdbc, manager, authority(dispatch), keys, Clock.systemUTC(), liveIam());
    var request =
        new StoreRequest(
            tenant,
            "2",
            "10",
            "1",
            "message",
            "b".repeat(16000),
            null,
            List.of(new NodeTarget("20", locals)),
            "conversation",
            null,
            true);
    error("network.protocol_error", () -> call(tenant, () -> grouped.store(request)));
    assertEquals(
        0,
        sql(
            tenant,
            () ->
                jdbc.queryForObject("SELECT count(*) FROM collab.network_message", Integer.class)));
    assertEquals(
        0,
        sql(
            tenant,
            () -> jdbc.queryForObject("SELECT count(*) FROM collab.network_node", Integer.class)));
  }

  @Test
  void broadcastUsageAndCurrentGrantRejectionAreAudited() {
    UUID tenant = UUID.randomUUID();
    var dispatch =
        new NetworkSourceAuthority.Dispatch(
            "{\"version\":1,\"realm\":\"2\",\"node\":\"0\",\"local\":\"1\",\"type\":\"group\"}",
            "snapshot",
            Map.of("20.0", "membership"),
            Map.of("20.0", "system"),
            "grant",
            "1",
            "agent");
    var iam = liveIam();
    var broadcast =
        new NetworkMailboxService(jdbc, manager, authority(dispatch), keys, Clock.systemUTC(), iam);
    var request = StoreRequest.unicast(tenant, "2", "10", "1", "broadcast", "20", "0");
    call(tenant, () -> broadcast.store(request));
    when(iam.check(
            argThat(r -> r != null && r.operation() == NetworkAuthority.Operation.BROADCAST)))
        .thenReturn(new NetworkAuthority.Decision(false, "network_denied", Map.of()));
    error("network.unauthorized", () -> call(tenant, () -> broadcast.store(request)));
    assertEquals(2, auditCount(tenant, "collab.network.broadcast.submit"));
    call(tenant, () -> broadcast.ready(tenant, "2", "20"));
    var frame =
        NetworkJson.parse(call(tenant, () -> broadcast.fetchPage(tenant, "2", "20", 0, 10, 65536)));
    assertEquals("gap", frame.path("items").get(0).path("type").asString());
    assertEquals("awaiting_authorization", frame.path("items").get(0).path("reason").asString());
  }

  static int auditCount(UUID tenant, String action) {
    // The shared infrastructure outbox stores its CloudEvents envelope as text.
    return sql(
        tenant,
        () ->
            jdbc.queryForObject(
                """
        SELECT count(*) FROM collab.outbox_event
        WHERE tenant_id=? AND event_type='audit.recorded'
          AND envelope::jsonb->'data'->>'action'=?
        """,
                Integer.class,
                tenant,
                action));
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

      public String resolveTargetType(UUID tenant, String realm, String node, String local) {
        return "0".equals(local) ? "system" : "session";
      }

      public Target currentTarget(UUID tenant) {
        long epoch =
            jdbc.queryForObject(
                "SELECT coalesce(max(connection_epoch),0) FROM collab.network_node WHERE tenant_id=? AND realm='2' AND node_id='20'",
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
  void expiredItemBecomesTerminalAndDoesNotBlockLaterDelivery() {
    UUID tenant = UUID.randomUUID();
    call(
        tenant,
        () ->
            mailbox.store(
                StoreRequest.unicast(tenant, "2", "10", "1", "short", "20", "3"), "{\"ttl\":1}"));
    call(
        tenant,
        () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "later", "20", "3")));
    call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    var later =
        new NetworkMailboxService(
            jdbc,
            manager,
            authority(null),
            keys,
            Clock.offset(Clock.systemUTC(), java.time.Duration.ofSeconds(2)),
            liveIam());
    var frame =
        NetworkJson.parse(call(tenant, () -> later.fetchPage(tenant, "2", "20", 0, 10, 65536)));
    assertEquals("terminal", frame.path("items").get(0).path("type").asString());
    assertEquals(
        "expired",
        frame.path("items").get(0).path("recipients").get(0).path("transport_status").asString());
    assertEquals("delivery", frame.path("items").get(1).path("type").asString());
    assertEquals("2", frame.path("page").path("scan_after").asString());
  }

  @Test
  void pendingFirstContactIsApprovalPreviewAndDoesNotBlockLaterDelivery() {
    UUID tenant = UUID.randomUUID();
    call(
        tenant,
        () ->
            mailbox.store(
                StoreRequest.firstContact(
                    tenant,
                    "2",
                    "10",
                    "1",
                    "preview",
                    "20",
                    Instant.now().plusSeconds(3600),
                    true)));
    call(
        tenant,
        () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "later", "20", "3")));
    call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    var frame =
        NetworkJson.parse(call(tenant, () -> mailbox.fetchPage(tenant, "2", "20", 0, 10, 65536)));
    var prompt = frame.path("items").get(0);
    assertEquals("contact_prompt", prompt.path("type").asString());
    assertEquals("preview", prompt.path("preview").asString());
    assertFalse(prompt.has("message"));
    assertEquals("delivery", frame.path("items").get(1).path("type").asString());
  }

  @Test
  void oneAuthorizationFailureBecomesGapWithoutAbortingPage() {
    UUID tenant = UUID.randomUUID();
    call(
        tenant,
        () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "denied", "20", "3")));
    call(
        tenant,
        () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "later", "20", "3")));
    call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    var delegate = authority(null);
    var rejecting =
        new NetworkSourceAuthority() {
          public Source currentSource(UUID id) {
            return delegate.currentSource(id);
          }

          public Target currentTarget(UUID id) {
            return delegate.currentTarget(id);
          }

          public Instant authorizeDelivery(UUID id, String message, String source, String node) {
            if (NetworkJson.parse(message).path("body").path("text").asString().equals("denied"))
              throw new AurigaException(NetworkErrors.UNAUTHORIZED);
            return delegate.authorizeDelivery(id, message, source, node);
          }
        };
    var restricted =
        new NetworkMailboxService(jdbc, manager, rejecting, keys, Clock.systemUTC(), liveIam());
    var frame =
        NetworkJson.parse(
            call(tenant, () -> restricted.fetchPage(tenant, "2", "20", 0, 10, 65536)));
    assertEquals("gap", frame.path("items").get(0).path("type").asString());
    assertEquals("awaiting_authorization", frame.path("items").get(0).path("reason").asString());
    assertFalse(frame.path("items").get(0).has("message"));
    assertEquals("delivery", frame.path("items").get(1).path("type").asString());
  }

  @Test
  void oversizedSignedItemBecomesGapAndEntireFrameFitsRequestedBudget() {
    UUID tenant = UUID.randomUUID();
    call(
        tenant,
        () ->
            mailbox.store(
                StoreRequest.unicast(tenant, "2", "10", "1", "large", "20", "3"),
                "{\"ext\":{\"com.example.padding\":\"" + "x".repeat(3000) + "\"}}"));
    call(
        tenant,
        () -> mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "later", "20", "3")));
    call(tenant, () -> mailbox.ready(tenant, "2", "20"));
    String encoded = call(tenant, () -> mailbox.fetchPage(tenant, "2", "20", 0, 10, 4096));
    assertTrue(encoded.getBytes(java.nio.charset.StandardCharsets.UTF_8).length <= 4096);
    var frame = NetworkJson.parse(encoded);
    assertEquals("gap", frame.path("items").get(0).path("type").asString());
    assertEquals("delivery", frame.path("items").get(1).path("type").asString());
    assertEquals(2, frame.path("page").path("manifest").size());
  }

  @Test
  void forgedOriginAndInvisibleReplyReferenceCannotBeStamped() {
    UUID tenant = UUID.randomUUID();
    var original =
        call(
            tenant,
            () ->
                mailbox.store(StoreRequest.unicast(tenant, "2", "10", "1", "request", "20", "3")));
    var request = StoreRequest.unicast(tenant, "2", "10", "1", "forged", "20", "3");
    error(
        "network.unauthorized",
        () ->
            call(
                tenant,
                () ->
                    mailbox.store(
                        request,
                        "{\"origin\":{\"message_id\":\"other-message\",\"recipient_id\":\"other-recipient\"}}")));
    var reply =
        new StoreRequest(
            tenant,
            "2",
            "10",
            "1",
            "reply",
            "reply",
            null,
            List.of(new NodeTarget("20", List.of("3"))),
            "conversation",
            null,
            true);
    error(
        "network.unauthorized",
        () ->
            call(
                tenant,
                () ->
                    mailbox.store(
                        reply,
                        "{\"in_reply_to\":\""
                            + original.messageId()
                            + "\",\"reply_mode\":\"private\"}")));
    error(
        "network.protocol_error",
        () ->
            call(
                tenant,
                () -> mailbox.store(request, "{\"response_deadline\":\"2099-01-01T00:00:00Z\"}")));
    error(
        "network.protocol_error",
        () ->
            call(
                tenant,
                () ->
                    mailbox.store(
                        request,
                        "{\"to\":{\"version\":1,\"realm\":\"2\",\"node\":\"20\",\"local\":\"3\",\"type\":\"subagent\"}}")));
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
    assertEquals("request_submitted", stored.durabilityStatus());
    assertEquals("request_submitted", stored.visibleStatus());
    assertTrue(stored.restrictedAppearance());
    assertTrue(stored.deliveries().isEmpty());
    assertNull(stored.contactId());
    UUID contactId =
        sql(
            tenant,
            () ->
                jdbc.queryForObject(
                    "SELECT id FROM collab.network_contact WHERE tenant_id=?", UUID.class, tenant));
    assertEquals(
        0L, call(tenant, () -> mailbox.fetch(tenant, "2", "20", 0, 10)).route().committedCursor());

    var expired = call(tenant, () -> mailbox.expireContactIfDue(tenant, contactId, false));
    assertEquals("expired", expired.status());
    assertEquals("request_submitted", expired.visibleStatus());
    assertTrue(expired.restrictedAppearance());
    assertEquals("request_submitted", expired.presentationCode());

    var afterExpire = call(tenant, () -> mailbox.fetch(tenant, "2", "20", 0, 10));
    assertEquals(
        "expired", afterExpire.items().getFirst().recipients().getFirst().transportStatus());
    assertEquals(1L, afterExpire.route().committedCursor());

    var viewerExpired = call(tenant, () -> mailbox.expireContactIfDue(tenant, contactId, true));
    assertEquals("contact_expired", viewerExpired.visibleStatus());
    assertEquals("network.contact_expired", viewerExpired.presentationCode());

    error(
        NetworkErrors.CONTACT_EXPIRED.code(),
        () ->
            call(
                tenant,
                () ->
                    mailbox.contactDecide(
                        new ContactDecideRequest(tenant, contactId, "allowed", true))));
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
        new NetworkMailboxService(
            jdbc, manager, authority(dispatch), keys, Clock.systemUTC(), liveIam());
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
