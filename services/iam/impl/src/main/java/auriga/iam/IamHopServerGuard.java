package auriga.iam;

import org.springframework.beans.factory.config.BeanPostProcessor;
import org.springframework.boot.web.server.AbstractConfigurableWebServerFactory;
import org.springframework.boot.web.server.Ssl;

/** Validate the actual configured server factory, after Boot's property customizers have run. */
final class IamHopServerGuard implements BeanPostProcessor {
  private final boolean loopbackHttp;

  IamHopServerGuard(boolean loopbackHttp) {
    this.loopbackHttp = loopbackHttp;
  }

  @Override
  public Object postProcessAfterInitialization(Object bean, String name) {
    if (bean instanceof org.springframework.boot.web.server.servlet.ServletWebServerFactory) {
      if (!(bean instanceof AbstractConfigurableWebServerFactory factory))
        throw new IllegalStateException("Unsupported IAM HTTP server factory");
      Ssl ssl = factory.getSsl();
      if (loopbackHttp && !Ssl.isEnabled(ssl)) {
        if (factory.getAddress() == null || !factory.getAddress().isLoopbackAddress())
          throw new IllegalStateException("IAM development HTTP hop must bind a loopback address");
      } else if (!Ssl.isEnabled(ssl)
          || ssl.getClientAuth() != Ssl.ClientAuth.NEED
          || (!org.springframework.util.StringUtils.hasText(ssl.getTrustStore())
              && !org.springframework.util.StringUtils.hasText(ssl.getTrustCertificate()))) {
        throw new IllegalStateException(
            "IAM HTTP connector must enforce mutual TLS with a client CA");
      }
    }
    return bean;
  }
}
