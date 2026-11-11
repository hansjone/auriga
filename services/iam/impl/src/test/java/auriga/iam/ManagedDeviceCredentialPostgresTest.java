package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.contracts.workspace.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.api.ManagedInstanceBindings;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class ManagedDeviceCredentialPostgresTest {
  static IamStore store;
  final UUID tenant = UUID.randomUUID(), owner = UUID.randomUUID(), instance = UUID.randomUUID();
  final AdminCsrfPostgresTest.Time clock = new AdminCsrfPostgresTest.Time();
  final ManagedInstanceBindings bindings = mock(ManagedInstanceBindings.class);
  final InstanceAiAuthorization permissions = mock(InstanceAiAuthorization.class);
  IamManagedDevices managed;
  DeviceAccessAuthority authority;
  DeviceAiAccessAuthority aiAuthority;
  GetManagedInstanceBindingResponse binding;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            ManagedDeviceCredentialPostgresTest.class, "managed_owner", "managed_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    store =
        new IamStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @BeforeEach
  void setup() throws Exception {
    clock.now = Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    var key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    var tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            "https://portal.example.com",
            Map.of(),
            List.of("https://managed.example.com"));
    var sessions =
        new SessionManager(
            store,
            mock(SessionKv.class),
            tokens,
            clock,
            mock(EventPublisher.class),
            mock(AuditLogger.class));
    binding =
        GetManagedInstanceBindingResponse.newBuilder()
            .setTenantId(tenant.toString())
            .setInstanceId(instance.toString())
            .setOwnerId(owner.toString())
            .setCredentialVersion(1)
            .setActive(true)
            .build();
    doAnswer(i -> binding).when(bindings).get(any());
    when(permissions.getEffectivePermissions(any()))
        .thenReturn(
            GetEffectivePermissionsResponse.newBuilder()
                .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                .addAllPermissions(
                    List.of(
                        "auriga.connect.collab.use",
                        "auriga.connect.models.invoke",
                        "iam.accounts.admin"))
                .build());
    byte[] encryptionKey = new byte[32];
    new java.security.SecureRandom().nextBytes(encryptionKey);
    managed =
        new IamManagedDevices(
            store,
            sessions,
            tokens,
            new Secrets(Base64.getEncoder().encodeToString(encryptionKey)),
            bindings,
            permissions,
            clock);
    sessions.managedValidator(managed::validSession);
    authority = new DeviceAccessAuthority(store, sessions, tokens);
    aiAuthority =
        new DeviceAiAccessAuthority(authority, tokens, permissions, clock).managedModels(managed);
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () ->
                    store.insert(
                        "account",
                        owner.toString(),
                        Json.object("accountId", owner, "status", "active"),
                        null)));
  }

  IssueManagedDeviceRequest request(long version) {
    return IssueManagedDeviceRequest.newBuilder()
        .setTenantId(tenant.toString())
        .setOwnerId(owner.toString())
        .setInstanceId(instance.toString())
        .setCredentialVersion(version)
        .build();
  }

  String access(IssueManagedDeviceResponse issued) {
    return managed
        .exchange(
            Map.of(
                "instance_id",
                instance.toString(),
                "owner_id",
                owner.toString(),
                "credential",
                issued.getCredential()))
        .path("access_token")
        .asString();
  }

  void denied(Runnable action) {
    assertEquals(
        "common.forbidden", assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  void revoked(Runnable action) {
    var error = assertThrows(AurigaException.class, action::run);
    assertEquals("network.revoked", error.errorCode().code());
    assertEquals(403, error.errorCode().httpStatus());
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(
      strings = {
        "device", "material", "session", "sessionExpired", "account", "memberRemoved",
        "memberDisabled", "memberMissing", "permission", "instanceStopped", "instanceDeleted",
            "bindingVersion"
      })
  void verifiedRevocationDoesNotConsumeFailureBuckets(String reason) {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          switch (reason) {
            case "device", "session", "sessionExpired", "account" ->
                store.tx(
                    () -> {
                      var device = store.get("device", issued.getDeviceId(), true);
                      var row =
                          switch (reason) {
                            case "device" -> device;
                            case "account" -> store.get("account", owner.toString(), true);
                            default ->
                                store.byId("session", device.path("sessionId").asString(), true);
                          };
                      if (reason.equals("account")) row.put("status", "disabled");
                      else if (reason.equals("sessionExpired"))
                        row.put("expiresAt", clock.instant().toString());
                      else row.put("revoked", true);
                      store.save(
                          reason.equals("device")
                              ? "device"
                              : reason.equals("account") ? "account" : "session",
                          row);
                      return null;
                    });
            case "material" ->
                managed.revoke(
                    RevokeManagedDeviceRequest.newBuilder()
                        .setTenantId(tenant.toString())
                        .setInstanceId(instance.toString())
                        .setCredentialVersion(1)
                        .build());
            case "memberRemoved", "memberDisabled", "permission" ->
                when(permissions.getEffectivePermissions(any()))
                    .thenReturn(
                        GetEffectivePermissionsResponse.newBuilder()
                            .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                            .setStatus(
                                reason.equals("memberRemoved")
                                    ? MemberStatus.MEMBER_STATUS_OFFBOARDED
                                    : reason.equals("memberDisabled")
                                        ? MemberStatus.MEMBER_STATUS_DISABLED
                                        : MemberStatus.MEMBER_STATUS_ACTIVE)
                            .build());
            case "memberMissing" ->
                doThrow(new AurigaException(auriga.common.error.CommonErrors.NOT_FOUND))
                    .when(permissions)
                    .getEffectivePermissions(any());
            case "instanceStopped" -> binding = binding.toBuilder().setActive(false).build();
            case "instanceDeleted" ->
                doThrow(new AurigaException(auriga.common.error.CommonErrors.NOT_FOUND))
                    .when(bindings)
                    .get(any());
            case "bindingVersion" -> binding = binding.toBuilder().setCredentialVersion(2).build();
            default -> fail(reason);
          }
          revoked(() -> access(issued));
          assertTrue(store.tx(() -> store.all("rate_limit").isEmpty()));
          // Even for revoked state an unauthenticated caller must get the generic failure.
          denied(
              () ->
                  managed.exchange(
                      Map.of(
                          "instance_id",
                          instance.toString(),
                          "owner_id",
                          owner.toString(),
                          "credential",
                          "wrong")));
          assertEquals(3, store.tx(() -> store.all("rate_limit").size()));
        });
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(
      strings = {
        "credential",
        "instance",
        "owner",
        "tenant",
        "unknownDevice",
        "malformed",
        "missingCredential",
        "extraField",
        "deviceOwner",
        "deviceInstance",
        "deviceVersion"
      })
  void unverifiedMaterialRemainsGenericAndConsumesFailureBuckets(String reason) {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          var input =
              new HashMap<>(
                  Map.of(
                      "instance_id",
                      instance.toString(),
                      "owner_id",
                      owner.toString(),
                      "credential",
                      issued.getCredential()));
          switch (reason) {
            case "credential" -> input.put("credential", Secrets.random());
            case "missingCredential" -> input.remove("credential");
            case "extraField" -> input.put("device_id", issued.getDeviceId());
            case "deviceOwner", "deviceInstance", "deviceVersion" ->
                store.tx(
                    () -> {
                      var device = store.get("device", issued.getDeviceId(), true);
                      if (reason.equals("deviceVersion")) device.put("managedVersion", 2);
                      else {
                        String other = UUID.randomUUID().toString();
                        if (reason.equals("deviceOwner"))
                          store.insert(
                              "account",
                              other,
                              Json.object("accountId", other, "status", "active"),
                              null);
                        device.put(
                            reason.equals("deviceOwner") ? "accountId" : "managedInstance", other);
                      }
                      store.save("device", device);
                      return null;
                    });
            case "instance" -> input.put("instance_id", UUID.randomUUID().toString());
            case "owner" -> input.put("owner_id", UUID.randomUUID().toString());
            case "malformed" -> input.put("instance_id", "invalid");
            case "unknownDevice" ->
                store.tx(
                    () -> {
                      var row = store.get("managed_device", instance.toString(), true);
                      row.put("device", UUID.randomUUID().toString());
                      store.save("managed_device", row);
                      return null;
                    });
            default -> {}
          }
          UUID target = reason.equals("tenant") ? UUID.randomUUID() : tenant;
          TenantContext.run(
              target,
              () -> {
                denied(() -> managed.exchange(input));
                var buckets = store.tx(() -> store.all("rate_limit"));
                assertEquals(3, buckets.size());
                for (var bucket : buckets) assertEquals(1, bucket.path("count").asInt());
              });
        });
  }

  @Test
  void authorityOutageIsTransientAfterVerificationAndDoesNotConsumeFailureBuckets() {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          doThrow(new IllegalStateException("offline")).when(bindings).get(any());
          assertEquals(
              "common.unavailable",
              assertThrows(AurigaException.class, () -> access(issued)).errorCode().code());
          assertTrue(store.tx(() -> store.all("rate_limit").isEmpty()));
        });
  }

  @Test
  void modelCredentialIsSeparateAndBoundToCurrentInstanceMemberAndGeneration() {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          String model = issued.getModelCredential();
          assertTrue(model.startsWith("amc."));
          assertNotEquals(issued.getCredential(), model);
          assertEquals(model, managed.issue(request(1)).getModelCredential());
          assertEquals(owner.toString(), managed.modelAccess(model).account());
          assertNotNull(aiAuthority.access(model));
          assertNull(
              managed.modelAccess(
                  model.substring(0, model.length() - 1) + (model.endsWith("a") ? "b" : "a")));
          binding = binding.toBuilder().setActive(false).build();
          assertNull(managed.modelAccess(model));
          binding = binding.toBuilder().setActive(true).build();
          assertNull(authority.access(model));
          denied(
              () ->
                  managed.exchange(
                      Map.of(
                          "instance_id",
                          instance.toString(),
                          "owner_id",
                          owner.toString(),
                          "credential",
                          model)));
          assertNull(
              managed.modelAccess(model.replace(owner.toString(), UUID.randomUUID().toString())));
          assertNull(
              managed.modelAccess(
                  model.replace(instance.toString(), UUID.randomUUID().toString())));
          assertNull(
              managed.modelAccess(
                  model.replace(issued.getDeviceId(), UUID.randomUUID().toString())));
          TenantContext.run(UUID.randomUUID(), () -> denied(() -> managed.modelAccess(model)));
          binding = binding.toBuilder().setCredentialVersion(2).build();
          assertNull(managed.modelAccess(model));
          var next = managed.issue(request(2));
          assertNotEquals(model, next.getModelCredential());
          assertNull(managed.modelAccess(model));
          assertNotNull(managed.modelAccess(next.getModelCredential()));
          managed.revoke(
              RevokeManagedDeviceRequest.newBuilder()
                  .setTenantId(tenant.toString())
                  .setInstanceId(instance.toString())
                  .setCredentialVersion(2)
                  .build());
          assertNull(managed.modelAccess(next.getModelCredential()));
        });
  }

  @Test
  void modelCredentialRejectsRevokedDeviceWithLiveLineage() {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          store.tx(
              () -> {
                var device = store.get("device", issued.getDeviceId(), true);
                device.put("revoked", true);
                store.save("device", device);
                assertFalse(
                    store
                        .get("managed_device", instance.toString(), false)
                        .path("revoked")
                        .asBoolean());
                return null;
              });
          assertNull(managed.modelAccess(issued.getModelCredential()));
          assertFalse(
              store.tx(
                  () ->
                      store
                          .get("managed_device", instance.toString(), false)
                          .path("revoked")
                          .asBoolean()));
        });
  }

  @Test
  void modelCredentialExpiresAndChecksCurrentModelPermissionAndMembership() {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          var allowed =
              permissions.getEffectivePermissions(
                  GetEffectivePermissionsRequest.getDefaultInstance());
          when(permissions.getEffectivePermissions(any()))
              .thenReturn(
                  allowed.toBuilder()
                      .clearPermissions()
                      .addPermissions("auriga.connect.collab.use")
                      .build());
          assertNull(managed.modelAccess(issued.getModelCredential()));
          assertEquals("", managed.issue(request(1)).getModelCredential());
          when(permissions.getEffectivePermissions(any()))
              .thenReturn(
                  allowed.toBuilder().setStatus(MemberStatus.MEMBER_STATUS_OFFBOARDED).build());
          assertNull(managed.modelAccess(issued.getModelCredential()));
          when(permissions.getEffectivePermissions(any())).thenReturn(allowed);
          clock.now = clock.now.plus(Duration.ofDays(31));
          assertNull(managed.modelAccess(issued.getModelCredential()));
        });
  }

  @Test
  void managedDeviceIssuedThroughApprovedOriginRemainsValidAtInternalAuthority() throws Exception {
    var request = new org.springframework.mock.web.MockHttpServletRequest();
    request.setScheme("https");
    request.setRemoteAddr("192.0.2.30");
    request.addHeader("Host", "managed.example.com");
    var origins =
        new auriga.common.web.LinkOrigins(List.of("https://managed.example.com"), List.of());
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          String token = origins.call(request, () -> access(issued));
          try {
            assertEquals(
                "https://managed.example.com",
                com.nimbusds.jwt.SignedJWT.parse(token).getJWTClaimsSet().getIssuer());
          } catch (java.text.ParseException error) {
            throw new AssertionError(error);
          }
          assertTrue(authority.access(token).expiresAt().isAfter(clock.instant()));
        });
  }

  @Test
  void missingNetworkPermissionIsDeniedForMembersAndExternalAccounts() {
    TenantContext.run(
        tenant,
        () -> {
          for (var type : List.of(MemberType.MEMBER_TYPE_MEMBER, MemberType.MEMBER_TYPE_EXTERNAL)) {
            when(permissions.getEffectivePermissions(any()))
                .thenReturn(
                    GetEffectivePermissionsResponse.newBuilder()
                        .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                        .setMemberType(type)
                        .build());
            denied(() -> managed.issue(request(1)));
          }
          assertTrue(store.tx(() -> store.all("managed_device").isEmpty()));
        });
  }

  @Test
  void failedCredentialBucketsNeverThrottleValidCredentialsAndSourcesAreIndependent() {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          var valid =
              Map.of(
                  "instance_id",
                  instance.toString(),
                  "owner_id",
                  owner.toString(),
                  "credential",
                  issued.getCredential());
          var bad =
              Map.of(
                  "instance_id",
                  instance.toString(),
                  "owner_id",
                  owner.toString(),
                  "credential",
                  "wrong");
          assertEquals(
              "common.forbidden",
              assertThrows(AurigaException.class, () -> managed.exchange(bad, "192.0.2.1"))
                  .errorCode()
                  .code());
          store.tx(
              () -> {
                var count = store.all("rate_limit");
                assertEquals(3, count.size());
                for (var row : count) {
                  row.put("count", 3000);
                  store.save("rate_limit", row);
                }
                return null;
              });
          assertEquals(
              "common.rate_limited",
              assertThrows(AurigaException.class, () -> managed.exchange(bad, "192.0.2.1"))
                  .errorCode()
                  .code());
          assertNotNull(managed.exchange(valid, "192.0.2.1").get("access_token"));
          assertEquals(3, store.tx(() -> store.all("rate_limit").size()));
        });
  }

  @Test
  void sourceFailureBucketLimitsUnknownInstancesAcrossTheSameTrustedSource() {
    TenantContext.run(
        tenant,
        () -> {
          for (int i = 0; i < 300; i++) {
            var bad =
                Map.of(
                    "instance_id",
                    UUID.randomUUID().toString(),
                    "owner_id",
                    owner.toString(),
                    "credential",
                    "wrong");
            assertEquals(
                403,
                assertThrows(AurigaException.class, () -> managed.exchange(bad, "192.0.2.2"))
                    .errorCode()
                    .httpStatus());
          }
          var bad =
              Map.of(
                  "instance_id",
                  UUID.randomUUID().toString(),
                  "owner_id",
                  owner.toString(),
                  "credential",
                  "wrong");
          assertEquals(
              429,
              assertThrows(AurigaException.class, () -> managed.exchange(bad, "192.0.2.2"))
                  .errorCode()
                  .httpStatus());
          assertEquals(
              403,
              assertThrows(AurigaException.class, () -> managed.exchange(bad, "192.0.2.3"))
                  .errorCode()
                  .httpStatus());
        });
  }

  @Test
  void revokedBackendSessionRequestsRotationButRevokedDeviceStaysDenied() {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          store.tx(
              () -> {
                var device = store.get("device", issued.getDeviceId(), false);
                var session = store.byId("session", device.path("sessionId").asString(), true);
                session.put("revoked", true);
                store.save("session", session);
                return null;
              });
          assertEquals(
              "network.revoked",
              assertThrows(AurigaException.class, () -> managed.issue(request(1)))
                  .errorCode()
                  .code());
          binding = binding.toBuilder().setCredentialVersion(2).build();
          var replacement = managed.issue(request(2));
          assertNotEquals(issued.getDeviceId(), replacement.getDeviceId());
          store.tx(
              () -> {
                var device = store.get("device", replacement.getDeviceId(), true);
                device.put("revoked", true);
                store.save("device", device);
                return null;
              });
          denied(() -> managed.issue(request(2)));
        });
  }

  @Test
  void issueExchangeRotateAndStopInvalidateOldCredentialsAndTokens() {
    TenantContext.run(
        tenant,
        () -> {
          var first = managed.issue(request(1));
          assertEquals(first, managed.issue(request(1)));
          String oldAccess = access(first);
          assertNotNull(authority.access(oldAccess));
          binding = binding.toBuilder().setCredentialVersion(2).build();
          assertNull(authority.access(oldAccess));
          revoked(() -> access(first));
          var second = managed.issue(request(2));
          assertNotEquals(first.getDeviceId(), second.getDeviceId());
          assertNotEquals(first.getCredential(), second.getCredential());
          String fresh = access(second);
          assertNotNull(authority.access(fresh));
          binding = binding.toBuilder().setActive(false).build();
          assertNull(authority.access(fresh));
          revoked(() -> access(second));
        });
  }

  @Test
  void wrongMemberInstanceTenantAndExpiredOrRevokedCredentialsAreDenied() {
    TenantContext.run(
        tenant,
        () -> {
          denied(
              () ->
                  managed.issue(
                      request(1).toBuilder().setOwnerId(UUID.randomUUID().toString()).build()));
          denied(
              () ->
                  managed.issue(
                      request(1).toBuilder().setInstanceId(UUID.randomUUID().toString()).build()));
          var issued = managed.issue(request(1));
          var response =
              managed.exchange(
                  Map.of(
                      "instance_id",
                      instance.toString(),
                      "owner_id",
                      owner.toString(),
                      "credential",
                      issued.getCredential()));
          var claims =
              Json.read(
                  new String(
                      Base64.getUrlDecoder()
                          .decode(response.path("access_token").asString().split("\\.")[1])));
          assertEquals(
              List.of("auriga.connect.collab.use"),
              Json.MAPPER.convertValue(claims.path("perms"), List.class));
          denied(
              () ->
                  managed.exchange(
                      Map.of(
                          "instance_id",
                          UUID.randomUUID().toString(),
                          "owner_id",
                          owner.toString(),
                          "credential",
                          issued.getCredential())));
          managed.revoke(
              RevokeManagedDeviceRequest.newBuilder()
                  .setTenantId(tenant.toString())
                  .setInstanceId(instance.toString())
                  .setCredentialVersion(1)
                  .build());
          revoked(() -> access(issued));
          denied(() -> managed.issue(request(1)));
          binding = binding.toBuilder().setCredentialVersion(2).build();
          var next = managed.issue(request(2));
          clock.now = clock.now.plus(Duration.ofDays(31));
          revoked(() -> access(next));
        });
    TenantContext.run(UUID.randomUUID(), () -> denied(() -> managed.issue(request(1))));
  }

  @Test
  void memberRemovalAndAuthorityOutageFailClosed() {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          String token = access(issued);
          when(permissions.getEffectivePermissions(any()))
              .thenReturn(
                  GetEffectivePermissionsResponse.newBuilder()
                      .setStatus(MemberStatus.MEMBER_STATUS_OFFBOARDED)
                      .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                      .build());
          assertNull(authority.access(token));
          revoked(() -> access(issued));
        });
  }

  @Test
  void authorityOutageAndAdministratorDeviceRevocationCannotApprove() {
    TenantContext.run(
        tenant,
        () -> {
          var issued = managed.issue(request(1));
          String token = access(issued);
          when(bindings.get(any())).thenThrow(new IllegalStateException("authority unavailable"));
          assertEquals(
              "common.unavailable",
              assertThrows(AurigaException.class, () -> authority.access(token))
                  .errorCode()
                  .code());
          doAnswer(i -> binding).when(bindings).get(any());
          store.tx(
              () -> {
                var device = store.get("device", issued.getDeviceId(), true);
                device.put("revoked", true);
                store.save("device", device);
                return null;
              });
          assertNull(authority.access(token));
          revoked(() -> access(issued));
          denied(() -> managed.issue(request(1)));
        });
  }
}
