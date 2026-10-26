package auriga.iam;

import static auriga.common.error.CommonErrors.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.security.AuthenticatedOnly;
import auriga.common.security.AuthorizationRules;
import auriga.contracts.iam.v1.*;
import io.grpc.stub.StreamObserver;
import java.util.Set;
import java.util.function.Supplier;

/** Standard internal service identity plus a distinct delegated business tenant. */
public class IamGrpcNetworkSources
    extends NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceImplBase {
  private final IamNetworkSourceAuthority authority;

  IamGrpcNetworkSources(IamNetworkSourceAuthority authority) {
    this.authority = authority;
  }

  @Override
  @AuthenticatedOnly
  public void registerAgentSource(
      RegisterAgentSourceRequest r, StreamObserver<RegisterAgentSourceResponse> observer) {
    respond(r.getConnection(), observer, () -> authority.register(r));
  }

  @Override
  @AuthenticatedOnly
  public void readAgentSourceReceipt(
      ReadAgentSourceReceiptRequest r, StreamObserver<RegisterAgentSourceResponse> observer) {
    respond(r.getOriginalRequest().getConnection(), observer, () -> authority.readReceipt(r));
  }

  @Override
  @AuthenticatedOnly
  public void getAgentSource(
      GetAgentSourceRequest r, StreamObserver<GetAgentSourceResponse> observer) {
    respond(r.getConnection(), observer, () -> authority.get(r));
  }

  @Override
  @AuthenticatedOnly
  public void revokeAgentSource(
      RevokeAgentSourceRequest r, StreamObserver<RevokeAgentSourceResponse> observer) {
    respond(r.getConnection(), observer, () -> authority.revoke(r));
  }

  private static <T> void respond(
      NetworkAuthorityConnection c, StreamObserver<T> observer, Supplier<T> work) {
    var p =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(UNAUTHENTICATED));
    if (!p.isInternalService()
        || !p.subject().equals("svc:collab")
        || !p.tenantId().equals(TenantContext.PLATFORM_TENANT)
        || TenantContext.current()
            .filter(
                t ->
                    !t.equals(TenantContext.PLATFORM_TENANT)
                        && t.toString().equals(c.getTenantId()))
            .isEmpty()) throw new AurigaException(FORBIDDEN);
    T response;
    try {
      if (io.grpc.Context.current().isCancelled()) throw new AurigaException(UNAVAILABLE);
      response = work.get();
      if (io.grpc.Context.current().isCancelled()) throw new AurigaException(UNAVAILABLE);
    } catch (AurigaException error) {
      if (Set.of(VALIDATION_FAILED.code(), CONFLICT.code(), FORBIDDEN.code(), UNAVAILABLE.code())
          .contains(error.errorCode().code())) throw new AurigaException(error.errorCode());
      throw new AurigaException(UNAVAILABLE);
    } catch (RuntimeException error) {
      throw new AurigaException(UNAVAILABLE);
    }
    observer.onNext(response);
    observer.onCompleted();
  }
}
