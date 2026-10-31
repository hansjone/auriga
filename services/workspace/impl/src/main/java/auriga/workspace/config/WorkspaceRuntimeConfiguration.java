package auriga.workspace.config;

import auriga.common.events.EventPublisher;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.security.*;
import auriga.contracts.node.v1.*;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.app.*;
import auriga.workspace.domain.*;
import auriga.workspace.grpc.*;
import auriga.workspace.persistence.WorkspaceStore;
import auriga.workspace.web.model.ConnectManifest;
import java.nio.file.*;
import java.time.Clock;
import java.util.*;
import java.util.concurrent.*;
import javax.sql.DataSource;
import org.springframework.beans.factory.*;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;
import org.springframework.core.annotation.Order;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.config.http.SessionCreationPolicy;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
public class WorkspaceRuntimeConfiguration {
  @Bean
  auriga.common.error.ErrorMessages workspaceErrorMessages(
      auriga.common.autoconfigure.AurigaProperties properties) {
    var bundles = new ArrayList<>(properties.getErrors().getMessageBundles());
    bundles.add("messages/workspace-errors");
    return new auriga.common.error.ErrorMessages(
        bundles, properties.getErrors().getDefaultLocale());
  }

  @Bean(destroyMethod = "shutdownNow")
  @ConditionalOnExpression("!${auriga.platform.combined:false}")
  io.grpc.ManagedChannel workspaceOrgChannel(
      Environment env, auriga.common.grpc.AurigaClientInterceptor interceptor) {
    return auriga.common.grpc.GrpcChannels.openTls(
        auriga.common.grpc.GrpcChannels.target(env, "org"),
        Path.of(env.getRequiredProperty("auriga.workspace.org-client-certificate-file")).toFile(),
        Path.of(env.getRequiredProperty("auriga.workspace.org-client-key-file")).toFile(),
        Path.of(env.getRequiredProperty("auriga.workspace.org-ca-file")).toFile(),
        interceptor);
  }

  @Bean
  @ConditionalOnExpression("!${auriga.platform.combined:false}")
  WorkspaceInputs remoteWorkspaceInputs(
      @Qualifier("workspaceOrgChannel") io.grpc.ManagedChannel channel) {
    return new RemoteWorkspaceInputs(channel);
  }

  @Bean
  @ConditionalOnMissingBean(Clock.class)
  Clock workspaceClock() {
    return Clock.systemUTC();
  }

  @Bean
  WorkspaceStore workspaceStore(
      ListableBeanFactory beans, ObjectProvider<TenantTransactionManager> managers) {
    DataSource ds =
        beans.containsBean("workspaceDataSource")
            ? beans.getBean("workspaceDataSource", DataSource.class)
            : beans.getBean(DataSource.class);
    var manager =
        beans.containsBean("workspaceTransactionManager")
            ? beans.getBean("workspaceTransactionManager", TenantTransactionManager.class)
            : managers.getObject();
    return new WorkspaceStore(new JdbcTemplate(ds), manager);
  }

  @Bean
  WorkspaceTemplates workspaceTemplates(Environment env) throws Exception {
    List<InstanceSpec> templates = new ArrayList<>();
    String directory = env.getProperty("auriga.workspace.templates-directory", "");
    if (!directory.isBlank())
      try (var files = Files.list(Path.of(directory))) {
        for (var file :
            files.filter(p -> p.getFileName().toString().endsWith(".pb")).sorted().toList())
          templates.add(InstanceSpec.parseFrom(Files.readAllBytes(file)));
      }
    return new WorkspaceTemplates(templates);
  }

  @Bean
  WorkspaceCrypto workspaceCrypto(Environment env) {
    return new WorkspaceCrypto(
        Base64.getDecoder().decode(env.getRequiredProperty("auriga.workspace.encryption-key")));
  }

  @Bean
  NodeCa workspaceNodeCa(Environment env, Clock clock) {
    return NodeCa.load(
        Path.of(env.getRequiredProperty("auriga.workspace.ca-certificate-file")),
        Path.of(env.getRequiredProperty("auriga.workspace.ca-private-key-file")),
        clock);
  }

  @Bean
  WorkspaceService workspaceService(
      WorkspaceStore store,
      WorkspaceInputs inputs,
      WorkspaceTemplates templates,
      WorkspaceCrypto crypto,
      Clock clock,
      ListableBeanFactory beans,
      ObjectProvider<EventPublisher> events,
      Environment env) {
    var publisher =
        beans.containsBean("workspaceEventPublisher")
            ? beans.getBean("workspaceEventPublisher", EventPublisher.class)
            : events.getObject();
    var service = new WorkspaceService(store, inputs, templates, crypto, publisher, clock);
    if (env.containsProperty("auriga.workspace.managed-network-installation-file"))
      service.requireManagedNetworkCredentials();
    return service;
  }

