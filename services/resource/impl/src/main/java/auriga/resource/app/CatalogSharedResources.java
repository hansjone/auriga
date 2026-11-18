package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.json;
import static auriga.resource.persistence.ResourceWorkflowStore.number;
import static auriga.resource.web.ResourceBody.JSON;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.UuidV7;
import auriga.contracts.node.v1.DshInstanceMode;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.node.v1.SharedDshResource;
import auriga.contracts.node.v1.SharedDshResourceView;
import auriga.resource.api.SharedResourceNodes;
import auriga.resource.api.SharedResourceResolution;
import auriga.resource.persistence.ResourceStore;
import auriga.resource.persistence.ResourceWorkflowStore;
import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import java.util.function.Supplier;
import tools.jackson.databind.JsonNode;

/** The unified catalog remains the only source of shared resource identity and signed leases. */
public final class CatalogSharedResources implements SharedResourceResolution {
  private final ResourceStore tx;
  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final CatalogDelivery delivery;
  private final CatalogWorkflow catalog;
  private final ResourceDistributions releases;
  private final Clock clock;
  private final Supplier<SharedResourceNodes> nodes;
  private final CatalogSharedInstallations installations;

  public CatalogSharedResources(
      ResourceStore tx,
      ResourceWorkflowStore db,
      ResourceAccess access,
      CatalogDelivery delivery,
      CatalogWorkflow catalog,
      ResourceDistributions releases,
      Clock clock,
      Supplier<SharedResourceNodes> nodes) {
    this.tx = tx;
    this.db = db;
    this.access = access;
    this.delivery = delivery;
    this.catalog = catalog;
    this.releases = releases;
    this.clock = clock;
    this.nodes = nodes;
    this.installations = new CatalogSharedInstallations(db);
  }

  private void tenant(UUID tenant) {
    if (!TenantContext.require().equals(tenant) || tenant.equals(TenantContext.PLATFORM_TENANT))
      throw new AurigaException(CommonErrors.NOT_FOUND);
  }

  private void binding(SharedDshBinding binding) {
    tenant(UUID.fromString(binding.getTenantId()));
    UUID.fromString(binding.getMemberId());
    UUID.fromString(binding.getPlacementId());
    UUID.fromString(binding.getPhysicalInstanceId());
    if ((binding.getMode() != DshInstanceMode.DSH_INSTANCE_MODE_SHARED
            && binding.getMode() != DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED)
        || binding.getEpoch() < 1
        || binding.getRevision() < 1
        || binding.getEpoch() > ResourceOperations.MAX
        || binding.getRevision() > ResourceOperations.MAX
        || !binding.getUnknownFields().asMap().isEmpty())
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }

  static String digest(UUID tenant, Map<UUID, CatalogAssignments.Effective> selections) {
    var value = JSON.createObjectNode().put("schemaVersion", 1).put("tenantId", tenant.toString());
    var resources = value.putArray("resources");
    selections.entrySet().stream()
        .sorted(Map.Entry.comparingByKey(Comparator.comparing(UUID::toString)))
        .forEach(
            entry -> {
              var selection = entry.getValue();
              var resource = JSON.createObjectNode();
              resource.set("identity", selection.identity());
              resource.put("installationMode", selection.mode());
              resource.put("releaseSequence", selection.sequence());
              resources.add(resource);
            });
    return CatalogManifest.digest(
        ResourceSigning.canonical(value).getBytes(StandardCharsets.UTF_8));
  }

  @Override
  public String authorizationSetDigest(UUID tenant, UUID member) {
    tenant(tenant);
    var reader = access.target(tenant, member);
    return tx.write(
        () -> {
          db.lock("catalog-assignments");
          return digest(tenant, delivery.sharedSelections(reader, null));
        });
  }

