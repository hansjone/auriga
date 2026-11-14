package auriga.iam;

import auriga.contracts.msgnet.model.*;
import auriga.iam.api.MessageNetworkControlLedger;
import java.net.URI;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.*;
import java.util.function.Supplier;
import javax.net.ssl.SSLContext;

/** Fixed-origin internal HTTPS ledger client. It has no private-material methods. */
public final class RemoteMessageNetworkLedger
    implements MessageNetworkControlLedger, AutoCloseable {
  private final URI origin;
  private final HttpClient client;
  private final Supplier<String> token;
  private final ContractSchemas schemas = new ContractSchemas();

  public RemoteMessageNetworkLedger(URI origin, SSLContext tls, Supplier<String> token) {
    if (!"https".equals(origin.getScheme())
        || origin.getHost() == null
        || origin.getUserInfo() != null
        || origin.getRawQuery() != null
        || origin.getRawFragment() != null
        || !Set.of("", "/").contains(origin.getRawPath()))
      throw new IllegalArgumentException("fixed HTTPS ledger origin required");
    this.origin = origin;
    this.token = Objects.requireNonNull(token);
    client =
        HttpClient.newBuilder()
            .sslContext(Objects.requireNonNull(tls))
            .connectTimeout(Duration.ofSeconds(3))
            .followRedirects(HttpClient.Redirect.NEVER)
            .proxy(HttpClient.Builder.NO_PROXY)
            .build();
  }

  private <T> T read(String path, String definition, Class<T> type, Object body, boolean missing) {
    var request =
        HttpRequest.newBuilder(origin.resolve("/api/collab/v2/network" + path))
            .timeout(Duration.ofSeconds(10))
            .header("Cache-Control", "no-store");
    if (!path.startsWith("/bootstrap/")) {
      String credential = token.get();
      if (credential == null
          || credential.isBlank()
          || credential.chars().anyMatch(Character::isWhitespace))
        throw new IllegalStateException("ledger credential unavailable");
      request.header("Authorization", "Bearer " + credential);
    }
    if (body == null) request.GET();
    else
      request
          .header("Content-Type", "application/json")
          .POST(HttpRequest.BodyPublishers.ofByteArray(canonical(body)));
    try {
      var response = client.send(request.build(), HttpResponse.BodyHandlers.ofInputStream());
      try (var input = response.body()) {
        if (missing && response.statusCode() == 404) return null;
        if (response.statusCode() != 200
            || !response
                .headers()
                .firstValue("Cache-Control")
                .orElse("")
                .toLowerCase(Locale.ROOT)
                .contains("no-store")) throw new IllegalStateException("ledger unavailable");
        byte[] raw = input.readNBytes(32 * 1024 * 1024 + 1);
        if (raw.length > 32 * 1024 * 1024)
          throw new IllegalStateException("ledger response too large");
        var json = InstallationJson.parse(raw);
        schemas.validate("network/auto-renewal-v2.schema.json#/$defs/" + definition, json);
        return Json.MAPPER.treeToValue(json, type);
      }
    } catch (InterruptedException interrupted) {
      Thread.currentThread().interrupt();
      throw new IllegalStateException("ledger interrupted", interrupted);
    } catch (java.io.IOException unavailable) {
      throw new IllegalStateException("ledger unavailable", unavailable);
    }
  }

  private static String target(String target) {
    if (target == null || !target.matches("[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}"))
      throw new IllegalArgumentException("invalid ledger target");
    return target;
  }

  private static void scope(
      String tenant, String realm, String target, String t, String r, String id) {
    if (!Objects.equals(tenant, t) || !Objects.equals(realm, r) || !Objects.equals(target, id))
      throw new IllegalStateException("ledger scope mismatch");
  }

  @Override
  public Optional<ControlPackage> pending(String tenant, String realm, String target) {
    var value =
        read(
            "/controls/pending/" + target(target),
            "ControlPackage",
            ControlPackage.class,
            null,
            true);
    if (value != null)
      scope(tenant, realm, target, value.getTenantId(), value.getRealm(), value.getTargetId());
    return Optional.ofNullable(value);
  }

  @Override
  public IsolationRecord isolation(String tenant, String realm, String target) {
    var value =
        read("/isolation/" + target(target), "IsolationRecord", IsolationRecord.class, null, true);
    if (value != null)
      scope(tenant, realm, target, value.getTenantId(), value.getRealm(), value.getTargetId());
    return value;
  }

  @Override
  public IsolationRecord confirm(InstallationReceipt receipt) {
    var value =
        read("/controls/receipts", "IsolationRecord", IsolationRecord.class, receipt, false);
    scope(
        receipt.getTenantId(),
        receipt.getRealm(),
        receipt.getTargetId(),
        value.getTenantId(),
        value.getRealm(),
        value.getTargetId());
    return value;
  }

  @Override
  public SignedTransportTrustBundle transport() {
    return read(
        "/bootstrap/transport",
        "SignedTransportTrustBundle",
        SignedTransportTrustBundle.class,
        null,
        false);
  }

  private byte[] canonical(Object value) {
    return InstallationJson.canonical(Json.MAPPER.valueToTree(value))
        .getBytes(StandardCharsets.UTF_8);
  }

  @Override
  public byte[] canonicalControl(ControlPackage value) {
    schemas.validate(
        "network/auto-renewal-v2.schema.json#/$defs/ControlPackage",
        Json.MAPPER.valueToTree(value));
    return canonical(value);
  }

  @Override
  public byte[] canonicalTransport(SignedTransportTrustBundle value) {
    schemas.validate(
        "network/auto-renewal-v2.schema.json#/$defs/SignedTransportTrustBundle",
        Json.MAPPER.valueToTree(value));
    return canonical(value);
  }

  @Override
  public void close() {
    client.close();
  }
}