  @Bean
  WorkspaceQueries workspaceQueries(WorkspaceService service) {
    return new WorkspaceQueries(service);
  }

  @Bean
  auriga.workspace.api.ManagedInstanceBindings workspaceManagedBindings(
      WorkspaceStore store, WorkspaceInputs inputs) {
    return new WorkspaceManagedBindings(store, inputs);
  }

  @Bean
  ManagedInstanceBindingGrpc managedInstanceBindingGrpc(
      auriga.workspace.api.ManagedInstanceBindings bindings) {
    return new ManagedInstanceBindingGrpc(bindings);
  }

  @Bean(destroyMethod = "shutdownNow")
  @ConditionalOnProperty(name = "auriga.workspace.managed-network-installation-file")
  ScheduledExecutorService workspaceManagedNetworkExecutor(
      WorkspaceService service,
      WorkspaceCrypto crypto,
      auriga.iam.api.ManagedDeviceCredentials devices,
      Environment env)
      throws java.io.IOException {
    var installation =
        (tools.jackson.databind.node.ObjectNode)
            WorkspaceService.JSON.readTree(
                Files.readAllBytes(
                    Path.of(
                        env.getRequiredProperty(
                            "auriga.workspace.managed-network-installation-file"))));
    installation =
        auriga.workspace.app.WorkspaceManagedInstallation.project(
            installation,
            env.getProperty("auriga.workspace.managed-platform-origin", ""),
            java.util.Arrays.asList(env.getProperty("auriga.links.allowed-origins", "").split(",")),
            env.getProperty("auriga.workspace.managed-ca-file", ""));
    var network = new WorkspaceManagedNetwork(service, crypto, devices, installation);
    var executor =
        Executors.newSingleThreadScheduledExecutor(
            r -> {
              var thread = new Thread(r, "workspace-managed-network");
              thread.setDaemon(true);
              return thread;
            });
    executor.scheduleWithFixedDelay(
        () -> {
          try {
            network.tick();
          } catch (RuntimeException error) {
            org.slf4j.LoggerFactory.getLogger(WorkspaceManagedNetwork.class)
                .warn("Managed network reconciliation deferred");
          }
        },
        0,
        1,
        TimeUnit.SECONDS);
    return executor;
  }

  @Bean(destroyMethod = "shutdownNow")
  @ConditionalOnExpression("!${auriga.platform.combined:false}")
  @ConditionalOnProperty(name = "auriga.workspace.managed-network-installation-file")
  io.grpc.ManagedChannel workspaceIamChannel(
      Environment env, auriga.common.grpc.AurigaClientInterceptor interceptor) {
    return auriga.common.grpc.GrpcChannels.openTls(
        auriga.common.grpc.GrpcChannels.target(env, "iam"),
        Path.of(env.getRequiredProperty("auriga.workspace.iam-client-certificate-file")).toFile(),
        Path.of(env.getRequiredProperty("auriga.workspace.iam-client-key-file")).toFile(),
        Path.of(env.getRequiredProperty("auriga.workspace.iam-ca-file")).toFile(),
        interceptor);
  }

  @Bean
  @ConditionalOnExpression("!${auriga.platform.combined:false}")
  @ConditionalOnProperty(name = "auriga.workspace.managed-network-installation-file")
  auriga.iam.api.ManagedDeviceCredentials remoteManagedDeviceCredentials(
      @Qualifier("workspaceIamChannel") io.grpc.ManagedChannel channel) {
    return new auriga.iam.api.ManagedDeviceCredentials() {
      public auriga.contracts.iam.v1.IssueManagedDeviceResponse issue(
          auriga.contracts.iam.v1.IssueManagedDeviceRequest request) {
        return auriga.common.grpc.GrpcErrors.call(
            () ->
                auriga.contracts.iam.v1.ManagedDeviceServiceGrpc.newBlockingStub(channel)
                    .issueManagedDevice(request));
      }

      public void revoke(auriga.contracts.iam.v1.RevokeManagedDeviceRequest request) {
        auriga.common.grpc.GrpcErrors.call(
            () ->
                auriga.contracts.iam.v1.ManagedDeviceServiceGrpc.newBlockingStub(channel)
                    .revokeManagedDevice(request));
      }
    };
  }

