package auriga.iam;

import auriga.iam.api.IdentityConnector;
import java.util.List;
import tools.jackson.databind.JsonNode;

/** Credential verification is a core capability, so connectors never access persistence. */
public final class EmergencyConnector implements IdentityConnector {
  @Override
  public JsonNode descriptor() {
    return Json.object(
        "type",
        "emergency",
        "spiVersion",
        "1",
        "displayName",
        Json.object("en", "Emergency account", "zh-CN", "应急账号"),
        "steps",
        List.of("form"),
        "configSchema",
        Json.object("type", "object", "additionalProperties", false),
        "multipleInstances",
        false);
  }

  @Override
  public void validateConfig(JsonNode config) {}

  @Override
  public JsonNode begin(JsonNode context, JsonNode config) {
    return Json.object(
        "step",
        Json.object(
            "type",
            "form",
            "formId",
            "emergency",
            "fields",
            List.of(
                Json.object("name", "username", "kind", "text", "required", true),
                Json.object("name", "password", "kind", "password", "required", true),
                Json.object("name", "otp", "kind", "otp", "required", true))));
  }

  @Override
  public JsonNode advance(JsonNode context, JsonNode config, JsonNode input) {
    // The core consumes this form and substitutes a verified profile before admission.
    return Json.object("step", Json.object("type", "fail", "code", "iam.invalid_credentials"));
  }
}
