package auriga.collab.network.keys;

/**
 * J2/J3 transport seam. Broadcast a complete root-signed release to authenticated realm routes.
 * Return only after durable acceptance for fanout; duplicates are expected. Never use a business
 * signature as the root proof. The realm comes from installation, never model/request parameters.
 */
@FunctionalInterface
public interface NetworkKeyPush {
  void broadcast(String realm, String signedKeyset, String announcement);
}
