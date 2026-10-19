package io.github.hansjone.auriga.common.grpc;

/**
 * Supplies this service's own service token (an iam access token with {@code ctx=service}, obtained
 * by client credentials). The implementation belongs to iam integration; common only attaches it to
 * outgoing calls.
 */
@FunctionalInterface
public interface ServiceTokenProvider {
  String token();
}
