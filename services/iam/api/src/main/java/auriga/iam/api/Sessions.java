package auriga.iam.api;

import auriga.contracts.iam.v1.*;

/** Same generated messages for local calls and gRPC. Tenant must be bound by the caller. */
public interface Sessions {
  IntrospectSessionResponse introspect(IntrospectSessionRequest request);

  TouchSessionResponse touch(TouchSessionRequest request);

  RevokeSessionsResponse revoke(RevokeSessionsRequest request);
}
