package auriga.common.context;

import java.util.Objects;
import java.util.Optional;
import java.util.UUID;

/**
 * The tenant the current unit of work runs for.
 *
 * <p>Backed by a {@link ScopedValue} (final in Java 25), not a ThreadLocal: a binding is immutable,
 * ends exactly when its scope returns and is never inherited implicitly by other threads, so a
 * pooled or virtual thread can not leak one request's tenant into the next. Work handed to another
 * thread (executors, NATS callbacks, schedulers) must re-bind the tenant explicitly with {@link
 * #call} / {@link #run}. Only {@code StructuredTaskScope} forks inherit the binding.
 *
 * <p>Sources of the binding: the HTTP security filter (access token {@code tid}), the gRPC server
 * interceptor (service token + {@code auriga-tenant-id} metadata), the event consumer (envelope
 * {@code tenantid}) and background jobs (explicit {@link #run}).
 */
public final class TenantContext {
  /**
   * Platform-level events and records without a tenant use the all-zero tenant (events contract).
   */
  public static final UUID PLATFORM_TENANT = new UUID(0L, 0L);

  private static final ScopedValue<Binding> BINDING = ScopedValue.newInstance();

  private TenantContext() {}

  private record Binding(UUID tenantId, boolean system) {}

  /** The bound tenant, empty when nothing is bound or inside an explicit system scope. */
  public static Optional<UUID> current() {
    return BINDING.isBound() ? Optional.ofNullable(BINDING.get().tenantId()) : Optional.empty();
  }

  /**
   * The bound tenant.
   *
   * @throws MissingTenantException when no tenant is bound; tenant scoped data access must never
   *     fall back to "all tenants".
   */
  public static UUID require() {
    if (BINDING.isBound() && BINDING.get().tenantId() != null) return BINDING.get().tenantId();
    throw new MissingTenantException(
        isSystem()
            ? "Tenant scoped access inside a system scope; bind a tenant explicitly"
            : "No tenant bound to the current unit of work");
  }

  /** True inside {@link #callAsSystem} / {@link #runAsSystem}. */
  public static boolean isSystem() {
    return BINDING.isBound() && BINDING.get().system();
  }

  public static <T, X extends Throwable> T call(UUID tenantId, ScopedValue.CallableOp<T, X> op)
      throws X {
    Objects.requireNonNull(tenantId, "tenantId");
    return ScopedValue.where(BINDING, new Binding(tenantId, false)).call(op);
  }

  public static void run(UUID tenantId, Runnable op) {
    Objects.requireNonNull(tenantId, "tenantId");
    ScopedValue.where(BINDING, new Binding(tenantId, false)).run(op);
  }

  /**
   * Runs explicitly marked system-level work: the MyBatis-Plus tenant line is not added. Row level
   * security still applies in the database, so tenant tables stay invisible; use this only for
   * tables without {@code tenant_id} or for cross-tenant maintenance that binds tenants itself.
   */
  public static <T, X extends Throwable> T callAsSystem(ScopedValue.CallableOp<T, X> op) throws X {
    return ScopedValue.where(BINDING, new Binding(null, true)).call(op);
  }

  public static void runAsSystem(Runnable op) {
    ScopedValue.where(BINDING, new Binding(null, true)).run(op);
  }
}
