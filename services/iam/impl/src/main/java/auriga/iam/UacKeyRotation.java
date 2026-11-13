package auriga.iam;

import auriga.common.context.TenantContext;
import java.util.*;

/** Fixed envelope paths only. No plaintext is logged or materialized outside process memory. */
final class UacKeyRotation {
  private record Envelope(String table, String field) {}

  private static final List<Envelope> ENVELOPES =
      List.of(
          new Envelope("uac_credential", "envelope"),
          new Envelope("uac_challenge", "credential"),
          new Envelope("uac_challenge", "issued"),
          new Envelope("login_tx", "uacCredential"),
          new Envelope("uac_navigation", "response"));
  private final IamStore store;
  private final UacCredentialCipher cipher;

  UacKeyRotation(IamStore store, UacCredentialCipher cipher) {
    this.store = store;
    this.cipher = cipher;
  }

  void verifyKeys() {
    UUID cursor = null;
    for (; ; ) {
      var tenants = store.uacTenants(cursor);
      if (tenants.isEmpty()) return;
      for (UUID tenant : tenants) TenantContext.run(tenant, this::verifyTenantKeys);
      cursor = tenants.getLast();
    }
  }

  void verifyTenantKeys() {
    store.tx(
        () -> {
          for (Envelope envelope : ENVELOPES) {
            var ids =
                store
                    .networkJdbc()
                    .queryForList(
                        "SELECT DISTINCT data->'"
                            + envelope.field()
                            + "'->>'keyId' FROM iam."
                            + envelope.table()
                            + " WHERE tenant_id=? AND jsonb_exists(data, '"
                            + envelope.field()
                            + "')",
                        String.class,
                        TenantContext.require());
            if (ids.stream().anyMatch(id -> !cipher.canDecrypt(id)))
              throw new IllegalStateException(
                  "UAC decryption keys are missing; retain all referenced historical keys");
            UUID after = new UUID(0, 0);
            for (; ; ) {
              var rows =
                  store
                      .networkJdbc()
                      .query(
                          "SELECT id,data->>'providerId' AS provider,data->'"
                              + envelope.field()
                              + "' AS envelope FROM iam."
                              + envelope.table()
                              + " WHERE tenant_id=? AND id>? AND jsonb_exists(data,'"
                              + envelope.field()
                              + "') ORDER BY id LIMIT 500",
                          (rs, i) ->
                              Json.object(
                                  "id",
                                  rs.getString("id"),
                                  "provider",
                                  rs.getString("provider"),
                                  "envelope",
                                  Json.MAPPER.readTree(rs.getString("envelope"))),
                          TenantContext.require(),
                          after);
              for (var row : rows)
                cipher.decrypt(
                    row.path("envelope"),
                    TenantContext.require(),
                    UUID.fromString(row.path("provider").asString()));
              if (rows.size() < 500) break;
              after = UUID.fromString(rows.getLast().path("id").asString());
            }
          }
          return null;
        });
  }

  int rotateBatch(int limit) {
    if (limit < 1 || limit > 50)
      throw new IllegalArgumentException("Invalid UAC key rotation batch");
    return store.tx(
        () -> {
          int changed = 0;
          for (Envelope envelope : ENVELOPES) {
            var rows =
                store
                    .networkJdbc()
                    .query(
                        "SELECT data::text FROM iam."
                            + envelope.table()
                            + " WHERE tenant_id=? AND data->'"
                            + envelope.field()
                            + "'->>'keyId'<>? ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED",
                        (rs, i) -> Json.read(rs.getString(1)),
                        TenantContext.require(),
                        cipher.activeKeyId(),
                        limit - changed);
            for (var row : rows) {
              row.set(
                  envelope.field(),
                  cipher.reencrypt(
                      row.path(envelope.field()),
                      TenantContext.require(),
                      UUID.fromString(row.path("providerId").asString())));
              store.save(envelope.table(), row);
              changed++;
            }
            if (changed == limit) break;
          }
          return changed;
        });
  }
}
