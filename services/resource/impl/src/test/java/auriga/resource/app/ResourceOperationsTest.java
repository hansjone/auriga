package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.events.EventEnvelope;
import auriga.common.events.EventPublisher;
import auriga.common.security.AurigaPrincipal;
import auriga.resource.persistence.ResourceStore;
import auriga.resource.persistence.ResourceWorkflowStore;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.UUID;
import java.util.function.Supplier;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.JsonNode;

class ResourceOperationsTest {
  private static final String OP = "catalog/example";
  private static final String KEY = "attempt-1";
  private static final String ETAG = "\"1\"";
  // SHA-256 of catalog/example, the quoted ETag and the canonical body, separated by LF.
  private static final String FINGERPRINT =
      "91f23d4ce8e4e53c178e94b4dee977f86c0d1181cf10484407a0a8110c550586";

  private final UUID tenant = UUID.randomUUID();
  private final UUID account = UUID.randomUUID();
  private final UUID target = UUID.randomUUID();
  private final Instant now = Instant.parse("2026-11-18T00:00:00Z");
  private final Clock clock = Clock.fixed(now, ZoneOffset.UTC);
  private final AurigaPrincipal actor = mock(AurigaPrincipal.class);
  private final ResourceAccess.Reader reader =
      new ResourceAccess.Reader(tenant, account, false, Set.of(), true);
  private final ResourceStore tx = mock(ResourceStore.class);
  private final ResourceWorkflowStore db = mock(ResourceWorkflowStore.class);
  private final ResourceAccess access = mock(ResourceAccess.class);
  private final EventPublisher events = mock(EventPublisher.class);
  private final ResourceOperations operations =
      new ResourceOperations(tx, db, access, events, clock);
  private final CatalogWorkflow catalog =
      new CatalogWorkflow(
          tx, db, access, operations, () -> null, () -> null, () -> true, null, events, clock);
  private final Runnable guard = mock(Runnable.class);

  @BeforeEach
  void fixture() {
    when(actor.subject()).thenReturn(account.toString());
    when(access.member(actor)).thenReturn(reader);
    when(tx.write(any()))
        .thenAnswer(
            call -> {
              assertEquals(tenant, TenantContext.require());
              return call.<Supplier<?>>getArgument(0).get();
            });
  }

  private JsonNode request(boolean replayResponse, Supplier<JsonNode> action) {
    return TenantContext.call(
        tenant,
        () ->
            catalog.mutate(
                actor,
                ResourceAccess.READ,
                false,
                "example",
                KEY,
                ETAG,
                JSON.createObjectNode().put("z", true).put("a", 1),
                r -> guard.run(),
                r -> action.get(),
                replayResponse));
  }

  @Test
  void catalogCanReplayAnExistingReceiptAfterAuthorizationAndGuard() {
    var saved = JSON.createObjectNode().put("revision", 2);
    when(db.replay(account, OP, KEY, FINGERPRINT, now)).thenReturn(Optional.of(saved));

    assertEquals(saved, request(true, () -> fail("A replay must not execute the mutation")));

    var order = inOrder(access, tx, db, guard);
    order.verify(access).member(actor);
    order.verify(tx).write(any());
    order.verify(db).lock(account + ":" + OP + ":" + KEY);
    order.verify(access).member(actor);
    order.verify(guard).run();
    order.verify(db).replay(account, OP, KEY, FINGERPRINT, now);
    verify(db, never()).remember(any(), any(), any(), any(), any(), any());
    verifyNoInteractions(events);
  }

  @Test
  void authorizationLostInsideTheTransactionPreventsReplay() {
    when(access.member(actor))
        .thenReturn(reader)
        .thenThrow(new AurigaException(CommonErrors.FORBIDDEN));

    var error =
        assertThrows(
            AurigaException.class, () -> request(true, () -> fail("Authorization was revoked")));

    assertEquals(CommonErrors.FORBIDDEN, error.errorCode());
    verify(db).lock(account + ":" + OP + ":" + KEY);
    verifyNoMoreInteractions(db);
    verifyNoInteractions(guard, events);
  }

  @Test
  void oneTimeResponseStoresOnlyCompletionAndAuditsTheOriginalMutation() {
    var response = JSON.createObjectNode().put("ticket", "fixture-ticket");
    var completed = JSON.createObjectNode().put("completed", true);
    when(db.replay(account, OP, KEY, FINGERPRINT, now))
        .thenReturn(Optional.empty(), Optional.of(completed));

    assertEquals(
        response,
        request(
            false,
            () -> {
              catalog.assignmentRecorded(reader, target);
              return response;
            }));
    var error =
        assertThrows(
            AurigaException.class, () -> request(false, () -> fail("One-time receipt replayed")));

    assertEquals(CommonErrors.CONFLICT, error.errorCode());
    var order = inOrder(events, db);
    order
        .verify(events)
        .publish(
            EventEnvelope.AUDIT_TYPE,
            target.toString(),
            Map.of(
                "actor",
                Map.of("type", "user", "id", account.toString()),
                "action",
                "resource.distribution.request",
                "target",
                Map.of("type", "resource", "id", target.toString()),
                "result",
                "success"));
    order.verify(db).remember(account, OP, KEY, FINGERPRINT, completed, now);
    verifyNoMoreInteractions(events);
    verify(db).remember(any(), any(), any(), any(), any(), any());
  }
}
