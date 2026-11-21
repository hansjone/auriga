package auriga.iam;

import auriga.common.error.*;
import auriga.contracts.iam.v1.*;
import io.grpc.Context;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.regex.Pattern;
import org.springframework.core.env.Environment;
import tools.jackson.core.StreamReadConstraints;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.core.json.JsonFactory;
import tools.jackson.databind.*;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Bounded enterprise delegation. Only sanitized business results leave IAM. */
final class SharedOutbound {
  static final int MAX_BODY = 1024 * 1024;
  private static final Set<String> METHODS =
      Set.of("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD");
  private static final Set<String> REQUEST_HEADERS =
      Set.of("content-type", "accept", "accept-language", "x-lang-id", "x-requested-with");
  private static final Set<String> RESPONSE_HEADERS = Set.of("content-type", "content-language");
  private static final Set<String> TRANSPORT_HEADERS =
      Set.of(
          "host",
          "content-length",
          "transfer-encoding",
          "connection",
          "proxy-authorization",
          "te",
          "trailer",
          "upgrade");
  private static final Pattern AUTH_FIELD =
      Pattern.compile(
          "(?i)token|cookie|authorization|emp_?no|auth_?value|password|secret|credential");
  private static final Pattern AUTH_TEXT =
      Pattern.compile(
          "(?i)((?:token|cookie|authorization|emp_?no|auth_?value|password|secret|credential)[\\w-]*[\\s\"':=]+)(?:(?:Bearer|Basic)\\s+[^\\s,;}]+|\"[^\"]*\"|'[^']*'|[^\\s,;}]+)");
  private static final JsonMapper JSON =
      JsonMapper.builder(
              JsonFactory.builder()
                  .streamReadConstraints(
                      StreamReadConstraints.builder()
                          .maxNestingDepth(64)
                          .maxStringLength(MAX_BODY)
                          .build())
                  .build())
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
          .build();
  private final SharedTasks tasks;
  private final UacHttp http;
  private final String employeeHeader, tokenHeader;

  static JsonNode settings(Environment env) {
    String file = env.getProperty("auriga.iam.uac.adapter-config-file");
    if (file == null) return Json.object().path("sharedOutbound");
    try (var input = Files.newInputStream(Path.of(file))) {
      byte[] bytes = input.readNBytes(UacTokenVerification.MAX_BODY + 1);
      if (bytes.length > UacTokenVerification.MAX_BODY) throw new IllegalArgumentException();
      JsonNode root = JSON.readTree(bytes);
      if (root == null || !root.isObject()) throw new IllegalArgumentException();
      return root.path("sharedOutbound");
    } catch (IOException | RuntimeException error) {
      throw new IllegalArgumentException("Invalid private shared outbound configuration");
    }
  }

  SharedOutbound(SharedTasks tasks, JsonNode settings, boolean testLoopback) {
    this.tasks = tasks;
    if (settings.isMissingNode()) {
      http = null;
      employeeHeader = null;
      tokenHeader = null;
      return;
    }
    try {
      if (!settings.isObject()
          || settings.size() != 3
          || !settings.path("allowedDestinations").isArray()
          || settings.path("allowedDestinations").isEmpty()
          || settings.path("allowedDestinations").size() > 64) throw new IllegalArgumentException();
      employeeHeader = headerName(settings.path("empNoHeader"));
      tokenHeader = headerName(settings.path("authValueHeader"));
      if (employeeHeader.equalsIgnoreCase(tokenHeader)) throw new IllegalArgumentException();
      var origins = new HashSet<String>();
      for (JsonNode target : settings.path("allowedDestinations")) {
        URI uri = URI.create(target.path("origin").asString());
        boolean local =
            testLoopback && "http".equals(uri.getScheme()) && OidcHttp.localHost(uri.getHost());
        if (target.size() != 2
            || uri.getHost() == null
            || !("https".equals(uri.getScheme()) || local)
            || uri.getRawUserInfo() != null
            || uri.getRawQuery() != null
            || uri.getRawFragment() != null
            || !(uri.getRawPath().isEmpty() || uri.getRawPath().equals("/"))
            || !target.path("addresses").isArray()
            || target.path("addresses").isEmpty()
            || target.path("addresses").size() > 32
            || !origins.add(
                uri.getScheme()
                    + "://"
                    + uri.getHost().toLowerCase(Locale.ROOT)
                    + ":"
                    + (uri.getPort() == -1
                        ? ("https".equals(uri.getScheme()) ? 443 : 80)
                        : uri.getPort()))) throw new IllegalArgumentException();
      }
      http = new UacHttp(settings, testLoopback);
    } catch (RuntimeException error) {
      throw new IllegalArgumentException("Invalid private shared outbound configuration");
    }
  }

  private static String headerName(JsonNode value) {
    if (!value.isString()
        || !value.asString().matches("[!#$%&'*+.^_`|~0-9A-Za-z-]{1,128}")
        || TRANSPORT_HEADERS.contains(value.asString().toLowerCase(Locale.ROOT)))
      throw new IllegalArgumentException();
    return value.asString();
  }

