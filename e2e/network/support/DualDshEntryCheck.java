package auriga.iam;

import auriga.common.error.AurigaException;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Set;
import org.springframework.mock.web.MockHttpServletRequest;

/** Pure production ingress validation. No login, database or successful SEND is simulated. */
public final class DualDshEntryCheck {
  public static void main(String[] args) throws Exception {
    if (args.length != 2) throw new IllegalArgumentException("access and request fixtures required");
    var access = DeploymentAccess.load(Files.readAllBytes(Path.of(args[0])));
    var identity =
        new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    var inputs = Json.read(Files.readString(Path.of(args[1])));
    for (String name : new String[] {"before", "after", "foreignOrigin"}) {
      var request = new MockHttpServletRequest();
      request.setMethod("POST");
      request.setRequestURI("/api/iam/v1/login/transactions");
      request.setRemoteAddr("127.0.0.1");
      request.setScheme("http");
      request.setSecure(false);
      inputs
          .path(name)
          .properties()
          .forEach(e -> request.addHeader(e.getKey(), e.getValue().asString()));
      try {
        String ip =
            identity.call(
                request,
                () -> {
                  identity.sameOrigin(request);
                  return identity.clientIp(request);
                });
        if (!name.equals("after") || !ip.equals("127.0.0.1"))
          throw new AssertionError("unexpected ingress acceptance");
        System.out.println(Json.object("case", name, "ingressAccepted", true, "clientIp", ip));
      } catch (AurigaException error) {
        int status = name.equals("before") ? 400 : 403;
        String code = name.equals("before") ? "common.validation_failed" : "common.forbidden";
        if (name.equals("after")
            || !error.errorCode().code().equals(code)
            || error.errorCode().httpStatus() != status) throw error;
        System.out.println(Json.object("case", name, "status", status, "code", code));
      }
    }
  }
}
