package auriga.resource.api;

import auriga.contracts.resource.v1.ResolveUnifiedResourcesRequest;
import auriga.contracts.resource.v1.ResolveUnifiedResourcesResponse;

/** Resource-owned resolver. Same selection revision always resolves the same immutable bytes. */
public interface UnifiedResourceResolution {
  ResolveUnifiedResourcesResponse resolve(ResolveUnifiedResourcesRequest request);
}
