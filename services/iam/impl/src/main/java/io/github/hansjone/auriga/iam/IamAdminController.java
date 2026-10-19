package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.common.security.*;
import io.github.hansjone.auriga.iam.web.model.ProviderInput;
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
                            : c instanceof EmergencyConnector ? "builtin" : "plugin"))
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
  public ObjectNode create(@RequestBody ProviderInput input) {
    return providers.create(Json.MAPPER.valueToTree(input));
  }

  @DeleteMapping("/providers/{id}")
  @RequiresPermission("iam.providers.write")
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void delete(@PathVariable String id) {
    store.tx(
        () -> {
          var p = store.get("idp_provider", id, true);
          IamError.require(p != null, "not_found", 404);
          IamError.require(!p.path("builtin").asBoolean(), "builtin_provider", 409);
          p.put("enabled", false);
          store.save("idp_provider", p);
          return null;
        });
  }
}
