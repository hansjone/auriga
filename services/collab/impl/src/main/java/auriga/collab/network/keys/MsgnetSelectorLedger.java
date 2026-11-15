package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import java.util.UUID;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** C-owned material approvals, separate from receipt phases and signed reader evidence. */
final class MsgnetSelectorLedger {
  record Snapshot(ObjectNode selectors, ObjectNode identities, ObjectNode bindings) {}

  static void freeze(JdbcTemplate jdbc, UUID tenant, String realm, JsonNode bundle) {
    for (var reader : bundle.path("payload").path("required_reader_ids")) {
      String id = reader.asString();
      require(
          find(bundle.path("payload").path("scope"), "identity_id", id) != null, "invalid_chain");
      jdbc.update(
          "INSERT INTO collab.msgnet_transport_selector (id, tenant_id, realm, bundle_digest, reader_id, identity_id) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, bundle_digest, reader_id) DO NOTHING",
          auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
          tenant,
          realm,
          digest(bundle),
          id,
          id);
    }
  }

  static void approve(
      JdbcTemplate jdbc, UUID tenant, String realm, JsonNode snapshot, JsonNode proposed) {
    // Reconstruct from C's authorized bytes; never copy an evidence/receipt digest into authority.
    var manifest = ((ObjectNode) snapshot).deepCopy();
    manifest.set("material_revision", proposed.path("material_revision"));
    schema("LocalProjectionManifest", manifest, "invalid_chain");
    var selector = manifest.deepCopy();
    selector.remove("files");
    selector.put("manifest_digest", digest(manifest));
    schema("LocalProjectionSelector", selector, "invalid_chain");
    require(selector.equals(proposed), "invalid_chain");
    var rows =
        jdbc.queryForList(
            "SELECT identity_id, selector, ambiguous FROM collab.msgnet_transport_selector WHERE tenant_id = ? AND realm = ? AND bundle_digest = ? AND reader_id = ? FOR UPDATE",
            tenant,
            realm,
            text(snapshot, "bundle_digest"),
            text(snapshot, "installation_id"));
    require(
        rows.size() == 1 && !Boolean.TRUE.equals(rows.getFirst().get("ambiguous")),
        "projection_pending");
    var row = rows.getFirst();
    require(text(snapshot, "installation_id").equals(row.get("identity_id")), "scope_denied");
    if (row.get("selector") != null) {
      // Subsequent legitimate exports may refresh business material. They do not replace this
      // bundle's canonical CA-stage binding; completion still requires its exact approved receipt.
      return;
    }
    jdbc.update(
        "UPDATE collab.msgnet_transport_selector SET selector = ?, authorization_snapshot = ? WHERE tenant_id = ? AND realm = ? AND bundle_digest = ? AND reader_id = ?",
        MsgnetRenewal.canonical(selector),
        MsgnetRenewal.canonical(snapshot),
        tenant,
        realm,
        text(snapshot, "bundle_digest"),
        text(snapshot, "installation_id"));
  }

  static Snapshot read(JdbcTemplate jdbc, UUID tenant, String realm, JsonNode bundle) {
    var rows =
        jdbc.queryForList(
            "SELECT reader_id, identity_id, selector, authorization_snapshot, ambiguous FROM collab.msgnet_transport_selector WHERE tenant_id = ? AND realm = ? AND bundle_digest = ?",
            tenant,
            realm,
            digest(bundle));
    var selectors = MsgnetRenewal.object();
    var identities = MsgnetRenewal.object();
    var bindings = MsgnetRenewal.object();
    require(
        rows.size() == bundle.path("payload").path("required_reader_ids").size(),
        "projection_pending");
    for (var row : rows) {
      String id = row.get("reader_id").toString();
      require(
          contains(bundle.path("payload").path("required_reader_ids"), id)
              && !Boolean.TRUE.equals(row.get("ambiguous")),
          "projection_pending");
      identities.put(id, row.get("identity_id").toString());
      if (row.get("selector") == null) selectors.putNull(id);
      else {
        require(row.get("authorization_snapshot") != null, "projection_pending");
        var selector = NetworkJson.parse(row.get("selector").toString(), 1048576);
        var snapshot = NetworkJson.parse(row.get("authorization_snapshot").toString(), 1048576);
        var manifest = ((ObjectNode) snapshot).deepCopy();
        manifest.set("material_revision", selector.path("material_revision"));
        var reconstructed = manifest.deepCopy();
        reconstructed.remove("files");
        reconstructed.put("manifest_digest", digest(manifest));
        require(
            reconstructed.equals(selector)
                && text(selector, "installation_id").equals(row.get("identity_id"))
                && text(selector, "bundle_digest").equals(digest(bundle))
                && selector
                    .path("bundle_version")
                    .equals(bundle.path("payload").path("bundle_version")),
            "invalid_chain");
        schema("LocalProjectionSelector", selector, "invalid_chain");
        selectors.put(id, digest(selector));
        bindings.set(id, selector);
      }
    }
    return new Snapshot(selectors, identities, bindings);
  }
}
