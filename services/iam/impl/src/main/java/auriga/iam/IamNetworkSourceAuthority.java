package auriga.iam;

import static auriga.common.error.CommonErrors.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import com.google.protobuf.InvalidProtocolBufferException;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.time.*;
import java.time.temporal.ChronoUnit;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;

/** Three restricted source operations. No delivery authority, native mapping or cached allow. */
final class IamNetworkSourceAuthority {
  record Policy(long sourceSeconds, long decisionSeconds) {
    static Policy from(Environment env) {
      long source = seconds(env, "source-max-seconds"),
          decision = seconds(env, "source-decision-seconds");
      if (decision > 300 || source > 0 && decision > source)
        throw new IllegalStateException("Invalid IAM network source lifetime policy");
      return new Policy(source, decision);
    }

    private static long seconds(Environment env, String key) {
      String value = env.getProperty("auriga.iam.network." + key);
      if (value == null) return 0;
      try {
        if (!value.matches("[0-9]+")) throw new NumberFormatException();
        return Long.parseLong(value);
      } catch (NumberFormatException invalid) {
        throw new IllegalStateException("Invalid IAM network source lifetime policy");
      }
    }

    boolean enabled() {
      return sourceSeconds > 0 && decisionSeconds > 0;
    }
  }

  private final IamStore store;
  private final JdbcTemplate jdbc;
  private final IamNetworkAuthority network;
  private final DeviceAccessAuthority devices;
  private final InstanceAiAuthorization org;
  private final IamNetworkSources kernel;
  private final IamNetworkPolicyRevision revisions;
  private final Clock clock;
  private final Policy policy;

  IamNetworkSourceAuthority(
      IamStore store,
      IamNetworkAuthority network,
      DeviceAccessAuthority devices,
      InstanceAiAuthorization org,
      Clock clock,
      Policy policy) {
    this.store = store;
    this.jdbc = store.networkJdbc();
    this.network = network;
    this.devices = devices;
    this.org = org;
    this.clock = clock;
    this.policy = policy;
    kernel = new IamNetworkSources(store, clock);
    revisions = new IamNetworkPolicyRevision(store);
  }

  RegisterAgentSourceResponse register(RegisterAgentSourceRequest r) {
    IamNetworkSources.validate(r);
    var saved = tx(() -> kernel.registerReceipt(r));
    if (saved != null) return saved;
    Instant began = clock.instant();
    var before = online(r.getConnection());
    var credential = authorize(before);
    return tx(
        () -> {
          // An operation may have committed during the remote check. Its original receipt wins.
          var receipt = kernel.registerReceipt(r);
          if (receipt != null) return receipt;
          require(!clock.instant().isBefore(began), UNAVAILABLE);
          var current = recheck(r.getConnection(), before, credential);
          Instant deadline =
              min(plus(clock.instant(), policy.sourceSeconds()), deadline(current, credential));
          deadline = second(deadline);
          require(clock.instant().isBefore(deadline), FORBIDDEN);
          return kernel.register(
              r, verified(r.getConnection(), r.getRegistration(), current, deadline));
        });
  }

  GetAgentSourceResponse get(GetAgentSourceRequest r) {
    IamNetworkSources.validate(r);
    var terminal = tx(() -> terminal(r));
    if (terminal != null) return terminal;
    Instant began = clock.instant();
    var before = online(r.getConnection());
    var credential = authorize(before);
    return tx(
        () -> {
          // A concurrent stop/expiry is observable even when current issuance is disabled.
          var stopped = terminal(r);
          if (stopped != null) return stopped;
          require(!clock.instant().isBefore(began), UNAVAILABLE);
          var current = recheck(r.getConnection(), before, credential);
          Instant until =
              second(
                  min(
                      plus(clock.instant(), policy.decisionSeconds()),
                      deadline(current, credential)));
          require(clock.instant().isBefore(until), FORBIDDEN);
          var view =
              kernel.get(r, verified(r.getConnection(), r.getRegistration(), current, until));
          var source = view.source();
          until = second(min(until, Instant.parse(source.getExpiresAt())));
          Instant observed = second(clock.instant());
          if (source.getStateValue() == 1 && !view.stopped()) {
            require(!observed.isBefore(Instant.parse(source.getNotBefore())), UNAVAILABLE);
            require(clock.instant().isBefore(until), FORBIDDEN);
          }
          return response(
              r,
              source,
              current.revision(),
              observed,
              source.getStateValue() == 1 && !view.stopped() ? until.toString() : "");
        });
  }

