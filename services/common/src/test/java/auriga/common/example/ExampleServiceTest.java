package auriga.common.example;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.MissingTenantException;
import auriga.common.context.TenantContext;
import auriga.common.events.NatsEvents;
import auriga.common.example.ExampleService.Widget;
import auriga.common.example.ExampleService.WidgetSeenHandler;
import auriga.common.example.ExampleService.WidgetService;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.NatsExtension;
import auriga.testsupport.PostgresExtension;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import io.nats.client.Connection;
import io.nats.client.api.MessageInfo;
import io.nats.client.impl.Headers;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.BooleanSupplier;
import javax.sql.DataSource;
import org.apache.ibatis.annotations.Mapper;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mybatis.spring.annotation.MapperScan;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.SpringBootConfiguration;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Import;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.transaction.PlatformTransactionManager;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/**
 * End to end through the auto-configuration: MyBatis-Plus tenant line + RLS, Flyway with common
 * migrations, outbox in the business transaction, forwarder to a real NATS JetStream, durable
 * consumer with de-duplication and dead letters.
 */
@SpringBootTest(
    classes = ExampleServiceTest.App.class,
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = {
      "auriga.service.name=example",
      "auriga.service.schema=example",
      "spring.flyway.schemas=example",
      "spring.flyway.locations=classpath:db/example",
      "auriga.events.poll-interval=100ms",
      "auriga.events.base-backoff=100ms",
      "auriga.events.max-deliveries=3",
      "auriga.events.producers[widget.created]=example",
      "auriga.events.ack-wait=5s"
    })
