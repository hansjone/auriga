package auriga.resource.app;

import auriga.contracts.node.v1.*;
import auriga.contracts.resource.v1.*;
import com.google.protobuf.ByteString;
import com.google.protobuf.Timestamp;
import java.time.Instant;
import java.util.Base64;
import tools.jackson.databind.JsonNode;

/** Explicit lossless mapping. Signed integer/time fields never pass through protobuf JSON. */
public final class ResourceProtocol {
  private ResourceProtocol() {}

  static ResourceSignature signature(JsonNode n) {
    return ResourceSignature.newBuilder()
        .setAlg(n.path("alg").asString())
        .setKid(n.path("kid").asString())
        .setValue(n.path("value").asString())
        .build();
  }

  static ResourceArtifactStatement statement(JsonNode n) {
    return ResourceArtifactStatement.newBuilder()
        .setPurpose(n.path("purpose").asString())
        .setName(n.path("name").asString())
        .setVersion(n.path("version").asString())
        .setOs(n.path("os").asString())
        .setArch(n.path("arch").asString())
        .setDigest(n.path("digest").asString())
        .setIssuedAt(n.path("issuedAt").asString())
        .build();
  }

  public static ResourceEnvelope envelope(JsonNode n) {
    return ResourceEnvelope.newBuilder()
        .setSchemaVersion(1)
        .setTenantId(n.path("tenantId").asString())
        .setPackageId(n.path("packageId").asString())
        .setVersionId(n.path("versionId").asString())
        .setReleaseSequence(n.path("releaseSequence").asLong())
        .setNotAfter(n.path("notAfter").asString())
        .setStatement(statement(n.path("statement")))
        .setSignature(signature(n.path("signature")))
        .build();
  }

  public static ResourceRevocationList revocations(JsonNode n) {
    var b =
        ResourceRevocationList.newBuilder()
            .setSchemaVersion(1)
            .setTenantId(n.path("tenantId").asString())
            .setRevision(n.path("revision").asLong())
            .setIssuedAt(n.path("issuedAt").asString())
            .setExpiresAt(n.path("expiresAt").asString())
            .setSignature(signature(n.path("signature")));
    n.path("revokedKids").forEach(k -> b.addRevokedKids(k.asString()));
    n.path("revokedArtifacts")
        .forEach(
            a ->
                b.addRevokedArtifacts(
                    RevokedArtifact.newBuilder()
                        .setPackageId(a.path("packageId").asString())
                        .setVersionId(a.path("versionId").asString())
                        .setDigest(a.path("digest").asString())));
    return b.build();
  }

  public static PluginRef plugin(
      JsonNode envelope, String level, String url, String downloadId, ResourceSigning signing) {
    var s = envelope.path("statement");
    var time = Instant.parse(s.path("issuedAt").asString());
    var legacy =
        ArtifactStatement.newBuilder()
            .setPurpose("plugin")
            .setName(s.path("name").asString())
            .setVersion(s.path("version").asString())
            .setOs(s.path("os").asString())
            .setArch(s.path("arch").asString())
            .setDigest(s.path("digest").asString())
            .setIssuedAt(
                Timestamp.newBuilder().setSeconds(time.getEpochSecond()).setNanos(time.getNano()))
            .build();
    var signed = signing.sign((tools.jackson.databind.node.ObjectNode) s, false).path("signature");
    return PluginRef.newBuilder()
        .setName(legacy.getName())
        .setVersion(legacy.getVersion())
        .setLevel(PluginLevel.valueOf("PLUGIN_LEVEL_" + level.toUpperCase(java.util.Locale.ROOT)))
        .setDigest(legacy.getDigest())
        .setEnabled(true)
        .setUrl(url)
        .setDownloadId(downloadId)
        .setResourceEnvelope(envelope(envelope))
        .setSigned(
            SignedArtifact.newBuilder()
                .setStatement(legacy)
                .setSigningKid(signed.path("kid").asString())
                .setSignature(
                    ByteString.copyFrom(
                        Base64.getUrlDecoder().decode(signed.path("value").asString()))))
        .build();
  }
}
