package auriga.collab.grpc;

import auriga.collab.app.CollabService;
import auriga.common.security.AuthenticatedOnly;
import auriga.contracts.collab.v1.*;
import io.grpc.stub.StreamObserver;
import java.util.function.Supplier;

/** Only exposed through the common internal gRPC server, never an HTTP controller. */
public class CollabGrpcService
    extends CollabCommunicationServiceGrpc.CollabCommunicationServiceImplBase {
  private final CollabService service;

  public CollabGrpcService(CollabService service) {
    this.service = service;
  }

  @Override
  @AuthenticatedOnly
  public void registerEndpoint(
      RegisterEndpointRequest r, StreamObserver<RegisterEndpointResponse> o) {
    respond(
        o,
        () ->
            RegisterEndpointResponse.newBuilder().setEndpoint(service.registerEndpoint(r)).build());
  }

  @Override
  @AuthenticatedOnly
  public void bindSession(BindSessionRequest r, StreamObserver<BindSessionResponse> o) {
    respond(o, () -> BindSessionResponse.newBuilder().setBinding(service.bindSession(r)).build());
  }

  @Override
  @AuthenticatedOnly
  public void listEndpoints(ListEndpointsRequest r, StreamObserver<ListEndpointsResponse> o) {
    respond(o, () -> ListEndpointsResponse.newBuilder().setPage(service.listEndpoints(r)).build());
  }

  @Override
  @AuthenticatedOnly
  public void openThread(OpenThreadRequest r, StreamObserver<OpenThreadResponse> o) {
    respond(o, () -> OpenThreadResponse.newBuilder().setMessage(service.openThread(r)).build());
  }

  @Override
  @AuthenticatedOnly
  public void replyThread(ReplyThreadRequest r, StreamObserver<ReplyThreadResponse> o) {
    respond(o, () -> ReplyThreadResponse.newBuilder().setMessage(service.replyThread(r)).build());
  }

  @Override
  @AuthenticatedOnly
  public void readThread(ReadThreadRequest r, StreamObserver<ReadThreadResponse> o) {
    respond(o, () -> ReadThreadResponse.newBuilder().setPage(service.readThread(r)).build());
  }

  @Override
  @AuthenticatedOnly
  public void pullDeliveries(PullDeliveriesRequest r, StreamObserver<PullDeliveriesResponse> o) {
    respond(
        o, () -> PullDeliveriesResponse.newBuilder().setPage(service.pullDeliveries(r)).build());
  }

  @Override
  @AuthenticatedOnly
  public void ackDelivery(AckDeliveryRequest r, StreamObserver<AckDeliveryResponse> o) {
    respond(o, () -> AckDeliveryResponse.newBuilder().setDelivery(service.ackDelivery(r)).build());
  }

  @Override
  @AuthenticatedOnly
  public void revokeEndpoint(RevokeEndpointRequest r, StreamObserver<RevokeEndpointResponse> o) {
    respond(
        o,
        () -> RevokeEndpointResponse.newBuilder().setEndpoint(service.revokeEndpoint(r)).build());
  }

  private static <T> void respond(StreamObserver<T> observer, Supplier<T> work) {
    observer.onNext(work.get());
    observer.onCompleted();
  }
}
