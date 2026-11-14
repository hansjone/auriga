package auriga.audit.api;

import java.time.Instant;
import java.util.List;
import java.util.UUID;

/** Tenant-bound diagnostic projection; no arbitrary query or cross-tenant mode. */
public interface UacAuditEvents {
  record Page(List<String> events, boolean truncated) {}

  Page readUac(Instant from, Instant to, UUID provider, UUID correlation, int limit);
}
