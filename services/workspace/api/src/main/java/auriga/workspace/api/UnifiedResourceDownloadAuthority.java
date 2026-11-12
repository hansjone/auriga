package auriga.workspace.api;

import auriga.contracts.workspace.v1.CheckUnifiedResourceDownloadRequest;
import auriga.contracts.workspace.v1.CheckUnifiedResourceDownloadResponse;

/** Online authority for node scheduling, ownership and current unified resource selection. */
public interface UnifiedResourceDownloadAuthority {
  CheckUnifiedResourceDownloadResponse check(CheckUnifiedResourceDownloadRequest request);
}
