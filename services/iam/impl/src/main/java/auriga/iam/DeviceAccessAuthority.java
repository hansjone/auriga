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
        || !claims.path("scope").asString().equals("auriga.connect")) return null;
    boolean team = false;
    for (var permission : claims.path("perms"))
      if (permission.asString().equals("auriga.connect.collab.use")) team = true;
    if (!team) return null;
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
              || !current.path("accountId").asString().equals(account)
              || !current.path("sessionId").asString().equals(session.path("sessionId").asString())
              || !current.path("scope").asString().equals("auriga.connect")) return null;
          Instant absolute = Instant.parse(session.path("expiresAt").asString()),
              idle = Instant.parse(session.path("idleExpiresAt").asString());
          return new Identity(tenant, account, device, absolute.isBefore(idle) ? absolute : idle);
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
