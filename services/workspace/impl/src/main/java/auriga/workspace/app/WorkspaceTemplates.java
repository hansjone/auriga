package auriga.workspace.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.*;
import com.google.protobuf.Message;
import com.google.protobuf.Timestamp;
import java.util.Base64;
import java.util.List;

/**
 * Deployment-provisioned formal release templates. This validates shape and mapping only; the agent
 * verifies the signature against its pinned trusted keyring before execution.
 */
public final class WorkspaceTemplates {
  private final List<InstanceSpec> templates;

  public WorkspaceTemplates(List<InstanceSpec> templates) {
    this.templates = List.copyOf(templates);
    for (var template : templates) {
      if (!valid(template))
        throw new IllegalArgumentException("Invalid runtime release workspace template");
    }
  }

  private static boolean known(Message message) {
    return message.getUnknownFields().asMap().isEmpty();
  }

  private static boolean digest(String value) {
    return value.matches("sha256:[a-f0-9]{64}");
  }

  private static boolean timestamp(Timestamp value) {
    return known(value)
        && value.getSeconds() >= -62135596800L
        && value.getSeconds() <= 253402300799L
        && value.getNanos() >= 0
        && value.getNanos() <= 999999999;
  }

  private static boolean valid(InstanceSpec t) {
    if (!known(t)
        || !t.hasDsh()
        || !known(t.getDsh())
        || !t.hasResources()
        || !known(t.getResources())
        || !t.hasProfile()
        || !known(t.getProfile())
        || t.getRuntime() == RuntimeKind.RUNTIME_KIND_UNSPECIFIED
        || t.getRuntime() == RuntimeKind.UNRECOGNIZED
        || t.getResources().getCpuMillis() < 100
        || t.getResources().getMemoryMb() < 256
        || t.getResources().getDiskGb() < 1
        || !t.getDsh().hasReleaseEnvelope()) return false;
    var d = t.getDsh();
    var e = d.getReleaseEnvelope();
    if (!known(e)
        || e.getSchemaVersion() != 1
        || !e.getPurpose().equals("dsh_runtime")
        || !e.getName().equals("dsh")
        || e.getArtifactId().isEmpty()
        || !e.getArtifactId().equals(d.getArtifactId())
        || e.getDshVersion().isEmpty()
        || !e.getDshVersion().equals(d.getVersion())
        || !(e.getOs().equals("linux") || e.getOs().equals("windows"))
        || !(e.getArch().equals("amd64") || e.getArch().equals("arm64"))
        || e.getReleaseSequence() < 1
        || e.getReleaseSequence() > 9007199254740991L
        || !e.hasIssuedAt()
        || !timestamp(e.getIssuedAt())
        || !e.hasSignature()
        || !known(e.getSignature())
        || !e.getSignature().getAlg().equals("Ed25519")
        || !e.getSignature().getKid().matches("[A-Za-z0-9._-]{1,128}")
        || !e.getArchiveDigest().equals(d.getDigest())
        || (!d.getDigest().isEmpty() && !digest(d.getDigest()))) return false;
    var encoded = e.getSignature().getValue();
    try {
      var raw = Base64.getUrlDecoder().decode(encoded);
      if (raw.length != 64
          || !Base64.getUrlEncoder().withoutPadding().encodeToString(raw).equals(encoded))
        return false;
    } catch (IllegalArgumentException invalid) {
      return false;
    }
    if (t.getRuntime() == RuntimeKind.RUNTIME_KIND_DOCKER) {
      if (!digest(e.getImageDigest()) || !d.getImage().matches("[^@\\s]+@" + e.getImageDigest()))
        return false;
      if (d.getDigest().isEmpty() && (!d.getUrl().isEmpty() || d.hasSigned())) return false;
    } else if (!e.getImageDigest().isEmpty() || !d.getImage().isEmpty() || d.getDigest().isEmpty())
      return false;
    if (d.hasSigned()) {
      var signed = d.getSigned();
      if (!known(signed)
          || signed.getSignature().size() != 64
          || !signed.getSigningKid().matches("[A-Za-z0-9._-]{1,128}")
          || !signed.hasStatement()) return false;
      var s = signed.getStatement();
      if (!known(s)
          || !s.getPurpose().equals(e.getPurpose())
          || !s.getName().equals(e.getName())
          || !s.getVersion().equals(e.getDshVersion())
          || !s.getOs().equals(e.getOs())
          || !s.getArch().equals(e.getArch())
          || !s.getDigest().equals(e.getArchiveDigest())
          || !s.hasIssuedAt()
          || !timestamp(s.getIssuedAt())
          || !s.getIssuedAt().equals(e.getIssuedAt())) return false;
    }
    return true;
  }

  public List<InstanceSpec> all() {
    return templates;
  }

  public InstanceSpec get(String version, RuntimeKind runtime, String os, String arch) {
    return templates.stream()
        .filter(
            t ->
                (version == null || version.isBlank() || t.getDsh().getVersion().equals(version))
                    && (runtime == null || t.getRuntime() == runtime)
                    && (os == null || t.getDsh().getReleaseEnvelope().getOs().equals(os))
                    && (arch == null || t.getDsh().getReleaseEnvelope().getArch().equals(arch)))
        .findFirst()
        .orElseThrow(() -> new AurigaException(CommonErrors.UNAVAILABLE));
  }
}
