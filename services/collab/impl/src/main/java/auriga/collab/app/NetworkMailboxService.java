package auriga.collab.app;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.error.ErrorCode;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AuthorizationRules;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.UUID;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

/**
 * Internal (non-proto) network mailbox API for ai-gateway HEAD/FETCH/RECEIPT wiring. Durable truth
 * lives in PostgreSQL with tenant RLS; transport and signing stay elsewhere.
 */
public class NetworkMailboxService {
  /** contracts/network/limits.json dispatch.atomicMaxNodes */
  static final int ATOMIC_MAX_NODES = 32;

  /** contracts/network/limits.json dispatch.atomicMaxLocals */
  static final int ATOMIC_MAX_LOCALS = 256;

  /** contracts/network/limits.json fetch.maxItems */
  static final int FETCH_MAX_ITEMS = 128;

  /** contracts/network/limits.json message.firstContactPendingMaxSeconds */
  static final long FIRST_CONTACT_MAX_SECONDS = 86400L;

  /** contracts/network/limits.json encoding.maxBodyBytes */
  static final int MAX_BODY_BYTES = 16384;

  /** contracts/network/limits.json receipt.maxResultsPerReceipt */
  static final int RECEIPT_MAX_RESULTS = 256;

  /** contracts/network/limits.json processing.maxAutomaticAttempts */
  static final int MAX_ATTEMPTS = 5;

  private static final Set<String> TERMINAL =
      Set.of("host_accepted", "expired", "canceled", "revoked", "skipped", "unresolved");
  private static final Set<String> TRANSPORT =
      Set.of(
          "pending",
          "quarantined",
          "host_accepted",
          "expired",
          "canceled",
          "revoked",
          "skipped",
          "unresolved");

  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;

  public NetworkMailboxService(JdbcTemplate jdbc, TenantTransactionManager manager) {
    this.jdbc = Objects.requireNonNull(jdbc);
    this.tx = new TransactionTemplate(manager);
  }

  /** READY-shaped takeover: bumps connection_epoch and returns current mailbox cursors. */
  public NodeRoute ready(UUID tenantId, String realm, String nodeId) {
    return run(
        tenantId,
        () -> {
          ensureNodeLocked(tenantId, realm, nodeId);
          long epoch =
              jdbc.queryForObject(
                  """
                  UPDATE collab.network_node
                     SET connection_epoch = connection_epoch + 1,
                         updated_at = now()
                   WHERE tenant_id=? AND realm=? AND node_id=?
               RETURNING connection_epoch
                  """,
                  Long.class,
                  tenantId,
                  realm,
                  nodeId);
          return loadRoute(tenantId, realm, nodeId, epoch);
        });
  }

