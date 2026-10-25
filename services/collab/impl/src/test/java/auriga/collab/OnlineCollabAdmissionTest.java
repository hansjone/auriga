package auriga.collab;

import static org.assertj.core.api.Assertions.assertThatThrownBy;

import auriga.collab.api.CollabAdmission.Operation;
import auriga.collab.app.OnlineCollabAdmission;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.contracts.collab.v1.Actor;
import auriga.contracts.org.v1.GetEffectivePermissionsResponse;
import auriga.contracts.org.v1.MemberStatus;
import io.grpc.Status;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;

class OnlineCollabAdmissionTest {
  @Test
  void repeatsAuthorityForReceiveAndEveryOperationAndFailsClosed() {
    UUID tenant = UUID.randomUUID();
    Actor actor =
        Actor.newBuilder()
            .setTenantId(tenant.toString())
            .setAccountId(UUID.randomUUID().toString())
            .setCredentialKind("DEVICE")
            .setCredentialSubject(UUID.randomUUID().toString())
            .build();
    var calls = new AtomicInteger();
    var permission =
        new AtomicReference<>(
            GetEffectivePermissionsResponse.newBuilder()
                .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                .addPermissions("collab.messages.use")
                .addPermissions("auriga.connect.collab.use")
                .build());
    var admission = new OnlineCollabAdmission(a -> calls.incrementAndGet(), a -> permission.get());
    TenantContext.run(
        tenant,
        () -> {
          for (Operation operation : Operation.values()) admission.verify(actor, operation);
          org.assertj.core.api.Assertions.assertThat(calls.get())
              .isEqualTo(Operation.values().length);
          permission.set(
              permission.get().toBuilder().setStatus(MemberStatus.MEMBER_STATUS_DISABLED).build());
          denied(() -> admission.verify(actor, Operation.RECEIVE), "common.forbidden");
          permission.set(
              permission.get().toBuilder()
                  .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                  .clearPermissions()
                  .addPermissions("ai.models.invoke")
                  .build());
          denied(() -> admission.verify(actor, Operation.OPEN), "common.forbidden");
          denied(
              () ->
                  admission.verify(
                      actor.toBuilder().setTenantId(UUID.randomUUID().toString()).build(),
                      Operation.ACK),
              "common.forbidden");
          denied(
              () ->
                  admission.verify(
                      actor.toBuilder().setCredentialKind("INSTANCE").build(), Operation.OPEN),
              "common.forbidden");
          var offline =
              new OnlineCollabAdmission(
                  a -> {
                    throw Status.UNAVAILABLE.asRuntimeException();
                  },
                  a -> permission.get());
          denied(() -> offline.verify(actor, Operation.OPEN), "common.unavailable");
          var revoked =
              new OnlineCollabAdmission(
                  a -> {
                    throw Status.PERMISSION_DENIED.asRuntimeException();
                  },
                  a -> permission.get());
          denied(() -> revoked.verify(actor, Operation.RECEIVE), "common.forbidden");
        });
  }

  private static void denied(Runnable work, String code) {
    assertThatThrownBy(work::run)
        .isInstanceOfSatisfying(
            AurigaException.class,
            e -> org.assertj.core.api.Assertions.assertThat(e.errorCode().code()).isEqualTo(code));
  }
}
