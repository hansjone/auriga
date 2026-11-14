package auriga.platform;

import auriga.common.error.*;
import auriga.contracts.node.v1.*;
import auriga.contracts.org.v1.MemberStatus;
import auriga.contracts.resource.v1.ResourceRevocationList;
import auriga.resource.api.ResourceWorkspace;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.app.*;
import java.util.*;

/** Resource wiring uses workspace's tenant transaction and spec writer, never resource-side SQL. */
public final class PlatformResourceWorkspace implements ResourceWorkspace {
  /** Kept separate because the legacy and unified ports have different Snapshot return types. */
  public static final class Unified implements auriga.resource.api.UnifiedResourceWorkspace {
    private final WorkspaceUnifiedResources resources;

    public Unified(WorkspaceUnifiedResources resources) {
      this.resources = resources;
    }

    private Snapshot view(WorkspaceUnifiedResources.View view) {
      return new Snapshot(
          view.instance(),
          view.owner(),
          view.node(),
          view.spec().getResourceSelectionRevision(),
          view.spec().getGeneration(),
          view.observedGeneration(),
          view.ready(),
          view.reports(),
          view.spec().getUnifiedResourcesList());
    }

    @Override
    public Snapshot snapshot(UUID tenant, UUID instance) {
      return view(resources.snapshot(tenant, instance));
    }

    @Override
    public Snapshot apply(
        UUID tenant,
        UUID instance,
        UUID owner,
        long revision,
        boolean administrator,
        List<auriga.contracts.resource.v1.UnifiedSelection> selections,
        ResourceRevocationList revocations) {
      var applied = resources.apply(tenant, instance, owner, revision, selections);
      resources.revoke(tenant, instance, revocations);
      return view(resources.snapshot(tenant, applied.instance()));
    }

    @Override
    public Snapshot refresh(
        UUID tenant,
        UUID instance,
        UUID owner,
        long revision,
        auriga.contracts.resource.v1.ResolveUnifiedResourcesResponse resolution) {
      return view(resources.refresh(tenant, instance, owner, revision, resolution));
    }

    @Override
    public void revoke(UUID tenant, UUID instance, ResourceRevocationList revocations) {
      resources.revoke(tenant, instance, revocations);
    }
  }

  private final WorkspaceService service;
  private final WorkspaceInputs inputs;
  private final NodeEnrollment enrollment;

  public PlatformResourceWorkspace(
      WorkspaceService service, WorkspaceInputs inputs, NodeEnrollment enrollment) {
    this.service = service;
    this.inputs = inputs;
    this.enrollment = enrollment;
  }

  @Override
  public List<UUID> tenants() {
    return service.store.tenants();
  }

  @Override
  public UUID authenticate(java.security.cert.X509Certificate[] chain) {
    if (chain == null || chain.length == 0) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    return enrollment.identity(chain[0]);
  }

  @Override
  public UUID nodeTenant(UUID node) {
    return service.store.route(node, "node");
  }

  private static long number(Map<String, Object> row, String key) {
    return ((Number) row.get(key)).longValue();
  }

  private Snapshot view(Map<String, Object> row) {
    var spec = WorkspaceService.spec(row);
    String error = "";
    if (row.get("status") instanceof byte[] bytes)
      try {
        var status = InstanceStatus.parseFrom(bytes);
        if (status.getObservedGeneration() == number(row, "generation")
            && status.hasError()
            && !status.getError().getCode().isBlank()) error = "workspace.apply_failed";
      } catch (com.google.protobuf.InvalidProtocolBufferException e) {
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      }
    return new Snapshot(
        (UUID) row.get("id"),
        (UUID) row.get("owner_id"),
        (UUID) row.get("node_id"),
        spec.getResourceSelectionRevision(),
        number(row, "generation"),
        number(row, "observed_generation"),
        (Boolean) row.get("ready"),
        error,
        spec.getPluginsList());
  }

  @Override
  public Snapshot snapshot(UUID tenant, UUID instance) {
    return service.store.in(tenant, () -> view(service.store.instance(instance)));
  }

