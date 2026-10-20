package auriga.workspace.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.*;
import java.util.List;

/** Deployment-provisioned signed templates; source of truth for immutable artifact references. */
public final class WorkspaceTemplates {
  private final List<InstanceSpec> templates;

  public WorkspaceTemplates(List<InstanceSpec> templates) {
    this.templates = List.copyOf(templates);
    for (var template : templates) {
      if (!template.getDsh().hasSigned()
          || template.getDsh().getSigned().getSignature().isEmpty()
          || template.getRuntime() == RuntimeKind.RUNTIME_KIND_UNSPECIFIED
          || template.getResources().getCpuMillis() < 100
          || template.getResources().getMemoryMb() < 256
          || template.getResources().getDiskGb() < 1)
        throw new IllegalArgumentException("Invalid signed workspace template");
    }
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
                    && (os == null || t.getDsh().getSigned().getStatement().getOs().equals(os))
                    && (arch == null
                        || t.getDsh().getSigned().getStatement().getArch().equals(arch)))
        .findFirst()
        .orElseThrow(() -> new AurigaException(CommonErrors.UNAVAILABLE));
  }
}