  /**
   * Atomic small-batch / unicast store: one immutable body + per-node mail items + recipients in
   * the same transaction. Returns platform_stored-shaped durability.
   */
  public StoreResult store(StoreRequest request) {
    return run(
        request.tenantId(),
        () -> {
          validateStore(request);
          UUID messageId = UuidV7.next();
          jdbc.update(
              """
              INSERT INTO collab.network_message(
                id, tenant_id, realm, kind, body, source_node, source_local, thread_id)
              VALUES (?,?,?,?,?,?,?,?)
              """,
              messageId,
              request.tenantId(),
              request.realm(),
              request.kind(),
              request.body(),
              request.sourceNode(),
              request.sourceLocal(),
              request.threadId());

          List<DeliveryStored> deliveries = new ArrayList<>();
          for (NodeTarget target : request.targets()) {
            ensureNodeLocked(request.tenantId(), request.realm(), target.nodeId());
            long mailSeq =
                jdbc.queryForObject(
                    """
                    UPDATE collab.network_node
                       SET head_seq = head_seq + 1, updated_at = now()
                     WHERE tenant_id=? AND realm=? AND node_id=?
                 RETURNING head_seq
                    """,
                    Long.class,
                    request.tenantId(),
                    request.realm(),
                    target.nodeId());
            UUID deliveryId = UuidV7.next();
            jdbc.update(
                """
                INSERT INTO collab.network_mail_item(
                  id, tenant_id, message_id, target_realm, target_node, mail_seq)
                VALUES (?,?,?,?,?,?)
                """,
                deliveryId,
                request.tenantId(),
                messageId,
                request.realm(),
                target.nodeId(),
                mailSeq);
            List<UUID> recipientIds = new ArrayList<>();
            for (String local : target.locals()) {
              UUID recipientId = UuidV7.next();
              jdbc.update(
                  """
                  INSERT INTO collab.network_recipient(
                    id, tenant_id, delivery_id, target_local, transport_status)
                  VALUES (?,?,?,?, 'pending')
                  """,
                  recipientId,
                  request.tenantId(),
                  deliveryId,
                  local);
              recipientIds.add(recipientId);
            }
            deliveries.add(
                new DeliveryStored(
                    deliveryId, target.nodeId(), mailSeq, List.copyOf(recipientIds)));
          }

          UUID contactId = null;
          String visible = "platform_stored";
          boolean restricted = false;
          if ("first_contact".equals(request.kind())) {
            contactId = messageId;
            Instant expires =
                request.contactExpiresAt() != null
                    ? request.contactExpiresAt()
                    : Instant.now().plusSeconds(FIRST_CONTACT_MAX_SECONDS);
            require(
                !expires.isAfter(Instant.now().plusSeconds(FIRST_CONTACT_MAX_SECONDS)),
                CommonErrors.VALIDATION_FAILED);
            NodeTarget only = request.targets().getFirst();
            jdbc.update(
                """
                INSERT INTO collab.network_contact(
                  id, tenant_id, message_id, scope, status,
                  source_realm, source_node, source_local,
                  target_realm, target_node, expires_at, status_viewer)
                VALUES (?,?,?,?, 'pending', ?,?,?,?,?,?,?)
                """,
                contactId,
                request.tenantId(),
                messageId,
                request.contactScope(),
                request.realm(),
                request.sourceNode(),
                request.sourceLocal(),
                request.realm(),
                only.nodeId(),
                java.sql.Timestamp.from(expires),
                request.statusViewer());
            if (request.statusViewer()) {
              visible = "contact_pending";
            } else {
              visible = "request_submitted";
              restricted = true;
            }
          }
          return new StoreResult(
              messageId,
              "platform_stored",
              visible,
              restricted,
              List.copyOf(deliveries),
              contactId);
        });
  }

  /** Cursor-paged fetch; does not mutate transport status. */
  public FetchResult fetch(
      UUID tenantId, String realm, String nodeId, long afterCursor, int limit) {
    return run(
        tenantId,
        () -> {
          require(afterCursor >= 0, CommonErrors.VALIDATION_FAILED);
          require(limit >= 1 && limit <= FETCH_MAX_ITEMS, CommonErrors.VALIDATION_FAILED);
          ensureNodeLocked(tenantId, realm, nodeId);
          NodeRoute route = loadRoute(tenantId, realm, nodeId, null);
          var rows =
              jdbc.queryForList(
                  """
                  SELECT i.id AS delivery_id, i.mail_seq, i.message_id, m.body, m.kind,
                         m.source_node, m.source_local, m.thread_id, m.created_at
                    FROM collab.network_mail_item i
                    JOIN collab.network_message m
                      ON m.tenant_id=i.tenant_id AND m.id=i.message_id
                   WHERE i.tenant_id=? AND i.target_realm=? AND i.target_node=?
                     AND i.mail_seq>?
                   ORDER BY i.mail_seq
                   LIMIT ?
                  """,
                  tenantId,
                  realm,
                  nodeId,
                  afterCursor,
                  limit);
          List<FetchItem> items = new ArrayList<>();
          for (var row : rows) {
            UUID deliveryId = (UUID) row.get("delivery_id");
            var recipients =
                jdbc.queryForList(
                    """
                    SELECT id, target_local, transport_status, state_version
                      FROM collab.network_recipient
                     WHERE tenant_id=? AND delivery_id=?
                     ORDER BY target_local
                    """,
                    tenantId,
                    deliveryId);
            List<RecipientView> views = new ArrayList<>();
            for (var r : recipients) {
              views.add(
                  new RecipientView(
                      (UUID) r.get("id"),
                      r.get("target_local").toString(),
                      r.get("transport_status").toString(),
                      ((Number) r.get("state_version")).longValue()));
            }
            items.add(
                new FetchItem(
                    deliveryId,
                    ((Number) row.get("mail_seq")).longValue(),
                    (UUID) row.get("message_id"),
                    row.get("body").toString(),
                    row.get("kind").toString(),
                    row.get("source_node").toString(),
                    row.get("source_local").toString(),
                    row.get("thread_id") == null ? null : (UUID) row.get("thread_id"),
                    ((java.sql.Timestamp) row.get("created_at")).toInstant(),
                    List.copyOf(views)));
          }
          return new FetchResult(route, List.copyOf(items));
        });
  }

