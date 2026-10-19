package auriga.iam;

import auriga.iam.api.IdentityConnector;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.*;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jwt.JWTClaimsSet;
import com.nimbusds.jwt.SignedJWT;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.time.*;
import java.util.*;
import java.util.stream.Collectors;
import java.util.stream.StreamSupport;
import org.slf4j.LoggerFactory;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Built-in generic OIDC / OAuth 2.0 connector ({@code type=oidc}). Login uses authorization code +
 * PKCE; callback URL comes from {@code ctx.callbackUrl}.
 */
public final class OidcConnector implements IdentityConnector {
  private static final List<String> DEFAULT_SCOPES = List.of("openid", "profile", "email");

  private final OidcHttp http;
  private final Clock clock;
  private final OidcMetadata metadata;

  public OidcConnector() {
    this(false, Clock.systemUTC());
  }

  OidcConnector(boolean allowLocalHttp, Clock clock) {
    this.http = new OidcHttp(allowLocalHttp);
    this.clock = clock;
    this.metadata = new OidcMetadata(http, clock);
  }

  @Override
  public JsonNode descriptor() {
    return Json.object(
        "type",
        "oidc",
        "spiVersion",
        "1",
        "displayName",
        Json.object("en", "OIDC / OAuth 2.0", "zh-CN", "OIDC / OAuth 2.0"),
        "description",
        Json.object(
            "en",
            "Connect any standards-based OIDC or OAuth 2.0 identity provider.",
            "zh-CN",
            "对接任意标准 OIDC / OAuth 2.0 身份提供方。"),
        "steps",
        List.of("redirect"),
        "configSchema",
        configSchema(),
        "multipleInstances",
        true);
  }

  static JsonNode configSchema() {
    return Json.object(
        "type",
        "object",
        "additionalProperties",
        false,
        "required",
        List.of("issuer", "clientId", "clientSecret"),
        "properties",
        Json.object(
            "issuer",
            Json.object(
                "type",
                "string",
                "format",
                "uri",
                "minLength",
                1,
                "description",
                "OIDC issuer URL (discovery at {issuer}/.well-known/openid-configuration)."),
            "clientId",
            Json.object("type", "string", "minLength", 1),
            "clientSecret",
            Json.object("type", "string", "minLength", 1, "x-secret", true),
            "scopes",
            Json.object(
                "type",
                "array",
                "minItems",
                1,
                "items",
                Json.object("type", "string", "minLength", 1),
                "default",
                DEFAULT_SCOPES,
                "description",
                "OAuth scopes. Include openid for OIDC id_token."),
            "claimMapping",
            Json.object(
                "type",
                "object",
                "additionalProperties",
                false,
                "description",
                "Map ExternalProfile fields to IdP claim names. Defaults: sub/name/email/email_verified.",
                "properties",
                Json.object(
                    "subject",
                    Json.object("type", "string", "minLength", 1, "default", "sub"),
                    "displayName",
                    Json.object("type", "string", "minLength", 1, "default", "name"),
                    "email",
                    Json.object("type", "string", "minLength", 1, "default", "email"),
                    "emailVerified",
                    Json.object("type", "string", "minLength", 1, "default", "email_verified"),
                    "phone",
                    Json.object("type", "string", "minLength", 1, "default", "phone_number")))));
  }

  @Override
  public void validateConfig(JsonNode config) {
    OidcMetadata.requireIssuerUri(config.path("issuer").asString(""));
    IamError.require(!config.path("clientId").asString("").isBlank(), "invalid_config", 400);
    IamError.require(!config.path("clientSecret").asString("").isBlank(), "invalid_config", 400);
    metadata.get(config.path("issuer").asString());
  }

