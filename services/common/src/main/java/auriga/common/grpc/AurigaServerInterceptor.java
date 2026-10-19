package auriga.common.grpc;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.observability.LogFields;
import auriga.common.security.AccessTokenVerifier;
import auriga.common.security.AurigaPrincipal;
import io.grpc.ForwardingServerCall;
import io.grpc.ForwardingServerCallListener;
import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.Status;
import java.util.Locale;
import java.util.Objects;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicBoolean;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * Server side of internal gRPC (contracts/README.md, "服务 → 服务"): only internal service tokens
 * ({@code aud=auriga.internal}, {@code ptyp=internal_service}, {@code sub=svc:<name>}) are
 * accepted; external client credentials tokens (service accounts), user and device tokens are
 * refused. Propagates request id and traceparent, maps errors, and runs every listener callback
 * with {@link RequestContext} / {@link TenantContext} bound, because gRPC may call them on
 * different threads.
 *
 * <p>Tenant rule: without {@code auriga-tenant-id} no tenant is bound. With it, the caller's {@code
 * sub} must be in the configured delegate allowlist, otherwise PERMISSION_DENIED.
 */
public final class AurigaServerInterceptor implements ServerInterceptor {
  private static final Logger log = LoggerFactory.getLogger(AurigaServerInterceptor.class);

  private final AccessTokenVerifier verifier;
  private final Set<String> publicMethods;
  private final Set<String> tenantDelegates;

  /**
   * @param verifier must wrap {@link auriga.common.security.AccessTokenDecoders#createInternal}
   * @param publicMethods full method names callable without a token (e.g. health checks)
   * @param tenantDelegates internal service subjects ({@code svc:<name>}) allowed to act for a
   *     tenant via {@code auriga-tenant-id}; everyone else calls without a tenant
   */
  public AurigaServerInterceptor(
      AccessTokenVerifier verifier, Set<String> publicMethods, Set<String> tenantDelegates) {
    this.verifier = Objects.requireNonNull(verifier);
    this.publicMethods = Set.copyOf(publicMethods == null ? Set.of() : publicMethods);
    this.tenantDelegates = Set.copyOf(tenantDelegates == null ? Set.of() : tenantDelegates);
  }

  @Override
  public <Q, R> ServerCall.Listener<Q> interceptCall(
      ServerCall<Q, R> call, Metadata headers, ServerCallHandler<Q, R> next) {
    RequestContext request =
        RequestContext.start(
            headers.get(GrpcMetadata.REQUEST_ID), headers.get(GrpcMetadata.TRACEPARENT));
    UUID tenant;
    try {
      if (publicMethods.contains(call.getMethodDescriptor().getFullMethodName())) {
        tenant = null;
      } else {
        AurigaPrincipal principal = authenticate(headers);
        tenant = tenant(principal, headers.get(GrpcMetadata.TENANT_ID), tenantDelegates);
        request = request.withPrincipal(principal);
      }
    } catch (AurigaException e) {
      var status = GrpcErrors.toStatus(e);
      call.close(status.getStatus(), status.getTrailers());
      return new ServerCall.Listener<>() {};
    }
    Scope scope = new Scope(request, tenant);
    AtomicBoolean closed = new AtomicBoolean();
    ServerCall<Q, R> guarded =
        new ForwardingServerCall.SimpleForwardingServerCall<>(call) {
          @Override
          public void close(Status status, Metadata trailers) {
            if (closed.compareAndSet(false, true)) super.close(status, trailers);
          }
        };
    ServerCall.Listener<Q> delegate = scope.call(() -> next.startCall(guarded, headers), guarded);
    if (delegate == null) return new ServerCall.Listener<>() {};
    return new ForwardingServerCallListener.SimpleForwardingServerCallListener<>(delegate) {
      @Override
      public void onMessage(Q message) {
        scope.run(() -> super.onMessage(message), guarded);
      }

      @Override
      public void onHalfClose() {
        scope.run(super::onHalfClose, guarded);
      }

      @Override
      public void onCancel() {
        scope.run(super::onCancel, guarded);
      }

      @Override
      public void onComplete() {
        scope.run(super::onComplete, guarded);
      }

      @Override
      public void onReady() {
        scope.run(super::onReady, guarded);
      }
    };
  }

  private AurigaPrincipal authenticate(Metadata headers) {
    String authorization = headers.get(GrpcMetadata.AUTHORIZATION);
    if (authorization == null
        || !authorization.regionMatches(true, 0, "Bearer ", 0, 7)
        || authorization.length() <= 7) {
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    }
    AurigaPrincipal principal = verifier.verify(authorization.substring(7).trim());
    // The internal decoder already guarantees this; checked again so a misconfigured verifier
    // (e.g. the platform decoder) still cannot let service accounts or users in.
    if (!principal.isInternalService()) {
      throw AurigaException.of(CommonErrors.FORBIDDEN, "reason", "internal_service_token_required");
    }
    return principal;
  }

  static UUID tenant(AurigaPrincipal principal, String header, Set<String> delegates) {
    if (header == null || header.isBlank()) return null;
    if (!delegates.contains(principal.subject())) {
      throw AurigaException.of(CommonErrors.FORBIDDEN, "reason", "tenant_delegation_not_allowed");
    }
    UUID requested;
    try {
      requested = UUID.fromString(header);
      if (!requested.toString().equals(header.toLowerCase(Locale.ROOT))) {
        throw new IllegalArgumentException();
      }
    } catch (IllegalArgumentException e) {
      throw AurigaException.of(CommonErrors.VALIDATION_FAILED, "field", "auriga-tenant-id");
    }
    if (TenantContext.PLATFORM_TENANT.equals(requested)) {
      throw AurigaException.of(CommonErrors.VALIDATION_FAILED, "field", "auriga-tenant-id");
    }
    return requested;
  }

  /** Binds request/tenant context and MDC around a callback and maps thrown errors. */
  private record Scope(RequestContext request, UUID tenant) {
    <T> T call(ScopedValue.CallableOp<T, RuntimeException> op, ServerCall<?, ?> call) {
      try (var a = LogFields.of(request);
          var b = LogFields.tenant(tenant)) {
        return request.call(() -> tenant == null ? op.call() : TenantContext.call(tenant, op));
      } catch (RuntimeException e) {
        fail(call, e);
        return null;
      }
    }

    void run(Runnable op, ServerCall<?, ?> call) {
      call(
          () -> {
            op.run();
            return null;
          },
          call);
    }

    private static void fail(ServerCall<?, ?> call, RuntimeException e) {
      if (!(e instanceof AurigaException ae) || ae.errorCode().httpStatus() >= 500) {
        log.error("gRPC call {} failed", call.getMethodDescriptor().getFullMethodName(), e);
      }
      var status = GrpcErrors.toStatus(e);
      try {
        call.close(
            status.getStatus(),
            status.getTrailers() == null ? new Metadata() : status.getTrailers());
      } catch (IllegalStateException alreadyClosed) {
        // the handler closed the call itself
      }
    }
  }
}
