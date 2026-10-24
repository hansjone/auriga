package auriga.org;

import auriga.common.events.EventPublisher;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.security.AurigaWebSecurity;
import auriga.common.security.EndpointAuthorizationCheck;
import auriga.common.security.ProblemSecurityHandlers;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.iam.api.IdentityPolicyHooks;
import io.grpc.BindableService;
import java.time.Clock;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.annotation.Primary;
import org.springframework.core.annotation.Order;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.org.enabled:true} && ('${auriga.service.schema:}' == 'org' || ${auriga.platform.combined:false})")
public class OrgRuntimeConfiguration {
  @Bean
  @org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean(OrgStore.class)
  OrgStore orgStore(
      org.springframework.beans.factory.ListableBeanFactory beans,
      @org.springframework.beans.factory.annotation.Qualifier("orgTransactionManager")
          ObjectProvider<TenantTransactionManager> orgTx,
      ObjectProvider<TenantTransactionManager> tx) {
    DataSource source;
    TenantTransactionManager manager;
    if (beans.containsBean("orgDataSource")) {
      source = beans.getBean("orgDataSource", DataSource.class);
      manager =
          orgTx.getIfAvailable(
              () -> {
                throw new IllegalStateException(
                    "orgDataSource present but orgTransactionManager missing");
              });
    } else {
      source = beans.getBean(DataSource.class);
      manager = orgTx.getIfAvailable(tx::getObject);
    }
    return new OrgStore(new JdbcTemplate(source), manager);
  }

  @Bean
  @org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean(Clock.class)
  Clock orgClock() {
    return Clock.systemUTC();
  }

  @Bean
  OrgService orgService(
      OrgStore store,
      org.springframework.beans.factory.ListableBeanFactory beans,
      @org.springframework.beans.factory.annotation.Qualifier("orgEventPublisher")
          ObjectProvider<EventPublisher> orgEvents,
      ObjectProvider<EventPublisher> events,
      Clock clock,
      ObjectProvider<DeploymentAccess> deploymentAccess) {
    EventPublisher publisher;
    if (beans.containsBean("orgEventPublisher")) {
      publisher =
          orgEvents.getIfAvailable(
              () -> {
                throw new IllegalStateException("orgEventPublisher bean missing");
              });
    } else {
      publisher = orgEvents.getIfAvailable(events::getObject);
    }
    // Even a primary bean cannot disambiguate multiple installation identities safely.
    var sources = deploymentAccess.stream().limit(2).toList();
    return new OrgService(store, publisher, clock, sources.size() == 1 ? sources.getFirst() : null);
  }

  @Bean
  @Primary
  OrgPermissionResolver orgPermissionResolver(OrgService org) {
    return new OrgPermissionResolver(org);
  }

  @Bean
  @Primary
  IdentityPolicyHooks orgIdentityPolicyHooks(OrgService org) {
    return new OrgIdentityPolicyHooks(org);
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
      auriga.common.autoconfigure.AurigaProperties properties,
      ObjectProvider<RequestMappingHandlerMapping> mappings)
      throws Exception {
    http.securityMatcher("/api/org/**");
    return AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        properties.getSecurity().getPublicPaths(),
        EndpointAuthorizationCheck.publicEndpoints(mappings));
  }
}
