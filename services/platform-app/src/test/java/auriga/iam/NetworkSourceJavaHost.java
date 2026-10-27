package auriga.iam;

import auriga.collab.app.*;
import auriga.collab.grpc.*;
import auriga.collab.network.keys.*;
import auriga.collab.network.local.LocalRegistry;
import auriga.common.audit.OutboxAuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.events.*;
import auriga.common.grpc.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.org.*;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.source.ImmutableJWKSet;
import io.grpc.*;
import java.io.*;
import java.net.*;
import java.net.http.*;
import java.nio.file.*;
import java.security.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.TimeUnit;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/**
 * Isolated T3a child process, explicitly assembled from production services. No source/chain/
 * receipt/connection is seeded. EOF or "stop" closes HTTP, mailbox, IAM, pools, then own PG.
 */
public final class NetworkSourceJavaHost implements AutoCloseable {
  final UUID tenant = UuidV7.next();
  final Clock clock = Clock.systemUTC();
  final List<String> closed = new ArrayList<>();
  final Map<String, String> secrets =
      Map.of("collab", Secrets.random(), "ai-gateway", Secrets.random());
  final List<Rpc> channels = new ArrayList<>();
  final Set<Integer> ports = new LinkedHashSet<>();
  Path directory;
  NetworkSourceJavaTls tls;
  NetworkSourceJavaDatabase database;
  NetworkSourceJavaWeb web;
  IamStore store;
  OrgService org;
  OrgPermissionResolver permissions;
  TokenService tokens;
  SigningKeys keys;
  SessionManager sessions;
  OAuthProvider oauth;
  AccessTokenVerifier verifier;
  NetworkSigningKeys signing;
  NetworkKeyOperations publications;
  NetworkKeySettings keySettings;
  ObjectNode protocolSigning;
  NetworkSourceJavaFaults faults = new NetworkSourceJavaFaults(false, event -> {});
  IamNetworkAuthority network;
  GrpcServer iam, mailbox;
  HttpClient http;
  boolean started, stopped;
  long clientId = 40;

  record Device(
      UUID account, UUID binding, String session, String device, String access, String refresh) {}

  record Rpc(ManagedChannel channel) implements AutoCloseable {
    @Override
    public void close() throws Exception {
      channel.shutdown();
      if (!channel.awaitTermination(5, TimeUnit.SECONDS)) {
        channel.shutdownNow();
        if (!channel.awaitTermination(5, TimeUnit.SECONDS))
          throw new IOException("gRPC channel survived close");
      }
    }
  }