  /**
   * Idempotent receipt by recipient_id. Advances committed_cursor over the gapless closed prefix.
   * Stale connection_epoch → network.stale_epoch.
   */
  public ReceiptCommitResult receipt(ReceiptRequest request) {
    return run(
        request.tenantId(),
        () -> {
          require(
              request.results() != null
                  && !request.results().isEmpty()
                  && request.results().size() <= RECEIPT_MAX_RESULTS,
              CommonErrors.VALIDATION_FAILED);
          ensureNodeLocked(request.tenantId(), request.realm(), request.nodeId());
          NodeRoute route = loadRoute(request.tenantId(), request.realm(), request.nodeId(), null);
          requireEpoch(route.connectionEpoch(), request.epoch());

          var item =
              one(
                  """
                  SELECT * FROM collab.network_mail_item
                   WHERE tenant_id=? AND id=? AND target_realm=? AND target_node=?
                   FOR UPDATE
                  """,
                  request.tenantId(),
                  request.deliveryId(),
                  request.realm(),
                  request.nodeId());
          require(
              ((Number) item.get("mail_seq")).longValue() == request.mailSeq(),
              NetworkErrors.PROTOCOL_ERROR);

          List<ReceiptResultView> committed = new ArrayList<>();
          for (ReceiptItem result : request.results()) {
            String status = normalizeTransport(result.transportStatus());
            require(
                TERMINAL.contains(status) || "quarantined".equals(status),
                NetworkErrors.PROTOCOL_ERROR);
            var row =
                one(
                    """
                    SELECT * FROM collab.network_recipient
                     WHERE tenant_id=? AND id=? AND delivery_id=?
                     FOR UPDATE
                    """,
                    request.tenantId(),
                    result.recipientId(),
                    request.deliveryId());
            String current = row.get("transport_status").toString();
            long version = ((Number) row.get("state_version")).longValue();
            if (TERMINAL.contains(current) || "quarantined".equals(current)) {
              require(current.equals(status), NetworkErrors.CONFLICT);
              committed.add(new ReceiptResultView(result.recipientId(), current, version));
              continue;
            }
            require("pending".equals(current), NetworkErrors.CONFLICT);
            jdbc.update(
                """
                UPDATE collab.network_recipient
                   SET transport_status=?, state_version=state_version+1
                 WHERE tenant_id=? AND id=?
                """,
                status,
                request.tenantId(),
                result.recipientId());
            committed.add(new ReceiptResultView(result.recipientId(), status, version + 1));
          }

          boolean closed = mailItemClosed(request.tenantId(), request.deliveryId());
          long cursor =
              advanceCommittedCursor(request.tenantId(), request.realm(), request.nodeId());
          return new ReceiptCommitResult(
              request.deliveryId(), request.mailSeq(), List.copyOf(committed), closed, cursor);
        });
  }

