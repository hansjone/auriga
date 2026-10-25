package auriga.collab.api;

import java.util.UUID;

/** Collab-owned opaque registration lookup. Never query IAM or expose native session mappings. */
public interface NetworkLocalTargets {
  Registration lookup(UUID tenant, String realm, String node, String local);

  record Registration(String realm, String node, String local, String type, String revision) {}
}
