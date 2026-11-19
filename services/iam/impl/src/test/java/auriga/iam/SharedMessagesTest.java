package auriga.iam;

import static auriga.iam.SharedTaskTokens.*;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.contracts.iam.v1.*;
import java.math.BigInteger;
import java.util.*;
import org.junit.jupiter.api.Test;

class SharedMessagesTest {
  @Test
  void newTasksAndCapabilityRenewalKeepNativeAddressButEndedCallsCannotUseIt() throws Exception {
    var f = new SharedTasksTest.Fixture();
    try (var policy =
        mockConstruction(
            IamNetworkPolicyRevision.class,
            (mock, ctx) -> when(mock.current()).thenReturn(BigInteger.ONE))) {
      f.run(
          () -> {
            var authority =
                new SharedMessages(f.store, f.tasks, mock(IamNetworkAuthority.class), f.clock);
            var first = f.begin(MESSAGE, List.of("message.send")).getCapability();
            var context = context(f);
            f.records.put(
                "shared_message_context:" + context.getContextId(),
                Json.object(
                    "binding",
                    bindingJson(f.binding),
                    "wire",
                    Base64.getEncoder().encodeToString(context.toByteArray())));
            var request = open(context, first);
            assertEquals(context, authority.open(request));
            f.tasks.finish(
                FinishSharedTaskRequest.newBuilder()
                    .setBinding(f.binding)
                    .setTaskId(first.getTaskId())
                    .build());
            assertThrows(AurigaException.class, () -> authority.open(request));
            assertEquals(
                context,
                authority.read(
                    ReadSharedMessageContextRequest.newBuilder()
                        .setContextId(context.getContextId())
                        .build()));
            var next =
                f.tasks.begin(
                    BeginSharedTaskRequest.newBuilder()
                        .setBinding(f.binding)
                        .setTaskId(UUID.randomUUID().toString())
                        .setAudience(MESSAGE)
                        .addScopes("message.send")
                        .build());
            var reopened =
                authority.open(request.toBuilder().setCapability(next.getCapability()).build());
            assertEquals(context.getNode(), reopened.getNode());
            assertEquals(context.getSourceBinding(), reopened.getSourceBinding());
            var renewed =
                f.tasks.issue(
                    IssueSharedCapabilityRequest.newBuilder()
                        .setBinding(f.binding)
                        .setTaskId(next.getCapability().getTaskId())
                        .setAudience(MESSAGE)
                        .addScopes("message.send")
                        .build());
            assertEquals(
                reopened,
                authority.open(request.toBuilder().setCapability(renewed.getCapability()).build()));
            verify(f.store, never())
                .insert(eq("shared_message_context"), anyString(), any(), any());
            f.records.get("shared_message_context:" + context.getContextId()).put("revoked", true);
            assertThrows(
                AurigaException.class,
                () ->
                    authority.open(
                        request.toBuilder().setCapability(renewed.getCapability()).build()));
            assertThrows(
                AurigaException.class,
                () ->
                    authority.read(
                        ReadSharedMessageContextRequest.newBuilder()
                            .setContextId(context.getContextId())
                            .build()));
            return null;
          });
    }
  }

