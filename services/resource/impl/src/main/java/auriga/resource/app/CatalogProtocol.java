package auriga.resource.app;

import auriga.contracts.resource.v1.*;
import tools.jackson.databind.JsonNode;

/** Signed fields retain original text and safe integer values through the public Java port. */
public final class CatalogProtocol {
  private CatalogProtocol() {}

  public static UnifiedResourceIdentity identity(JsonNode n) {
    return UnifiedResourceIdentity.newBuilder()
        .setTenantId(n.path("tenantId").asString())
        .setPackageId(n.path("packageId").asString())
        .setVersionId(n.path("versionId").asString())
        .setKind(n.path("kind").asString())
        .setVersion(n.path("version").asString())
        .setManifestDigest(n.path("manifestDigest").asString())
        .build();
  }

  public static UnifiedSelection selection(JsonNode n) {
    return UnifiedSelection.newBuilder()
        .setIdentity(identity(n.path("identity")))
        .setAction(n.path("action").asString())
        .build();
  }

  public static UnifiedResourceRef ref(JsonNode n) {
    var e = n.path("envelope");
    var s = e.path("statement");
    var statement =
        UnifiedStatement.newBuilder()
            .setPurpose(s.path("purpose").asString())
            .setKind(s.path("kind").asString())
            .setName(s.path("name").asString())
            .setVersion(s.path("version").asString())
            .setManifestDigest(s.path("manifestDigest").asString())
            .setIssuedAt(s.path("issuedAt").asString());
    s.path("artifactDigests").forEach(d -> statement.addArtifactDigests(d.asString()));
    var envelope =
        UnifiedEnvelope.newBuilder()
            .setSchemaVersion(2)
            .setTenantId(e.path("tenantId").asString())
            .setPackageId(e.path("packageId").asString())
            .setVersionId(e.path("versionId").asString())
            .setReleaseSequence(e.path("releaseSequence").asLong())
            .setNotAfter(e.path("notAfter").asString())
            .setStatement(statement)
            .setSignature(ResourceProtocol.signature(e.path("signature")));
    var l = n.path("lease");
    var b =
        UnifiedResourceRef.newBuilder()
            .setIdentity(identity(n.path("identity")))
            .setEnvelope(envelope)
            .setManifestDownloadPath(n.path("manifestDownloadPath").asString())
            .setReleaseSequence(n.path("releaseSequence").asLong())
            .setEnabled(n.path("enabled").asBoolean())
            .setLease(
                UnifiedLease.newBuilder()
                    .setEnvelopeNotAfter(l.path("envelopeNotAfter").asString())
                    .setRevocationsExpiresAt(l.path("revocationsExpiresAt").asString())
                    .setExpiresAt(l.path("expiresAt").asString())
                    .setRevocationRevision(l.path("revocationRevision").asLong()));
    n.path("artifacts")
        .forEach(
            a ->
                b.addArtifacts(
                    UnifiedArtifactDownload.newBuilder()
                        .setArtifactId(a.path("artifactId").asString())
                        .setDigest(a.path("digest").asString())
                        .setSizeBytes(a.path("sizeBytes").asLong())
                        .setDownloadPath(a.path("downloadPath").asString())));
    return b.build();
  }
}