  @Override
  public JsonNode begin(JsonNode context, JsonNode config) {
    try {
      OidcMetadata.Document doc = metadata.get(config.path("issuer").asString());
      String oauthState = Secrets.random();
      String nonce = Secrets.random();
      String verifier = Secrets.random();
      String challenge = OAuthProvider.challenge(verifier);
      String scopes = scopes(config);
      String url =
          doc.authorizationEndpoint()
              + (doc.authorizationEndpoint().getRawQuery() == null ? "?" : "&")
              + "response_type=code"
              + "&client_id="
              + enc(config.path("clientId").asString())
              + "&redirect_uri="
              + enc(context.path("callbackUrl").asString())
              + "&scope="
              + enc(scopes)
              + "&state="
              + enc(oauthState)
              + "&nonce="
              + enc(nonce)
              + "&code_challenge="
              + enc(challenge)
              + "&code_challenge_method=S256";
      return Json.object(
          "step",
          Json.object("type", "redirect", "url", url, "method", "GET"),
          "state",
          Json.object(
              "oauthState",
              oauthState,
              "nonce",
              nonce,
              "codeVerifier",
              verifier,
              "scopes",
              scopes));
    } catch (RuntimeException e) {
      return failUnavailable(e);
    }
  }

  @Override
  public JsonNode advance(JsonNode context, JsonNode config, JsonNode input) {
    if (!"callback".equals(input.path("type").asString())) return fail("iam.external_denied");
    JsonNode params = input.path("params");
    JsonNode state = context.path("state");
    if (!state.isObject()) return fail("iam.external_denied");
    String expected = state.path("oauthState").asString("");
    String actual = params.path("state").asString("");
    if (expected.isBlank() || !Secrets.same(expected, actual)) return fail("iam.external_denied");
    if (params.has("error")) return fail("iam.external_denied");
    String code = params.path("code").asString("");
    if (code.isBlank()) return fail("iam.external_denied");
    try {
      OidcMetadata.Document doc = metadata.get(config.path("issuer").asString());
      JsonNode tokens =
          exchange(
              doc,
              config,
              code,
              context.path("callbackUrl").asString(),
              state.path("codeVerifier").asString(""));
      ObjectNode claims = collectClaims(doc, tokens, state.path("nonce").asString(""), config);
      JsonNode profile = toProfile(claims, config.path("claimMapping"));
      if (profile.path("subject").asString("").isBlank()) return fail("iam.external_denied");
      return Json.object("step", Json.object("type", "done", "profile", profile));
    } catch (AurigaFail e) {
      return fail(e.code);
    } catch (RuntimeException e) {
      return failUnavailable(e);
    }
  }

  private JsonNode exchange(
      OidcMetadata.Document doc, JsonNode config, String code, String redirect, String verifier) {
    if (verifier.isBlank() || !verifier.matches("[A-Za-z0-9._~-]{43,128}"))
      throw new AurigaFail("iam.external_denied");
    String body =
        "grant_type=authorization_code"
            + "&code="
            + enc(code)
            + "&redirect_uri="
            + enc(redirect)
            + "&client_id="
            + enc(config.path("clientId").asString())
            + "&client_secret="
            + enc(config.path("clientSecret").asString())
            + "&code_verifier="
            + enc(verifier);
    long started = System.nanoTime();
    String outcome = "failure";
    try {
      OidcHttp.Response response = http.send(doc.tokenEndpoint(), "POST", body, Map.of());
      if (response.statusCode() >= 400 && response.statusCode() < 500)
        throw new AurigaFail("iam.external_denied");
      IamError.require(response.statusCode() == 200, "connector_unavailable", 502);
      JsonNode tokens = Json.MAPPER.readTree(response.body());
      if (tokens.has("error")) throw new AurigaFail("iam.external_denied");
      outcome = "success";
      return tokens;
    } catch (java.io.IOException e) {
      throw IamError.error("connector_unavailable", 502);
    } finally {
      LoggerFactory.getLogger(OidcConnector.class)
          .info(
              "OIDC token exchange host={} outcome={} elapsedMs={}",
              doc.tokenEndpoint().getHost(),
              outcome,
              (System.nanoTime() - started) / 1_000_000);
    }
  }