  void start() throws Exception {
    if (started || stopped) throw new IllegalStateException("Host is single use");
    started = true;
    try {
      directory = privateDirectory();
      NetworkSourceJavaPrivateFiles.requirePrivate(directory);
      tls = new NetworkSourceJavaTls(directory);
      database = new NetworkSourceJavaDatabase(directory);
      database.start();
      ports.add(database.pg.getPort());
      var iamData = faults.dataSource(database.runtime.get("iam"));
      store = new IamStore(new JdbcTemplate(iamData), new TenantTransactionManager(iamData));
      var orgData = database.runtime.get("org");
      var orgJdbc = new JdbcTemplate(orgData);
      org =
          new OrgService(
              new OrgStore(orgJdbc, new TenantTransactionManager(orgData)),
              events(orgJdbc, "org"),
              clock);
      permissions = new OrgPermissionResolver(org);
      org.store()
          .tenant(
              tenant,
              () -> {
                orgJdbc.update(
                    "INSERT INTO org.tenant(id,tenant_id,slug,display_name) VALUES(?,?,'bridge','Example bridge')",
                    tenant,
                    tenant);
                return null;
              });
      security();
      var schemas = new ContractSchemas();
      var audit = new OutboxAuditLogger(events(new JdbcTemplate(iamData), "iam"));
      sessions =
          new SessionManager(
              store,
              new PostgresSessionKv(store, schemas, clock),
              tokens,
              clock,
              events(new JdbcTemplate(iamData), "iam"),
              audit);
      oauth =
          new OAuthProvider(
              store,
              sessions,
              tokens,
              clock,
              List.of(
                  new OAuthClient(
                      "bridge-device",
                      tenant,
                      null,
                      Set.of(),
                      Set.of("auriga.connect"),
                      Set.of(OAuthProvider.DEVICE, "refresh_token"),
                      true)));
      var devices = new DeviceAccessAuthority(store, sessions, tokens);
      network = new IamNetworkAuthority(store, devices, audit, clock, Set.of("server-a"));
      var sources =
          new IamNetworkSourceAuthority(
              store,
              network,
              devices,
              permissions,
              clock,
              new IamNetworkSourceAuthority.Policy(180, 30));
      iam =
          server(
              List.of(
                  new IamGrpcNetwork(network),
                  new IamGrpcNetworkAuthority(network, clock, sources),
                  faults.service(sources)),
              Set.of("svc:collab", "svc:ai-gateway"));
      var collabData = database.runtime.get("collab");
      var jdbc = new JdbcTemplate(collabData);
      var tx = new TenantTransactionManager(collabData);
      var client = new NetworkAuthorityClient(rpc("collab").channel(), Duration.ofSeconds(5));
      var authority = new NetworkSourceGrpcAuthority(client, tx);
      // Only initial realm configuration is seeded; no node or connection is seeded.
      String realm =
          store.tenant(
              tenant,
              () -> {
                store
                    .networkJdbc()
                    .update(
                        "INSERT INTO iam.network_realm(tenant_id,realm) VALUES (?,nextval('iam.network_realm_number')::numeric+9223372036854775809)",
                        tenant);
                return store
                    .networkJdbc()
                    .queryForObject(
                        "SELECT realm::text FROM iam.network_realm WHERE tenant_id=?",
                        String.class,
                        tenant);
              });
      signing =
          new NetworkSigningKeys(
              clock,
              1,
              directory.resolve("network-key-state.json"),
              (keys, signature) ->
                  publications.signApproved(keys.publicKeysJson(realm), signature));
      installNetworkKeys(realm);
      var locals = new LocalRegistry(jdbc, tx, authority, client, clock);
      var wire =
          new NetworkMailboxWireService(
              new NetworkMailboxService(jdbc, tx, authority, signing, clock, client),
              locals,
              jdbc,
              tx,
              authority,
              signing,
              clock);
      mailbox = server(List.of(new NetworkMailboxGrpcService(wire)), Set.of("svc:ai-gateway"));
      web = new NetworkSourceJavaWeb(this);
      web.start();
      ports.add(web.port());
      http =
          HttpClient.newBuilder()
              .sslContext(tls.clientContext())
              .connectTimeout(Duration.ofSeconds(5))
              .build();
    } catch (Exception | Error failure) {
      try {
        close();
      } catch (Exception cleanup) {
        failure.addSuppressed(cleanup);
      }
      throw failure;
    }
  }

  private EventPublisher events(JdbcTemplate jdbc, String schema) {
    return new OutboxEventPublisher(
        jdbc, schema, schema, new EventSchemas(), JsonMapper.builder().build(), clock, () -> {});
  }

