package auriga.iam;

import auriga.contracts.msgnet.model.*;
import java.time.Instant;
import java.util.*;

/** Independent C snapshot validation. No value is inferred from a confirmation row. */
final class MessageNetworkSelectors {
  private MessageNetworkSelectors() {}

  static String digest(Object value) {
    return "sha256:" + Secrets.hash(InstallationJson.canonical(MessageNetworkWire.tree(value)));
  }

  private static void require(boolean valid, String code) {
    if (!valid) throw new IllegalStateException(code);
  }

  static void verify(
      ExpectedSelectors r,
      ExpectedSelectorsQuery q,
      SignedTransportTrustBundle bundle,
      String tenant,
      String realm,
      String revision,
      String controlDigest,
      Instant now) {
    var b = bundle.getPayload();
    require(
        Objects.equals(tenant, r.getTenantId())
            && Objects.equals(realm, r.getRealm())
            && Objects.equals(tenant, b.getTenantId())
            && Objects.equals(realm, b.getRealm())
            && Objects.equals(tenant, q.getTenantId())
            && Objects.equals(realm, q.getRealm()),
        "scope_denied");
    require(
        Objects.equals(r.getGeneration(), b.getGeneration())
            && Objects.equals(r.getBundleVersion(), b.getBundleVersion())
            && Objects.equals(r.getPhase().getValue(), b.getPhase().getValue())
            && Objects.equals(r.getAuthorityId(), b.getAuthorityId())
            && Objects.equals(r.getAuthorityDigest(), b.getAuthorityDigest())
            && Objects.equals(r.getBundleDigest(), digest(bundle))
            && Objects.equals(q.getBundleDigest(), r.getBundleDigest())
            && Objects.equals(q.getBundleVersion(), r.getBundleVersion()),
        "invalid_chain");
    require(
        Objects.equals(revision, r.getControlRevision())
            && Objects.equals(revision, q.getControlRevision())
            && Objects.equals(controlDigest, r.getControlDigest())
            && Objects.equals(controlDigest, q.getControlDigest()),
        "control_required");
    require(Objects.equals(r.getChallenge(), q.getChallenge()), "invalid_chain");
    var issued = Instant.parse(r.getIssuedAt());
    require(!issued.isAfter(now) && !issued.isBefore(now.minusSeconds(60)), "projection_pending");
    var ids = new HashSet<>(b.getRequiredReaderIds());
    require(
        ids.size() == b.getRequiredReaderIds().size()
            && ids.equals(new HashSet<>(r.getRequiredReaderIds()))
            && ids.size() == r.getRequiredReaderIds().size()
            && r.getExpectedSelectors().keySet().equals(ids)
            && r.getReaderIdentities().keySet().equals(ids),
        "invalid_chain");
  }

  static void readers(
      List<TransportReaderEvidence> rows,
      ExpectedSelectors snapshot,
      SignedTransportTrustBundle bundle) {
    var required = new HashSet<>(bundle.getPayload().getRequiredReaderIds());
    require(
        snapshot.getExpectedSelectors() != null
            && snapshot.getReaderIdentities() != null
            && snapshot.getExpectedSelectors().keySet().equals(required)
            && snapshot.getReaderIdentities().keySet().equals(required),
        "invalid_chain");
    for (var row : rows) reader(row, snapshot, bundle);
  }

  private static void reader(
      TransportReaderEvidence row, ExpectedSelectors snapshot, SignedTransportTrustBundle bundle) {
    String id = row.getReaderId();
    require(
        snapshot.getReaderIdentities().containsKey(id)
            && Objects.equals(snapshot.getReaderIdentities().get(id), row.getIdentityId())
            && snapshot.getExpectedSelectors().containsKey(id),
        "invalid_chain");
    var selector = row.getSelector();
    if (selector == null) {
      require(
          row.getPhase() == TransportReaderEvidence.PhaseEnum.ISOLATED
              && snapshot.getExpectedSelectors().get(id) == null,
          "invalid_chain");
    } else {
      var b = bundle.getPayload();
      require(
          Objects.equals(selector.getInstallationId(), row.getIdentityId())
              && Objects.equals(selector.getTenantId(), b.getTenantId())
              && Objects.equals(selector.getRealm(), b.getRealm())
              && Objects.equals(selector.getBundleVersion(), b.getBundleVersion())
              && Objects.equals(selector.getBundleDigest(), digest(bundle))
              && Objects.equals(snapshot.getExpectedSelectors().get(id), digest(selector)),
          "invalid_chain");
    }
    if (row.getPhase() == TransportReaderEvidence.PhaseEnum.ISOLATED)
      require(Boolean.TRUE.equals(row.getIsolationEnforced()), "identity_quarantined");
  }
}
