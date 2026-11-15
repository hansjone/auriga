package auriga.iam.api;

/**
 * W calls this port under the authenticated caller and tenant context, before changing state. HTTP
 * callers must additionally enforce trusted entry, Origin and CSRF at their boundary. This is a
 * local port; remote deployments use the existing IAM session/permission RPCs.
 */
public interface PlacementAuthorization {
  void requireRead();

  void requireWrite();
}