  @Override
  public Snapshot apply(
      UUID tenant,
      UUID instance,
      UUID owner,
      long revision,
      boolean administrator,
      List<Selection> changes,
      ResourceRevocationList revocations) {
    return update(tenant, instance, owner, revision, administrator, changes, revocations, false);
  }

  @Override
  public Snapshot refresh(
      UUID tenant,
      UUID instance,
      UUID owner,
      long revision,
      List<Selection> changes,
      ResourceRevocationList revocations) {
    return update(tenant, instance, owner, revision, false, changes, revocations, true);
  }

  private Snapshot update(
      UUID tenant,
      UUID instance,
      UUID owner,
      long revision,
      boolean administrator,
      List<Selection> changes,
      ResourceRevocationList revocations,
      boolean refresh) {
    return service.store.in(
        tenant,
        () -> {
          service.store.lock();
          var row = service.store.instance(instance);
          var previous = WorkspaceService.spec(row);
          var currentRevocations = effective(previous, revocations);
          if (!owner.equals(row.get("owner_id"))
              || inputs.member(tenant, owner).getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE)
            throw new AurigaException(CommonErrors.FORBIDDEN);
          if (previous.getResourceSelectionRevision() > revision)
            throw new AurigaException(CommonErrors.CONFLICT);
          if (refresh && previous.getResourceSelectionRevision() != revision)
            throw new AurigaException(CommonErrors.CONFLICT);
          boolean replay = !refresh && previous.getResourceSelectionRevision() == revision;
          Map<String, PluginRef> plugins = new LinkedHashMap<>();
          previous.getPluginsList().forEach(p -> plugins.put(p.getName(), p));
          for (var change : replay ? List.<Selection>of() : changes) {
            if (refresh && !change.action().equals("retain"))
              throw new AurigaException(CommonErrors.FORBIDDEN);
            var plugin = change.plugin();
            var old = plugins.get(plugin.getName());
            if (plugin.getLevel() == PluginLevel.PLUGIN_LEVEL_L0
                || old != null && old.getLevel() == PluginLevel.PLUGIN_LEVEL_L0)
              throw new AurigaException(CommonErrors.FORBIDDEN);
            if (plugin.getLevel() == PluginLevel.PLUGIN_LEVEL_L4
                && !Boolean.TRUE.equals(inputs.policy(tenant).getAllowPersonalPlugins()))
              throw new AurigaException(CommonErrors.FORBIDDEN);
            if (old != null && old.hasResourceEnvelope()) {
              var before = old.getResourceEnvelope();
              var after = plugin.getResourceEnvelope();
              if (!before.getPackageId().equals(after.getPackageId())
                  || after.getReleaseSequence() < before.getReleaseSequence()
                  || after.getReleaseSequence() == before.getReleaseSequence()
                      && (!after.getVersionId().equals(before.getVersionId())
                          || !after
                              .getStatement()
                              .getDigest()
                              .equals(before.getStatement().getDigest())))
                throw new AurigaException(CommonErrors.CONFLICT);
            }
            switch (change.action()) {
              case "retain" -> {
                if (old == null
                    || !old.hasResourceEnvelope()
                    || !old.getResourceEnvelope()
                        .getVersionId()
                        .equals(plugin.getResourceEnvelope().getVersionId())
                    || old.getResourceEnvelope().getReleaseSequence()
                        != plugin.getResourceEnvelope().getReleaseSequence()
                    || !old.getDigest().equals(plugin.getDigest()))
                  throw new AurigaException(CommonErrors.CONFLICT);
                plugins.put(
                    plugin.getName(), plugin.toBuilder().setEnabled(old.getEnabled()).build());
              }
              case "install", "enable" ->
                  plugins.put(plugin.getName(), plugin.toBuilder().setEnabled(true).build());
              case "disable" -> {
                if (!administrator && plugin.getLevel() == PluginLevel.PLUGIN_LEVEL_L1
                    || old == null) throw new AurigaException(CommonErrors.FORBIDDEN);
                plugins.put(plugin.getName(), old.toBuilder().setEnabled(false).build());
              }
              case "remove" -> {
                if (!administrator
                        && (plugin.getLevel() == PluginLevel.PLUGIN_LEVEL_L1
                            || plugin.getLevel() == PluginLevel.PLUGIN_LEVEL_L2)
                    || old == null) throw new AurigaException(CommonErrors.FORBIDDEN);
                plugins.remove(plugin.getName());
              }
              default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
            }
          }
          boolean affected = affected(plugins.values(), currentRevocations);
          if (affected)
            plugins.replaceAll(
                (name, plugin) ->
                    revoked(plugin, currentRevocations)
                        ? plugin.toBuilder().setEnabled(false).build()
                        : plugin);
          if ((refresh || replay)
              && (!affected || previous.getDesired() == DesiredState.DESIRED_STATE_STOPPED)
              && previous.getPluginsList().equals(new ArrayList<>(plugins.values()))
              && previous.getResourceRevocationsList().equals(List.of(currentRevocations)))
            return view(row);
          if (number(row, "generation") >= 9007199254740991L)
            throw new AurigaException(CommonErrors.CONFLICT);
          var next =
              previous.toBuilder()
                  .clearPlugins()
                  .addAllPlugins(plugins.values())
                  .setResourceSelectionRevision(revision)
                  .setGeneration(number(row, "generation") + 1)
                  .clearResourceRevocations()
                  .addResourceRevocations(currentRevocations)
                  .build();
          if (affected)
            next = next.toBuilder().setDesired(DesiredState.DESIRED_STATE_STOPPED).build();
          service.saveSpec(row, next);
          return view(service.store.instance(instance));
        });
  }

