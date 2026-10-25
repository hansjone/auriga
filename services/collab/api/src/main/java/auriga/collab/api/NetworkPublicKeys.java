package auriga.collab.api;

/**
 * Public verification material only. The authenticated configuration endpoint must root-sign it.
 */
@FunctionalInterface
public interface NetworkPublicKeys {
  /** Includes active/next/retired public keys and explicit revocation state, never private keys. */
  String publicKeysJson(String realm);
}
