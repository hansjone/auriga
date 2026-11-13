package auriga.iam;

import auriga.common.error.*;
import auriga.iam.api.UacEnterpriseAdapter;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.databind.*;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Private deployment mappings describe the enterprise protocol, never browser input. */
final class UacConfiguredAdapter implements UacEnterpriseAdapter {
  private static final JsonMapper JSON =
      JsonMapper.builder()
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
          .build();
  private final JsonNode settings;
  private final UacHttp http;

  UacConfiguredAdapter(JsonNode settings, boolean testLoopback) {
    String ip = settings.path("clientIP").asString("");
    if (settings.has("clientIP") && !settings.path("clientIP").isString())
      throw new IllegalArgumentException("Invalid private UAC client IP");
    literalIp(ip);
    this.settings = settings.deepCopy();
    this.http = new UacHttp(settings, testLoopback);
  }

  @Override
  public JsonNode descriptor() {
    return Json.object(
        "type",
        "uac",
        "spiVersion",
        "1",
        "version",
        "1",
        "displayName",
        Json.object("en", "Company account", "zh-CN", "公司账号"),
        "steps",
        List.of(),
        "capabilities",
        List.of("credential", "session_check"),
        "configSchema",
        Json.object(
            "type",
            "object",
            "required",
            List.of("baseUrl", "systemCode"),
            "properties",
            Json.object(
                "baseUrl",
                Json.object("type", "string", "format", "uri"),
                "systemCode",
                Json.object("type", "string", "minLength", 1, "maxLength", 128)),
            "additionalProperties",
            false));
  }

