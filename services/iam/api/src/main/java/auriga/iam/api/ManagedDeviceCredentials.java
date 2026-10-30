package auriga.iam.api;

import auriga.contracts.iam.v1.*;

/** Trusted workspace adapter; transport authenticates the service separately. */
public interface ManagedDeviceCredentials {
  IssueManagedDeviceResponse issue(IssueManagedDeviceRequest request);

  void revoke(RevokeManagedDeviceRequest request);
}
