package auriga.iam;

import auriga.common.audit.AuditLogger;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.*;
import java.net.URI;
import java.nio.file.Path;
import java.time.*;
import java.util.*;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.boot.context.properties.bind.*;
import org.springframework.context.annotation.*;
import org.springframework.core.annotation.Order;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.web.SecurityFilterChain;

/** Explicit issuer + encryption key required; an unconfigured skeleton exposes no IAM endpoints. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(prefix = "auriga.iam", name = "issuer")
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
public class IamRuntimeConfiguration {
  @Bean
  auriga.common.security.AccessTokenSettings iamLocalTokenSettings(
      auriga.contracts.deploymentconfig.DeploymentAccess access,
      auriga.common.autoconfigure.AurigaProperties properties,
      Clock clock) {
    var jwt = properties.getSecurity().getJwt();
    if (jwt.getIssuer() != null && !access.issuer().equals(jwt.getIssuer()))
      throw new IllegalStateException("IAM token verification issuer must equal deployment issuer");
    // The URI is metadata only: both decoders below receive a local, public-key-only source.
    return new auriga.common.security.AccessTokenSettings(
        access.issuer(),
        URI.create(access.issuer() + "/oauth2/jwks"),
        jwt.getAudience(),
        jwt.getClockSkew(),
        jwt.getJwksCacheTtl(),
        jwt.getJwksMinRefreshInterval(),
        clock,
        access.issuer().startsWith("http://"));
  }

  @Bean
  com.nimbusds.jose.jwk.source.JWKSource<com.nimbusds.jose.proc.SecurityContext> iamLocalJwkSource(
      SigningKeys keys, auriga.common.security.AccessTokenSettings settings) {
    return com.nimbusds.jose.jwk.source.JWKSourceBuilder
        .<com.nimbusds.jose.proc.SecurityContext>create(
            new com.nimbusds.jose.jwk.source.JWKSetSource<>() {
              @Override
              public com.nimbusds.jose.jwk.JWKSet getJWKSet(
                  com.nimbusds.jose.jwk.source.JWKSetCacheRefreshEvaluator refresh,
                  long now,
                  com.nimbusds.jose.proc.SecurityContext context)
                  throws com.nimbusds.jose.KeySourceException {
                try {
                  return com.nimbusds.jose.jwk.JWKSet.parse(keys.jwks().toString())
                      .toPublicJWKSet();
                } catch (java.text.ParseException | RuntimeException e) {
                  throw new com.nimbusds.jose.KeySourceException(
                      "Local IAM public keys unavailable", e);
                }
              }

              @Override
              public void close() {}
            })
        .cache(
            settings.jwksCacheTtl().toMillis(),
            com.nimbusds.jose.jwk.source.JWKSourceBuilder.DEFAULT_CACHE_REFRESH_TIMEOUT)
        .refreshAheadCache(false)
        .rateLimited(Math.max(1, settings.jwksMinRefreshInterval().toMillis()))
        .retrying(true)
        .build();
  }

  @Bean
  org.springframework.security.oauth2.jwt.JwtDecoder iamAccessTokenDecoder(
      auriga.common.security.AccessTokenSettings settings,
      com.nimbusds.jose.jwk.source.JWKSource<com.nimbusds.jose.proc.SecurityContext> source) {
    return auriga.common.security.AccessTokenDecoders.create(settings, source);
  }

  @Bean
  auriga.common.security.InternalTokenVerifier iamInternalTokenVerifier(
      auriga.common.security.AccessTokenSettings settings,
      com.nimbusds.jose.jwk.source.JWKSource<com.nimbusds.jose.proc.SecurityContext> source) {
    return new auriga.common.security.InternalTokenVerifier(
        new auriga.common.security.AccessTokenVerifier(
            auriga.common.security.AccessTokenDecoders.createInternal(settings, source)));
  }

  @Bean
  ContractSchemas iamSchemas() {
    return new ContractSchemas();
  }

  @Bean
  Secrets iamSecrets(Environment env) {
    return new Secrets(env.getRequiredProperty("auriga.iam.encryption-key"));
  }

  @Bean
  @ConditionalOnMissingBean(IamStore.class)
  IamStore iamStore(
      org.springframework.beans.factory.ListableBeanFactory beans,
      TenantTransactionManager manager) {
    javax.sql.DataSource source =
        beans.containsBean("iamDataSource")
            ? beans.getBean("iamDataSource", javax.sql.DataSource.class)
            : beans.getBean(javax.sql.DataSource.class);
    return new IamStore(new JdbcTemplate(source), manager);
  }

  @Bean
  SigningKeys iamSigningKeys(IamStore store, Secrets secrets, Clock clock, Environment env) {
    return new SigningKeys(
        store,
        secrets,
        clock,
        Duration.ofSeconds(
            env.getProperty("auriga.iam.jwks-propagation-seconds", Long.class, 300L)));
  }

  @Bean
  @ConditionalOnMissingBean(PermissionResolver.class)
  PermissionResolver iamPermissionResolver(Environment env) {
    Map<String, String> configured =
        Binder.get(env)
            .bind("auriga.iam.permissions", Bindable.mapOf(String.class, String.class))
            .orElse(Map.of());
    return (tenant, subject, context) -> {
      String value = configured.getOrDefault(tenant + "/" + subject, "");
      return value.isBlank() ? Set.of() : Set.copyOf(List.of(value.split(" *, *")));
    };
  }

  @Bean
  TokenService iamTokens(
      SigningKeys keys,
      ContractSchemas schemas,
      PermissionResolver permissions,
      Clock clock,
      auriga.contracts.deploymentconfig.DeploymentAccess access,
      Environment env) {
    if (!access.issuer().equals(env.getRequiredProperty("auriga.iam.issuer")))
      throw new IllegalStateException("IAM issuer must equal deployment portal Origin");
    return new TokenService(
        keys,
        schemas,
        permissions,
        clock,
        env.getRequiredProperty("auriga.iam.issuer"),
        Binder.get(env)
            .bind("auriga.iam.internal-secret-hashes", Bindable.mapOf(String.class, String.class))
            .orElse(Map.of()));
  }

  @Bean
  @ConditionalOnMissingBean(SessionKv.class)
  SessionKv iamSessionKv(
      IamStore store,
      ContractSchemas schemas,
      Clock clock,
      org.springframework.beans.factory.ObjectProvider<io.nats.client.Connection> nats,
      Environment env) {
    SessionKv postgres = new PostgresSessionKv(store, schemas, clock);
    // Check flag before resolving Connection — getIfAvailable() would eagerly start NATS.
    if (!env.getProperty("auriga.iam.session-kv-nats", Boolean.class, true)) {
      return postgres;
    }
    io.nats.client.Connection connection = nats.getIfAvailable();
    if (connection == null) {
      return postgres;
    }
    return new CompositeSessionKv(store, postgres, new NatsSessionKv(connection, schemas), clock);
  }

  @Bean
  SessionManager iamSessions(
      IamStore store,
      SessionKv kv,
      TokenService tokens,
      Clock clock,
      @org.springframework.beans.factory.annotation.Qualifier("iamEventPublisher")
          org.springframework.beans.factory.ObjectProvider<EventPublisher> iamEvents,
      org.springframework.beans.factory.ObjectProvider<EventPublisher> events,
      AuditLogger audit) {
    return new SessionManager(
        store, kv, tokens, clock, iamEvents.getIfAvailable(events::getObject), audit);
  }

  @Bean
  IamGrpcSessions iamGrpcSessions(SessionManager sessions, Environment env) {
    if (env.getProperty("auriga.grpc.server.enabled", Boolean.class, false)) {
      String host = env.getProperty("auriga.grpc.server.host", "127.0.0.1");
      boolean loopback = Set.of("127.0.0.1", "::1", "0:0:0:0:0:0:0:1").contains(host);
      if (!loopback
          && (!env.containsProperty("auriga.grpc.server.tls-cert-chain")
              || !env.containsProperty("auriga.grpc.server.tls-private-key")
              || !env.containsProperty("auriga.grpc.server.tls-trust-cert-collection")))
        throw new IllegalStateException("IAM non-loopback gRPC requires mTLS");
    }
    return new IamGrpcSessions(sessions);
  }

  @Bean
  IamGrpcDevices iamGrpcDevices(
      IamStore store,
      SessionManager sessions,
      TokenService tokens,
      IamGrpcSessions transportGuard) {
    return new IamGrpcDevices(new DeviceAccessAuthority(store, sessions, tokens));
  }

  @Bean
  NetworkAuthority iamNetworkAuthority(
      IamStore store,
      SessionManager sessions,
      TokenService tokens,
      AuditLogger audit,
      Clock clock,
      Environment env) {
    return new IamNetworkAuthority(
        store,
        new DeviceAccessAuthority(store, sessions, tokens),
        audit,
        clock,
        Set.copyOf(
            Binder.get(env)
                .bind("auriga.iam.network.server-audiences", Bindable.listOf(String.class))
                .orElse(List.of())));
  }

  @Bean
  IamAccounts iamAccounts(IamStore store) {
    return new IamAccounts(store);
  }

  @Bean
  IamInstanceAiTokens iamInstanceAiTokens(
      TokenService tokens,
      IamAccounts accounts,
      org.springframework.beans.factory.ObjectProvider<InstanceAiAuthorization> authorization) {
    return new IamInstanceAiTokens(
        tokens,
        accounts,
        authorization.getIfAvailable(
            () ->
                request -> {
                  throw new auriga.common.error.AurigaException(
                      auriga.common.error.CommonErrors.UNAVAILABLE);
                }));
  }

  @Bean
  @ConditionalOnProperty(prefix = "auriga.iam", name = "local-service-name")
  auriga.common.grpc.ServiceTokenProvider iamLocalServiceTokenProvider(
      TokenService tokens, Environment env) {
    return () ->
        tokens.issue(
            env.getRequiredProperty("auriga.iam.local-service-name"),
            env.getRequiredProperty("auriga.iam.local-service-secret"));
  }

  @Bean
  IamAccountEvents iamAccountEvents(IamStore store, SessionManager sessions) {
    return new IamAccountEvents(store, sessions);
  }

  @Bean
  EmergencyAccounts iamEmergencyAccounts(
      IamStore store, Secrets secrets, Clock clock, Environment env) {
    return new EmergencyAccounts(
        store,
        secrets,
        clock,
        env.getProperty("auriga.iam.emergency-remote-enabled", Boolean.class, false));
  }

  @Bean
  TeamAccounts iamTeamAccounts(
      IamStore store, Secrets secrets, Clock clock, EventPublisher events) {
    return new TeamAccounts(store, secrets, clock, events);
  }

  @Bean
  TeamPasskeys iamTeamPasskeys(
      IamStore store, Secrets secrets, Clock clock, EventPublisher events) {
    return new TeamPasskeys(store, secrets, clock, events);
  }

  @Bean
  QrChallenges iamQrChallenges(IamStore store, Clock clock) {
    return new QrChallenges(store, clock);
  }

  @Bean
  IdentityConnector teamAccountConnector(
      TeamAccounts accounts, TeamPasskeys passkeys, QrChallenges qr, TokenService tokens) {
    return new TeamAccountConnector(accounts, passkeys, qr, tokens.issuer());
  }

  @Bean
  IdentityConnector oidcConnector(
      @org.springframework.beans.factory.annotation.Value(
              "${auriga.iam.oidc.allow-local-http:false}")
          boolean allowLocalHttp,
      Clock clock) {
    return new OidcConnector(allowLocalHttp, clock);
  }

  @Bean
  @ConditionalOnMissingBean(IdentityPolicyHooks.class)
  IdentityPolicyHooks iamIdentityPolicyHooks(TeamAccounts accounts) {
    return new IdentityPolicyHooks() {
      @Override
      public boolean verifyMfa(UUID account, tools.jackson.databind.JsonNode input) {
        return accounts.verifyMfaInput(account, input);
      }
    };
  }

  public record RemoteRegistration(URI url, List<String> sharedKeys) {}

  @Bean(destroyMethod = "close")
  ConnectorRegistry iamConnectors(
      ContractSchemas schemas,
      Environment env,
      Clock clock,
      org.springframework.beans.factory.ObjectProvider<IdentityConnector> builtins)
      throws java.io.IOException {
    ConnectorRegistry registry = new ConnectorRegistry(schemas, List.of(new EmergencyConnector()));
    builtins.orderedStream().forEach(registry::register);
    registry.load(Path.of(env.getProperty("auriga.iam.plugins-directory", "plugins/identity")));
    for (RemoteRegistration remote :
        Binder.get(env)
            .bind("auriga.iam.remote-connectors", Bindable.listOf(RemoteRegistration.class))
            .orElse(List.of()))
      registry.register(
          new RemoteConnector(
              remote.url(),
              remote.sharedKeys().stream().map(k -> Base64.getDecoder().decode(k)).toList(),
              clock));
    return registry;
  }

  @Bean
  LoginManager iamLogin(
      IamStore store,
      Secrets secrets,
      ContractSchemas schemas,
      ConnectorRegistry registry,
      EmergencyAccounts emergency,
      TeamAccounts teamAccounts,
      SessionManager sessions,
      IdentityPolicyHooks hooks,
      @org.springframework.beans.factory.annotation.Qualifier("iamEventPublisher")
          org.springframework.beans.factory.ObjectProvider<EventPublisher> iamEvents,
      org.springframework.beans.factory.ObjectProvider<EventPublisher> events,
      AuditLogger audit,
      Clock clock,
      TokenService tokens,
      OAuthProvider nativeDevices) {
    var manager =
        new LoginManager(
            store,
            secrets,
            schemas,
            registry,
            emergency,
            teamAccounts,
            sessions,
            hooks,
            iamEvents.getIfAvailable(events::getObject),
            audit,
            clock,
            tokens.issuer());
    manager.nativeDevices(nativeDevices);
    return manager;
  }

  @Bean
  ProviderManager iamProviders(
      IamStore store,
      ConnectorRegistry registry,
      Secrets secrets,
      AuditLogger audit,
      Clock clock,
      TokenService tokens) {
    return new ProviderManager(store, registry, secrets, audit, clock, tokens.issuer());
  }

  @Bean
  OAuthProvider iamOAuth(
      IamStore store, SessionManager sessions, TokenService tokens, Clock clock, Environment env) {
    return new OAuthProvider(
        store,
        sessions,
        tokens,
        clock,
        Binder.get(env)
            .bind("auriga.iam.clients", Bindable.listOf(OAuthClient.class))
            .orElse(List.of()));
  }

  @Bean
  auriga.contracts.deploymentconfig.DeploymentAccess iamDeploymentAccess(Environment env)
      throws java.io.IOException {
    return auriga.contracts.deploymentconfig.DeploymentAccess.load(
        java.nio.file.Files.readAllBytes(
            Path.of(env.getRequiredProperty("auriga.iam.deployment-access-file"))));
  }

  @Bean
  static org.springframework.beans.factory.config.BeanPostProcessor iamHopServerGuard(
      Environment env) {
    return new IamHopServerGuard(
        env.getProperty("auriga.iam.edge-hop.allow-loopback-http", Boolean.class, false));
  }

  @Bean
  RequestIdentity iamRequestIdentity(
      Environment env, auriga.contracts.deploymentconfig.DeploymentAccess access) {
    boolean loopbackHttp =
        env.getProperty("auriga.iam.edge-hop.allow-loopback-http", Boolean.class, false);
    if (!"none".equalsIgnoreCase(env.getProperty("server.forward-headers-strategy", "none")))
      throw new IllegalStateException("IAM edge hop requires raw transport peer and TLS state");
    if (!loopbackHttp
        && (!env.getProperty("server.ssl.enabled", Boolean.class, false)
            || !"need".equalsIgnoreCase(env.getProperty("server.ssl.client-auth", ""))
            || (!env.containsProperty("server.ssl.trust-store")
                && !env.containsProperty("server.ssl.trust-certificate"))))
      throw new IllegalStateException(
          "IAM HTTP edge hop requires TLS and client-auth=need with a client CA");
    var peers =
        Set.copyOf(
            Binder.get(env)
                .bind("auriga.iam.edge-hop.peer-addresses", Bindable.listOf(String.class))
                .orElse(List.of()));
    if (loopbackHttp
        && peers.stream().anyMatch(p -> !Set.of("127.0.0.1", "::1", "0:0:0:0:0:0:0:1").contains(p)))
      throw new IllegalStateException(
          "IAM insecure development hop must have only numeric loopback peers");
    return new RequestIdentity(
        access,
        new RequestIdentity.Hop(
            peers, env.getProperty("auriga.iam.edge-hop.dns-san"), loopbackHttp));
  }

  @Bean
  @Order(0)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain iamOAuthChain(HttpSecurity http) throws Exception {
    // Userinfo verifies third-party-audience tokens itself; common only accepts auriga tokens.
    http.securityMatcher("/oauth2/**", "/.well-known/openid-configuration", "/userinfo")
        .authorizeHttpRequests(a -> a.anyRequest().permitAll())
        .csrf(AbstractHttpConfigurer::disable)
        .requestCache(AbstractHttpConfigurer::disable)
        .sessionManagement(
            s ->
                s.sessionCreationPolicy(
                    org.springframework.security.config.http.SessionCreationPolicy.STATELESS));
    return http.build();
  }

  @Bean
  @Order(1)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain iamApiChain(
      HttpSecurity http,
      org.springframework.security.oauth2.jwt.JwtDecoder decoder,
      auriga.common.security.ProblemSecurityHandlers handlers,
      auriga.common.autoconfigure.AurigaProperties properties,
      org.springframework.beans.factory.ObjectProvider<
              org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping>
          mappings)
      throws Exception {
    // Scope to IAM HTTP surface so combined assemblies can also register orgApiChain.
    http.securityMatcher("/api/iam/**");
    return auriga.common.security.AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        properties.getSecurity().getPublicPaths(),
        auriga.common.security.EndpointAuthorizationCheck.publicEndpoints(mappings));
  }
}