  @Override
  public Snapshot snapshot(SharedDshBinding binding) {
    binding(binding);
    UUID tenant = UUID.fromString(binding.getTenantId());
    UUID member = UUID.fromString(binding.getMemberId());
    UUID placement = UUID.fromString(binding.getPlacementId());
    UUID pool = UUID.fromString(binding.getPhysicalInstanceId());
    var reader = access.target(tenant, member);
    return tx.write(
        () -> {
          db.lock("catalog-assignments");
          db.lock("shared-view/" + placement);
          var selections = delivery.sharedSelections(reader, null);
          String digest = digest(tenant, selections);
          if (!digest.equals(digest(tenant, delivery.sharedSelections(reader, pool))))
            throw new AurigaException(CommonErrors.CONFLICT);
          // Match catalog delivery's package/version-before-revocations lock order, including
          // a view that can otherwise reuse its persisted signed material.
          for (var selection :
              selections.entrySet().stream()
                  .sorted(Map.Entry.comparingByKey(Comparator.comparing(UUID::toString)))
                  .toList()) {
            var identity = selection.getValue().identity();
            var version =
                catalog.ver(
                    reader,
                    UUID.fromString(identity.path("packageId").asString()),
                    UUID.fromString(identity.path("versionId").asString()),
                    false);
            if (!version.get("state").equals("published") || catalog.revoked(version))
              throw new AurigaException(CommonErrors.NOT_FOUND);
          }
          var revocations = releases.revocations();
          long now = clock.instant().getEpochSecond();
          if (Instant.parse(revocations.path("issuedAt").asString()).getEpochSecond() > now
              || Instant.parse(revocations.path("expiresAt").asString()).getEpochSecond() <= now)
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          var rows =
              db.rows(
                  "SELECT * FROM resource.catalog_shared_view WHERE tenant_id=:tenant AND placement_id=:placement FOR UPDATE",
                  db.args("placement", placement));
          Map<UUID, Long> revisions;
          if (!rows.isEmpty()) {
            var row = rows.getFirst();
            if (!row.get("member_id").equals(member)
                || number(row, "epoch") > binding.getEpoch()
                || number(row, "revision") > binding.getRevision())
              throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
            var document = json(row, "document");
            boolean future =
                document
                    .path("resources")
                    .valueStream()
                    .anyMatch(
                        item ->
                            Instant.parse(
                                        item.path("resource")
                                            .path("envelope")
                                            .path("statement")
                                            .path("issuedAt")
                                            .asString())
                                    .getEpochSecond()
                                > now);
            if (future) throw new AurigaException(CommonErrors.UNAVAILABLE);
            revisions = installations.reconcile(placement, selections);
            if (row.get("pool_id").equals(pool)
                && document.path("schemaVersion").asInt() == 2
                && row.get("mode").equals(mode(binding))
                && number(row, "epoch") == binding.getEpoch()
                && number(row, "revision") == binding.getRevision()
                && row.get("authorization_digest").equals(digest)
                && number(row, "revocation_revision") == revocations.path("revision").asLong()
                && number(row, "expires_at") > now + 300)
              return snapshot(binding, (UUID) row.get("view_id"), digest, document);
          } else revisions = installations.reconcile(placement, selections);
          var document = JSON.createObjectNode().put("schemaVersion", 2);
          document.set("revocations", revocations);
          var resources = document.putArray("resources");
          long expires = Instant.parse(revocations.path("expiresAt").asString()).getEpochSecond();
          for (var selection :
              selections.entrySet().stream()
                  .sorted(Map.Entry.comparingByKey(Comparator.comparing(UUID::toString)))
                  .toList()) {
            // Optional choices are authorized metadata, never installed into the common process.
            if (selection.getValue().mode().equals("optional")) continue;
            var ref = delivery.sharedRef(reader, selection.getValue());
            long end =
                Instant.parse(ref.path("lease").path("expiresAt").asString()).getEpochSecond();
            if (end <= clock.instant().getEpochSecond())
              throw new AurigaException(CommonErrors.UNAVAILABLE);
            String leaseId = UuidV7.next().toString();
            String download = "/internal/resource/v1/catalog/downloads/" + leaseId;
            ref.put("manifestDownloadPath", download + "/manifest");
            for (var artifact : ref.path("artifacts"))
              ((tools.jackson.databind.node.ObjectNode) artifact)
                  .put(
                      "downloadPath",
                      download + "/artifacts/" + artifact.path("artifactId").asString());
            CatalogStatePolicy.dispatch(ref, revisions.get(selection.getKey()), "ENABLED");
            resources.add(JSON.createObjectNode().put("leaseId", leaseId).set("resource", ref));
            expires = Math.min(expires, end);
          }
          if (expires <= clock.instant().getEpochSecond())
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          UUID view = UuidV7.next();
          db.update(
              "INSERT INTO resource.catalog_shared_view(tenant_id,placement_id,member_id,pool_id,mode,epoch,revision,view_id,authorization_digest,revocation_revision,expires_at,document) VALUES(:tenant,:placement,:member,:pool,:mode,:epoch,:revision,:view,:digest,:revocations,:expires,CAST(:document AS jsonb)) ON CONFLICT(tenant_id,placement_id) DO UPDATE SET member_id=excluded.member_id,pool_id=excluded.pool_id,mode=excluded.mode,epoch=excluded.epoch,revision=excluded.revision,view_id=excluded.view_id,authorization_digest=excluded.authorization_digest,revocation_revision=excluded.revocation_revision,expires_at=excluded.expires_at,document=excluded.document",
              db.args(
                  "placement", placement,
                  "member", member,
                  "pool", pool,
                  "mode", mode(binding),
                  "epoch", binding.getEpoch(),
                  "revision", binding.getRevision(),
                  "view", view,
                  "digest", digest,
                  "revocations", revocations.path("revision").asLong(),
                  "expires", expires,
                  "document", document.toString()));
          return snapshot(binding, view, digest, document);
        });
  }

  private static String mode(SharedDshBinding binding) {
    return binding.getMode() == DshInstanceMode.DSH_INSTANCE_MODE_SHARED ? "shared" : "dedicated";
  }

