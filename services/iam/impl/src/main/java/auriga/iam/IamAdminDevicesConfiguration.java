package auriga.iam;

import auriga.common.audit.AuditLogger;
import java.time.Clock;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
public class IamAdminDevicesConfiguration {
  @Bean
  IamGrpcAdminDevices iamGrpcAdminDevices(IamAdminDevices devices, IamGrpcSessions transportGuard) {
    return new IamGrpcAdminDevices(devices);
  }

  @Bean
  IamAdminDevices iamAdminDevices(IamStore store, Clock clock, AuditLogger audit) {
    return new IamAdminDevices(store, clock, audit);
  }
}