  private ObjectNode collectClaims(
      OidcMetadata.Document doc, JsonNode tokens, String nonce, JsonNode config) {
    ObjectNode claims = Json.object();
    String idToken = tokens.path("id_token").asString("");
    if (!idToken.isBlank()) {
      try {
        SignedJWT jwt = SignedJWT.parse(idToken);
        verifySignature(doc, jwt);
        JWTClaimsSet set = jwt.getJWTClaimsSet();
        if (set.getSubject() == null || set.getSubject().isBlank())
          throw new AurigaFail("iam.external_denied");
        if (!OidcMetadata.normalizeIssuer(config.path("issuer").asString())
            .equals(OidcMetadata.normalizeIssuer(Objects.toString(set.getIssuer(), ""))))
          throw new AurigaFail("iam.external_denied");
        List<String> audience = set.getAudience();
        if (audience == null
            || audience.stream().noneMatch(a -> config.path("clientId").asString().equals(a)))
          throw new AurigaFail("iam.external_denied");
        String client = config.path("clientId").asString();
        if ((audience.size() > 1 || set.getClaim("azp") != null)
            && !client.equals(set.getStringClaim("azp")))
          throw new AurigaFail("iam.external_denied");
        if (set.getIssueTime() == null
            || set.getIssueTime().toInstant().isAfter(clock.instant().plusSeconds(30))
            || set.getNotBeforeTime() != null
                && set.getNotBeforeTime().toInstant().isAfter(clock.instant().plusSeconds(30)))
          throw new AurigaFail("iam.external_denied");
        String tokenNonce = set.getStringClaim("nonce");
        if (!nonce.isBlank() && !Secrets.same(nonce, tokenNonce == null ? "" : tokenNonce))
          throw new AurigaFail("iam.external_denied");
        Date exp = set.getExpirationTime();
        if (exp == null || exp.toInstant().isBefore(clock.instant().minusSeconds(30)))
          throw new AurigaFail("iam.external_denied");
        set.toJSONObject().forEach((k, v) -> claims.set(k, Json.MAPPER.valueToTree(v)));
      } catch (java.text.ParseException e) {
        throw new AurigaFail("iam.external_denied");
      }
    }
    if (idToken.isBlank() && Arrays.asList(scopes(config).split(" ")).contains("openid"))
      throw new AurigaFail("iam.external_denied");
    String access = tokens.path("access_token").asString("");
    if (doc.userinfoEndpoint() != null && !access.isBlank()) {
      JsonNode info = userinfo(doc, access);
      if (!idToken.isBlank() && !claims.path("sub").equals(info.path("sub")))
        throw new AurigaFail("iam.external_denied");
      // Signed claims remain authoritative, including custom identity/email mappings.
      info.properties()
          .forEach(
              e -> {
                if (!claims.has(e.getKey())
                    && !Set.of(
                            "iss",
                            "aud",
                            "nonce",
                            "azp",
                            "exp",
                            "iat",
                            "nbf",
                            "auth_time",
                            "amr",
                            "acr")
                        .contains(e.getKey())) claims.set(e.getKey(), e.getValue());
              });
    }
    if (claims.isEmpty()) throw new AurigaFail("iam.external_denied");
    return claims;
  }

  private void verifySignature(OidcMetadata.Document doc, SignedJWT jwt) {
    if (doc.jwksUri() == null
        || !Set.of(JWSAlgorithm.RS256, JWSAlgorithm.ES256).contains(jwt.getHeader().getAlgorithm())
        || jwt.getHeader().getCriticalParams() != null
            && !jwt.getHeader().getCriticalParams().isEmpty())
      throw new AurigaFail("iam.external_denied");
    try {
      OidcHttp.Response response = http.send(doc.jwksUri(), "GET", null, Map.of());
      IamError.require(response.statusCode() == 200, "connector_unavailable", 502);
      for (JWK key : JWKSet.parse(response.body()).getKeys()) {
        if (key.isPrivate()
            || key.getKeyUse() != null && !KeyUse.SIGNATURE.equals(key.getKeyUse())
            || key.getKeyOperations() != null
                && !key.getKeyOperations().contains(KeyOperation.VERIFY)
            || key.getAlgorithm() != null
                && !key.getAlgorithm().equals(jwt.getHeader().getAlgorithm())
            || jwt.getHeader().getKeyID() != null
                && !jwt.getHeader().getKeyID().equals(key.getKeyID())) continue;
        if (key instanceof RSAKey rsa
            && JWSAlgorithm.RS256.equals(jwt.getHeader().getAlgorithm())
            && rsa.size() >= 2048
            && jwt.verify(new RSASSAVerifier(rsa))) return;
        if (key instanceof ECKey ec
            && Curve.P_256.equals(ec.getCurve())
            && JWSAlgorithm.ES256.equals(jwt.getHeader().getAlgorithm())
            && jwt.verify(new ECDSAVerifier(ec))) return;
      }
      throw new AurigaFail("iam.external_denied");
    } catch (java.text.ParseException | JOSEException e) {
      throw new AurigaFail("iam.external_denied");
    } catch (java.io.IOException e) {
      throw IamError.error("connector_unavailable", 502);
    }
  }