  @Bean(initMethod = "start", destroyMethod = "close")
  @ConditionalOnProperty(name = "auriga.workspace.routes.nats-url")
  WorkspaceRoutes workspaceRoutes(WorkspaceStore store, Environment env) {
    return WorkspaceRoutes.configured(
        store,
        env.getRequiredProperty("auriga.workspace.routes.nats-url"),
        env.getProperty("auriga.workspace.routes.nats-creds-file", ""),
        env.getProperty("auriga.workspace.routes.nats-nkey-seed-file", ""),
        env.getProperty("auriga.workspace.routes.nats-tls", Boolean.class, false));
  }

  @Bean
  WorkspaceAdmin workspaceAdmin(WorkspaceService service, WorkspaceInputs inputs) {
    return new WorkspaceAdmin(service, inputs);
  }

  @Bean
  NodeEnrollment workspaceNodeEnrollment(
      WorkspaceStore store, NodeCa ca, Clock clock, Environment env) throws Exception {
    List<ArtifactSigningKey> keys = new ArrayList<>();
    String directory = env.getProperty("auriga.workspace.artifact-keys-directory", "");
    if (!directory.isBlank())
      try (var files = Files.list(Path.of(directory))) {
        for (var file :
            files.filter(p -> p.getFileName().toString().endsWith(".pb")).sorted().toList())
          keys.add(ArtifactSigningKey.parseFrom(Files.readAllBytes(file)));
      }
    return new NodeEnrollment(
        store, ca, clock, env.getRequiredProperty("auriga.workspace.control-endpoint"), keys);
  }

  @Bean
  NodeSessions workspaceNodeSessions(NodeEnrollment enrollment, WorkspaceService service) {
    return new NodeSessions(enrollment, service);
  }

  @Bean
  NodeGrpcServer workspaceNodeServer(
      NodeEnrollment enrollment,
      NodeSessions sessions,
      Environment env,
      ObjectProvider<io.micrometer.core.instrument.MeterRegistry> metrics) {
    return new NodeGrpcServer(
        env.getProperty("auriga.workspace.node-server.host", "127.0.0.1"),
        env.getProperty("auriga.workspace.node-server.port", Integer.class, 0),
        Path.of(env.getRequiredProperty("auriga.workspace.tls-certificate-file")).toFile(),
        Path.of(env.getRequiredProperty("auriga.workspace.tls-private-key-file")).toFile(),
        enrollment,
        sessions,
        metrics.getIfAvailable(io.micrometer.core.instrument.simple.SimpleMeterRegistry::new));
  }

  @Bean
  ConnectManifest workspaceConnectManifest(Environment env) throws Exception {
    String file = env.getProperty("auriga.workspace.connect-manifest-file", "");
    return file.isBlank()
        ? new ConnectManifest()
        : WorkspaceService.JSON.readValue(Files.readAllBytes(Path.of(file)), ConnectManifest.class);
  }

  @Bean(destroyMethod = "shutdownNow")
  ScheduledExecutorService workspaceTicker(WorkspaceService service, NodeSessions sessions) {
    var executor =
        Executors.newSingleThreadScheduledExecutor(
            Thread.ofVirtual().name("workspace-lifecycle").factory());
    executor.scheduleWithFixedDelay(
        () -> {
          try {
            sessions.expire();
            service.tick();
            sessions.dispatch();
          } catch (RuntimeException e) {
            org.slf4j.LoggerFactory.getLogger(getClass())
                .error("Workspace reconciliation failed", e);
          }
        },
        5,
        5,
        TimeUnit.SECONDS);
    return executor;
  }

  @Bean
  @Order(3)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain workspaceApiChain(
      HttpSecurity http,
      JwtDecoder decoder,
      ProblemSecurityHandlers handlers,
      auriga.common.autoconfigure.AurigaProperties properties,
      ObjectProvider<RequestMappingHandlerMapping> mappings)
      throws Exception {
    http.securityMatcher("/api/workspace/**");
    return AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        properties.getSecurity().getPublicPaths(),
        EndpointAuthorizationCheck.publicEndpoints(mappings));
  }

  @Bean
  @Order(2)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain workspaceInstanceChain(HttpSecurity http) throws Exception {
    http.securityMatcher("/api/workspace/v1/instance/self", "/api/workspace/v1/instance/heartbeat")
        .authorizeHttpRequests(a -> a.anyRequest().permitAll())
        .csrf(AbstractHttpConfigurer::disable)
        .sessionManagement(s -> s.sessionCreationPolicy(SessionCreationPolicy.STATELESS));
    return http.build();
  }
}