  RevokeAgentSourceResponse revoke(RevokeAgentSourceRequest r) {
    IamNetworkSources.validate(r);
    return tx(() -> kernel.revoke(r));
  }

  private IamNetworkAuthority.SourceSnapshot online(NetworkAuthorityConnection c) {
    require(policy.enabled(), UNAVAILABLE);
    return tx(() -> snapshot(c));
  }

  private IamNetworkAuthority.SourceSnapshot snapshot(NetworkAuthorityConnection c) {
    var value = network.sourceSnapshot(binding(c), true);
    require(value != null, FORBIDDEN);
    require(value.config() == null || value.config().path("enabled").asBoolean(true), FORBIDDEN);
    require(value.revision() != null && value.revision().signum() > 0, UNAVAILABLE);
    return value;
  }

  private DeviceAccessAuthority.CredentialSnapshot authorize(
      IamNetworkAuthority.SourceSnapshot before) {
    store.requireTransactionReleased();
    var node = before.target().node();
    var credential =
        devices.credentialSnapshot(
            TenantContext.require().toString(), node.account(), node.device());
    require(credential != null, FORBIDDEN);
    require(
        credential.session().equals(before.target().session().path("sessionId").asString()),
        FORBIDDEN);
    store.requireTransactionReleased();
    GetEffectivePermissionsResponse effective;
    try {
      require(!io.grpc.Context.current().isCancelled(), UNAVAILABLE);
      effective =
          org.getEffectivePermissions(
              GetEffectivePermissionsRequest.newBuilder()
                  .setTenantId(TenantContext.require().toString())
                  .setAccountId(node.account())
                  .build());
      require(!io.grpc.Context.current().isCancelled(), UNAVAILABLE);
    } catch (AurigaException error) {
      if (Set.of("common.not_found", "common.forbidden").contains(error.errorCode().code()))
        throw new AurigaException(FORBIDDEN);
      throw new AurigaException(UNAVAILABLE);
    } catch (RuntimeException error) {
      throw new AurigaException(UNAVAILABLE);
    }
    require(
        effective != null
            && effective.getStatusValue() > 0
            && effective.getStatusValue() <= 3
            && effective.getMemberTypeValue() > 0
            && effective.getMemberTypeValue() <= 2,
        UNAVAILABLE);
    require(
        effective.getStatus() == MemberStatus.MEMBER_STATUS_ACTIVE
            && (effective.getMemberType() == MemberType.MEMBER_TYPE_MEMBER
                || effective.getMemberType() == MemberType.MEMBER_TYPE_EXTERNAL)
            && effective.getPermissionsList().contains("auriga.connect.collab.use"),
        FORBIDDEN);
    return credential;
  }

  private IamNetworkAuthority.SourceSnapshot recheck(
      NetworkAuthorityConnection c,
      IamNetworkAuthority.SourceSnapshot before,
      DeviceAccessAuthority.CredentialSnapshot credential) {
    var current = snapshot(c);
    require(before.equals(current), FORBIDDEN);
    var now =
        devices.credentialSnapshot(
            TenantContext.require().toString(),
            current.target().node().account(),
            current.target().node().device());
    require(credential.equals(now), FORBIDDEN);
    return current;
  }

  private Instant deadline(
      IamNetworkAuthority.SourceSnapshot s, DeviceAccessAuthority.CredentialSnapshot c) {
    Instant result =
        min(
            c.identity().expiresAt(),
            min(
                s.ingressExpiry(),
                Instant.parse(s.target().ingress().path("expiresAt").asString())));
    for (Instant expiry : s.identityExpiries()) result = min(result, expiry);
    return result;
  }

