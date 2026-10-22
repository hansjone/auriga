package auriga.org;

import auriga.common.persistence.UuidV7;
import auriga.iam.api.M3PermissionCatalog;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import org.springframework.transaction.support.TransactionSynchronization;
import org.springframework.transaction.support.TransactionSynchronizationManager;

/** Tenant-scoped, idempotent registration, using only the formal catalog as metadata authority. */
final class M3Permissions {
  private final OrgStore store;
  private final Set<UUID> registered = ConcurrentHashMap.newKeySet();

  M3Permissions(OrgStore store) {
    this.store = store;
  }

  void register() {
    UUID tenant = store.tenantId();
    if (registered.contains(tenant)) return;
    for (var permission : M3PermissionCatalog.all()) {
      store
          .jdbc()
          .update(
              "INSERT INTO permission (id, tenant_id, key, service, description_en, description_zh, sensitive)"
                  + " VALUES (?,?,?,?,?,?,?) ON CONFLICT (tenant_id,key) DO UPDATE"
                  + " SET service=excluded.service, description_en=excluded.description_en,"
                  + " description_zh=excluded.description_zh, sensitive=excluded.sensitive"
                  + " WHERE (permission.service,permission.description_en,permission.description_zh,permission.sensitive)"
                  + " IS DISTINCT FROM (excluded.service,excluded.description_en,excluded.description_zh,excluded.sensitive)",
              UuidV7.next(),
              store.tenantId(),
              permission.key(),
              permission.key().split("\\.")[0],
              permission.english(),
              permission.chinese(),
              permission.sensitive());
    }
    var roles = new TreeSet<String>();
    M3PermissionCatalog.all().forEach(permission -> roles.addAll(permission.builtinRoles()));
    for (String role : roles) {
      String[] additions =
          M3PermissionCatalog.forRole(role).stream().sorted().toArray(String[]::new);
      OrgArrays.update(
          store.jdbc(),
          "UPDATE role SET permissions=ARRAY(SELECT DISTINCT p FROM unnest(permissions || ?::text[]) p ORDER BY p)"
              + " WHERE tenant_id=? AND key=? AND builtin=true AND NOT permissions @> ?::text[]",
          additions,
          store.tenantId(),
          role,
          additions);
    }
    // Cache registration only after commit, never membership, role bindings or effective rights.
    TransactionSynchronizationManager.registerSynchronization(
        new TransactionSynchronization() {
          @Override
          public void afterCommit() {
            registered.add(tenant);
          }
        });
  }
}
