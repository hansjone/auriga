package auriga.workspace.app;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.events.*;
import auriga.common.persistence.UuidV7;
import auriga.contracts.node.v1.*;
import auriga.contracts.org.v1.MemberStatus;
import auriga.contracts.resource.v1.*;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.domain.UnifiedResourceIntent;
import auriga.workspace.domain.UnifiedSpec;
import java.util.*;
import java.util.concurrent.*;
import java.util.function.*;

/** Durable resource intents; network resolution always runs outside workspace transactions. */
public final class WorkspaceUnifiedResources implements EventHandler, AutoCloseable {
  public record View(
      UUID instance,
      UUID owner,
      UUID node,
      InstanceSpec spec,
      long observedGeneration,
      boolean ready,
      List<UnifiedInstallationReport> reports) {}

  private final WorkspaceService service;
  private final WorkspaceInputs inputs;
  private final Function<ResolveUnifiedResourcesRequest, ResolveUnifiedResourcesResponse> resolver;
  private final ScheduledExecutorService worker =
      Executors.newSingleThreadScheduledExecutor(
          Thread.ofVirtual().name("workspace-unified-resources").factory());

  public WorkspaceUnifiedResources(
      WorkspaceService service,
      WorkspaceInputs inputs,
      Function<ResolveUnifiedResourcesRequest, ResolveUnifiedResourcesResponse> resolver) {
    this.service = service;
    this.inputs = inputs;
    this.resolver = resolver;
  }

  public void start() {
    worker.scheduleWithFixedDelay(this::reconcile, 0, 5, TimeUnit.SECONDS);
  }

  @Override
  public void close() {
    worker.shutdownNow();
  }

  @Override
  public String name() {
    return "unified-resource-intents";
  }

  @Override
  public Set<String> eventTypes() {
    return Set.of("resource.catalog_distribution_requested", "resource.catalog_version_withdrawn");
  }

