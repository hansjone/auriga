package auriga.common.autoconfigure;

import auriga.common.security.AurigaWebSecurity;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import org.springframework.boot.context.properties.ConfigurationProperties;

/** {@code auriga.*} settings consumed by the common auto-configuration. */
@ConfigurationProperties("auriga")
public class AurigaProperties {
  private final Service service = new Service();
  private final Errors errors = new Errors();
  private final Security security = new Security();
  private final Persistence persistence = new Persistence();
  private final Events events = new Events();
  private final Grpc grpc = new Grpc();

  public Service getService() {
    return service;
  }

  public Errors getErrors() {
    return errors;
  }

  public Security getSecurity() {
    return security;
  }

  public Persistence getPersistence() {
    return persistence;
  }

  public Events getEvents() {
    return events;
  }

  public Grpc getGrpc() {
    return grpc;
  }

  /** The service this process runs (single-service deployments). */
  public static class Service {
    /** e.g. {@code org}; used for event source {@code auriga/org} and audit subjects. */
    private String name;

    /** Database schema owned by the service; enables common migrations, outbox and inbox. */
    private String schema;

    public String getName() {
      return name;
    }

    public void setName(String name) {
      this.name = name;
    }

    public String getSchema() {
      return schema;
    }

    public void setSchema(String schema) {
      this.schema = schema;
    }
  }

  public static class Errors {
    private Locale defaultLocale = Locale.ENGLISH;

    /** Extra message bundles searched before the common one, e.g. {@code messages/org-errors}. */
    private List<String> messageBundles = new ArrayList<>();

    public Locale getDefaultLocale() {
      return defaultLocale;
    }

    public void setDefaultLocale(Locale defaultLocale) {
      this.defaultLocale = defaultLocale;
    }

    public List<String> getMessageBundles() {
      return messageBundles;
    }

    public void setMessageBundles(List<String> messageBundles) {
      this.messageBundles = messageBundles;
    }
  }

  public static class Security {
    private final Jwt jwt = new Jwt();
    private List<String> publicPaths = new ArrayList<>(AurigaWebSecurity.DEFAULT_PUBLIC_PATHS);

    public Jwt getJwt() {
      return jwt;
    }

    public List<String> getPublicPaths() {
      return publicPaths;
    }

    public void setPublicPaths(List<String> publicPaths) {
      this.publicPaths = publicPaths;
    }
  }

  public static class Jwt {
    /** Expected {@code iss}. Without issuer and jwk-set-uri every token is rejected. */
    private String issuer;

    private String jwkSetUri;
    private String audience = "auriga";
    private Duration clockSkew = Duration.ofSeconds(60);
    private Duration jwksCacheTtl = Duration.ofMinutes(5);
    private Duration jwksMinRefreshInterval = Duration.ofSeconds(30);

    /** Allow a plain http JWKS URI on a non-loopback host (isolated networks only). */
    private boolean allowInsecureJwksUri;

    public boolean isAllowInsecureJwksUri() {
      return allowInsecureJwksUri;
    }

    public void setAllowInsecureJwksUri(boolean allowInsecureJwksUri) {
      this.allowInsecureJwksUri = allowInsecureJwksUri;
    }

    public String getIssuer() {
      return issuer;
    }

    public void setIssuer(String issuer) {
      this.issuer = issuer;
    }

    public String getJwkSetUri() {
      return jwkSetUri;
    }

    public void setJwkSetUri(String jwkSetUri) {
      this.jwkSetUri = jwkSetUri;
    }

    public String getAudience() {
      return audience;
    }

    public void setAudience(String audience) {
      this.audience = audience;
    }

    public Duration getClockSkew() {
      return clockSkew;
    }

    public void setClockSkew(Duration clockSkew) {
      this.clockSkew = clockSkew;
    }

    public Duration getJwksCacheTtl() {
      return jwksCacheTtl;
    }

    public void setJwksCacheTtl(Duration jwksCacheTtl) {
      this.jwksCacheTtl = jwksCacheTtl;
    }

    public Duration getJwksMinRefreshInterval() {
      return jwksMinRefreshInterval;
    }

    public void setJwksMinRefreshInterval(Duration jwksMinRefreshInterval) {
      this.jwksMinRefreshInterval = jwksMinRefreshInterval;
    }
  }

  public static class Persistence {
    /** Business tables without tenant_id (MyBatis-Plus adds no tenant condition). */
    private List<String> nonTenantTables = new ArrayList<>();

    public List<String> getNonTenantTables() {
      return nonTenantTables;
    }

    public void setNonTenantTables(List<String> nonTenantTables) {
      this.nonTenantTables = nonTenantTables;
    }
  }

  public static class Events {
    /** Validate produced events against contracts/events (on in dev/test; may be off in prod). */
    private boolean validateSchemas = true;

    /** NATS server URL; without it events stay in the outbox (nothing is forwarded/consumed). */
    private String natsUrl;

    private boolean ensureStreams = true;
    private Duration duplicateWindow = Duration.ofMinutes(2);
    private int batchSize = 100;
    private Duration pollInterval = Duration.ofMillis(500);
    private Duration baseBackoff = Duration.ofSeconds(1);
    private Duration maxBackoff = Duration.ofMinutes(5);
    private Duration retention = Duration.ofDays(7);
    private int maxDeliveries = 5;

    /**
     * Extra or overriding legitimate producers, {@code type} or {@code prefix.*} to service names,
     * e.g. {@code auriga.events.producers[widget.created]=example}. Defaults follow the contract.
     */
    private Map<String, List<String>> producers = new HashMap<>();

    /** NATS credentials file (JWT + nkey, {@code .creds}). */
    private String natsCredsFile;