@ExtendWith({PostgresExtension.class, NatsExtension.class})
@DirtiesContext
class ExampleServiceTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();

  @SpringBootConfiguration
  @EnableAutoConfiguration
  @MapperScan(basePackageClasses = ExampleService.class, annotationClass = Mapper.class)
  @Import(WidgetService.class)
  static class App {
    @Bean
    WidgetSeenHandler widgetSeenHandler(DataSource dataSource) {
      return new WidgetSeenHandler(new JdbcTemplate(dataSource));
    }

    @Bean
    MeterRegistry meterRegistry() {
      return new SimpleMeterRegistry();
    }
  }

  @DynamicPropertySource
  static void props(DynamicPropertyRegistry registry) {
    // production shape: Flyway as the schema owner, the service as a DML-only runtime role
    PostgresExtension.serviceRolesProperties(
        ExampleServiceTest.class, "example_owner", "example_app", "example", registry);
    NatsExtension.properties(registry);
  }

  @Autowired WidgetService widgets;
  @Autowired WidgetSeenHandler handler;
  @Autowired Connection nats;
  @Autowired MeterRegistry meters;
  @Autowired PlatformTransactionManager transactionManager;

  private final JdbcTemplate admin =
      new JdbcTemplate(PostgresExtension.database(ExampleServiceTest.class).dataSource());

  private static void await(String what, BooleanSupplier condition) throws InterruptedException {
    long deadline = System.nanoTime() + Duration.ofSeconds(30).toNanos();
    while (!condition.getAsBoolean()) {
      if (System.nanoTime() > deadline) fail("Timed out waiting for " + what);
      Thread.sleep(50);
    }
  }

  private static boolean causedBy(Throwable t, Class<? extends Throwable> type) {
    for (Throwable c = t; c != null; c = c.getCause()) if (type.isInstance(c)) return true;
    return false;
  }

  @Test
  void usesTenantAwareTransactionManager() {
    assertInstanceOf(TenantTransactionManager.class, transactionManager);
  }

  @Test
  void myBatisPlusScopesEveryStatementToTheTenant() {
    UUID a = UUID.randomUUID();
    UUID b = UUID.randomUUID();
    UUID wa = TenantContext.call(a, () -> widgets.create("alpha"));
    TenantContext.call(b, () -> widgets.create("beta"));

    List<Widget> seenByA = TenantContext.call(a, widgets::list);
    assertEquals(List.of(wa), seenByA.stream().map(Widget::getId).toList());
    assertEquals(a, seenByA.getFirst().getTenantId());

    // explicit system-level query skips the tenant line, RLS still limits it to the tenant
    assertEquals(
        List.of(wa),
        TenantContext.call(a, widgets::listIgnoringTenantLine).stream()
            .map(Widget::getId)
            .toList());
    assertEquals(List.of(), TenantContext.callAsSystem(widgets::listIgnoringTenantLine));
    // the superuser sees both rows: they really exist
    assertEquals(
        2L,
        admin.queryForObject(
            "SELECT count(*) FROM example.widget WHERE tenant_id IN (?, ?)", Long.class, a, b));
  }

  @Test
  void noTenantContextIsAnErrorNotAFullScan() {
    RuntimeException e = assertThrows(RuntimeException.class, widgets::list);
    assertTrue(causedBy(e, MissingTenantException.class), e.toString());
    RuntimeException insert = assertThrows(RuntimeException.class, () -> widgets.create("x"));
    assertTrue(causedBy(insert, MissingTenantException.class), insert.toString());
  }

  @Test
  void rollbackDiscardsBusinessDataAndEvent() {
    UUID tenant = UUID.randomUUID();
    assertThrows(
        IllegalStateException.class,
        () -> TenantContext.call(tenant, () -> widgets.createThenFail("doomed")));
    assertEquals(
        0L,
        admin.queryForObject(
            "SELECT count(*) FROM example.widget WHERE tenant_id = ?", Long.class, tenant));
    assertEquals(
        0L,
        admin.queryForObject(
            "SELECT count(*) FROM example.outbox_event WHERE tenant_id = ?", Long.class, tenant));
  }

  @Test
  void eventAndAuditTravelThroughNatsAndAreConsumedOnce() throws Exception {
    UUID tenant = UUID.randomUUID();
    UUID widget = TenantContext.call(tenant, () -> widgets.create("gamma"));
    String eventId =
        admin.queryForObject(
            "SELECT id::text FROM example.outbox_event WHERE tenant_id = ? AND event_type = 'widget.created'",
            String.class,
            tenant);
    String auditId =
        admin.queryForObject(
            "SELECT id::text FROM example.outbox_event WHERE tenant_id = ? AND event_type = 'audit.recorded'",
            String.class,
            tenant);

    await(
        "consumer to record the widget",
        () ->
            admin.queryForObject(
                    "SELECT count(*) FROM example.widget_seen WHERE widget_id = ?",
                    Long.class,
                    widget)
                == 1);
    // the forwarder marks published_at after the broker ack, which can lag the consumer
    await(
        "forwarder to mark both events published",
        () ->
            admin.queryForObject(
                    "SELECT count(*) FROM example.outbox_event WHERE tenant_id = ? AND published_at IS NULL",
                    Long.class,
                    tenant)
                == 0);
    assertEquals(
        tenant,
        admin.queryForObject(
            "SELECT tenant_id FROM example.widget_seen WHERE widget_id = ?", UUID.class, widget));
    assertEquals(
        0L,
        admin.queryForObject(
            "SELECT count(*) FROM example.outbox_event WHERE tenant_id = ? AND published_at IS NULL",
            Long.class,
            tenant));

    // the message on the stream carries Nats-Msg-Id = event id
    MessageInfo onStream =
        nats.jetStreamManagement()
            .getLastMessage(NatsEvents.EVENTS_STREAM, "auriga.evt.widget.created");
    assertEquals(eventId, onStream.getHeaders().getFirst(NatsEvents.MSG_ID_HEADER));
    MessageInfo audit =
        nats.jetStreamManagement().getLastMessage(NatsEvents.AUDIT_STREAM, "auriga.audit.example");
    assertEquals(auditId, audit.getHeaders().getFirst(NatsEvents.MSG_ID_HEADER));
    String auditJson = new String(audit.getData());
    assertTrue(auditJson.contains("\"apiKey\":{\"redacted\":true}"), auditJson);

    // redeliver the same event under a different broker message id: processed only once
    assertEquals(1, handler.invocationsOf(eventId));
    Headers headers = new Headers();
    headers.put(NatsEvents.MSG_ID_HEADER, UUID.randomUUID().toString());
    long duplicateSeq =
        nats.jetStream().publish("auriga.evt.widget.created", headers, onStream.getData()).getSeqno();
    // wait until the consumer has acknowledged the duplicate's own stream sequence
    await("duplicate to be acknowledged", () -> ackedThrough(duplicateSeq));
    assertEquals(1, handler.invocationsOf(eventId));
    assertEquals(
        1L,
        admin.queryForObject(
            "SELECT count(*) FROM example.widget_seen WHERE widget_id = ?", Long.class, widget));
    assertEquals(
        0L,
        admin.queryForObject(
            "SELECT count(*) FROM example.dead_letter_event WHERE event_id = ?::uuid",
            Long.class,
            eventId));
  }

  private boolean ackedThrough(long streamSeq) {
    try {
      var info =
          nats.jetStreamManagement()
              .getConsumerInfo(NatsEvents.EVENTS_STREAM, "example-widget-seen");
      return info.getAckFloor().getStreamSequence() >= streamSeq;
    } catch (Exception e) {
      return false;
    }
  }

  private boolean consumerIdle() {
    try {
      var info =
          nats.jetStreamManagement()
              .getConsumerInfo(NatsEvents.EVENTS_STREAM, "example-widget-seen");
      return info.getNumPending() == 0 && info.getNumAckPending() == 0;
    } catch (Exception e) {
      return false;
    }
  }

  @Test
  void poisonEventIsDeadLetteredAfterMaxDeliveries() throws Exception {
    UUID tenant = UUID.randomUUID();
    TenantContext.call(tenant, () -> widgets.create("poison-pill"));
    await(
        "dead letter",
        () ->
            admin.queryForObject(
                    "SELECT count(*) FROM example.dead_letter_event WHERE tenant_id = ?",
                    Long.class,
                    tenant)
                == 1);
    Map<String, Object> dead =
        admin.queryForMap("SELECT * FROM example.dead_letter_event WHERE tenant_id = ?", tenant);
    assertEquals("example-widget-seen", dead.get("consumer"));
    assertEquals(3, dead.get("attempts"));
    assertTrue(((String) dead.get("error")).contains("cannot handle poison"));
    ObjectNode payload = (ObjectNode) JSON.readTree((String) dead.get("payload"));
    assertEquals("widget.created", payload.get("type").asString());
    assertTrue(
        meters
                .get("auriga.events.dead_lettered")
                .tag("consumer", "example-widget-seen")
                .counter()
                .count()
            >= 1);
    await("consumer to settle", this::consumerIdle);
  }
}
