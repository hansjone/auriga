package io.github.hansjone.auriga.common.events;

import static org.junit.jupiter.api.Assertions.*;

import io.github.hansjone.auriga.common.context.RequestContext;
import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.events.InboxProcessor.Outcome;
import io.github.hansjone.auriga.common.observability.EventMetrics;
import io.github.hansjone.auriga.common.persistence.CommonMigrations;
import io.github.hansjone.auriga.common.persistence.TenantTransactionManager;
import io.github.hansjone.auriga.testsupport.PostgresExtension;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import java.time.Clock;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class InboxProcessorTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private static final UUID TENANT = UUID.randomUUID();
  private static final String SUBJECT = "auriga.evt.user.created";
  private static PostgresExtension.ServiceRole role;
  private static JdbcTemplate jdbc;

  private SimpleMeterRegistry registry;
  private InboxProcessor processor;

  @BeforeAll
  static void schema() {
    role = PostgresExtension.serviceRole(InboxProcessorTest.class, "inb_app", "inb");
    CommonMigrations.migrate(role.dataSource(), "inb");
    jdbc = new JdbcTemplate(role.dataSource());
    jdbc.execute("CREATE TABLE inb.seen (event_id uuid PRIMARY KEY, tenant_id uuid NOT NULL)");
    jdbc.execute(CommonMigrations.enableTenantRls("inb", "seen"));
  }

  @BeforeEach
  void setUp() {
    jdbc.update("DELETE FROM inb.processed_event");
    jdbc.update("DELETE FROM inb.dead_letter_event");
    // RLS hides tenant rows from the service role without a tenant; clean up as superuser
    new JdbcTemplate(PostgresExtension.database(InboxProcessorTest.class).dataSource())
        .update("DELETE FROM inb.seen");
    registry = new SimpleMeterRegistry();
    processor =
        new InboxProcessor(
            new TenantTransactionManager(role.dataSource()),
            jdbc,
            "inb",
            new EventMetrics(registry, "inb"),
            Clock.systemUTC(),
            3,
            EventProducers.of(null));
  }

  private static byte[] event(UUID id) {
    return event(id, "auriga/org");
  }

  private static byte[] event(UUID id, String source) {
    ObjectNode e = EventSchemasTest.envelope("user.created", EventSchemasTest.userCreated());
    e.put("source", source);
    e.put("id", id.toString());
    e.put("tenantid", TENANT.toString());
    e.put("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01");
    e.put("unknownattribute", "ignored");
    return JSON.writeValueAsBytes(e);
  }

  /** Records into an RLS table: proves the tenant is bound in the handler's transaction. */
  private static final class Recording implements EventHandler {
    final List<UUID> handled = new ArrayList<>();
    final AtomicInteger failuresLeft;
    String seenTrace;

    Recording(int failures) {
      this.failuresLeft = new AtomicInteger(failures);
    }

    @Override
    public String name() {
      return "recording";
    }

    @Override
    public Set<String> eventTypes() {
      return Set.of("user.created");
    }

    @Override
    public void handle(ReceivedEvent event) {
      assertEquals(TENANT, TenantContext.require());
      seenTrace = RequestContext.current().orElseThrow().trace().traceId();
      jdbc.update("INSERT INTO inb.seen VALUES (?, ?)", event.id(), event.tenantId());
      if (failuresLeft.getAndDecrement() > 0) throw new IllegalStateException("downstream failed");
      handled.add(event.id());
    }
  }

  private long processedRows() {
    return jdbc.queryForObject("SELECT count(*) FROM inb.processed_event", Long.class);
  }

  @Test
  void processesOnceAndDeduplicatesById() {
    Recording handler = new Recording(0);
    UUID id = UUID.randomUUID();
    assertEquals(Outcome.PROCESSED, processor.process("c1", handler, SUBJECT, event(id), 1));
    assertEquals(Outcome.DUPLICATE, processor.process("c1", handler, SUBJECT, event(id), 2));
    assertEquals(Outcome.DUPLICATE, processor.process("c1", handler, SUBJECT, event(id), 1));
    assertEquals(List.of(id), handler.handled);
    assertEquals("4bf92f3577b34da6a3ce929d0e0e4736", handler.seenTrace);
    // another consumer processes the same event independently
    assertEquals(
        Outcome.PROCESSED,
        processor.process("c2", new Recording(0), SUBJECT, event(UUID.randomUUID()), 1));
    // the handler's row was written under the event's tenant
    assertEquals(
        2L,
        TenantContext.<Long, RuntimeException>call(
            TENANT,
            () ->
                new org.springframework.transaction.support.TransactionTemplate(
                        new TenantTransactionManager(role.dataSource()))
                    .execute(
                        s -> jdbc.queryForObject("SELECT count(*) FROM inb.seen", Long.class))));
  }

  @Test
  void failureRollsBackHandlerAndDedupRecordThenDeadLetters() {
    Recording handler = new Recording(10);
    UUID id = UUID.randomUUID();
    assertEquals(Outcome.RETRY, processor.process("c1", handler, SUBJECT, event(id), 1));
    assertEquals(0, processedRows());
    assertEquals(Outcome.RETRY, processor.process("c1", handler, SUBJECT, event(id), 2));
    assertEquals(Outcome.DEAD_LETTERED, processor.process("c1", handler, SUBJECT, event(id), 3));
    assertEquals(0, processedRows());
    Map<String, Object> dead = jdbc.queryForMap("SELECT * FROM inb.dead_letter_event");
    assertEquals(id, dead.get("event_id"));
    assertEquals("c1", dead.get("consumer"));
    assertEquals(3, dead.get("attempts"));
    assertTrue(((String) dead.get("error")).contains("downstream failed"));
    assertEquals(
        3.0,
        registry.get("auriga.events.consume.failures").tag("consumer", "c1").counter().count());
    assertEquals(
        1.0,
        registry.get("auriga.events.dead_lettered").tag("type", "user.created").counter().count());
  }

  @Test
  void retrySucceedsAfterTransientFailure() {
    Recording handler = new Recording(1);
    UUID id = UUID.randomUUID();
    assertEquals(Outcome.RETRY, processor.process("c1", handler, SUBJECT, event(id), 1));
    assertEquals(Outcome.PROCESSED, processor.process("c1", handler, SUBJECT, event(id), 2));
    assertEquals(List.of(id), handler.handled);
    assertEquals(1, processedRows());
  }

  @Test
  void malformedMessageIsDeadLetteredImmediately() {
    Recording handler = new Recording(0);
    assertEquals(
        Outcome.DEAD_LETTERED, processor.process("c1", handler, SUBJECT, "not json".getBytes(), 1));
    assertEquals(
        Outcome.DEAD_LETTERED,
        processor.process("c1", handler, SUBJECT, "{\"id\":\"x\",\"type\":\"a.b\"}".getBytes(), 1));
    assertTrue(handler.handled.isEmpty());
    assertEquals(2L, jdbc.queryForObject("SELECT count(*) FROM inb.dead_letter_event", Long.class));
  }

  @Test
  void forgedEventsAreDeadLetteredWithoutRunningTheHandler() {
    Recording handler = new Recording(0);
    // wrong subject for the type (e.g. published on another type's subject)
    assertEquals(
        Outcome.DEAD_LETTERED,
        processor.process("c1", handler, "auriga.evt.user.deleted", event(UUID.randomUUID()), 1));
    // a service that is not the contract's producer of user.* (workspace forging org events)
    assertEquals(
        Outcome.DEAD_LETTERED,
        processor.process("c1", handler, SUBJECT, event(UUID.randomUUID(), "auriga/workspace"), 1));
    assertEquals(
        Outcome.DEAD_LETTERED,
        processor.process("c1", handler, SUBJECT, event(UUID.randomUUID(), "evil/org"), 1));
    // a type the handler never subscribed to
    ObjectNode other = EventSchemasTest.envelope("identity.login", EventSchemasTest.userCreated());
    other.put("tenantid", TENANT.toString());
    other.put("source", "auriga/iam");
    assertEquals(
        Outcome.DEAD_LETTERED,
        processor.process(
            "c1", handler, "auriga.evt.identity.login", JSON.writeValueAsBytes(other), 1));
    assertTrue(handler.handled.isEmpty());
    assertEquals(0, processedRows());
    assertEquals(4L, jdbc.queryForObject("SELECT count(*) FROM inb.dead_letter_event", Long.class));
    assertEquals(
        4.0, registry.get("auriga.events.rejected").tag("consumer", "c1").counter().count());
  }

  @Test
  void producerMappingIsConfigurable() {
    EventProducers producers = EventProducers.of(Map.of("user.*", List.of("org", "migration")));
    assertTrue(producers.allows("user.created", "auriga/migration"));
    assertTrue(producers.allows("identity.login", "auriga/iam"));
    assertFalse(producers.allows("identity.login", "auriga/org"));
    assertFalse(producers.allows("widget.created", "auriga/org"), "unknown types fail closed");
    EventProducers exact =
        EventProducers.of(Map.of("user.deleted", List.of("gdpr"), "widget.created", List.of("w")));
    assertTrue(exact.allows("user.deleted", "auriga/gdpr"));
    assertFalse(exact.allows("user.deleted", "auriga/org"), "exact key wins over prefix");
    assertTrue(exact.allows("widget.created", "auriga/w"));
  }

  @Test
  void auditRecordsMustArriveOnTheSourceServicesSubject() {
    EventHandler audit =
        new EventHandler() {
          @Override
          public String name() {
            return "audit";
          }

          @Override
          public Set<String> eventTypes() {
            return Set.of(EventEnvelope.AUDIT_TYPE);
          }

          @Override
          public void handle(ReceivedEvent event) {}
        };
    ReceivedEvent fromOrg =
        ReceivedEvent.parse(
            JSON.writeValueAsBytes(
                EventSchemasTest.envelope(EventEnvelope.AUDIT_TYPE, Map.of("a", 1))));
    processor.verifyOrigin(audit, "auriga.audit.org", fromOrg);
    assertThrows(
        IllegalArgumentException.class,
        () -> processor.verifyOrigin(audit, "auriga.audit.iam", fromOrg));
    assertThrows(
        IllegalArgumentException.class,
        () -> processor.verifyOrigin(audit, "auriga.evt.audit.recorded", fromOrg));
  }
}
