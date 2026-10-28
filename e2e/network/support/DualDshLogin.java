package auriga.iam;

import auriga.collab.app.*;
import auriga.collab.grpc.CollabGrpcService;
import auriga.common.audit.OutboxAuditLogger;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.IdentityPolicyHooks;
import java.io.*;
import java.nio.file.*;
import java.time.*;
import java.util.*;
import org.springframework.context.annotation.*;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.json.JsonMapper;

/** Test assembly of production login and Team services; no device, source or mail is seeded. */
@Configuration(proxyBeanMethods = false)
public class DualDshLogin {
  static final UUID provider = UuidV7.next();
  static String password;

  @Bean
  Secrets loginSecrets() {
    byte[] bytes = new byte[32];
    new java.security.SecureRandom().nextBytes(bytes);
    return new Secrets(Base64.getEncoder().encodeToString(bytes));
  }

  static EventPublisher events(NetworkSourceJavaHost h) {
    return new OutboxEventPublisher(
        h.store.networkJdbc(),
        "iam",
        "iam",
        new EventSchemas(),
        JsonMapper.builder().build(),
        h.clock,
        () -> {});
  }

  @Bean
  ConnectorRegistry loginConnectors(NetworkSourceJavaHost h, Secrets s) {
    return new ConnectorRegistry(
        new ContractSchemas(),
        List.of(
            new TeamAccountConnector(
                new TeamAccounts(h.store, s, h.clock, events(h)),
                new TeamPasskeys(h.store, s, h.clock, events(h)),
                new QrChallenges(h.store, h.clock),
                h.tokens.issuer())));
  }

  @Bean
  LoginManager nativeLogin(NetworkSourceJavaHost h, Secrets s, ConnectorRegistry registry) {
    var team = new TeamAccounts(h.store, s, h.clock, events(h));
    h.oauth =
        new OAuthProvider(
            h.store,
            h.sessions,
            h.tokens,
            h.clock,
            List.of(
                new OAuthClient(
                    "bridge-device",
                    h.tenant,
                    null,
                    Set.of(),
                    Set.of("auriga.connect"),
                    Set.of(OAuthProvider.DEVICE, "refresh_token"),
                    true,
                    true)));
    var login =
        new LoginManager(
            h.store,
            s,
            new ContractSchemas(),
            registry,
            new EmergencyAccounts(h.store, s, h.clock, false),
            team,
            h.sessions,
            new IdentityPolicyHooks() {},
            events(h),
            new OutboxAuditLogger(events(h)),
            h.clock,
            h.tokens.issuer());
    login.nativeDevices(h.oauth);
    // Installation accounts, passwords and memberships only. Login transactions,
    // backing sessions and device grants must come from normal HTTP operations.
    password = Secrets.random();
    h.store.tenant(
        h.tenant,
        () -> {
          h.store.insert(
              "idp_provider",
              provider.toString(),
              Json.object(
                  "providerId",
                  provider,
                  "connectorType",
                  "team-account",
                  "enabled",
                  true,
                  "display",
                  Json.object("name", Json.object("en", "Example team")),
                  "config",
                  s.encrypt(
                      Json.object(
                              "rpId",
                              "127.0.0.1",
                              "origins",
                              List.of(h.tokens.issuer()),
                              "primaryStep",
                              "form")
                          .toString(),
                      "provider:" + h.tenant + ":" + provider),
                  "admission",
                  Json.object(
                      "mode",
                      "invite_only",
                      "mfa",
                      "never",
                      "sessionTtlSeconds",
                      3600,
                      "idleTimeoutSeconds",
                      3600)),
              null);
          for (String username : List.of("alice", "bob")) {
            UUID account = UuidV7.next();
            team.register(provider.toString(), username, password, username);
            h.store.insert(
                "account",
                account.toString(),
                Json.object(
                    "accountId",
                    account,
                    "status",
                    "active",
                    "memberType",
                    "member",
                    "createdAt",
                    h.clock.instant().toString()),
                null);
            h.store.insert(
                "identity",
                provider + ":" + username,
                Json.object("providerId", provider, "accountId", account, "subject", username),
                null);
            h.org
                .store()
                .tenant(
                    h.tenant,
                    () -> {
                      var j = h.org.store().jdbc();
                      h.org
                          .store()
                          .insertMember(
                              new auriga.org.OrgStore.MemberRow(
                                  account,
                                  username,
                                  null,
                                  "member",
                                  "active",
                                  1,
                                  h.clock.instant(),
                                  null,
                                  null));
                      UUID role = UuidV7.next();
                      j.update(
                          "INSERT INTO org.role(id,tenant_id,key,name_en,name_zh,permissions)"
                              + " VALUES(?,?,?,'Example','Example',ARRAY['auriga.connect','auriga.connect.collab.use','collab.messages.use'])",
                          role,
                          h.tenant,
                          "send4-" + role);
                      j.update(
                          "INSERT INTO"
                              + " org.role_binding(id,tenant_id,role_id,subject_type,subject_id)"
                              + " VALUES(?,?,?,'member',?)",
                          UuidV7.next(),
                          h.tenant,
                          role,
                          account);
                      return null;
                    });
          }
          return null;
        });
    return login;
  }