  @Test
  void exactNativeNodeBootAndFullBindingAreRequiredOnEveryReopen() throws Exception {
    var f = new SharedTasksTest.Fixture();
    try (var policy =
        mockConstruction(
            IamNetworkPolicyRevision.class,
            (mock, ctx) -> when(mock.current()).thenReturn(BigInteger.ONE))) {
      f.run(
          () -> {
            var authority =
                new SharedMessages(f.store, f.tasks, mock(IamNetworkAuthority.class), f.clock);
            var context = context(f);
            var cap = f.begin(MESSAGE, List.of("message.receive")).getCapability();
            f.records.put(
                "shared_message_context:" + context.getContextId(),
                Json.object(
                    "binding",
                    bindingJson(f.binding),
                    "wire",
                    Base64.getEncoder().encodeToString(context.toByteArray())));
            var request = open(context, cap);
            for (var changed :
                List.of(
                    request.toBuilder()
                        .setAuthenticatedNodeId(UUID.randomUUID().toString())
                        .build(),
                    request.toBuilder().setBootId(UUID.randomUUID().toString()).build(),
                    request.toBuilder().setNativeBindingId(UUID.randomUUID().toString()).build(),
                    request.toBuilder().setNativeBindingRevision("2").build()))
              assertThrows(AurigaException.class, () -> authority.open(changed));
            f.currentOwnership.set(f.binding.toBuilder().setAuthGeneration(2).build());
            assertThrows(AurigaException.class, () -> authority.open(request));
            assertThrows(
                AurigaException.class,
                () ->
                    authority.read(
                        ReadSharedMessageContextRequest.newBuilder()
                            .setContextId(context.getContextId())
                            .build()));
            return null;
          });
    }
  }

  @Test
  void oldPermissionVersionIsRejectedWhileFreshCapabilityKeepsTheIdentity() throws Exception {
    var f = new SharedTasksTest.Fixture();
    try (var policy =
        mockConstruction(
            IamNetworkPolicyRevision.class,
            (mock, ctx) -> when(mock.current()).thenReturn(BigInteger.ONE))) {
      f.run(
          () -> {
            var authority =
                new SharedMessages(f.store, f.tasks, mock(IamNetworkAuthority.class), f.clock);
            var context = context(f);
            var cap = f.begin(MESSAGE, List.of("message.send")).getCapability();
            f.records.put(
                "shared_message_context:" + context.getContextId(),
                Json.object(
                    "binding",
                    bindingJson(f.binding),
                    "wire",
                    Base64.getEncoder().encodeToString(context.toByteArray())));
            var request = open(context, cap);
            f.permissions.set(Set.of());
            assertThrows(AurigaException.class, () -> authority.open(request));
            f.permissions.set(Set.of("collab.messages.use"));
            assertThrows(AurigaException.class, () -> authority.open(request));
            var updated =
                f.tasks.issue(
                    IssueSharedCapabilityRequest.newBuilder()
                        .setBinding(f.binding)
                        .setTaskId(cap.getTaskId())
                        .setAudience(MESSAGE)
                        .addScopes("message.send")
                        .build());
            assertEquals(
                context,
                authority.open(request.toBuilder().setCapability(updated.getCapability()).build()));
            return null;
          });
    }
  }

  static SharedMessageContext context(SharedTasksTest.Fixture f) {
    String op = UUID.randomUUID().toString();
    return SharedMessageContext.newBuilder()
        .setBinding(f.binding)
        .setContextId(op)
        .setOperationId(op)
        .setAuthenticatedNodeId(UUID.randomUUID().toString())
        .setBootId(UUID.randomUUID().toString())
        .setNativeBindingId(UUID.randomUUID().toString())
        .setNativeBindingRevision("1")
        .setRealm("9223372036854775809")
        .setNode("1")
        .setIncarnation(UUID.randomUUID().toString())
        .setChannelNonce(UUID.randomUUID().toString())
        .setSourceBinding(UUID.randomUUID().toString())
        .setSourceRevision(1)
        .setPolicyRevision(1)
        .setNotBefore(f.clock.instant().toString())
        .setExpiresAt(f.clock.instant().plusSeconds(3600).toString())
        .build();
  }

  static OpenSharedMessageContextRequest open(
      SharedMessageContext c, auriga.contracts.node.v1.SharedDshCapability cap) {
    return OpenSharedMessageContextRequest.newBuilder()
        .setCapability(cap)
        .setOperationId(c.getOperationId())
        .setAuthenticatedNodeId(c.getAuthenticatedNodeId())
        .setBootId(c.getBootId())
        .setNativeBindingId(c.getNativeBindingId())
        .setNativeBindingRevision(c.getNativeBindingRevision())
        .build();
  }
}
