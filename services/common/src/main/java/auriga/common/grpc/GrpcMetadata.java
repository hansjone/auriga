package auriga.common.grpc;

import io.grpc.Metadata;

/** Metadata keys used between services. */
public final class GrpcMetadata {
  public static final Metadata.Key<String> AUTHORIZATION =
      Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);
  public static final Metadata.Key<String> TENANT_ID =
      Metadata.Key.of("auriga-tenant-id", Metadata.ASCII_STRING_MARSHALLER);
  public static final Metadata.Key<String> REQUEST_ID =
      Metadata.Key.of("x-request-id", Metadata.ASCII_STRING_MARSHALLER);
  public static final Metadata.Key<String> TRACEPARENT =
      Metadata.Key.of("traceparent", Metadata.ASCII_STRING_MARSHALLER);
  public static final Metadata.Key<String> ERROR_CODE =
      Metadata.Key.of("auriga-error-code", Metadata.ASCII_STRING_MARSHALLER);

  /** JSON object of error params, UTF-8 (binary header). */
  public static final Metadata.Key<byte[]> ERROR_PARAMS =
      Metadata.Key.of("auriga-error-params-bin", Metadata.BINARY_BYTE_MARSHALLER);

  private GrpcMetadata() {}
}
