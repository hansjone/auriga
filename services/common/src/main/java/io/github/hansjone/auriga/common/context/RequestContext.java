package io.github.hansjone.auriga.common.context;

import io.github.hansjone.auriga.common.security.AurigaPrincipal;
import java.util.Objects;
import java.util.Optional;
import java.util.UUID;

/**
 * Per-request correlation data (request id, trace, authenticated principal, client info), bound
 * with a {@link ScopedValue} like {@link TenantContext} and for the same reasons.
 *
 * @param requestId {@code X-Request-Id} from edge, or generated
 * @param trace W3C trace context of the local span
 * @param principal authenticated caller, null before authentication / for anonymous calls
 * @param clientIp original client address as reported by edge, may be null
 * @param userAgent caller user agent, may be null
 */
public record RequestContext(
    String requestId,
    TraceContext trace,
    AurigaPrincipal principal,
    String clientIp,
    String userAgent) {
  private static final ScopedValue<RequestContext> CURRENT = ScopedValue.newInstance();

  public RequestContext {
    Objects.requireNonNull(requestId, "requestId");
    Objects.requireNonNull(trace, "trace");
  }

  public static RequestContext start(String requestId, String traceparent) {
    return new RequestContext(
        sanitizeRequestId(requestId), TraceContext.continueOrStart(traceparent), null, null, null);
  }

  public static Optional<RequestContext> current() {
    return CURRENT.isBound() ? Optional.of(CURRENT.get()) : Optional.empty();
  }

  public static Optional<AurigaPrincipal> currentPrincipal() {
    return current().map(RequestContext::principal);
  }

  public RequestContext withPrincipal(AurigaPrincipal newPrincipal) {
    return new RequestContext(requestId, trace, newPrincipal, clientIp, userAgent);
  }

  public RequestContext withClient(String ip, String agent) {
    return new RequestContext(requestId, trace, principal, ip, agent);
  }

  public <T, X extends Throwable> T call(ScopedValue.CallableOp<T, X> op) throws X {
    return ScopedValue.where(CURRENT, this).call(op);
  }

  public void run(Runnable op) {
    ScopedValue.where(CURRENT, this).run(op);
  }

  /**
   * Accepts an incoming request id only if it is short and printable (it ends up in logs and audit
   * records); otherwise generates a new one.
   */
  public static String sanitizeRequestId(String requestId) {
    if (requestId != null
        && !requestId.isEmpty()
        && requestId.length() <= 128
        && requestId.chars().allMatch(c -> c > 0x20 && c < 0x7f && c != '"' && c != '\\')) {
      return requestId;
    }
    return UUID.randomUUID().toString();
  }
}
