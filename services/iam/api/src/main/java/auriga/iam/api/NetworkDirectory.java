package auriga.iam.api;

import auriga.contracts.iam.v1.ListNetworkSystemsResponse;
import auriga.contracts.iam.v1.ResolveNetworkNodeResponse;

/** Tenant-bound registration directory. It never indexes or resolves session locals. */
public interface NetworkDirectory {
  /** Authenticated user/device only; current organization policies are rechecked on every page. */
  ListNetworkSystemsResponse list(int limit, String cursor);

  /** Exact svc:collab only. Offline DSH registration is distinct from a live connection. */
  ResolveNetworkNodeResponse resolve(String realm, String node);
}
