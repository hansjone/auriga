package io.github.hansjone.auriga.common.persistence;

import com.baomidou.mybatisplus.extension.plugins.handler.TenantLineHandler;
import io.github.hansjone.auriga.common.context.TenantContext;
import java.util.Locale;
import java.util.Set;
import java.util.UUID;
import java.util.stream.Collectors;
import net.sf.jsqlparser.expression.Expression;
import net.sf.jsqlparser.expression.StringValue;

/**
 * MyBatis-Plus tenant line handler: every statement on a tenant table gets {@code tenant_id =
 * <current tenant>} (and inserts get the column). Without a bound tenant it throws {@link
 * io.github.hansjone.auriga.common.context.MissingTenantException} instead of reading all tenants.
 *
 * <p>Exceptions: tables listed as non-tenant (infrastructure tables such as the outbox), work
 * inside {@link TenantContext#callAsSystem}, and mapper methods annotated with MyBatis-Plus
 * {@code @InterceptorIgnore(tenantLine = "true")}. Row level security still applies to all of them.
 */
public final class AurigaTenantLineHandler implements TenantLineHandler {
  public static final String TENANT_COLUMN = "tenant_id";

  /** Tables of the common library that do not carry tenant isolation. */
  public static final Set<String> INFRASTRUCTURE_TABLES =
      Set.of(
          "outbox_event",
          "processed_event",
          "dead_letter_event",
          "flyway_schema_history",
          "auriga_common_schema_history");

  private final Set<String> nonTenantTables;

  public AurigaTenantLineHandler(Set<String> nonTenantTables) {
    this.nonTenantTables =
        java.util.stream.Stream.concat(INFRASTRUCTURE_TABLES.stream(), nonTenantTables.stream())
            .map(AurigaTenantLineHandler::normalize)
            .collect(Collectors.toUnmodifiableSet());
  }

  @Override
  public Expression getTenantId() {
    UUID tenant = TenantContext.require();
    TenantTransactionManager.verifyTransactionTenant();
    return new StringValue(tenant.toString());
  }

  @Override
  public String getTenantIdColumn() {
    return TENANT_COLUMN;
  }

  @Override
  public boolean ignoreTable(String tableName) {
    if (TenantContext.isSystem()) {
      TenantTransactionManager.verifyTransactionTenant();
      return true;
    }
    return nonTenantTables.contains(normalize(tableName));
  }

  /** Strips schema and quotes: {@code "org"."member"} and {@code member} are the same table. */
  static String normalize(String table) {
    String name = table.replace("\"", "");
    int dot = name.lastIndexOf('.');
    return (dot >= 0 ? name.substring(dot + 1) : name).toLowerCase(Locale.ROOT);
  }
}
