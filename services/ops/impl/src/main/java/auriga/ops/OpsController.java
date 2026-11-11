package auriga.ops;

import auriga.common.security.*;
import java.util.UUID;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.ops.enabled:true} && ('${auriga.service.schema:}' == 'ops' || (${auriga.platform.combined:false}))")
@RequestMapping("/api/ops/v1/admin")
@AdminOnly
public class OpsController {
  private final OpsStore ops;

  public OpsController(OpsStore ops) {
    this.ops = ops;
  }

  @GetMapping("/alerts")
  @RequiresPermission("ops.alerts.read")
  public ObjectNode alerts() {
    return ops.alerts();
  }

  @GetMapping("/alerts/{id}/deliveries")
  @RequiresPermission("ops.alerts.read")
  public ObjectNode deliveries(@PathVariable UUID id) {
    return ops.deliveries(id);
  }

  @PostMapping("/alerts/{id}/{action}")
  @RequiresPermission("ops.alerts.write")
  public void action(
      @PathVariable UUID id,
      @PathVariable String action,
      @RequestParam(defaultValue = "60") int minutes) {
    ops.action(id, action, minutes);
  }

  @GetMapping("/notification-channels")
  @RequiresPermission("ops.notifications.read")
  public ObjectNode channels() {
    return ops.channels();
  }

  @PostMapping("/notification-channels")
  @RequiresPermission("ops.notifications.write")
  @RequiresRecentStepUp
  public ObjectNode create(@RequestBody ObjectNode body) {
    UUID id = auriga.common.persistence.UuidV7.next();
    save(id, body);
    return tools.jackson.databind.json.JsonMapper.builder()
        .build()
        .createObjectNode()
        .put("id", id.toString());
  }

  @PutMapping("/notification-channels/{id}")
  @RequiresPermission("ops.notifications.write")
  @RequiresRecentStepUp
  public void save(@PathVariable UUID id, @RequestBody ObjectNode body) {
    OpsError.require(body.path("enabled").isBoolean());
    ops.save(
        id,
        body.path("name").asText(),
        body.path("url").asText(),
        body.path("bearer").asText(""),
        body.path("enabled").asBoolean(),
        body.path("minSeverity").asText());
  }

  @DeleteMapping("/notification-channels/{id}")
  @RequiresPermission("ops.notifications.write")
  @RequiresRecentStepUp
  public void delete(@PathVariable UUID id) {
    ops.delete(id);
  }

  @PostMapping("/notification-channels/{id}/test")
  @RequiresPermission("ops.notifications.write")
  @RequiresRecentStepUp
  public void test(@PathVariable UUID id) {
    ops.test(id);
  }
}
