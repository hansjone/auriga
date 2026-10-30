package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import java.security.KeyPair;
import java.security.KeyPairGenerator;
import java.time.Instant;
import java.util.Base64;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

final class KeyTestMaterial {
  static final Instant NOW = Instant.parse("2026-10-25T00:00:00Z");
  static final JsonMapper JSON = JsonMapper.builder().build();
  final KeyPair root = pair();
  final KeyPair business = pair();
  final KeyPair protocol = pair();
  final KeyPair management = pair();

  static KeyPair pair() {
    try {
      return KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    } catch (java.security.GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }

  ObjectNode payload(int version, Instant issued) {
    var p = JSON.createObjectNode();
    p.put("schema_version", 1)
        .put("realm", "2")
        .put("keyset_version", "" + version)
        .put("issued_at", issued.toString())
        .put("valid_until", issued.plusSeconds(86400).toString());
    var keys = p.putArray("keys");
    keys.add(key("business", "business_delivery", business));
    keys.add(key("protocol", "protocol_response", protocol));
    keys.add(key("management", "management_state", management));
    return p;
  }

  ObjectNode key(String kid, String purpose, KeyPair pair) {
    return JSON.createObjectNode()
        .put("kid", kid)
        .put("alg", "Ed25519")
        .put("purpose", purpose)
        .put("state", "active")
        .put("not_before", NOW.toString())
        .put("not_after", NOW.plusSeconds(604800).toString())
        .put(
            "public_key_spki",
            Base64.getUrlEncoder().withoutPadding().encodeToString(pair.getPublic().getEncoded()));
  }

  RootKeyPublication verifier(java.time.Clock clock) {
    return new RootKeyPublication(
        "2", "root", root.getPublic(), RootKeyPublication.fingerprint(root.getPublic()), clock);
  }

  RootKeyPublication.Release release(ObjectNode payload) {
    return OfflineKeySigning.sign(NetworkJson.canonical(payload), "root", root.getPrivate());
  }
}
