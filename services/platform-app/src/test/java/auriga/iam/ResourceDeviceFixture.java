package auriga.iam;

import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.IssueManagedDeviceRequest;
import auriga.contracts.org.v1.*;
import auriga.contracts.workspace.v1.*;
import auriga.iam.api.*;
import auriga.workspace.api.ManagedInstanceBindings;
import com.nimbusds.jose.jwk.ECKey;
import java.time.*;
import java.util.*;
import javax.sql.DataSource;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real IAM signing, PostgreSQL lineage and OAuth exchanges for resource HTTP tests. */
public final class ResourceDeviceFixture {
  public record Grant(String token, UUID device) {}

  private final IamStore store;
  private final SessionManager sessions;
  private final TokenService tokens;
  private final InstanceAiAuthorization authorization;
  private final Clock clock;

  public ResourceDeviceFixture(
      DataSource source,
      ECKey key,
      String issuer,
      Clock clock,
      PermissionResolver resolver,
      InstanceAiAuthorization authorization) {
    store = new IamStore(new JdbcTemplate(source), new TenantTransactionManager(source));
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    tokens = new TokenService(keys, new ContractSchemas(), resolver, clock, issuer, Map.of());
    sessions =
        new SessionManager(
            store,
            mock(SessionKv.class),
            tokens,
            clock,
            mock(EventPublisher.class),
            mock(AuditLogger.class));
    this.authorization = authorization;
    this.clock = clock;
  }

  private OAuthProvider oauth(UUID tenant) {
    return new OAuthProvider(
        store,
        sessions,
        tokens,
        clock,
        List.of(
            new OAuthClient(
                "auriga-dsh",
                tenant,
                null,
                Set.of(),
                Set.of("auriga.connect"),
                Set.of(OAuthProvider.DEVICE, "refresh_token"),
                true,
                false)),
        authorization);
  }

  public DevicePermissionAuthority authority() {
    // Resolve the registration within the request's tenant, as in production OAuth wiring.
    return (tenant, account, device, permission) ->
        new IamDevicePermissionAuthority(oauth(tenant).deviceAuthority(), authorization)
            .allowed(tenant, account, device, permission);
  }

  public Grant issue(UUID tenant, UUID account, String kind) {
    return TenantContext.call(
        tenant,
        () -> {
          UUID provider = UUID.randomUUID();
          String memberType =
              authorization
                          .getEffectivePermissions(
                              GetEffectivePermissionsRequest.newBuilder()
                                  .setTenantId(tenant.toString())
                                  .setAccountId(account.toString())
                                  .build())
                          .getMemberType()
                      == MemberType.MEMBER_TYPE_EXTERNAL
                  ? "external"
                  : "member";
          var source =
              store.tx(
                  () -> {
                    if (store.get("account", account.toString(), false) == null)
                      store.insert(
                          "account",
                          account.toString(),
                          Json.object(
                              "accountId", account, "status", "active", "memberType", memberType),
                          null);
                    store.insert(
                        "idp_provider",
                        provider.toString(),
                        Json.object("providerId", provider, "enabled", true),
                        null);
                    return sessions
                        .create(
                            account,
                            provider,
                            "portal",
                            false,
                            Duration.ofHours(1),
                            Duration.ofHours(1))
                        .session();
                  });
          if (kind.equals("managed")) return managed(tenant, account);
          var oauth = oauth(tenant);
          if (kind.equals("standard")) {
            var challenge =
                oauth.deviceAuthorization(
                    Map.of("client_id", "auriga-dsh", "scope", "auriga.connect"));
            oauth.decideDevice(
                challenge.path("user_code").asString(), source.path("sessionId").asString(), true);
            var response =
                oauth.token(
                    Map.of(
                        "client_id",
                        "auriga-dsh",
                        "grant_type",
                        OAuthProvider.DEVICE,
                        "device_code",
                        challenge.path("device_code").asString()));
            String token = response.path("access_token").asString();
            return new Grant(token, UUID.fromString(tokens.verify(token).path("did").asString()));
          }
          UUID device = UUID.randomUUID();
          return store.tx(
              () -> {
                store.insert(
                    "device",
                    device.toString(),
                    Json.object(
                        "deviceId",
                        device,
                        "accountId",
                        account,
                        "sessionId",
                        source.path("sessionId"),
                        "scope",
                        "auriga.connect",
                        "clientId",
                        "auriga-dsh-native",
                        "authorizationMode",
                        "native_tx",
                        "revoked",
                        false),
                    null);
                return new Grant(
                    tokens.access(
                        tenant,
                        account.toString(),
                        "auriga",
                        "device",
                        "device",
                        null,
                        device,
                        clock.instant(),
                        null,
                        "auriga.connect",
                        memberType),
                    device);
              });
        });
  }

  private Grant managed(UUID tenant, UUID account) {
    UUID instance = UUID.randomUUID();
    byte[] encryptionKey = new byte[32];
    new java.security.SecureRandom().nextBytes(encryptionKey);
    ManagedInstanceBindings bindings =
        request ->
            GetManagedInstanceBindingResponse.newBuilder()
                .setTenantId(tenant.toString())
                .setOwnerId(account.toString())
                .setInstanceId(instance.toString())
                .setCredentialVersion(1)
                .setActive(true)
                .build();
    var managed =
        new IamManagedDevices(
            store,
            sessions,
            tokens,
            new Secrets(Base64.getEncoder().encodeToString(encryptionKey)),
            bindings,
            authorization,
            clock);
    sessions.managedValidator(managed::validSession);
    var issued =
        managed.issue(
            IssueManagedDeviceRequest.newBuilder()
                .setTenantId(tenant.toString())
                .setOwnerId(account.toString())
                .setInstanceId(instance.toString())
                .setCredentialVersion(1)
                .build());
    String token =
        managed
            .exchange(
                Map.of(
                    "instance_id",
                    instance.toString(),
                    "owner_id",
                    account.toString(),
                    "credential",
                    issued.getCredential()))
            .path("access_token")
            .asString();
    return new Grant(token, UUID.fromString(issued.getDeviceId()));
  }

  public void revoke(UUID tenant, UUID device) {
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  sessions.revokeDevice(store.get("device", device.toString(), true));
                  return null;
                }));
  }
}
