package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyFactory;
import java.security.PrivateKey;
import java.security.Signature;
import java.security.spec.PKCS8EncodedKeySpec;
import java.time.Clock;
import java.util.Arrays;
import java.util.Base64;
import tools.jackson.databind.json.JsonMapper;

/** Offline executable only. Never registered as an online bean or HTTP signing oracle. */
public final class OfflineKeySigning {
  private OfflineKeySigning() {}

  public static RootKeyPublication.Release sign(String payloadJson, String kid, PrivateKey root) {
    var payload = NetworkJson.parse(payloadJson);
    RootKeyPublication.checkPayload(payload);
    if (kid == null || !kid.matches("[A-Za-z0-9._-]{1,128}")) throw RootKeyPublication.invalid();
    var json = JsonMapper.builder().build();
    var signed = json.createObjectNode();
    signed.set("keyset", payload);
    signed
        .putObject("root_auth")
        .put("alg", "Ed25519")
        .put("kid", kid)
        .put("signature", signature(RootKeyPublication.KEYSET_DOMAIN, payload, root));
    var announcement =
        json.createObjectNode()
            .put("keyset_version", payload.path("keyset_version").asString())
            .put("manifest_digest", NetworkJson.digest(payload))
            .put("valid_until", payload.path("valid_until").asString())
            .put("root_kid", kid);
    String proof = signature(RootKeyPublication.ANNOUNCEMENT_DOMAIN, announcement, root);
    announcement.put("root_signature", proof);
    return new RootKeyPublication.Release(
        NetworkJson.canonical(signed), NetworkJson.canonical(announcement));
  }

  private static String signature(
      String domain, tools.jackson.databind.JsonNode body, PrivateKey key) {
    try {
      var signer = Signature.getInstance("Ed25519");
      signer.initSign(key);
      signer.update((domain + NetworkJson.canonical(body)).getBytes(StandardCharsets.UTF_8));
      return Base64.getUrlEncoder().withoutPadding().encodeToString(signer.sign());
    } catch (java.security.GeneralSecurityException e) {
      throw RootKeyPublication.invalid();
    }
  }

  /** sign payload root-kid root-private.der signed-output announcement-output */
  public static void main(String[] args) throws Exception {
    if (args.length == 6 && "sign".equals(args[0])) {
      byte[] secret = ProtectedKeyFiles.readSecret(Path.of(args[3]));
      try {
        var key =
            KeyFactory.getInstance("Ed25519").generatePrivate(new PKCS8EncodedKeySpec(secret));
        var release = sign(ProtectedKeyFiles.readText(Path.of(args[1])), args[2], key);
        // CREATE_NEW prevents accidentally replacing an already distributed release.
        Files.writeString(
            Path.of(args[4]),
            release.signedKeyset(),
            StandardCharsets.UTF_8,
            java.nio.file.StandardOpenOption.CREATE_NEW);
        Files.writeString(
            Path.of(args[5]),
            release.announcement(),
            StandardCharsets.UTF_8,
            java.nio.file.StandardOpenOption.CREATE_NEW);
      } finally {
        Arrays.fill(secret, (byte) 0);
      }
      return;
    }
    // install signed announcement root-kid root-public.der fingerprint state-file
    if (args.length == 7 && "install".equals(args[0])) {
      var root =
          KeyFactory.getInstance("Ed25519")
              .generatePublic(
                  new java.security.spec.X509EncodedKeySpec(
                      ProtectedKeyFiles.read(Path.of(args[4]), 1024)));
      var verifier =
          new RootKeyPublication(
              NetworkJson.parse(ProtectedKeyFiles.readText(Path.of(args[1])))
                  .path("keyset")
                  .path("realm")
                  .asString(),
              args[3],
              root,
              args[5],
              Clock.systemUTC());
      var release =
          verifier.verify(
              ProtectedKeyFiles.readText(Path.of(args[1])),
              ProtectedKeyFiles.readText(Path.of(args[2])));
      SigningKeyLoader.installState(release.payload(), Path.of(args[6]));
      return;
    }
    throw new IllegalArgumentException(
        "Expected sign or install arguments; see key operations contract");
  }
}
