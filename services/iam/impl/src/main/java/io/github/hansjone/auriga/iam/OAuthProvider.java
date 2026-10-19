package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.common.context.TenantContext;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.time.*;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** OAuth grants share IAM sessions, revocation, and tenant RLS; all secrets are hashed at rest. */
public final class OAuthProvider {
  public static final String DEVICE = "urn:ietf:params:oauth:grant-type:device_code";
  private final IamStore store;
  private final SessionManager sessions;
  private final TokenService tokens;
  private final Clock clock;
  private final Map<String, OAuthClient> clients;

  public OAuthProvider(
      IamStore store,
      SessionManager sessions,
      TokenService tokens,
      Clock clock,
      Collection<OAuthClient> clients) {
    this.store = store;
    this.sessions = sessions;
    this.tokens = tokens;
    this.clock = clock;
    Map<String, OAuthClient> registered = new HashMap<>();
    for (var client : clients)
      if (registered.putIfAbsent(client.id(), client) != null)
        throw new IllegalArgumentException("Duplicate OAuth client");
    this.clients = Map.copyOf(registered);
  }

  public ObjectNode metadata() {
    String issuer = tokens.issuer();
    return Json.object(
        "issuer",
        issuer,
        "authorization_endpoint",
        issuer + "/oauth2/authorize",
        "token_endpoint",
        issuer + "/oauth2/token",
        "jwks_uri",
        issuer + "/oauth2/jwks",
        "userinfo_endpoint",
        issuer + "/userinfo",
        "device_authorization_endpoint",
        issuer + "/oauth2/device_authorization",
        "response_types_supported",
        List.of("code"),
        "grant_types_supported",
        List.of("authorization_code", "refresh_token", "client_credentials", DEVICE),
        "code_challenge_methods_supported",
        List.of("S256"),
        "subject_types_supported",
        List.of("public"),
        "id_token_signing_alg_values_supported",
        List.of("ES256"),
        "token_endpoint_auth_methods_supported",
        List.of("client_secret_basic", "client_secret_post", "none"),
        "scopes_supported",
        List.of("openid", "profile", "email", "offline_access", "auriga.connect"));
  }

  private OAuthClient client(String id) {
    OAuthClient c = id == null ? null : clients.get(id);
    IamError.require(
        c != null && c.tenant().equals(TenantContext.require()), "invalid_client", 401);
    return c;
  }

  private boolean authenticated(OAuthClient c, String secret) {
    return c.secretHash() == null || c.secretHash().isEmpty()
        ? secret == null || secret.isEmpty()
        : secret != null && Secrets.same(c.secretHash(), Secrets.hash(secret));
  }

  private static Set<String> scopes(String scope) {
    return scope == null || scope.isBlank()
        ? Set.of()
        : new HashSet<>(List.of(scope.trim().split(" +")));
  }

  public String authorize(Map<String, String> input, String cookie, String audience) {
    return store.tx(
        () -> {
          OAuthClient c = client(input.get("client_id"));
          IamError.require(
              c.grants().contains("authorization_code")
                  && "code".equals(input.get("response_type")),
              "unsupported_response_type",
              400);
          String redirect = input.get("redirect_uri");
          IamError.require(c.redirectUris().contains(redirect), "invalid_redirect_uri", 400);
          IamError.require(
              "S256".equals(input.get("code_challenge_method"))
                  && input.getOrDefault("code_challenge", "").matches("[A-Za-z0-9_-]{43}"),
              "invalid_pkce",
              400);
          IamError.require(
              c.scopes().containsAll(scopes(input.get("scope"))), "invalid_scope", 400);
          ObjectNode session = sessions.activeByCookie(cookie, audience);
          IamError.require(session != null, "login_required", 401);
          // This endpoint is the registered first-party consent boundary. Third-party consent is
          // explicit.
          // Only the CSRF-checked POST consent handler may call this method.
          String code = Secrets.random();
          store.insert(
              "oauth_grant",
              Secrets.hash(code),
              Json.object(
                  "kind",
                  "code",
                  "clientId",
                  c.id(),
                  "sessionId",
                  session.path("sessionId").asString(),
                  "redirectUri",
                  redirect,
                  "challenge",
                  input.get("code_challenge"),
                  "nonce",
                  input.getOrDefault("nonce", ""),
                  "scope",
                  input.getOrDefault("scope", ""),
                  "expiresAt",
                  clock.instant().plusSeconds(60).toString(),
                  "used",
                  false),
              clock.instant().plusSeconds(60));
          return redirect
              + (redirect.contains("?") ? "&" : "?")
              + "code="
              + encode(code)
              + (input.containsKey("state") ? "&state=" + encode(input.get("state")) : "");
        });
  }

