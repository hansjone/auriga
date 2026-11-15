package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Base64;
import java.util.HexFormat;
import java.util.Map;
import java.util.TreeMap;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Reconstructs the exact frozen on-disk bytes; stores only file hashes, never private material. */
final class MsgnetProjectionBinding {
  private MsgnetProjectionBinding() {}

  static ObjectNode snapshot(JsonNode material, JsonNode publication) {
    var result = MsgnetRenewal.object();
    for (String field :
        java.util.List.of(
            "tenant_id",
            "realm",
            "installation_id",
            "component",
            "nonce",
            "generation",
            "keyset_version",
            "publication_digest")) result.set(field, material.path(field));
    result.set("bundle_version", material.path("bundle").path("payload").path("bundle_version"));
    result.put("bundle_digest", digest(material.path("bundle")));
    Map<String, byte[]> bytes = new TreeMap<>();
    bytes.put("publication.json", json(publication));
    bytes.put("bundle.json", json(material.path("bundle")));
    if (material.has("protocol")) bytes.put("protocol.json", json(material.path("protocol")));
    int index = 0;
    for (var leaf : material.path("leaves")) {
      String prefix = String.format(java.util.Locale.ROOT, "leaf-%03d", index++);
      bytes.put(prefix + ".cert.pem", pem("CERTIFICATE", text(leaf, "certificate_der")));
      bytes.put(prefix + ".key.pem", pem("PRIVATE KEY", text(leaf, "private_pkcs8")));
    }
    var files = result.putArray("files");
    try {
      for (var file : bytes.entrySet())
        files
            .addObject()
            .put("name", file.getKey())
            .put("size", file.getValue().length)
            .put("sha256", hash(file.getValue()));
    } finally {
      bytes.values().forEach(value -> java.util.Arrays.fill(value, (byte) 0));
    }
    return result;
  }

  static void verify(JsonNode snapshot, JsonNode selector, JsonNode receipt, JsonNode previous) {
    schema("LocalProjectionSelector", selector, "invalid_chain");
    for (String field : snapshot.propertyNames()) {
      if (!"files".equals(field))
        require(snapshot.path(field).equals(selector.path(field)), "invalid_chain");
    }
    for (String field :
        java.util.List.of(
            "tenant_id",
            "realm",
            "installation_id",
            "component",
            "nonce",
            "generation",
            "keyset_version",
            "publication_digest",
            "bundle_version",
            "bundle_digest"))
      require(selector.path(field).equals(receipt.path(field)), "invalid_chain");
    var manifest = ((ObjectNode) snapshot).deepCopy();
    manifest.set("material_revision", selector.path("material_revision"));
    schema("LocalProjectionManifest", manifest, "invalid_chain");
    require(digest(manifest).equals(text(selector, "manifest_digest")), "invalid_chain");
    if (previous != null) {
      int generation = number(selector, "generation").compareTo(number(previous, "generation"));
      require(
          generation >= 0
              && (generation != 0
                  || number(selector, "keyset_version")
                          .compareTo(number(previous, "keyset_version"))
                      >= 0)
              && (generation > 0
                  || number(selector, "bundle_version")
                          .compareTo(number(previous, "bundle_version"))
                      >= 0)
              && number(selector, "material_revision")
                      .compareTo(number(previous, "material_revision"))
                  >= 0,
          "version_rollback");
      if (selector.path("material_revision").equals(previous.path("material_revision")))
        require(selector.equals(previous), "same_version_conflict");
    }
  }

  private static byte[] json(JsonNode node) {
    return MsgnetRenewal.canonical(node).getBytes(StandardCharsets.UTF_8);
  }

  private static byte[] pem(String type, String encoded) {
    byte[] der = Base64.getUrlDecoder().decode(encoded);
    try {
      String base64 = Base64.getMimeEncoder(64, new byte[] {10}).encodeToString(der);
      return ("-----BEGIN " + type + "-----\n" + base64 + "\n-----END " + type + "-----\n")
          .getBytes(StandardCharsets.US_ASCII);
    } finally {
      java.util.Arrays.fill(der, (byte) 0);
    }
  }

  private static String hash(byte[] bytes) {
    try {
      return "sha256:"
          + HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
    } catch (java.security.GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }
}
