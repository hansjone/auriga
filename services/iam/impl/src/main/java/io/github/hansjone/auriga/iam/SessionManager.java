package io.github.hansjone.auriga.iam;

import com.google.protobuf.Timestamp;
import io.github.hansjone.auriga.common.audit.*;
import io.github.hansjone.auriga.common.context.*;
import io.github.hansjone.auriga.common.events.EventPublisher;
import io.github.hansjone.auriga.contracts.iam.v1.*;
import io.github.hansjone.auriga.iam.api.Sessions;
import java.time.*;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

public final class SessionManager implements Sessions {
  public record Issued(ObjectNode session, String cookie, String csrf) {}

  private final IamStore store;
  private final SessionKv kv;
  private final TokenService tokens;
  private final Clock clock;
  private final EventPublisher events;
  private final AuditLogger audit;

  public SessionManager(
      IamStore store,
      SessionKv kv,
      TokenService tokens,
      Clock clock,
      EventPublisher events,
      AuditLogger audit) {
    this.store = store;
    this.kv = kv;
    this.tokens = tokens;
    this.clock = clock;
    this.events = events;
    this.audit = audit;
  }

  public Issued create(
      UUID account, UUID provider, String audience, boolean mfa, Duration ttl, Duration idle) {
    IamError.require(Set.of("portal", "admin").contains(audience), "invalid_audience", 400);
    String cookie = Secrets.random(), csrf = Secrets.random();
    Instant now = clock.instant();
    ObjectNode row =
        store.insert(
            "session",
            Secrets.hash(cookie),
            Json.object(
                "accountId",
                account.toString(),
                "tenantId",
                TenantContext.require().toString(),
                "providerId",
                provider.toString(),
                "audience",
                audience,
                "cookieHash",
                Secrets.hash(cookie),
                "csrfHash",
                Secrets.hash(csrf),
                "mfa",
                mfa,
                "authTime",
                now.toString(),
                "expiresAt",
                now.plus(ttl).toString(),
                "idleExpiresAt",
                now.plus(idle.compareTo(ttl) < 0 ? idle : ttl).toString(),
                "idleSeconds",
                idle.toSeconds(),
                "revoked",
                false),
            now.plus(ttl));
    row.put("sessionId", row.path("id").asString());
    ObjectNode accountRow = store.get("account", account.toString(), false);
    row.put("memberType", accountRow.path("memberType").asString("member"));
    if (mfa) row.put("stepUpAt", now.toString());
    refresh(row);
    return new Issued(row, cookie, csrf);
  }

  private void refresh(ObjectNode row) {
    Instant now = clock.instant();
    String access =
        tokens.access(
            TenantContext.require(),
            row.path("accountId").asString(),
            "auriga",
            "user",
            row.path("audience").asString(),
            UUID.fromString(row.path("sessionId").asString()),
            null,
            Instant.parse(row.path("authTime").asString()),
            row.has("stepUpAt") ? Instant.parse(row.path("stepUpAt").asString()) : null,
            "openid",
            row.path("memberType").asString("member"));
    row.put("accessToken", access);
    row.put("accessTokenExpiresAt", now.plusSeconds(300).toString());
    store.save("session", row);
    cache(row);
  }

  private void cache(ObjectNode row) {
    ObjectNode value = Json.object();
    for (String f :
        List.of(
            "sessionId",
            "accountId",
            "tenantId",
            "audience",
            "accessToken",
            "accessTokenExpiresAt",
            "expiresAt",
            "idleExpiresAt",
            "mfa",
            "authTime",
            "stepUpAt",
            "csrfHash")) if (row.has(f)) value.set(f, row.get(f));
    kv.put(row.path("cookieHash").asString(), value);
  }

  public ObjectNode activeByCookie(String raw, String audience) {
    if (raw == null || raw.isEmpty()) return null;
    ObjectNode row = store.get("session", Secrets.hash(raw), true);
    return valid(row) && row.path("audience").asString().equals(audience) ? row : null;
  }

  public ObjectNode activeById(String id) {
    ObjectNode row = store.byId("session", id, true);
    return valid(row) ? row : null;
  }

  public boolean valid(ObjectNode row) {
    if (row == null
        || row.path("revoked").asBoolean()
        || !clock.instant().isBefore(Instant.parse(row.path("expiresAt").asString()))
        || !clock.instant().isBefore(Instant.parse(row.path("idleExpiresAt").asString())))
      return false;
    ObjectNode account = store.get("account", row.path("accountId").asString(), false);
    return account != null && account.path("status").asString().equals("active");
  }

  public void stepUp(ObjectNode row) {
    row.put("mfa", true);
    row.put("stepUpAt", clock.instant().toString());
    refresh(row);
  }

  public void permissionsChanged(String accountId) {
    for (ObjectNode candidate : store.all("session"))
      if (candidate.path("accountId").asString().equals(accountId)) {
        ObjectNode row = store.byId("session", candidate.path("id").asString(), true);
        if (row.path("revoked").asBoolean()) continue;
        row.put("accessTokenExpiresAt", Instant.EPOCH.toString());
        store.save("session", row);
        cache(row);
      }
  }

