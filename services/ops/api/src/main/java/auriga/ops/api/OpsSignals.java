package auriga.ops.api;

import java.util.UUID;

/** Trusted service integration, never exposed as an admin HTTP ingestion endpoint. */
public interface OpsSignals {
  void observe(UUID tenant, String source, String subject, String severity, boolean active);
}
