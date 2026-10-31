package auriga.iam;

import auriga.iam.api.*;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.*;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jwt.*;
import java.time.*;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

public final class TokenService implements InternalTokenIssuer {
  private final SigningKeys keys;
  private final ContractSchemas schemas;
  private final PermissionResolver permissions;
  private final Clock clock;
  private final String issuer;
  private final Set<String> acceptedIssuers;
  private final Map<String, String> internalSecrets;
  private final ScopedValue<ECKey> requestSigningKey = ScopedValue.newInstance();

  /** Snapshot the current authoritative key before a login transaction, for this request only. */
  <T> T withSigningKey(java.util.function.Supplier<T> work) {
    ECKey key = keys.current();
    return ScopedValue.where(requestSigningKey, key).call(work::get);
  }

  public TokenService(
      SigningKeys keys,
      ContractSchemas schemas,
      PermissionResolver permissions,
      Clock clock,
      String issuer,
      Map<String, String> internalSecretHashes) {
    this(keys, schemas, permissions, clock, issuer, internalSecretHashes, List.of());
  }

  public TokenService(
      SigningKeys keys,
      ContractSchemas schemas,
      PermissionResolver permissions,
      Clock clock,
      String issuer,
      Map<String, String> internalSecretHashes,
      List<String> allowedOrigins) {
    this.keys = keys;
    this.schemas = schemas;
    this.permissions = permissions;
    this.clock = clock;
    this.issuer = issuer.replaceAll("/$", "");
    this.acceptedIssuers = auriga.common.web.LinkOrigins.issuerOrigins(this.issuer, allowedOrigins);
    this.internalSecrets = Map.copyOf(internalSecretHashes);
    var unique = new HashSet<String>();
    for (String digest : internalSecrets.values())
      if (!unique.add(digest.toLowerCase(Locale.ROOT)))
        throw new IllegalStateException("Internal services must use distinct installation secrets");
  }

  public String issuer() {
    return issuer;
  }

  /**
   * Only a trusted request origin also approved by this issuer configuration can select an alias.
   */
  public String requestIssuer() {
    String selected = auriga.common.web.LinkOrigins.currentOr(issuer);
    return acceptedIssuers.contains(selected) ? selected : issuer;
  }

  public String access(
      UUID tenant,
      String subject,
      String audience,
      String principalType,
      String context,
      UUID session,
      UUID device,
      Instant authTime,
      Instant stepUp,
      String scope) {
    return access(
        tenant,
        subject,
        audience,
        principalType,
        context,
        session,
        device,
        authTime,
        stepUp,
        scope,
        "member");
  }

  public String access(
      UUID tenant,
      String subject,
      String audience,
      String principalType,
      String context,
      UUID session,
      UUID device,
      Instant authTime,
      Instant stepUp,
      String scope,
      String memberType) {
    IamError.require(
        !"internal_service".equals(principalType) && !"auriga.internal".equals(audience),
        "invalid_audience",
        400);
    // Instance tokens can only be issued after the dedicated internal workspace checks.
    IamError.validate(
        tenant != null
            && subject != null
            && !subject.isBlank()
            && audience != null
            && !audience.isBlank()
            && switch (context == null ? "" : context) {
              case "portal", "admin" ->
                  "user".equals(principalType)
                      && session != null
                      && device == null
                      && !"auriga.ai-gateway".equals(audience);
              case "device" -> "device".equals(principalType) && device != null && session == null;
              case "service" ->
                  "service_account".equals(principalType)
                      && session == null
                      && device == null
                      && !"auriga.ai-gateway".equals(audience);
              default -> false;
            });
    var perms =
        permissions.resolve(tenant, subject, context).stream()
            .filter(p -> allowedPermission(p, context))
            .sorted()
            .toList();
    ObjectNode claims = base(tenant, subject, audience, principalType, context);
    claims.set("perms", Json.MAPPER.valueToTree(perms));
    claims.put("scope", scope);
    if (session != null) claims.put("sid", session.toString());
    if (device != null) claims.put("did", device.toString());
    if (authTime != null) claims.put("auth_time", authTime.getEpochSecond());
    if (stepUp != null) claims.put("stepup_time", stepUp.getEpochSecond());
    if ("user".equals(principalType) || "device".equals(principalType))
      claims.put("mtyp", memberType);
    return signAccess(claims);
  }

  static boolean allowedPermission(String permission, String context) {
    if ("instance".equals(context))
      return Set.of("ai.models.invoke", "ai.usage.read").contains(permission);
    if ("device".equals(context))
      return permission.equals("auriga.connect")
          || permission.startsWith("auriga.connect.")
              && !permission.matches(".*\\.(write|admin|grant)(\\..*)?");
    if ("portal".equals(context))
      return !(M3PermissionCatalog.adminOnly(permission)
          || permission.startsWith("iam.")
          || permission.startsWith("org.")
          || permission.startsWith("ops.")
          || permission.matches(".*\\.(admin|grant)(\\..*)?"));
    return "admin".equals(context) || "service".equals(context);
  }

  record IssuedToken(String accessToken, Instant expiresAt) {}

  IssuedToken managedDevice(
      UUID tenant, String owner, UUID device, Instant expiresAt, String memberType) {
    ObjectNode claims = base(tenant, owner, "auriga", "device", "device");
    claims.put("did", device.toString());
    claims.put("scope", "auriga.connect");
    claims.put("mtyp", memberType);
    claims.set("perms", Json.MAPPER.valueToTree(List.of("auriga.connect.collab.use")));
    Instant expiry = clock.instant().plusSeconds(300);
    if (expiresAt.isBefore(expiry)) expiry = expiresAt;
    claims.put("exp", expiry.getEpochSecond());
    return new IssuedToken(signAccess(claims), expiry);
  }