  /**
   * Conversation-scope CONTACT_DECIDE. pending→allowed/denied; past expires_at→expired. Callers
   * without status_viewer see restricted request_submitted appearance.
   */
  public ContactDecideResult contactDecide(ContactDecideRequest request) {
    return run(
        request.tenantId(),
        () -> {
          require(
              "allowed".equals(request.decision()) || "denied".equals(request.decision()),
              CommonErrors.VALIDATION_FAILED);
          var row =
              one(
                  """
                  SELECT * FROM collab.network_contact
                   WHERE tenant_id=? AND id=?
                   FOR UPDATE
                  """,
                  request.tenantId(),
                  request.contactId());
          require("conversation".equals(row.get("scope").toString()), NetworkErrors.PROTOCOL_ERROR);
          Instant expires = ((java.sql.Timestamp) row.get("expires_at")).toInstant();
          String status = row.get("status").toString();
          boolean viewer =
              request.statusViewer() != null
                  ? request.statusViewer()
                  : Boolean.TRUE.equals(row.get("status_viewer"));

          if ("pending".equals(status) && !expires.isAfter(Instant.now())) {
            jdbc.update(
                """
                UPDATE collab.network_contact
                   SET status='expired', decided_at=now(), decision_version=decision_version+1
                 WHERE tenant_id=? AND id=?
                """,
                request.tenantId(),
                request.contactId());
            // Design §1.2: contact_expired + Expired tombstone on undelivered recipients.
            closeUndeliveredContactRecipients(request.tenantId(), row, "expired");
            return contactView(request.contactId(), "expired", viewer);
          }
          if ("expired".equals(status)) {
            // Repair any missing tombstones from earlier writes, then reject late decide.
            closeUndeliveredContactRecipients(request.tenantId(), row, "expired");
            throw new AurigaException(NetworkErrors.CONTACT_EXPIRED);
          }
          if ("denied".equals(status) && "allowed".equals(request.decision())) {
            closeUndeliveredContactRecipients(request.tenantId(), row, "revoked");
            throw new AurigaException(NetworkErrors.CONTACT_DENIED);
          }
          if (!"pending".equals(status)) {
            require(status.equals(request.decision()), NetworkErrors.CONFLICT);
            if ("denied".equals(status)) {
              closeUndeliveredContactRecipients(request.tenantId(), row, "revoked");
            }
            return contactView(request.contactId(), status, viewer);
          }
          jdbc.update(
              """
              UPDATE collab.network_contact
                 SET status=?, decided_at=now(), decision_version=decision_version+1
               WHERE tenant_id=? AND id=?
              """,
              request.decision(),
              request.tenantId(),
              request.contactId());
          // First-layer deny → Revoked transport tombstone; allow keeps recipients pending.
          if ("denied".equals(request.decision())) {
            closeUndeliveredContactRecipients(request.tenantId(), row, "revoked");
          }
          return contactView(request.contactId(), request.decision(), viewer);
        });
  }

  /** Materialize pending→expired when past deadline (scanner / READY hook shape). */
  public ContactDecideResult expireContactIfDue(
      UUID tenantId, UUID contactId, boolean statusViewer) {
    return run(
        tenantId,
        () -> {
          var row =
              one(
                  """
                  SELECT * FROM collab.network_contact
                   WHERE tenant_id=? AND id=?
                   FOR UPDATE
                  """,
                  tenantId,
                  contactId);
          String status = row.get("status").toString();
          Instant expires = ((java.sql.Timestamp) row.get("expires_at")).toInstant();
          if ("pending".equals(status) && !expires.isAfter(Instant.now())) {
            jdbc.update(
                """
                UPDATE collab.network_contact
                   SET status='expired', decided_at=now(), decision_version=decision_version+1
                 WHERE tenant_id=? AND id=?
                """,
                tenantId,
                contactId);
            status = "expired";
          }
          if ("expired".equals(status)) {
            closeUndeliveredContactRecipients(tenantId, row, "expired");
          } else if ("denied".equals(status)) {
            closeUndeliveredContactRecipients(tenantId, row, "revoked");
          }
          return contactView(contactId, status, statusViewer);
        });
  }

