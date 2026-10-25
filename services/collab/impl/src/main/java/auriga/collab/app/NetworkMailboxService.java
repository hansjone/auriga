package auriga.collab.app;

import auriga.collab.api.NetworkSourceAuthority;
import auriga.common.audit.AuditEntry;
import auriga.common.audit.AuditLogger;
import auriga.common.audit.OutboxAuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.error.ErrorCode;
import auriga.common.events.EventSchemas;
import auriga.common.events.OutboxEventPublisher;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AuthorizationRules;
import auriga.iam.api.NetworkAuthority;
import auriga.iam.api.NetworkAuthority.*;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.UUID;
import java.util.function.Supplier;
import org.springframework.dao.DataAccessException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

/**
 * Internal (non-proto) network mailbox API for ai-gateway HEAD/FETCH/RECEIPT wiring. Durable truth
 * lives in PostgreSQL with tenant RLS. Only fetchSigned produces network envelopes; raw fetch is an
 * internal persistence projection and must never be serialized to a plugin.
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

  static final Set<String> TERMINAL =
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
  private final NetworkSourceAuthority authority;
  private final NetworkEnvelopeSigner signer;
  private final Clock clock;
  private final NetworkIamChecks iam;
  private final NetworkContactBudget budget;
  private final AuditLogger audit;

  public NetworkMailboxService(JdbcTemplate jdbc, TenantTransactionManager manager) {
    this(jdbc, manager, null, new NetworkSigningKeys(Clock.systemUTC(), 1), Clock.systemUTC());
  }

  public NetworkMailboxService(
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkSourceAuthority authority,
      NetworkSigningKeys keys,
      Clock clock) {
    this(jdbc, manager, authority, keys, clock, null);
  }

  public NetworkMailboxService(
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkSourceAuthority authority,
      NetworkSigningKeys keys,
      Clock clock,
      NetworkAuthority networkAuthority) {
    this.jdbc = Objects.requireNonNull(jdbc);
    this.tx = new TransactionTemplate(manager);
    this.authority = authority;
    this.signer = new NetworkEnvelopeSigner(Objects.requireNonNull(keys), clock);
    this.clock = Objects.requireNonNull(clock);
    this.iam = new NetworkIamChecks(networkAuthority, manager);
    this.budget = new NetworkContactBudget(jdbc);
    this.audit =
        new OutboxAuditLogger(
            new OutboxEventPublisher(
                jdbc, "collab", "collab", new EventSchemas(), NetworkJson.JSON, clock, null));
  }

  /** READY-shaped takeover: bumps connection_epoch and returns current mailbox cursors. */
  public NodeRoute ready(UUID tenantId, String realm, String nodeId) {
    return run(
        tenantId,
        () -> {
          checkTarget(tenantId, realm, nodeId, Operation.CONNECT);
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

  /** Current route after an online RECEIVE check, without fetching message rows. */
  public NodeRoute head(UUID tenantId, String realm, String nodeId) {
    return run(
        tenantId,
        () -> {
          checkTarget(tenantId, realm, nodeId, Operation.RECEIVE);
          ensureNodeLocked(tenantId, realm, nodeId);
          return loadRoute(tenantId, realm, nodeId, null);
        });
  }

  /**
   * Atomic small-batch / unicast store: one immutable body + per-node mail items + recipients in
   * the same transaction. Returns platform_stored-shaped durability.
   */
  public StoreResult store(StoreRequest request) {
    return store(request, null);
  }

  /** Optional structured message fields retain ext values; server-owned identity is rejected. */
  public StoreResult store(StoreRequest request, String messageFieldsJson) {
    return store(request, messageFieldsJson, "1", UuidV7.next().toString());
  }

  /** Gateway passes the authenticated producer epoch and the SEND idempotency key. */
  public StoreResult store(
      StoreRequest request, String messageFieldsJson, String producerEpoch, String key) {
    require(
        !TransactionSynchronizationManager.isActualTransactionActive()
            && !TransactionSynchronizationManager.isSynchronizationActive()
            && !TransactionSynchronizationManager.hasResource(jdbc.getDataSource()),
        CommonErrors.UNAVAILABLE);
    StoreOutcome outcome;
    var auditContext = new SubmissionAudit(request.kind());
    try {
      var prepared = prepareStore(request, messageFieldsJson, producerEpoch, key, auditContext);
      var reservation =
          prepared.fingerprint() == null
              ? null
              : run(
                  request.tenantId(),
                  () -> {
                    var reserved =
                        budget.reserve(
                            request.tenantId(),
                            prepared.source(),
                            request.targets().getFirst().nodeId(),
                            producerEpoch,
                            key,
                            prepared.fingerprint(),
                            clock.instant(),
                            prepared.expires(),
                            prepared.ceilings());
                    if (reserved.error() != null && !reserved.retry())
                      recordSubmission(auditContext, reserved.error());
                    return reserved;
                  });
      if (reservation != null && (reservation.error() != null || reservation.resultJson() != null))
        outcome = reservationOutcome(reservation);
      else
        outcome = storeTransaction(request, messageFieldsJson, prepared, reservation, auditContext);
    } catch (RuntimeException e) {
      run(
          request.tenantId(),
          () -> {
            recordSubmission(auditContext, errorOf(e));
            return null;
          });
      throw e;
    }
    if (outcome.error() != null) throw new AurigaException(outcome.error());
    return outcome.result();
  }

  private record StoreOutcome(StoreResult result, ErrorCode error) {}

  private record PreparedStore(
      NetworkSourceAuthority.Source source,
      NetworkSourceAuthority.Dispatch dispatch,
      Decision ceilings,
      long bodyBytes,
      String kind,
      Instant expires,
      String fingerprint) {}

  private static final class SubmissionAudit {
    NetworkSourceAuthority.Source source;
    NetworkSourceAuthority.Dispatch dispatch;
    UUID messageId;
    String kind;

    SubmissionAudit(String requestKind) {
      kind = "first_contact".equals(requestKind) ? "first_contact" : "send";
    }
  }

  private PreparedStore prepareStore(
      StoreRequest request,
      String messageFieldsJson,
      String producerEpoch,
      String key,
      SubmissionAudit auditContext) {
    return run(
        request.tenantId(),
        () -> {
          validateStore(request);
          require(authority != null, CommonErrors.UNAVAILABLE);
          var source = authority.currentSource(request.tenantId());
          auditContext.source = source;
          var dispatch = authority.currentDispatch(request.tenantId());
          auditContext.dispatch = dispatch;
          auditContext.kind =
              "first_contact".equals(request.kind())
                  ? "first_contact"
                  : dispatch != null
                          && "1"
                              .equals(
                                  NetworkJson.parse(dispatch.addressJson())
                                      .path("local")
                                      .asString())
                      ? "broadcast"
                      : "send";
          require(
              source != null
                  && request.realm().equals(source.realm())
                  && request.sourceNode().equals(source.node())
                  && request.sourceLocal().equals(source.local()),
              NetworkErrors.SENDER_MISMATCH);
          require(
              source.validUntil() != null
                  && source.validUntil().isAfter(clock.instant())
                  && source.user() != null
                  && !source.user().isBlank(),
              NetworkErrors.UNAUTHORIZED);
          var body =
              messageFieldsJson == null ? null : NetworkJson.parse(messageFieldsJson).get("body");
          if (body == null)
            body = NetworkJson.object().put("subtype", "text").put("text", request.body());
          long bodyBytes = NetworkJson.canonical(body).getBytes(StandardCharsets.UTF_8).length;
          iam.active(source.realm(), source.node());
          var ceilings =
              iam.check(
                  new Check(
                      source.realm(),
                      source.node(),
                      source.node(),
                      source.local(),
                      sender(source.senderKind()),
                      Operation.CONNECT,
                      bodyBytes,
                      0,
                      0));
          require(
              request.targets().size()
                      <= NetworkIamChecks.limit(ceilings, Limit.ATOMIC_TARGET_NODES)
                  && request.targets().stream().mapToLong(t -> t.locals().size()).sum()
                      <= NetworkIamChecks.limit(ceilings, Limit.ATOMIC_TARGET_LOCALS),
              NetworkErrors.QUOTA_EXCEEDED);
          var firstTarget = request.targets().getFirst();
          boolean firstContact = "first_contact".equals(request.kind());
          boolean broadcast =
              dispatch != null
                  && "1".equals(NetworkJson.parse(dispatch.addressJson()).path("local").asString());
          String auditedKind = firstContact ? "first_contact" : broadcast ? "broadcast" : null;
          auditContext.kind = auditedKind == null ? "send" : auditedKind;
          Instant contactExpiry =
              request.contactExpiresAt() == null
                  ? clock.instant().plusSeconds(FIRST_CONTACT_MAX_SECONDS)
                  : request.contactExpiresAt();
          String fingerprint = null;
          if (firstContact) {
            // Check the source capability against its own registered node before inspecting the
            // requested target. A missing agent grant is a source error, never a private success.
            ceilings =
                iam.check(
                    new Check(
                        source.realm(),
                        source.node(),
                        source.node(),
                        source.local(),
                        sender(source.senderKind()),
                        Operation.FIRST_CONTACT,
                        bodyBytes,
                        1,
                        1));
            require(
                dispatch == null && firstTarget.locals().equals(List.of("0")),
                NetworkErrors.PROTOCOL_ERROR);
            // Validate all source-controlled fields/size before any target-dependent branch.
            var probe =
                NetworkSourceStamp.message(
                    request,
                    source,
                    UuidV7.next(),
                    clock.instant(),
                    messageFieldsJson,
                    null,
                    "system");
            probe.put("thread_sequence", Long.toString(Long.MAX_VALUE));
            preflight(request, probe, NetworkSourceStamp.context(source, probe), null);
            Instant messageExpiry = Instant.parse(probe.path("expires_at").asString());
            if (messageExpiry.isBefore(contactExpiry)) contactExpiry = messageExpiry;
            require(
                producerEpoch != null
                    && producerEpoch.matches("[1-9][0-9]{0,19}")
                    && new java.math.BigInteger(producerEpoch).bitLength() <= 64
                    && key != null
                    && key.matches("[A-Za-z0-9][A-Za-z0-9._:-]{0,127}"),
                NetworkErrors.PROTOCOL_ERROR);
            require(
                !contactExpiry.isAfter(clock.instant().plusSeconds(FIRST_CONTACT_MAX_SECONDS)),
                CommonErrors.VALIDATION_FAILED);
            var fingerprintObject = NetworkJson.object();
            fingerprintObject.put(
                "request", NetworkJson.digest(NetworkJson.JSON.valueToTree(request)));
            fingerprintObject.put(
                "fields",
                messageFieldsJson == null
                    ? ""
                    : NetworkJson.digest(NetworkJson.parse(messageFieldsJson)));
            fingerprint = NetworkJson.digest(fingerprintObject);
          }
          return new PreparedStore(
              source, dispatch, ceilings, bodyBytes, auditedKind, contactExpiry, fingerprint);
        });
  }

  private StoreOutcome storeTransaction(
      StoreRequest request,
      String messageFieldsJson,
      PreparedStore prepared,
      NetworkContactBudget.Reservation initialReservation,
      SubmissionAudit auditContext) {
    return run(
        request.tenantId(),
        () -> {
          var source = prepared.source();
          var dispatch = prepared.dispatch();
          var firstTarget = request.targets().getFirst();
          boolean firstContact = prepared.fingerprint() != null;
          boolean broadcast = "broadcast".equals(prepared.kind());
          long bodyBytes = prepared.bodyBytes();
          Instant contactExpiry =
              initialReservation == null
                  ? prepared.expires()
                  : budget.visibleUntil(request.tenantId(), initialReservation.id());
          var reservation = initialReservation;
          try {
            var active =
                iam.activeBatch(
                    request.targets().stream()
                        .map(t -> new NodeReference(request.realm(), t.nodeId()))
                        .toList());
            for (var target : request.targets()) {
              require(
                  Boolean.TRUE.equals(
                      active.get(new NodeReference(request.realm(), target.nodeId()))),
                  NetworkErrors.UNAUTHORIZED);
              iam.check(
                  new Check(
                      source.realm(),
                      source.node(),
                      target.nodeId(),
                      source.local(),
                      sender(source.senderKind()),
                      firstContact
                          ? Operation.FIRST_CONTACT
                          : broadcast ? Operation.BROADCAST : Operation.SEND,
                      bodyBytes,
                      broadcast ? request.targets().size() : 1,
                      broadcast
                          ? request.targets().stream().mapToLong(t -> t.locals().size()).sum()
                          : 1));
            }
          } catch (AurigaException e) {
            return targetFailure(request, reservation, auditContext, e.errorCode());
          }
          String targetType;
          try {
            targetType =
                dispatch == null
                    ? authority.resolveTargetType(
                        request.tenantId(),
                        request.realm(),
                        firstTarget.nodeId(),
                        firstTarget.locals().getFirst())
                    : null;
            require(dispatch != null || targetType != null, CommonErrors.UNAVAILABLE);
          } catch (DataAccessException e) {
            throw e;
          } catch (RuntimeException e) {
            return targetFailure(request, reservation, auditContext, errorOf(e));
          }
          UUID messageId = UuidV7.next();
          auditContext.messageId = messageId;
          ObjectNode message =
              NetworkSourceStamp.message(
                  request,
                  source,
                  messageId,
                  clock.instant(),
                  messageFieldsJson,
                  dispatch,
                  targetType);
          UUID threadId = UUID.fromString(message.path("thread_id").asString());
          String sequence = nextThreadSequence(request.tenantId(), threadId);
          message.put("thread_sequence", sequence);
          NetworkSchemas.validate("Message", message);
          verifyMessageReferences(request.tenantId(), source, message);
          ObjectNode sourceContext = NetworkSourceStamp.context(source, message);
          String messageJson = NetworkJson.canonical(message);
          String contextJson = NetworkJson.canonical(sourceContext);
          String dispatchJson = null;
          if (dispatch != null) {
            dispatchJson =
                NetworkJson.canonical(
                    NetworkSourceStamp.dispatchContext(dispatch, request.targets()));
          }
          for (var target : request.targets()) {
            try {
              Instant deadline =
                  authority.authorizeDelivery(
                      request.tenantId(), messageJson, contextJson, target.nodeId());
              require(
                  deadline != null && deadline.isAfter(clock.instant()),
                  NetworkErrors.UNAUTHORIZED);
            } catch (DataAccessException e) {
              throw e;
            } catch (RuntimeException e) {
              return targetFailure(request, reservation, auditContext, errorOf(e));
            }
          }
          try {
            preflight(request, message, sourceContext, dispatchJson);
          } catch (DataAccessException e) {
            throw e;
          } catch (RuntimeException e) {
            if (reservation != null && !request.statusViewer())
              return targetFailure(request, reservation, auditContext, errorOf(e));
            throw e;
          }
          if (reservation != null) {
            reservation = budget.current(request.tenantId(), reservation.id());
            if (reservation.error() != null || reservation.resultJson() != null)
              return reservationOutcome(reservation);
          }
          jdbc.queryForList(
              "SELECT pg_advisory_xact_lock(hashtextextended(?,0))",
              request.tenantId() + "/" + threadId);
          if (!sequence.equals(nextThreadSequence(request.tenantId(), threadId)))
            return targetFailure(request, reservation, auditContext, NetworkErrors.CONFLICT);
          jdbc.update(
              """
              INSERT INTO collab.network_message(
                id, tenant_id, realm, kind, body, source_node, source_local, thread_id,
                message_json, source_context_json, dispatch_context_json)
              VALUES (?,?,?,?,?,?,?,?,?,?,?)
              """,
              messageId,
              request.tenantId(),
              request.realm(),
              request.kind(),
              request.body(),
              request.sourceNode(),
              request.sourceLocal(),
              threadId,
              messageJson,
              contextJson,
              dispatchJson);

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
            Instant expires = contactExpiry;
            require(
                !expires.isAfter(clock.instant().plusSeconds(FIRST_CONTACT_MAX_SECONDS)),
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
          var result =
              new StoreResult(
                  messageId,
                  "platform_stored",
                  visible,
                  restricted,
                  List.copyOf(deliveries),
                  contactId,
                  null,
                  null);
          if (reservation != null) {
            budget.materialized(request.tenantId(), reservation.id(), messageId);
            if (!request.statusViewer()) result = submission(request.tenantId(), reservation.id());
            budget.complete(request.tenantId(), reservation.id(), result, null);
          }
          recordSubmission(auditContext, null);
          return new StoreOutcome(result, null);
        });
  }

  /** Internal database projection; not a network payload. Does not mutate transport status. */
  public FetchResult fetch(
      UUID tenantId, String realm, String nodeId, long afterCursor, int limit) {
    return fetch(tenantId, realm, nodeId, afterCursor, limit, true);
  }

  private FetchResult fetch(
      UUID tenantId, String realm, String nodeId, long afterCursor, int limit, boolean authorize) {
    return run(
        tenantId,
        () -> {
          require(afterCursor >= 0, CommonErrors.VALIDATION_FAILED);
          require(limit >= 1 && limit <= FETCH_MAX_ITEMS, CommonErrors.VALIDATION_FAILED);
          if (authorize) {
            checkTarget(tenantId, realm, nodeId, Operation.RECEIVE);
            ensureNodePresent(tenantId, realm, nodeId);
          }
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
   * Builds signed node envelopes exclusively from committed rows. The caller must wrap the item
   * list and cursors in the separately signed FETCH_RESULT transport envelope. No unsigned
   * fallback.
   */
  public List<String> fetchSigned(
      UUID tenantId, String realm, String nodeId, long afterCursor, int limit) {
    var frame = NetworkJson.parse(fetchPage(tenantId, realm, nodeId, afterCursor, limit, 65536));
    List<String> items = new ArrayList<>();
    for (var item : frame.path("items")) items.add(NetworkJson.canonical(item));
    return List.copyOf(items);
  }

  /** Signed page/manifest plus independently signed items, bounded by the entire wire frame. */
  public String fetchPage(
      UUID tenantId, String realm, String nodeId, long afterCursor, int limit, int maxBytes) {
    return run(
        tenantId,
        () -> {
          require(authority != null, CommonErrors.UNAVAILABLE);
          require(maxBytes >= 4096 && maxBytes <= 65536, CommonErrors.VALIDATION_FAILED);
          var target = authority.currentTarget(tenantId);
          require(
              target != null && realm.equals(target.realm()) && nodeId.equals(target.node()),
              NetworkErrors.UNAUTHORIZED);
          require(
              target.validUntil() != null && target.validUntil().isAfter(clock.instant()),
              NetworkErrors.UNAUTHORIZED);
          var raw = fetch(tenantId, realm, nodeId, afterCursor, limit);
          require(target.epoch() > 0, NetworkErrors.STALE_EPOCH);
          requireEpoch(raw.route().connectionEpoch(), target.epoch());
          require(afterCursor <= raw.route().headSeq(), NetworkErrors.PROTOCOL_ERROR);
          var context =
              new NetworkEnvelopeSigner.Context(
                  realm,
                  nodeId,
                  target.incarnation(),
                  target.epoch(),
                  target.channelNonce(),
                  target.requestId());
          var permissions =
              new NetworkDispatchAuthorization(jdbc, iam, authority, clock)
                  .check(tenantId, raw.items(), realm, nodeId);
          // Decisions are complete before any expiry, cursor, or audit mutation takes a row lock.
          var envelopes = new NetworkMailboxEnvelopeFactory(signer);
          ensureNodeLocked(tenantId, realm, nodeId);
          var current = fetch(tenantId, realm, nodeId, afterCursor, limit, false);
          var route = current.route();
          requireEpoch(route.connectionEpoch(), target.epoch());
          var authorizedIds = new LinkedHashSet<UUID>();
          for (var item : raw.items()) authorizedIds.add(item.deliveryId());
          List<ObjectNode> items = new ArrayList<>();
          ObjectNode frame =
              envelopes.page(items, route, afterCursor, context, target.validUntil());
          require(frameFits(frame, maxBytes), NetworkErrors.PROTOCOL_ERROR);
          for (var item : current.items()) {
            if (!authorizedIds.contains(item.deliveryId())) continue;
            ObjectNode candidate;
            try {
              candidate =
                  signedMailboxItem(
                      tenantId,
                      item,
                      context,
                      target.validUntil(),
                      envelopes,
                      permissions.get(item.deliveryId()));
            } catch (AurigaException e) {
              String code = e.errorCode().code();
              String reason =
                  Set.of("network.unauthorized", "common.forbidden", "common.unavailable")
                          .contains(code)
                      ? "awaiting_authorization"
                      : "quarantined";
              auditItem(tenantId, item, e.errorCode(), "quarantined".equals(reason));
              candidate = envelopes.gap(item.mailSeq(), reason, context, target.validUntil());
            } catch (DataAccessException e) {
              throw e;
            } catch (RuntimeException e) {
              auditItem(tenantId, item, NetworkErrors.PROTOCOL_ERROR, true);
              candidate =
                  envelopes.gap(item.mailSeq(), "quarantined", context, target.validUntil());
            }
            if ("terminal".equals(candidate.path("type").asString())
                && item.recipients().stream()
                    .anyMatch(r -> !TERMINAL.contains(r.transportStatus())))
              route = loadRoute(tenantId, realm, nodeId, null);
            if (!frameFits(
                envelopes.page(
                    List.of(candidate), route, item.mailSeq(), context, target.validUntil()),
                maxBytes)) {
              candidate =
                  envelopes.gap(item.mailSeq(), "quarantined", context, target.validUntil());
            }
            var proposed = new ArrayList<>(items);
            proposed.add(candidate);
            var next =
                envelopes.page(proposed, route, item.mailSeq(), context, target.validUntil());
            if (!frameFits(next, maxBytes)) break;
            items.add(candidate);
            recordDispatchUse(tenantId, item, candidate);
            frame = next;
          }
          return NetworkJson.canonical(frame);
        });
  }

  private ObjectNode signedMailboxItem(
      UUID tenantId,
      FetchItem item,
      NetworkEnvelopeSigner.Context context,
      Instant deadline,
      NetworkMailboxEnvelopeFactory envelopes,
      NetworkDispatchAuthorization.Outcome authorization) {
    if (item.recipients().stream().allMatch(r -> TERMINAL.contains(r.transportStatus())))
      return envelopes.terminal(item, item.recipients(), context, deadline);
    var stored =
        one(
            "SELECT message_json, source_context_json, dispatch_context_json FROM collab.network_message WHERE tenant_id=? AND id=?",
            tenantId,
            item.messageId());
    require(
        stored.get("message_json") != null && stored.get("source_context_json") != null,
        NetworkErrors.PROTOCOL_ERROR);
    ObjectNode message = (ObjectNode) NetworkJson.parse(stored.get("message_json").toString());
    ObjectNode source =
        (ObjectNode) NetworkJson.parse(stored.get("source_context_json").toString());
    ObjectNode contact = null;
    Instant expires = Instant.parse(message.path("expires_at").asString());
    if ("first_contact".equals(item.kind())) {
      var row =
          one(
              "SELECT status, decision_version, expires_at FROM collab.network_contact WHERE tenant_id=? AND message_id=?",
              tenantId,
              item.messageId());
      Instant contactExpiry =
          ((java.sql.Timestamp) row.get("expires_at"))
              .toInstant()
              .truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
      contact =
          NetworkJson.object()
              .put("status", row.get("status").toString())
              .put("decision_version", row.get("decision_version").toString())
              .put("expires_at", contactExpiry.toString());
      if (contactExpiry.isBefore(expires)) expires = contactExpiry;
    }
    if (!expires.isAfter(clock.instant())) {
      jdbc.update(
          "UPDATE collab.network_recipient SET transport_status='expired', state_version=state_version+1 WHERE tenant_id=? AND delivery_id=? AND transport_status IN ('pending','quarantined')",
          tenantId,
          item.deliveryId());
      // Contact lifecycle transitions use expireContactIfDue's contact -> node lock order.
      // Authority calls for the whole page have finished before expiry acquires node locks.
      advanceCommittedCursor(tenantId, context.realm(), context.node());
      var recipients =
          item.recipients().stream()
              .map(
                  r ->
                      TERMINAL.contains(r.transportStatus())
                          ? r
                          : new RecipientView(
                              r.recipientId(), r.targetLocal(), "expired", r.stateVersion() + 1))
              .toList();
      return envelopes.terminal(item, recipients, context, deadline);
    }
    if (item.recipients().stream().anyMatch(r -> "quarantined".equals(r.transportStatus())))
      return envelopes.gap(item.mailSeq(), "quarantined", context, deadline);
    require(authorization != null, CommonErrors.UNAVAILABLE);
    if (authorization.failure() != null) throw authorization.failure();
    var checkedDispatch =
        stored.get("dispatch_context_json") == null
            ? null
            : NetworkJson.parse(stored.get("dispatch_context_json").toString());
    require(
        NetworkDispatchAuthorization.fingerprint(message, source, checkedDispatch)
            .equals(authorization.fingerprint()),
        NetworkErrors.PROTOCOL_ERROR);
    Instant permission = authorization.deadline();
    require(permission != null && permission.isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
    if (permission.isBefore(deadline)) deadline = permission;
    if (expires.isBefore(deadline)) deadline = expires;
    var dispatch =
        stored.get("dispatch_context_json") == null
            ? null
            : NetworkJson.parse(stored.get("dispatch_context_json").toString());
    var result = envelopes.delivery(item, message, source, dispatch, contact, context, deadline);

    return result;
  }

  private static boolean frameFits(ObjectNode frame, int budget) {
    try {
      return NetworkJson.canonical(frame).getBytes(StandardCharsets.UTF_8).length <= budget;
    } catch (AurigaException e) {
      if (!e.errorCode().code().equals(NetworkErrors.PROTOCOL_ERROR.code())) throw e;
      return false;
    }
  }

  private static Sender sender(String kind) {
    require(Set.of("human", "agent").contains(kind), NetworkErrors.SENDER_MISMATCH);
    return "human".equals(kind) ? Sender.HUMAN : Sender.AGENT;
  }

  private void checkTarget(UUID tenant, String realm, String node, Operation operation) {
    text(realm, 32);
    text(node, 32);
    require(authority != null, CommonErrors.UNAVAILABLE);
    var target = authority.currentTarget(tenant);
    require(
        target != null
            && realm.equals(target.realm())
            && node.equals(target.node())
            && target.validUntil() != null
            && target.validUntil().isAfter(clock.instant()),
        NetworkErrors.UNAUTHORIZED);
    iam.active(realm, node);
    iam.check(new Check(realm, node, node, "0", Sender.HUMAN, operation, 0, 1, 1));
  }

  private StoreResult submission(UUID tenant, UUID id) {
    // Restricted replies expose no target-derived message, contact, delivery IDs or platform ACK.
    return new StoreResult(
        null,
        "request_submitted",
        "request_submitted",
        true,
        List.of(),
        null,
        id,
        budget.visibleUntil(tenant, id));
  }

  private StoreOutcome targetFailure(
      StoreRequest request,
      NetworkContactBudget.Reservation reservation,
      SubmissionAudit auditContext,
      ErrorCode error) {
    if (reservation != null) {
      // Compete with successful materialization only after the external authority has returned.
      reservation = budget.current(request.tenantId(), reservation.id());
      if (reservation.error() != null || reservation.resultJson() != null)
        return reservationOutcome(reservation);
    }
    recordSubmission(auditContext, error);
    if (reservation == null) return new StoreOutcome(null, error);
    if (!request.statusViewer() && !CommonErrors.UNAVAILABLE.code().equals(error.code())) {
      var submitted = submission(request.tenantId(), reservation.id());
      budget.complete(request.tenantId(), reservation.id(), submitted, null);
      return new StoreOutcome(submitted, null);
    }
    budget.complete(request.tenantId(), reservation.id(), null, error);
    return new StoreOutcome(null, error);
  }

  private static ErrorCode errorOf(RuntimeException error) {
    return error instanceof AurigaException a ? a.errorCode() : CommonErrors.INTERNAL;
  }

  private static StoreOutcome reservationOutcome(NetworkContactBudget.Reservation reservation) {
    return new StoreOutcome(
        reservation.resultJson() == null
            ? null
            : NetworkJson.JSON.readValue(reservation.resultJson(), StoreResult.class),
        reservation.error());
  }

  private String nextThreadSequence(UUID tenant, UUID thread) {
    return jdbc.queryForObject(
        """
        SELECT coalesce(max((message_json::jsonb->>'thread_sequence')::numeric),0)+1
        FROM collab.network_message WHERE tenant_id=? AND thread_id=?
        """,
        String.class,
        tenant,
        thread);
  }

  private void recordSubmission(SubmissionAudit context, ErrorCode error) {
    if ("send".equals(context.kind) && error == null) return;
    var entry =
        AuditEntry.builder("collab.network." + context.kind + ".submit")
            .actor(new AuditEntry.Actor("system", "svc:collab"))
            .target(
                "network_operation", context.source == null ? "unverified" : context.source.node())
            .detail("message_id", context.messageId == null ? null : context.messageId.toString());
    if (context.source != null)
      entry
          .detail("user", context.source.user())
          .detail("source_node", context.source.node())
          .detail("source_local", context.source.local());
    if (context.dispatch != null)
      entry
          .detail("snapshot_id", context.dispatch.snapshotId())
          .detail("broadcast_grant_id", context.dispatch.broadcastGrantId())
          .detail("grant_revision", context.dispatch.grantRevision());
    if (error != null) entry.denied(error.code());
    audit.record(entry.build());
  }

  private void auditItem(UUID tenantId, FetchItem item, ErrorCode error, boolean corrupted) {
    if (corrupted
        && jdbc.update(
                """
            UPDATE collab.network_mail_item SET corruption_audited=true
            WHERE tenant_id=? AND id=? AND NOT corruption_audited
            """,
                tenantId,
                item.deliveryId())
            == 0) return;
    if (!corrupted
        && jdbc.update(
                """
        UPDATE collab.network_mail_item SET authorization_audit_state=?
        WHERE tenant_id=? AND id=? AND authorization_audit_state IS DISTINCT FROM ?
        """,
                error.code(),
                tenantId,
                item.deliveryId(),
                error.code())
            == 0) return;
    // Never log body, display labels, raw exceptions or damaged JSON.
    audit.record(
        AuditEntry.builder("collab.network.dispatch")
            .actor(new AuditEntry.Actor("system", "svc:collab"))
            .target("message", item.messageId().toString())
            .denied(error.code())
            .build());
  }

  private void recordDispatchUse(UUID tenant, FetchItem item, ObjectNode envelope) {
    if (!Set.of("delivery", "contact_prompt").contains(envelope.path("type").asString())) return;
    jdbc.update(
        "UPDATE collab.network_mail_item SET authorization_audit_state=NULL WHERE tenant_id=? AND id=? AND authorization_audit_state IS NOT NULL",
        tenant,
        item.deliveryId());
    String kind =
        "first_contact".equals(item.kind())
            ? "first_contact"
            : "broadcast".equals(envelope.path("message").path("message_class").asString())
                ? "broadcast"
                : null;
    if (kind == null
        || jdbc.update(
                """
        UPDATE collab.network_mail_item SET dispatch_audited=true
        WHERE tenant_id=? AND id=? AND NOT dispatch_audited
        """,
                tenant,
                item.deliveryId())
            == 0) return;
    var message = envelope.path("message");
    audit.record(
        AuditEntry.builder("collab.network." + kind + ".dispatch")
            .actor(new AuditEntry.Actor("system", "svc:collab"))
            .target("message", item.messageId().toString())
            .detail("message_id", item.messageId().toString())
            .detail("delivery_id", item.deliveryId().toString())
            .detail("user", envelope.path("source_context").path("user").asString())
            .detail("snapshot_id", envelope.path("snapshot_id").asString(null))
            .detail(
                "broadcast_grant_id",
                message.path("authorization_ref").path("broadcast_grant_id").asString(null))
            .build());
  }

  private void preflight(
      StoreRequest request, ObjectNode message, ObjectNode source, String dispatchJson) {
    var factory = new NetworkMailboxEnvelopeFactory(signer);
    var dispatch = dispatchJson == null ? null : NetworkJson.parse(dispatchJson);
    var maximumOpaque = "x".repeat(128);
    for (var target : request.targets()) {
      var context =
          new NetworkEnvelopeSigner.Context(
              request.realm(),
              target.nodeId(),
              maximumOpaque,
              Long.MAX_VALUE,
              maximumOpaque,
              maximumOpaque);
      var recipients =
          target.locals().stream()
              .map(local -> new RecipientView(UuidV7.next(), local, "pending", Long.MAX_VALUE))
              .toList();
      var item =
          new FetchItem(
              UuidV7.next(),
              Long.MAX_VALUE,
              UUID.fromString(message.path("message_id").asString()),
              request.body(),
              request.kind(),
              request.sourceNode(),
              request.sourceLocal(),
              request.threadId(),
              clock.instant(),
              recipients);
      ObjectNode contact = null;
      if ("first_contact".equals(request.kind()))
        contact =
            NetworkJson.object()
                .put("status", "allowed")
                .put("decision_version", Long.toString(Long.MAX_VALUE))
                .put("expires_at", message.path("expires_at").asString());
      var deadline = Instant.parse(message.path("expires_at").asString());
      var envelope = factory.delivery(item, message, source, dispatch, contact, context, deadline);
      var frame =
          factory.page(
              List.of(envelope),
              new NodeRoute(
                  request.realm(), target.nodeId(), Long.MAX_VALUE, Long.MAX_VALUE, Long.MAX_VALUE),
              Long.MAX_VALUE,
              context,
              deadline);
      require(frameFits(frame, 65536), NetworkErrors.PROTOCOL_ERROR);
    }
  }

  private void verifyMessageReferences(
      UUID tenantId, NetworkSourceAuthority.Source source, ObjectNode message) {
    if (message.path("in_reply_to").isNull()) return;
    require("reply".equals(message.path("kind").asString()), NetworkErrors.UNAUTHORIZED);
    UUID originalId;
    try {
      originalId = UUID.fromString(message.path("in_reply_to").asString());
    } catch (IllegalArgumentException e) {
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    }
    var original =
        jdbc.queryForList(
            """
        SELECT m.message_json, r.id AS recipient_id FROM collab.network_message m
        JOIN collab.network_mail_item i ON i.tenant_id=m.tenant_id AND i.message_id=m.id
        JOIN collab.network_recipient r ON r.tenant_id=i.tenant_id AND r.delivery_id=i.id
        WHERE m.tenant_id=? AND m.id=? AND i.target_realm=? AND i.target_node=? AND r.target_local=?
        """,
            tenantId,
            originalId,
            source.realm(),
            source.node(),
            source.local());
    require(
        original.size() == 1 && original.getFirst().get("message_json") != null,
        NetworkErrors.UNAUTHORIZED);
    var previous = NetworkJson.parse(original.getFirst().get("message_json").toString());
    require(
        previous.path("thread_id").equals(message.path("thread_id")), NetworkErrors.UNAUTHORIZED);
    if ("group".equals(message.path("reply_mode").asString()))
      require(previous.path("to").equals(message.path("to")), NetworkErrors.UNAUTHORIZED);
    else require(previous.path("from").equals(message.path("to")), NetworkErrors.UNAUTHORIZED);
    if (!"unicast".equals(previous.path("message_class").asString()))
      message
          .putObject("origin")
          .put("message_id", originalId.toString())
          .put("recipient_id", original.getFirst().get("recipient_id").toString());
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
          checkTarget(request.tenantId(), request.realm(), request.nodeId(), Operation.RECEIVE);
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
          checkTarget(
              request.tenantId(),
              row.get("target_realm").toString(),
              row.get("target_node").toString(),
              Operation.RECEIVE);
          iam.active(row.get("source_realm").toString(), row.get("source_node").toString());
          require("conversation".equals(row.get("scope").toString()), NetworkErrors.PROTOCOL_ERROR);
          Instant expires = ((java.sql.Timestamp) row.get("expires_at")).toInstant();
          String status = row.get("status").toString();
          boolean viewer =
              request.statusViewer() != null
                  ? request.statusViewer()
                  : Boolean.TRUE.equals(row.get("status_viewer"));

          if ("pending".equals(status) && !expires.isAfter(clock.instant())) {
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
          if ("pending".equals(status) && !expires.isAfter(clock.instant())) {
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
          checkTarget(request.tenantId(), request.realm(), request.nodeId(), Operation.RECEIVE);
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
          checkTarget(request.tenantId(), request.realm(), request.nodeId(), Operation.RECEIVE);
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

  private void ensureNodePresent(UUID tenantId, String realm, String nodeId) {
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
  }

  private void ensureNodeLocked(UUID tenantId, String realm, String nodeId) {
    ensureNodePresent(tenantId, realm, nodeId);
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
      UUID contactId,
      UUID submissionId,
      Instant waitUntil) {}

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
