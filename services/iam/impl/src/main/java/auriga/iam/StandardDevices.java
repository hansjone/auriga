package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import java.time.*;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Standard RFC8628 devices retain their own refresh family and authoritative portal source. */
final class StandardDevices {
  static final String MODE = "standard_device";
  static final Set<String> AI = Set.of(DeviceAiResource.INVOKE, "auriga.connect.collab.use");

  record State(ObjectNode session, ObjectNode device, Instant expires) {}

  private final IamStore store;
  private final SessionManager sessions;
  private final TokenService tokens;
  private final Clock clock;
  private final Map<String, OAuthClient> clients;
  private final InstanceAiAuthorization authorization;

  StandardDevices(
      IamStore store,
      SessionManager sessions,
      TokenService tokens,
      Clock clock,
      Map<String, OAuthClient> clients,
      InstanceAiAuthorization authorization) {
    this.store = store;
    this.sessions = sessions;
    this.tokens = tokens;
    this.clock = clock;
    this.clients = clients;
    this.authorization = authorization;
  }

  static boolean client(OAuthClient client) {
    return client != null
        && client.platform()
        && !client.nativeConnect()
        && (client.secretHash() == null || client.secretHash().isEmpty())
        && client.grants().contains(OAuthProvider.DEVICE)
        && client.grants().contains("refresh_token")
        && client.scopes().contains("auriga.connect");
  }

  static Set<String> scopes(String scope) {
    return scope == null || scope.isBlank()
        ? Set.of()
        : Set.copyOf(Arrays.asList(scope.strip().split(" +")));
  }

