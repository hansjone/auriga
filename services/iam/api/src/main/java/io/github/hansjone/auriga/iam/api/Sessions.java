package io.github.hansjone.auriga.iam.api;

import io.github.hansjone.auriga.contracts.iam.v1.*;

/** Same generated messages for local calls and gRPC. Tenant must be bound by the caller. */
public interface Sessions {
  IntrospectSessionResponse introspect(IntrospectSessionRequest request);

  TouchSessionResponse touch(TouchSessionRequest request);

  RevokeSessionsResponse revoke(RevokeSessionsRequest request);
}
