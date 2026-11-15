package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import java.time.Instant;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import tools.jackson.databind.JsonNode;

/** Validate the authenticated C response against independently accepted bundle/control objects. */
public final class MsgnetExpectedSelectors {
  private MsgnetExpectedSelectors() {}

  public static final class Snapshot {
    private final JsonNode response, query, bundle, control;
    private final String tenant, realm;

    private Snapshot(
        JsonNode response,
        JsonNode query,
        JsonNode bundle,
        JsonNode control,
        String tenant,
        String realm) {
      this.response = response.deepCopy();
      this.query = query.deepCopy();
      this.bundle = bundle.deepCopy();
      this.control = control.deepCopy();
      this.tenant = tenant;
      this.realm = realm;
    }

    public void confirmation(
        MsgnetTransportVerifier verifier,
        JsonNode evidence,
        JsonNode authority,
        JsonNode latestControl,
        String challenge,
        Instant now,
        String requiredPhase) {
      require(control.equals(latestControl), "control_required");
      verify(response, query, bundle, latestControl, tenant, realm, now, true);
      verifier.confirmation(
          evidence,
          bundle,
          authority,
          latestControl,
          challenge,
          now,
          requiredPhase,
          response.path("expected_selectors"),
          response.path("reader_identities"));
    }
  }

  /**
   * Authentication is supplied by the fixed HTTPS/mTLS/JWT transport, never a response field.
   * Bundle/control signatures must already have been independently verified by that consumer.
   */
  public static Snapshot verify(
      JsonNode response,
      JsonNode query,
      JsonNode bundle,
      JsonNode control,
      String tenant,
      String realm,
      Instant now,
      boolean authenticatedLedger) {
    require(authenticatedLedger, "scope_denied");
    var b = bundle.path("payload");
    var c = control.path("payload");
    for (var value : List.of(response, query, b, c))
      require(
          tenant.equals(text(value, "tenant_id")) && realm.equals(text(value, "realm")),
          "scope_denied");
    for (String field :
        List.of("generation", "bundle_version", "phase", "authority_id", "authority_digest"))
      require(response.path(field).equals(b.path(field)), "invalid_chain");
    require(
        text(response, "bundle_digest").equals(digest(bundle))
            && query.path("bundle_digest").equals(response.path("bundle_digest"))
            && query.path("bundle_version").equals(response.path("bundle_version")),
        "invalid_chain");
    require(
        response.path("control_revision").equals(c.path("control_revision"))
            && query.path("control_revision").equals(c.path("control_revision"))
            && text(response, "control_digest").equals(digest(control))
            && query.path("control_digest").equals(response.path("control_digest")),
        "control_required");
    require(response.path("challenge").equals(query.path("challenge")), "invalid_chain");
    require(
        !time(response, "issued_at").isAfter(now)
            && !time(response, "issued_at").isBefore(now.minusSeconds(60)),
        "projection_pending");
    var ids = new HashSet<String>();
    for (var id : b.path("required_reader_ids")) require(ids.add(id.asString()), "invalid_chain");
    var returned = new HashSet<String>();
    for (var id : response.path("required_reader_ids"))
      require(returned.add(id.asString()), "invalid_chain");
    require(returned.equals(ids), "invalid_chain");
    require(
        keys(response.path("expected_selectors")).equals(ids)
            && keys(response.path("reader_identities")).equals(ids),
        "invalid_chain");
    schema("ExpectedSelectorsQuery", query, "invalid_chain");
    schema("ExpectedSelectors", response, "invalid_chain");
    return new Snapshot(response, query, bundle, control, tenant, realm);
  }

  static Set<String> keys(JsonNode map) {
    require(map.isObject(), "invalid_chain");
    return new HashSet<>(map.propertyNames());
  }
}