  /** ATTEMPT_BEGIN shape: verifies connection epoch before processing may start. */
  public AttemptBeginResult attemptBegin(AttemptBeginRequest request) {
    return run(
        request.tenantId(),
        () -> {
          ensureNodeLocked(request.tenantId(), request.realm(), request.nodeId());
          NodeRoute route = loadRoute(request.tenantId(), request.realm(), request.nodeId(), null);
          requireEpoch(route.connectionEpoch(), request.epoch());
          var recipient =
              one(
                  """
                  SELECT r.*, i.target_node
                    FROM collab.network_recipient r
                    JOIN collab.network_mail_item i
                      ON i.tenant_id=r.tenant_id AND i.id=r.delivery_id
                   WHERE r.tenant_id=? AND r.id=? AND r.delivery_id=?
                   FOR UPDATE OF r
                  """,
                  request.tenantId(),
                  request.recipientId(),
                  request.deliveryId());
          require(
              request.nodeId().equals(recipient.get("target_node").toString()),
              NetworkErrors.UNAUTHORIZED);
          int count = ((Number) recipient.get("attempt_count")).intValue();
          require(count < MAX_ATTEMPTS, NetworkErrors.QUOTA_EXCEEDED);
          jdbc.update(
              """
              UPDATE collab.network_recipient
                 SET attempt_count = attempt_count + 1
               WHERE tenant_id=? AND id=?
              """,
              request.tenantId(),
              request.recipientId());
          return new AttemptBeginResult(request.recipientId(), count + 1, MAX_ATTEMPTS, "entered");
        });
  }

  /** EXECUTION_ADMIT shape: verifies epoch; first admit wins, retries are already_admitted. */
  public ExecutionAdmitResult executionAdmit(ExecutionAdmitRequest request) {
    return run(
        request.tenantId(),
        () -> {
          ensureNodeLocked(request.tenantId(), request.realm(), request.nodeId());
          NodeRoute route = loadRoute(request.tenantId(), request.realm(), request.nodeId(), null);
          requireEpoch(route.connectionEpoch(), request.connectionEpoch());
          var recipient =
              one(
                  """
                  SELECT r.*, i.target_node
                    FROM collab.network_recipient r
                    JOIN collab.network_mail_item i
                      ON i.tenant_id=r.tenant_id AND i.id=r.delivery_id
                   WHERE r.tenant_id=? AND r.id=?
                   FOR UPDATE OF r
                  """,
                  request.tenantId(),
                  request.recipientId());
          require(
              request.nodeId().equals(recipient.get("target_node").toString()),
              NetworkErrors.UNAUTHORIZED);
          if (recipient.get("admission_id") != null) {
            return new ExecutionAdmitResult(
                request.recipientId(),
                "already_admitted",
                (UUID) recipient.get("admission_id"),
                ((Number) recipient.get("admitted_epoch")).longValue());
          }
          UUID admissionId = UuidV7.next();
          jdbc.update(
              """
              UPDATE collab.network_recipient
                 SET admission_id=?, admitted_epoch=?, admitted_boot_id=?
               WHERE tenant_id=? AND id=?
              """,
              admissionId,
              request.connectionEpoch(),
              request.bootId(),
              request.tenantId(),
              request.recipientId());
          return new ExecutionAdmitResult(
              request.recipientId(), "admitted", admissionId, request.connectionEpoch());
        });
  }

  private ContactDecideResult contactView(UUID contactId, String status, boolean statusViewer) {
    String visible = statusViewer ? mapContactVisible(status) : "request_submitted";
    return new ContactDecideResult(
        contactId, status, visible, !statusViewer, mapContactCode(status, statusViewer));
  }

  private static String mapContactVisible(String status) {
    return switch (status) {
      case "pending" -> "contact_pending";
      case "allowed" -> "contact_allowed";
      case "denied" -> "contact_denied";
      case "expired" -> "contact_expired";
      default -> status;
    };
  }

  private static String mapContactCode(String status, boolean statusViewer) {
    if (!statusViewer) return "request_submitted";
    return switch (status) {
      case "pending" -> "network.contact_pending";
      case "denied" -> "network.contact_denied";
      case "expired" -> "network.contact_expired";
      default -> null;
    };
  }

