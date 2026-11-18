package auriga.iam;

import static auriga.iam.SharedTaskTokens.*;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.iam.v1.*;
import auriga.contracts.node.v1.*;
import auriga.iam.api.PermissionResolver;
import auriga.workspace.api.SharedDshSessions;
import java.security.KeyPairGenerator;
import java.time.*;
import java.util.*;
import java.util.concurrent.atomic.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

/** Transaction/storage doubles isolate task policy; actual PG migration and RLS run in CI. */
class SharedTasksTest {
  @Test
  void readsCurrentEmployeeCredentialAndRejectsOldVersionWithoutChangingTaskOwner()
      throws Exception {
    var f = new Fixture();
    f.run(
        () -> {
          var first = f.begin(ENTERPRISE, List.of("credentials", "outbound"));
          var raw =
              f.tasks.credentials(
                  ReadSharedCredentialsRequest.newBuilder()
                      .setCapability(first.getCapability())
                      .build());
          assertEquals("000042", raw.getEmpNo());
          assertEquals("synthetic-v1", raw.getToken());
          f.credential.put("version", 2);
          f.material.put("credentialVersion", 2).put("token", "synthetic-v2");
          assertFalse(f.authorize(first.getCapability()).getActive());
          var updated = f.begin(ENTERPRISE, List.of("credentials", "outbound"));
          assertEquals(2, updated.getCapability().getCredentialVersion());
          assertEquals(
              "synthetic-v2",
              f.tasks
                  .credentials(
                      ReadSharedCredentialsRequest.newBuilder()
                          .setCapability(updated.getCapability())
                          .build())
                  .getToken());
          var switched = f.binding.toBuilder().setMemberId(UUID.randomUUID().toString()).build();
          f.currentOwnership.set(switched);
          assertFalse(f.authorize(updated.getCapability()).getActive());
          return null;
        });
  }

  @Test
  void endingTaskPersistsAcrossAuthorityRecreationAndCannotBeReopened() throws Exception {
    var f = new Fixture();
    f.run(
        () -> {
          var issued = f.begin(ENTERPRISE, List.of("outbound"));
          var finish =
              FinishSharedTaskRequest.newBuilder().setBinding(f.binding).setTaskId(f.task).build();
          f.tasks.finish(finish);
          f.tasks = f.authority();
          f.tasks.finish(finish);
          assertFalse(f.authorize(issued.getCapability()).getActive());
          assertThrows(AurigaException.class, () -> f.begin(ENTERPRISE, List.of("outbound")));
          return null;
        });
  }

  @Test
  void rawSwitchStopsRawReadsWhileExistingOutboundCapabilityRemainsActive() throws Exception {
    var f = new Fixture();
    f.run(
        () -> {
          var issued = f.begin(ENTERPRISE, List.of("credentials", "outbound"));
          f.raw.set(false);
          var state = f.authorize(issued.getCapability());
          assertTrue(state.getActive());
          assertFalse(state.getAllowRawAgentToken());
          assertThrows(
              AurigaException.class,
              () ->
                  f.tasks.credentials(
                      ReadSharedCredentialsRequest.newBuilder()
                          .setCapability(issued.getCapability())
                          .build()));
          assertTrue(
              f.authorize(f.begin(ENTERPRISE, List.of("outbound")).getCapability()).getActive());
          assertThrows(AurigaException.class, () -> f.begin(ENTERPRISE, List.of("credentials")));
          return null;
        });
  }

  @Test
  void modelTaskWorksWithoutEnterpriseCredentialsAndPermissionChangesFenceOldCapabilities()
      throws Exception {
    var f = new Fixture();
    f.session.remove("uacCredentialId");
    f.run(
        () -> {
          var issued = f.begin(MODEL, List.of("model.invoke"));
          assertTrue(f.authorize(issued.getCapability()).getActive());
          f.permissions.set(Set.of());
          assertFalse(f.authorize(issued.getCapability()).getActive());
          f.permissions.set(Set.of("ai.models.invoke"));
          assertFalse(f.authorize(issued.getCapability()).getActive());
          var replacement =
              f.tasks.issue(
                  IssueSharedCapabilityRequest.newBuilder()
                      .setBinding(f.binding)
                      .setTaskId(f.task)
                      .setAudience(MODEL)
                      .addScopes("model.invoke")
                      .build());
          assertTrue(
              replacement.getCapability().getCredentialVersion()
                  > issued.getCapability().getCredentialVersion());
          assertTrue(f.authorize(replacement.getCapability()).getActive());
          var lookup =
              ResolveSharedTaskRequest.newBuilder()
                  .setTenantId(f.tenant.toString())
                  .setSessionId(f.binding.getSessionId())
                  .setTaskId(f.task)
                  .setAudience(MODEL)
                  .setJti(replacement.getCapability().getJti())
                  .build();
          assertTrue(f.tasks.resolve(lookup).getActive());
          assertFalse(
              f.tasks
                  .resolve(lookup.toBuilder().setJti(UUID.randomUUID().toString()).build())
                  .getActive());
          return null;
        });
  }

  @Test
  void rejectsInventedJtiChangedClaimsAndRevokedPlatformSession() throws Exception {
    var f = new Fixture();
    f.run(
        () -> {
          var issued = f.begin(ENTERPRISE, List.of("outbound"));
          assertFalse(
              f.authorize(
                      issued.getCapability().toBuilder()
                          .setJti(UUID.randomUUID().toString())
                          .build())
                  .getActive());
          assertFalse(
              f.authorize(issued.getCapability().toBuilder().addScopes("credentials").build())
                  .getActive());
          f.active.set(false);
          assertFalse(f.authorize(issued.getCapability()).getActive());
          assertThrows(
              AurigaException.class,
              () ->
                  f.tasks.credentials(
                      ReadSharedCredentialsRequest.newBuilder()
                          .setCapability(issued.getCapability())
                          .build()));
          return null;
        });
  }

