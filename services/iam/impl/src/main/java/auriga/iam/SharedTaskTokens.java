package auriga.iam;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.DshInstanceMode;
import auriga.contracts.node.v1.SharedDshCapability;
import auriga.contracts.node.v1.SharedDshSessionBinding;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Exact K JSON mapping and purpose-specific Ed25519 signing, independent of platform JWTs. */
final class SharedTaskTokens {
  static final String ENTERPRISE = "auriga.shared.enterprise";
  static final String MODEL = "auriga.shared.model";
  static final String MESSAGE = "auriga.shared.message";
  static final long MAX_SAFE_INTEGER = 9007199254740991L;
  private static final Set<String> AUDIENCES = Set.of(ENTERPRISE, MODEL, MESSAGE);

  record Key(String kid, PrivateKey privateKey) {
    Key {
      if (kid == null
          || !kid.matches("[A-Za-z0-9._-]{1,128}")
          || privateKey == null
          || !Set.of("EdDSA", "Ed25519").contains(privateKey.getAlgorithm()))
        throw new IllegalArgumentException("Invalid shared capability signing key");
    }

    @Override
    public String toString() {
      return "Key[redacted]";
    }
  }

  private final Map<String, Key> keys;

  SharedTaskTokens(Map<String, Key> keys) {
    if (!keys.keySet().equals(AUDIENCES))
      throw new IllegalArgumentException("All shared capability signing purposes are required");
    var distinct = new HashSet<String>();
    for (var key : keys.values())
      if (!distinct.add(Base64.getEncoder().encodeToString(key.privateKey().getEncoded())))
        throw new IllegalArgumentException(
            "Shared capability signing purposes require separate keys");
    this.keys = Map.copyOf(keys);
  }

  String sign(SharedDshCapability capability) {
    validate(capability);
    var key = keys.get(capability.getAudience());
    var header = new TreeMap<String, Object>();
    header.put("alg", "EdDSA");
    header.put("kid", key.kid());
    header.put("typ", "auriga-shared-capability+jwt");
    var encoder = Base64.getUrlEncoder().withoutPadding();
    String input =
        encoder.encodeToString(Json.MAPPER.writeValueAsBytes(header))
            + "."
            + encoder.encodeToString(Json.MAPPER.writeValueAsBytes(claims(capability)));
    try {
      var signer = Signature.getInstance("Ed25519");
      signer.initSign(key.privateKey());
      signer.update(input.getBytes(StandardCharsets.US_ASCII));
      return input + "." + encoder.encodeToString(signer.sign());
    } catch (GeneralSecurityException error) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  static void validate(SharedDshSessionBinding binding) {
    valid(binding != null && binding.getUnknownFields().asMap().isEmpty());
    uuid(binding.getTenantId());
    uuid(binding.getMemberId());
    uuid(binding.getPlacementId());
    uuid(binding.getPhysicalInstanceId());
    uuid(binding.getSessionId());
    uuid(binding.getPlatformSessionId());
    valid(
        binding.getMode() == DshInstanceMode.DSH_INSTANCE_MODE_SHARED
            || binding.getMode() == DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED);
    positive(binding.getEpoch());
    positive(binding.getRevision());
    positive(binding.getAuthGeneration());
  }

  static void validate(SharedDshCapability capability) {
    valid(capability != null && capability.getUnknownFields().asMap().isEmpty());
    valid(capability.getSchemaVersion() == 1 && capability.hasBinding());
    validate(capability.getBinding());
    uuid(capability.getTaskId());
    uuid(capability.getJti());
    scopes(capability.getAudience(), capability.getScopesList());
    positive(capability.getCredentialVersion());
    valid(
        capability.getIssuedAt() >= 0
            && capability.getExpiresAt() <= MAX_SAFE_INTEGER
            && capability.getExpiresAt() > capability.getIssuedAt()
            && capability.getExpiresAt() - capability.getIssuedAt() <= 600);
  }

  static List<String> scopes(String audience, List<String> requested) {
    Set<String> allowed =
        switch (audience) {
          case ENTERPRISE -> Set.of("outbound", "credentials");
          case MODEL -> Set.of("model.invoke");
          case MESSAGE -> Set.of("message.send", "message.receive");
          default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
        };
    valid(
        !requested.isEmpty()
            && requested.size() == new HashSet<>(requested).size()
            && allowed.containsAll(requested));
    return requested.stream().sorted().toList();
  }

  static SortedMap<String, Object> binding(SharedDshSessionBinding value) {
    var result = new TreeMap<String, Object>();
    result.put("tenantId", value.getTenantId());
    result.put("memberId", value.getMemberId());
    result.put("placementId", value.getPlacementId());
    result.put("physicalInstanceId", value.getPhysicalInstanceId());
    result.put(
        "mode",
        value.getMode() == DshInstanceMode.DSH_INSTANCE_MODE_SHARED ? "shared" : "dedicated");
    result.put("epoch", value.getEpoch());
    result.put("revision", value.getRevision());
    result.put("sessionId", value.getSessionId());
    result.put("platformSessionId", value.getPlatformSessionId());
    result.put("authGeneration", value.getAuthGeneration());
    return result;
  }

  static SortedMap<String, Object> claims(SharedDshCapability value) {
    var result = new TreeMap<String, Object>();
    result.put("schemaVersion", value.getSchemaVersion());
    result.put("binding", binding(value.getBinding()));
    result.put("taskId", value.getTaskId());
    result.put("audience", value.getAudience());
    result.put("scopes", value.getScopesList());
    result.put("credentialVersion", value.getCredentialVersion());
    result.put("issuedAt", value.getIssuedAt());
    result.put("expiresAt", value.getExpiresAt());
    result.put("jti", value.getJti());
    return result;
  }

  static ObjectNode bindingJson(SharedDshSessionBinding binding) {
    return persistedJson(binding(binding));
  }

  static ObjectNode claimsJson(SharedDshCapability capability) {
    return persistedJson(claims(capability));
  }

  private static ObjectNode persistedJson(SortedMap<String, Object> value) {
    // valueToTree keeps protobuf long values as LongNode; readTree of jsonb
    // text chooses IntNode for small integers. Use the same read boundary for
    // reference trees so exact field/type equality also works after storage.
    // Floating-point, string, missing and extra fields remain unequal.
    return Json.read(Json.MAPPER.writeValueAsString(value));
  }

  static UUID uuid(String value) {
    valid(
        value != null
            && value.matches("[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"));
    return UUID.fromString(value);
  }

  static void positive(long value) {
    valid(value > 0 && value <= MAX_SAFE_INTEGER);
  }

  static void valid(boolean condition) {
    if (!condition) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }
}
