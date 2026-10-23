package auriga.resource.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AurigaPrincipal;
import auriga.resource.persistence.ResourceStore;
import auriga.resource.web.model.*;
import java.util.UUID;

/** Only metadata reads: these operations never grant byte access or install permission. */
public final class ResourceQueries {
  private final ResourceStore store;
  private final ResourceAccess access;
  private final ResourceCursor cursors;

  public ResourceQueries(ResourceStore store, ResourceAccess access, ResourceCursor cursors) {
    this.store = store;
    this.access = access;
    this.cursors = cursors;
  }

  public PackagePage packages(AurigaPrincipal actor, int limit, String cursor) {
    checkLimit(limit);
    var reader = access.resolve(actor);
    var decoded = cursors.decode(cursor, reader, "packages", limit);
    return store.read(
        () -> {
          var position = decoded == null ? cursors.initial(store.ceiling(reader, null)) : decoded;
          var rows = store.packages(reader, position.after(), position.ceiling(), limit + 1);
          var page = new PackagePage(rows.stream().limit(limit).toList());
          if (rows.size() > limit)
            page.nextCursor(
                cursors.encode(
                    reader,
                    "packages",
                    limit,
                    new ResourceCursor.Position(
                        rows.get(limit - 1).getId(), position.ceiling(), position.expires())));
          return page;
        });
  }

  public ModelPackage resourcePackage(AurigaPrincipal actor, UUID packageId) {
    var reader = access.resolve(actor);
    return store.read(
        () ->
            store
                .packageById(reader, packageId)
                .orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND)));
  }

  public VersionPage versions(AurigaPrincipal actor, UUID packageId, int limit, String cursor) {
    checkLimit(limit);
    var reader = access.resolve(actor);
    String scope = "versions/" + packageId;
    var decoded = cursors.decode(cursor, reader, scope, limit);
    return store.read(
        () -> {
          store
              .packageById(reader, packageId)
              .orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
          var position =
              decoded == null ? cursors.initial(store.ceiling(reader, packageId)) : decoded;
          var rows =
              store.versions(reader, packageId, position.after(), position.ceiling(), limit + 1);
          var page = new VersionPage(rows.stream().limit(limit).toList());
          if (rows.size() > limit)
            page.nextCursor(
                cursors.encode(
                    reader,
                    scope,
                    limit,
                    new ResourceCursor.Position(
                        rows.get(limit - 1).getId(), position.ceiling(), position.expires())));
          return page;
        });
  }

  public Version version(AurigaPrincipal actor, UUID packageId, UUID versionId) {
    var reader = access.resolve(actor);
    return store.read(
        () -> {
          store
              .packageById(reader, packageId)
              .orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
          return store
              .versionById(reader, packageId, versionId)
              .orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
        });
  }

  private static void checkLimit(int limit) {
    if (limit < 1 || limit > 200) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }
}
