package auriga.iam;

import java.io.ByteArrayOutputStream;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.security.interfaces.ECPublicKey;
import java.security.spec.ECGenParameterSpec;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** A runtime-generated ES256 authenticator; assertions go through the real WebAuthn verifier. */
final class StepUpPasskeyFixture {
  private final KeyPair key;
  private final byte[] credential = new byte[32];
  private final Base64.Encoder b64 = Base64.getUrlEncoder().withoutPadding();

  StepUpPasskeyFixture() throws Exception {
    var generator = KeyPairGenerator.getInstance("EC");
    generator.initialize(new ECGenParameterSpec("secp256r1"));
    key = generator.generateKeyPair();
    new SecureRandom().nextBytes(credential);
  }

  void enroll(IamStore store, UUID account) {
    var pub = (ECPublicKey) key.getPublic();
    byte[] cose =
        com.upokecenter.cbor.CBORObject.NewMap()
            .Add(1, 2)
            .Add(3, -7)
            .Add(-1, 1)
            .Add(-2, coordinate(pub.getW().getAffineX()))
            .Add(-3, coordinate(pub.getW().getAffineY()))
            .EncodeToBytes();
    String id = UUID.randomUUID().toString();
    store.insert(
        "team_passkey",
        id,
        Json.object(
            "passkeyId",
            id,
            "accountId",
            account.toString(),
            "credentialId",
            b64.encodeToString(credential),
            "publicKeyCose",
            b64.encodeToString(cose),
            "signCount",
            0),
        null);
  }

  ObjectNode assertion(JsonNode options, UUID account) throws Exception {
    byte[] client =
        Json.object(
                "type",
                "webauthn.get",
                "challenge",
                options.path("challenge").asString(),
                "origin",
                "https://portal.example.com",
                "crossOrigin",
                false)
            .toString()
            .getBytes(StandardCharsets.UTF_8);
    var data = new ByteArrayOutputStream();
    var sha = MessageDigest.getInstance("SHA-256");
    data.write(sha.digest("portal.example.com".getBytes(StandardCharsets.UTF_8)));
    data.write(0x05); // user present and user verified
    data.write(new byte[] {0, 0, 0, 1});
    var signature = Signature.getInstance("SHA256withECDSA");
    signature.initSign(key.getPrivate());
    signature.update(data.toByteArray());
    signature.update(sha.digest(client));
    byte[] handle =
        ByteBuffer.allocate(16)
            .putLong(account.getMostSignificantBits())
            .putLong(account.getLeastSignificantBits())
            .array();
    return Json.object(
        "id",
        b64.encodeToString(credential),
        "rawId",
        b64.encodeToString(credential),
        "type",
        "public-key",
        "response",
        Json.object(
            "clientDataJSON",
            b64.encodeToString(client),
            "authenticatorData",
            b64.encodeToString(data.toByteArray()),
            "signature",
            b64.encodeToString(signature.sign()),
            "userHandle",
            b64.encodeToString(handle)),
        "clientExtensionResults",
        Json.object());
  }

  private static byte[] coordinate(java.math.BigInteger value) {
    byte[] raw = value.toByteArray(), out = new byte[32];
    System.arraycopy(
        raw,
        Math.max(0, raw.length - 32),
        out,
        Math.max(0, 32 - raw.length),
        Math.min(32, raw.length));
    return out;
  }
}