  /**
   * Write Expired/Revoked transport tombstones for still-open recipients of a contact's message,
   * then advance committed_cursor on each affected node mailbox (design §1.2 / §4).
   */
  private void closeUndeliveredContactRecipients(
      UUID tenantId, Map<String, Object> contactRow, String terminalStatus) {
    require(TERMINAL.contains(terminalStatus), NetworkErrors.PROTOCOL_ERROR);
    UUID messageId = (UUID) contactRow.get("message_id");
    String targetRealm = contactRow.get("target_realm").toString();
    String targetNode = contactRow.get("target_node").toString();
    ensureNodeLocked(tenantId, targetRealm, targetNode);
    jdbc.update(
        """
        UPDATE collab.network_recipient r
           SET transport_status=?, state_version=state_version+1
          FROM collab.network_mail_item i
         WHERE r.tenant_id=? AND r.delivery_id=i.id
           AND i.tenant_id=? AND i.message_id=?
           AND r.transport_status IN ('pending', 'quarantined')
        """,
        terminalStatus,
        tenantId,
        tenantId,
        messageId);
    var nodes =
        jdbc.queryForList(
            """
            SELECT DISTINCT target_realm, target_node
              FROM collab.network_mail_item
             WHERE tenant_id=? AND message_id=?
            """,
            tenantId,
            messageId);
    for (var node : nodes) {
      String realm = node.get("target_realm").toString();
      String nodeId = node.get("target_node").toString();
      if (!(realm.equals(targetRealm) && nodeId.equals(targetNode))) {
        ensureNodeLocked(tenantId, realm, nodeId);
      }
      advanceCommittedCursor(tenantId, realm, nodeId);
    }
  }

  private boolean mailItemClosed(UUID tenantId, UUID deliveryId) {
    Integer open =
        jdbc.queryForObject(
            """
            SELECT count(*) FROM collab.network_recipient
             WHERE tenant_id=? AND delivery_id=?
               AND transport_status IN ('pending', 'quarantined')
            """,
            Integer.class,
            tenantId,
            deliveryId);
    return open != null && open == 0;
  }

  private long advanceCommittedCursor(UUID tenantId, String realm, String nodeId) {
    var open =
        jdbc.queryForList(
            """
            SELECT i.mail_seq
              FROM collab.network_mail_item i
             WHERE i.tenant_id=? AND i.target_realm=? AND i.target_node=?
               AND i.mail_seq > (
                     SELECT committed_cursor FROM collab.network_node
                      WHERE tenant_id=? AND realm=? AND node_id=?)
               AND EXISTS (
                     SELECT 1 FROM collab.network_recipient r
                      WHERE r.tenant_id=i.tenant_id AND r.delivery_id=i.id
                        AND r.transport_status IN ('pending', 'quarantined'))
             ORDER BY i.mail_seq
             LIMIT 1
            """,
            tenantId,
            realm,
            nodeId,
            tenantId,
            realm,
            nodeId);
    long head =
        jdbc.queryForObject(
            """
            SELECT head_seq FROM collab.network_node
             WHERE tenant_id=? AND realm=? AND node_id=?
            """,
            Long.class,
            tenantId,
            realm,
            nodeId);
    long nextCursor;
    if (open.isEmpty()) {
      nextCursor = head;
    } else {
      nextCursor = ((Number) open.getFirst().get("mail_seq")).longValue() - 1;
    }
    jdbc.update(
        """
        UPDATE collab.network_node
           SET committed_cursor=?, updated_at=now()
         WHERE tenant_id=? AND realm=? AND node_id=?
        """,
        nextCursor,
        tenantId,
        realm,
        nodeId);
    return nextCursor;
  }

  private void ensureNodeLocked(UUID tenantId, String realm, String nodeId) {
    text(realm, 32);
    text(nodeId, 32);
    jdbc.update(
        """
        INSERT INTO collab.network_node(tenant_id, realm, node_id)
        VALUES (?,?,?)
        ON CONFLICT (tenant_id, realm, node_id) DO NOTHING
        """,
        tenantId,
        realm,
        nodeId);
    one(
        """
        SELECT * FROM collab.network_node
         WHERE tenant_id=? AND realm=? AND node_id=?
         FOR UPDATE
        """,
        tenantId,
        realm,
        nodeId);
  }

