package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import java.time.Clock;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Bounded device-owned installation facts, independent of browser sessions and client logs. */
final class DesktopDeviceReports {
  private final IamStore store;
  private final SessionManager sessions;
  private final Clock clock;
  static final Set<String> STAGES =
      Set.of(
          "verify-current",
          "verify-release",
          "check-release",
          "verify-input",
          "verify-archive",
          "extract",
          "verify-package",
          "install",
          "profile",
          "smoke",
          "download",
          "verify",
          "installation",
          "copy",
          "verify-copy",
          "commit");
  static final Set<String> REASONS =
      Set.of(
          "updateFailed",
          "ENOENT",
          "ENOTDIR",
          "EBUSY",
          "EPERM",
          "EACCES",
          "ENOTEMPTY",
          "desktop.pending_incomplete",
          "desktop.release_invalid",
          "desktop.release_withdrawn",
          "desktop.release_replaced",
          "desktop.archive_invalid",
          "desktop.package_invalid",
          "desktop.smoke_failed",
          "desktop.extract_failed",
          "desktop.install_cancelled",
          "desktop.in_use");

  DesktopDeviceReports(IamStore store, SessionManager sessions, Clock clock) {
    this.store = store;
    this.sessions = sessions;
    this.clock = clock;
  }

  static void validate(ObjectNode body) {
    IamError.validate(
        body.propertyNames()
                .equals(Set.of("installationId", "sequence", "clientVersion", "channel"))
            || body.propertyNames()
                .equals(
                    Set.of(
                        "installationId", "sequence", "clientVersion", "channel", "lastUpdate")));
    try {
      IamError.validate(
          body.path("installationId")
              .asString()
              .equals(UUID.fromString(body.path("installationId").asString()).toString()));
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
    IamError.validate(
        body.path("sequence").isIntegralNumber()
            && body.path("sequence").canConvertToLong()
            && body.path("sequence").asLong() > 0
            && body.path("sequence").asLong() <= 9007199254740991L);
    version(body.path("clientVersion"));
    IamError.validate(Set.of("stable", "beta").contains(body.path("channel").asString()));
    if (!body.has("lastUpdate")) return;
    var update = body.path("lastUpdate");
    IamError.validate(
        update.isObject()
            && update
                .propertyNames()
                .equals(Set.of("attemptId", "targetVersion", "result", "stage", "reasonCode")));
    try {
      IamError.validate(
          update
              .path("attemptId")
              .asString()
              .equals(UUID.fromString(update.path("attemptId").asString()).toString()));
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
    version(update.path("targetVersion"));
    String result = update.path("result").asString();
    IamError.validate(Set.of("succeeded", "failed", "rolled_back").contains(result));
    if (result.equals("succeeded")) {
      IamError.validate(update.path("stage").isNull() && update.path("reasonCode").isNull());
    } else {
      IamError.validate(
          STAGES.contains(update.path("stage").asString())
              && REASONS.contains(update.path("reasonCode").asString()));
    }
  }

  private static void version(JsonNode node) {
    IamError.validate(
        node.isString()
            && node.asString().length() <= 128
            && node.asString()
                .matches(
                    "(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\\+[0-9A-Za-z.-]+)?"));
  }

  void record(ObjectNode identity, ObjectNode body) {
    validate(body);
    IamError.require(
        TenantContext.require().toString().equals(identity.path("tenantId").asString()),
        "invalid_token",
        401);
    String deviceId = identity.path("deviceId").asString();
    store.limit("desktop-report:" + deviceId, clock.instant(), 30);
    store.tx(
        () -> {
          var initial = store.get("device", deviceId, false);
          IamError.require(initial != null, "invalid_token", 401);
          var session = sessions.activeById(initial.path("sessionId").asString());
          var device = store.get("device", deviceId, true);
          IamError.require(
              session != null
                  && device != null
                  && !device.path("revoked").asBoolean()
                  && device.path("accountId").equals(identity.path("accountId"))
                  && session.path("accountId").equals(device.path("accountId"))
                  && session.path("sessionId").equals(device.path("sessionId"))
                  && "portal".equals(session.path("audience").asString())
                  && "dsh_connect".equals(device.path("kind").asString())
                  && !"managed".equals(device.path("deviceType").asString())
                  && UacSessions.boundTo(device, session),
              "invalid_token",
              401);
          var previous = device.path("desktopReport");
          if (previous.path("installationId").equals(body.path("installationId"))) {
            long oldSequence = previous.path("sequence").asLong();
            if (body.path("sequence").asLong() < oldSequence) return null;
            if (body.path("sequence").asLong() == oldSequence) {
              var oldBody = ((ObjectNode) previous).deepCopy();
              oldBody.remove("reportedAt");
              if (!oldBody.equals(body)) throw new AurigaException(CommonErrors.CONFLICT);
              return null;
            }
          }
          device.put("clientVersion", body.path("clientVersion").asString());
          device.set(
              "desktopReport", body.deepCopy().put("reportedAt", clock.instant().toString()));
          store.save("device", device);
          return null;
        });
  }
}
