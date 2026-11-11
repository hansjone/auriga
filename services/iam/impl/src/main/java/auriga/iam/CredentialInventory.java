package auriga.iam;

import auriga.common.audit.AuditEntry;
import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.iam.v1.RevokeManagedDeviceRequest;
import java.time.*;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Explicit metadata projection: encrypted credentials and private keys never leave IAM. */
public final class CredentialInventory {
  private final IamStore store;
  private final IamManagedDevices managed;
  private final Clock clock;
  private final AuditLogger audit;
  private final java.util.function.Supplier<List<auriga.iam.api.CredentialMetadataSource>> sources;
  private final java.util.function.Supplier<auriga.workspace.api.ManagedCredentialRotation>
      rotations;

  public CredentialInventory(
      IamStore store,
      IamManagedDevices managed,
      Clock clock,
      AuditLogger audit,
      java.util.function.Supplier<List<auriga.iam.api.CredentialMetadataSource>> sources,
      java.util.function.Supplier<auriga.workspace.api.ManagedCredentialRotation> rotations) {
    this.store = store;
    this.managed = managed;
    this.clock = clock;
    this.audit = audit;
    this.sources = sources;
    this.rotations = rotations;
  }

  public ObjectNode list(String type, String status, boolean expiring) {
    IamError.validate(
        type == null || Set.of("managed", "signing", "service", "node", "keyset").contains(type));
    IamError.validate(
        status == null
            || Set.of("active", "revoked", "expired", "next", "retiring", "instance_deleted")
                .contains(status));
    List<ObjectNode> items = new ArrayList<>();
    if (type == null || type.equals("managed"))
      items.addAll(store.tx(() -> store.all("managed_device").stream().map(this::device).toList()));
    boolean platform = TenantContext.require().equals(SigningKeys.PLATFORM);
    if (platform && (type == null || type.equals("signing")))
      items.addAll(
          store.isolated(
              SigningKeys.PLATFORM,
              () -> store.all("signing_key").stream().map(this::signing).toList()));
    var gaps = new ArrayList<auriga.iam.api.CredentialMetadataSource.Gap>();
    if (items.stream()
        .anyMatch(item -> item.path("type").asString().equals("signing") && !item.has("expiresAt")))
      gaps.add(
          new auriga.iam.api.CredentialMetadataSource.Gap(
              "iam-signing-keys", "validity_unavailable"));
    boolean serviceAvailable = false;
    boolean nodeAvailable = false;
    var availableSources = sources.get();
    for (var source : availableSources) {
      if (!platform && source.name().equals("iam-service-issuance")) {
        gaps.add(new auriga.iam.api.CredentialMetadataSource.Gap(source.name(), "not_configured"));
        continue;
      }
      try {
        var snapshot = source.snapshot();
        if (source.name().equals("workspace-node-certificates")) nodeAvailable = true;
        gaps.addAll(snapshot.gaps());
        for (var entry : snapshot.entries()) {
          if (!platform
              && (entry.type().equals("service")
                  || entry.type().equals("signing")
                  || Set.of("identity-header", "panel-manifest", "network-root")
                      .contains(entry.source()))) continue;
          if (entry.type().equals("service")) serviceAvailable = true;
          if (entry.type().equals("node")) nodeAvailable = true;
          if (type != null && !type.equals(entry.type())) continue;
          String expiry = entry.expiresAt() == null ? "" : entry.expiresAt().toString();
          String state = entry.status();
          if (!state.equals("revoked") && expired(expiry)) state = "expired";
          var view =
              Json.object(
                  "id",
                  entry.id(),
                  "type",
                  entry.type(),
                  "source",
                  entry.source(),
                  "versionLabel",
                  entry.versionLabel(),
                  "status",
                  state,
                  "expiringSoon",
                  soon(expiry) && !state.equals("revoked"));
          if (entry.issuedAt() != null) view.put("issuedAt", entry.issuedAt().toString());
          if (!expiry.isEmpty()) view.put("expiresAt", expiry);
          items.add(view);
        }
      } catch (RuntimeException unavailable) {
        gaps.add(new auriga.iam.api.CredentialMetadataSource.Gap(source.name(), "unavailable"));
      }
    }
    var rotation = rotations.get();
    var managedItems =
        items.stream().filter(i -> i.path("type").asString().equals("managed")).toList();
    if (!managedItems.isEmpty()) {
      try {
        if (rotation == null) throw new IllegalStateException();
        var versions =
            rotation.currentVersions(
                TenantContext.require(),
                managedItems.stream().map(i -> UUID.fromString(i.path("id").asString())).toList());
        for (var item : managedItems) {
          Long current = versions.get(UUID.fromString(item.path("id").asString()));
          if (current == null) {
            item.put("status", "instance_deleted");
            item.put("expiringSoon", false);
          } else if (current > item.path("version").asLong())
            item.put("replacementVersion", current);
        }
      } catch (RuntimeException unavailable) {
        gaps.add(
            new auriga.iam.api.CredentialMetadataSource.Gap("workspace-rotation", "unavailable"));
      }
    }
    if (availableSources.isEmpty())
      gaps.add(
          new auriga.iam.api.CredentialMetadataSource.Gap("platform-keysets", "not_configured"));
    if (!platform && (type == null || type.equals("signing")))
      gaps.add(
          new auriga.iam.api.CredentialMetadataSource.Gap("iam-signing-keys", "not_configured"));
    var filtered =
        items.stream()
            .filter(r -> status == null || status.equals(r.path("status").asString()))
            .filter(r -> !expiring || r.path("expiringSoon").asBoolean())
            .sorted(Comparator.comparing(r -> r.path("id").asString()))
            .toList();
    return Json.object(
        "items",
        filtered,
        "serviceMetadataAvailable",
        serviceAvailable && nodeAvailable,
        "sourceGaps",
        gaps);
  }