  private void security() {
    byte[] encryption = new byte[32];
    new SecureRandom().nextBytes(encryption);
    // Only SigningKeys' initial publication clock is 31 seconds old. TLS, token issuance,
    // verification, sessions and business services always use real system UTC.
    var bootstrap =
        new Clock() {
          boolean live;

          public Instant instant() {
            return live ? clock.instant() : clock.instant().minusSeconds(31);
          }

          public ZoneId getZone() {
            return ZoneOffset.UTC;
          }

          public Clock withZone(ZoneId zone) {
            return this;
          }
        };
    keys =
        new SigningKeys(
            store,
            new Secrets(Base64.getEncoder().encodeToString(encryption)),
            bootstrap,
            Duration.ofSeconds(30));
    Arrays.fill(encryption, (byte) 0);
    String kid = keys.publishNext();
    bootstrap.live = true;
    keys.activate(kid);
    tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            permissions,
            clock,
            "https://localhost",
            Map.of(
                "collab",
                Secrets.hash(secrets.get("collab")),
                "ai-gateway",
                Secrets.hash(secrets.get("ai-gateway"))));
    verifier =
        new AccessTokenVerifier(AccessTokenDecoders.createInternal(settings(), publicKeys()));
  }

  AccessTokenSettings settings() {
    return new AccessTokenSettings(
        tokens.issuer(),
        URI.create(tokens.issuer() + "/oauth2/jwks"),
        null,
        null,
        null,
        null,
        clock,
        false);
  }

  ImmutableJWKSet<com.nimbusds.jose.proc.SecurityContext> publicKeys() {
    try {
      return new ImmutableJWKSet<>(JWKSet.parse(keys.jwks().toString()).toPublicJWKSet());
    } catch (java.text.ParseException e) {
      throw new IllegalStateException("Invalid issuer public keys", e);
    }
  }

  String serviceToken(String service) {
    return ((auriga.iam.api.InternalTokenIssuer) tokens).issue(service, secrets.get(service));
  }

  Rpc rpc(String service) {
    var rpc =
        new Rpc(
            GrpcChannels.open(
                "127.0.0.1:" + iam.port(),
                new AurigaClientInterceptor(() -> serviceToken(service), Duration.ofSeconds(5))));
    channels.add(rpc);
    return rpc;
  }

  Rpc mailboxRpc() {
    var rpc =
        new Rpc(
            GrpcChannels.open(
                "127.0.0.1:" + mailbox.port(),
                new AurigaClientInterceptor(
                    () -> serviceToken("ai-gateway"), Duration.ofSeconds(10))));
    channels.add(rpc);
    return rpc;
  }

  private GrpcServer server(List<BindableService> services, Set<String> delegates) {
    var server =
        new GrpcServer(
            "127.0.0.1",
            0,
            services,
            new AurigaServerInterceptor(verifier, Set.of(), delegates),
            null,
            false,
            Set.of());
    server.start();
    ports.add(server.port());
    return server;
  }

  Device seed(String memberType) {
    if (!Set.of("member", "external").contains(memberType))
      throw new IllegalArgumentException("Invalid seed member type");
    UUID account = UuidV7.next(), role = UuidV7.next(), binding = UuidV7.next();
    org.store()
        .tenant(
            tenant,
            () -> {
              var jdbc = org.store().jdbc();
              org.store()
                  .insertMember(
                      new OrgStore.MemberRow(
                          account,
                          "Example member",
                          null,
                          memberType,
                          "active",
                          1,
                          clock.instant(),
                          null,
                          null));
              jdbc.update(
                  "INSERT INTO org.role(id,tenant_id,key,name_en,name_zh,permissions) VALUES(?,?,?,'Example','Example',ARRAY['auriga.connect','auriga.connect.collab.use'])",
                  role,
                  tenant,
                  "bridge-" + role);
              jdbc.update(
                  "INSERT INTO org.role_binding(id,tenant_id,role_id,subject_type,subject_id) VALUES(?,?,?,'member',?)",
                  binding,
                  tenant,
                  role,
                  account);
              return null;
            });
    String session =
        store.tenant(
            tenant,
            () -> {
              UUID provider = UuidV7.next();
              store.insert(
                  "account",
                  account.toString(),
                  Json.object("status", "active", "memberType", memberType),
                  null);
              store.insert("idp_provider", provider.toString(), Json.object(), null);
              return sessions
                  .create(
                      account, provider, "portal", true, Duration.ofHours(1), Duration.ofHours(1))
                  .session()
                  .path("sessionId")
                  .asString();
            });
    return TenantContext.call(
        tenant,
        () -> {
          var grant =
              oauth.deviceAuthorization(
                  Map.of("client_id", "bridge-device", "scope", "auriga.connect"));
          oauth.decideDevice(grant.path("user_code").asString(), session, true);
          var token =
              oauth.token(
                  Map.of(
                      "grant_type",
                      OAuthProvider.DEVICE,
                      "client_id",
                      "bridge-device",
                      "device_code",
                      grant.path("device_code").asString()));
          var access = token.path("access_token").asString();
          return new Device(
              account,
              binding,
              session,
              tokens.verify(access).path("did").asString(),
              access,
              token.path("refresh_token").asString());
        });
  }

  HttpResponse<String> http(String path, String body, String bearer) throws Exception {
    var request =
        HttpRequest.newBuilder(URI.create("https://127.0.0.1:" + web.port() + path))
            .timeout(Duration.ofSeconds(10));
    if (bearer != null) request.header("Authorization", "Bearer " + bearer);
    if (body == null) request.GET();
    else
      request
          .header("Content-Type", "application/json")
          .POST(HttpRequest.BodyPublishers.ofString(body));
    return http.send(request.build(), HttpResponse.BodyHandlers.ofString());
  }

  JsonNode register(Device device) throws Exception {
    var response = http("/api/iam/v1/network/register", "{}", device.access());
    if (response.statusCode() != 200)
      throw new AssertionError("HTTPS register status " + response.statusCode());
    var result = NetworkJson.parse(response.body());
    if (publications == null) installNetworkKeys(result.path("realm").asString());
    return result;
  }

  private void installNetworkKeys(String realm) throws Exception {
    var jdbc = new JdbcTemplate(database.runtime.get("collab"));
    var tx = new TenantTransactionManager(database.runtime.get("collab"));
    var root = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    KeyPair protocol = null;
    Instant protocolBegin = null, protocolEnd = null;
    for (var purpose : List.of("protocol_response", "business_delivery", "management_state")) {
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      var begin = clock.instant().minusSeconds(5);
      var end = clock.instant().plusSeconds(3600);
      signing.add(
          realm,
          purpose,
          "bridge-" + purpose,
          pair.getPublic(),
          pair.getPrivate(),
          begin,
          end,
          NetworkSigningKeys.State.ACTIVE);
      if (purpose.equals("protocol_response")) {
        protocol = pair;
        protocolBegin = begin;
        protocolEnd = end;
      }
    }
    var trust =
        new RootKeyPublication(
            realm,
            "bridge-root",
            root.getPublic(),
            RootKeyPublication.fingerprint(root.getPublic()),
            clock);
    publications = new NetworkKeyOperations(jdbc, tx, tenant, realm, trust, "1", clock);
    var release =
        OfflineKeySigning.sign(signing.publicKeysJson(realm), "bridge-root", root.getPrivate());
    publications.publish(release);
    exportProtocolSigner(
        Objects.requireNonNull(protocol),
        "bridge-protocol_response",
        NetworkJson.parse(signing.publicKeysJson(realm)).path("keyset_version").asString(),
        protocolBegin.truncatedTo(java.time.temporal.ChronoUnit.SECONDS),
        protocolEnd.truncatedTo(java.time.temporal.ChronoUnit.SECONDS));
    var publicFile = directory.resolve("network-root.der");
    Files.write(publicFile, root.getPublic().getEncoded());
    keySettings =
        new NetworkKeySettings(
            tenant,
            realm,
            "bridge-root",
            publicFile,
            RootKeyPublication.fingerprint(root.getPublic()),
            directory.resolve("signed.json"),
            directory.resolve("announcement.json"),
            directory.resolve("state.json"),
            "1",
            Map.of());
    Files.writeString(keySettings.signedFile(), release.signedKeyset());
    Files.writeString(keySettings.announcementFile(), release.announcement());
  }

  // The same root-approved key, never a second signer or a new approval chain.
  void exportProtocolSigner(KeyPair pair, String kid, String version, Instant begin, Instant end)
      throws IOException {
    var file = directory.resolve("network-protocol.pem");
    byte[] encoded = pair.getPrivate().getEncoded();
    try {
      NetworkSourceJavaPrivateFiles.replace(
          file,
          "-----BEGIN PRIVATE KEY-----\n"
              + Base64.getMimeEncoder(64, new byte[] {'\n'}).encodeToString(encoded)
              + "\n-----END PRIVATE KEY-----\n");
    } finally {
      Arrays.fill(encoded, (byte) 0);
    }
    NetworkSourceJavaPrivateFiles.requirePrivateFile(file);
    protocolSigning =
        Json.object(
            "privateKeyFile",
            file.toString(),
            "keyId",
            kid,
            "keysetVersion",
            version,
            "notBefore",
            begin.toString(),
            "notAfter",
            end.toString());
  }

  NetworkAuthorityConnection redeem(Device device, JsonNode registration) throws Exception {
    var response =
        http(
            "/api/iam/v1/network/tickets",
            Json.object("node", registration.path("node").asString()).toString(),
            device.access());
    if (response.statusCode() != 200)
      throw new AssertionError("HTTPS ticket status " + response.statusCode());
    var ticket = NetworkJson.parse(response.body());
    long id = ++clientId;
    var request =
        RedeemNetworkTicketRequest.newBuilder()
            .setTicket(ticket.path("ticket").asString())
            .setNode(registration.path("node").asString())
            .setDevice(device.device())
            .setServerAudience("server-a")
            .setServerId("N" + "B".repeat(55))
            .setClientId(Long.toString(id))
            .setUserNkey("U" + "A".repeat(55))
            .setIngress(ticket.path("ingress").asString())
            .setClientAccount("BRIDGE-" + id)
            .build();
    try (var rpc = rpc("ai-gateway")) {
      TenantContext.call(
          tenant,
          () ->
              NetworkGatewayServiceGrpc.newBlockingStub(rpc.channel())
                  .redeemNetworkTicket(request));
    }
    return NetworkAuthorityConnection.newBuilder()
        .setTenantId(tenant.toString())
        .setRealm(registration.path("realm").asString())
        .setNode(request.getNode())
        .setIngress(request.getIngress())
        .setServerId(request.getServerId())
        .setClientId(request.getClientId())
        .setClientAccount(request.getClientAccount())
        .build();
  }

  void revoke(Device device) {
    TenantContext.run(tenant, () -> org.deleteRoleBinding(device.binding()));
  }

  void assertRedeemFacts(NetworkAuthorityConnection connection, Device device) {
    store.tenant(
        tenant,
        () -> {
          var row =
              store
                  .networkJdbc()
                  .queryForMap(
                      "SELECT * FROM iam.network_connection_anchor WHERE tenant_id=? AND ingress_key=?",
                      tenant,
                      connection.getIngress());
          if (!Arrays.equals(connection.toByteArray(), (byte[]) row.get("connection"))
              || !device.account().equals(row.get("owner_id"))
              || !UUID.fromString(device.device()).equals(row.get("device_id"))
              || !UUID.fromString(device.session()).equals(row.get("session_id")))
            throw new AssertionError("Redeem did not persist exact V12 authority facts");
          if (store.get("network_ingress", connection.getIngress(), false) == null)
            throw new AssertionError("Redeem ingress missing");
          return null;
        });
  }

  int sourceCount() {
    return store.tenant(
        tenant,
        () ->
            store
                .networkJdbc()
                .queryForObject(
                    "SELECT count(*) FROM iam.network_source WHERE tenant_id=?",
                    Integer.class,
                    tenant));
  }

  void assertRoleIsolation() {
    database.assertIsolation();
  }

  void assertPublishedKeys(Device device) throws Exception {
    var served = http("/api/collab/v1/network/keys", null, device.access());
    var announcement = http("/api/collab/v1/network/keys/announcement", null, device.access());
    if (served.statusCode() != 200
        || announcement.statusCode() != 200
        || !served.body().equals(publications.current().signedKeyset())
        || !announcement.body().equals(publications.current().announcement()))
      throw new AssertionError("HTTPS key release differs from real publication");
  }

  ObjectNode ready() {
    var p = ProcessHandle.current();
    return Json.object(
        "stage",
        "ready",
        "https",
        "https://127.0.0.1:" + web.port(),
        "iam",
        "127.0.0.1:" + iam.port(),
        "collab",
        "127.0.0.1:" + mailbox.port(),
        "ca",
        tls.ca.toString(),
        "config",
        directory.resolve("bridge.json").toString(),
        "pid",
        p.pid(),
        "parentPid",
        p.parent().map(ProcessHandle::pid).orElse(0L),
        "created",
        p.info().startInstant().orElseThrow().toString(),
        "pgLineage",
        database.lineage(),
        "pgPid",
        database.root.pid(),
        "pgCreated",
        database.created.toString(),
        "pgRoot",
        database.data.toString(),
        "pgPort",
        database.pg.getPort());
  }

  static Path privateDirectory() throws IOException {
    return NetworkSourceJavaPrivateFiles.createDirectory();
  }

  @Override
  public synchronized void close() throws Exception {
    if (stopped) return;
    stopped = true;
    faults.close();
    Exception failure = null;
    for (var stage : List.of("https", "mailbox", "channels", "iam", "keys", "pg", "files")) {
      try {
        switch (stage) {
          case "https" -> {
            if (web != null) web.close();
            if (http != null) http.close();
          }
          case "mailbox" -> {
            if (mailbox != null) mailbox.stop();
          }
          case "channels" -> {
            for (var rpc : channels) rpc.close();
          }
          case "iam" -> {
            if (iam != null) iam.stop();
          }
          case "keys" -> {
            if (signing != null) signing.close();
          }
          case "pg" -> {
            if (database != null) database.close();
          }
          case "files" -> {
            if (directory != null)
              try (var paths = Files.walk(directory)) {
                for (var path : paths.sorted(Comparator.reverseOrder()).toList())
                  Files.deleteIfExists(path);
              }
          }
          default -> throw new IllegalStateException();
        }
        closed.add(stage);
      } catch (Exception e) {
        if (failure == null) failure = e;
        else failure.addSuppressed(e);
      }
    }
    if (failure != null) throw failure;
  }

  void assertClosed() throws IOException {
    if (!closed.equals(List.of("https", "mailbox", "channels", "iam", "keys", "pg", "files")))
      throw new AssertionError("Incomplete shutdown order");
    for (int port : ports) {
      try (var socket = new java.net.Socket()) {
        socket.connect(new InetSocketAddress("127.0.0.1", port), 200);
      } catch (ConnectException expected) {
        continue;
      }
      throw new AssertionError("Host port survived close: " + port);
    }
    if (Files.exists(directory)) throw new AssertionError("Own temporary root survived close");
  }

  public static void main(String[] args) throws Exception {
    if (args.length != 0)
      throw new IllegalArgumentException("No credential or configuration arguments accepted");
    var host = new NetworkSourceJavaHost();
    if ("1".equals(System.getenv("AURIGA_SOURCE_FAULTS")))
      host.faults = new NetworkSourceJavaFaults(true, System.out::println);
    var hook =
        new Thread(
            () -> {
              try {
                host.close();
              } catch (Exception ignored) {
                /* main/finally reports normal close failures */
              }
            },
            "bridge-shutdown");
    Runtime.getRuntime().addShutdownHook(hook);
    try (host;
        var input = new BufferedReader(new InputStreamReader(System.in))) {
      host.start();
      // Trusted login-completion seed only. Register, ticket, Redeem and source remain real calls.
      var device = host.seed("member");
      var external = host.seed("external");
      host.export(device, external);
      System.out.println(host.ready());
      for (String command; (command = input.readLine()) != null; ) {
        if (command.equals("stop")) break;
        if (command.equals("refresh")) {
          device = host.refreshDevice(device);
          external = host.refreshDevice(external);
          host.export(device, external);
          System.out.println(Json.object("stage", "refreshed"));
        } else if (command.equals("revoke-member")) {
          host.revoke(device);
          System.out.println(Json.object("stage", "member-revoked"));
        } else if (command.startsWith("fault ")) {
          var request = NetworkJson.parse(command.substring(6));
          if ("revoke-external".equals(request.path("op").asString())) {
            // Require the opt-in guard even for this real OrgService operation.
            host.faults.control(Json.object("id", request.path("id").asString(), "op", "trace"));
            host.revoke(external);
            System.out.println(
                Json.object(
                    "stage",
                    "fault-control",
                    "id",
                    request.path("id").asString(),
                    "revoked",
                    true));
          } else System.out.println(host.faults.control(request));
        } else throw new IllegalArgumentException("Unknown host control command");
      }
    } finally {
      Runtime.getRuntime().removeShutdownHook(hook);
    }
    host.assertClosed();
    System.out.println(Json.object("stage", "closed", "order", host.closed));
  }

  Device refreshDevice(Device device) {
    return TenantContext.call(
        tenant,
        () -> {
          var response =
              oauth.token(
                  Map.of(
                      "grant_type",
                      "refresh_token",
                      "client_id",
                      "bridge-device",
                      "refresh_token",
                      device.refresh()));
          var access = response.path("access_token").asString();
          if (!device.device().equals(tokens.verify(access).path("did").asString()))
            throw new IllegalStateException("Device changed during refresh");
          return new Device(
              device.account(),
              device.binding(),
              device.session(),
              device.device(),
              access,
              response.path("refresh_token").asString());
        });
  }

  void export(Device device, Device external) throws Exception {
    NetworkSourceJavaPrivateFiles.requirePrivate(directory);
    for (var service : secrets.keySet())
      NetworkSourceJavaPrivateFiles.replace(
          directory.resolve(service + ".jwt"), serviceToken(service));
    NetworkSourceJavaPrivateFiles.replace(
        directory.resolve("device.json"),
        Json.object("access_token", device.access(), "refresh_token", device.refresh()).toString());
    NetworkSourceJavaPrivateFiles.replace(
        directory.resolve("external-device.json"),
        Json.object("access_token", external.access(), "refresh_token", external.refresh())
            .toString());
    NetworkSourceJavaPrivateFiles.replace(
        directory.resolve("bridge.json"),
        Json.object(
                "tenant",
                tenant.toString(),
                "device",
                directory.resolve("device.json").toString(),
                "externalDevice",
                directory.resolve("external-device.json").toString(),
                "gatewayToken",
                directory.resolve("ai-gateway.jwt").toString(),
                "collabToken",
                directory.resolve("collab.jwt").toString(),
                "serverAudience",
                "server-a",
                "realm",
                keySettings.realm(),
                "rootKid",
                keySettings.rootKid(),
                "rootPublicFile",
                keySettings.rootPublicFile().toString(),
                "rootFingerprint",
                keySettings.rootFingerprint(),
                "protocolSigning",
                protocolSigning,
                "expiresAt",
                clock.instant().plusSeconds(300).toString())
            .toString());
  }
}
