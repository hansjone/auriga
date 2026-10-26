package auriga.iam;

import static auriga.common.error.CommonErrors.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import com.google.protobuf.InvalidProtocolBufferException;
import com.google.protobuf.Message;
import com.google.protobuf.Parser;
import java.math.BigDecimal;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.sql.Timestamp;
import java.time.*;
import java.time.temporal.ChronoUnit;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionSynchronizationManager;

/**
 * Package-only persistence kernel, deliberately NOT a Bean or authority. Caller authenticates the
 * service/tenant and completes all remote checks outside transactions, then revalidates under
 * tenant network-authority FIRST -> session -> device -> account/policy/ingress locks before
 * constructing a VerifiedSnapshot in that SAME transaction. Only then enter operation -> chain ->
 * source locks below. No callback or remote work runs here.
 */
final class IamNetworkSources {
  private final IamStore store;
  private final JdbcTemplate jdbc;
  private final Clock clock;

  IamNetworkSources(IamStore store, Clock clock) {
    this.store = store;
    this.jdbc = store.networkJdbc();
    this.clock = clock;
  }

  /**
   * Internal attestation, not constructible by a wire request. No production authority supplies it
   * yet.
   */
  static final class VerifiedSnapshot {
    private final Object transaction;
    private final UUID tenant;
    private final NetworkAuthorityConnection connection;
    private final NetworkAgentSourceRegistration registration;
    private final UUID owner, device, session;
    private final Instant deadline;

    private VerifiedSnapshot(
        Object transaction,
        UUID tenant,
        NetworkAuthorityConnection connection,
        NetworkAgentSourceRegistration registration,
        UUID owner,
        UUID device,
        UUID session,
        Instant deadline) {
      this.transaction = transaction;
      this.tenant = tenant;
      this.connection = connection;
      this.registration = registration;
      this.owner = Objects.requireNonNull(owner);
      this.device = Objects.requireNonNull(device);
      this.session = Objects.requireNonNull(session);
      this.deadline = Objects.requireNonNull(deadline);
    }
  }

  VerifiedSnapshot verified(
      NetworkAuthorityConnection connection,
      NetworkAgentSourceRegistration registration,
      UUID owner,
      UUID device,
      UUID session,
      Instant deadline) {
    check(deadline != null, VALIDATION_FAILED);
    return new VerifiedSnapshot(
        transaction(),
        TenantContext.require(),
        connection,
        registration,
        owner,
        device,
        session,
        deadline);
  }

  /** Saved facts only: intentionally cannot manufacture a Get RPC policy_revision/valid_until. */
  record View(NetworkAgentSource source, boolean stopped, Instant observedAt) {}

  private record Anchor(
      UUID id,
      UUID ingress,
      NetworkAuthorityConnection connection,
      UUID owner,
      UUID device,
      UUID session) {}

  private record Chain(
      UUID id, byte[] registration, int scope, UUID anchor, UUID head, boolean stopped) {}

  private record Source(UUID id, UUID chain, UUID anchor, NetworkAgentSource fact) {}

  private record Operation(byte[] digest, byte[] request, byte[] response, UUID source) {}

  RegisterAgentSourceResponse registerReceipt(RegisterAgentSourceRequest request) {
    validate(request);
    transaction();
    var op = operation("Register", request.getOperationId());
    return op == null ? null : parse(receipt(op, request), RegisterAgentSourceResponse.parser());
  }

  RegisterAgentSourceResponse register(
      RegisterAgentSourceRequest request, VerifiedSnapshot verified) {
    transaction();
    return store.atomic(() -> registerWithin(request, verified));
  }