    /** NATS nkey seed file (nkey authentication without JWT). */
    private String natsNkeySeedFile;

    /** Use TLS to NATS (JVM trust store); also implied by a {@code tls://} URL. */
    private boolean natsTls;

    public Map<String, List<String>> getProducers() {
      return producers;
    }

    public void setProducers(Map<String, List<String>> producers) {
      this.producers = producers;
    }

    public String getNatsCredsFile() {
      return natsCredsFile;
    }

    public void setNatsCredsFile(String natsCredsFile) {
      this.natsCredsFile = natsCredsFile;
    }

    public String getNatsNkeySeedFile() {
      return natsNkeySeedFile;
    }

    public void setNatsNkeySeedFile(String natsNkeySeedFile) {
      this.natsNkeySeedFile = natsNkeySeedFile;
    }

    public boolean isNatsTls() {
      return natsTls;
    }

    public void setNatsTls(boolean natsTls) {
      this.natsTls = natsTls;
    }

    private Duration ackWait = Duration.ofSeconds(30);

    public boolean isValidateSchemas() {
      return validateSchemas;
    }

    public void setValidateSchemas(boolean validateSchemas) {
      this.validateSchemas = validateSchemas;
    }

    public String getNatsUrl() {
      return natsUrl;
    }

    public void setNatsUrl(String natsUrl) {
      this.natsUrl = natsUrl;
    }

    public boolean isEnsureStreams() {
      return ensureStreams;
    }

    public void setEnsureStreams(boolean ensureStreams) {
      this.ensureStreams = ensureStreams;
    }

    public Duration getDuplicateWindow() {
      return duplicateWindow;
    }

    public void setDuplicateWindow(Duration duplicateWindow) {
      this.duplicateWindow = duplicateWindow;
    }

    public int getBatchSize() {
      return batchSize;
    }

    public void setBatchSize(int batchSize) {
      this.batchSize = batchSize;
    }

    public Duration getPollInterval() {
      return pollInterval;
    }

    public void setPollInterval(Duration pollInterval) {
      this.pollInterval = pollInterval;
    }

    public Duration getBaseBackoff() {
      return baseBackoff;
    }

    public void setBaseBackoff(Duration baseBackoff) {
      this.baseBackoff = baseBackoff;
    }

    public Duration getMaxBackoff() {
      return maxBackoff;
    }

    public void setMaxBackoff(Duration maxBackoff) {
      this.maxBackoff = maxBackoff;
    }

    public Duration getRetention() {
      return retention;
    }

    public void setRetention(Duration retention) {
      this.retention = retention;
    }

    public int getMaxDeliveries() {
      return maxDeliveries;
    }

    public void setMaxDeliveries(int maxDeliveries) {
      this.maxDeliveries = maxDeliveries;
    }

    public Duration getAckWait() {
      return ackWait;
    }

    public void setAckWait(Duration ackWait) {
      this.ackWait = ackWait;
    }
  }

  public static class Grpc {
    private final Server server = new Server();
    private Duration clientTimeout = Duration.ofSeconds(5);

    public Server getServer() {
      return server;
    }

    public Duration getClientTimeout() {
      return clientTimeout;
    }

    public void setClientTimeout(Duration clientTimeout) {
      this.clientTimeout = clientTimeout;
    }
  }

  public static class Server {
    private boolean enabled;
    private String host = "127.0.0.1";

    /** Subjects ({@code svc:<name>}) allowed to call with {@code auriga-tenant-id}. */
    private List<String> tenantDelegates = new ArrayList<>();

    /** Serve plaintext on a non-loopback address; never in production. */
    private boolean allowInsecureNonLoopback;

    /** PEM paths; with trust-cert-collection, client certificates are required (mTLS). */
    private String tlsCertChain;

    private String tlsPrivateKey;
    private String tlsTrustCertCollection;

    public List<String> getTenantDelegates() {
      return tenantDelegates;
    }

    public void setTenantDelegates(List<String> tenantDelegates) {
      this.tenantDelegates = tenantDelegates;
    }

    public boolean isAllowInsecureNonLoopback() {
      return allowInsecureNonLoopback;
    }

    public void setAllowInsecureNonLoopback(boolean allowInsecureNonLoopback) {
      this.allowInsecureNonLoopback = allowInsecureNonLoopback;
    }

    public String getTlsCertChain() {
      return tlsCertChain;
    }

    public void setTlsCertChain(String tlsCertChain) {
      this.tlsCertChain = tlsCertChain;
    }

    public String getTlsPrivateKey() {
      return tlsPrivateKey;
    }

    public void setTlsPrivateKey(String tlsPrivateKey) {
      this.tlsPrivateKey = tlsPrivateKey;
    }

    public String getTlsTrustCertCollection() {
      return tlsTrustCertCollection;
    }

    public void setTlsTrustCertCollection(String tlsTrustCertCollection) {
      this.tlsTrustCertCollection = tlsTrustCertCollection;
    }

    private int port = 9090;
    private List<String> publicMethods = new ArrayList<>(List.of("grpc.health.v1.Health/Check"));

    public boolean isEnabled() {
      return enabled;
    }

    public void setEnabled(boolean enabled) {
      this.enabled = enabled;
    }

    public String getHost() {
      return host;
    }

    public void setHost(String host) {
      this.host = host;
    }

    public int getPort() {
      return port;
    }

    public void setPort(int port) {
      this.port = port;
    }

    public List<String> getPublicMethods() {
      return publicMethods;
    }

    public void setPublicMethods(List<String> publicMethods) {
      this.publicMethods = publicMethods;
    }
  }
}
