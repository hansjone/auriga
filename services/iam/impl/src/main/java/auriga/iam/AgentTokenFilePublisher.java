package auriga.iam;

import auriga.iam.api.InternalTokenIssuer;
import java.nio.file.Path;

/** Installation-only fixed svc:agent export; POSIX owner-only, never a group credential. */
public final class AgentTokenFilePublisher extends ServiceTokenFilePublisher {
  public AgentTokenFilePublisher(
      InternalTokenIssuer issuer, Path secret, Path token, int installationOwner) {
    super("agent", issuer, secret, token, -1, installationOwner);
    if (secret.getParent().startsWith(token.getParent())
        || token.getParent().startsWith(secret.getParent()))
      throw new IllegalStateException(
          "Agent secret and token require disjoint private directories");
  }
}
