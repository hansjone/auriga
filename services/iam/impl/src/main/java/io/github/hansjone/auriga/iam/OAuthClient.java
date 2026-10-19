package io.github.hansjone.auriga.iam;

import java.util.Set;
import java.util.UUID;

/** Installation-owned client registration. No dynamic public registration or audience parameter. */
public record OAuthClient(
    String id,
    UUID tenant,
    String secretHash,
    Set<String> redirectUris,
    Set<String> scopes,
    Set<String> grants,
    boolean platform) {
  public OAuthClient {
    if (id == null
        || id.isBlank()
        || Set.of("auriga", "auriga.internal", "auriga.ai-gateway").contains(id))
      throw new IllegalArgumentException("Reserved client id");
    redirectUris = Set.copyOf(redirectUris);
    scopes = Set.copyOf(scopes);
    grants = Set.copyOf(grants);
    for (String uri : redirectUris) {
      java.net.URI parsed = java.net.URI.create(uri);
      if (parsed.getFragment() != null
          || parsed.getRawUserInfo() != null
          || parsed.getHost() == null
          || !(parsed.getScheme().equals("https") || parsed.getScheme().equals("http")))
        throw new IllegalArgumentException("Invalid redirect URI");
    }
  }

  public String audience() {
    return platform ? "auriga" : id;
  }
}