  @Override
  public void validateConfig(JsonNode config) {
    IamError.validate(
        config.path("baseUrl").isString()
            && config.path("systemCode").isString()
            && !config.path("systemCode").asString().isBlank()
            && config.path("systemCode").asString().length() <= 128);
    try {
      http.validate(URI.create(config.path("baseUrl").asString()), true);
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
    IamError.validate(
        !settings.path("qr").path("path").asString().isBlank()
            && !settings.path("department").path("url").asString().isBlank()
            && !settings.path("tokenVerifyUrl").asString().isBlank());
    try {
      http.validate(URI.create(settings.path("tokenVerifyUrl").asString()), false);
      http.validate(URI.create(settings.path("department").path("url").asString()), false);
      http.validate(
          URI.create(
              config.path("baseUrl").asString() + settings.path("qr").path("path").asString()),
          false);
      ObjectNode coordinates = Json.object("x", "0".repeat(32), "y", "0".repeat(32));
      expand(settings.path("qr").path("body"), values(config, coordinates, settings.path("qr")));
      qrContent(config, coordinates);
      expand(
          settings.path("department").path("body"),
          values(
              config,
              Json.object(
                  "token",
                  "validation",
                  "empNo",
                  "validation",
                  "clientIP",
                  "",
                  "purpose",
                  "login_info"),
              settings.path("department")));
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }

  @Override
  public JsonNode begin(JsonNode context, JsonNode config) {
    throw new AurigaException(CommonErrors.UNAVAILABLE);
  }

  @Override
  public JsonNode advance(JsonNode context, JsonNode config, JsonNode input) {
    throw new AurigaException(CommonErrors.UNAVAILABLE);
  }

  private static void literalIp(String value) {
    if (value.isEmpty()) return;
    if (value.length() > 64 || !value.matches("[0-9a-fA-F:.]+"))
      throw new IllegalArgumentException("Invalid private UAC client IP");
    InetAddress.ofLiteral(value);
  }

  private ObjectNode values(JsonNode config, JsonNode input, JsonNode operation) {
    ObjectNode values = input.isObject() ? ((ObjectNode) input).deepCopy() : Json.object();
    values.put("systemCode", config.path("systemCode").asString());
    if (!values.has("clientIP")) values.put("clientIP", settings.path("clientIP").asString(""));
    if (settings.has("constants")) {
      IamError.validate(
          settings.path("constants").isObject() && settings.path("constants").size() <= 32);
      for (var constant : settings.path("constants").properties()) {
        privateVariable(constant.getKey());
        IamError.validate(
            constant.getValue().isString() && constant.getValue().asString().length() <= 16384);
        values.set(constant.getKey(), constant.getValue());
      }
    }
    if (operation.has("derived")) {
      IamError.validate(
          operation.path("derived").isObject() && operation.path("derived").size() <= 16);
      for (var field : operation.path("derived").properties()) {
        privateVariable(field.getKey());
        JsonNode definition = field.getValue();
        IamError.validate(
            definition.isObject()
                && definition.path("template").isString()
                && Set.of("md5", "sha256").contains(definition.path("algorithm").asString()));
        String raw = render(definition.path("template").asString(), values);
        try {
          values.put(
              field.getKey(),
              HexFormat.of()
                  .formatHex(
                      java.security.MessageDigest.getInstance(
                              definition.path("algorithm").asString().equals("md5")
                                  ? "MD5"
                                  : "SHA-256")
                          .digest(raw.getBytes(StandardCharsets.UTF_8))));
        } catch (java.security.NoSuchAlgorithmException e) {
          throw new IllegalStateException("Required protocol digest unavailable");
        }
      }
    }
    return values;
  }

  private static void privateVariable(String name) {
    IamError.validate(
        name.matches("[A-Za-z][A-Za-z0-9]{0,63}")
            && !Set.of("x", "y", "systemCode", "token", "empNo", "clientIP", "purpose")
                .contains(name));
  }

  private static String render(String template, JsonNode values) {
    IamError.validate(template.length() <= 48 * 1024);
    var matcher =
        java.util.regex.Pattern.compile("\\$\\{([A-Za-z][A-Za-z0-9]{0,63})}").matcher(template);
    StringBuilder output = new StringBuilder();
    while (matcher.find()) {
      JsonNode value = values.path(matcher.group(1));
      IamError.validate(value.isString());
      matcher.appendReplacement(output, java.util.regex.Matcher.quoteReplacement(value.asString()));
    }
    matcher.appendTail(output);
    IamError.validate(output.length() <= 48 * 1024 && !output.toString().contains("${"));
    return output.toString();
  }

  private JsonNode expand(JsonNode template, JsonNode values) {
    if (template.isString() && template.asString().contains("${"))
      return JSON.valueToTree(render(template.asString(), values));
    if (template.isObject()) {
      ObjectNode output = Json.object();
      template.properties().forEach(e -> output.set(e.getKey(), expand(e.getValue(), values)));
      return output;
    }
    if (template.isArray()) {
      var output = JSON.createArrayNode();
      template.forEach(e -> output.add(expand(e, values)));
      return output;
    }
    return template;
  }

  @Override
  public String qrContent(JsonNode config, JsonNode coordinates) {
    String template = settings.path("qr").path("contentTemplate").asString();
    for (var entry : values(config, coordinates, settings.path("qr")).properties())
      template =
          template.replace(
              "${" + entry.getKey() + "}",
              URLEncoder.encode(entry.getValue().asString(), StandardCharsets.UTF_8));
    IamError.validate(!template.isBlank() && template.length() <= 2048 && !template.contains("${"));
    return template;
  }

  @Override
  public JsonNode scan(JsonNode config, JsonNode coordinates) {
    JsonNode mapping = settings.path("qr");
    try {
      URI target = URI.create(config.path("baseUrl").asString() + mapping.path("path").asString());
      var response =
          http.post(
              target,
              expand(mapping.path("body"), values(config, coordinates, mapping)),
              headers(mapping, values(config, coordinates, mapping)),
              25);
      UacTelemetry.capture(response.status(), response.body());
      if (response.status() < 200 || response.status() >= 300)
        return failure("unavailable", "transport_error");
      JsonNode root = JSON.readTree(response.body());
      String state = root.at(mapping.path("statusPointer").asString()).asString();
      for (String pending : List.of("waiting", "scanned"))
        if (mapping.path(pending).isString() && state.equals(mapping.path(pending).asString()))
          return Json.object("state", pending);
      if (!state.equals(mapping.path("success").asString()))
        return failure("denied", "upstream_rejected");
      JsonNode token = root.at(mapping.path("tokenPointer").asString()),
          empNo = root.at(mapping.path("empNoPointer").asString());
      if (!token.isString() || token.asString().isBlank() || token.asString().length() > 16384)
        return failure("failed", "missing_token");
      if (!empNo.isString()
          || empNo.asString().strip().isEmpty()
          || empNo.asString().strip().length() > 128) return failure("failed", "missing_emp_no");
      return Json.object(
          "state",
          "verified",
          "token",
          token.asString(),
          "empNo",
          empNo.asString().strip(),
          "clientIP",
          settings.path("clientIP").asString(""));
    } catch (java.io.IOException e) {
      return failure("unavailable", "transport_error");
    } catch (RuntimeException e) {
      return failure("failed", "invalid_response");
    }
  }

  @Override
  public JsonNode department(JsonNode config, JsonNode credential, String purpose) {
    IamError.validate(Set.of("login_info", "revalidate").contains(purpose));
    JsonNode mapping = settings.path("department");
    ObjectNode context = ((ObjectNode) credential).deepCopy();
    context.put("purpose", purpose);
    ObjectNode values = values(config, context, mapping);
    try {
      var response =
          http.post(
              URI.create(mapping.path("url").asString()),
              expand(mapping.path("body"), values),
              headers(mapping, values),
              3);
      UacTelemetry.capture(response.status(), response.body());
      if (response.status() < 200 || response.status() >= 300)
        return Json.object("available", false, "faultReason", "http_error");
      JsonNode root = JSON.readTree(response.body());
      if (!root.at(mapping.path("successPointer").asString()).equals(mapping.path("successValue")))
        return Json.object("available", false, "failureClass", "business_rejected");
      JsonNode list = root.at(mapping.path("listPointer").asString());
      if (!list.isArray())
        return Json.object("available", false, "faultReason", "invalid_success_payload");
      List<JsonNode> matches = new ArrayList<>();
      for (JsonNode entry : list) {
        JsonNode id = entry.at(mapping.path("empNoPointer").asString());
        if (id.isString()
            && auriga.iam.api.UacEmployee.canonical(id.asString())
                .equals(auriga.iam.api.UacEmployee.canonical(credential.path("empNo").asString())))
          matches.add(entry);
      }
      if (matches.size() != 1)
        return Json.object(
            "available",
            false,
            "failureClass",
            "identity_mismatch",
            "matchingCount",
            matches.size());
      int branch = 0;
      for (JsonNode pointer : mapping.path("departmentPointers")) {
        branch++;
        JsonNode department = matches.getFirst().at(pointer.asString());
        if (department.isString()
            && !department.asString().strip().isEmpty()
            && department.asString().length() <= 1024)
          return Json.object(
              "available",
              true,
              "department",
              department.asString().strip(),
              "matchingCount",
              1,
              "fieldBranch",
              branch == 1 ? "primary" : "fallback");
      }
      return Json.object(
          "available", false, "faultReason", "missing_department", "matchingCount", 1);
    } catch (java.io.IOException e) {
      return Json.object("available", false, "faultReason", "transport_error");
    } catch (RuntimeException e) {
      return Json.object("available", false, "faultReason", "invalid_json");
    }
  }

  @Override
  public JsonNode verify(JsonNode config, JsonNode credential) {
    try {
      String account = credential.path("empNo").asString();
      var response =
          http.post(
              URI.create(settings.path("tokenVerifyUrl").asString()),
              UacTokenVerification.request(
                  account,
                  credential.path("token").asString(),
                  credential.path("clientIP").asString(""),
                  config.path("systemCode").asString()),
              Map.of("X-Emp-No", account),
              3);
      UacTelemetry.capture(response.status(), response.body());
      var decision = UacTokenVerification.evaluate(response.status(), response.body(), account);
      return Json.object(
          "success",
          decision.success(),
          "failureClass",
          decision.failureClass(),
          "faultReason",
          decision.faultReason(),
          "identityComparisonVersion",
          "strip-ascii-fold-v1",
          "identityMatched",
          decision.success()
              ? Boolean.TRUE
              : "identity_mismatch".equals(decision.failureClass()) ? Boolean.FALSE : null,
          "token",
          decision.replacementToken());
    } catch (java.io.IOException e) {
      return Json.object(
          "success", false, "failureClass", "upstream_fault", "faultReason", "transport_error");
    }
  }

  private Map<String, String> headers(JsonNode mapping, JsonNode values) {
    Map<String, String> headers = new HashMap<>();
    mapping
        .path("headers")
        .properties()
        .forEach(e -> headers.put(e.getKey(), expand(e.getValue(), values).asString()));
    return headers;
  }

  private static ObjectNode failure(String state, String code) {
    return Json.object("state", state, "failureCode", code);
  }

  @Override
  public JsonNode ssoCookies(JsonNode cookies) {
    try {
      return selectCookies(cookies);
    } catch (IllegalArgumentException error) {
      return Json.object();
    }
  }

  @Override
  public Boolean ssoCookiePresent(JsonNode cookies) {
    for (String family : List.of("portal", "uds"))
      for (String role : List.of("token", "account"))
        if (cookies.has(settings.path("cookies").path(family).path(role).path("cookie").asString()))
          return true;
    return false;
  }

  @Override
  public Map<String, Boolean> diagnosticCapabilities() {
    Map<String, Boolean> result = new LinkedHashMap<>();
    for (String family : List.of("portal", "uds")) {
      JsonNode roles = settings.path("cookies").path(family);
      result.put(
          family + "CookieConfigured",
          !roles.path("token").path("cookie").asString().isBlank()
              && !roles.path("account").path("cookie").asString().isBlank());
    }
    JsonNode empty = settings.path("diagnostics").path("originSystemCodeEmpty");
    if (empty.isBoolean()) result.put("originSystemCodeEmpty", empty.asBoolean());
    return Map.copyOf(result);
  }

  private JsonNode selectCookies(JsonNode cookies) {
    for (String source : List.of("portal", "uds"))
      for (var role : settings.path("cookies").path(source).properties()) {
        JsonNode values = cookies.path(role.getValue().path("cookie").asString());
        if (!values.isMissingNode() && (!values.isArray() || values.size() != 1))
          return Json.object();
      }
    for (String system : List.of("portal", "uds")) {
      JsonNode roles = settings.path("cookies").path(system);
      String token = cookieValue(cookies, roles.path("token")),
          account = cookieValue(cookies, roles.path("account"));
      if (token != null && (token.isBlank() || token.length() > 16384)
          || account != null && (account.strip().isEmpty() || account.strip().length() > 128))
        return Json.object();
      if (token == null || account == null) continue;
      if (token.isBlank()
          || token.length() > 16384
          || account.strip().isEmpty()
          || account.strip().length() > 128) return Json.object();
      String ip = cookieValue(cookies, settings.path("cookies").path("uds").path("clientIP"));
      if (ip == null) ip = settings.path("clientIP").asString("");
      literalIp(ip);
      return Json.object(
          "empNo", account.strip(), "token", token, "clientIP", ip, "cookieFamily", system);
    }
    return Json.object();
  }

  private String cookieValue(JsonNode cookies, JsonNode role) {
    JsonNode entries = cookies.path(role.path("cookie").asString());
    if (entries.isMissingNode()) return null;
    if (!entries.isArray() || entries.size() != 1 || !entries.get(0).isString())
      throw new IllegalArgumentException("Invalid SSO Cookie");
    try {
      String raw = entries.get(0).asString();
      if (raw.length() > 32768) throw new IllegalArgumentException("Invalid SSO Cookie");
      if (role.path("urlEncoded").asBoolean()) raw = URLDecoder.decode(raw, StandardCharsets.UTF_8);
      if (role.has("pointer")) {
        JsonNode value = JSON.readTree(raw).at(role.path("pointer").asString());
        if (value.isMissingNode() || value.isNull()) return null;
        if (!value.isString()) throw new IllegalArgumentException("Invalid SSO Cookie");
        return value.asString();
      }
      return raw;
    } catch (RuntimeException e) {
      throw new IllegalArgumentException("Invalid SSO Cookie");
    }
  }
}
