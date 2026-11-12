package auriga.resource.config;

import auriga.common.persistence.TenantTransactionManager;
import auriga.common.security.*;
import auriga.resource.api.ResourceAccessInputs;
import auriga.resource.app.*;
import auriga.resource.objectstore.*;
import auriga.resource.persistence.ResourceStore;
import auriga.resource.persistence.ResourceWorkflowStore;
import auriga.resource.web.ResourceAdministrationController;
import auriga.resource.web.ResourceController;
import auriga.resource.web.ResourceMetadataController;
import auriga.resource.web.ResourceWorkflowController;
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
@org.springframework.scheduling.annotation.EnableScheduling
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
@Import({
  auriga.resource.web.DesktopController.class,
  ResourceController.class,
  ResourceMetadataController.class,
  ResourceAdministrationController.class,
  ResourceWorkflowController.class,
  auriga.resource.web.ResourceDistributionController.class,
  auriga.resource.web.ResourceNodeDownloadController.class,
  auriga.resource.web.ResourceRevocationController.class,
  auriga.resource.web.ResourceMemberController.class
})
public class ResourceRuntimeConfiguration {
  @Bean
  @ConditionalOnProperty({
    "auriga.resource.desktop-directory",
    "auriga.resource.desktop-public-key-file"
  })
  DesktopReleases desktopReleases(Environment env, Clock clock, ResourceAccess access)
      throws Exception {
    return new DesktopReleases(
        java.nio.file.Path.of(env.getRequiredProperty("auriga.resource.desktop-directory")),
        java.nio.file.Files.readString(
            java.nio.file.Path.of(
                env.getRequiredProperty("auriga.resource.desktop-public-key-file"))),
        clock,
        access);
  }

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

  private DataSource resourceSource(ListableBeanFactory beans) {
    return beans.containsBean("resourceDataSource")
        ? beans.getBean("resourceDataSource", DataSource.class)
        : beans.getBean(DataSource.class);
  }

  @Bean
  ResourceWorkflowStore resourceWorkflowStore(ListableBeanFactory beans) {
    return new ResourceWorkflowStore(new JdbcTemplate(resourceSource(beans)));
  }

  @Bean(destroyMethod = "close")
  @ConditionalOnProperty("auriga.resource.object-directory")
  @ConditionalOnMissingBean(ObjectStore.class)
  ObjectStore resourceObjects(Environment env, Clock clock) {
    return new LocalObjectStore(
        java.nio.file.Path.of(env.getRequiredProperty("auriga.resource.object-directory")),
        clock,
        java.time.Duration.ofHours(24));
  }

  @Bean
  @ConditionalOnProperty({
    "auriga.resource.release-key-file",
    "auriga.resource.revocation-key-file"
  })
  @ConditionalOnMissingBean(ResourceSigning.class)
  ResourceSigning resourceSigning(Environment env) {
    return new ResourceSigning(
        env.getRequiredProperty("auriga.resource.release-kid"),
        ResourceSigning.load(
            java.nio.file.Path.of(env.getRequiredProperty("auriga.resource.release-key-file"))),
        env.getRequiredProperty("auriga.resource.revocation-kid"),
        ResourceSigning.load(
            java.nio.file.Path.of(env.getRequiredProperty("auriga.resource.revocation-key-file"))));
  }

  @Bean
  ResourceWorkflow resourceWorkflow(
      ResourceStore store,
      ResourceWorkflowStore db,
      ResourceAccess access,
      ObjectProvider<ObjectStore> objects,
      ObjectProvider<ResourceSigning> signing,
      ListableBeanFactory beans,
      Clock clock) {
    var events =
        new auriga.common.events.OutboxEventPublisher(
            new JdbcTemplate(resourceSource(beans)),
            "resource",
            "resource",
            new auriga.common.events.EventSchemas(),
            tools.jackson.databind.json.JsonMapper.builder().build(),
            clock,
            () -> {});
    return new ResourceWorkflow(
        store, db, access, () -> required(objects), () -> required(signing), events, clock);
  }

  @Bean
  ResourceMembers resourceMembers(
      ResourceStore store,
      ResourceWorkflowStore db,
      ResourceAccess access,
      ResourceWorkflow workflow,
      ResourceDistributions distributions,
      ObjectProvider<ObjectStore> objects,
      Clock clock) {
    return new ResourceMembers(
        store, db, access, workflow, distributions, () -> required(objects), clock);
  }

  @Bean
  ResourceGarbageCollector resourceGarbageCollector(
      ResourceStore store,
      ResourceWorkflowStore db,
      ObjectProvider<ObjectStore> objects,
      Clock clock) {
    return new ResourceGarbageCollector(store, db, objects::getIfAvailable, clock);
  }

  @Bean
  ResourceDistributions resourceDistributions(
      ResourceStore store,
      ResourceWorkflowStore db,
      ResourceAccess access,
      ResourceWorkflow workflow,
      ObjectProvider<auriga.resource.api.ResourceWorkspace> workspace,
      ObjectProvider<ResourceSigning> signing,
      ObjectProvider<ObjectStore> objects,
      Environment env,
      ResourceCursor cursors,
      ListableBeanFactory beans,
      Clock clock) {
    var events =
        new auriga.common.events.OutboxEventPublisher(
            new JdbcTemplate(resourceSource(beans)),
            "resource",
            "resource",
            new auriga.common.events.EventSchemas(),
            tools.jackson.databind.json.JsonMapper.builder().build(),
            clock,
            () -> {});
    return new ResourceDistributions(
        store,
        db,
        access,
        workflow,
        workspace::getIfAvailable,
        () -> required(signing),
        () -> required(objects),
        () -> env.getRequiredProperty("auriga.resource.download-base-url"),
        cursors,
        events,
        clock);
  }

  static <T> T required(ObjectProvider<T> provider) {
    var value = provider.getIfAvailable();
    if (value == null)
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.UNAVAILABLE);
    return value;
  }

  @Bean
  auriga.resource.web.ResourceMultipartDirectory resourceMultipartDirectory()
      throws java.io.IOException {
    return new auriga.resource.web.ResourceMultipartDirectory();
  }

  @Bean
  jakarta.servlet.MultipartConfigElement resourceMultipart(
      auriga.resource.web.ResourceMultipartDirectory directory) {
    return new jakarta.servlet.MultipartConfigElement(directory.location(), 67108864, 67239936, 0);
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
    http.securityMatcher("/api/resource/**", "/internal/resource/**");
    return AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        java.util.List.of(),
        EndpointAuthorizationCheck.publicEndpoints(mappings));
  }
}
