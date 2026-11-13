package auriga.iam;

import auriga.common.security.*;
import auriga.iam.web.model.ProviderInput;
import java.util.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
@RequestMapping("/api/iam/v1/admin")
@AdminOnly
public class IamAdminController {
  private final ProviderManager providers;
  private final ConnectorRegistry registry;
  private final IamStore store;

  public IamAdminController(ProviderManager providers, ConnectorRegistry registry, IamStore store) {
    this.providers = providers;
    this.registry = registry;
    this.store = store;
  }

  @GetMapping("/connectors")
  @RequiresPermission("iam.providers.read")
  public ObjectNode types() {
    return Json.object(
        "items",
        registry.all().stream()
            .map(
                c ->
                    Json.object(
                        "descriptor",
                        c.descriptor(),
                        "source",
                        c instanceof RemoteConnector
                            ? "remote"
                            : c instanceof EmergencyConnector
                                    || c instanceof TeamAccountConnector
                                    || c instanceof OidcConnector
                                ? "builtin"
                                : "plugin"))
            .toList());
  }

  @GetMapping("/providers")
  @RequiresPermission("iam.providers.read")
  public ObjectNode list() {
    return store.tx(
        () ->
            Json.object("items", store.all("idp_provider").stream().map(providers::view).toList()));
  }

  @PostMapping("/providers")
  @RequiresPermission("iam.providers.write")
  @ResponseStatus(org.springframework.http.HttpStatus.CREATED)
  public ObjectNode create(@RequestBody tools.jackson.databind.JsonNode input) {
    IamError.validate(input.isObject());
    IamError.validate(!input.has("enabled") || input.path("enabled").isBoolean());
    ProviderInput parsed;
    try {
      parsed = Json.MAPPER.treeToValue(input, ProviderInput.class);
    } catch (tools.jackson.core.JacksonException e) {
      throw new auriga.common.error.AurigaException(
          auriga.common.error.CommonErrors.VALIDATION_FAILED);
    }
    ObjectNode normalized = Json.MAPPER.valueToTree(parsed);
    // The generated generic model defaults enabled=true. UAC must retain field absence.
    if (!input.has("enabled")) normalized.remove("enabled");
    return providers.create(normalized);
  }

  @GetMapping("/providers/{id}")
  @RequiresPermission("iam.providers.read")
  public ObjectNode get(
      @PathVariable String id, jakarta.servlet.http.HttpServletResponse response) {
    return store.tx(
        () -> {
          ObjectNode result = providers.get(id);
          response.setHeader("ETag", UacProviderService.etag(store.get("idp_provider", id, false)));
          return result;
        });
  }

  public ObjectNode get(String id) {
    return providers.get(id);
  }

  @PatchMapping(path = "/providers/{id}", consumes = "application/merge-patch+json")
  @RequiresPermission("iam.providers.write")
  public ObjectNode update(
      @PathVariable String id,
      @RequestBody tools.jackson.databind.JsonNode patch,
      @RequestHeader(value = "If-Match", required = false) String match,
      jakarta.servlet.http.HttpServletResponse response) {
    return store.tx(
        () -> {
          ObjectNode row = store.get("idp_provider", id, true);
          IamError.require(row != null, "not_found", 404);
          if ("uac".equals(row.path("connectorType").asString())
              && !patch.has("enabled")
              && !patch.has("config")) UacProviderService.match(row, match);
          ObjectNode result = providers.update(id, patch);
          response.setHeader("ETag", UacProviderService.etag(store.get("idp_provider", id, false)));
          return result;
        });
  }

  public ObjectNode update(String id, tools.jackson.databind.JsonNode patch) {
    return providers.update(id, patch);
  }

  @DeleteMapping("/providers/{id}")
  @RequiresPermission("iam.providers.write")
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void delete(@PathVariable String id) {
    providers.delete(id);
  }
}
