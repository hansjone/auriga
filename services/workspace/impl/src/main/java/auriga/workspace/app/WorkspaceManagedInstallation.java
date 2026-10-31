package auriga.workspace.app;

import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.util.List;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManagerFactory;
import tools.jackson.databind.node.ObjectNode;

/** Installation-owned projection; never accepts a request-selected origin or key source. */
public final class WorkspaceManagedInstallation {
  private WorkspaceManagedInstallation() {}

  public static ObjectNode project(
      ObjectNode installation, String origin, List<String> allowed, String caFile) {
    var result = installation.deepCopy();
    if (origin == null || origin.isEmpty()) return result;
    var uri = URI.create(origin);
    if (!"https".equals(uri.getScheme())
        || uri.getHost() == null
        || uri.getUserInfo() != null
        || !uri.getRawPath().isEmpty()
        || uri.getRawQuery() != null
        || uri.getRawFragment() != null
        || !origin.equals("https://" + uri.getRawAuthority())
        || !allowed.contains(origin)
        || uri.getPort() == 0
        || uri.getPort() > 65535
        || caFile == null
        || caFile.isBlank())
      throw new IllegalArgumentException(
          "Managed platform requires an approved HTTPS origin and CA file");
    if (!(result.path("network") instanceof ObjectNode network))
      throw new IllegalArgumentException("Managed network installation required");
    result.put("platformOrigin", origin);
    result.put("issuer", origin);
    result.put("modelBaseUrl", origin + "/ai/v1");
    result.put("modelCaFile", caFile);
    network.put("wssUrl", "wss://" + uri.getRawAuthority() + "/net/v1/ws");
    return result;
  }

  static SSLContext tls(String caFile) {
    try {
      var path = Path.of(caFile);
      if (!path.isAbsolute() || Files.size(path) > 65536) throw new IllegalArgumentException();
      var store = KeyStore.getInstance(KeyStore.getDefaultType());
      store.load(null);
      try (var input = Files.newInputStream(path)) {
        int index = 0;
        for (var cert : CertificateFactory.getInstance("X.509").generateCertificates(input)) {
          var ca = (X509Certificate) cert;
          ca.checkValidity();
          if (ca.getBasicConstraints() < 0) throw new IllegalArgumentException();
          store.setCertificateEntry("managed-" + index++, ca);
        }
        if (index == 0) throw new IllegalArgumentException();
      }
      var managers = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
      managers.init(store);
      var context = SSLContext.getInstance("TLS");
      context.init(null, managers.getTrustManagers(), null);
      return context;
    } catch (Exception invalid) {
      throw new IllegalArgumentException("Invalid installation managed CA", invalid);
    }
  }
}
