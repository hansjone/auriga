package auriga.iam.api;

import tools.jackson.databind.JsonNode;

/** Trusted enterprise extension. Raw credentials and private protocol mappings stay server-side. */
public interface UacEnterpriseAdapter extends IdentityConnector {
  String qrContent(JsonNode config, JsonNode coordinates);

  JsonNode scan(JsonNode config, JsonNode coordinates);

  JsonNode department(JsonNode config, JsonNode credential, String purpose);

  JsonNode verify(JsonNode config, JsonNode credential);

  /**
   * New-login proof must return an explicit authoritative subject; legacy verify is insufficient.
   */
  default JsonNode verifyProof(JsonNode config, JsonNode credential) {
    throw new UnsupportedOperationException("UAC proof unavailable");
  }

  JsonNode ssoCookies(JsonNode cookies);

  /** Presence only; never return private Cookie names to diagnostics. Null means unknown. */
  default Boolean ssoCookiePresent(JsonNode cookies) {
    return null;
  }

  /** Fixed capability booleans only; no private names, values or protocol mappings. */
  default java.util.Map<String, Boolean> diagnosticCapabilities() {
    return java.util.Map.of();
  }
}
