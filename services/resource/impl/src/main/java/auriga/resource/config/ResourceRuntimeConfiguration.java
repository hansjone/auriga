package auriga.resource.config;

import auriga.common.persistence.TenantTransactionManager;
import auriga.common.security.*;
import auriga.resource.api.ResourceAccessInputs;
import auriga.resource.app.*;
import auriga.resource.persistence.ResourceStore;
import auriga.resource.web.ResourceController;
import auriga.resource.web.ResourceMetadataController;
import auriga.resource.web.model.PackagePage;
import auriga.resource.web.model.Version;
import auriga.resource.web.model.VersionPage;
import com.fasterxml.jackson.annotation.JsonInclude;
import java.time.Clock;
import java.util.Base64;
import javax.sql.DataSource;
import org.springframework.beans.factory.ListableBeanFactory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;
import org.springframework.core.annotation.Order;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
@Import({ResourceController.class, ResourceMetadataController.class})
public class ResourceRuntimeConfiguration {
  @Bean
  org.springframework.boot.jackson.autoconfigure.JsonMapperBuilderCustomizer resourceJsonModels() {
    // Optional contract fields must be absent, not null. Do not change other services' models
    // or Package.defaultVersionId, which is explicitly required and nullable.
    return builder ->
        builder
            .addMixIn(PackagePage.class, OptionalFields.class)
            .addMixIn(VersionPage.class, OptionalFields.class)
            .addMixIn(Version.class, OptionalFields.class);
  }

  @JsonInclude(JsonInclude.Include.NON_NULL)
  private abstract static class OptionalFields {}

  @Bean
  ResourceStore resourceStore(ListableBeanFactory beans) {
    DataSource source =
        beans.containsBean("resourceDataSource")
            ? beans.getBean("resourceDataSource", DataSource.class)
            : beans.getBean(DataSource.class);
    TenantTransactionManager manager =
        beans.containsBean("resourceTransactionManager")
            ? beans.getBean("resourceTransactionManager", TenantTransactionManager.class)
            : beans.getBean(TenantTransactionManager.class);
    return new ResourceStore(new JdbcTemplate(source), manager);
  }

  @Bean
  @ConditionalOnMissingBean(Clock.class)
  Clock resourceClock() {
    return Clock.systemUTC();
  }

  @Bean
  ResourceCursor resourceCursor(Environment env, Clock clock) {
    return new ResourceCursor(
        Base64.getDecoder().decode(env.getRequiredProperty("auriga.resource.cursor-signing-key")),
        clock);
  }

  @Bean
  ResourceAccess resourceAccess(ResourceAccessInputs inputs) {
    return new ResourceAccess(inputs);
  }

  @Bean
  ResourceQueries resourceQueries(
      ResourceStore store, ResourceAccess access, ResourceCursor cursors) {
    return new ResourceQueries(store, access, cursors);
  }

  @Bean
  ResourceCommands resourceCommands(ResourceStore store, ResourceAccess access, Clock clock) {
    return new ResourceCommands(store, access, clock);
  }

  @Bean
  @Order(4)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain resourceApiChain(
      HttpSecurity http,
      JwtDecoder decoder,
      ProblemSecurityHandlers handlers,
      ObjectProvider<RequestMappingHandlerMapping> mappings)
      throws Exception {
    http.securityMatcher("/api/resource/**");
    return AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        java.util.List.of(),
        EndpointAuthorizationCheck.publicEndpoints(mappings));
  }
}
