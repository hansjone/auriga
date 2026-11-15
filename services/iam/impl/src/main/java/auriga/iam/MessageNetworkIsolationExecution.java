package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.contracts.msgnet.model.IsolationRecord;
import auriga.iam.api.*;
import java.math.BigDecimal;
import java.util.*;

/** Every network grant rechecks this fence under the same network-authority lock. */
final class MessageNetworkIsolationExecution implements MessageNetworkIsolation {
  private final IamStore store;
  private final MessageNetworkTargets targets;
  private final NodeControlAuthority nodes;
  private final MessageNetworkControlLedger ledger;
  private final MessageNetworkDisconnector disconnector;
  private final java.time.Clock clock;

  MessageNetworkIsolationExecution(
      IamStore store,
      MessageNetworkTargets targets,
      NodeControlAuthority nodes,
      MessageNetworkControlLedger ledger,
      MessageNetworkDisconnector disconnector,
      java.time.Clock clock) {
    this.store = store;
    this.targets = targets;
    this.nodes = nodes;
    this.ledger = ledger;
    this.disconnector = disconnector;
    this.clock = clock;
  }

  @Override
  public IsolationRecord ready(UUID tenant, UUID node, IsolationRecord request) {
    return TenantContext.call(
        tenant,
        () -> {
          store.requireTransactionReleased();
          var binding = nodes.binding(tenant, node);
          var current =
              ledger.isolation(binding.tenant(), binding.realm(), binding.pin().getTargetId());
          if (current == null
              || Boolean.TRUE.equals(current.getIdentityCompromised())
              || current.getState() == IsolationRecord.StateEnum.QUARANTINED)
            throw new IllegalStateException("independent installation receipt required");
          var expected =
              Json.MAPPER
                  .treeToValue(Json.MAPPER.valueToTree(current), IsolationRecord.class)
                  .state(IsolationRecord.StateEnum.READY)
                  .newChainReady(true);
          if (!expected.equals(request))
            throw new IllegalStateException("node readiness scope mismatch");
          var bundle = ledger.transport();
          if (!binding.tenant().equals(bundle.getPayload().getTenantId())
              || !binding.realm().equals(bundle.getPayload().getRealm()))
            throw new IllegalStateException("transport readiness scope mismatch");
          byte[] challenge = new byte[32];
          new java.security.SecureRandom().nextBytes(challenge);
          String digest =
              "sha256:" + Secrets.hash(InstallationJson.canonical(Json.MAPPER.valueToTree(bundle)));
          var query =
              new auriga.contracts.msgnet.model.TransportConfirmationQuery()
                  .tenantId(binding.tenant())
                  .realm(binding.realm())
                  .bundleVersion(bundle.getPayload().getBundleVersion())
                  .bundleDigest(digest)
                  .challenge(Base64.getUrlEncoder().withoutPadding().encodeToString(challenge));
          var evidence = ledger.confirmation(query).getPayload();
          var issued = java.time.Instant.parse(evidence.getIssuedAt());
          if (!evidence.getTenantId().equals(query.getTenantId())
              || !evidence.getRealm().equals(query.getRealm())
              || !evidence.getBundleVersion().equals(query.getBundleVersion())
              || !evidence.getBundleDigest().equals(query.getBundleDigest())
              || !evidence.getChallenge().equals(query.getChallenge())
              || !evidence.getControlRevision().equals(current.getMinimumControlRevision())
              || !evidence.getAuthorityId().equals(bundle.getPayload().getAuthorityId())
              || !evidence.getAuthorityDigest().equals(bundle.getPayload().getAuthorityDigest())
              || !evidence.getPhase().getValue().equals(bundle.getPayload().getPhase().getValue())
              || !evidence
                  .getRequiredReaderIds()
                  .equals(new HashSet<>(bundle.getPayload().getRequiredReaderIds()))
              || issued.isAfter(clock.instant())
              || issued.isBefore(clock.instant().minusSeconds(60)))
            throw new IllegalStateException("current global transport evidence required");
          var readers = new HashSet<String>();
          for (var row : evidence.getReaders()) {
            if (!readers.add(row.getReaderId()))
              throw new IllegalStateException("duplicate transport reader");
            if (row.getPhase()
                == auriga.contracts.msgnet.model.TransportReaderEvidence.PhaseEnum.ISOLATED) {
              if (!Boolean.TRUE.equals(row.getIsolationEnforced()))
                throw new IllegalStateException("reader isolation unconfirmed");
              continue;
            }
            var selector = row.getSelector();
            if (selector == null
                || !selector.getTenantId().equals(binding.tenant())
                || !selector.getRealm().equals(binding.realm())
                || !selector.getInstallationId().equals(row.getIdentityId())
                || !selector.getBundleDigest().equals(digest)
                || !selector.getBundleVersion().equals(bundle.getPayload().getBundleVersion())
                || (bundle.getPayload().getPhase()
                        != auriga.contracts.msgnet.model.TransportTrustBundle.PhaseEnum.DUAL_TRUST
                    && row.getPhase()
                        != auriga.contracts.msgnet.model.TransportReaderEvidence.PhaseEnum
                            .HANDSHAKE_VERIFIED))
              throw new IllegalStateException("reader new-chain readiness unconfirmed");
          }
          if (readers.isEmpty()
              || !readers.equals(new HashSet<>(bundle.getPayload().getRequiredReaderIds()))
              || readers.size() != bundle.getPayload().getRequiredReaderIds().size())
            throw new IllegalStateException("incomplete transport readiness");
          // C rechecks the root, independent receipt and compromise fence atomically.
          return ledger.enforceIsolation(expected);
        });
  }

