package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.contracts.node.v1.*;
import auriga.contracts.resource.v1.ResourceRevocationList;
import auriga.resource.api.*;
import auriga.resource.persistence.*;
import java.time.Clock;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;

class CatalogPlacementMarketTest {
  static final class Fixture {
    final UUID tenant = UUID.randomUUID(),
        member = UUID.randomUUID(),
        placement = UUID.randomUUID(),
        session = UUID.randomUUID(),
        pack = UUID.randomUUID();
    final ResourceStore tx = mock(ResourceStore.class);
    final ResourceWorkflowStore db = mock(ResourceWorkflowStore.class);
    final ResourceAccess access = mock(ResourceAccess.class);
    final CatalogWorkflow catalog = mock(CatalogWorkflow.class);
    final CatalogDelivery delivery = mock(CatalogDelivery.class);
    final CatalogSharedResources resources = mock(CatalogSharedResources.class);
    final SharedResourcePlacements placements = mock(SharedResourcePlacements.class);
    final AurigaPrincipal actor =
        actor(PrincipalType.USER, TokenContext.PORTAL, session.toString());
    final ResourceAccess.Reader reader =
        new ResourceAccess.Reader(tenant, member, false, Set.of(), false);
    final SharedDshBinding binding =
        SharedDshBinding.newBuilder()
            .setTenantId(tenant.toString())
            .setMemberId(member.toString())
            .setPlacementId(placement.toString())
            .setPhysicalInstanceId(UUID.randomUUID().toString())
            .setEpoch(1)
            .setRevision(1)
            .setMode(DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED)
            .build();
    final SharedDshResourceView view =
        SharedDshResourceView.newBuilder()
            .setBinding(binding)
            .setViewId(UUID.randomUUID().toString())
            .build();
    final CatalogPlacementMarket market;

    Fixture() {
      when(tx.write(any())).thenAnswer(c -> c.<Supplier<?>>getArgument(0).get());
      when(access.member(actor)).thenReturn(reader);
      when(placements.resolve(tenant, member, placement, session)).thenReturn(binding);
      when(placements.report(view)).thenReturn(Optional.empty());
      when(resources.snapshot(binding))
          .thenReturn(
              new SharedResourceResolution.Snapshot(
                  view, List.of(), ResourceRevocationList.getDefaultInstance()));
      when(db.rows(anyString(), any())).thenReturn(List.of());
      market =
          new CatalogPlacementMarket(
              tx,
              db,
              access,
              catalog,
              delivery,
              resources,
              () -> placements,
              new ResourceCursor(new byte[32], Clock.systemUTC()));
    }

    AurigaPrincipal actor(PrincipalType type, TokenContext context, String sid) {
      return new AurigaPrincipal(
          member.toString(),
          tenant,
          type,
          context,
          sid,
          null,
          "member",
          List.of(),
          Set.of(ResourceAccess.READ),
          null,
          null,
          null);
    }

    void selected() {
      var identity =
          JSON.createObjectNode()
              .put("tenantId", tenant.toString())
              .put("packageId", pack.toString())
              .put("versionId", UUID.randomUUID().toString())
              .put("version", "1.0.0")
              .put("kind", "plugin")
              .put("manifestDigest", "sha256:" + "a".repeat(64));
      when(db.rows(contains("catalog_placement_selection"), any()))
          .thenReturn(
              List.of(
                  Map.of(
                      "package_id",
                      pack,
                      "owner_id",
                      member,
                      "epoch",
                      1L,
                      "revision",
                      4L,
                      "desired_state",
                      "DISABLED",
                      "identity",
                      identity.toString())));
    }
  }

  @Test
  void portalSessionIsRequiredAndNoOwnerOrDeviceAuthorityIsBorrowed() {
    var f = new Fixture();
    for (var actor :
        List.of(
            f.actor(PrincipalType.DEVICE, TokenContext.DEVICE, null),
            f.actor(PrincipalType.USER, TokenContext.ADMIN, f.session.toString()),
            f.actor(PrincipalType.USER, TokenContext.PORTAL, null))) {
      assertThrows(AurigaException.class, () -> f.market.list(actor, f.placement, null, 50, null));
    }
    verifyNoInteractions(f.access, f.placements, f.resources);
    var forged = JSON.createObjectNode();
    forged.set("selection", JSON.createObjectNode());
    forged.put("memberId", UUID.randomUUID().toString());
    assertThrows(
        AurigaException.class, () -> f.market.select(f.actor, f.placement, forged, "k", "\"0\""));
  }

  @Test
  void disabledIsOnlyObservedAfterTheExactCurrentViewReceipt() {
    var f = new Fixture();
    f.selected();
    var pending = f.market.list(f.actor, f.placement, "plugin", 50, null);
    assertFalse(pending.path("nodeObserved").asBoolean());
    assertEquals("applying", pending.path("items").get(0).path("state").asString());
    when(f.placements.report(f.view))
        .thenReturn(
            Optional.of(
                SharedDshResourceReport.newBuilder()
                    .setBinding(f.binding)
                    .setViewId(f.view.getViewId())
                    .build()));
    var applied = f.market.list(f.actor, f.placement, "plugin", 50, null);
    assertEquals("disabled", applied.path("items").get(0).path("state").asString());
    assertEquals(4, applied.path("items").get(0).path("selectionRevision").asLong());
  }

  @Test
  void memberPlacementIsFencedAfterReceiptAndSharedSelectionIsExplicitlyRejected() {
    var f = new Fixture();
    when(f.placements.resolve(f.tenant, f.member, f.placement, f.session))
        .thenReturn(f.binding, f.binding.toBuilder().setRevision(2).build());
    assertThrows(AurigaException.class, () -> f.market.list(f.actor, f.placement, null, 50, null));
    when(f.placements.resolve(f.tenant, f.member, f.placement, f.session))
        .thenReturn(
            f.binding.toBuilder().setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED).build());
    var body = JSON.createObjectNode();
    body.set("selection", JSON.createObjectNode().put("action", "install"));
    var error =
        assertThrows(
            AurigaException.class, () -> f.market.select(f.actor, f.placement, body, "k", "\"0\""));
    assertEquals("resource.shared_requires_dedicated", error.errorCode().code());
    verifyNoInteractions(f.catalog);
  }
}
