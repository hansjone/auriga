package auriga.iam;

import auriga.iam.api.IdentityConnector;
import java.net.URI;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.time.*;
import java.util.*;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import org.slf4j.LoggerFactory;
import tools.jackson.databind.JsonNode;

/** Signed requests AND responses from contracts/identity/remote-connector.yaml. */
public final class RemoteConnector
    implements IdentityConnector,
        auriga.iam.api.DirectoryCapability,
        auriga.iam.api.SessionCheckCapability,
        auriga.iam.api.CredentialCapability {
  private final URI base;
  private final List<byte[]> keys;
  private final Clock clock;
  private final HttpClient http;
  private final JsonNode descriptor;

  public RemoteConnector(URI base, List<byte[]> keys, Clock clock) {
    IamError.require(
        "https".equals(base.getScheme())
            && base.getRawUserInfo() == null
            && base.getRawQuery() == null
            && base.getFragment() == null,
        "invalid_connector_url",
        400);
    IamError.require(
        !keys.isEmpty() && keys.stream().allMatch(k -> k.length >= 32),
        "invalid_connector_key",
        400);
    this.base = base;
    this.keys = keys.stream().map(byte[]::clone).toList();
    this.clock = clock;
    http =
        HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(10))
            .followRedirects(HttpClient.Redirect.NEVER)
            .build();
    descriptor = call("GET", "/descriptor", null);
    IamError.require(
        descriptor.path("type").asString().startsWith("remote-"), "invalid_remote_type", 400);
  }

  @Override
  public JsonNode descriptor() {
    return descriptor.deepCopy();
  }

  @Override
  public void validateConfig(JsonNode config) {
    IamError.require(
        call("POST", "/validate-config", Json.object("config", config)).path("ok").asBoolean(),
        "invalid_config",
        400);
  }

  @Override
  public JsonNode begin(JsonNode context, JsonNode config) {
    return step("/begin", Json.object("ctx", context, "config", config));
  }

  @Override
  public JsonNode advance(JsonNode context, JsonNode config, JsonNode input) {
    return step("/advance", Json.object("ctx", context, "config", config, "input", input));
  }

  @Override
  public JsonNode search(JsonNode request) {
    return capability(
        "directory", "/directory/search", "DirectorySearchRequest", "DirectoryResult", request);
  }

  @Override
  public JsonNode lookup(JsonNode request) {
    return capability(
        "directory", "/directory/lookup", "DirectoryLookupRequest", "DirectoryResult", request);
  }

  @Override
  public JsonNode checkSession(JsonNode request) {
    return capability(
        "session_check", "/session/check", "SessionCheckRequest", "SessionCheckResult", request);
  }

  @Override
  public JsonNode issueCredential(JsonNode request) {
    return capability(
        "credential",
        "/credential/issue",
        "CredentialIssueRequest",
        "DownstreamCredential",
        request);
  }

  private JsonNode capability(
      String capability, String path, String input, String output, JsonNode request) {
    IamError.require(
        java.util.stream.StreamSupport.stream(descriptor.path("capabilities").spliterator(), false)
            .anyMatch(c -> c.asString().equals(capability)),
        "unsupported_capability",
        400);
    ContractSchemas schemas = new ContractSchemas();
    schemas.validate("identity/connector.schema.json#/$defs/" + input, request);
    JsonNode result = call("POST", path, request);
    schemas.validate("identity/connector.schema.json#/$defs/" + output, result);
    return result;
  }

  private JsonNode step(String path, JsonNode body) {
    try {
      return call("POST", path, body);
    } catch (RuntimeException e) {
      return Json.object("step", Json.object("type", "fail", "code", "iam.connector_unavailable"));
    }
  }

  static String hmac(byte[] key, String text) {
    try {
      Mac mac = Mac.getInstance("HmacSHA256");
      mac.init(new SecretKeySpec(key, "HmacSHA256"));
      return HexFormat.of().formatHex(mac.doFinal(text.getBytes(StandardCharsets.UTF_8)));
    } catch (java.security.GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }

  private JsonNode call(String method, String path, JsonNode json) {
    long started = System.nanoTime();
    String outcome = "failure";
    try {
      URI target = URI.create(base.toString().replaceAll("/$", "") + path);
      byte[] nonceBytes = new byte[16];
      new java.security.SecureRandom().nextBytes(nonceBytes);
      String nonce = Base64.getUrlEncoder().withoutPadding().encodeToString(nonceBytes),
          timestamp = Long.toString(clock.instant().getEpochSecond()),
          body = json == null ? "" : json.toString();
      String message =
          timestamp
              + "."
              + nonce
              + "."
              + method
              + "."
              + target.getRawAuthority()
              + "."
              + target.getRawPath()
              + "."
              + body;
      String signatures =
          String.join(",", keys.stream().map(k -> "v1=" + hmac(k, message)).toList());
      HttpRequest request =
          HttpRequest.newBuilder(target)
              .timeout(Duration.ofSeconds(10))
              .header("Content-Type", "application/json")
              .header("Auriga-Timestamp", timestamp)
              .header("Auriga-Nonce", nonce)
              .header("Auriga-Signature", signatures)
              .method(method, HttpRequest.BodyPublishers.ofString(body))
              .build();
      HttpResponse<String> response = http.send(request, info -> new LimitedBody());
      String responseBody = response.body();
      verifyResponse(
          keys,
          clock,
          nonce,
          response.statusCode(),
          response.headers().firstValue("Auriga-Timestamp").orElse(""),
          response.headers().firstValue("Auriga-Signature").orElse(""),
          responseBody);
      IamError.require(response.statusCode() == 200, "connector_unavailable", 502);
      outcome = "success";
      return Json.MAPPER.readTree(responseBody);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new IllegalStateException("Connector interrupted");
    } catch (java.io.IOException e) {
      throw new IllegalStateException("Connector unavailable");
    } finally {
      LoggerFactory.getLogger(RemoteConnector.class)
          .info(
              "Identity connector call method={} path={} outcome={} elapsedMs={}",
              method,
              path,
              outcome,
              (System.nanoTime() - started) / 1000000);
    }
  }

  static void verifyResponse(
      List<byte[]> keys,
      Clock clock,
      String nonce,
      int status,
      String time,
      String signature,
      String body) {
    try {
      long timestamp = Long.parseLong(time), now = clock.instant().getEpochSecond();
      IamError.require(
          timestamp >= now - 300 && timestamp <= now + 300, "connector_signature", 502);
      String message = time + "." + nonce + "." + status + "." + body;
      IamError.require(
          keys.stream()
              .anyMatch(
                  k ->
                      Arrays.stream(signature.split(","))
                          .anyMatch(s -> Secrets.same(s.trim(), "v1=" + hmac(k, message)))),
          "connector_signature",
          502);
    } catch (NumberFormatException e) {
      throw IamError.error("connector_signature", 502);
    }
  }

  private static final class LimitedBody implements HttpResponse.BodySubscriber<String> {
    private final java.util.concurrent.CompletableFuture<String> body =
        new java.util.concurrent.CompletableFuture<>();
    private final java.io.ByteArrayOutputStream bytes = new java.io.ByteArrayOutputStream();
    private java.util.concurrent.Flow.Subscription subscription;

    @Override
    public java.util.concurrent.CompletionStage<String> getBody() {
      return body;
    }

    @Override
    public void onSubscribe(java.util.concurrent.Flow.Subscription s) {
      subscription = s;
      s.request(1);
    }

    @Override
    public void onNext(List<java.nio.ByteBuffer> buffers) {
      for (java.nio.ByteBuffer buffer : buffers) {
        if (buffer.remaining() > 65536 - bytes.size()) {
          subscription.cancel();
          body.completeExceptionally(new java.io.IOException("Connector response exceeds 64 KiB"));
          return;
        }
        byte[] next = new byte[buffer.remaining()];
        buffer.get(next);
        bytes.writeBytes(next);
      }
      subscription.request(1);
    }

    @Override
    public void onError(Throwable error) {
      body.completeExceptionally(error);
    }

    @Override
    public void onComplete() {
      body.complete(bytes.toString(StandardCharsets.UTF_8));
    }
  }
}