  private NodeRoute loadRoute(UUID tenantId, String realm, String nodeId, Long epochOverride) {
    var row =
        one(
            """
            SELECT connection_epoch, head_seq, committed_cursor
              FROM collab.network_node
             WHERE tenant_id=? AND realm=? AND node_id=?
            """,
            tenantId,
            realm,
            nodeId);
    long epoch =
        epochOverride != null ? epochOverride : ((Number) row.get("connection_epoch")).longValue();
    return new NodeRoute(
        realm,
        nodeId,
        epoch,
        ((Number) row.get("head_seq")).longValue(),
        ((Number) row.get("committed_cursor")).longValue());
  }

  private void validateStore(StoreRequest request) {
    require(
        request.targets() != null && !request.targets().isEmpty(), CommonErrors.VALIDATION_FAILED);
    require(request.targets().size() <= ATOMIC_MAX_NODES, NetworkErrors.QUOTA_EXCEEDED);
    require(
        Set.of("message", "first_contact", "reply").contains(request.kind()),
        CommonErrors.VALIDATION_FAILED);
    require(
        !request.body().isBlank()
            && request.body().getBytes(StandardCharsets.UTF_8).length <= MAX_BODY_BYTES,
        CommonErrors.VALIDATION_FAILED);
    text(request.realm(), 32);
    text(request.sourceNode(), 32);
    text(request.sourceLocal(), 32);
    int locals = 0;
    Set<String> nodes = new LinkedHashSet<>();
    for (NodeTarget target : request.targets()) {
      text(target.nodeId(), 32);
      require(nodes.add(target.nodeId()), NetworkErrors.PROTOCOL_ERROR);
      require(
          target.locals() != null && !target.locals().isEmpty(), CommonErrors.VALIDATION_FAILED);
      require(target.locals().size() <= ATOMIC_MAX_LOCALS, NetworkErrors.QUOTA_EXCEEDED);
      Set<String> seen = new LinkedHashSet<>();
      for (String local : target.locals()) {
        text(local, 32);
        require(seen.add(local), NetworkErrors.PROTOCOL_ERROR);
        locals++;
      }
    }
    require(locals <= ATOMIC_MAX_LOCALS, NetworkErrors.QUOTA_EXCEEDED);
    if ("first_contact".equals(request.kind())) {
      require(request.targets().size() == 1, NetworkErrors.PROTOCOL_ERROR);
      require(
          "conversation".equals(request.contactScope())
              || "human_dialogue".equals(request.contactScope())
              || "group_join".equals(request.contactScope()),
          CommonErrors.VALIDATION_FAILED);
      require(
          "conversation".equals(request.contactScope()),
          NetworkErrors.PROTOCOL_ERROR); // N1: conversation only
    }
  }

  private static String normalizeTransport(String status) {
    if ("rejected".equals(status)) return "revoked";
    require(status != null && TRANSPORT.contains(status), NetworkErrors.PROTOCOL_ERROR);
    return status;
  }

  private static void requireEpoch(long current, long provided) {
    require(provided == current, NetworkErrors.STALE_EPOCH);
  }

  private <T> T run(UUID tenantId, Supplier<T> work) {
    var caller =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    require(
        caller.isInternalService()
            && "svc:ai-gateway".equals(caller.subject())
            && TenantContext.PLATFORM_TENANT.equals(caller.tenantId()),
        CommonErrors.FORBIDDEN);
    require(
        TenantContext.current().filter(tenantId::equals).isPresent()
            && !TenantContext.PLATFORM_TENANT.equals(tenantId),
        CommonErrors.FORBIDDEN);
    return tx.execute(
        status -> {
          TenantTransactionManager.verifyTransactionTenant();
          return work.get();
        });
  }

  private List<Map<String, Object>> rows(String sql, Object... args) {
    return jdbc.queryForList(sql, args);
  }

