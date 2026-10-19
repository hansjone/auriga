package io.github.hansjone.auriga.iam;

import com.yubico.webauthn.*;
import com.yubico.webauthn.data.*;
import com.yubico.webauthn.exception.*;
import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.events.EventPublisher;
import io.github.hansjone.auriga.common.persistence.UuidV7;
import java.time.*;
import java.util.*;
import java.util.stream.Collectors;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** WebAuthn passkey registration and assertion for team accounts (yubico webauthn-server-core). */
public final class TeamPasskeys {
  private final IamStore store;
  private final Secrets secrets;
  private final Clock clock;
  private final EventPublisher events;

  public TeamPasskeys(IamStore store, Secrets secrets, Clock clock, EventPublisher events) {
    this.store = store;
    this.secrets = secrets;
    this.clock = clock;
    this.events = events;
  }

  public List<ObjectNode> list(UUID accountId) {
    return store.all("team_passkey").stream()
        .filter(p -> p.path("accountId").asString().equals(accountId.toString()))
        .map(this::view)
        .toList();
  }

  public ObjectNode beginRegistration(UUID accountId, String displayName, JsonNode config) {
    RelyingParty rp = relyingParty(config, accountId);
    PublicKeyCredentialCreationOptions options =
        rp.startRegistration(
            StartRegistrationOptions.builder()
                .user(
                    UserIdentity.builder()
                        .name(accountId.toString())
                        .displayName(
                            displayName == null || displayName.isBlank() ? "user" : displayName)
                        .id(new ByteArray(uuidBytes(accountId)))
                        .build())
                .authenticatorSelection(
                    AuthenticatorSelectionCriteria.builder()
                        .residentKey(ResidentKeyRequirement.PREFERRED)
                        .userVerification(UserVerificationRequirement.PREFERRED)
                        .build())
                .build());
    String registrationId = UuidV7.next().toString();
    Instant expires = clock.instant().plusSeconds(300);
    String createJson;
    String persistJson;
    try {
      createJson = options.toCredentialsCreateJson();
      persistJson = options.toJson();
    } catch (Exception e) {
      throw new IllegalStateException(e);
    }
    store.insert(
        "team_ceremony",
        registrationId,
        Json.object(
            "kind",
            "passkey_registration",
            "accountId",
            accountId.toString(),
            "request",
            secrets.encrypt(persistJson, aad("reg", registrationId)),
            "expiresAt",
            expires.toString()),
        expires);
    ObjectNode wire = Json.read(createJson);
    return Json.object(
        "registrationId",
        registrationId,
        "publicKey",
        wire.has("publicKey") ? wire.path("publicKey") : wire);
  }

  public ObjectNode completeRegistration(
      UUID accountId, String registrationId, String name, JsonNode credential, JsonNode config) {
    ObjectNode ceremony = store.get("team_ceremony", registrationId, true);
    IamError.require(
        ceremony != null
            && ceremony.path("kind").asString().equals("passkey_registration")
            && ceremony.path("accountId").asString().equals(accountId.toString())
            && clock.instant().isBefore(Instant.parse(ceremony.path("expiresAt").asString())),
        "not_found",
        404);
    try {
      PublicKeyCredentialCreationOptions request =
          PublicKeyCredentialCreationOptions.fromJson(
              secrets.decrypt(ceremony.path("request").asString(), aad("reg", registrationId)));
      RegistrationResult result =
          relyingParty(config, accountId)
              .finishRegistration(
                  FinishRegistrationOptions.builder()
                      .request(request)
                      .response(
                          PublicKeyCredential.parseRegistrationResponseJson(credential.toString()))
                      .build());
      String passkeyId = UuidV7.next().toString();
      String label =
          name == null || name.isBlank()
              ? "Passkey"
              : name.length() > 64 ? name.substring(0, 64) : name;
      ObjectNode row =
          store.insert(
              "team_passkey",
              passkeyId,
              Json.object(
                  "passkeyId",
                  passkeyId,
                  "accountId",
                  accountId.toString(),
                  "name",
                  label,
                  "credentialId",
                  result.getKeyId().getId().getBase64Url(),
                  "publicKeyCose",
                  result.getPublicKeyCose().getBase64Url(),
                  "signCount",
                  result.getSignatureCount(),
                  "backedUp",
                  result.isBackedUp(),
                  "createdAt",
                  clock.instant().toString()),
              null);
      store.delete("team_ceremony", registrationId);
      events.publish(
          "identity.mfa_changed",
          accountId.toString(),
          Json.object(
              "accountId",
              accountId.toString(),
              "method",
              "passkey",
              "change",
              "added",
              "actorId",
              accountId.toString()));
      return view(row);
    } catch (RegistrationFailedException | java.io.IOException e) {
      throw IamError.error("invalid_credentials", 400);
    }
  }

