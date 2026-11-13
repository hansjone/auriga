package auriga.ops.api;

import java.time.Instant;
import java.util.List;
import java.util.UUID;

/** Safe tenant/provider projection only, never notification targets or credentials. */
public interface UacOpsAlerts {
  record Page(List<String> alerts, boolean truncated) {}

  Page readUac(Instant from, Instant to, UUID provider, int limit);
}
