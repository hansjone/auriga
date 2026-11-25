package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.iam.api.IdentityPolicyHooks;
import auriga.iam.api.UacEnterpriseAdapter;
import auriga.org.OrgRuntimeConfiguration;
import auriga.org.OrgStore;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicReference;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.core.env.MapPropertySource;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.env.MockEnvironment;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class UacFirstLoginRecoveryPostgresTest {
  private static final String ORIGIN = "https://portal.example.test";
  private static final String EMPLOYEE = "009Xy";

  @Test
  void aFreshScanRecoversAfterOrgCommitAndNativeGrantExpiryWithoutPlatformMfa() {
    UUID tenant = UUID.randomUUID();
    DataSource iamData = migrate("iam");
    DataSource orgData = migrate("org");
    var store = new IamStore(new JdbcTemplate(iamData), new TenantTransactionManager(iamData));
    var orgJdbc = new JdbcTemplate(orgData);
    var org = new OrgStore(orgJdbc, new TenantTransactionManager(orgData));
    org.tenant(
        tenant,
        () -> {
          orgJdbc.update(
              "INSERT INTO tenant(id,tenant_id,slug,display_name) VALUES (?,?,?,'UAC test tenant')",
              tenant,
              tenant,
              tenant.toString());
          return null;
        });
    var now = new AtomicReference<>(Instant.parse("2026-01-01T00:00:00Z"));
    Clock clock =
        new Clock() {
          public ZoneId getZone() {
            return ZoneOffset.UTC;
          }

          public Clock withZone(ZoneId zone) {
            return this;
          }

          public Instant instant() {
            return now.get();
          }
        };
    var events = mock(EventPublisher.class);
    var audit = mock(AuditLogger.class);
    var tokens = mock(TokenService.class);
    try (var orgContext = new AnnotationConfigApplicationContext()) {
      orgContext
          .getEnvironment()
          .getPropertySources()
          .addFirst(new MapPropertySource("test", Map.of("auriga.service.schema", "org")));
      orgContext.registerBean(OrgStore.class, () -> org);
      orgContext.registerBean(EventPublisher.class, () -> events);
      orgContext.registerBean(Clock.class, () -> clock);
      orgContext.register(OrgRuntimeConfiguration.class);
      orgContext.refresh();
      var hooks = spy(orgContext.getBean(IdentityPolicyHooks.class));
      doAnswer(
              call -> {
                boolean committed = (boolean) call.callRealMethod();
                if (committed) now.updateAndGet(t -> t.plusSeconds(301));
                return committed;
              })
          .when(hooks)
          .provisionVerifiedUacMember(any(), anyString(), any(), anyString());
      var schemas = new ContractSchemas();
      String key = Base64.getEncoder().encodeToString(new byte[32]);
      var secrets = new Secrets(key);
      var registry = new ConnectorRegistry(schemas, List.of());
      var providers = new ProviderManager(store, registry, secrets, audit, clock, ORIGIN);
      var adapter = mock(UacEnterpriseAdapter.class);
      when(adapter.qrContent(any(), any())).thenReturn("synthetic-qr");
      var sessions = new SessionManager(store, mock(SessionKv.class), tokens, clock, events, audit);
      var config = new UacProviderService(store, providers, adapter, sessions, audit, clock);
      var cipher =
          new UacCredentialCipher(
              schemas,
              Json.object("activeKeyId", "test", "decryptKeyIds", List.of("test"), "revision", 0),
              Map.of("test", key));
      var uac =
          new UacSessions(
              store,
              cipher,
              adapter,
              providers,
              sessions,
              clock,
              new UacRevalidationConfiguration(new MockEnvironment()));
      var team = new TeamAccounts(store, secrets, clock, events);
      var login =
          new LoginManager(
              store,
              secrets,
              schemas,
              registry,
              mock(EmergencyAccounts.class),
              team,
              sessions,
              hooks,
              events,
              audit,
              clock,
              ORIGIN);
      login.uacEstablished(uac::established);
      var oauth =
          new OAuthProvider(
              store,
              sessions,
              tokens,
              clock,
              List.of(
                  new OAuthClient(
                      "recovery-test",
                      tenant,
                      null,
                      Set.of(),
                      Set.of("auriga.connect"),
                      Set.of(OAuthProvider.DEVICE, "refresh_token"),
                      true,
                      true)));
      login.nativeDevices(oauth);
      var flow = new UacFlow(store, config, providers, adapter, uac, login, sessions, clock);
      String provider = UUID.randomUUID().toString();
      store.tenant(
          tenant,
          () -> {
            var row =
                Json.object(
                    "providerId",
                    provider,
                    "connectorType",
                    "uac",
                    "enabled",
                    true,
                    "uacConfigured",
                    true,
                    "revision",
                    1,
                    "display",
                    Json.object("name", Json.object("en", "UAC fixture")),
                    "admission",
                    Json.object("mode", "invite_only", "mfa", "always"));
            providers.encryptConfig(row, Json.object("baseUrl", ORIGIN, "systemCode", "test"));
            return store.insert("idp_provider", provider, row, null);
          });
      String verifier = Secrets.random();
      var request =
          Json.object(
              "providerId",
              provider,
              "nativeConnect",
              Json.object(
                  "clientId", "recovery-test",
                  "codeChallenge", OAuthProvider.challenge(verifier),
                  "codeChallengeMethod", "S256"));
      var first = verifiedScan(store, tenant, flow, uac, request, null);
      assertEquals(
          "iam.invalid_grant",
          assertThrows(AurigaException.class, () -> completeScan(store, tenant, flow, first))
              .errorCode()
              .code());
      UUID account =
          org.tenant(
              tenant,
              () -> hooks.resolveUacEmployee(UUID.fromString(provider), EMPLOYEE).orElseThrow());
      store.tenant(
          tenant,
          () -> {
            for (String table : List.of("account", "identity", "team_totp", "session"))
              assertTrue(store.all(table).isEmpty(), table);
            var grant = store.get("oauth_grant", first.grant(), false);
            // Completion rolled back; the prior pending grant stays expired and unapproved.
            assertEquals("pending", grant.path("status").asString());
            assertFalse(grant.path("used").asBoolean());
            assertFalse(grant.has("sessionId"));
            assertTrue(clock.instant().isAfter(Instant.parse(grant.path("expiresAt").asString())));
            assertEquals(
                "expired_token",
                oauth
                    .token(
                        Map.of(
                            "grant_type",
                            OAuthProvider.DEVICE,
                            "client_id",
                            "recovery-test",
                            "device_code",
                            first.deviceCode(),
                            "code_verifier",
                            verifier))
                    .path("error")
                    .asString());
            return null;
          });

      var retry = verifiedScan(store, tenant, flow, uac, request, first.binding());
      var completed = completeScan(store, tenant, flow, retry);
      assertNotNull(completed.session());
      assertEquals("complete", completed.transaction().path("step").path("type").asString());
      assertEquals(account.toString(), completed.session().session().path("accountId").asString());
      assertEquals("portal", completed.session().session().path("audience").asString());
      assertFalse(completed.session().session().path("mfa").asBoolean());
      verify(hooks, times(1)).provisionVerifiedUacMember(any(), anyString(), any(), anyString());
      store.tenant(
          tenant,
          () -> {
            for (String table : List.of("account", "identity", "session"))
              assertEquals(1, store.all(table).size(), table);
            assertTrue(store.all("team_totp").isEmpty());
            assertFalse(team.hasTotp(account));
            assertTrue(store.byId("uac_challenge", first.id(), false).path("replaced").asBoolean());
            assertEquals(
                "member",
                store.get("account", account.toString(), false).path("memberType").asString());
            assertEquals(
                account.toString(),
                store
                    .get("identity", provider + ":" + EMPLOYEE, false)
                    .path("accountId")
                    .asString());
            assertEquals(
                "approved",
                store.get("oauth_grant", retry.grant(), false).path("status").asString());
            return null;
          });
      org.tenant(
          tenant,
          () -> {
            assertEquals(1, orgJdbc.queryForObject("SELECT count(*) FROM member", Integer.class));
            assertEquals(
                1,
                orgJdbc.queryForObject("SELECT count(*) FROM uac_employee_binding", Integer.class));
            assertEquals("member", hooks.uacMember(account).orElseThrow().memberType());
            return null;
          });
    }
  }

  private record VerifiedScan(String id, String binding, String grant, String deviceCode) {}

  private static VerifiedScan verifiedScan(
      IamStore store,
      UUID tenant,
      UacFlow flow,
      UacSessions uac,
      ObjectNode request,
      String previous) {
    return store.tenant(
        tenant,
        () -> {
          var created = flow.create(request, "portal", ORIGIN, "127.0.0.1", previous, null);
          String id = created.challenge().path("id").asString();
          // Only the vendor scan result is synthetic; completion and all writes use real stores.
          var challenge = store.byId("uac_challenge", id, true);
          challenge.put("state", "verified");
          challenge.set(
              "credential",
              uac.seal(
                  Json.object("empNo", EMPLOYEE, "token", Secrets.random()),
                  challenge.path("providerId").asString()));
          store.save("uac_challenge", challenge);
          String deviceCode =
              created.challenge().path("nativeDevice").path("deviceCode").asString();
          return new VerifiedScan(id, created.binding(), Secrets.hash(deviceCode), deviceCode);
        });
  }

  private static LoginManager.Result completeScan(
      IamStore store, UUID tenant, UacFlow flow, VerifiedScan scan) {
    // Match separate create/complete requests so a failed completion cannot erase the grant.
    return store.tenant(tenant, () -> flow.complete(scan.id(), scan.binding(), "portal", ORIGIN));
  }

  private static DataSource migrate(String schema) {
    var roles =
        PostgresExtension.serviceRoles(
            UacFirstLoginRecoveryPostgresTest.class,
            "recovery_" + schema + "_owner",
            "recovery_" + schema + "_app",
            schema);
    CommonMigrations.migrate(roles.owner().dataSource(), schema);
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas(schema)
                .locations("classpath:db/" + schema)
                .load())
        .migrate();
    return roles.runtime().dataSource();
  }
}
