package auriga.resource.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.resource.v1.*;
import auriga.resource.api.UnifiedResourceWorkspace.Snapshot;
import java.util.List;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class CatalogMarketReportsTest {
  @Test
  void marketNeedsTheCurrentNodeVersionRevisionAndGeneration() {
    var instance = UUID.randomUUID();
    var owner = UUID.randomUUID();
    var node = UUID.randomUUID();
    var resource =
        UnifiedResourceRef.newBuilder()
            .setIdentity(
                UnifiedResourceIdentity.newBuilder().setVersionId(UUID.randomUUID().toString()))
            .setInstallationRevision(4)
            .setEnabled(true)
            .build();
    var report =
        UnifiedInstallationReport.newBuilder()
            .setClientId(instance.toString())
            .setIdentity(resource.getIdentity())
            .setInstallationRevision(4)
            .setSelectionRevision(9)
            .setGeneration(38)
            .setObservedGeneration(38)
            .setReportSource("node")
            .setState("applied")
            .build();
    var current = new Snapshot(instance, owner, node, 9, 38, 38, true, List.of(report));
    assertTrue(CatalogStatePolicy.currentNodeReport(resource, current, report));
    for (var incorrect :
        List.of(
            report.toBuilder().setClientId(UUID.randomUUID().toString()),
            report.toBuilder()
                .setIdentity(
                    resource.getIdentity().toBuilder().setVersionId(UUID.randomUUID().toString())),
            report.toBuilder().setReportSource("member"),
            report.toBuilder().setInstallationRevision(3),
            report.toBuilder().setSelectionRevision(8),
            report.toBuilder().setGeneration(37).setObservedGeneration(37),
            report.toBuilder().setObservedGeneration(37),
            report.toBuilder().setState("removed"))) {
      assertFalse(CatalogStatePolicy.currentNodeReport(resource, current, incorrect.build()));
    }
    var notReady = new Snapshot(instance, owner, node, 9, 38, 38, false, List.of(report));
    assertFalse(CatalogStatePolicy.currentNodeReport(resource, notReady, report));
    assertTrue(
        CatalogStatePolicy.currentNodeReport(
            resource, notReady, report.toBuilder().setState("pending_restart").build()));
  }
}
