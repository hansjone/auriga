package io.github.hansjone.auriga.common.grpc;

import io.github.hansjone.auriga.common.context.RequestContext;
import io.github.hansjone.auriga.common.context.TenantContext;
import io.grpc.CallOptions;
import io.grpc.Channel;
import io.grpc.ClientCall;
import io.grpc.ClientInterceptor;
import io.grpc.ForwardingClientCall;
import io.grpc.Metadata;
import io.grpc.MethodDescriptor;
import java.time.Duration;
import java.util.Objects;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

/**
 * Client side of internal gRPC: attaches the service token, the current tenant ({@code
 * auriga-tenant-id}), request id and a child {@code traceparent}, and applies a default deadline
 * when the caller set none.
 */
public final class AurigaClientInterceptor implements ClientInterceptor {
  private final ServiceTokenProvider tokens;
  private final Duration defaultTimeout;

  public AurigaClientInterceptor(ServiceTokenProvider tokens, Duration defaultTimeout) {
    this.tokens = Objects.requireNonNull(tokens);
    this.defaultTimeout = defaultTimeout == null ? Duration.ofSeconds(5) : defaultTimeout;
  }

  @Override
  public <Q, R> ClientCall<Q, R> interceptCall(
      MethodDescriptor<Q, R> method, CallOptions options, Channel next) {
    CallOptions effective =
        options.getDeadline() == null
            ? options.withDeadlineAfter(defaultTimeout.toMillis(), TimeUnit.MILLISECONDS)
            : options;
    // Capture context on the calling thread; start() may run elsewhere.
    String token = tokens.token();
    UUID tenant = TenantContext.current().orElse(null);
    RequestContext request = RequestContext.current().orElse(null);
    return new ForwardingClientCall.SimpleForwardingClientCall<>(next.newCall(method, effective)) {
      @Override
      public void start(Listener<R> listener, Metadata headers) {
        headers.put(GrpcMetadata.AUTHORIZATION, "Bearer " + token);
        if (tenant != null) headers.put(GrpcMetadata.TENANT_ID, tenant.toString());
        if (request != null) {
          headers.put(GrpcMetadata.REQUEST_ID, request.requestId());
          headers.put(GrpcMetadata.TRACEPARENT, request.trace().child().toTraceparent());
        }
        super.start(listener, headers);
      }
    };
  }
}