  public void revokeOne(ObjectNode row, String reason) {
    ObjectNode locked = store.byId("session", row.path("id").asString(), true);
    IamError.require(locked != null, "not_found", 404);
    row = locked;
    row.put("revoked", true);
    store.save("session", row);
    kv.delete(row.path("cookieHash").asString());
    Map<String, Object> data = new LinkedHashMap<>();
    data.put("accountId", row.path("accountId").asString());
    data.put("sessionIds", List.of(row.path("sessionId").asString()));
    data.put("reason", reason);
    data.put(
        "actorId",
        RequestContext.currentPrincipal()
            .filter(p -> p.subject() != null && !p.subject().startsWith("svc:"))
            .map(p -> p.subject())
            .orElse(null));
    events.publish("identity.session_revoked", row.path("accountId").asString(), data);
    audit.record(
        AuditEntry.builder("iam.session.revoke")
            .target("session", row.path("sessionId").asString())
            .detail("reason", reason)
            .build());
  }

  static Timestamp timestamp(String time) {
    Instant t = Instant.parse(time);
    return Timestamp.newBuilder().setSeconds(t.getEpochSecond()).setNanos(t.getNano()).build();
  }

  private static String audience(SessionAudience a) {
    return switch (a) {
      case SESSION_AUDIENCE_PORTAL -> "portal";
      case SESSION_AUDIENCE_ADMIN -> "admin";
      default -> throw IamError.error("invalid_audience", 400);
    };
  }

  @Override
  public IntrospectSessionResponse introspect(IntrospectSessionRequest request) {
    return store.tx(
        () -> {
          ObjectNode row =
              activeByCookie(request.getSessionToken(), audience(request.getAudience()));
          if (row == null) return IntrospectSessionResponse.getDefaultInstance();
          if (!clock
              .instant()
              .plusSeconds(60)
              .isBefore(Instant.parse(row.path("accessTokenExpiresAt").asString()))) refresh(row);
          Session.Builder s =
              Session.newBuilder()
                  .setSessionId(row.path("sessionId").asString())
                  .setAccountId(row.path("accountId").asString())
                  .setTenantId(row.path("tenantId").asString())
                  .setAudience(request.getAudience())
                  .setProviderId(row.path("providerId").asString())
                  .setMfa(row.path("mfa").asBoolean())
                  .setCreatedAt(timestamp(row.path("authTime").asString()))
                  .setExpiresAt(timestamp(row.path("expiresAt").asString()))
                  .setIdleExpiresAt(timestamp(row.path("idleExpiresAt").asString()));
          if (row.has("stepUpAt")) s.setStepUpAt(timestamp(row.path("stepUpAt").asString()));
          return IntrospectSessionResponse.newBuilder()
              .setActive(true)
              .setSession(s)
              .setAccessToken(row.path("accessToken").asString())
              .setAccessTokenExpiresAt(timestamp(row.path("accessTokenExpiresAt").asString()))
              .build();
        });
  }

  @Override
  public TouchSessionResponse touch(TouchSessionRequest request) {
    return store.tx(
        () -> {
          ObjectNode row =
              activeByCookie(request.getSessionToken(), audience(request.getAudience()));
          if (row == null) return TouchSessionResponse.getDefaultInstance();
          Instant absolute = Instant.parse(row.path("expiresAt").asString()),
              idle = clock.instant().plusSeconds(row.path("idleSeconds").asLong());
          row.put("idleExpiresAt", (idle.isBefore(absolute) ? idle : absolute).toString());
          store.save("session", row);
          cache(row);
          return TouchSessionResponse.newBuilder()
              .setActive(true)
              .setIdleExpiresAt(timestamp(row.path("idleExpiresAt").asString()))
              .build();
        });
  }

  @Override
  public RevokeSessionsResponse revoke(RevokeSessionsRequest request) {
    IamError.require(
        request.getReason() != RevokeReason.REVOKE_REASON_UNSPECIFIED
            && request.getReason() != RevokeReason.UNRECOGNIZED,
        "invalid_revoke_reason",
        400);
    IamError.require(
        TenantContext.require().toString().equals(request.getTenantId()), "tenant_mismatch", 403);
    return store.tx(
        () -> {
          long count = 0;
          for (ObjectNode row : store.all("session"))
            if (row.path("accountId").asString().equals(request.getAccountId())
                && (request.getSessionIdsList().isEmpty()
                    || request.getSessionIdsList().contains(row.path("sessionId").asString()))
                && !row.path("revoked").asBoolean()) {
              revokeOne(
                  row,
                  request
                      .getReason()
                      .name()
                      .replace("REVOKE_REASON_", "")
                      .toLowerCase(Locale.ROOT));
              count++;
            }
          if (request.getIncludeDevices())
            for (ObjectNode device : store.all("device"))
              if (device.path("accountId").asString().equals(request.getAccountId())) {
                device.put("revoked", true);
                store.save("device", device);
              }
          return RevokeSessionsResponse.newBuilder().setRevoked(count).build();
        });
  }
}