  private RegisterAgentSourceResponse registerWithin(
      RegisterAgentSourceRequest request, VerifiedSnapshot verified) {
    var saved =
        registerReceipt(
            request); // Original transport/CAS/clock is irrelevant to committed receipts.
    if (saved != null) return saved;
    Anchor anchor = fresh(verified, request.getConnection(), request.getRegistration());
    operationLock("Register", request.getOperationId());
    saved =
        registerReceipt(
            request); // Another transaction may have committed while we checked identity.
    if (saved != null) return saved;
    var r = request.getRegistration();
    store.lock("network-source-chain:" + r.getRegistrationId() + ":" + request.getScopeValue());
    Chain chain = chain(r.getRegistrationId(), request.getScopeValue());
    UUID predecessor = null;
    if (chain == null) {
      check(request.getPredecessorBinding().isEmpty(), CONFLICT);
      retain(anchor);
      chain =
          new Chain(
              UuidV7.next(), r.toByteArray(), request.getScopeValue(), anchor.id(), null, false);
      jdbc.update(
          "INSERT INTO iam.network_source_chain(id,tenant_id,registration_id,registration,scope,anchor_id) VALUES (?,?,?,?,?,?)",
          chain.id(),
          tenant(),
          r.getRegistrationId(),
          chain.registration(),
          chain.scope(),
          chain.anchor());
    } else {
      check(!chain.stopped() && Arrays.equals(chain.registration(), r.toByteArray()), CONFLICT);
      check(sameIdentity(anchor(chain.anchor()), anchor), FORBIDDEN);
      check(
          !request.getPredecessorBinding().isEmpty()
              && chain.head() != null
              && chain.head().toString().equals(request.getPredecessorBinding()),
          CONFLICT);
      Source previous = observe(source(chain.head(), true));
      check(
          previous.fact().getStateValue() == 3
              && previous.fact().getRevision() == request.getExpectedRevision(),
          CONFLICT);
      predecessor = previous.id();
      retain(anchor);
    }
    Instant now = clock.instant().truncatedTo(ChronoUnit.SECONDS);
    check(now.isBefore(verified.deadline), FORBIDDEN);
    UUID id = UuidV7.next();
    var fact =
        NetworkAgentSource.newBuilder()
            .setSourceBinding(id.toString())
            .setOriginalConnection(request.getConnection())
            .setRegistration(r)
            .setScopeValue(request.getScopeValue())
            .setRevision(1)
            .setStateValue(1)
            .setOwnerUserId(anchor.owner().toString())
            .setNotBefore(now.toString())
            .setExpiresAt(verified.deadline.toString())
            .setPredecessorBinding(predecessor == null ? "" : predecessor.toString())
            .build();
    jdbc.update(
        "INSERT INTO iam.network_source(id,tenant_id,chain_id,anchor_id,predecessor,fact,not_before,expires_at,state,revision) VALUES (?,?,?,?,?,?,?,?,1,1)",
        id,
        tenant(),
        chain.id(),
        anchor.id(),
        predecessor,
        fact.toByteArray(),
        Timestamp.from(now),
        Timestamp.from(verified.deadline));
    jdbc.update(
        "UPDATE iam.network_source_chain SET head=? WHERE tenant_id=? AND id=?",
        id,
        tenant(),
        chain.id());
    var response = RegisterAgentSourceResponse.newBuilder().setSource(fact).build();
    saveOperation("Register", request.getOperationId(), request, response, id);
    return response;
  }

  View get(GetAgentSourceRequest request, VerifiedSnapshot verified) {
    transaction();
    validate(request);
    Source initial = source(binding(request.getSourceBinding()), false);
    check(initial != null, FORBIDDEN);
    Anchor current = fresh(verified, request.getConnection(), request.getRegistration());
    Anchor original = anchor(initial.anchor());
    check(sameIdentity(original, current), FORBIDDEN);
    check(
        Arrays.equals(
            initial.fact().getRegistration().toByteArray(),
            request.getRegistration().toByteArray()),
        FORBIDDEN);
    Chain chain = lockChain(initial.chain());
    Source result = observe(source(initial.id(), true));
    return new View(result.fact(), chain.stopped(), clock.instant());
  }

  /** Original retained connection is sufficient for unilateral stop, even after expiry/logout. */
  RevokeAgentSourceResponse revoke(RevokeAgentSourceRequest request) {
    transaction();
    return store.atomic(() -> revokeWithin(request));
  }

