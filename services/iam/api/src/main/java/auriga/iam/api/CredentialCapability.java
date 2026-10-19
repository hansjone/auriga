package auriga.iam.api;

import tools.jackson.databind.JsonNode;

public interface CredentialCapability {
  JsonNode issueCredential(JsonNode request);
}
