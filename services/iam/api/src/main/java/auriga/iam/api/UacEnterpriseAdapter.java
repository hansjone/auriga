package auriga.iam.api;

import tools.jackson.databind.JsonNode;

/** Trusted enterprise extension. Raw credentials and private protocol mappings stay server-side. */
public interface UacEnterpriseAdapter extends IdentityConnector {
  String qrContent(JsonNode config, JsonNode coordinates);

  JsonNode scan(JsonNode config, JsonNode coordinates);

  JsonNode department(JsonNode config, JsonNode credential, String purpose);

  JsonNode verify(JsonNode config, JsonNode credential);

  JsonNode ssoCookies(JsonNode cookies);
}
