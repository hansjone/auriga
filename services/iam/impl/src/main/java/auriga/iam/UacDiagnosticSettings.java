package auriga.iam;

import auriga.common.audit.*;
import auriga.common.error.*;
import java.time.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Independent revision: diagnostics never changes the provider authentication generation. */
final class UacDiagnosticSettings {
  private final IamStore store;
  private final UacProviderService providers;
  private final AuditLogger audit;
  private final Clock clock;

  UacDiagnosticSettings(
      IamStore store, UacProviderService providers, AuditLogger audit, Clock clock) {
    this.store = store;
    this.providers = providers;
    this.audit = audit;
    this.clock = clock;
  }

  private ObjectNode row(String provider, boolean lock) {
    providers.row(provider, false);
    var row = store.get("uac_diagnostics", "settings:" + provider, lock);
    return row == null ? Json.object("enabled", false, "revision", 0) : row;
  }

  ObjectNode get(String provider) {
    return store.tx(() -> view(row(provider, false)));
  }

  static String etag(JsonNode setting) {
    return "\"uac-diagnostics-" + setting.path("revision").asLong() + "\"";
  }

  private ObjectNode view(ObjectNode row) {
    boolean enabled =
        row.path("enabled").asBoolean()
            && row.has("expiresAt")
            && clock.instant().isBefore(Instant.parse(row.path("expiresAt").asString()));
    var result = Json.object("enabled", enabled, "revision", row.path("revision").asLong());
    if (row.has("expiresAt")) result.set("expiresAt", row.path("expiresAt"));
    return result;
  }

  ObjectNode put(String provider, JsonNode input, String match) {
    new ContractSchemas()
        .validate("auth/uac-control.schema.json#/$defs/UacDiagnosticSettingsWrite", input);
    return store.tx(
        () -> {
          // Same order as provider disable; no diagnostics -> provider lock inversion.
          providers.row(provider, true);
          store.lock("uac-diagnostics:" + provider);
          var row = row(provider, true);
          if (!etag(row).equals(match)) throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
          row.put("kind", "settings");
          row.put("providerId", provider);
          row.put("enabled", input.path("enabled").asBoolean());
          row.put("revision", Math.addExact(row.path("revision").asLong(), 1));
          if (row.path("revision").asLong() > 9007199254740991L)
            throw new AurigaException(CommonErrors.CONFLICT);
          if (input.path("enabled").asBoolean())
            row.put(
                "expiresAt",
                clock.instant().plusSeconds(input.path("ttlSeconds").asInt(900)).toString());
          else row.remove("expiresAt");
          if (row.has("id")) store.save("uac_diagnostics", row);
          else store.insert("uac_diagnostics", "settings:" + provider, row, null);
          IamAudit.record(
              audit,
              IamAudit.entry("iam.provider.update", "provider", provider, null)
                  .detail(
                      "fields",
                      java.util.List.of("uac.diagnostics.enabled", "uac.diagnostics.revision"))
                  .detail("enabled", input.path("enabled").asBoolean())
                  .build());
          return view(row);
        });
  }

  /** Capture before sending; the same revision is checked again before writing detail. */
  long capture(String provider) {
    try {
      return store.tx(
          () -> {
            var p = providers.row(provider, false);
            var row = row(provider, false);
            return p.path("enabled").asBoolean() && view(row).path("enabled").asBoolean()
                ? row.path("revision").asLong()
                : -1L;
          });
    } catch (RuntimeException e) {
      return -1;
    }
  }

  boolean permits(String provider, long revision) {
    return revision >= 0 && capture(provider) == revision;
  }
}