  private static ResourceRevocationList effective(
      InstanceSpec spec, ResourceRevocationList incoming) {
    return spec.getResourceRevocationsList().stream()
        .filter(
            r ->
                r.getTenantId().equals(incoming.getTenantId())
                    && r.getRevision() > incoming.getRevision())
        .max(Comparator.comparingLong(ResourceRevocationList::getRevision))
        .orElse(incoming);
  }

  private static boolean affected(Collection<PluginRef> plugins, ResourceRevocationList list) {
    return plugins.stream().anyMatch(p -> revoked(p, list));
  }

  private static boolean revoked(PluginRef plugin, ResourceRevocationList list) {
    if (!plugin.hasResourceEnvelope()) return false;
    var envelope = plugin.getResourceEnvelope();
    return list.getRevokedKidsList().contains(envelope.getSignature().getKid())
        || list.getRevokedArtifactsList().stream()
            .anyMatch(
                a ->
                    a.getPackageId().equals(envelope.getPackageId())
                        && a.getVersionId().equals(envelope.getVersionId())
                        && a.getDigest().equals(plugin.getDigest()));
  }

  @Override
  public void revoke(UUID tenant, UUID instance, ResourceRevocationList revocations) {
    service.store.in(
        tenant,
        () -> {
          service.store.lock();
          var row = service.store.instance(instance);
          var old = WorkspaceService.spec(row);
          var effective = effective(old, revocations);
          boolean affected = affected(old.getPluginsList(), effective);
          var plugins =
              old.getPluginsList().stream()
                  .map(p -> revoked(p, effective) ? p.toBuilder().setEnabled(false).build() : p)
                  .toList();
          if (old.getResourceRevocationsList().equals(List.of(effective))
              && old.getPluginsList().equals(plugins)
              && (!affected || old.getDesired() == DesiredState.DESIRED_STATE_STOPPED)) return null;
          if (old.getGeneration() >= 9007199254740991L)
            throw new AurigaException(CommonErrors.CONFLICT);
          var next =
              old.toBuilder()
                  .clearResourceRevocations()
                  .addResourceRevocations(effective)
                  .clearPlugins()
                  .addAllPlugins(plugins)
                  .setGeneration(old.getGeneration() + 1);
          if (affected) next.setDesired(DesiredState.DESIRED_STATE_STOPPED);
          service.saveSpec(row, next.build());
          return null;
        });
  }
}
