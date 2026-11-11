package auriga.iam;

import auriga.common.security.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
@RequestMapping("/api/iam/v1/admin/devices")
@AdminOnly
public class IamAdminDevicesController {
  private final IamAdminDevices devices;

  public IamAdminDevicesController(IamAdminDevices devices) {
    this.devices = devices;
  }

  @GetMapping
  @RequiresPermission("iam.network.manage")
  public ObjectNode list(
      @RequestParam(required = false) String member,
      @RequestParam(required = false) String online) {
    return devices.list(member, online);
  }

  @GetMapping("/{deviceId}")
  @RequiresPermission("iam.network.manage")
  public ObjectNode get(@PathVariable String deviceId) {
    return devices.get(deviceId);
  }

  @GetMapping("/connection-bindings")
  @RequiresPermission("iam.network.manage")
  public ObjectNode connectionBindings() {
    return devices.connectionBindings();
  }

  @DeleteMapping("/{deviceId}")
  @RequiresPermission("iam.network.manage")
  @RequiresRecentStepUp
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void revoke(@PathVariable String deviceId) {
    devices.revoke(deviceId);
  }
}
