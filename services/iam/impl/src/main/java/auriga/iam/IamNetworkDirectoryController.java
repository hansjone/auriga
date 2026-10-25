package auriga.iam;

import auriga.common.security.AuthenticatedOnly;
import auriga.iam.api.NetworkDirectory;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

@RestController
@ConditionalOnExpression("${auriga.services.iam.enabled:true}")
@ConditionalOnProperty(prefix = "auriga.iam", name = "issuer")
@RequestMapping("/api/iam/v1/network")
public class IamNetworkDirectoryController {
  private final NetworkDirectory directory;

  public IamNetworkDirectoryController(NetworkDirectory directory) {
    this.directory = directory;
  }

  @GetMapping("/systems")
  @AuthenticatedOnly
  public ObjectNode systems(
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(defaultValue = "") String cursor) {
    var page = directory.list(limit, cursor);
    var response = Json.object();
    var items = response.putArray("items");
    for (var item : page.getItemsList())
      items.add(
          Json.object(
              "systemAddress",
              item.getSystemAddress(),
              "displayName",
              item.getDisplayName(),
              "ownerUserId",
              item.getOwnerUserId(),
              "ownerDisplayName",
              item.getOwnerDisplayName()));
    if (!page.getNextCursor().isEmpty()) response.put("nextCursor", page.getNextCursor());
    return response;
  }
}
