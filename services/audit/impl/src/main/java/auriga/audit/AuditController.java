package auriga.audit;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AdminOnly;
import auriga.common.security.RequiresPermission;
import java.time.Instant;
import java.util.UUID;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;

@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.audit.enabled:true} && '${auriga.audit.jdbc-url:}' != ''")
@RestController
@RequestMapping("/api/audit/v1/admin/events")
@AdminOnly
@RequiresPermission("audit.events.read")
public class AuditController {
  private final AuditStore store;

  public AuditController(AuditStore store) {
    this.store = store;
  }

  @GetMapping
  public AuditStore.Page list(
      @RequestParam(required = false) Instant from,
      @RequestParam(required = false) Instant to,
      @RequestParam(defaultValue = "") String actor,
      @RequestParam(defaultValue = "") String action,
      @RequestParam(defaultValue = "") String result,
      @RequestParam(defaultValue = "") String q,
      @RequestParam(required = false) String cursor,
      @RequestParam(defaultValue = "50") int limit) {
    return query(from, to, actor, action, result, q, cursor, limit, false);
  }

  @GetMapping("/{id}")
  public JsonNode detail(@PathVariable UUID id) {
    var item = store.detail(id);
    if (item == null) throw new AurigaException(CommonErrors.NOT_FOUND);
    return item;
  }

  @GetMapping("/export")
  public ResponseEntity<java.util.Map<String, String>> export(
      @RequestParam(required = false) Instant from,
      @RequestParam(required = false) Instant to,
      @RequestParam(defaultValue = "") String actor,
      @RequestParam(defaultValue = "") String action,
      @RequestParam(defaultValue = "") String result,
      @RequestParam(defaultValue = "") String q) {
    var page = query(from, to, actor, action, result, q, null, 1000, true);
    var csv = new StringBuilder("\ufefftime,actor,action,target,result,ip,traceId\r\n");
    for (var row : page.items()) {
      csv.append(
              String.join(
                  ",",
                  cell(row.path("time").asText()),
                  cell(row.path("actor").path("id").asText()),
                  cell(row.path("action").asText()),
                  cell(row.path("target").path("id").asText()),
                  cell(row.path("result").asText()),
                  cell(row.path("actor").path("ip").asText()),
                  cell(row.path("traceId").asText())))
          .append("\r\n");
    }
    return ResponseEntity.ok()
        .header("Cache-Control", "no-store")
        .body(java.util.Map.of("csv", csv.toString()));
  }

  private AuditStore.Page query(
      Instant from,
      Instant to,
      String actor,
      String action,
      String result,
      String q,
      String cursor,
      int limit,
      boolean export) {
    try {
      return store.query(from, to, actor, action, result, q, cursor, limit, export);
    } catch (IllegalArgumentException ex) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }

  static String cell(String value) {
    var leading = value.stripLeading();
    if ((!leading.isEmpty() && "=+@-".indexOf(leading.charAt(0)) >= 0)
        || value.startsWith("\t")
        || value.startsWith("\r")
        || value.startsWith("\n")) value = "'" + value;
    return "\"" + value.replace("\"", "\"\"") + "\"";
  }
}
