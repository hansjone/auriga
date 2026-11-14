package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.persistence.TenantTransactionManager;
import java.nio.file.Path;
import java.time.Clock;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;

/** Public trust channel remains usable when KEK/runtime/leaf secrets cannot be loaded. */
public final class MsgnetPublicControl {
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final UUID tenant;
  private final String realm;
  private final MsgnetV2Verifier verifier;
  private final Path source;

  public MsgnetPublicControl(
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      UUID tenant,
      String realm,
      MsgnetV2Verifier verifier,
      Path source) {
    this.jdbc = jdbc;
    this.tx = new TransactionTemplate(manager);
    this.tenant = tenant;
    this.realm = realm;
    this.verifier = verifier;
    this.source = source;
  }

  public JsonNode refresh() {
    var candidate = NetworkJson.parse(ProtectedKeyFiles.readText(source, 1048576), 1048576);
    verifier.verifyControl(candidate);
    return TenantContext.call(
        tenant,
        () ->
            tx.execute(
                status -> {
                  lock();
                  var old = highest();
                  if (old != null
                      && number(old.path("payload"), "control_revision")
                              .compareTo(number(candidate.path("payload"), "control_revision"))
                          > 0) return old;
                  install(candidate, false);
                  return candidate;
                }));
  }

  public String current() {
    return MsgnetRenewal.canonical(refresh());
  }

  void install(JsonNode control, boolean migration) {
    verifier.verifyControl(control);
    TenantContext.call(
        tenant,
        () ->
            tx.execute(
                status -> {
                  lock();
                  if (!migration)
                    require(
                        jdbc.queryForObject(
                                "SELECT count(*) FROM collab.network_key_publication p WHERE tenant_id = ? AND realm = ? AND NOT EXISTS (SELECT 1 FROM collab.msgnet_realm r WHERE r.tenant_id = p.tenant_id AND r.realm = p.realm AND r.legacy_disabled)",
                                Integer.class,
                                tenant,
                                realm)
                            == 0,
                        "migration_outside_commitment");
                  var old = highest();
                  if (old != null) {
                    int order =
                        number(control.path("payload"), "control_revision")
                            .compareTo(number(old.path("payload"), "control_revision"));
                    require(order >= 0, "control_rollback");
                    if (order == 0) require(old.equals(control), "same_version_conflict");
                    int generationOrder =
                        number(control.path("payload"), "minimum_generation")
                            .compareTo(number(old.path("payload"), "minimum_generation"));
                    require(
                        generationOrder >= 0
                            && (generationOrder > 0
                                || number(control.path("payload"), "minimum_keyset_version")
                                        .compareTo(
                                            number(old.path("payload"), "minimum_keyset_version"))
                                    >= 0),
                        "control_rollback");
                    for (String field :
                        List.of("revoked_authority_ids", "revoked_installation_ids"))
                      for (var id : old.path("payload").path(field))
                        require(
                            contains(control.path("payload").path(field), id.asString()),
                            "control_rollback");
                  }
                  jdbc.update(
                      "INSERT INTO collab.msgnet_public_control (id, tenant_id, realm, root_control) VALUES (?, ?, ?, ?) ON CONFLICT (tenant_id, realm) DO UPDATE SET root_control = EXCLUDED.root_control",
                      auriga.common.persistence.UuidV7.next(Clock.systemUTC()),
                      tenant,
                      realm,
                      MsgnetRenewal.canonical(control));
                  jdbc.update(
                      "UPDATE collab.msgnet_realm SET root_control = ? WHERE tenant_id = ? AND realm = ?",
                      MsgnetRenewal.canonical(control),
                      tenant,
                      realm);
                  return null;
                }));
  }

  private JsonNode highest() {
    var rows =
        jdbc.queryForList(
            "SELECT root_control FROM collab.msgnet_public_control WHERE tenant_id = ? AND realm = ? UNION ALL SELECT root_control FROM collab.msgnet_realm WHERE tenant_id = ? AND realm = ?",
            String.class,
            tenant,
            realm,
            tenant,
            realm);
    JsonNode highest = null;
    for (String row : rows) {
      var control = NetworkJson.parse(row, 1048576);
      verifier.verifyControl(control);
      if (highest == null) highest = control;
      else {
        int order =
            number(control.path("payload"), "control_revision")
                .compareTo(number(highest.path("payload"), "control_revision"));
        if (order == 0) require(control.equals(highest), "same_version_conflict");
        if (order > 0) highest = control;
      }
    }
    return highest;
  }

  private void lock() {
    TenantTransactionManager.verifyTransactionTenant();
    jdbc.queryForObject(
        "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
        Object.class,
        tenant + ":network-keys:" + realm);
  }
}