  public ObjectNode beginAssertion(String providerId, String username, JsonNode config) {
    UUID accountId = null;
    if (username != null && !username.isBlank()) {
      ObjectNode identity =
          store.get("identity", providerId + ":" + username.toLowerCase(Locale.ROOT), false);
      if (identity != null) accountId = UUID.fromString(identity.path("accountId").asString());
    }
    AssertionRequest request =
        relyingParty(config, accountId)
            .startAssertion(
                StartAssertionOptions.builder()
                    .username(
                        accountId == null ? Optional.empty() : Optional.of(accountId.toString()))
                    .userVerification(UserVerificationRequirement.PREFERRED)
                    .build());
    String challengeId = UuidV7.next().toString();
    Instant expires = clock.instant().plusSeconds(300);
    String getJson;
    String persistJson;
    try {
      getJson = request.toCredentialsGetJson();
      persistJson = request.toJson();
    } catch (Exception e) {
      throw new IllegalStateException(e);
    }
    store.insert(
        "team_ceremony",
        challengeId,
        Json.object(
            "kind",
            "passkey_assertion",
            "providerId",
            providerId,
            "accountId",
            accountId == null ? "" : accountId.toString(),
            "request",
            secrets.encrypt(persistJson, aad("assert", challengeId)),
            "expiresAt",
            expires.toString()),
        expires);
    ObjectNode publicKey = Json.read(getJson);
    if (publicKey.has("publicKey")) publicKey = (ObjectNode) publicKey.path("publicKey");
    return Json.object(
        "step",
        Json.object("type", "webauthn", "ceremony", "get", "publicKey", publicKey),
        "state",
        Json.object("phase", "webauthn", "challengeId", challengeId));
  }

  public ObjectNode finishAssertion(String challengeId, JsonNode credential, JsonNode config) {
    ObjectNode ceremony = store.get("team_ceremony", challengeId, true);
    IamError.require(
        ceremony != null
            && ceremony.path("kind").asString().equals("passkey_assertion")
            && clock.instant().isBefore(Instant.parse(ceremony.path("expiresAt").asString())),
        "invalid_credentials",
        401);
    try {
      AssertionRequest request =
          AssertionRequest.fromJson(
              secrets.decrypt(ceremony.path("request").asString(), aad("assert", challengeId)));
      AssertionResult result =
          relyingParty(config, null)
              .finishAssertion(
                  FinishAssertionOptions.builder()
                      .request(request)
                      .response(
                          PublicKeyCredential.parseAssertionResponseJson(credential.toString()))
                      .build());
      IamError.require(result.isSuccess(), "invalid_credentials", 401);
      String accountId = result.getUsername();
      ObjectNode passkey =
          store.all("team_passkey").stream()
              .filter(
                  p ->
                      p.path("accountId").asString().equals(accountId)
                          && p.path("credentialId")
                              .asString()
                              .equals(result.getCredential().getCredentialId().getBase64Url()))
              .findFirst()
              .orElseThrow(() -> IamError.error("invalid_credentials", 401));
      passkey.put("signCount", result.getSignatureCount());
      passkey.put("lastUsedAt", clock.instant().toString());
      store.save("team_passkey", passkey);
      store.delete("team_ceremony", challengeId);
      ObjectNode identity =
          store.all("identity").stream()
              .filter(
                  i ->
                      i.path("accountId").asString().equals(accountId)
                          && i.path("providerId")
                              .asString()
                              .equals(ceremony.path("providerId").asString()))
              .findFirst()
              .orElseThrow(() -> IamError.error("invalid_credentials", 401));
      return Json.object(
          "subject",
          identity.path("subject").asString(),
          "displayName",
          identity.path("profile").path("displayName").asString(""),
          "mfaPerformed",
          true,
          "authTime",
          clock.instant().toString());
    } catch (AssertionFailedException | java.io.IOException e) {
      throw IamError.error("invalid_credentials", 401);
    }
  }

  public ObjectNode rename(UUID accountId, String passkeyId, String name) {
    ObjectNode row = requireOwned(accountId, passkeyId);
    IamError.require(name != null && !name.isBlank() && name.length() <= 64, "invalid_password", 400);
    row.put("name", name);
    store.save("team_passkey", row);
    return view(row);
  }

  public void delete(
      UUID accountId, String passkeyId, boolean requireRemainingMfa, boolean hasTotp) {
    requireOwned(accountId, passkeyId);
    long remaining =
        store.all("team_passkey").stream()
            .filter(p -> p.path("accountId").asString().equals(accountId.toString()))
            .count();
    if (requireRemainingMfa && remaining <= 1 && !hasTotp)
      throw IamError.error("mfa_required", 409);
    store.delete("team_passkey", passkeyId);
    events.publish(
        "identity.mfa_changed",
        accountId.toString(),
        Json.object(
            "accountId",
            accountId.toString(),
            "method",
            "passkey",
            "change",
            "removed",
            "actorId",
            accountId.toString()));
  }

  private ObjectNode requireOwned(UUID accountId, String passkeyId) {
    ObjectNode row = store.get("team_passkey", passkeyId, true);
    IamError.require(
        row != null && row.path("accountId").asString().equals(accountId.toString()),
        "not_found",
        404);
    return row;
  }

