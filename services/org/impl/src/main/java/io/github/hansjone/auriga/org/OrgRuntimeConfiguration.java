package io.github.hansjone.auriga.org;

import io.github.hansjone.auriga.common.events.EventPublisher;
import io.github.hansjone.auriga.common.persistence.TenantTransactionManager;
import io.github.hansjone.auriga.common.security.AurigaWebSecurity;
import io.github.hansjone.auriga.common.security.EndpointAuthorizationCheck;
import io.github.hansjone.auriga.common.security.ProblemSecurityHandlers;
import io.github.hansjone.auriga.iam.api.IdentityPolicyHooks;
import io.github.hansjone.auriga.iam.api.PermissionResolver;
import io.grpc.BindableService;
import java.time.Clock;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.annotation.Order;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression("${auriga.services.org.enabled:true}")
@ConditionalOnProperty(prefix = "auriga.service", name = "schema", havingValue = "org")
public class OrgRuntimeConfiguration {
  @Bean
  OrgStore orgStore(DataSource source, TenantTransactionManager manager) {
    return new OrgStore(new JdbcTemplate(source), manager);
  }

  @Bean
  @org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean(Clock.class)
  Clock orgClock() {
    return Clock.systemUTC();
  }

  @Bean
  OrgService orgService(OrgStore store, EventPublisher events, Clock clock) {
    return new OrgService(store, events, clock);
  }

  @Bean
  PermissionResolver orgPermissionResolver(OrgService org) {
    return new OrgPermissionResolver(org);
  }

  @Bean
  IdentityPolicyHooks orgIdentityPolicyHooks(OrgService org) {
    return new OrgIdentityPolicyHooks(org);
  }

  @Bean
  OrgPortalController orgPortalController(OrgService org) {
    return new OrgPortalController(org);
  }

  @Bean
  OrgAdminController orgAdminController(OrgService org) {
    return new OrgAdminController(org);
  }

  @Bean
  BindableService orgTenantGrpc(OrgService org) {
    return new OrgGrpcServices.TenantGrpc(org);
  }

  @Bean
  BindableService orgMemberGrpc(OrgService org) {
    return new OrgGrpcServices.MemberGrpc(org);
  }

  @Bean
  BindableService orgAuthorizationGrpc(OrgService org) {
    return new OrgGrpcServices.AuthorizationGrpc(org);
  }

  @Bean
  BindableService orgInvitationGrpc(OrgService org) {
    return new OrgGrpcServices.InvitationGrpc(org);
  }

  @Bean
  BindableService orgQuotaGrpc(OrgService org) {
    return new OrgGrpcServices.QuotaGrpc(org);
  }

  @Bean
  BindableService orgPolicyGrpc(OrgService org) {
    return new OrgGrpcServices.PolicyGrpc(org);
  }

  @Bean
  @Order(2)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain orgApiChain(
      HttpSecurity http,
      JwtDecoder decoder,
      ProblemSecurityHandlers handlers,
      io.github.hansjone.auriga.common.autoconfigure.AurigaProperties properties,
      ObjectProvider<RequestMappingHandlerMapping> mappings)
      throws Exception {
    return AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        properties.getSecurity().getPublicPaths(),
        EndpointAuthorizationCheck.publicEndpoints(mappings));
  }
}
