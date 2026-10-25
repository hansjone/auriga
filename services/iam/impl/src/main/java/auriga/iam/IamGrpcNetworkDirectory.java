package auriga.iam;

import auriga.common.error.*;
import auriga.common.security.AuthenticatedOnly;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkDirectory;
import io.grpc.stub.StreamObserver;

/**
 * Collab-only registration adapter; the common interceptor owns JWT, tenant and transport checks.
 */
public class IamGrpcNetworkDirectory
    extends NetworkDirectoryServiceGrpc.NetworkDirectoryServiceImplBase {
  private final NetworkDirectory directory;

  public IamGrpcNetworkDirectory(NetworkDirectory directory) {
    this.directory = directory;
  }

  @Override
  @AuthenticatedOnly
  public void resolveNetworkNode(
      ResolveNetworkNodeRequest request, StreamObserver<ResolveNetworkNodeResponse> observer) {
    IamNetworkDirectory.collabCaller();
    ResolveNetworkNodeResponse response;
    try {
      response = directory.resolve(request.getRealm(), request.getNode());
    } catch (AurigaException error) {
      throw error;
    } catch (RuntimeException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    observer.onNext(response);
    observer.onCompleted();
  }
}
