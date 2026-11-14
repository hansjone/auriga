package auriga.iam;

import auriga.common.error.*;
import java.io.*;
import java.net.*;
import java.net.http.*;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.Flow;
import tools.jackson.core.StreamReadConstraints;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.core.json.JsonFactory;
import tools.jackson.databind.*;
import tools.jackson.databind.json.JsonMapper;

/** Only a deployment-owned origin and a fixed tenant-bound LogQL template are accepted. */
final class UacLokiSource {
  record Result(List<String> lines, boolean available, boolean truncated, String reason) {}

  private static final int MAX_RESPONSE = 16 * 1024 * 1024, LIMIT = 5000;
  private final URI origin;
  private static final JsonMapper JSON =
      JsonMapper.builder(
              JsonFactory.builder()
                  .streamReadConstraints(
                      StreamReadConstraints.builder()
                          .maxNestingDepth(32)
                          .maxStringLength(64 * 1024)
                          .build())
                  .build())
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
          .build();

  UacLokiSource(String configuredOrigin) {
    if (configuredOrigin == null || configuredOrigin.isBlank()) {
      origin = null;
      return;
    }
    try {
      URI value = URI.create(configuredOrigin);
      if (!Set.of("http", "https").contains(value.getScheme())
          || value.getHost() == null
          || value.getRawUserInfo() != null
          || value.getRawQuery() != null
          || value.getRawFragment() != null
          || !Set.of("", "/").contains(value.getRawPath())) throw new IllegalArgumentException();
      origin = URI.create(value.getScheme() + "://" + value.getRawAuthority());
    } catch (RuntimeException e) {
      throw new IllegalArgumentException("Invalid trusted UAC Loki origin");
    }
  }

  static String query(UUID tenant, UUID provider, UUID correlation) {
    String query =
        "{service=~\"platform|iam\",job=~\"auriga-(compose|nas)\"} | json uac_message=\"message\" | line_format \"{{.uac_message}}\" | json uac_tenant=\"tenantId\",uac_provider=\"providerId\",uac_correlation=\"correlationId\",uac_service=\"service\" | __error__=\"\" | uac_service=\"iam\" | uac_tenant=\""
            + tenant
            + "\"";
    if (provider != null) query += " | uac_provider=\"" + provider + "\"";
    if (correlation != null) query += " | uac_correlation=\"" + correlation + "\"";
    return query;
  }

  Result read(UUID tenant, UUID provider, UUID correlation, Instant from, Instant to) {
    if (origin == null) return new Result(List.of(), false, false, "missing");
    String query = query(tenant, provider, correlation);
    String parameters =
        "query="
            + URLEncoder.encode(query, StandardCharsets.UTF_8)
            + "&start="
            + from.getEpochSecond()
            + String.format(Locale.ROOT, "%09d", from.getNano())
            + "&end="
            + to.getEpochSecond()
            + String.format(Locale.ROOT, "%09d", to.getNano())
            + "&direction=forward&limit="
            + LIMIT;
    HttpClient client =
        HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(2))
            .followRedirects(HttpClient.Redirect.NEVER)
            .build();
    CompletableFuture<HttpResponse<byte[]>> future = null;
    try {
      var request =
          HttpRequest.newBuilder(URI.create(origin + "/loki/api/v1/query_range?" + parameters))
              .timeout(Duration.ofSeconds(4))
              .header("Accept", "application/json")
              .header("X-Scope-OrgID", tenant.toString())
              .GET()
              .build();
      future =
          client.sendAsync(
              request,
              info -> {
                if (info.statusCode() == 401 || info.statusCode() == 403)
                  throw new AurigaException(CommonErrors.FORBIDDEN);
                return new BoundedBody();
              });
      var response = future.get(6, TimeUnit.SECONDS);
      if (response.statusCode() == 401 || response.statusCode() == 403)
        throw new AurigaException(CommonErrors.FORBIDDEN);
      if (response.statusCode() != 200) return new Result(List.of(), false, false, "unreachable");
      byte[] body = response.body();
      JsonNode root = JSON.readTree(body);
      if (!"success".equals(root.path("status").asString())
          || !"streams".equals(root.path("data").path("resultType").asString())
          || !root.path("data").path("result").isArray())
        return new Result(List.of(), false, false, "invalid_log");
      List<String> lines = new ArrayList<>();
      boolean malformed = false, truncated = false;
      for (JsonNode stream : root.path("data").path("result")) {
        if (!stream.path("values").isArray()) {
          malformed = true;
          continue;
        }
        for (JsonNode pair : stream.path("values")) {
          if (pair.isArray() && pair.size() == 2 && pair.get(1).isString()) {
            if (lines.size() == LIMIT) {
              truncated = true;
              break;
            }
            lines.add(pair.get(1).asString());
          } else malformed = true;
        }
      }
      truncated |= lines.size() == LIMIT;
      return new Result(
          List.copyOf(lines),
          true,
          truncated,
          malformed ? "invalid_log" : truncated ? "truncated" : "available");
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      return new Result(List.of(), false, false, "unreachable");
    } catch (TimeoutException e) {
      return new Result(List.of(), false, false, "unreachable");
    } catch (ExecutionException e) {
      if (e.getCause() instanceof AurigaException auth) throw auth;
      if (e.getCause() instanceof SizeLimit) return new Result(List.of(), false, true, "truncated");
      return new Result(List.of(), false, false, "unreachable");
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException e) {
      return new Result(List.of(), false, false, "invalid_log");
    } finally {
      if (future != null && !future.isDone()) future.cancel(true);
      client.shutdownNow();
    }
  }

  private static final class BoundedBody implements HttpResponse.BodySubscriber<byte[]> {
    private final CompletableFuture<byte[]> body = new CompletableFuture<>();
    private final ByteArrayOutputStream output = new ByteArrayOutputStream();
    private Flow.Subscription subscription;

    public CompletionStage<byte[]> getBody() {
      return body;
    }

    public void onSubscribe(Flow.Subscription value) {
      subscription = value;
      value.request(1);
    }

    public void onNext(List<ByteBuffer> buffers) {
      for (ByteBuffer buffer : buffers) {
        if (buffer.remaining() > MAX_RESPONSE - output.size()) {
          subscription.cancel();
          body.completeExceptionally(new SizeLimit());
          return;
        }
        byte[] bytes = new byte[buffer.remaining()];
        buffer.get(bytes);
        output.writeBytes(bytes);
      }
      subscription.request(1);
    }

    public void onError(Throwable error) {
      body.completeExceptionally(error);
    }

    public void onComplete() {
      body.complete(output.toByteArray());
    }
  }

  private static final class SizeLimit extends IOException {
    SizeLimit() {
      super("Diagnostic source size limit");
    }
  }
}