  private ObjectNode device(ObjectNode row) {
    var device = store.get("device", row.path("device").asString(), false);
    String expiry = row.path("expiresAt").asString("");
    String status =
        row.path("revoked").asBoolean() ? "revoked" : expired(expiry) ? "expired" : "active";
    var view =
        Json.object(
            "id",
            row.path("id").asString(),
            "type",
            "managed",
            "version",
            row.path("version").asLong(),
            "status",
            status,
            "instanceId",
            row.path("id").asString(),
            "expiringSoon",
            soon(expiry) && status.equals("active"));
    if (!expiry.isEmpty()) view.put("expiresAt", expiry);
    if (device != null && device.has("createdAt"))
      view.put("issuedAt", device.path("createdAt").asString());
    return view;
  }

  private ObjectNode signing(ObjectNode row) {
    String expiry = row.path("retainUntil").asString("");
    String state = row.path("state").asString();
    var view =
        Json.object(
            "id",
            row.path("kid").asString(),
            "type",
            "signing",
            "status",
            expired(expiry) ? "expired" : state.equals("current") ? "active" : state,
            "expiringSoon",
            soon(expiry));
    if (row.has("publishedAt")) view.put("issuedAt", row.path("publishedAt").asString());
    if (!expiry.isEmpty()) view.put("expiresAt", expiry);
    return view;
  }

  private boolean expired(String expiry) {
    return !expiry.isEmpty() && !Instant.parse(expiry).isAfter(clock.instant());
  }

  private boolean soon(String expiry) {
    return !expiry.isEmpty()
        && !expired(expiry)
        && !Instant.parse(expiry).isAfter(clock.instant().plus(Duration.ofDays(7)));
  }

  public ObjectNode revoke(String instance, long version) {
    store.requireTransactionReleased();
    var rotation = rotations.get();
    if (rotation == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    UUID tenant = TenantContext.require();
    var initial =
        store.tx(
            () -> {
              var row = store.get("managed_device", instance, false);
              IamError.require(row != null, "not_found", 404);
              if (row.path("version").asLong() != version)
                throw new AurigaException(CommonErrors.CONFLICT);
              return device(row);
            });
    Long current =
        rotation
            .currentVersions(tenant, List.of(UUID.fromString(instance)))
            .get(UUID.fromString(instance));
    if (current == null) {
      initial.put("status", "instance_deleted");
      initial.put("expiringSoon", false);
      return initial;
    }
    if (initial.path("status").asString().equals("revoked") && current > version) {
      initial.put("replacementVersion", current);
      return initial;
    }
    var result =
        store.tx(
            () -> {
              store.lock("managed:" + instance);
              var row = store.get("managed_device", instance, true);
              IamError.require(row != null, "not_found", 404);
              if (row.path("version").asLong() != version)
                throw new AurigaException(CommonErrors.CONFLICT);
              if (!row.path("revoked").asBoolean()) {
                managed.revoke(
                    RevokeManagedDeviceRequest.newBuilder()
                        .setTenantId(tenant.toString())
                        .setInstanceId(instance)
                        .setCredentialVersion(version)
                        .build());
                audit.record(
                    AuditEntry.builder("iam.credential.revoke")
                        .target("instance", instance)
                        .detail("version", version)
                        .build());
              }
              return device(store.get("managed_device", instance, false));
            });
    try {
      long replacement = rotation.rotate(tenant, UUID.fromString(instance), version);
      if (replacement <= version || replacement > 9007199254740991L)
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      result.put("replacementVersion", replacement);
      store.tx(
          () -> {
            audit.record(
                AuditEntry.builder("iam.credential.reissue")
                    .target("instance", instance)
                    .detail("version", version)
                    .detail("replacementVersion", replacement)
                    .build());
            return null;
          });
      return result;
    } catch (RuntimeException failure) {
      if (failure instanceof AurigaException e && e.errorCode().code().equals("common.not_found")) {
        result.put("status", "instance_deleted");
        result.put("expiringSoon", false);
        return result;
      }
      store.tx(
          () -> {
            audit.record(
                AuditEntry.builder("iam.credential.reissue")
                    .target("instance", instance)
                    .detail("version", version)
                    .failure(
                        failure instanceof AurigaException e
                            ? e.errorCode().code()
                            : "common.unavailable")
                    .build());
            return null;
          });
      throw failure;
    }
  }
}