  private RevokeAgentSourceResponse revokeWithin(RevokeAgentSourceRequest request) {
    validate(request);
    transaction();
    var op = operation("Revoke", request.getOperationId());
    if (op != null) return parse(receipt(op, request), RevokeAgentSourceResponse.parser());
    Source initial = source(binding(request.getSourceBinding()), false);
    check(initial != null, FORBIDDEN);
    Anchor anchor = anchor(initial.anchor());
    check(anchor.connection().equals(request.getConnection()), FORBIDDEN);
    ownership(anchor); // Never require the old transport or identity to be active for a stop.
    operationLock("Revoke", request.getOperationId());
    op = operation("Revoke", request.getOperationId());
    if (op != null) return parse(receipt(op, request), RevokeAgentSourceResponse.parser());
    Chain chain = lockChain(initial.chain());
    // Same-class locks in stable order, including the named old generation and the current head.
    var ids = new TreeSet<UUID>(List.of(initial.id(), chain.head()));
    var locked = new HashMap<UUID, Source>();
    for (UUID id : ids) locked.put(id, source(id, true));
    Source named = locked.get(initial.id());
    check(named.fact().getRevision() == request.getExpectedRevision(), CONFLICT);
    jdbc.update(
        "UPDATE iam.network_source_chain SET stopped=true WHERE tenant_id=? AND id=?",
        tenant(),
        chain.id());
    for (UUID id : ids) {
      Source terminal = observe(locked.get(id));
      if (terminal.fact().getStateValue() == 1) terminal = transition(terminal, 2);
      locked.put(id, terminal);
    }
    var response =
        RevokeAgentSourceResponse.newBuilder().setSource(locked.get(initial.id()).fact()).build();
    saveOperation("Revoke", request.getOperationId(), request, response, initial.id());
    return response;
  }

  private Anchor fresh(
      VerifiedSnapshot v, NetworkAuthorityConnection c, NetworkAgentSourceRegistration r) {
    check(v != null && v.transaction == transaction() && tenant().equals(v.tenant), UNAVAILABLE);
    check(c.equals(v.connection) && r.equals(v.registration), FORBIDDEN);
    check(v.deadline.getNano() == 0 && clock.instant().isBefore(v.deadline), VALIDATION_FAILED);
    var anchor = new Anchor(UuidV7.next(), null, c, v.owner, v.device, v.session);
    ownership(anchor);
    var rows =
        jdbc.query(
            "SELECT id,expires_at,data::text FROM iam.network_ingress WHERE tenant_id=? AND lookup_key=? FOR SHARE",
            (rs, i) -> {
              var data = Json.read(rs.getString(3));
              check(
                  c.getRealm().equals(data.path("realm").asString())
                      && c.getNode().equals(data.path("node").asString())
                      && v.device.toString().equals(data.path("device").asString())
                      && c.getServerId().equals(data.path("serverId").asString())
                      && c.getClientId().equals(data.path("clientId").asString())
                      && c.getClientAccount().equals(data.path("account").asString()),
                  FORBIDDEN);
              var expiry = rs.getTimestamp(2);
              check(
                  expiry != null
                      && !v.deadline.isAfter(expiry.toInstant())
                      && !v.deadline.isAfter(Instant.parse(data.path("expiresAt").asString())),
                  FORBIDDEN);
              return new Anchor(
                  anchor.id(), rs.getObject(1, UUID.class), c, v.owner, v.device, v.session);
            },
            tenant(),
            c.getIngress());
    check(rows.size() == 1, FORBIDDEN);
    return rows.getFirst();
  }

