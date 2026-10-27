package auriga.iam;

import auriga.collab.network.keys.NetworkKeysController;
import auriga.common.security.AccessTokenDecoders;
import org.springframework.boot.WebApplicationType;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.boot.builder.SpringApplicationBuilder;
import org.springframework.boot.web.server.context.WebServerApplicationContext;
import org.springframework.context.ConfigurableApplicationContext;
import org.springframework.context.annotation.Bean;
import org.springframework.security.oauth2.jwt.JwtDecoder;

/** No component scan or alternate authentication: uses common's production security auto-config. */
final class NetworkSourceJavaWeb implements AutoCloseable {
  private final NetworkSourceJavaHost host;
  private ConfigurableApplicationContext context;

  NetworkSourceJavaWeb(NetworkSourceJavaHost host) {
    this.host = host;
  }

  void start() {
    context =
        new SpringApplicationBuilder(App.class)
            .web(WebApplicationType.SERVLET)
            .registerShutdownHook(false)
            .initializers(c -> c.getBeanFactory().registerSingleton("bridgeHost", host))
            .properties(
                "spring.config.name=network-source-java-isolated",
                "spring.main.banner-mode=off",
                "server.address=127.0.0.1",
                "server.port=0",
                "server.ssl.enabled=true",
                "server.ssl.key-store=" + host.tls.store.toUri(),
                "server.ssl.key-store-type=PKCS12",
                "server.ssl.key-store-password=" + host.tls.password,
                "server.ssl.key-alias=server",
                "server.tomcat.basedir=" + host.directory.resolve("tomcat"),
                "auriga.grpc.server.enabled=false",
                "auriga.logging.json=false",
                "auriga.iam.issuer=https://localhost",
                "spring.autoconfigure.exclude=org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration,org.springframework.boot.flyway.autoconfigure.FlywayAutoConfiguration,auriga.common.autoconfigure.AurigaPersistenceAutoConfiguration,auriga.common.autoconfigure.AurigaEventsAutoConfiguration")
            .run();
  }

  int port() {
    return ((WebServerApplicationContext) context).getWebServer().getPort();
  }

  @Override
  public void close() {
    if (context != null) context.close();
  }

  @EnableAutoConfiguration
  static class App {
    @Bean
    JwtDecoder decoder(NetworkSourceJavaHost host) {
      return AccessTokenDecoders.create(host.settings(), host.publicKeys());
    }

    @Bean
    IamNetworkClientController networkClients(NetworkSourceJavaHost host) {
      return new IamNetworkClientController(
          new IamNetworkClient(
              host.network, host.store, host.clock, "server-a", java.util.Set.of("server-a")),
          new ContractSchemas());
    }

    @Bean
    NetworkKeysController networkKeys(NetworkSourceJavaHost host) {
      return new NetworkKeysController(host.publications, host.keySettings);
    }
  }
}