  private GetEffectivePermissionsResponse permissions(String account) {
    store.requireTransactionReleased();
    if (io.grpc.Context.current().isCancelled())
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    try {
      var result =
          authorization.getEffectivePermissions(
              GetEffectivePermissionsRequest.newBuilder()
                  .setTenantId(TenantContext.require().toString())
                  .setAccountId(account)
                  .build());
      if (result == null || io.grpc.Context.current().isCancelled())
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      if (result.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || !Set.of(MemberType.MEMBER_TYPE_MEMBER, MemberType.MEMBER_TYPE_EXTERNAL)
              .contains(result.getMemberType())) return null;
      return result;
    } catch (AurigaException failure) {
      if (Set.of("common.forbidden", "common.not_found").contains(failure.errorCode().code()))
        return null;
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException failure) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private State source(OAuthClient client, ObjectNode grant) {
    if (!client(client)
        || !client.tenant().equals(TenantContext.require())
        || grant == null
        || !MODE.equals(grant.path("authorizationMode").asString())
        || !client.id().equals(grant.path("clientId").asString())) return null;
    var session = sessions.activeById(grant.path("sessionId").asString());
    if (session == null
        || !"portal".equals(session.path("audience").asString())
        || !session.path("accountId").equals(grant.path("accountId"))
        || !session.path("providerId").equals(grant.path("providerId"))
        || !UacSessions.boundTo(grant, session)) return null;
    var provider = store.get("idp_provider", session.path("providerId").asString(), false);
    if (provider == null
        || !provider.path("enabled").asBoolean()
        || !scopes(grant.path("scope").asString()).contains("auriga.connect")
        || !client.scopes().containsAll(scopes(grant.path("scope").asString()))) return null;
    ObjectNode device = null;
    Instant expiry =
        minimum(
            Instant.parse(session.path("expiresAt").asString()),
            Instant.parse(session.path("idleExpiresAt").asString()));
    if ("refresh".equals(grant.path("kind").asString()))
      expiry = minimum(expiry, Instant.parse(grant.path("expiresAt").asString()));
    if (grant.has("deviceId")) {
      device = store.get("device", grant.path("deviceId").asString(), true);
      if (device == null
          || device.path("revoked").asBoolean()
          || !MODE.equals(device.path("authorizationMode").asString())
          || !device.path("accountId").equals(grant.path("accountId"))
          || !device.path("providerId").equals(grant.path("providerId"))
          || !device.path("sessionId").equals(grant.path("sessionId"))
          || !client.id().equals(device.path("clientId").asString())
          || !scopes(device.path("scope").asString())
              .containsAll(scopes(grant.path("scope").asString()))
          || !UacSessions.boundTo(device, session)) return null;
      expiry = minimum(expiry, Instant.parse(device.path("expiresAt").asString()));
    }
    expiry = expiry.truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    if (!clock.instant().isBefore(expiry)) return null;
    return new State(session, device, expiry);
  }

  ObjectNode token(OAuthClient client, Map<String, String> input) {
    boolean refresh = "refresh_token".equals(input.get("grant_type"));
    var allowed =
        refresh
            ? Set.of("grant_type", "client_id", "refresh_token", "scope", "resource")
            : Set.of("grant_type", "client_id", "device_code");
    if (!allowed.containsAll(input.keySet())) return OAuthProvider.error("invalid_request");
    boolean ai = input.containsKey("resource");
    if (ai && (!refresh || !DeviceAiResource.RESOURCE.equals(input.get("resource"))))
      return OAuthProvider.error("invalid_target");
    String raw = input.getOrDefault(refresh ? "refresh_token" : "device_code", "");
    if (raw.isEmpty() || raw.length() > 16384) return OAuthProvider.error("invalid_grant");
    String lookup = Secrets.hash(raw);
    ObjectNode snapshot =
        store.tx(
            () -> {
              var grant = store.get("oauth_grant", lookup, true);
              if (grant == null
                  || !MODE.equals(grant.path("authorizationMode").asString())
                  || !client.id().equals(grant.path("clientId").asString())
                  || !grant.path("kind").asString().equals(refresh ? "refresh" : "device")
                  || grant.path("revoked").asBoolean()) return OAuthProvider.error("invalid_grant");
              if (!clock.instant().isBefore(Instant.parse(grant.path("expiresAt").asString())))
                return OAuthProvider.error(refresh ? "invalid_grant" : "expired_token");
              if (!refresh) {
                if (grant.path("used").asBoolean()) return OAuthProvider.error("invalid_grant");
                if (clock.instant().isBefore(Instant.parse(grant.path("nextPoll").asString()))) {
                  grant.put("interval", grant.path("interval").asInt() + 5);
                  grant.put(
                      "nextPoll",
                      clock.instant().plusSeconds(grant.path("interval").asInt()).toString());
                  store.save("oauth_grant", grant);
                  return OAuthProvider.error("slow_down");
                }
                grant.put(
                    "nextPoll",
                    clock.instant().plusSeconds(grant.path("interval").asInt()).toString());
                store.save("oauth_grant", grant);
                if ("pending".equals(grant.path("status").asString()))
                  return OAuthProvider.error("authorization_pending");
                if (!"approved".equals(grant.path("status").asString()))
                  return OAuthProvider.error("access_denied");
              }
              return grant;
            });
    if (snapshot.has("error")) return snapshot;
    String scope = input.getOrDefault("scope", snapshot.path("scope").asString());
    if (!scopes(scope).contains("auriga.connect")
        || !scopes(snapshot.path("scope").asString()).containsAll(scopes(scope)))
      return OAuthProvider.error("invalid_scope");
    State before = store.tx(() -> source(client, snapshot));
    if (before == null) return OAuthProvider.error("invalid_grant");
    var effective = permissions(before.session().path("accountId").asString());
    if (effective == null) return OAuthProvider.error("invalid_grant");
    Set<String> granted = new HashSet<>(effective.getPermissionsList());
    granted.removeIf(p -> !TokenService.allowedPermission(p, "device") || ai && !AI.contains(p));
    if (granted.isEmpty()) return OAuthProvider.error("invalid_grant");
    return tokens.withSigningKey(
        () ->
            store.tx(
                () -> {
                  // The source lock precedes grant/device locks, matching approval and source
                  // revocation.
                  var lockedSource = sessions.activeById(snapshot.path("sessionId").asString());
                  var grant = store.get("oauth_grant", lookup, true);
                  if (lockedSource == null
                      || grant == null
                      || !grant.path("sessionId").equals(snapshot.path("sessionId"))
                      || grant.path("revoked").asBoolean()
                      || !clock
                          .instant()
                          .isBefore(Instant.parse(grant.path("expiresAt").asString())))
                    return OAuthProvider.error("invalid_grant");
                  var current = source(client, grant);
                  if (current == null) return OAuthProvider.error("invalid_grant");
                  if (grant.path("used").asBoolean()) {
                    if (refresh && current.device() != null)
                      sessions.revokeDevice(current.device());
                    return OAuthProvider.error("invalid_grant");
                  }
                  if (!refresh) {
                    if (!"approved".equals(grant.path("status").asString()))
                      return OAuthProvider.error("invalid_grant");
                    String did = auriga.common.persistence.UuidV7.next().toString();
                    ObjectNode device =
                        Json.object(
                            "deviceId",
                            did,
                            "accountId",
                            grant.path("accountId"),
                            "providerId",
                            grant.path("providerId"),
                            "sessionId",
                            grant.path("sessionId"),
                            "clientId",
                            client.id(),
                            "scope",
                            scope,
                            "authorizationMode",
                            MODE,
                            "kind",
                            "dsh_connect",
                            "name",
                            "Device",
                            "revoked",
                            false,
                            "expiresAt",
                            current.session().path("expiresAt"),
                            "createdAt",
                            clock.instant().toString());
                    if (current.session().has("uacSessionBinding"))
                      device.set("uacSessionBinding", current.session().path("uacSessionBinding"));
                    store.insert("device", did, device, null);
                    grant.put("deviceId", did);
                  }
                  grant.put("used", true);
                  store.save("oauth_grant", grant);
                  ObjectNode device = store.get("device", grant.path("deviceId").asString(), true);
                  var identity =
                      new DeviceAccessAuthority.Identity(
                          client.tenant().toString(),
                          grant.path("accountId").asString(),
                          grant.path("deviceId").asString(),
                          current.expires());
                  var issued =
                      tokens.standardDevice(
                          identity,
                          current.session(),
                          ai ? "auriga.ai-gateway" : "auriga",
                          scope,
                          granted,
                          effective.getMemberType() == MemberType.MEMBER_TYPE_EXTERNAL
                              ? "external"
                              : "member");
                  String successor = Secrets.random();
                  var next =
                      Json.object(
                          "kind",
                          "refresh",
                          "authorizationMode",
                          MODE,
                          "clientId",
                          client.id(),
                          "deviceId",
                          device.path("deviceId"),
                          "sessionId",
                          grant.path("sessionId"),
                          "accountId",
                          grant.path("accountId"),
                          "providerId",
                          grant.path("providerId"),
                          "scope",
                          scope,
                          "used",
                          false,
                          "expiresAt",
                          device.path("expiresAt"));
                  if (refresh)
                    next.put(
                        "expiresAt",
                        minimum(
                                Instant.parse(grant.path("expiresAt").asString()),
                                Instant.parse(device.path("expiresAt").asString()))
                            .toString());
                  if (device.has("uacSessionBinding"))
                    next.set("uacSessionBinding", device.path("uacSessionBinding"));
                  store.insert(
                      "oauth_grant",
                      Secrets.hash(successor),
                      next,
                      Instant.parse(next.path("expiresAt").asString()));
                  return Json.object(
                      "access_token",
                      issued.accessToken(),
                      "token_type",
                      "Bearer",
                      "refresh_token",
                      successor,
                      "expires_in",
                      issued.expiresAt().getEpochSecond() - clock.instant().getEpochSecond(),
                      "scope",
                      scope);
                }));
  }

  DeviceAccessAuthority.Identity access(String raw, String audience, String permission) {
    var claims = claims(raw, audience);
    if (claims == null || permission != null && !contains(claims.path("perms"), permission))
      return null;
    String did = claims.path("did").asString();
    ObjectNode device = store.tx(() -> store.get("device", did, false));
    if (device == null) return null;
    var client = clients.get(device.path("clientId").asString());
    State state = store.tx(() -> source(client, device));
    if (state == null || !matches(claims, state)) return null;
    var effective = permissions(claims.path("sub").asString());
    if (effective == null
        || permission != null && !effective.getPermissionsList().contains(permission)) return null;
    if (effective.getPermissionsList().stream()
        .noneMatch(p -> TokenService.allowedPermission(p, "device"))) return null;
    return store.tx(
        () -> {
          var current = source(client, device);
          if (current == null || !matches(claims, current)) return null;
          Instant expiry =
              minimum(current.expires(), Instant.ofEpochSecond(claims.path("exp").asLong()));
          return clock.instant().isBefore(expiry)
              ? new DeviceAccessAuthority.Identity(
                  TenantContext.require().toString(), claims.path("sub").asString(), did, expiry)
              : null;
        });
  }

  DeviceAccessAuthority.Identity credential(
      String tenant, String account, String did, String permission) {
    if (!TenantContext.require().toString().equals(tenant))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    var device = store.tx(() -> store.get("device", did, false));
    if (device == null || !account.equals(device.path("accountId").asString())) return null;
    var client = clients.get(device.path("clientId").asString());
    var state = store.tx(() -> source(client, device));
    if (state == null) return null;
    var effective = permissions(account);
    if (effective == null
        || permission != null && !effective.getPermissionsList().contains(permission)) return null;
    return store.tx(
        () -> {
          var current = source(client, device);
          return current == null
              ? null
              : new DeviceAccessAuthority.Identity(tenant, account, did, current.expires());
        });
  }

  /** Local recheck only; the owning network operation obtains Org authority outside its locks. */
  DeviceAccessAuthority.Identity credentialState(String tenant, String account, String did) {
    if (!TenantContext.require().toString().equals(tenant))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return store.tx(
        () -> {
          var device = store.get("device", did, false);
          if (device == null || !account.equals(device.path("accountId").asString())) return null;
          var state = source(clients.get(device.path("clientId").asString()), device);
          return state == null
              ? null
              : new DeviceAccessAuthority.Identity(tenant, account, did, state.expires());
        });
  }

  ObjectNode identity(String raw) {
    return identity(raw, false);
  }

  ObjectNode profile(String raw) {
    return identity(raw, true);
  }

  private ObjectNode identity(String raw, boolean profile) {
    var valid = access(raw, "auriga", null);
    if (valid == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    return store.tx(
        () -> {
          var device = store.get("device", valid.device(), false);
          if (device == null || !valid.account().equals(device.path("accountId").asString()))
            throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          var state = source(clients.get(device.path("clientId").asString()), device);
          if (state == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          if (profile) {
            var account = store.get("account", valid.account(), false);
            if (account == null || !"active".equals(account.path("status").asString()))
              throw new AurigaException(CommonErrors.UNAUTHENTICATED);
            var projected =
                Json.object(
                    "account",
                    Json.object(
                        "id",
                        valid.account(),
                        "tenantId",
                        valid.tenant(),
                        "displayName",
                        account.path("displayName").asString("")));
            new ContractSchemas()
                .validate(
                    "auth/standard-device.schema.json#/$defs/StandardDeviceProfile", projected);
            return projected;
          }

          var result =
              Json.object(
                  "accountId",
                  valid.account(),
                  "memberId",
                  valid.account(),
                  "tenantId",
                  valid.tenant(),
                  "deviceId",
                  valid.device(),
                  "clientId",
                  device.path("clientId"),
                  "providerId",
                  device.path("providerId"),
                  "authorizationMode",
                  MODE,
                  "expiresAt",
                  minimum(valid.expiresAt(), state.expires()).toString());
          if (device.has("uacSessionBinding"))
            result.set("uacSessionBinding", device.path("uacSessionBinding"));
          new ContractSchemas()
              .validate("auth/standard-device.schema.json#/$defs/StandardDeviceIdentity", result);
          return result;
        });
  }

  ObjectNode revoke(OAuthClient client, Map<String, String> input) {
    if (!Set.of("client_id", "token", "token_type_hint").containsAll(input.keySet())
        || input.containsKey("token_type_hint")
            && !Set.of("access_token", "refresh_token").contains(input.get("token_type_hint")))
      return OAuthProvider.error("invalid_request");
    String raw = input.getOrDefault("token", "");
    if (raw.isEmpty() || raw.length() > 16384) return Json.object();
    ObjectNode proof = store.tx(() -> store.get("oauth_grant", Secrets.hash(raw), false));
    if (proof != null) {
      if (!"refresh".equals(proof.path("kind").asString())
          || !MODE.equals(proof.path("authorizationMode").asString())
          || !client.id().equals(proof.path("clientId").asString())
          || proof.path("revoked").asBoolean()
          || !clock.instant().isBefore(Instant.parse(proof.path("expiresAt").asString())))
        return Json.object();
    } else {
      proof = claims(raw, null);
      if (proof == null) return Json.object();
      if (access(raw, proof.path("aud").asString(), null) == null) return Json.object();
    }
    ObjectNode selected = proof;
    String did = proof.path("deviceId").asString(proof.path("did").asString());
    return store.tx(
        () -> {
          var initial = store.get("device", did, false);
          if (initial == null
              || !MODE.equals(initial.path("authorizationMode").asString())
              || !client.id().equals(initial.path("clientId").asString())
              || !initial
                  .path("accountId")
                  .asString()
                  .equals(selected.path("accountId").asString(selected.path("sub").asString())))
            return Json.object();
          store.byId("session", initial.path("sessionId").asString(), true);
          var device = store.get("device", did, true);
          if (device != null && client.id().equals(device.path("clientId").asString()))
            sessions.revokeDevice(device);
          return Json.object();
        });
  }

  private ObjectNode claims(String raw, String audience) {
    if (raw == null || raw.isEmpty() || raw.length() > 16384) return null;
    ObjectNode claims;
    try {
      claims = tokens.verify(raw);
    } catch (AurigaException failure) {
      if (Set.of(400, 401, 403).contains(failure.errorCode().httpStatus())) return null;
      throw failure;
    }
    if (!TenantContext.require().toString().equals(claims.path("tid").asString())) return null;
    String aud = claims.path("aud").asString();
    if (!claims.path("aud").isString()
        || !Set.of("auriga", "auriga.ai-gateway").contains(aud)
        || audience != null && !audience.equals(aud)
        || !"device".equals(claims.path("ptyp").asString())
        || !"device".equals(claims.path("ctx").asString())
        || !uuid(claims.path("did").asString())
        || !uuid(claims.path("sub").asString())
        || !scopes(claims.path("scope").asString()).contains("auriga.connect")
        || claims.has("sid")
        || claims.has("instanceId")
        || claims.has("roles")
        || claims.path("iat").asLong() > clock.instant().plusSeconds(30).getEpochSecond()
        || claims.path("exp").asLong() > claims.path("iat").asLong() + 300) return null;
    if ("auriga.ai-gateway".equals(aud)) {
      if (claims.path("perms").isEmpty()) return null;
      for (var permission : claims.path("perms"))
        if (!AI.contains(permission.asString())) return null;
    }
    return claims;
  }

  private static boolean matches(ObjectNode claims, State state) {
    return claims.path("sub").equals(state.session().path("accountId"))
        && claims.path("did").equals(state.device().path("deviceId"))
        && claims.path("uacSessionBinding").equals(state.device().path("uacSessionBinding"))
        && claims.path("auth_time").asLong()
            == Instant.parse(state.session().path("authTime").asString()).getEpochSecond()
        && scopes(state.device().path("scope").asString())
            .containsAll(scopes(claims.path("scope").asString()));
  }

  private static boolean contains(tools.jackson.databind.JsonNode values, String permission) {
    for (var value : values) if (permission.equals(value.asString())) return true;
    return false;
  }

  static Instant minimum(Instant a, Instant b) {
    return a.isBefore(b) ? a : b;
  }

  private static boolean uuid(String value) {
    try {
      return UUID.fromString(value).toString().equals(value);
    } catch (IllegalArgumentException failure) {
      return false;
    }
  }
}
