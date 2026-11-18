package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.DshInstanceMode;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.resource.api.SharedResourceNodes;
import auriga.resource.persistence.ResourceStore;
import auriga.resource.persistence.ResourceWorkflowStore;
import java.nio.charset.StandardCharsets;
import java.security.KeyPairGenerator;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.*;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class CatalogSharedResourcesTest {
  @Test
  void sharedIntentHistoryDoesNotAdvanceForRenewalAndNeverResetsAfterRemoval() {
    var f = new Fixture();
    f.run(
        () -> {
          var history = new CatalogSharedInstallations(f.db);
          UUID placement = UUID.fromString(f.binding.getPlacementId());
          var first = Map.of(f.pack, f.selection("required"));
          assertEquals(1L, history.reconcile(placement, first).get(f.pack));
          assertEquals(
              1L, new CatalogSharedInstallations(f.db).reconcile(placement, first).get(f.pack));
          var changedMode = Map.of(f.pack, f.selection("default"));
          assertEquals(2L, history.reconcile(placement, changedMode).get(f.pack));
          assertTrue(history.reconcile(placement, Map.of()).isEmpty());
          assertTrue(history.reconcile(placement, Map.of()).isEmpty());
          assertEquals(4L, history.reconcile(placement, changedMode).get(f.pack));
          var nextIdentity = f.selection.identity().deepCopy();
          ((ObjectNode) nextIdentity).put("versionId", UUID.randomUUID().toString());
          var next = Map.of(f.pack, new CatalogAssignments.Effective(nextIdentity, "default", 2));
          assertEquals(5L, history.reconcile(placement, next).get(f.pack));
          assertTrue(history.reconcile(placement, Map.of()).isEmpty());
          assertThrows(AurigaException.class, () -> history.reconcile(placement, first));
          var conflictIdentity = nextIdentity.deepCopy();
          ((ObjectNode) conflictIdentity).put("versionId", UUID.randomUUID().toString());
          assertThrows(
              AurigaException.class,
              () ->
                  history.reconcile(
                      placement,
                      Map.of(
                          f.pack,
                          new CatalogAssignments.Effective(conflictIdentity, "default", 2))));
          return null;
        });
  }

  @Test
  void artifactOpenRequiresTheSignedReferenceDigestAndSizeBeforeImmutableObjectAccess() {
    var f = new Fixture();
    var objects = mock(auriga.resource.objectstore.ObjectStore.class);
    var workflow =
        new CatalogWorkflow(null, f.db, null, null, () -> objects, null, null, null, null, null);
    var version = Map.<String, Object>of("package_id", f.pack, "id", f.version);
    String digest = "sha256:" + "b".repeat(64);
    when(f.db.row(startsWith("SELECT * FROM resource.catalog_artifact"), anyMap()))
        .thenReturn(Map.of("digest", digest, "size_bytes", 7L));
    when(objects.open(f.tenant, new auriga.resource.objectstore.ObjectStore.ObjectInfo(digest, 7L)))
        .thenReturn(new java.io.ByteArrayInputStream("payload".getBytes(StandardCharsets.UTF_8)));
    f.run(
        () -> {
          try (var content = workflow.nodeArtifact(f.reader, version, "payload", digest, 7L)) {
            assertEquals("payload", new String(content.readAllBytes(), StandardCharsets.UTF_8));
          } catch (java.io.IOException e) {
            throw new AssertionError(e);
          }
          assertThrows(
              AurigaException.class,
              () -> workflow.nodeArtifact(f.reader, version, "payload", digest, 8L));
          assertThrows(
              AurigaException.class,
              () ->
                  workflow.nodeArtifact(
                      f.reader, version, "payload", "sha256:" + "c".repeat(64), 7L));
          verify(objects, times(1)).open(any(), any());
          return null;
        });
  }

  @Test
  void equalMaterialForTwoMembersKeepsIndependentLeasesAndActualNodeLocators() {
    var f = new Fixture();
    UUID other = UUID.randomUUID();
    var reader = new ResourceAccess.Reader(f.tenant, other, false, Set.of(), false);
    when(f.access.target(f.tenant, other)).thenReturn(reader);
    when(f.delivery.sharedSelections(eq(reader), nullable(UUID.class)))
        .thenAnswer(c -> f.selections());
    when(f.delivery.sharedRef(eq(reader), any())).thenAnswer(c -> f.reference());
    when(f.catalog.ver(reader, f.pack, f.version, false)).thenReturn(Map.of("state", "published"));
    f.run(
        () -> {
          var first = f.service.snapshot(f.binding);
          var binding =
              f.binding.toBuilder()
                  .setMemberId(other.toString())
                  .setPlacementId(UUID.randomUUID().toString())
                  .build();
          var second = f.service.snapshot(binding);
          assertEquals(
              first.view().getAuthorizationSetDigest(), second.view().getAuthorizationSetDigest());
          assertEquals(
              first.resources().getFirst().getIdentity(),
              second.resources().getFirst().getIdentity());
          assertEquals(
              first.view().getResources(0).getResourceId(),
              second.view().getResources(0).getResourceId());
          assertEquals(
              first.view().getResources(0).getDigest(), second.view().getResources(0).getDigest());
          assertNotEquals(first.view().getViewId(), second.view().getViewId());
          assertNotEquals(
              first.view().getResources(0).getLeaseId(),
              second.view().getResources(0).getLeaseId());
          for (var snapshot : List.of(first, second))
            assertEquals(
                "/internal/resource/v1/catalog/downloads/"
                    + snapshot.view().getResources(0).getLeaseId()
                    + "/manifest",
                snapshot.resources().getFirst().getManifestDownloadPath());
          assertThrows(
              AurigaException.class,
              () ->
                  f.service.artifact(
                      binding, UUID.fromString(first.view().getResources(0).getLeaseId()), null));
          return null;
        });
  }

  @Test
  void nodeDownloadUsesPersistedBindingAndCurrentNodeAuthorityAndRejectsStaleOrCorruptBytes() {
    var f = new Fixture();
    f.run(
        () -> {
          var snapshot = f.service.snapshot(f.binding);
          UUID lease = UUID.fromString(snapshot.view().getResources(0).getLeaseId());
          try (var in =
              f.service.nodeDownload(f.tenant, f.node, lease, null, false).orElseThrow()) {
            assertEquals("manifest", new String(in.readAllBytes(), StandardCharsets.UTF_8));
          } catch (java.io.IOException e) {
            throw new AssertionError(e);
          }
          verify(f.nodes).requireCurrent(f.tenant, f.node, f.binding);
          assertThrows(
              AurigaException.class,
              () -> f.service.nodeDownload(f.tenant, UUID.randomUUID(), lease, null, false));
          assertThrows(
              AurigaException.class,
              () -> f.service.nodeDownload(f.tenant, f.node, lease, null, true));
          when(f.catalog.ver(f.reader, f.pack, f.version, false))
              .thenReturn(
                  Map.of(
                      "state",
                      "published",
                      "manifest_bytes",
                      "corrupt".getBytes(StandardCharsets.UTF_8)));
          assertThrows(
              AurigaException.class,
              () -> f.service.nodeDownload(f.tenant, f.node, lease, null, false));
          f.revocations = f.revocations(2);
          assertThrows(
              AurigaException.class,
              () -> f.service.nodeDownload(f.tenant, f.node, lease, null, false));
          return null;
        });
    assertTrue(f.service.nodeDownload(f.tenant, f.node, UUID.randomUUID(), null, false).isEmpty());
  }

  @Test
  void canonicalAuthorizationUsesIdentityVersionModeAndTenantWithStableOrdering() {
    var f = new Fixture();
    UUID other = UUID.randomUUID();
    var first = f.selection("required");
    var second = new CatalogAssignments.Effective(first.identity().deepCopy(), "default", 2);
    var a = new LinkedHashMap<UUID, CatalogAssignments.Effective>();
    a.put(f.pack, first);
    a.put(other, second);
    var b = new LinkedHashMap<UUID, CatalogAssignments.Effective>();
    b.put(other, second);
    b.put(f.pack, first);
    String digest = CatalogSharedResources.digest(f.tenant, a);
    assertEquals(digest, CatalogSharedResources.digest(f.tenant, b));
    b.put(f.pack, f.selection("optional"));
    assertNotEquals(digest, CatalogSharedResources.digest(f.tenant, b));
    assertNotEquals(digest, CatalogSharedResources.digest(UUID.randomUUID(), a));
  }

  @Test
  void repeatedSnapshotReturnsPersistedSignedMaterialAndLeaseIdentity() {
    var f = new Fixture();
    f.run(
        () -> {
          var first = f.service.snapshot(f.binding);
          var next = f.service.snapshot(f.binding);
          assertEquals(first, next);
          assertEquals(1, first.view().getResourcesCount());
          assertEquals(1, first.resources().getFirst().getInstallationRevision());
          assertEquals(
              auriga.contracts.resource.v1.UnifiedResourceDesiredState
                  .UNIFIED_RESOURCE_DESIRED_STATE_ENABLED,
              first.resources().getFirst().getDesiredState());
          assertEquals(
              first.resources().getFirst().getIdentity().getPackageId(),
              first.view().getResources(0).getResourceId());
          assertEquals(
              first.resources().getFirst().getLease().getExpiresAt(),
              Instant.ofEpochSecond(first.view().getResources(0).getExpiresAt()).toString());
          assertFalse(
              first.resources().getFirst().getEnvelope().getSignature().getValue().isEmpty());
          assertEquals(
              first.revocations().getRevision(),
              first.resources().getFirst().getLease().getRevocationRevision());
          verify(f.delivery, times(1)).sharedRef(eq(f.reader), any());
          return null;
        });
  }

  @Test
  void changedRevocationsReplaceLeaseAndOldNodeDownloadIsRejected() {
    var f = new Fixture();
    f.run(
        () -> {
          var first = f.service.snapshot(f.binding);
          UUID lease = UUID.fromString(first.view().getResources(0).getLeaseId());
          f.revocations = f.revocations(2);
          var changed = f.service.snapshot(f.binding);
          assertNotEquals(first.view().getViewId(), changed.view().getViewId());
          assertNotEquals(lease.toString(), changed.view().getResources(0).getLeaseId());
          assertEquals(
              "common.not_found",
              assertThrows(AurigaException.class, () -> f.service.artifact(f.binding, lease, null))
                  .errorCode()
                  .code());
          assertEquals(
              first.resources().getFirst().getInstallationRevision(),
              changed.resources().getFirst().getInstallationRevision());
          return null;
        });
  }

  @Test
  void optionalAssignmentsAffectAuthorizationButNeverLoadIntoTheCommonProcess() {
    var f = new Fixture();
    f.selection = f.selection("optional");
    f.run(
        () -> {
          var view = f.service.snapshot(f.binding);
          assertEquals(0, view.view().getResourcesCount());
          assertTrue(view.resources().isEmpty());
          assertEquals(
              f.service.authorizationSetDigest(f.tenant, f.member),
              view.view().getAuthorizationSetDigest());
          verify(f.delivery, never()).sharedRef(any(), any());
          return null;
        });
  }

  @Test
  void incompatiblePoolPolicyStaleBindingClockRollbackAndLostMembershipDeny() {
    var f = new Fixture();
    f.run(
        () -> {
          f.service.snapshot(f.binding);
          var newer = f.binding.toBuilder().setEpoch(2).setRevision(2).build();
          f.service.snapshot(newer);
          assertEquals(
              "common.precondition_failed",
              assertThrows(AurigaException.class, () -> f.service.snapshot(f.binding))
                  .errorCode()
                  .code());
          when(f.delivery.sharedSelections(f.reader, f.pool)).thenReturn(Map.of());
          assertEquals(
              "common.conflict",
              assertThrows(AurigaException.class, () -> f.service.snapshot(newer))
                  .errorCode()
                  .code());
          when(f.delivery.sharedSelections(f.reader, f.pool)).thenAnswer(c -> f.selections());
          f.now.set(f.now.get().minusSeconds(1));
          assertEquals(
              "common.unavailable",
              assertThrows(AurigaException.class, () -> f.service.snapshot(newer))
                  .errorCode()
                  .code());
          when(f.access.target(f.tenant, f.member))
              .thenThrow(new AurigaException(CommonErrors.FORBIDDEN));
          assertEquals(
              "common.forbidden",
              assertThrows(AurigaException.class, () -> f.service.snapshot(newer))
                  .errorCode()
                  .code());
          return null;
        });
    assertThrows(
        AurigaException.class,
        () -> TenantContext.call(UUID.randomUUID(), () -> f.service.snapshot(f.binding)));
  }

  static final class Fixture {
    final UUID tenant = UUID.randomUUID(), member = UUID.randomUUID(), pool = UUID.randomUUID();
    final UUID pack = UUID.randomUUID(), version = UUID.randomUUID();
    final AtomicReference<Instant> now =
        new AtomicReference<>(Instant.parse("2026-11-01T00:00:00Z"));
    final Clock clock =
        new Clock() {
          public java.time.ZoneId getZone() {
            return ZoneOffset.UTC;
          }

          public Clock withZone(java.time.ZoneId zone) {
            return this;
          }

          public Instant instant() {
            return now.get();
          }
        };
    final ResourceStore tx = mock(ResourceStore.class);
    final ResourceWorkflowStore db = mock(ResourceWorkflowStore.class);
    final ResourceAccess access = mock(ResourceAccess.class);
    final CatalogDelivery delivery = mock(CatalogDelivery.class);
    final CatalogWorkflow catalog = mock(CatalogWorkflow.class);
    final ResourceDistributions releases = mock(ResourceDistributions.class);
    final ResourceAccess.Reader reader =
        new ResourceAccess.Reader(tenant, member, false, Set.of(), false);
    final ResourceSigning signing;
    final SharedDshBinding binding =
        SharedDshBinding.newBuilder()
            .setTenantId(tenant.toString())
            .setMemberId(member.toString())
            .setPlacementId(UUID.randomUUID().toString())
            .setPhysicalInstanceId(pool.toString())
            .setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
            .setEpoch(1)
            .setRevision(1)
            .build();
    final CatalogSharedResources service;
    final Map<UUID, Map<String, Object>> stored = new HashMap<>();
    final Map<String, Map<String, Object>> history = new HashMap<>();
    final SharedResourceNodes nodes = mock(SharedResourceNodes.class);
    final UUID node = UUID.randomUUID();
    CatalogAssignments.Effective selection;
    ObjectNode revocations;

    Fixture() {
      try {
        var generator = KeyPairGenerator.getInstance("Ed25519");
        signing =
            new ResourceSigning(
                "release",
                generator.generateKeyPair().getPrivate(),
                "revocation",
                generator.generateKeyPair().getPrivate());
      } catch (java.security.GeneralSecurityException failure) {
        throw new AssertionError(failure);
      }
      selection = selection("required");
      revocations = revocations(1);
      doAnswer(c -> c.<Supplier<?>>getArgument(0).get()).when(tx).write(any());
      when(access.target(tenant, member)).thenReturn(reader);
      when(delivery.sharedSelections(eq(reader), nullable(UUID.class)))
          .thenAnswer(c -> selections());
      when(delivery.sharedRef(eq(reader), any())).thenAnswer(c -> reference());
      when(catalog.ver(eq(reader), eq(pack), eq(version), eq(false)))
          .thenReturn(
              Map.of(
                  "state",
                  "published",
                  "manifest_bytes",
                  "manifest".getBytes(StandardCharsets.UTF_8)));
      when(catalog.pkg(reader, pack, false)).thenReturn(Map.of("enabled", true));
      when(nodes.requireCurrent(eq(tenant), eq(node), any()))
          .thenAnswer(c -> CatalogSharedResources.digest(tenant, selections()));
      when(releases.revocations()).thenAnswer(c -> revocations);
      when(db.args(any(Object[].class)))
          .thenAnswer(
              c -> {
                var args = new HashMap<String, Object>();
                args.put("tenant", tenant);
                Object[] pairs = (Object[]) c.getRawArguments()[0];
                for (int i = 0; i < pairs.length; i += 2) args.put((String) pairs[i], pairs[i + 1]);
                return args;
              });
      when(db.rows(anyString(), anyMap()))
          .thenAnswer(
              c -> {
                Map<String, Object> args = c.getArgument(1);
                if (c.<String>getArgument(0).contains("catalog_shared_installation"))
                  return history.values().stream()
                      .filter(row -> row.get("placement").equals(args.get("placement")))
                      .toList();
                if (args.containsKey("lease")) {
                  String lease =
                      JSON.readTree(args.get("lease").toString()).get(0).path("leaseId").asString();
                  return stored.values().stream()
                      .filter(row -> row.get("document").toString().contains(lease))
                      .toList();
                }
                var row = stored.get(args.get("placement"));
                return row == null ? List.of() : List.of(row);
              });
      when(db.update(anyString(), anyMap()))
          .thenAnswer(
              c -> {
                Map<String, Object> args = c.getArgument(1);
                if (c.<String>getArgument(0).contains("catalog_shared_installation")) {
                  var row = new HashMap<>(args);
                  row.put("package_id", args.get("package"));
                  history.put(args.get("placement") + "/" + args.get("package"), row);
                  return 1;
                }
                Map<String, Object> row =
                    new HashMap<>(
                        Map.of(
                            "member_id",
                            args.get("member"),
                            "pool_id",
                            args.get("pool"),
                            "mode",
                            args.get("mode"),
                            "epoch",
                            args.get("epoch"),
                            "revision",
                            args.get("revision"),
                            "view_id",
                            args.get("view"),
                            "authorization_digest",
                            args.get("digest"),
                            "revocation_revision",
                            args.get("revocations"),
                            "expires_at",
                            args.get("expires"),
                            "document",
                            args.get("document")));
                row.put("placement_id", args.get("placement"));
                stored.put((UUID) args.get("placement"), row);
                return 1;
              });
      service =
          new CatalogSharedResources(
              tx, db, access, delivery, catalog, releases, clock, () -> nodes);
    }

    <T> T run(Supplier<T> action) {
      return TenantContext.call(tenant, () -> action.get());
    }

    Map<UUID, CatalogAssignments.Effective> selections() {
      return Map.of(pack, selection);
    }

    CatalogAssignments.Effective selection(String mode) {
      return new CatalogAssignments.Effective(
          JSON.createObjectNode()
              .put("tenantId", tenant.toString())
              .put("packageId", pack.toString())
              .put("versionId", version.toString())
              .put("kind", "skill")
              .put("version", "1.0.0")
              .put(
                  "manifestDigest",
                  CatalogManifest.digest("manifest".getBytes(StandardCharsets.UTF_8))),
          mode,
          1);
    }

    ObjectNode revocations(long revision) {
      var value =
          JSON.createObjectNode()
              .put("schemaVersion", 1)
              .put("tenantId", tenant.toString())
              .put("revision", revision)
              .put("issuedAt", now.get().toString())
              .put("expiresAt", now.get().plusSeconds(86400).toString());
      value.putArray("revokedKids");
      value.putArray("revokedArtifacts");
      return signing.sign(value, true);
    }

    ObjectNode reference() {
      var envelope =
          JSON.createObjectNode()
              .put("schemaVersion", 2)
              .put("tenantId", tenant.toString())
              .put("packageId", pack.toString())
              .put("versionId", version.toString())
              .put("releaseSequence", 1)
              .put("notAfter", now.get().plusSeconds(86400).toString());
      var statement =
          envelope
              .putObject("statement")
              .put("purpose", "resource")
              .put("kind", "skill")
              .put("name", "shared-test")
              .put("version", "1.0.0")
              .put("manifestDigest", selection.identity().path("manifestDigest").asString())
              .put("issuedAt", now.get().toString());
      statement.putArray("artifactDigests");
      var ref =
          JSON.createObjectNode()
              .put("releaseSequence", 1)
              .put("enabled", true)
              .put("installationMode", selection.mode())
              .put("manifestDownloadPath", "/manifest");
      ref.set("identity", selection.identity());
      ref.set("envelope", signing.sign(envelope, false));
      ref.putObject("lease")
          .put("envelopeNotAfter", envelope.path("notAfter").asString())
          .put("revocationsExpiresAt", revocations.path("expiresAt").asString())
          .put("expiresAt", revocations.path("expiresAt").asString())
          .put("revocationRevision", revocations.path("revision").asLong());
      ref.putArray("artifacts");
      return ref;
    }
  }
}
