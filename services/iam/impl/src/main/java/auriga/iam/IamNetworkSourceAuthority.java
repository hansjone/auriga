package auriga.iam;

import static auriga.common.error.CommonErrors.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import auriga.iam.api.NetworkAuthority;
import java.math.BigInteger;
import java.time.*;
import java.time.temporal.ChronoUnit;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;

/**
 * Online source and delivery authority. Native mapping and recipient relationships stay in collab.
 */
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

  RegisterAgentSourceResponse readReceipt(ReadAgentSourceReceiptRequest r) {
    require(
        r != null && r.hasOriginalRequest() && r.getUnknownFields().asMap().isEmpty(),
        VALIDATION_FAILED);
    return tx(
        () -> {
          // This path must never fall back to register: a miss is only an unknown result.
          var receipt = kernel.registerReceipt(r.getOriginalRequest());
          require(receipt != null, UNAVAILABLE);
          require(receipt.hasSource() != receipt.getDefinitivelyUncommitted(), UNAVAILABLE);
          return receipt;
        });
  }

  RegisterAgentSourceResponse fenceReceipt(FenceAgentSourceReceiptRequest r) {
    require(
        r != null && r.hasOriginalRequest() && r.getUnknownFields().asMap().isEmpty(),
        VALIDATION_FAILED);
    IamNetworkSources.validate(r.getOriginalRequest());
    return tx(() -> kernel.fence(r.getOriginalRequest()));
  }

  GetAgentSourceResponse get(GetAgentSourceRequest r) {
    IamNetworkSources.validate(r);
    Instant began = clock.instant();
    // Every Get, including terminal facts and disabled issuance, proves current live authority.
    var before = tx(() -> snapshot(r.getConnection()));
    var credential = authorize(before);
    return tx(
        () -> {
          require(!clock.instant().isBefore(began), UNAVAILABLE);
          var current = recheck(r.getConnection(), before, credential);
          // This attests only the real live identity/connection to the kernel, never a TTL lease.
          Instant liveUntil = second(deadline(current, credential));
          require(clock.instant().isBefore(liveUntil), FORBIDDEN);
          var view =
              kernel.get(r, verified(r.getConnection(), r.getRegistration(), current, liveUntil));
          var source = view.source();
          Instant observed = second(clock.instant());
          String validUntil = "";
          if (source.getStateValue() == 1 && !view.stopped()) {
            require(policy.enabled(), UNAVAILABLE);
            Instant until =
                second(
                    min(
                        plus(clock.instant(), policy.decisionSeconds()),
                        min(liveUntil, Instant.parse(source.getExpiresAt()))));
            require(!observed.isBefore(Instant.parse(source.getNotBefore())), UNAVAILABLE);
            require(clock.instant().isBefore(until), FORBIDDEN);
            validUntil = until.toString();
          }
          return response(r, source, current.revision(), observed, validUntil, view.stopped());
        });
  }

  RevokeAgentSourceResponse revoke(RevokeAgentSourceRequest r) {
    IamNetworkSources.validate(r);
    return tx(() -> kernel.revoke(r));
  }

  private record DeliverySnapshot(
      IamNetworkSources.DeliveryEvidence evidence,
      IamNetworkAuthority.SourceSnapshot source,
      IamNetworkAuthority.SourceSnapshot target) {}

  private DeliverySnapshot deliverySnapshot(String id, NetworkAuthorityConnection target) {
    var evidence = kernel.deliveryEvidence(id);
    var pair = network.deliverySnapshots(binding(evidence.connection()), binding(target));
    require(pair != null, FORBIDDEN);
    var source = checkedSnapshot(pair.source());
    var destination = checkedSnapshot(pair.target());
    require(source.revision().equals(destination.revision()), FORBIDDEN);
    return new DeliverySnapshot(evidence, source, destination);
  }

  AuthorizeNetworkDeliveryResponse authorizeDelivery(AuthorizeNetworkDeliveryRequest r) {
    IamNetworkSources.connection(r.getConnection());
    require(r.getUnknownFields().asMap().isEmpty(), VALIDATION_FAILED);
    var message = IamNetworkDeliveryJson.parse(r.getMessageJson().toByteArray());
    var context = IamNetworkDeliveryJson.parse(r.getSourceContextJson().toByteArray());
    IamNetworkDeliveryJson.validate(message, context);
    require(policy.enabled(), UNAVAILABLE);
    var reference = message.path("authorization_ref");
    String id = reference.path("source_binding").asString();
    Instant began = clock.instant();
    var before = tx(() -> deliverySnapshot(id, r.getConnection()));
    var sourceCredential = authorize(before.source());
    var targetCredential = authorize(before.target());
    var response =
        tx(
            () -> {
              require(!clock.instant().isBefore(began), UNAVAILABLE);
              var current = deliverySnapshot(id, r.getConnection());
              require(before.equals(current), FORBIDDEN);
              var source =
                  recheck(current.evidence().connection(), before.source(), sourceCredential);
              var target = recheck(r.getConnection(), before.target(), targetCredential);
              require(
                  reference.path("policy_revision").asString().equals(source.revision().toString()),
                  FORBIDDEN);
              var to = message.path("to");
              require(
                  r.getConnection().getRealm().equals(to.path("realm").asString())
                      && r.getConnection().getNode().equals(to.path("node").asString()),
                  FORBIDDEN);
              var limits = network.deliveryLimits();
              int bodyBytes =
                  IamNetworkDeliveryJson.bytes(
                      IamNetworkDeliveryJson.canonical(message.path("body")));
              require(
                  r.getMessageJson().size() <= limits.get(NetworkAuthority.Limit.PAYLOAD_BYTES)
                      && r.getSourceContextJson().size()
                          <= limits.get(NetworkAuthority.Limit.PAYLOAD_BYTES)
                      && bodyBytes <= limits.get(NetworkAuthority.Limit.BODY_BYTES),
                  FORBIDDEN);
              var from = message.path("from");
              for (var operation :
                  List.of(NetworkAuthority.Operation.SEND, NetworkAuthority.Operation.RECEIVE)) {
                require(
                    network.permitsDelivery(
                        new NetworkAuthority.Check(
                            from.path("realm").asString(),
                            from.path("node").asString(),
                            to.path("node").asString(),
                            from.path("local").asString(),
                            NetworkAuthority.Sender.AGENT,
                            operation,
                            bodyBytes,
                            1,
                            1)),
                    FORBIDDEN);
              }
              // Get's kernel checks original identity/registration and locks the NAMED generation
              // and chain.
              var evidence = current.evidence();
              var fact = evidence.source();
              Instant sourceUntil = second(deadline(source, sourceCredential));
              var view =
                  kernel.get(
                      GetAgentSourceRequest.newBuilder()
                          .setSourceBinding(id)
                          .setConnection(evidence.connection())
                          .setRegistration(fact.getRegistration())
                          .build(),
                      verified(evidence.connection(), fact.getRegistration(), source, sourceUntil));
              var live = view.source();
              var registration = live.getRegistration();
              // Commit a newly observed EXPIRED tombstone even though the outer RPC will deny.
              if (view.stopped() || live.getStateValue() != 1)
                return AuthorizeNetworkDeliveryResponse.getDefaultInstance();
              require(
                  live.getScopeValue() == 1
                      && reference
                          .path("source_revision")
                          .asString()
                          .equals(Long.toUnsignedString(live.getRevision()))
                      && registration.getRealm().equals(from.path("realm").asString())
                      && registration.getNode().equals(from.path("node").asString())
                      && registration.getLocal().equals(from.path("local").asString())
                      && registration.getAddressType().equals(from.path("type").asString())
                      && live.getOwnerUserId().equals(context.path("user").asString()),
                  FORBIDDEN);
              Instant now = clock.instant(), notBefore = Instant.parse(live.getNotBefore());
              Instant observed = Instant.parse(context.path("observed_at").asString());
              Instant created = Instant.parse(message.path("created_at").asString());
              require(
                  !now.isBefore(notBefore)
                      && !observed.isBefore(notBefore)
                      && !observed.isAfter(now)
                      && !created.isAfter(now),
                  FORBIDDEN);
              Instant until =
                  second(
                      min(
                          plus(now, Math.min(300, policy.decisionSeconds())),
                          min(
                              Instant.parse(live.getExpiresAt()),
                              min(
                                  sourceUntil,
                                  min(
                                      deadline(target, targetCredential),
                                      Instant.parse(message.path("expires_at").asString()))))));
              require(now.isBefore(until), FORBIDDEN);
              return AuthorizeNetworkDeliveryResponse.newBuilder()
                  .setAllowed(true)
                  .setConnection(r.getConnection())
                  .setValidUntil(until.toString())
                  .build();
            });
    require(response.getAllowed(), FORBIDDEN);
    require(
        !clock.instant().isBefore(began)
            && clock.instant().isBefore(Instant.parse(response.getValidUntil())),
        FORBIDDEN);
    return response;
  }

  private IamNetworkAuthority.SourceSnapshot online(NetworkAuthorityConnection c) {
    require(policy.enabled(), UNAVAILABLE);
    return tx(() -> snapshot(c));
  }

  private IamNetworkAuthority.SourceSnapshot snapshot(NetworkAuthorityConnection c) {
    return checkedSnapshot(network.sourceSnapshot(binding(c), true));
  }

  private IamNetworkAuthority.SourceSnapshot checkedSnapshot(
      IamNetworkAuthority.SourceSnapshot value) {
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

  private static GetAgentSourceResponse response(
      GetAgentSourceRequest r,
      NetworkAgentSource source,
      BigInteger revision,
      Instant observed,
      String until,
      boolean stopped) {
    require(revision != null && revision.signum() > 0 && revision.bitLength() <= 64, UNAVAILABLE);
    return GetAgentSourceResponse.newBuilder()
        .setSource(source)
        .setConnection(r.getConnection())
        .setPolicyRevision(revision.longValue())
        .setObservedAt(observed.toString())
        .setValidUntil(until)
        .setLineageStopped(stopped)
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