  @Override
  public void beforeControl(UUID tenant, UUID node) {
    TenantContext.run(
        tenant,
        () -> {
          store.requireTransactionReleased();
          var binding = nodes.binding(tenant, node);
          var devices = targets.forHost(tenant, binding.realm(), node);
          reconcile(tenant, binding.realm(), binding.pin().getTargetId(), devices);
        });
  }

  void reconcileDevice(MessageNetworkTargets.Binding binding) {
    TenantContext.run(
        binding.tenant(),
        () -> {
          reconcile(binding.tenant(), binding.realm(), binding.target(), List.of(binding));
        });
  }

  private void reconcile(
      UUID tenant, String realm, String target, List<MessageNetworkTargets.Binding> devices) {
    store.requireTransactionReleased();
    var current = ledger.isolation(tenant.toString(), realm, target);
    if (current == null) throw new IllegalStateException("message isolation target unavailable");
    boolean ready =
        current.getState() == IsolationRecord.StateEnum.READY
            && Boolean.FALSE.equals(current.getIdentityCompromised())
            && Boolean.TRUE.equals(current.getNewChainReady());
    if (ready) {
      store.tx(
          () -> {
            store.lock("network-authority");
            for (var device : devices) {
              store
                  .networkJdbc()
                  .update(
                      "DELETE FROM iam.network_message_fence WHERE tenant_id=? AND device_id=? AND target_id=? AND (control_revision<? OR (control_revision=? AND control_digest=?))",
                      tenant,
                      device.device(),
                      target,
                      new BigDecimal(current.getMinimumControlRevision()),
                      new BigDecimal(current.getMinimumControlRevision()),
                      current.getControlDigest());
            }
            return null;
          });
      return;
    }
    store.tx(
        () -> {
          store.lock("network-authority");
          for (var device : devices) {
            store
                .networkJdbc()
                .update(
                    "INSERT INTO iam.network_message_fence(tenant_id,device_id,target_id,control_revision,control_digest) VALUES (?,?,?,?,?) ON CONFLICT(tenant_id,device_id,target_id) DO UPDATE SET control_revision=EXCLUDED.control_revision,control_digest=EXCLUDED.control_digest WHERE iam.network_message_fence.control_revision<EXCLUDED.control_revision OR (iam.network_message_fence.control_revision=EXCLUDED.control_revision AND iam.network_message_fence.control_digest=EXCLUDED.control_digest)",
                    tenant,
                    device.device(),
                    target,
                    new BigDecimal(current.getMinimumControlRevision()),
                    current.getControlDigest());
            store
                .networkJdbc()
                .update(
                    "DELETE FROM iam.network_ticket WHERE tenant_id=? AND data->>'device'=?",
                    tenant,
                    device.device().toString());
          }
          return null;
        });
    var confirmed =
        Json.MAPPER
            .treeToValue(Json.MAPPER.valueToTree(current), IsolationRecord.class)
            .state(IsolationRecord.StateEnum.QUARANTINED)
            .newChainReady(false);
    // No IAM transaction or device lock crosses the actual broker request or C's commit.
    disconnector.disconnect(confirmed);
    if (current.getState() != IsolationRecord.StateEnum.CONFIRMED)
      ledger.enforceIsolation(confirmed);
  }
}