  private Snapshot snapshot(SharedDshBinding binding, UUID id, String digest, JsonNode document) {
    var view =
        SharedDshResourceView.newBuilder()
            .setSchemaVersion(1)
            .setBinding(binding)
            .setViewId(id.toString())
            .setAuthorizationSetDigest(digest)
            .setSource("platform");
    var refs = new ArrayList<auriga.contracts.resource.v1.UnifiedResourceRef>();
    for (var item : document.path("resources")) {
      var resource = item.path("resource");
      view.addResources(
          SharedDshResource.newBuilder()
              .setResourceId(resource.path("identity").path("packageId").asString())
              .setDigest(resource.path("identity").path("manifestDigest").asString())
              .setLeaseId(item.path("leaseId").asString())
              .setExpiresAt(
                  Instant.parse(resource.path("lease").path("expiresAt").asString())
                      .getEpochSecond()));
      refs.add(CatalogProtocol.ref(resource));
    }
    return new Snapshot(
        view.build(), refs, ResourceProtocol.revocations(document.path("revocations")));
  }

  @Override
  public InputStream artifact(SharedDshBinding binding, UUID leaseId, String artifactId) {
    var current = snapshot(binding);
    var lease =
        current.view().getResourcesList().stream()
            .filter(resource -> resource.getLeaseId().equals(leaseId.toString()))
            .findFirst()
            .orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
    var reference =
        current.resources().stream()
            .filter(resource -> resource.getIdentity().getPackageId().equals(lease.getResourceId()))
            .findFirst()
            .orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
    var reader =
        access.target(
            UUID.fromString(binding.getTenantId()), UUID.fromString(binding.getMemberId()));
    return tx.write(
        () -> {
          db.lock("catalog-assignments");
          if (!digest(
                  UUID.fromString(binding.getTenantId()),
                  delivery.sharedSelections(
                      reader, UUID.fromString(binding.getPhysicalInstanceId())))
              .equals(current.view().getAuthorizationSetDigest()))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          if (!Boolean.TRUE.equals(
              catalog
                  .pkg(reader, UUID.fromString(reference.getIdentity().getPackageId()), false)
                  .get("enabled"))) throw new AurigaException(CommonErrors.NOT_FOUND);
          var version =
              catalog.ver(
                  reader,
                  UUID.fromString(reference.getIdentity().getPackageId()),
                  UUID.fromString(reference.getIdentity().getVersionId()),
                  false);
          if (!version.get("state").equals("published") || catalog.revoked(version))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          if (artifactId == null) {
            byte[] manifest = (byte[]) version.get("manifest_bytes");
            if (!CatalogManifest.digest(manifest)
                .equals(reference.getIdentity().getManifestDigest()))
              throw new AurigaException(CommonErrors.NOT_FOUND);
            return new ByteArrayInputStream(manifest);
          }
          var artifact =
              reference.getArtifactsList().stream()
                  .filter(a -> a.getArtifactId().equals(artifactId))
                  .findFirst()
                  .orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
          return catalog.nodeArtifact(
              reader, version, artifactId, artifact.getDigest(), artifact.getSizeBytes());
        });
  }

  /** Same finite mTLS node locator as ordinary catalog delivery, with independent member leases. */
  public Optional<InputStream> nodeDownload(
      UUID tenant, UUID node, UUID lease, String artifact, boolean range) {
    if (range) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    return TenantContext.call(
        tenant,
        () -> {
          var rows =
              tx.write(
                  () ->
                      db.rows(
                          "SELECT * FROM resource.catalog_shared_view WHERE tenant_id=:tenant AND document->'resources' @> CAST(:lease AS jsonb)",
                          db.args(
                              "lease",
                              JSON.createArrayNode()
                                  .add(JSON.createObjectNode().put("leaseId", lease.toString()))
                                  .toString())));
          if (rows.isEmpty()) return Optional.empty();
          if (rows.size() != 1) throw new AurigaException(CommonErrors.NOT_FOUND);
          var row = rows.getFirst();
          var binding =
              SharedDshBinding.newBuilder()
                  .setTenantId(tenant.toString())
                  .setMemberId(row.get("member_id").toString())
                  .setPlacementId(row.get("placement_id").toString())
                  .setPhysicalInstanceId(row.get("pool_id").toString())
                  .setMode(
                      row.get("mode").equals("shared")
                          ? DshInstanceMode.DSH_INSTANCE_MODE_SHARED
                          : DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED)
                  .setEpoch(number(row, "epoch"))
                  .setRevision(number(row, "revision"))
                  .build();
          var authority = nodes.get();
          if (authority == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
          String expected = authority.requireCurrent(tenant, node, binding);
          var current = snapshot(binding);
          if (!current.view().getAuthorizationSetDigest().equals(expected))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          return Optional.of(artifact(binding, lease, artifact));
        });
  }
}