  private IamNetworkSources.VerifiedSnapshot verified(
      NetworkAuthorityConnection c,
      NetworkAgentSourceRegistration r,
      IamNetworkAuthority.SourceSnapshot s,
      Instant deadline) {
    return kernel.verified(
        c,
        r,
        UUID.fromString(s.target().node().account()),
        UUID.fromString(s.target().node().device()),
        UUID.fromString(s.target().session().path("sessionId").asString()),
        deadline);
  }

  private record History(UUID chain, UUID anchor, NetworkAgentSource source) {}

  /** Only retained associations qualify an offline terminal read; no request-created anchors. */
  private GetAgentSourceResponse terminal(GetAgentSourceRequest r) {
    var initial = history(r.getSourceBinding(), false);
    require(initial != null, FORBIDDEN);
    var fact = initial.source();
    if (fact.getStateValue() == 1 && clock.instant().isBefore(Instant.parse(fact.getExpiresAt())))
      return null;
    require(fact.getRegistration().equals(r.getRegistration()), FORBIDDEN);
    retained(initial, r.getConnection());
    BigInteger revision = revisions.current();
    var expectedRegistration = fact.getRegistration();
    int expectedScope = fact.getScopeValue();
    var chains =
        jdbc.query(
            "SELECT registration,scope,stopped FROM iam.network_source_chain WHERE tenant_id=? AND id=? FOR UPDATE",
            (rs, i) ->
                Arrays.equals(rs.getBytes(1), expectedRegistration.toByteArray())
                    && rs.getInt(2) == expectedScope,
            TenantContext.require(),
            initial.chain());
    require(chains.size() == 1 && chains.getFirst(), FORBIDDEN);
    var locked = history(r.getSourceBinding(), true);
    require(locked != null && initial.equals(locked), FORBIDDEN);
    if (fact.getStateValue() == 1) {
      require(fact.getRevision() != -1L, UNAVAILABLE);
      int changed =
          jdbc.update(
              "UPDATE iam.network_source SET state=3,revision=revision+1 WHERE tenant_id=? AND id=? AND state=1 AND revision=?",
              TenantContext.require(),
              UUID.fromString(fact.getSourceBinding()),
              new BigDecimal(Long.toUnsignedString(fact.getRevision())));
      require(changed == 1, CONFLICT);
      fact = fact.toBuilder().setStateValue(3).setRevision(fact.getRevision() + 1).build();
    }
    require(fact.getStateValue() == 2 || fact.getStateValue() == 3, UNAVAILABLE);
    return response(r, fact, revision, second(clock.instant()), "");
  }

