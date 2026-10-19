package auriga.common.autoconfigure;

import auriga.common.error.ErrorMessages;
import auriga.common.error.ProblemExceptionHandler;
import auriga.common.error.Problems;
import auriga.common.license.LicenseCheckpoint;
import auriga.common.license.UsageMeter;
import auriga.common.security.AuthorizationAdvisor;
import auriga.common.security.CorrelationFilter;
import java.time.Clock;
import org.springframework.beans.factory.config.BeanDefinition;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.boot.web.servlet.FilterRegistrationBean;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.annotation.Role;
import org.springframework.core.Ordered;

/** Errors, request correlation, declarative authorization, license and metering defaults. */
@AutoConfiguration
@EnableConfigurationProperties(AurigaProperties.class)
public class AurigaCoreAutoConfiguration {
  @Bean
  @ConditionalOnMissingBean
  Clock aurigaClock() {
    return Clock.systemUTC();
  }

  @Bean
  @ConditionalOnMissingBean
  ErrorMessages aurigaErrorMessages(AurigaProperties properties) {
    return new ErrorMessages(
        properties.getErrors().getMessageBundles(), properties.getErrors().getDefaultLocale());
  }

  @Bean
  @ConditionalOnMissingBean
  Problems aurigaProblems(ErrorMessages messages) {
    return new Problems(messages);
  }

  @Bean
  @ConditionalOnMissingBean
  LicenseCheckpoint aurigaLicenseCheckpoint() {
    return LicenseCheckpoint.COMMUNITY;
  }

  @Bean
  @ConditionalOnMissingBean
  UsageMeter aurigaUsageMeter() {
    return UsageMeter.NOOP;
  }

  @Bean
  @Role(BeanDefinition.ROLE_INFRASTRUCTURE)
  static AuthorizationAdvisor aurigaAuthorizationAdvisor() {
    // Static and clock-independent at definition time: advisors are created before regular beans.
    return new AuthorizationAdvisor(Clock.systemUTC());
  }

  @Configuration(proxyBeanMethods = false)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  static class Web {
    @Bean
    @ConditionalOnMissingBean
    ProblemExceptionHandler aurigaProblemExceptionHandler(Problems problems) {
      return new ProblemExceptionHandler(problems);
    }

    @Bean
    FilterRegistrationBean<CorrelationFilter> aurigaCorrelationFilter() {
      FilterRegistrationBean<CorrelationFilter> registration =
          new FilterRegistrationBean<>(new CorrelationFilter());
      // Before Spring Security (-100) so 401/403 problems carry the trace id.
      registration.setOrder(Ordered.HIGHEST_PRECEDENCE + 10);
      return registration;
    }
  }
}
