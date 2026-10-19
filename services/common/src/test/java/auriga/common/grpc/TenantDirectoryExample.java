package auriga.common.grpc;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.RequiresPermission;
import auriga.contracts.org.v1.GetTenantRequest;
import auriga.contracts.org.v1.GetTenantResponse;
import auriga.contracts.org.v1.Tenant;
import auriga.contracts.org.v1.TenantServiceGrpc;
import io.grpc.ManagedChannel;
import io.grpc.stub.StreamObserver;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

/**
 * The local/remote switch (ADR 0003) on a stand-in for an {@code org-api} interface, backed by the
 * contract's internal {@code TenantService}. The org implementation is empty on purpose.
 */
final class TenantDirectoryExample {
  private TenantDirectoryExample() {}

  /** What org-api would export. */
  interface TenantDirectory {
    String displayName(String tenantId);
  }

  /** "org impl": the local bean used in merged deployments. */
  static final class LocalTenantDirectory implements TenantDirectory {
    @Override
    public String displayName(String tenantId) {
      if ("missing".equals(tenantId))
        throw AurigaException.of(CommonErrors.NOT_FOUND, "tenantId", tenantId);
      return "tenant " + tenantId;
    }
  }

  /** Remote adapter used in split deployments: same interface, same exceptions. */
  static final class GrpcTenantDirectory implements TenantDirectory {
    private final TenantServiceGrpc.TenantServiceBlockingStub stub;

    GrpcTenantDirectory(ManagedChannel channel) {
      this.stub = TenantServiceGrpc.newBlockingStub(channel);
    }

    @Override
    public String displayName(String tenantId) {
      return GrpcErrors.call(
          () ->
              stub.getTenant(GetTenantRequest.newBuilder().setTenantId(tenantId).build())
                  .getTenant()
                  .getDisplayName());
    }
  }

  /** "org impl" gRPC endpoint delegating to the local bean. */
  static class TenantGrpcService extends TenantServiceGrpc.TenantServiceImplBase {
    private final TenantDirectory local = new LocalTenantDirectory();

    @Override
    @RequiresPermission("org.tenants.read")
    public void getTenant(GetTenantRequest request, StreamObserver<GetTenantResponse> response) {
      switch (request.getTenantId()) {
        case "slow" -> sleep();
        case "crash" -> throw new IllegalStateException("internal secret detail");
        default -> {}
      }
      var ctx = RequestContext.current().orElseThrow();
      response.onNext(
          GetTenantResponse.newBuilder()
              .setTenant(
                  Tenant.newBuilder()
                      .setTenantId(TenantContext.current().map(Object::toString).orElse("none"))
                      .setSlug(ctx.requestId())
                      .setLocale(ctx.trace().traceId())
                      .setDisplayName(local.displayName(request.getTenantId())))
              .build());
      response.onCompleted();
    }

    private static void sleep() {
      try {
        Thread.sleep(2_000);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
      }
    }
  }

  @Configuration(proxyBeanMethods = false)
  static class Wiring {
    @Bean
    @ConditionalOnServiceMode(service = "org", mode = ConditionalOnServiceMode.Mode.LOCAL)
    TenantDirectory localTenantDirectory() {
      return new LocalTenantDirectory();
    }

    @Bean(destroyMethod = "shutdownNow")
    @ConditionalOnServiceMode(service = "org", mode = ConditionalOnServiceMode.Mode.GRPC)
    ManagedChannel orgChannel(Environment environment) {
      return GrpcChannels.open(
          GrpcChannels.target(environment, "org"),
          new AurigaClientInterceptor(() -> "unused-in-this-test", null));
    }

    @Bean
    @ConditionalOnServiceMode(service = "org", mode = ConditionalOnServiceMode.Mode.GRPC)
    TenantDirectory grpcTenantDirectory(ManagedChannel orgChannel) {
      return new GrpcTenantDirectory(orgChannel);
    }
  }
}