  private Map<String, Object> one(String sql, Object... args) {
    var r = rows(sql, args);
    require(r.size() == 1, CommonErrors.NOT_FOUND);
    return r.getFirst();
  }

  private static void text(String s, int max) {
    require(s != null && !s.isBlank() && s.length() <= max, CommonErrors.VALIDATION_FAILED);
  }

  private static void require(boolean ok, ErrorCode error) {
    if (!ok) throw new AurigaException(error);
  }

  public record NodeTarget(String nodeId, List<String> locals) {}

  public record StoreRequest(
      UUID tenantId,
      String realm,
      String sourceNode,
      String sourceLocal,
      String kind,
      String body,
      UUID threadId,
      List<NodeTarget> targets,
      String contactScope,
      Instant contactExpiresAt,
      boolean statusViewer) {
    public static StoreRequest unicast(
        UUID tenantId,
        String realm,
        String sourceNode,
        String sourceLocal,
        String body,
        String targetNode,
        String targetLocal) {
      return new StoreRequest(
          tenantId,
          realm,
          sourceNode,
          sourceLocal,
          "message",
          body,
          null,
          List.of(new NodeTarget(targetNode, List.of(targetLocal))),
          "conversation",
          null,
          true);
    }

    public static StoreRequest firstContact(
        UUID tenantId,
        String realm,
        String sourceNode,
        String sourceLocal,
        String body,
        String targetNode,
        Instant expiresAt,
        boolean statusViewer) {
      return new StoreRequest(
          tenantId,
          realm,
          sourceNode,
          sourceLocal,
          "first_contact",
          body,
          null,
          List.of(new NodeTarget(targetNode, List.of("0"))),
          "conversation",
          expiresAt,
          statusViewer);
    }
  }

  public record DeliveryStored(
      UUID deliveryId, String targetNode, long mailSeq, List<UUID> recipientIds) {}

  public record StoreResult(
      UUID messageId,
      String durabilityStatus,
      String visibleStatus,
      boolean restrictedAppearance,
      List<DeliveryStored> deliveries,
      UUID contactId) {}

  public record NodeRoute(
      String realm, String nodeId, long connectionEpoch, long headSeq, long committedCursor) {}

  public record RecipientView(
      UUID recipientId, String targetLocal, String transportStatus, long stateVersion) {}

  public record FetchItem(
      UUID deliveryId,
      long mailSeq,
      UUID messageId,
      String body,
      String kind,
      String sourceNode,
      String sourceLocal,
      UUID threadId,
      Instant createdAt,
      List<RecipientView> recipients) {}

  public record FetchResult(NodeRoute route, List<FetchItem> items) {}

  public record ReceiptItem(UUID recipientId, String transportStatus) {}

  public record ReceiptRequest(
      UUID tenantId,
      String realm,
      String nodeId,
      long epoch,
      UUID deliveryId,
      long mailSeq,
      List<ReceiptItem> results) {}

  public record ReceiptResultView(UUID recipientId, String transportStatus, long stateVersion) {}

  public record ReceiptCommitResult(
      UUID deliveryId,
      long mailSeq,
      List<ReceiptResultView> results,
      boolean mailItemClosed,
      long committedCursor) {}

  public record ContactDecideRequest(
      UUID tenantId, UUID contactId, String decision, Boolean statusViewer) {}

  public record ContactDecideResult(
      UUID contactId,
      String status,
      String visibleStatus,
      boolean restrictedAppearance,
      String presentationCode) {}

  public record AttemptBeginRequest(
      UUID tenantId,
      String realm,
      String nodeId,
      long epoch,
      UUID deliveryId,
      UUID recipientId,
      String purpose) {}

  public record AttemptBeginResult(
      UUID recipientId, int attemptCount, int maxAttempts, String result) {}

  public record ExecutionAdmitRequest(
      UUID tenantId,
      String realm,
      String nodeId,
      long connectionEpoch,
      UUID recipientId,
      String bootId,
      String hostRequestId) {}

  public record ExecutionAdmitResult(
      UUID recipientId, String result, UUID admissionId, long admittedEpoch) {}
}