  @Override
  public void handle(ReceivedEvent event) {
    if (!eventTypes().contains(event.type()) || !event.source().equals("auriga/resource"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (event.type().equals("resource.catalog_version_withdrawn")) {
      service.store.in(
          event.tenantId(),
          () ->
              service.store.jdbc.update(
                  "UPDATE instance SET unified_refresh_pending=true WHERE tenant_id=? AND NOT deleted",
                  event.tenantId()));
      return;
    }
    var data = event.data();
    var selections = new ArrayList<UnifiedSelection>();
    for (var item : data.path("selections")) {
      var id = item.path("identity");
      selections.add(
          UnifiedSelection.newBuilder()
              .setAction(item.path("action").asString())
              .setIdentity(
                  UnifiedResourceIdentity.newBuilder()
                      .setTenantId(id.path("tenantId").asString())
                      .setPackageId(id.path("packageId").asString())
                      .setVersionId(id.path("versionId").asString())
                      .setKind(id.path("kind").asString())
                      .setVersion(id.path("version").asString())
                      .setManifestDigest(id.path("manifestDigest").asString()))
              .build());
    }
    record(
        event.tenantId(),
        UUID.fromString(data.path("instanceId").asString()),
        UUID.fromString(data.path("ownerId").asString()),
        data.path("selectionRevision").asLong(),
        selections);
  }

  public void record(
      UUID tenant,
      UUID instance,
      UUID owner,
      long selectionRevision,
      List<UnifiedSelection> selections) {
    if (selectionRevision < 1 || selectionRevision > 9007199254740991L || selections.size() > 128)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    service.store.in(
        tenant,
        () -> {
          service.store.lock();
          var row = service.store.instance(instance);
          authorize(tenant, owner, row);
          byte[] bytes =
              UnifiedDistributionInput.newBuilder()
                  .setInstanceId(instance.toString())
                  .addAllSelections(selections)
                  .build()
                  .toByteArray();
          var saved =
              service.store.jdbc.queryForList(
                  "SELECT request,owner_id FROM unified_resource_intent WHERE tenant_id=? AND instance_id=? AND selection_revision=?",
                  tenant,
                  instance,
                  selectionRevision);
          if (!saved.isEmpty()) {
            if (!Arrays.equals(bytes, (byte[]) saved.getFirst().get("request"))
                || !owner.equals(saved.getFirst().get("owner_id")))
              throw new AurigaException(CommonErrors.CONFLICT);
            return null;
          }
          var spec = WorkspaceService.spec(row);
          boolean contained =
              spec.getResourceSelectionRevision() >= selectionRevision
                  && selections.stream()
                      .allMatch(
                          selection ->
                              spec.getUnifiedResourcesList().stream()
                                  .anyMatch(
                                      ref ->
                                          ref.getIdentity().equals(selection.getIdentity())
                                              && UnifiedResourceIntent.desired(ref)
                                                  .equals(
                                                      switch (selection.getAction()) {
                                                        case "install", "enable" -> "ENABLED";
                                                        case "disable" -> "DISABLED";
                                                        case "remove" -> "REMOVED";
                                                        default ->
                                                            throw new AurigaException(
                                                                CommonErrors.VALIDATION_FAILED);
                                                      })));
          service.store.jdbc.update(
              "INSERT INTO unified_resource_intent(id,tenant_id,instance_id,owner_id,selection_revision,request,applied) VALUES(?,?,?,?,?,?,?)",
              UuidV7.next(service.clock),
              tenant,
              instance,
              owner,
              selectionRevision,
              bytes,
              contained);
          return null;
        });
  }

  public View apply(
      UUID tenant,
      UUID instance,
      UUID owner,
      long selectionRevision,
      List<UnifiedSelection> selections) {
    record(tenant, instance, owner, selectionRevision, selections);
    var before = snapshot(tenant, instance);
    if (before.spec().getResourceSelectionRevision() >= selectionRevision) {
      boolean applied =
          service.store.in(
              tenant,
              () ->
                  Boolean.TRUE.equals(
                      service.store.jdbc.queryForObject(
                          "SELECT applied FROM unified_resource_intent WHERE tenant_id=? AND instance_id=? AND selection_revision=?",
                          Boolean.class,
                          tenant,
                          instance,
                          selectionRevision)));
      if (!applied) throw new AurigaException(CommonErrors.CONFLICT);
      return before;
    }
    if (before.spec().getResourceSelectionRevision() + 1 != selectionRevision) return before;
    if (before.node() == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    var resolution =
        TenantContext.call(
            tenant,
            () ->
                resolver.apply(
                    ResolveUnifiedResourcesRequest.newBuilder()
                        .setTenantId(tenant.toString())
                        .setOwnerId(owner.toString())
                        .setInstanceId(instance.toString())
                        .setNodeId(before.node().toString())
                        .setSelectionRevision(selectionRevision)
                        .addAllSelections(selections)
                        .build()));
    return service.store.in(
        tenant,
        () -> {
          service.store.lock();
          var row = service.store.instance(instance);
          authorize(tenant, owner, row);
          var old = WorkspaceService.spec(row);
          if (old.getResourceSelectionRevision() >= selectionRevision) return view(row);
          if (old.getResourceSelectionRevision() + 1 != selectionRevision)
            throw new AurigaException(CommonErrors.CONFLICT);
          if (!Objects.equals(row.get("node_id"), before.node()))
            throw new AurigaException(CommonErrors.CONFLICT);
          var next = UnifiedSpec.apply(old, selectionRevision, selections, resolution, false);
          service.saveSpec(row, UnifiedSpec.bump(next));
          service.store.jdbc.update(
              "UPDATE unified_resource_intent SET applied=true WHERE tenant_id=? AND instance_id=? AND selection_revision=?",
              tenant,
              instance,
              selectionRevision);
          return view(service.store.instance(instance));
        });
  }

  public View refresh(
      UUID tenant,
      UUID instance,
      UUID owner,
      long selectionRevision,
      ResolveUnifiedResourcesResponse response) {
    return service.store.in(
        tenant,
        () -> {
          service.store.lock();
          var row = service.store.instance(instance);
          authorize(tenant, owner, row);
          var old = WorkspaceService.spec(row);
          var retained = new ArrayList<UnifiedResourceRef>();
          var renewal = response.toBuilder();
          var reports = view(row).reports();
          for (var ref : old.getUnifiedResourcesList()) {
            boolean present =
                response.getResourcesList().stream()
                    .anyMatch(
                        r ->
                            r.getIdentity()
                                .getPackageId()
                                .equals(ref.getIdentity().getPackageId()));
            if (present) retained.add(ref);
            else if (UnifiedResourceIntent.desired(ref).equals("REMOVED")
                && reports.stream()
                    .anyMatch(
                        r ->
                            r.getIdentity().equals(ref.getIdentity())
                                && r.getState().equals("removed"))) {
              // Only an exact node acknowledgement permits next-generation collection.
            } else {
              // An unavailable entry cannot prevent healthy entries renewing. Preserve
              // its original signed deadline; absence never authorizes uninstall.
              retained.add(ref);
              renewal.addResources(ref);
            }
          }
          var base =
              old.toBuilder().clearUnifiedResources().addAllUnifiedResources(retained).build();
          var next = UnifiedSpec.apply(base, selectionRevision, List.of(), renewal.build(), true);
          if (!next.equals(old)) service.saveSpec(row, UnifiedSpec.bump(next));
          service.store.jdbc.update(
              "UPDATE instance SET unified_refresh_pending=false WHERE tenant_id=? AND id=?",
              tenant,
              instance);
          return view(service.store.instance(instance));
        });
  }

  public void revoke(UUID tenant, UUID instance, ResourceRevocationList list) {
    service.store.in(
        tenant,
        () -> {
          service.store.lock();
          var row = service.store.instance(instance);
          var old = WorkspaceService.spec(row);
          var next = UnifiedSpec.revocations(old, list, true);
          if (!next.equals(old)) service.saveSpec(row, next);
          return null;
        });
  }

  public View snapshot(UUID tenant, UUID instance) {
    return service.store.in(tenant, () -> view(service.store.instance(instance)));
  }

  private View view(Map<String, Object> row) {
    var spec = WorkspaceService.spec(row);
    long observed = ((Number) row.get("observed_generation")).longValue();
    List<UnifiedInstallationReport> reports = new ArrayList<>();
    if (row.get("status") instanceof byte[] bytes) {
      try {
        var status = InstanceStatus.parseFrom(bytes);
        for (var report : status.getUnifiedResourceReportsList()) {
          if (status.getObservedGeneration() != spec.getGeneration()
              || report.getGeneration() != spec.getGeneration()
              || report.getObservedGeneration() != spec.getGeneration()
              || report.getSelectionRevision() != spec.getResourceSelectionRevision()
              || !report.getClientId().equals(spec.getInstanceId())) continue;
          if (!Set.of(
                  "applying",
                  "applied",
                  "failed",
                  "pending_restart",
                  "superseded",
                  "overridden",
                  "expired",
                  "revoked",
                  "disabled",
                  "removed")
              .contains(report.getState())) continue;
          if (report.getState().equals("applied")) {
            var guardian = status.getUnifiedGuardian();
            if (!status.getReady()
                || observed != spec.getGeneration()
                || !Boolean.TRUE.equals(row.get("ready"))
                || !guardian.getState().equals("active")
                || !guardian.getTenantId().equals(spec.getTenantId())
                || !guardian.getOwnerId().equals(spec.getOwnerId())
                || !guardian.getHostId().equals(spec.getInstanceId())
                || guardian.getGeneration() != spec.getGeneration()
                || guardian.getSelectionRevision() != spec.getResourceSelectionRevision()) continue;
          }
          boolean matches =
              spec.getUnifiedResourcesList().stream()
                  .anyMatch(
                      ref ->
                          ref.getIdentity().equals(report.getIdentity())
                              && UnifiedResourceIntent.reportMatches(ref, report.getState())
                              && UnifiedResourceIntent.revision(ref)
                                  == report.getInstallationRevision());
          if (matches) reports.add(report.toBuilder().setReportSource("node").build());
        }
      } catch (com.google.protobuf.InvalidProtocolBufferException e) {
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      }
    }
    return new View(
        (UUID) row.get("id"),
        (UUID) row.get("owner_id"),
        (UUID) row.get("node_id"),
        spec,
        observed,
        Boolean.TRUE.equals(row.get("ready")) && observed == spec.getGeneration(),
        List.copyOf(reports));
  }

  private void authorize(UUID tenant, UUID owner, Map<String, Object> row) {
    if (!owner.equals(row.get("owner_id"))
        || inputs.member(tenant, owner).getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE)
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  public CheckUnifiedResourceDownloadResponse check(CheckUnifiedResourceDownloadRequest request) {
    UUID tenant = UUID.fromString(request.getTenantId());
    return service.store.in(
        tenant,
        () -> {
          var row = service.store.instance(UUID.fromString(request.getInstanceId()));
          var owner = UUID.fromString(request.getOwnerId());
          authorize(tenant, owner, row);
          var spec = WorkspaceService.spec(row);
          boolean allowed =
              Objects.equals(row.get("node_id"), UUID.fromString(request.getNodeId()))
                  && !Boolean.TRUE.equals(
                      service.store.node(UUID.fromString(request.getNodeId())).get("revoked"))
                  && spec.getResourceSelectionRevision() == request.getSelectionRevision()
                  && spec.getGeneration() == request.getGeneration()
                  && spec.getUnifiedResourcesList().stream()
                      .anyMatch(
                          r ->
                              r.getEnabled()
                                  && r.getIdentity().equals(request.getIdentity())
                                  && service
                                      .clock
                                      .instant()
                                      .isBefore(
                                          java.time.Instant.parse(r.getLease().getExpiresAt()))
                                  && (request.getArtifactId().isEmpty()
                                      ? r.getIdentity()
                                          .getManifestDigest()
                                          .equals(request.getArtifactDigest())
                                      : r.getArtifactsList().stream()
                                          .anyMatch(
                                              a ->
                                                  a.getArtifactId().equals(request.getArtifactId())
                                                      && a.getDigest()
                                                          .equals(request.getArtifactDigest()))));
          return CheckUnifiedResourceDownloadResponse.newBuilder()
              .setAllowed(allowed)
              .setGeneration(spec.getGeneration())
              .setSelectionRevision(spec.getResourceSelectionRevision())
              .build();
        });
  }

  public void reconcile() {
    List<UUID> tenants;
    try {
      tenants = service.store.tenants();
    } catch (Exception e) {
      org.slf4j.LoggerFactory.getLogger(getClass())
          .warn("Unified tenant reconciliation unavailable");
      return;
    }
    for (UUID tenant : tenants) {
      try {
        var pending =
            service.store.in(
                tenant,
                () ->
                    service.store.jdbc.queryForList(
                        "SELECT * FROM unified_resource_intent WHERE tenant_id=? AND NOT applied ORDER BY instance_id,selection_revision ASC LIMIT 200",
                        tenant));
        for (var intent : pending) {
          try {
            var input = UnifiedDistributionInput.parseFrom((byte[]) intent.get("request"));
            apply(
                tenant,
                (UUID) intent.get("instance_id"),
                (UUID) intent.get("owner_id"),
                ((Number) intent.get("selection_revision")).longValue(),
                input.getSelectionsList());
          } catch (Exception e) {
            org.slf4j.LoggerFactory.getLogger(getClass())
                .warn("Unified resource intent reconciliation deferred");
          }
        }
        var instances =
            service.store.in(
                tenant,
                () ->
                    service.store.jdbc.queryForList(
                        "SELECT * FROM instance WHERE tenant_id=? AND NOT deleted", tenant));
        for (var row : instances) {
          var spec = WorkspaceService.spec(row);
          if (spec.getUnifiedResourcesCount() == 0 || row.get("node_id") == null) continue;
          boolean due =
              Boolean.TRUE.equals(row.get("unified_refresh_pending"))
                  || spec.getUnifiedResourcesList().stream()
                      .anyMatch(
                          r ->
                              !service
                                  .clock
                                  .instant()
                                  .plusSeconds(1800)
                                  .isBefore(java.time.Instant.parse(r.getLease().getExpiresAt())));
          if (!due) continue;
          try {
            var request =
                ResolveUnifiedResourcesRequest.newBuilder()
                    .setTenantId(tenant.toString())
                    .setInstanceId(spec.getInstanceId())
                    .setOwnerId(spec.getOwnerId())
                    .setNodeId(row.get("node_id").toString())
                    .setSelectionRevision(spec.getResourceSelectionRevision());
            var original =
                service.store.in(
                    tenant,
                    () ->
                        service.store.jdbc.queryForList(
                            "SELECT request FROM unified_resource_intent WHERE tenant_id=? AND instance_id=? AND selection_revision=? AND applied",
                            tenant,
                            row.get("id"),
                            spec.getResourceSelectionRevision()));
            if (!original.isEmpty()) {
              // A resolves the immutable original distribution, including remove
              // actions. Renewing is not a new enable/disable operation.
              request.addAllSelections(
                  UnifiedDistributionInput.parseFrom((byte[]) original.getFirst().get("request"))
                      .getSelectionsList());
            } else
              spec.getUnifiedResourcesList()
                  .forEach(
                      r ->
                          request.addSelections(
                              UnifiedSelection.newBuilder()
                                  .setIdentity(r.getIdentity())
                                  .setAction(
                                      switch (UnifiedResourceIntent.desired(r)) {
                                        case "ENABLED" -> "enable";
                                        case "DISABLED" -> "disable";
                                        case "REMOVED" -> "remove";
                                        default -> throw new AurigaException(CommonErrors.CONFLICT);
                                      })));
            refresh(
                tenant,
                (UUID) row.get("id"),
                (UUID) row.get("owner_id"),
                spec.getResourceSelectionRevision(),
                TenantContext.call(tenant, () -> resolver.apply(request.build())));
          } catch (Exception e) {
            org.slf4j.LoggerFactory.getLogger(getClass())
                .warn("Unified lease reconciliation deferred");
          }
        }
      } catch (Exception e) {
        org.slf4j.LoggerFactory.getLogger(getClass())
            .warn("Unified resource reconciliation unavailable");
      }
    }
  }
}
