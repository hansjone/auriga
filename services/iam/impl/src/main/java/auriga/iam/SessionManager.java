package auriga.iam;

import auriga.common.audit.*;
import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.events.EventPublisher;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.Sessions;
import com.google.protobuf.Timestamp;
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

  void awaitVisible(Issued issued) {
    store.requireTransactionReleased();
    kv.awaitVisible(Secrets.hash(issued.cookie()));
  }

  <T> T withSigningKey(java.util.function.Supplier<T> work) {
    return tokens.withSigningKey(work);
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
            "csrfHash",
            "adminCsrfTokens")) if (row.has(f)) value.set(f, row.get(f).deepCopy());
    kv.put(row.path("cookieHash").asString(), value);
  }

  public ObjectNode activeByCookie(String raw, String audience) {
    if (raw == null || raw.isEmpty()) return null;
    ObjectNode row = store.get("session", Secrets.hash(raw), true);
    if (!valid(row)) {
      clearInvalid(row);
      return null;
    }
    return row.path("audience").asString().equals(audience) ? row : null;
  }

  public ObjectNode activeById(String id) {
    ObjectNode row = store.byId("session", id, true);
    if (!valid(row)) {
      clearInvalid(row);
      return null;
    }
    return row;
  }

  private void clearInvalid(ObjectNode row) {
    if (row == null) return;
    if (row.has("adminCsrfTokens")) {
      row.remove("adminCsrfTokens");
      store.save("session", row);
    }
    kv.delete(row.path("cookieHash").asString());
  }

  public boolean valid(ObjectNode row) {
    if (row == null
        || !TenantContext.require().toString().equals(row.path("tenantId").asString())
        || row.path("revoked").asBoolean()
        || !clock.instant().isBefore(Instant.parse(row.path("expiresAt").asString()))
        || !clock.instant().isBefore(Instant.parse(row.path("idleExpiresAt").asString())))
      return false;
    ObjectNode account = store.get("account", row.path("accountId").asString(), false);
    return account != null && account.path("status").asString().equals("active");
  }

  public void stepUp(ObjectNode row) {
    store.tx(
        () -> {
          ObjectNode locked = activeById(row.path("id").asString());
          if (locked == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          locked.put("mfa", true);
          locked.put("stepUpAt", clock.instant().toString());
          refresh(locked);
          return null;
        });
  }

  public void permissionsChanged(String accountId) {
    store.tx(
        () -> {
          for (ObjectNode candidate : store.all("session"))
            if (candidate.path("accountId").asString().equals(accountId)) {
              ObjectNode row = store.byId("session", candidate.path("id").asString(), true);
              if (!valid(row)) continue;
              row.put("accessTokenExpiresAt", Instant.EPOCH.toString());
              store.save("session", row);
              cache(row);
            }
          return null;
        });
  }

  public void revokeOne(ObjectNode row, String reason) {
    store.tx(
        () -> {
          revokeLocked(row, reason);
          return null;
        });
  }

  private void revokeLocked(ObjectNode row, String reason) {
    ObjectNode locked = store.byId("session", row.path("id").asString(), true);
    IamError.require(locked != null, "not_found", 404);
    row = locked;
    if (row.path("revoked").asBoolean()) return;
    row.put("revoked", true);
    row.remove("adminCsrfTokens");
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

  /** The cookie, JWT subject, sid and delegated tenant must identify the same locked row. */
  public Instant registerAdminCsrf(
      String cookie, String token, auriga.common.security.AurigaPrincipal principal) {
    if (!canonicalCsrf(token)) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    Instant registered =
        store.tx(
            () -> {
              ObjectNode row = activeByCookie(cookie, "admin");
              if (row == null) return null;
              if (principal == null
                  || !principal.isAdminContext()
                  || principal.type() != auriga.common.security.PrincipalType.USER
                  || !TenantContext.require().equals(principal.tenantId())
                  || !row.path("sessionId").asString().equals(principal.sessionId())
                  || !row.path("accountId").asString().equals(principal.subject()))
                throw new AurigaException(CommonErrors.UNAUTHENTICATED);
              Instant now = clock.instant();
              var entries = liveCsrf(row, now);
              String hash = Secrets.hash(token);
              for (var entry : entries)
                if (Secrets.same(entry.path("hash").asString(), hash)) {
                  row.set("adminCsrfTokens", entries);
                  store.save("session", row);
                  cache(row);
                  return Instant.parse(entry.path("expiresAt").asString());
                }
              if (entries.size() >= 32) {
                Instant first =
                    java.util.stream.StreamSupport.stream(entries.spliterator(), false)
                        .map(e -> Instant.parse(e.path("expiresAt").asString()))
                        .min(Comparator.naturalOrder())
                        .orElseThrow();
                long retry = Math.max(1, (Duration.between(now, first).toMillis() + 999) / 1000);
                throw AurigaException.of(CommonErrors.RATE_LIMITED, "retryAfter", retry);
              }
              Instant expiry = now.plusSeconds(900);
              for (String field : List.of("expiresAt", "idleExpiresAt")) {
                Instant bound = Instant.parse(row.path(field).asString());
                if (bound.isBefore(expiry)) expiry = bound;
              }
              entries.add(Json.object("hash", hash, "expiresAt", expiry.toString()));
              row.set("adminCsrfTokens", entries);
              store.save("session", row);
              cache(row);
              return expiry;
            });
    if (registered == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    return registered;
  }

  static boolean canonicalCsrf(String token) {
    if (token == null || !token.matches("[A-Za-z0-9_-]{43}")) return false;
    try {
      byte[] bytes = Base64.getUrlDecoder().decode(token);
      return bytes.length == 32
          && Base64.getUrlEncoder().withoutPadding().encodeToString(bytes).equals(token);
    } catch (IllegalArgumentException e) {
      return false;
    }
  }

  private tools.jackson.databind.node.ArrayNode liveCsrf(ObjectNode row, Instant now) {
    var entries = Json.MAPPER.createArrayNode();
    for (var entry : row.path("adminCsrfTokens"))
      if (now.isBefore(Instant.parse(entry.path("expiresAt").asString())))
        entries.add(entry.deepCopy());
    return entries;
  }

  @Override
  public VerifyAdminSessionCsrfResponse verifyAdminCsrf(VerifyAdminSessionCsrfRequest request) {
    try {
      VerifyAdminSessionCsrfResponse result =
          store.tx(
              () -> {
                ObjectNode row = activeByCookie(request.getSessionToken(), "admin");
                if (row == null) return null;
                String hash = Secrets.hash(request.getCsrfToken());
                boolean match = false;
                // Evaluate every candidate, rather than exposing the matching slot through early
                // exit.
                for (var entry : liveCsrf(row, clock.instant()))
                  match |= Secrets.same(entry.path("hash").asString(), hash);
                if (!canonicalCsrf(request.getCsrfToken()) || !match)
                  return VerifyAdminSessionCsrfResponse.getDefaultInstance();
                return VerifyAdminSessionCsrfResponse.newBuilder()
                    .setActive(true)
                    .setSession(session(row, SessionAudience.SESSION_AUDIENCE_ADMIN))
                    .build();
              });
      if (result == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
      return result;
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE, Map.of(), List.of(), e);
    }
  }

  private Session session(ObjectNode row, SessionAudience audience) {
    var s =
        Session.newBuilder()
            .setSessionId(row.path("sessionId").asString())
            .setAccountId(row.path("accountId").asString())
            .setTenantId(row.path("tenantId").asString())
            .setAudience(audience)
            .setProviderId(row.path("providerId").asString())
            .setMfa(row.path("mfa").asBoolean())
            .setCreatedAt(timestamp(row.path("authTime").asString()))
            .setExpiresAt(timestamp(row.path("expiresAt").asString()))
            .setIdleExpiresAt(timestamp(row.path("idleExpiresAt").asString()));
    if (row.has("stepUpAt")) s.setStepUpAt(timestamp(row.path("stepUpAt").asString()));
    return s.build();
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
