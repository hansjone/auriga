package auriga.collab.api;

import java.util.UUID;

/** Collab-owned opaque registration lookup. Never query IAM or expose native session mappings. */
public interface NetworkLocalTargets {
  /**
   * Return only the current active, non-tombstoned registration in this business tenant. Outside a
   * transaction the adapter owns a short tenant-scoped read transaction.
   */
  Registration lookup(UUID tenant, String realm, String node, String local);

  /**
   * Same-datasource transaction: lock the opaque registration witness and return its current
   * revision. Missing integration must fail closed; a lookup-only implementation cannot authorize a
   * commit.
   */
  default Registration lock(UUID tenant, Registration expected) {
    return null;
  }

  record Registration(String realm, String node, String local, String type, String revision) {}
}
