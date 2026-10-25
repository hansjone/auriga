package auriga.collab.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.Signature;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.Base64;
import tools.jackson.databind.node.ObjectNode;

/** Internal structured signer, deliberately not exposed as a client JSON signing endpoint. */
final class NetworkEnvelopeSigner {
  private final NetworkSigningKeys keys;
  private final Clock clock;

  NetworkEnvelopeSigner(NetworkSigningKeys keys, Clock clock) {
    this.keys = keys;
    this.clock = clock;
  }

  ObjectNode sign(
      ObjectNode envelope, String purpose, Context target, Instant authorizationDeadline) {
    Instant now = clock.instant().truncatedTo(ChronoUnit.SECONDS);
    String type = envelope.path("type").asString("");
    String expected =
        switch (type) {
          case "delivery", "contact_prompt" -> "business_delivery";
          case "state_update" -> "management_state";
          default -> "protocol_response";
        };
    if (!expected.equals(purpose)
        || authorizationDeadline == null
        || !authorizationDeadline.isAfter(now))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return keys.sign(
        target.realm(),
        purpose,
        now,
        (key, version) -> {
          Instant deadline = now.plusSeconds(300);
          if (key.notAfter.isBefore(deadline)) deadline = key.notAfter;
          if (authorizationDeadline.isBefore(deadline)) deadline = authorizationDeadline;
          if (envelope.has("expires_at")) {
            Instant expires = Instant.parse(envelope.path("expires_at").asString());
            if (expires.isBefore(deadline)) deadline = expires;
          }
          if (envelope.has("message")) {
            Instant expires = Instant.parse(envelope.path("message").path("expires_at").asString());
            if (expires.isBefore(deadline)) deadline = expires;
          }
          deadline = deadline.truncatedTo(ChronoUnit.SECONDS);
          if (!deadline.isAfter(now)) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
          ObjectNode signed = envelope.deepCopy();
          ObjectNode auth = signed.putObject("server_auth");
          auth.put("version", 1)
              .put("purpose", purpose)
              .put("alg", "Ed25519")
              .put("kid", key.kid)
              .put("issuer_realm", target.realm())
              .put("issued_at", now.toString())
              .put("not_after", deadline.toString());
          auth.putObject("audience")
              .put("realm", target.realm())
              .put("node", target.node())
              .put("incarnation", target.incarnation())
              .put("epoch", Long.toString(target.epoch()));
          if (target.requestId() == null) auth.putNull("request_id");
          else auth.put("request_id", target.requestId());
          auth.put("channel_nonce", target.channelNonce())
              .put("keyset_version", Long.toString(version));
          try {
            Signature signature = Signature.getInstance("Ed25519");
            signature.initSign(key.privateKey);
            signature.update(signingInput(signed));
            auth.put(
                "signature",
                Base64.getUrlEncoder().withoutPadding().encodeToString(signature.sign()));
            NetworkJson.canonical(signed);
            return signed;
          } catch (GeneralSecurityException e) {
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          }
        });
  }

  static byte[] signingInput(ObjectNode envelope) {
    ObjectNode unsigned = envelope.deepCopy();
    if (!(unsigned.get("server_auth") instanceof ObjectNode auth))
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    auth.remove("signature");
    byte[] prefix = "Auriga.ServerEnvelope.v1\0".getBytes(StandardCharsets.UTF_8);
    byte[] canonical = NetworkJson.canonical(unsigned).getBytes(StandardCharsets.UTF_8);
    byte[] input = java.util.Arrays.copyOf(prefix, prefix.length + canonical.length);
    System.arraycopy(canonical, 0, input, prefix.length, canonical.length);
    return input;
  }

  record Context(
      String realm,
      String node,
      String incarnation,
      long epoch,
      String channelNonce,
      String requestId) {}
}
