package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.time.Instant;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Online authority only. No token persistence, permission cache, or session activity touch. */
public final class DeviceAccessAuthority {
  public record Identity(String tenant, String account, String device, Instant expiresAt) {}

  private final IamStore store;
  private final SessionManager sessions;
  private final TokenService tokens;
  private StandardDevices standardDevices;

  DeviceAccessAuthority standardDevices(StandardDevices value) {
    standardDevices = value;
    return this;
  }

  boolean standard(String device) {
    if (device == null || !uuid(device)) return false;
    return store.tx(
        () -> {
          var row = store.get("device", device, false);
          return row != null
              && StandardDevices.MODE.equals(row.path("authorizationMode").asString());
        });
  }

  public DeviceAccessAuthority(IamStore store, SessionManager sessions, TokenService tokens) {
    this.store = store;
    this.sessions = sessions;
    this.tokens = tokens;
  }

  public Identity access(String raw) {
    if (raw == null || raw.isEmpty() || raw.length() > 16384) return null;
    ObjectNode claims;
    try {
      claims = tokens.verify(raw);
    } catch (AurigaException invalid) {
      int status = invalid.errorCode().httpStatus();
      if (status == 400 || status == 401 || status == 403) return null;
      throw invalid;
    }
    if (!TenantContext.require().toString().equals(claims.path("tid").asString()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (!platformAudience(claims)
        || !claims.path("ptyp").asString().equals("device")
        || !claims.path("ctx").asString().equals("device")
        || !StandardDevices.scopes(claims.path("scope").asString()).contains("auriga.connect"))
      return null;
    boolean team = false;
    for (var permission : claims.path("perms"))
      if (permission.asString().equals("auriga.connect.collab.use")) team = true;
    if (!team) return null;
    if (standard(claims.path("did").asString()))
      return standardDevices == null
          ? null
          : standardDevices.access(raw, "auriga", "auriga.connect.collab.use");
    if (!claims.path("scope").asString().equals("auriga.connect")) return null;
    Identity identity =
        credential(
            claims.path("tid").asString(),
            claims.path("sub").asString(),
            claims.path("did").asString());
    if (identity == null) return null;
    Instant tokenExpiry = Instant.ofEpochSecond(claims.path("exp").asLong());
    return new Identity(
        identity.tenant(),
        identity.account(),
        identity.device(),
        tokenExpiry.isBefore(identity.expiresAt()) ? tokenExpiry : identity.expiresAt());
  }

  public Identity credential(String tenant, String account, String device) {
    if (!TenantContext.require().toString().equals(tenant))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (!uuid(account) || !uuid(device)) return null;
    if (standard(device))
      return standardDevices == null
          ? null
          : standardDevices.credentialState(tenant, account, device);
    return store.tx(
        () -> {
          ObjectNode initial = store.get("device", device, false);
          if (initial == null
              || !initial.path("accountId").asString().equals(account)
              || !initial.has("sessionId")) return null;
          // Same order as refresh: backing session first, device second. Re-read after locks.
          ObjectNode session = sessions.activeById(initial.path("sessionId").asString());
          if (session == null
              || !session.path("audience").asString().equals("portal")
              || !session.path("accountId").asString().equals(account)) return null;
          ObjectNode current = store.get("device", device, true);
          if (current == null
              || current.path("revoked").asBoolean()
              || !UacSessions.boundTo(current, session)
              || !current.path("accountId").asString().equals(account)
              || !current.path("sessionId").asString().equals(session.path("sessionId").asString())
              || !current.path("scope").asString().equals("auriga.connect")) return null;
          Instant absolute = Instant.parse(session.path("expiresAt").asString()),
              idle = Instant.parse(session.path("idleExpiresAt").asString());
          return new Identity(tenant, account, device, absolute.isBefore(idle) ? absolute : idle);
        });
  }

  record CredentialSnapshot(Identity identity, String session) {}

  Optional<UUID> installationInstance(String tenant, String account, String device) {
    return store.tx(
        () -> {
          if (credential(tenant, account, device) == null) throw forbidden();
          var row = store.get("device", device, false);
          if (row == null) throw forbidden();
          boolean managed = "managed".equals(row.path("deviceType").asString());
          var session = store.byId("session", row.path("sessionId").asString(), false);
          boolean managedSession =
              session != null
                  && ("managed_device".equals(session.path("kind").asString())
                      || session.has("managedInstance")
                      || session.has("managedVersion"));
          if (!managed
              && !managedSession
              && !row.has("managedInstance")
              && !row.has("managedVersion")) return Optional.empty();

          String instance = row.path("managedInstance").asString();
          long version = row.path("managedVersion").asLong();
          if (!managed
              || !managedSession
              || !"managed_device".equals(session.path("kind").asString())
              || !uuid(instance)
              || version < 1
              || !device.equals(row.path("deviceId").asString())
              || !instance.equals(session.path("managedInstance").asString())
              || version != session.path("managedVersion").asLong()) throw forbidden();
          // credential() has checked the backing session against the live Workspace binding.
          // Match the current IAM lineage too, without taking the rotation lock after the session.
          var lineage = store.get("managed_device", instance, false);
          if (lineage == null
              || lineage.path("revoked").asBoolean()
              || !account.equals(lineage.path("owner").asString())
              || !device.equals(lineage.path("device").asString())
              || !lineage.path("expiresAt").equals(session.path("expiresAt"))
              || version != lineage.path("version").asLong()) throw forbidden();
          return Optional.of(UUID.fromString(instance));
        });
  }

  boolean desktopSessionActive(String tenant, String account, String device) {
    return store.tx(
        () -> {
          var snapshot = credentialSnapshot(tenant, account, device);
          if (snapshot == null) return false;
          var row = store.get("device", device, false);
          var source = store.byId("session", snapshot.session(), false);
          if (row == null
              || source == null
              || !"dsh_connect".equals(row.path("kind").asString())
              || !Set.of(StandardDevices.MODE, "native_tx")
                  .contains(row.path("authorizationMode").asString())
              || row.has("managedInstance")
              || row.has("managedVersion")
              || "managed".equals(row.path("deviceType").asString())
              || "managed_device".equals(source.path("kind").asString())
              || source.has("managedInstance")
              || source.has("managedVersion")
              || !source.path("providerId").equals(row.path("providerId"))) return false;
          var provider = store.get("idp_provider", source.path("providerId").asString(), false);
          return provider != null && provider.path("enabled").asBoolean();
        });
  }

  private static AurigaException forbidden() {
    return new AurigaException(CommonErrors.FORBIDDEN);
  }

  /** Network entry points call this before taking their local policy/identity locks. */
  boolean standardOnline(String tenant, String account, String device) {
    if (!standard(device)) return true;
    return standardDevices != null
        && standardDevices.credential(tenant, account, device, "auriga.connect.collab.use") != null;
  }

  /** Same canonical checks and lock order as credential; no activity or token mutation. */
  CredentialSnapshot credentialSnapshot(String tenant, String account, String device) {
    return store.tx(
        () -> {
          Identity identity = credential(tenant, account, device);
          if (identity == null) return null;
          ObjectNode current = store.get("device", device, true);
          return new CredentialSnapshot(identity, current.path("sessionId").asString());
        });
  }

  private static boolean uuid(String value) {
    try {
      return UUID.fromString(value).toString().equals(value);
    } catch (IllegalArgumentException e) {
      return false;
    }
  }

  static boolean platformAudience(ObjectNode claims) {
    var audience = claims.path("aud");
    return audience.isString() && audience.asString().equals("auriga")
        || audience.isArray()
            && audience.size() == 1
            && audience.get(0).asString().equals("auriga");
  }
}
