package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.contracts.node.v1.*;
import com.google.protobuf.ByteString;
import com.google.protobuf.Timestamp;
import com.google.protobuf.UnknownFieldSet;
import java.util.Base64;
import java.util.List;
import java.util.function.Consumer;
import org.junit.jupiter.api.Test;

class WorkspaceTemplatesTest {
  static InstanceSpec.Builder template() {
    var e =
        RuntimeReleaseEnvelope.newBuilder()
            .setSchemaVersion(1)
            .setPurpose("dsh_runtime")
            .setName("dsh")
            .setArtifactId("test-runtime")
            .setDshVersion("1.0.0")
            .setOs("linux")
            .setArch("amd64")
            .setImageDigest("sha256:" + "a".repeat(64))
            .setReleaseSequence(1)
            .setIssuedAt(Timestamp.newBuilder().setSeconds(1792713600))
            .setSignature(
                RuntimeReleaseSignature.newBuilder()
                    .setAlg("Ed25519")
                    .setKid("test-root")
                    .setValue(
                        Base64.getUrlEncoder().withoutPadding().encodeToString(new byte[64])));
    // An explicit shape-only declaration: WorkspaceTemplates owns no trust ring.
    return InstanceSpec.newBuilder()
        .setRuntime(RuntimeKind.RUNTIME_KIND_DOCKER)
        .setResources(Resources.newBuilder().setCpuMillis(500).setMemoryMb(512).setDiskGb(1))
        .setProfile(Profile.newBuilder().setListenPort(7860))
        .setDsh(
            DshRuntime.newBuilder()
                .setArtifactId("test-runtime")
                .setVersion("1.0.0")
                .setImage("example.com/dsh@sha256:" + "a".repeat(64))
                .setReleaseEnvelope(e));
  }

  @Test
  void filtersPlatformFromFormalEnvelope() {
    var linux = template().build();
    var windows = template();
    windows.getDshBuilder().getReleaseEnvelopeBuilder().setOs("windows").setArch("arm64");
    var templates = new WorkspaceTemplates(List.of(linux, windows.build()));
    assertSame(linux, templates.get("1.0.0", RuntimeKind.RUNTIME_KIND_DOCKER, "linux", "amd64"));
    assertEquals(
        "windows",
        templates.get(null, null, "windows", "arm64").getDsh().getReleaseEnvelope().getOs());
    assertThrows(AurigaException.class, () -> templates.get(null, null, "linux", "arm64"));
  }

  @Test
  void rejectsInvalidFormalShapeAndMappings() {
    List<Consumer<InstanceSpec.Builder>> mutations =
        List.of(
            t -> t.getDshBuilder().clearReleaseEnvelope(),
            t -> t.getDshBuilder().getReleaseEnvelopeBuilder().setSchemaVersion(2),
            t -> t.getDshBuilder().getReleaseEnvelopeBuilder().setPurpose("plugin"),
            t -> t.getDshBuilder().getReleaseEnvelopeBuilder().setName("fake-dsh"),
            t -> t.getDshBuilder().getReleaseEnvelopeBuilder().setArtifactId("other"),
            t -> t.getDshBuilder().getReleaseEnvelopeBuilder().setDshVersion("2"),
            t -> t.getDshBuilder().getReleaseEnvelopeBuilder().setOs("any"),
            t -> t.getDshBuilder().getReleaseEnvelopeBuilder().setArch("any"),
            t -> t.getDshBuilder().getReleaseEnvelopeBuilder().setReleaseSequence(0),
            t ->
                t.getDshBuilder().getReleaseEnvelopeBuilder().setReleaseSequence(9007199254740992L),
            t ->
                t.getDshBuilder()
                    .getReleaseEnvelopeBuilder()
                    .setIssuedAt(Timestamp.newBuilder().setNanos(-1)),
            t ->
                t.getDshBuilder().getReleaseEnvelopeBuilder().getSignatureBuilder().setAlg("EdDSA"),
            t -> t.getDshBuilder().getReleaseEnvelopeBuilder().getSignatureBuilder().setValue("AQ"),
            t ->
                t.getDshBuilder()
                    .getReleaseEnvelopeBuilder()
                    .getSignatureBuilder()
                    .setValue(Base64.getUrlEncoder().encodeToString(new byte[64])),
            t ->
                t.getDshBuilder()
                    .getReleaseEnvelopeBuilder()
                    .getSignatureBuilder()
                    .setKid("bad key"),
            t -> t.getDshBuilder().setImage("example.com/dsh:latest"),
            t -> t.getDshBuilder().setImage("example.com/dsh@sha256:" + "b".repeat(64)),
            t -> t.getDshBuilder().setUrl("https://example.com/archive"),
            t -> t.getDshBuilder().setDigest("sha256:" + "a".repeat(64)),
            t ->
                t.getDshBuilder()
                    .setSigned(
                        SignedArtifact.newBuilder()
                            .setSignature(ByteString.copyFrom(new byte[] {1}))),
            t -> t.setRuntimeValue(999),
            t ->
                t.getDshBuilder()
                    .getReleaseEnvelopeBuilder()
                    .setUnknownFields(
                        UnknownFieldSet.newBuilder()
                            .addField(99, UnknownFieldSet.Field.newBuilder().addVarint(1).build())
                            .build()));
    for (var mutate : mutations) {
      var t = template();
      mutate.accept(t);
      assertThrows(
          IllegalArgumentException.class, () -> new WorkspaceTemplates(List.of(t.build())));
    }
  }

  @Test
  void legacyArchiveStatementMustMatchFormalRelease() {
    var t = template();
    var d = t.getDshBuilder();
    var digest = "sha256:" + "b".repeat(64);
    d.setDigest(digest).setUrl("https://example.com/dsh.tgz");
    d.getReleaseEnvelopeBuilder().setArchiveDigest(digest);
    d.setSigned(
        SignedArtifact.newBuilder()
            .setSigningKid("test-root")
            .setSignature(ByteString.copyFrom(new byte[64]))
            .setStatement(
                ArtifactStatement.newBuilder()
                    .setPurpose("dsh_runtime")
                    .setName("dsh")
                    .setVersion("1.0.0")
                    .setOs("linux")
                    .setArch("amd64")
                    .setDigest(digest)
                    .setIssuedAt(d.getReleaseEnvelope().getIssuedAt())));
    assertDoesNotThrow(() -> new WorkspaceTemplates(List.of(t.build())));
    d.getSignedBuilder().getStatementBuilder().setDigest("sha256:" + "c".repeat(64));
    assertThrows(IllegalArgumentException.class, () -> new WorkspaceTemplates(List.of(t.build())));
  }
}
