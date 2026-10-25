package auriga.iam;

import auriga.iam.api.InternalTokenIssuer;
import java.nio.file.Path;

/** Installation-only, fixed svc:edge export for a same-host POSIX combined deployment. */
public final class EdgeTokenFilePublisher extends ServiceTokenFilePublisher {
  public EdgeTokenFilePublisher(
      InternalTokenIssuer issuer, Path secret, Path token, int readerGroup) {
    this(issuer, secret, token, readerGroup, -1);
  }

  public EdgeTokenFilePublisher(
      InternalTokenIssuer issuer, Path secret, Path token, int readerGroup, int installationOwner) {
    super("edge", issuer, secret, token, readerGroup, installationOwner);
  }
}