  public static String challenge(String verifier) {
    try {
      return Base64.getUrlEncoder()
          .withoutPadding()
          .encodeToString(
              MessageDigest.getInstance("SHA-256")
                  .digest(verifier.getBytes(StandardCharsets.US_ASCII)));
    } catch (GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }

  private static String encode(String value) {
    return java.net.URLEncoder.encode(value, StandardCharsets.UTF_8);
  }

  public ObjectNode token(Map<String, String> input) {
    return store.tx(
        () -> {
          OAuthClient c = client(input.get("client_id"));
          if (!authenticated(c, input.get("client_secret"))) return error("invalid_client");
          String type = input.getOrDefault("grant_type", "");
          if (!c.grants().contains(type)) return error("unauthorized_client");
          if (input.containsKey("audience") || input.containsKey("resource"))
            return error("invalid_target");
          return switch (type) {
            case "authorization_code" -> exchangeCode(c, input);
            case "refresh_token" -> refresh(c, input);
            case "client_credentials" -> credentials(c, input);
            case DEVICE -> exchangeDevice(c, input);
            default -> error("unsupported_grant_type");
          };
        });
  }

  private ObjectNode exchangeCode(OAuthClient c, Map<String, String> input) {
    ObjectNode grant = store.get("oauth_grant", Secrets.hash(input.getOrDefault("code", "")), true);
    if (!valid(grant, c, "code")) return error("invalid_grant");
    grant.put("used", true);
    store.save("oauth_grant", grant);
    String verifier = input.getOrDefault("code_verifier", "");
    if (!verifier.matches("[A-Za-z0-9._~-]{43,128}")
        || !Secrets.same(challenge(verifier), grant.path("challenge").asString())
        || !grant.path("redirectUri").asString().equals(input.get("redirect_uri")))
      return error("invalid_grant");
    ObjectNode session = sessions.activeById(grant.path("sessionId").asString());
    if (session == null) return error("invalid_grant");
    return userTokens(c, grant, session, true);
  }

  private boolean valid(ObjectNode grant, OAuthClient c, String kind) {
    return grant != null
        && grant.path("kind").asString().equals(kind)
        && !grant.path("used").asBoolean()
        && grant.path("clientId").asString().equals(c.id())
        && clock.instant().isBefore(Instant.parse(grant.path("expiresAt").asString()));
  }

  private ObjectNode credentials(OAuthClient c, Map<String, String> input) {
    if (c.secretHash() == null || c.secretHash().isBlank()) return error("invalid_client");
    String scope = input.getOrDefault("scope", "");
    if (!c.scopes().containsAll(scopes(scope))) return error("invalid_scope");
    return Json.object(
        "access_token",
        tokens.access(
            c.tenant(),
            c.id(),
            c.audience(),
            "service_account",
            "service",
            null,
            null,
            null,
            null,
            scope),
        "token_type",
        "Bearer",
        "expires_in",
        300,
        "scope",
        scope);
  }

  private ObjectNode refresh(OAuthClient c, Map<String, String> input) {
    ObjectNode grant =
        store.get("oauth_grant", Secrets.hash(input.getOrDefault("refresh_token", "")), true);
    if (grant != null
        && grant.path("used").asBoolean()
        && grant.path("clientId").asString().equals(c.id())) {
      ObjectNode session = sessions.activeById(grant.path("sessionId").asString());
      if (session != null) sessions.revokeOne(session, "security");
      return error("invalid_grant");
    }
    if (!valid(grant, c, "refresh")) return error("invalid_grant");
    ObjectNode session = sessions.activeById(grant.path("sessionId").asString());
    if (session == null) return error("invalid_grant");
    if (grant.has("deviceId")) {
      ObjectNode device = store.get("device", grant.path("deviceId").asString(), true);
      if (device == null || device.path("revoked").asBoolean()) return error("invalid_grant");
    }
    if (input.containsKey("scope")
        && !scopes(grant.path("scope").asString()).containsAll(scopes(input.get("scope"))))
      return error("invalid_scope");
    grant.put("used", true);
    store.save("oauth_grant", grant);
    if (input.containsKey("scope")) grant.put("scope", input.get("scope"));
    return userTokens(c, grant, session, true);
  }

  private ObjectNode userTokens(
      OAuthClient c, ObjectNode grant, ObjectNode session, boolean issueRefresh) {
    boolean device = grant.has("deviceId");
    String scope = grant.path("scope").asString();
    ObjectNode response =
        Json.object(
            "access_token",
            tokens.access(
                c.tenant(),
                session.path("accountId").asString(),
                c.audience(),
                device ? "device" : "user",
                device ? "device" : session.path("audience").asString(),
                device ? null : UUID.fromString(session.path("sessionId").asString()),
                device ? UUID.fromString(grant.path("deviceId").asString()) : null,
                Instant.parse(session.path("authTime").asString()),
                session.has("stepUpAt") ? Instant.parse(session.path("stepUpAt").asString()) : null,
                scope,
                session.path("memberType").asString("member")),
            "token_type",
            "Bearer",
            "expires_in",
            300,
            "scope",
            scope);
    if (scopes(scope).contains("openid") && !device)
      response.put("id_token", tokens.idToken(session, c.id(), grant.path("nonce").asString("")));
    if (issueRefresh
        && c.grants().contains("refresh_token")
        && (device || scopes(scope).contains("offline_access"))) {
      String refresh = Secrets.random();
      ObjectNode next =
          Json.object(
              "kind",
              "refresh",
              "clientId",
              c.id(),
              "sessionId",
              session.path("sessionId").asString(),
              "scope",
              scope,
              "expiresAt",
              session.path("expiresAt").asString(),
              "used",
              false);
      if (device) next.put("deviceId", grant.path("deviceId").asString());
      store.insert(
          "oauth_grant",
          Secrets.hash(refresh),
          next,
          Instant.parse(next.path("expiresAt").asString()));
      response.put("refresh_token", refresh);
    }
    return response;
  }

  public ObjectNode deviceAuthorization(Map<String, String> input) {
    return store.tx(
        () -> {
          OAuthClient c = client(input.get("client_id"));
          IamError.require(
              authenticated(c, input.get("client_secret")) && c.grants().contains(DEVICE),
              "invalid_client",
              401);
          String scope = input.getOrDefault("scope", "auriga.connect");
          IamError.require(
              c.scopes().containsAll(scopes(scope))
                  && scopes(scope).equals(Set.of("auriga.connect")),
              "invalid_scope",
              400);
          store.limit("device-client:" + c.id(), clock.instant(), 10);
          String code = Secrets.random(), alphabet = "BCDFGHJKLMNPQRSTVWXZ";
          SecureRandom random = new SecureRandom();
          StringBuilder u = new StringBuilder();
          for (int i = 0; i < 8; i++) u.append(alphabet.charAt(random.nextInt(alphabet.length())));
          String user = u.substring(0, 4) + "-" + u.substring(4);
          store.insert(
              "oauth_grant",
              Secrets.hash(code),
              Json.object(
                  "kind",
                  "device",
                  "clientId",
                  c.id(),
                  "userCode",
                  user,
                  "scope",
                  scope,
                  "status",
                  "pending",
                  "used",
                  false,
                  "interval",
                  5,
                  "nextPoll",
                  clock.instant().toString(),
                  "expiresAt",
                  clock.instant().plusSeconds(300).toString()),
              clock.instant().plusSeconds(300));
          return Json.object(
              "device_code",
              code,
              "user_code",
              user,
              "verification_uri",
              tokens.issuer() + "/device",
              "verification_uri_complete",
              tokens.issuer() + "/device?user_code=" + user,
              "expires_in",
              300,
              "interval",
              5);
        });
  }

  public void decideDevice(String userCode, String sessionId, boolean approved) {
    store.tx(
        () -> {
          store.limit("device-user-code", clock.instant(), 30);
          store.lock("device:" + userCode);
          ObjectNode grant =
              store.all("oauth_grant").stream()
                  .filter(
                      g ->
                          g.path("userCode")
                              .asString("")
                              .replace("-", "")
                              .equals(userCode.replace("-", "")))
                  .findFirst()
                  .orElseThrow(() -> IamError.error("not_found", 404));
          IamError.require(
              grant.path("status").asString().equals("pending")
                  && clock.instant().isBefore(Instant.parse(grant.path("expiresAt").asString())),
              "device_expired",
              409);
          ObjectNode session = sessions.activeById(sessionId);
          IamError.require(session != null, "unauthenticated", 401);
          grant.put("status", approved ? "approved" : "denied");
          grant.put("sessionId", sessionId);
          store.save("oauth_grant", grant);
          return null;
        });
  }

  private ObjectNode exchangeDevice(OAuthClient c, Map<String, String> input) {
    ObjectNode grant =
        store.get("oauth_grant", Secrets.hash(input.getOrDefault("device_code", "")), true);
    if (grant == null
        || !grant.path("clientId").asString().equals(c.id())
        || !grant.path("kind").asString().equals("device")
        || grant.path("used").asBoolean()) return error("invalid_grant");
    if (!clock.instant().isBefore(Instant.parse(grant.path("expiresAt").asString())))
      return error("expired_token");
    if (clock.instant().isBefore(Instant.parse(grant.path("nextPoll").asString()))) {
      grant.put("interval", grant.path("interval").asInt() + 5);
      grant.put("nextPoll", clock.instant().plusSeconds(grant.path("interval").asInt()).toString());
      store.save("oauth_grant", grant);
      return error("slow_down");
    }
    grant.put("nextPoll", clock.instant().plusSeconds(grant.path("interval").asInt()).toString());
    store.save("oauth_grant", grant);
    if (grant.path("status").asString().equals("pending")) return error("authorization_pending");
    if (grant.path("status").asString().equals("denied")) return error("access_denied");
    ObjectNode session = sessions.activeById(grant.path("sessionId").asString());
    if (session == null) return error("invalid_grant");
    String id = io.github.hansjone.auriga.common.persistence.UuidV7.next().toString();
    store.insert(
        "device",
        id,
        Json.object(
            "deviceId",
            id,
            "accountId",
            session.path("accountId").asString(),
            "name",
            "Device",
            "kind",
            "dsh_connect",
            "revoked",
            false,
            "createdAt",
            clock.instant().toString()),
        null);
    grant.put("deviceId", id);
    grant.put("used", true);
    store.save("oauth_grant", grant);
    return userTokens(c, grant, session, true);
  }

  public ObjectNode userinfo(String raw) {
    ObjectNode claims = tokens.verify(raw);
    IamError.require(
        claims.path("ptyp").asString().equals("user")
            && scopes(claims.path("scope").asString()).contains("openid"),
        "invalid_token",
        401);
    return store.tenant(
        UUID.fromString(claims.path("tid").asString()),
        () -> {
          ObjectNode session = sessions.activeById(claims.path("sid").asString());
          IamError.require(session != null, "invalid_token", 401);
          ObjectNode account = store.get("account", claims.path("sub").asString(), false);
          ObjectNode info = Json.object("sub", claims.path("sub").asString());
          if (scopes(claims.path("scope").asString()).contains("profile"))
            info.put("name", account.path("displayName").asString());
          if (scopes(claims.path("scope").asString()).contains("email") && account.has("email")) {
            info.put("email", account.path("email").asString());
            info.put("email_verified", true);
          }
          return info;
        });
  }

  static ObjectNode error(String error) {
    return Json.object("error", error);
  }
}
