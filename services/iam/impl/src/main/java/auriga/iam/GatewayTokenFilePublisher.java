package auriga.iam;

import auriga.iam.api.InternalTokenIssuer;
import java.nio.file.Path;

/** Installation-only, fixed svc:ai-gateway export for a same-host POSIX combined deployment. */
public final class GatewayTokenFilePublisher extends ServiceTokenFilePublisher {
  public GatewayTokenFilePublisher(
      InternalTokenIssuer issuer, Path secret, Path token, int readerGroup) {
    this(issuer, secret, token, readerGroup, -1);
  }

  public GatewayTokenFilePublisher(
      InternalTokenIssuer issuer, Path secret, Path token, int readerGroup, int installationOwner) {
    super("ai-gateway", issuer, secret, token, readerGroup, installationOwner);
  }
}