  /** Association checks only: expiry, revoked flags and policy do NOT gate a historical receipt. */
  private void ownership(Anchor a) {
    check(a != null, FORBIDDEN);
    var s = store.byId("session", a.session().toString(), true);
    var d = store.byId("device", a.device().toString(), true);
    var owner = store.byId("account", a.owner().toString(), true);
    check(
        s != null
            && d != null
            && owner != null
            && a.owner().toString().equals(s.path("accountId").asString())
            && a.owner().toString().equals(d.path("accountId").asString())
            && a.session().toString().equals(s.path("sessionId").asString())
            && a.session().toString().equals(d.path("sessionId").asString()),
        FORBIDDEN);
    var nodes =
        jdbc.query(
            "SELECT n.account_id,n.device_id FROM iam.network_node n JOIN iam.network_realm r ON r.tenant_id=n.tenant_id WHERE n.tenant_id=? AND r.tenant_id=? AND n.node=? AND r.realm=? FOR SHARE OF n,r",
            (rs, i) ->
                a.owner().equals(rs.getObject(1, UUID.class))
                    && a.device().equals(rs.getObject(2, UUID.class)),
            tenant(),
            tenant(),
            new BigDecimal(a.connection().getNode()),
            new BigDecimal(a.connection().getRealm()));
    check(nodes.size() == 1 && nodes.getFirst(), FORBIDDEN);
  }

  private boolean sameIdentity(Anchor a, Anchor b) {
    return a != null
        && b != null
        && a.owner().equals(b.owner())
        && a.device().equals(b.device())
        && a.session().equals(b.session())
        && a.connection().getTenantId().equals(b.connection().getTenantId())
        && a.connection().getRealm().equals(b.connection().getRealm())
        && a.connection().getNode().equals(b.connection().getNode());
  }

  private void retain(Anchor a) {
    jdbc.update(
        "INSERT INTO iam.network_source_anchor(id,tenant_id,ingress_id,connection,realm,node,owner_id,device_id,session_id) VALUES (?,?,?,?,?,?,?,?,?)",
        a.id(),
        tenant(),
        a.ingress(),
        a.connection().toByteArray(),
        new BigDecimal(a.connection().getRealm()),
        new BigDecimal(a.connection().getNode()),
        a.owner(),
        a.device(),
        a.session());
  }

  private Anchor anchor(UUID id) {
    var rows =
        jdbc.query(
            "SELECT id,ingress_id,connection,owner_id,device_id,session_id FROM iam.network_source_anchor WHERE tenant_id=? AND id=?",
            (rs, i) ->
                new Anchor(
                    rs.getObject(1, UUID.class),
                    rs.getObject(2, UUID.class),
                    parse(rs.getBytes(3), NetworkAuthorityConnection.parser()),
                    rs.getObject(4, UUID.class),
                    rs.getObject(5, UUID.class),
                    rs.getObject(6, UUID.class)),
            tenant(),
            id);
    check(rows.size() == 1, FORBIDDEN);
    return rows.getFirst();
  }