  private History history(String id, boolean lock) {
    var rows =
        jdbc.query(
            "SELECT chain_id,anchor_id,fact,state,revision FROM iam.network_source WHERE tenant_id=? AND id=?"
                + (lock ? " FOR UPDATE" : ""),
            (rs, i) -> {
              try {
                return new History(
                    rs.getObject(1, UUID.class),
                    rs.getObject(2, UUID.class),
                    NetworkAgentSource.parseFrom(rs.getBytes(3)).toBuilder()
                        .setStateValue(rs.getInt(4))
                        .setRevision(Long.parseUnsignedLong(rs.getString(5)))
                        .build());
              } catch (InvalidProtocolBufferException invalid) {
                throw new AurigaException(UNAVAILABLE);
              }
            },
            TenantContext.require(),
            UUID.fromString(id));
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private void retained(History h, NetworkAuthorityConnection c) {
    var rows =
        jdbc.query(
            "SELECT connection,owner_id,device_id,session_id,ingress_id,realm::text,node::text"
                + " FROM iam.network_source_anchor WHERE tenant_id=? AND id=?",
            (rs, i) -> {
              NetworkAuthorityConnection original;
              try {
                original = NetworkAuthorityConnection.parseFrom(rs.getBytes(1));
              } catch (InvalidProtocolBufferException invalid) {
                throw new AurigaException(UNAVAILABLE);
              }
              String owner = rs.getString(2), device = rs.getString(3), session = rs.getString(4);
              require(
                  original.equals(h.source().getOriginalConnection())
                      && owner.equals(h.source().getOwnerUserId())
                      && rs.getObject(5) != null
                      && original.getRealm().equals(rs.getString(6))
                      && original.getNode().equals(rs.getString(7))
                      && original.getTenantId().equals(TenantContext.require().toString()),
                  FORBIDDEN);
              var s = store.byId("session", session, true);
              var d = store.byId("device", device, true);
              var a = store.byId("account", owner, true);
              require(
                  s != null
                      && d != null
                      && a != null
                      && session.equals(s.path("sessionId").asString())
                      && session.equals(d.path("sessionId").asString())
                      && owner.equals(s.path("accountId").asString())
                      && owner.equals(d.path("accountId").asString()),
                  FORBIDDEN);
              var nodes =
                  jdbc.query(
                      "SELECT n.account_id::text,n.device_id::text FROM iam.network_node n JOIN iam.network_realm r USING(tenant_id)"
                          + " WHERE n.tenant_id=? AND n.node=? AND r.realm=? FOR SHARE OF n,r",
                      (row, j) -> owner.equals(row.getString(1)) && device.equals(row.getString(2)),
                      TenantContext.require(),
                      new BigDecimal(original.getNode()),
                      new BigDecimal(original.getRealm()));
              require(nodes.size() == 1 && nodes.getFirst(), FORBIDDEN);
              if (!original.equals(c)) {
                require(
                    c.getRealm().equals(original.getRealm())
                        && c.getNode().equals(original.getNode()),
                    FORBIDDEN);
                var current = network.sourceSnapshot(binding(c), true);
                require(
                    current != null
                        && current.target().node().account().equals(owner)
                        && current.target().node().device().equals(device)
                        && current.target().session().path("sessionId").asString().equals(session)
                        && c.getRealm().equals(original.getRealm())
                        && c.getNode().equals(original.getNode()),
                    FORBIDDEN);
              }
              return true;
            },
            TenantContext.require(),
            h.anchor());
    require(rows.size() == 1 && rows.getFirst(), FORBIDDEN);
  }

  private static GetAgentSourceResponse response(
      GetAgentSourceRequest r,
      NetworkAgentSource source,
      BigInteger revision,
      Instant observed,
      String until) {
    require(revision != null && revision.signum() > 0 && revision.bitLength() <= 64, UNAVAILABLE);
    return GetAgentSourceResponse.newBuilder()
        .setSource(source)
        .setConnection(r.getConnection())
        .setPolicyRevision(revision.longValue())
        .setObservedAt(observed.toString())
        .setValidUntil(until)
        .build();
  }

  private <T> T tx(Supplier<T> work) {
    store.requireTransactionReleased();
    return store.tx(
        () -> {
          jdbc.execute("SET LOCAL lock_timeout='5s'");
          store.lock("network-authority");
          return work.get();
        });
  }

  private static IamNetworkAuthority.TargetBinding binding(NetworkAuthorityConnection c) {
    return new IamNetworkAuthority.TargetBinding(
        c.getRealm(),
        c.getNode(),
        c.getIngress(),
        c.getServerId(),
        c.getClientId(),
        c.getClientAccount());
  }

  private static Instant plus(Instant now, long seconds) {
    try {
      return now.plusSeconds(seconds);
    } catch (DateTimeException | ArithmeticException overflow) {
      return Instant.MAX;
    }
  }

  private static Instant second(Instant value) {
    return value.truncatedTo(ChronoUnit.SECONDS);
  }

  private static Instant min(Instant a, Instant b) {
    return a.isBefore(b) ? a : b;
  }

  private static void require(boolean condition, ErrorCode error) {
    if (!condition) throw new AurigaException(error);
  }
}