  @Bean
  RequestIdentity entryIdentity(NetworkSourceJavaHost h) throws IOException {
    var file = Path.of(System.getenv("AURIGA_SEND4_ACCESS"));
    var document = Json.read(Files.readString(file));
    document.put("tenantId", h.tenant.toString());
    var access =
        DeploymentAccess.load(
            document.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8));
    return new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
  }

  @Bean
  IamLoginController loginController(
      NetworkSourceJavaHost h, LoginManager login, RequestIdentity requests, Secrets s) {
    return new IamLoginController(
        login, requests, h.store, h.sessions, new QrChallenges(h.store, h.clock), s);
  }

  @Bean
  OAuthController oauthController(
      NetworkSourceJavaHost h, LoginManager login, RequestIdentity requests) {
    return new OAuthController(h.oauth, h.keys, requests, h.store, h.sessions);
  }

  static CollabGrpcService communication(NetworkSourceJavaHost h) {
    var channel = h.rpc("collab").channel();
    var devices = DeviceAccessServiceGrpc.newBlockingStub(channel);
    var permissions = AuthorizationServiceGrpc.newBlockingStub(channel);
    var admission =
        new OnlineCollabAdmission(
            actor -> {
              var result =
                  devices.verifyDeviceCredential(
                      VerifyDeviceCredentialRequest.newBuilder()
                          .setTenantId(actor.getTenantId())
                          .setAccountId(actor.getAccountId())
                          .setDeviceId(actor.getCredentialSubject())
                          .build());
              if (!result.getActive()
                  || !result.getTenantId().equals(actor.getTenantId())
                  || !result.getAccountId().equals(actor.getAccountId())
                  || !result.getDeviceId().equals(actor.getCredentialSubject())
                  || !result.hasExpiresAt()
                  || result.getExpiresAt().getSeconds() <= Instant.now().getEpochSecond())
                throw new auriga.common.error.AurigaException(
                    auriga.common.error.CommonErrors.FORBIDDEN);
            },
            actor ->
                permissions.getEffectivePermissions(
                    GetEffectivePermissionsRequest.newBuilder()
                        .setTenantId(actor.getTenantId())
                        .setAccountId(actor.getAccountId())
                        .build()));
    var data = h.database.runtime.get("collab");
    return new CollabGrpcService(
        new CollabService(new JdbcTemplate(data), new TenantTransactionManager(data), admission));
  }

  public static void main(String[] args) throws Exception {
    if (args.length != 0) throw new IllegalArgumentException("No arguments");
    var h = new NetworkSourceJavaHost();
    try (h;
        var input = new BufferedReader(new InputStreamReader(System.in))) {
      h.start();
      h.database.assertIsolation();
      for (var service : h.secrets.keySet())
        NetworkSourceJavaPrivateFiles.replace(
            h.directory.resolve(service + ".jwt"), h.serviceToken(service));
      NetworkSourceJavaPrivateFiles.replace(
          h.directory.resolve("bridge.json"),
          Json.object(
                  "tenant",
                  h.tenant,
                  "gatewayToken",
                  h.directory.resolve("ai-gateway.jwt").toString(),
                  "collabToken",
                  h.directory.resolve("collab.jwt").toString(),
                  "serverAudience",
                  "server-a",
                  "realm",
                  h.keySettings.realm(),
                  "rootKid",
                  h.keySettings.rootKid(),
                  "rootPublicFile",
                  h.keySettings.rootPublicFile().toString(),
                  "rootFingerprint",
                  h.keySettings.rootFingerprint(),
                  "protocolSigning",
                  h.protocolSigning)
              .toString());
      NetworkSourceJavaPrivateFiles.replace(
          h.directory.resolve("login.json"),
          Json.object("providerId", provider, "password", password).toString());
      System.out.println(h.ready());
      for (String line; (line = input.readLine()) != null; ) {
        if (line.equals("stop")) break;
        if (!line.equals("refresh")) throw new IllegalArgumentException("Unknown control");
        for (var service : h.secrets.keySet())
          NetworkSourceJavaPrivateFiles.replace(
              h.directory.resolve(service + ".jwt"), h.serviceToken(service));
        System.out.println(Json.object("stage", "refreshed"));
      }
    }
    h.assertClosed();
    System.out.println(Json.object("stage", "closed", "order", h.closed));
  }
}