  ExecuteSharedOutboundResponse execute(ExecuteSharedOutboundRequest request) {
    if (http == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (!request.getUnknownFields().asMap().isEmpty()
        || !METHODS.contains(request.getMethod())
        || request.getTimeoutMs() < 1
        || request.getTimeoutMs() > 60000
        || request.getBody().size() > MAX_BODY
        || request.getHeadersCount() > 32
        || request.getUrl().length() > 8192)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    URI uri;
    try {
      uri = URI.create(request.getUrl());
      http.validate(uri, false);
    } catch (RuntimeException error) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
    Map<String, String> headers = new HashMap<>();
    for (var entry : request.getHeadersMap().entrySet()) {
      String name = entry.getKey().toLowerCase(Locale.ROOT), value = entry.getValue();
      if (!entry.getKey().matches("[!#$%&'*+.^_`|~0-9A-Za-z-]{1,128}")
          || value.length() > 8192
          || value.indexOf('\r') >= 0
          || value.indexOf('\n') >= 0) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
      if (REQUEST_HEADERS.contains(name)) {
        if (headers.putIfAbsent(name, value) != null)
          throw new AurigaException(CommonErrors.VALIDATION_FAILED);
      }
    }
    var capability = request.getCapability();
    var material = tasks.outboundMaterial(capability);
    String employee = material.path("empNo").asString(), token = material.path("token").asString();
    if (employee.isEmpty()
        || token.isEmpty()
        || employee.indexOf('\r') >= 0
        || employee.indexOf('\n') >= 0
        || token.indexOf('\r') >= 0
        || token.indexOf('\n') >= 0) throw new AurigaException(CommonErrors.UNAVAILABLE);
    headers.remove(employeeHeader.toLowerCase(Locale.ROOT));
    headers.remove(tokenHeader.toLowerCase(Locale.ROOT));
    headers.put(employeeHeader, employee);
    headers.put(tokenHeader, token);
    tasks.checkOutbound(capability);
    tasks.requireNoTransaction();
    UacHttp.Response response;
    try {
      response =
          http.execute(
              uri,
              request.getMethod(),
              request.getBody().toByteArray(),
              headers,
              request.getTimeoutMs(),
              MAX_BODY,
              Context.current());
    } catch (IOException | RuntimeException error) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    tasks.requireNoTransaction();
    tasks.checkOutbound(capability);
    var secrets = new ArrayList<String>(List.of(token, employee));
    for (var header : response.headers().entrySet())
      if (AUTH_FIELD.matcher(header.getKey()).find()
          || header.getKey().equalsIgnoreCase(employeeHeader)
          || header.getKey().equalsIgnoreCase(tokenHeader))
        for (String value : header.getValue())
          if (!value.isEmpty()) {
            secrets.add(value);
            if (value.matches("(?i)(Bearer|Basic)\\s+.+"))
              secrets.add(value.substring(value.indexOf(' ') + 1).strip());
          }
    for (JsonNode cookie : material.path("cookies"))
      if (cookie.isString() && !cookie.asString().isEmpty()) secrets.add(cookie.asString());
    for (String cookie : response.headers().getOrDefault("set-cookie", List.of())) {
      String pair = cookie.split(";", 2)[0];
      int separator = pair.indexOf('=');
      if (separator >= 0 && separator < pair.length() - 1)
        secrets.add(pair.substring(separator + 1));
    }
    String body;
    try {
      String text = new String(response.body(), StandardCharsets.UTF_8);
      String content =
          response
              .headers()
              .getOrDefault("content-type", List.of(""))
              .getFirst()
              .toLowerCase(Locale.ROOT);
      if (!text.isBlank()
          && (content.contains("json")
              || text.stripLeading().startsWith("{")
              || text.stripLeading().startsWith("["))) {
        JsonNode root = JSON.readTree(text);
        if (root == null) throw new IllegalArgumentException();
        collectSecrets(root, secrets, false);
        body = redact(root, secrets).toString();
      } else body = redactText(text, secrets);
      if (body.getBytes(StandardCharsets.UTF_8).length > MAX_BODY)
        throw new IllegalArgumentException();
    } catch (RuntimeException error) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    var result =
        ExecuteSharedOutboundResponse.newBuilder().setStatusCode(response.status()).setBody(body);
    for (String name : RESPONSE_HEADERS) {
      var values = response.headers().getOrDefault(name, List.of());
      if (values.size() == 1) result.putHeaders(name, redactText(values.getFirst(), secrets));
    }
    result.putHeaders(
        "content-length", Integer.toString(body.getBytes(StandardCharsets.UTF_8).length));
    return result.build();
  }

  private JsonNode redact(JsonNode value, List<String> secrets) {
    if (value.isObject()) {
      ObjectNode clean = JSON.createObjectNode();
      for (var entry : value.properties()) {
        String key = entry.getKey();
        if (!AUTH_FIELD.matcher(key).find()
            && !key.equalsIgnoreCase(employeeHeader)
            && !key.equalsIgnoreCase(tokenHeader)
            && secrets.stream().noneMatch(key::contains))
          clean.set(key, redact(entry.getValue(), secrets));
      }
      return clean;
    }
    if (value.isArray()) {
      var clean = JSON.createArrayNode();
      for (JsonNode item : value) clean.add(redact(item, secrets));
      return clean;
    }
    return value.isString() ? JSON.valueToTree(redactText(value.asString(), secrets)) : value;
  }

  private void collectSecrets(JsonNode value, List<String> secrets, boolean sensitive) {
    if (secrets.size() > 256) throw new IllegalArgumentException();
    if (value.isString()) {
      if (sensitive && !value.asString().isEmpty()) secrets.add(value.asString());
    } else if (value.isObject()) {
      for (var entry : value.properties())
        collectSecrets(
            entry.getValue(),
            secrets,
            sensitive
                || AUTH_FIELD.matcher(entry.getKey()).find()
                || entry.getKey().equalsIgnoreCase(employeeHeader)
                || entry.getKey().equalsIgnoreCase(tokenHeader));
    } else if (value.isArray())
      for (JsonNode child : value) collectSecrets(child, secrets, sensitive);
  }

  private static String redactText(String text, List<String> secrets) {
    for (String secret : secrets) text = text.replace(secret, "[REDACTED]");
    return AUTH_TEXT.matcher(text).replaceAll("$1[REDACTED]");
  }
}