  private Chain chain(String registration, int scope) {
    var rows =
        jdbc.query(
            "SELECT id,registration,scope,anchor_id,head,stopped FROM iam.network_source_chain WHERE tenant_id=? AND registration_id=? AND scope=? FOR UPDATE",
            (rs, i) ->
                new Chain(
                    rs.getObject(1, UUID.class),
                    rs.getBytes(2),
                    rs.getInt(3),
                    rs.getObject(4, UUID.class),
                    rs.getObject(5, UUID.class),
                    rs.getBoolean(6)),
            tenant(),
            registration,
            scope);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private Chain lockChain(UUID id) {
    var rows =
        jdbc.query(
            "SELECT id,registration,scope,anchor_id,head,stopped FROM iam.network_source_chain WHERE tenant_id=? AND id=? FOR UPDATE",
            (rs, i) ->
                new Chain(
                    rs.getObject(1, UUID.class),
                    rs.getBytes(2),
                    rs.getInt(3),
                    rs.getObject(4, UUID.class),
                    rs.getObject(5, UUID.class),
                    rs.getBoolean(6)),
            tenant(),
            id);
    check(rows.size() == 1 && rows.getFirst().head() != null, FORBIDDEN);
    return rows.getFirst();
  }

  private Source source(UUID id, boolean lock) {
    var rows =
        jdbc.query(
            "SELECT id,chain_id,anchor_id,fact,state,revision FROM iam.network_source WHERE tenant_id=? AND id=?"
                + (lock ? " FOR UPDATE" : ""),
            (rs, i) ->
                new Source(
                    rs.getObject(1, UUID.class),
                    rs.getObject(2, UUID.class),
                    rs.getObject(3, UUID.class),
                    parse(rs.getBytes(4), NetworkAgentSource.parser()).toBuilder()
                        .setStateValue(rs.getInt(5))
                        .setRevision(Long.parseUnsignedLong(rs.getString(6)))
                        .build()),
            tenant(),
            id);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private Source observe(Source source) {
    check(source != null, FORBIDDEN);
    var fact = source.fact();
    check(
        fact.getStateValue() >= 1 && fact.getStateValue() <= 3 && fact.getRevision() != 0,
        UNAVAILABLE);
    if (fact.getStateValue() == 1 && !clock.instant().isBefore(Instant.parse(fact.getExpiresAt())))
      return transition(source, 3);
    return source;
  }

  private Source transition(Source source, int state) {
    check(source.fact().getRevision() != -1L, UNAVAILABLE);
    long revision = source.fact().getRevision() + 1;
    int changed =
        jdbc.update(
            "UPDATE iam.network_source SET state=?,revision=? WHERE tenant_id=? AND id=? AND state=1 AND revision=?",
            state,
            new BigDecimal(Long.toUnsignedString(revision)),
            tenant(),
            source.id(),
            new BigDecimal(Long.toUnsignedString(source.fact().getRevision())));
    check(changed == 1, CONFLICT);
    return new Source(
        source.id(),
        source.chain(),
        source.anchor(),
        source.fact().toBuilder().setStateValue(state).setRevision(revision).build());
  }

  private Operation operation(String method, String id) {
    var rows =
        jdbc.query(
            "SELECT request_digest,request,response,source_id FROM iam.network_source_operation WHERE tenant_id=? AND method=? AND operation_id=?",
            (rs, i) ->
                new Operation(
                    rs.getBytes(1), rs.getBytes(2), rs.getBytes(3), rs.getObject(4, UUID.class)),
            tenant(),
            method,
            id);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private byte[] receipt(Operation op, Message request) {
    check(
        Arrays.equals(op.digest(), digest(request.toByteArray()))
            && Arrays.equals(op.request(), request.toByteArray()),
        CONFLICT);
    Source source = source(op.source(), false);
    check(source != null, FORBIDDEN);
    Anchor anchor = anchor(source.anchor());
    check(
        anchor.owner().toString().equals(source.fact().getOwnerUserId())
            && anchor.connection().equals(source.fact().getOriginalConnection()),
        FORBIDDEN);
    ownership(anchor);
    lockChain(source.chain());
    check(source(source.id(), true) != null, FORBIDDEN);
    return op.response(); // No current-state/expiry/CAS test and no expiration mutation here.
  }

  private void operationLock(String method, String op) {
    store.lock("network-source-operation:" + method + ":" + op);
  }

  private void saveOperation(
      String method, String op, Message request, Message response, UUID source) {
    jdbc.update(
        "INSERT INTO iam.network_source_operation(id,tenant_id,method,operation_id,request_digest,request,response,source_id) VALUES (?,?,?,?,?,?,?,?)",
        UuidV7.next(),
        tenant(),
        method,
        op,
        digest(request.toByteArray()),
        request.toByteArray(),
        response.toByteArray(),
        source);
  }

  private Object transaction() {
    TenantTransactionManager.verifyTransactionTenant();
    check(TransactionSynchronizationManager.isActualTransactionActive(), UNAVAILABLE);
    Object resource =
        TransactionSynchronizationManager.getResource(Objects.requireNonNull(jdbc.getDataSource()));
    check(resource != null, UNAVAILABLE);
    jdbc.execute("SET LOCAL lock_timeout='5s'");
    tenant();
    return resource;
  }

  private UUID tenant() {
    UUID tenant = TenantContext.require();
    TenantTransactionManager.verifyTransactionTenant();
    check(!TenantContext.PLATFORM_TENANT.equals(tenant), FORBIDDEN);
    return tenant;
  }

  static void validate(RegisterAgentSourceRequest r) {
    check(r != null, VALIDATION_FAILED);
    connection(r.getConnection());
    registration(r.getRegistration());
    opaque(r.getOperationId());
    unknown(r);
    check(r.getScopeValue() == 1, VALIDATION_FAILED);
    check(
        r.getConnection().getRealm().equals(r.getRegistration().getRealm())
            && r.getConnection().getNode().equals(r.getRegistration().getNode()),
        VALIDATION_FAILED);
    check(r.getPredecessorBinding().isEmpty() == (r.getExpectedRevision() == 0), VALIDATION_FAILED);
    if (!r.getPredecessorBinding().isEmpty()) opaque(r.getPredecessorBinding());
  }

  static void validate(RevokeAgentSourceRequest r) {
    check(r != null, VALIDATION_FAILED);
    binding(r.getSourceBinding());
    connection(r.getConnection());
    opaque(r.getOperationId());
    unknown(r);
    check(r.getExpectedRevision() != 0, VALIDATION_FAILED);
  }

  static void validate(GetAgentSourceRequest r) {
    check(r != null, VALIDATION_FAILED);
    binding(r.getSourceBinding());
    connection(r.getConnection());
    registration(r.getRegistration());
    unknown(r);
  }

  private static void connection(NetworkAuthorityConnection c) {
    unknown(c);
    check(
        c.getTenantId().equals(TenantContext.require().toString())
            && !TenantContext.PLATFORM_TENANT.toString().equals(c.getTenantId()),
        FORBIDDEN);
    check(
        IamNetworkAuthority.validNumber(c.getRealm(), false)
            && IamNetworkAuthority.validNumber(c.getNode(), false)
            && c.getIngress().matches("[a-z2-7]{26}")
            && c.getServerId().matches("N[A-Z2-7]{55}")
            && IamNetworkAuthority.validNumber(c.getClientId(), false)
            && !c.getClientAccount().isBlank()
            && c.getClientAccount().length() <= 256,
        VALIDATION_FAILED);
  }

  private static void registration(NetworkAgentSourceRegistration r) {
    unknown(r);
    opaque(r.getRegistrationId());
    opaque(r.getAllocationOpId());
    check(
        r.getAllocationFingerprint().matches("sha256:[0-9a-f]{64}")
            && r.getRegistrationRevision() != 0
            && IamNetworkAuthority.validNumber(r.getRealm(), false)
            && IamNetworkAuthority.validNumber(r.getNode(), false)
            && IamNetworkAuthority.validNumber(r.getLocal(), false)
            && r.getAddressType().equals("session"),
        VALIDATION_FAILED);
  }

  private static UUID binding(String id) {
    opaque(id);
    // Valid unknown opaque bindings must fail as unknown, not become guessed UUIDs.
    try {
      UUID parsed = UUID.fromString(id);
      check(parsed.toString().equals(id), FORBIDDEN);
      return parsed;
    } catch (IllegalArgumentException e) {
      throw new AurigaException(FORBIDDEN);
    }
  }

  private static void opaque(String value) {
    check(value != null && value.matches("[A-Za-z0-9][A-Za-z0-9._:-]{0,127}"), VALIDATION_FAILED);
  }

  private static void unknown(Message value) {
    check(value.getUnknownFields().asMap().isEmpty(), VALIDATION_FAILED);
  }

  private static void check(boolean condition, ErrorCode error) {
    if (!condition) throw new AurigaException(error);
  }

  private static byte[] digest(byte[] bytes) {
    try {
      return MessageDigest.getInstance("SHA-256").digest(bytes);
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  private static <T> T parse(byte[] bytes, Parser<T> parser) {
    try {
      return parser.parseFrom(bytes);
    } catch (InvalidProtocolBufferException e) {
      throw new AurigaException(UNAVAILABLE);
    }
  }
}