  private JsonNode userinfo(OidcMetadata.Document doc, String accessToken) {
    long started = System.nanoTime();
    String outcome = "failure";
    try {
      OidcHttp.Response response =
          http.send(
              doc.userinfoEndpoint(),
              "GET",
              null,
              Map.of("Authorization", "Bearer " + accessToken));
      if (response.statusCode() >= 400 && response.statusCode() < 500)
        throw new AurigaFail("iam.external_denied");
      IamError.require(response.statusCode() == 200, "connector_unavailable", 502);
      outcome = "success";
      return Json.MAPPER.readTree(response.body());
    } catch (java.io.IOException e) {
      throw IamError.error("connector_unavailable", 502);
    } finally {
      LoggerFactory.getLogger(OidcConnector.class)
          .info(
              "OIDC userinfo host={} outcome={} elapsedMs={}",
              doc.userinfoEndpoint().getHost(),
              outcome,
              (System.nanoTime() - started) / 1_000_000);
    }
  }

  private static JsonNode toProfile(JsonNode claims, JsonNode mapping) {
    String subjectClaim = claimName(mapping, "subject", "sub");
    String displayClaim = claimName(mapping, "displayName", "name");
    String emailClaim = claimName(mapping, "email", "email");
    String verifiedClaim = claimName(mapping, "emailVerified", "email_verified");
    String phoneClaim = claimName(mapping, "phone", "phone_number");
    ObjectNode profile = Json.object("subject", text(claims, subjectClaim));
    String display = text(claims, displayClaim);
    if (display.isBlank()) display = text(claims, "preferred_username");
    if (!display.isBlank()) profile.put("displayName", display);
    String email = text(claims, emailClaim);
    if (!email.isBlank()) {
      profile.put("email", email);
      if (claims.path(verifiedClaim).isBoolean())
        profile.put("emailVerified", claims.path(verifiedClaim).asBoolean());
      else if (claims.path(verifiedClaim).isString())
        profile.put("emailVerified", Boolean.parseBoolean(claims.path(verifiedClaim).asString()));
    }
    String phone = text(claims, phoneClaim);
    if (!phone.isBlank()) profile.put("phone", phone);
    if (claims.path("auth_time").isNumber())
      profile.put("authTime", Instant.ofEpochSecond(claims.path("auth_time").asLong()).toString());
    profile.set("attributes", claims.deepCopy());
    return profile;
  }

  private static String claimName(JsonNode mapping, String field, String fallback) {
    String custom = mapping.path(field).asString("");
    return custom.isBlank() ? fallback : custom;
  }

  private static String text(JsonNode claims, String name) {
    JsonNode n = claims.path(name);
    if (n.isMissingNode() || n.isNull()) return "";
    if (n.isArray() && n.size() > 0) return n.get(0).asString("");
    return n.asString("");
  }

  private static String scopes(JsonNode config) {
    JsonNode scopes = config.path("scopes");
    if (!scopes.isArray() || scopes.isEmpty()) return String.join(" ", DEFAULT_SCOPES);
    return StreamSupport.stream(scopes.spliterator(), false)
        .map(JsonNode::asString)
        .filter(s -> !s.isBlank())
        .collect(Collectors.joining(" "));
  }

  private static String enc(String value) {
    return URLEncoder.encode(value, StandardCharsets.UTF_8);
  }

  private static JsonNode fail(String code) {
    return Json.object("step", Json.object("type", "fail", "code", code));
  }

  private static JsonNode failUnavailable(RuntimeException e) {
    if (e instanceof auriga.common.error.AurigaException ae
        && ae.errorCode() != null
        && "iam.external_denied".equals(ae.errorCode().code())) return fail("iam.external_denied");
    return fail("iam.connector_unavailable");
  }

  /** Local fail signal that does not leak IdP details. */
  private static final class AurigaFail extends RuntimeException {
    final String code;

    AurigaFail(String code) {
      this.code = code;
    }
  }
}