  IssuedToken deviceAi(
      DeviceAccessAuthority.Identity identity,
      Instant authTime,
      String memberType,
      Set<String> effective) {
    if (!effective.contains(DeviceAiResource.INVOKE)) return null;
    long expiry =
        Math.min(
            clock.instant().plusSeconds(300).getEpochSecond(),
            identity.expiresAt().getEpochSecond());
    if (expiry <= clock.instant().getEpochSecond()) return null;
    ObjectNode claims =
        base(
            UUID.fromString(identity.tenant()),
            identity.account(),
            "auriga.ai-gateway",
            "device",
            "device");
    claims.put("exp", expiry);
    claims.put("did", identity.device());
    claims.put("scope", "auriga.connect");
    claims.put("mtyp", memberType);
    claims.put("auth_time", authTime.getEpochSecond());
    claims.set("perms", Json.MAPPER.valueToTree(List.of(DeviceAiResource.INVOKE)));
    return new IssuedToken(signAccess(claims), Instant.ofEpochSecond(expiry));
  }

  IssuedToken instanceAi(
      UUID tenant, UUID owner, UUID instance, long version, Set<String> effective) {
    IamError.validate(
        tenant != null
            && owner != null
            && instance != null
            && version >= 1
            && version <= 9007199254740991L);
    List<String> perms =
        effective.stream().filter(p -> allowedPermission(p, "instance")).sorted().toList();
    if (perms.isEmpty())
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
    ObjectNode claims = base(tenant, owner.toString(), "auriga.ai-gateway", "user", "instance");
    claims.put("instanceId", instance.toString());
    claims.put("credentialVersion", version);
    claims.set("perms", Json.MAPPER.valueToTree(perms));
    return new IssuedToken(signAccess(claims), Instant.ofEpochSecond(claims.path("exp").asLong()));
  }

  private ObjectNode base(UUID tenant, String sub, String aud, String type, String context) {
    long now = clock.instant().getEpochSecond();
    return Json.object(
        "iss",
        requestIssuer(),
        "sub",
        sub,
        "aud",
        aud,
        "iat",
        now,
        "nbf",
        now,
        "exp",
        now + 300,
        "jti",
        Secrets.random(),
        "tid",
        tenant.toString(),
        "ptyp",
        type,
        "ctx",
        context);
  }

  private String signAccess(ObjectNode claims) {
    schemas.validate("auth/access-token.schema.json", claims);
    return sign(claims, "at+jwt");
  }

  private String sign(ObjectNode claims, String typ) {
    try {
      ECKey key = requestSigningKey.isBound() ? requestSigningKey.get() : keys.current();
      SignedJWT jwt =
          new SignedJWT(
              new JWSHeader.Builder(JWSAlgorithm.ES256)
                  .type(new JOSEObjectType(typ))
                  .keyID(key.getKeyID())
                  .build(),
              JWTClaimsSet.parse(claims.toString()));
      jwt.sign(new ECDSASigner(key));
      return jwt.serialize();
    } catch (JOSEException | java.text.ParseException e) {
      throw new IllegalStateException(e);
    }
  }

  public String idToken(JsonNode session, String clientId, String nonce) {
    ObjectNode c =
        Json.object(
            "iss",
            requestIssuer(),
            "sub",
            session.path("accountId").asString(),
            "aud",
            clientId,
            "iat",
            clock.instant().getEpochSecond(),
            "exp",
            clock.instant().plusSeconds(300).getEpochSecond(),
            "auth_time",
            Instant.parse(session.path("authTime").asString()).getEpochSecond());
    if (!nonce.isEmpty()) c.put("nonce", nonce);
    return sign(c, "JWT");
  }

  public ObjectNode verify(String token) {
    try {
      SignedJWT jwt = SignedJWT.parse(token);
      IamError.require(
          JWSAlgorithm.ES256.equals(jwt.getHeader().getAlgorithm())
              && new JOSEObjectType("at+jwt").equals(jwt.getHeader().getType()),
          "invalid_token",
          401);
      ECKey key = null;
      for (JsonNode k : keys.jwks().path("keys"))
        if (k.path("kid").asString().equals(jwt.getHeader().getKeyID()))
          key = ECKey.parse(k.toString());
      IamError.require(key != null && jwt.verify(new ECDSAVerifier(key)), "invalid_token", 401);
      ObjectNode c = Json.read(jwt.getJWTClaimsSet().toString());
      schemas.validate("auth/access-token.schema.json", c);
      IamError.require(
          acceptedIssuers.contains(c.path("iss").asString())
              && c.path("exp").asLong() > clock.instant().getEpochSecond()
              && c.path("nbf").asLong() <= clock.instant().getEpochSecond() + 30
              && !c.path("ptyp").asString().equals("internal_service"),
          "invalid_token",
          401);
      return c;
    } catch (JOSEException | java.text.ParseException e) {
      throw IamError.error("invalid_token", 401);
    }
  }

  @Override
  public String issue(String service, String installationSecret) {
    IamError.require(
        service.matches("[a-z][a-z0-9-]{1,62}")
            && Secrets.same(internalSecrets.get(service), Secrets.hash(installationSecret)),
        "invalid_service_credentials",
        401);
    return signAccess(
        base(
            SigningKeys.PLATFORM,
            "svc:" + service,
            "auriga.internal",
            "internal_service",
            "service"));
  }
}