  private ObjectNode view(ObjectNode row) {
    ObjectNode v =
        Json.object(
            "id",
            row.path("passkeyId").asString(row.path("id").asString()),
            "name",
            row.path("name").asString(),
            "createdAt",
            row.path("createdAt").asString());
    if (row.has("lastUsedAt")) v.put("lastUsedAt", row.path("lastUsedAt").asString());
    if (row.has("backedUp")) v.put("backedUp", row.path("backedUp").asBoolean());
    return v;
  }

  private RelyingParty relyingParty(JsonNode config, UUID accountFilter) {
    String rpId = config.path("rpId").asString();
    Set<String> origins = new LinkedHashSet<>();
    for (JsonNode o : config.path("origins")) origins.add(o.asString());
    IamError.require(!rpId.isBlank() && !origins.isEmpty(), "invalid_config", 400);
    return RelyingParty.builder()
        .identity(RelyingPartyIdentity.builder().id(rpId).name("Auriga").build())
        .credentialRepository(new StoreCredentialRepository(accountFilter))
        .origins(origins)
        .build();
  }

  private String aad(String kind, String id) {
    return "webauthn:" + kind + ":" + TenantContext.require() + ":" + id;
  }

  private static byte[] uuidBytes(UUID id) {
    byte[] bytes = new byte[16];
    long msb = id.getMostSignificantBits(), lsb = id.getLeastSignificantBits();
    for (int i = 0; i < 8; i++) bytes[i] = (byte) (msb >>> (8 * (7 - i)));
    for (int i = 8; i < 16; i++) bytes[i] = (byte) (lsb >>> (8 * (15 - i)));
    return bytes;
  }

  private final class StoreCredentialRepository implements CredentialRepository {
    private final UUID accountFilter;

    StoreCredentialRepository(UUID accountFilter) {
      this.accountFilter = accountFilter;
    }

    @Override
    public Set<PublicKeyCredentialDescriptor> getCredentialIdsForUsername(String username) {
      return credentialsFor(username).stream()
          .map(
              p ->
                  PublicKeyCredentialDescriptor.builder()
                      .id(b64(p.path("credentialId").asString()))
                      .type(PublicKeyCredentialType.PUBLIC_KEY)
                      .build())
          .collect(Collectors.toSet());
    }

    @Override
    public Optional<ByteArray> getUserHandleForUsername(String username) {
      try {
        return Optional.of(new ByteArray(uuidBytes(UUID.fromString(username))));
      } catch (Exception e) {
        return Optional.empty();
      }
    }

    @Override
    public Optional<String> getUsernameForUserHandle(ByteArray userHandle) {
      if (userHandle.getBytes().length != 16) return Optional.empty();
      byte[] b = userHandle.getBytes();
      long msb = 0, lsb = 0;
      for (int i = 0; i < 8; i++) msb = (msb << 8) | (b[i] & 0xff);
      for (int i = 8; i < 16; i++) lsb = (lsb << 8) | (b[i] & 0xff);
      return Optional.of(new UUID(msb, lsb).toString());
    }

    @Override
    public Optional<RegisteredCredential> lookup(ByteArray credentialId, ByteArray userHandle) {
      String id = credentialId.getBase64Url();
      return store.all("team_passkey").stream()
          .filter(p -> p.path("credentialId").asString().equals(id))
          .filter(
              p ->
                  accountFilter == null
                      || p.path("accountId").asString().equals(accountFilter.toString()))
          .findFirst()
          .map(
              p ->
                  RegisteredCredential.builder()
                      .credentialId(credentialId)
                      .userHandle(userHandle)
                      .publicKeyCose(b64(p.path("publicKeyCose").asString()))
                      .signatureCount(p.path("signCount").asLong())
                      .build());
    }

    @Override
    public Set<RegisteredCredential> lookupAll(ByteArray credentialId) {
      String id = credentialId.getBase64Url();
      return store.all("team_passkey").stream()
          .filter(p -> p.path("credentialId").asString().equals(id))
          .map(
              p ->
                  RegisteredCredential.builder()
                      .credentialId(credentialId)
                      .userHandle(
                          new ByteArray(uuidBytes(UUID.fromString(p.path("accountId").asString()))))
                      .publicKeyCose(b64(p.path("publicKeyCose").asString()))
                      .signatureCount(p.path("signCount").asLong())
                      .build())
          .collect(Collectors.toSet());
    }

    private List<ObjectNode> credentialsFor(String username) {
      String accountId = accountFilter != null ? accountFilter.toString() : username;
      return store.all("team_passkey").stream()
          .filter(p -> p.path("accountId").asString().equals(accountId))
          .toList();
    }
  }

  private static ByteArray b64(String value) {
    try {
      return ByteArray.fromBase64Url(value);
    } catch (com.yubico.webauthn.data.exception.Base64UrlException e) {
      throw new IllegalStateException(e);
    }
  }
}
