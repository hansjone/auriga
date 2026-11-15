package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.security.PrivateKey;
import java.security.Signature;
import java.util.Base64;
import java.util.Map;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Offline executable. Its fixed domains cannot issue online keysets or arbitrary signed bytes. */
public final class MsgnetOfflineRoot {
  private static final Map<String, String> TYPES =
      Map.of(
          "authority",
          "NetworkRuntimeAuthority",
          "control",
          "NetworkRootControl",
          "migration",
          "NetworkMigrationPermit");
  private static final Map<String, String> SCHEMAS =
      Map.of(
          "authority",
          "RuntimeAuthority",
          "control",
          "RootControl",
          "migration",
          "MigrationPermit");

  private MsgnetOfflineRoot() {}

  public static ObjectNode sign(String type, JsonNode payload, String kid, PrivateKey key) {
    require(
        TYPES.containsKey(type) && kid != null && kid.matches("[A-Za-z0-9._-]{1,128}"),
        "scope_denied");
    schema(SCHEMAS.get(type), payload, "invalid_chain");
    if ("migration".equals(type))
      require(
          number(payload, "minimum_v2_version")
                  .compareTo(number(payload, "maximum_committed_v1_version"))
              > 0,
          "invalid_transition");
    try {
      var signer = Signature.getInstance("Ed25519");
      signer.initSign(key);
      signer.update(
          ("Auriga." + TYPES.get(type) + ".v2\0" + MsgnetRenewal.canonical(payload))
              .getBytes(StandardCharsets.UTF_8));
      var signed = MsgnetRenewal.object();
      signed.set("payload", payload.deepCopy());
      signed
          .putObject("root_auth")
          .put("alg", "Ed25519")
          .put("kid", kid)
          .put("signature", Base64.getUrlEncoder().withoutPadding().encodeToString(signer.sign()));
      return signed;
    } catch (java.security.GeneralSecurityException e) {
      throw failure("invalid_chain");
    }
  }

  /** authority|control|migration payload.json root-kid root-private.der signed-output.json */
  public static void main(String[] args) throws Exception {
    if (args.length != 5 || !TYPES.containsKey(args[0])) throw ProtectedKeyFiles.invalid();
    byte[] bytes = ProtectedKeyFiles.readSecret(Path.of(args[3]));
    try {
      var key =
          java.security.KeyFactory.getInstance("Ed25519")
              .generatePrivate(new java.security.spec.PKCS8EncodedKeySpec(bytes));
      var signed =
          sign(
              args[0],
              NetworkJson.parse(ProtectedKeyFiles.readText(Path.of(args[1]), 1048576), 1048576),
              args[2],
              key);
      Files.writeString(
          Path.of(args[4]),
          MsgnetRenewal.canonical(signed),
          StandardCharsets.UTF_8,
          StandardOpenOption.CREATE_NEW);
    } finally {
      java.util.Arrays.fill(bytes, (byte) 0);
    }
  }
}