  @Test
  void authorityFailureNeverFallsBackToPreviouslyIssuedCapability() throws Exception {
    var f = new Fixture();
    f.run(
        () -> {
          var issued = f.begin(ENTERPRISE, List.of("outbound"));
          f.unavailable.set(true);
          var error =
              assertThrows(AurigaException.class, () -> f.authorize(issued.getCapability()));
          assertEquals("common.unavailable", error.errorCode().code());
          return null;
        });
  }

  private static final class Fixture {
    final UUID tenant = UUID.randomUUID();
    final String task = UUID.randomUUID().toString();
    final Clock clock = Clock.fixed(Instant.parse("2026-11-19T00:00:00Z"), ZoneOffset.UTC);
    final SharedDshSessionBinding binding =
        SharedDshSessionBinding.newBuilder()
            .setTenantId(tenant.toString())
            .setMemberId(UUID.randomUUID().toString())
            .setPlacementId(UUID.randomUUID().toString())
            .setPhysicalInstanceId(UUID.randomUUID().toString())
            .setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
            .setEpoch(1)
            .setRevision(1)
            .setSessionId(UUID.randomUUID().toString())
            .setPlatformSessionId(UUID.randomUUID().toString())
            .setAuthGeneration(1)
            .build();
    final IamStore store = mock(IamStore.class);
    final SessionManager sessions = mock(SessionManager.class);
    final UacSessions uac = mock(UacSessions.class);
    final Map<String, ObjectNode> records = new HashMap<>();
    final AtomicBoolean active = new AtomicBoolean(true);
    final AtomicBoolean raw = new AtomicBoolean(true);
    final AtomicBoolean unavailable = new AtomicBoolean();
    final AtomicReference<Set<String>> permissions =
        new AtomicReference<>(Set.of("ai.models.invoke", "collab.messages.use"));
    final AtomicReference<SharedDshSessionBinding> currentOwnership =
        new AtomicReference<>(binding);
    final ObjectNode session =
        Json.object(
            "tenantId",
            tenant.toString(),
            "accountId",
            binding.getMemberId(),
            "audience",
            "portal",
            "authGeneration",
            1,
            "providerId",
            UUID.randomUUID().toString(),
            "uacCredentialId",
            UUID.randomUUID().toString(),
            "expiresAt",
            clock.instant().plusSeconds(3600).toString());
    final ObjectNode credential =
        Json.object(
            "accountId",
            binding.getMemberId(),
            "providerId",
            session.path("providerId"),
            "version",
            1,
            "invalid",
            false);
    final ObjectNode material =
        Json.object(
            "empNo",
            "000042",
            "token",
            "synthetic-v1",
            "updatedAt",
            clock.instant().toString(),
            "credentialVersion",
            1);
    final SharedTaskTokens tokens;
    SharedTasks tasks;

    Fixture() throws Exception {
      var keys = new HashMap<String, SharedTaskTokens.Key>();
      for (var audience : List.of(ENTERPRISE, MODEL, MESSAGE))
        keys.put(
            audience,
            new SharedTaskTokens.Key(
                "test", KeyPairGenerator.getInstance("Ed25519").generateKeyPair().getPrivate()));
      tokens = new SharedTaskTokens(keys);
      when(store.tx(any())).thenAnswer(call -> ((Supplier<?>) call.getArgument(0)).get());
      when(store.get(anyString(), anyString(), anyBoolean()))
          .thenAnswer(
              call -> {
                var value = records.get(call.getArgument(0) + ":" + call.getArgument(1));
                return value == null ? null : value.deepCopy();
              });
      when(store.insert(anyString(), anyString(), any(), any()))
          .thenAnswer(
              call -> {
                ObjectNode value = call.getArgument(2);
                value.put("id", (String) call.getArgument(1));
                records.put(call.getArgument(0) + ":" + call.getArgument(1), value.deepCopy());
                return value;
              });
      doAnswer(
              call -> {
                ObjectNode value = call.getArgument(1);
                records.put(
                    call.getArgument(0) + ":" + value.path("id").asString(), value.deepCopy());
                return null;
              })
          .when(store)
          .save(anyString(), any());
      when(sessions.activeById(binding.getPlatformSessionId()))
          .thenAnswer(call -> active.get() ? session.deepCopy() : null);
      when(store.byId(eq("uac_credential"), anyString(), eq(false)))
          .thenAnswer(call -> credential.deepCopy());
      when(uac.sharedCredentials(binding.getMemberId(), binding.getPlatformSessionId()))
          .thenAnswer(call -> material.deepCopy());
      tasks = authority();
    }

    SharedTasks authority() {
      SharedDshSessions ownership =
          (pool, session) -> {
            if (unavailable.get()) throw new AurigaException(CommonErrors.UNAVAILABLE);
            return currentOwnership.get();
          };
      PermissionResolver permissionResolver = (tenant, account, context) -> permissions.get();
      return new SharedTasks(
          store, sessions, uac, permissionResolver, ownership, tokens, clock, raw::get);
    }

    BeginSharedTaskResponse begin(String audience, List<String> requested) {
      return tasks.begin(
          BeginSharedTaskRequest.newBuilder()
              .setBinding(binding)
              .setTaskId(task)
              .setAudience(audience)
              .addAllScopes(requested)
              .build());
    }

    AuthorizeSharedCapabilityResponse authorize(SharedDshCapability capability) {
      return tasks.authorize(
          AuthorizeSharedCapabilityRequest.newBuilder().setCapability(capability).build());
    }

    <T> T run(Supplier<T> work) {
      return TenantContext.call(tenant, work::get);
    }
  }
}
