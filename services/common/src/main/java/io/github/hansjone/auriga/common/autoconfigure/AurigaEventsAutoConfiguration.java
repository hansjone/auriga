package io.github.hansjone.auriga.common.autoconfigure;

import io.github.hansjone.auriga.common.audit.AuditLogger;
import io.github.hansjone.auriga.common.audit.OutboxAuditLogger;
import io.github.hansjone.auriga.common.events.EventHandler;
import io.github.hansjone.auriga.common.events.EventProducers;
import io.github.hansjone.auriga.common.events.EventPublisher;
import io.github.hansjone.auriga.common.events.EventSchemas;
import io.github.hansjone.auriga.common.events.EventTransport;
import io.github.hansjone.auriga.common.events.InboxProcessor;
import io.github.hansjone.auriga.common.events.NatsEventConsumer;
import io.github.hansjone.auriga.common.events.NatsEvents;
import io.github.hansjone.auriga.common.events.OutboxEventPublisher;
import io.github.hansjone.auriga.common.events.OutboxForwarder;
import io.github.hansjone.auriga.common.observability.EventMetrics;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import io.nats.client.Connection;
import java.time.Clock;
import java.util.List;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.boot.autoconfigure.condition.ConditionalOnSingleCandidate;
import org.springframework.context.SmartLifecycle;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.PlatformTransactionManager;
import tools.jackson.databind.json.JsonMapper;

/**
 * Outbox publishing and audit (needs {@code auriga.service.name} and {@code .schema}); NATS
 * forwarding and consumers additionally need {@code auriga.events.nats-url}.
 */
@AutoConfiguration(after = AurigaPersistenceAutoConfiguration.class)
@ConditionalOnSingleCandidate(DataSource.class)
@ConditionalOnProperty(
    prefix = "auriga.service",
    name = {"name", "schema"})
public class AurigaEventsAutoConfiguration {
  @Bean
  @ConditionalOnMissingBean
  EventMetrics aurigaEventMetrics(
      ObjectProvider<MeterRegistry> registry, AurigaProperties properties) {
    return new EventMetrics(
        registry.getIfAvailable(SimpleMeterRegistry::new), properties.getService().getName());
  }

  @Bean
  @ConditionalOnMissingBean
  EventPublisher aurigaEventPublisher(
      DataSource dataSource,
      AurigaProperties properties,
      ObjectProvider<JsonMapper> jsonMapper,
      Clock clock,
      ObjectProvider<OutboxForwarder> forwarder) {
    return new OutboxEventPublisher(
        new JdbcTemplate(dataSource),
        properties.getService().getSchema(),
        properties.getService().getName(),
        properties.getEvents().isValidateSchemas() ? new EventSchemas() : null,
        jsonMapper.getIfAvailable(() -> JsonMapper.builder().build()),
        clock,
        () -> forwarder.ifAvailable(OutboxForwarder::wakeUp));
  }

  @Bean
  @ConditionalOnMissingBean
  AuditLogger aurigaAuditLogger(EventPublisher publisher) {
    return new OutboxAuditLogger(publisher);
  }

  @Bean
  @ConditionalOnMissingBean
  InboxProcessor aurigaInboxProcessor(
      PlatformTransactionManager transactionManager,
      DataSource dataSource,
      AurigaProperties properties,
      EventMetrics metrics,
      Clock clock) {
    return new InboxProcessor(
        transactionManager,
        new JdbcTemplate(dataSource),
        properties.getService().getSchema(),
        metrics,
        clock,
        properties.getEvents().getMaxDeliveries(),
        EventProducers.of(properties.getEvents().getProducers()));
  }

  @Configuration(proxyBeanMethods = false)
  @ConditionalOnProperty(prefix = "auriga.events", name = "nats-url")
  static class NatsTransport {
    @Bean(destroyMethod = "close")
    @ConditionalOnMissingBean
    Connection aurigaNatsConnection(AurigaProperties properties) throws Exception {
      Connection connection =
          io.nats.client.Nats.connect(
              NatsEvents.options(
                  properties.getEvents().getNatsUrl(),
                  "auriga-" + properties.getService().getName(),
                  properties.getEvents().getNatsCredsFile(),
                  properties.getEvents().getNatsNkeySeedFile(),
                  properties.getEvents().isNatsTls()));
      if (properties.getEvents().isEnsureStreams()) {
        NatsEvents.ensureStreams(connection, properties.getEvents().getDuplicateWindow());
      }
      return connection;
    }

    @Bean
    @ConditionalOnMissingBean
    EventTransport aurigaEventTransport(Connection connection) {
      return NatsEvents.transport(connection);
    }

    @Bean
    @ConditionalOnMissingBean
    OutboxForwarder aurigaOutboxForwarder(
        DataSource dataSource,
        EventTransport transport,
        EventMetrics metrics,
        Clock clock,
        AurigaProperties properties) {
      AurigaProperties.Events e = properties.getEvents();
      return new OutboxForwarder(
          dataSource,
          properties.getService().getSchema(),
          transport,
          metrics,
          clock,
          new OutboxForwarder.Settings(
              e.getBatchSize(),
              e.getPollInterval(),
              e.getBaseBackoff(),
              e.getMaxBackoff(),
              e.getRetention()));
    }

    @Bean
    EventConsumers aurigaEventConsumers(
        Connection connection,
        List<EventHandler> handlers,
        InboxProcessor processor,
        AurigaProperties properties) {
      return new EventConsumers(
          handlers.stream()
              .map(
                  h ->
                      new NatsEventConsumer(
                          connection,
                          properties.getService().getName(),
                          h,
                          processor,
                          properties.getEvents().getAckWait(),
                          properties.getEvents().getBaseBackoff()))
              .toList());
    }
  }

  /** Starts/stops all consumers together. */
  public static final class EventConsumers implements SmartLifecycle {
    private final List<NatsEventConsumer> consumers;
    private volatile boolean running;

    public EventConsumers(List<NatsEventConsumer> consumers) {
      this.consumers = List.copyOf(consumers);
    }

    public List<NatsEventConsumer> consumers() {
      return consumers;
    }

    @Override
    public void start() {
      consumers.forEach(NatsEventConsumer::start);
      running = true;
    }

    @Override
    public void stop() {
      consumers.forEach(NatsEventConsumer::stop);
      running = false;
    }

    @Override
    public boolean isRunning() {
      return running;
    }
  }
}
