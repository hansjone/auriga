package auriga.resource.app;

import auriga.common.error.*;
import auriga.common.security.AurigaPrincipal;
import auriga.resource.app.ResourceAccess.Reader;
import auriga.resource.persistence.ResourceStore;
import auriga.resource.web.PackageBody;
import auriga.resource.web.model.ModelPackage;
import java.time.Clock;
import java.util.*;

/** Only metadata creation/changes; publication, enablement and version state are separate flows. */
public final class ResourceCommands {
  private static final long MAX_REVISION = 9007199254740991L;
  private static final ErrorCode LEVEL_LOCKED =
      ErrorCode.of("resource.level_locked", 403, io.grpc.Status.Code.PERMISSION_DENIED);
  private static final ErrorCode STATE_CONFLICT =
      ErrorCode.of("resource.state_conflict", 409, io.grpc.Status.Code.ABORTED);
  private final ResourceStore store;
  private final ResourceAccess access;
  private final Clock clock;

  public ResourceCommands(ResourceStore store, ResourceAccess access, Clock clock) {
    this.store = store;
    this.access = access;
    this.clock = clock;
  }

  public ModelPackage create(AurigaPrincipal principal, String key, PackageBody body) {
    var actor = access.writer(principal);
    if (key != null && !key.matches("[\\x21-\\x7E]{1,128}"))
      throw error(CommonErrors.VALIDATION_FAILED);
    return store.write(
        () -> {
          if (key != null) store.lockCreation(actor, key);
          var current = access.writer(principal);
          Set<UUID> groups =
              validate(
                  current, body.visibility(), body.groups(), body.visibility().equals("private"));
          var now = clock.instant();
          if (key == null)
            return store.insert(current, body.name(), body.visibility(), groups, now);
          var fingerprint = body.fingerprint();
          var previous = store.creation(current, key, now);
          if (previous.isPresent()) {
            if (!previous.get().fingerprint().equals(fingerprint))
              throw error(CommonErrors.CONFLICT);
            return previous.get().response();
          }
          var result = store.insert(current, body.name(), body.visibility(), groups, now);
          store.rememberCreation(current, key, fingerprint, result, now);
          return result;
        });
  }

  public ModelPackage patch(AurigaPrincipal principal, UUID id, String etag, PackageBody body) {
    var actor = access.writer(principal);
    return store.write(
        () -> {
          store.lockPackage(actor, id).orElseThrow(() -> error(CommonErrors.NOT_FOUND));
          var current = access.writer(principal);
          var original =
              store.packageById(current, id).orElseThrow(() -> error(CommonErrors.NOT_FOUND));
          if (!original.getOwnerId().equals(current.account())) throw error(CommonErrors.FORBIDDEN);
          if (original.getLevel() != ModelPackage.LevelEnum.L3
              && original.getLevel() != ModelPackage.LevelEnum.L4) throw error(LEVEL_LOCKED);
          if (etag == null || !etag.equals("\"" + original.getRevision() + "\""))
            throw error(CommonErrors.PRECONDITION_FAILED);
          if (original.getRevision() >= MAX_REVISION) throw error(STATE_CONFLICT);
          String visibility =
              body.visibility() == null ? original.getVisibility().getValue() : body.visibility();
          if (original.getLevel() == ModelPackage.LevelEnum.L4 && !visibility.equals("private"))
            throw error(LEVEL_LOCKED);
          Set<UUID> groups =
              body.groups() == null
                  ? (visibility.equals("groups") ? original.getGroupIds() : Set.of())
                  : body.groups();
          groups =
              validate(
                  current, visibility, groups, original.getLevel() == ModelPackage.LevelEnum.L4);
          return store.update(
              current,
              id,
              body.name() == null ? original.getName() : body.name(),
              visibility,
              groups);
        });
  }

  private Set<UUID> validate(Reader actor, String visibility, Set<UUID> groups, boolean personal) {
    if (personal && !actor.personalPlugins()) throw error(CommonErrors.FORBIDDEN);
    if (visibility.equals("members") && actor.external()) throw error(CommonErrors.FORBIDDEN);
    if (!visibility.equals("groups")) {
      if (!groups.isEmpty()) throw error(CommonErrors.VALIDATION_FAILED);
      return Set.of();
    }
    if (groups.isEmpty()) throw error(CommonErrors.VALIDATION_FAILED);
    if (!actor.groups().containsAll(groups)) throw error(CommonErrors.FORBIDDEN);
    return groups;
  }

  private static AurigaException error(ErrorCode code) {
    return new AurigaException(code);
  }
}
