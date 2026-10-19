package auriga.common.license;

/**
 * The single place services ask whether a licensed feature or limit allows an action (platform
 * design section 11). The community default allows everything; the enterprise license module
 * replaces the bean with one that reads the signed license file.
 */
public interface LicenseCheckpoint {
  /**
   * @param feature feature key, e.g. {@code multi_tenant}, {@code saml}
   */
  boolean allows(String feature);

  /** Default implementation: no restrictions. */
  LicenseCheckpoint COMMUNITY = feature -> true;
}
