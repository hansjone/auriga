package io.github.hansjone.auriga.common.events;

import io.github.hansjone.auriga.common.observability.EventMetrics;
import io.github.hansjone.auriga.common.persistence.CommonMigrations;
import java.nio.charset.StandardCharsets;
import java.sql.Timestamp;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Objects;
import java.util.UUID;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import javax.sql.DataSource;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.context.SmartLifecycle;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.datasource.DataSourceTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/**
 * Moves committed outbox rows to the broker.
 *
 * <ul>
 *   <li>Rows are claimed with {@code FOR UPDATE SKIP LOCKED}, so any number of replicas can run
 *       concurrently without sending a row twice in normal operation.
 *   <li>Only the oldest unpublished row per {@code subject_key} is eligible, so events of one
 *       subject go out in order even across replicas and retries (head-of-line blocking per
 *       subject).
 *   <li>Failures back off exponentially (base * 2^attempts, capped) and are retried forever; the
 *       backlog gauge and failure counter are the alert signals.
 *   <li>A crash between broker ack and commit re-sends the row; {@code Nats-Msg-Id} lets JetStream
 *       drop it within its duplicate window and consumers de-duplicate by id anyway.
 * </ul>
 */
public final class OutboxForwarder implements SmartLifecycle {
  private static final Logger log = LoggerFactory.getLogger(OutboxForwarder.class);

  /** Tunables; defaults suit a small deployment. */
  public record Settings(
      int batchSize,
      Duration pollInterval,
      Duration baseBackoff,
      Duration maxBackoff,
      Duration retention) {
    public static Settings defaults() {
      return new Settings(
          100,
          Duration.ofMillis(500),
          Duration.ofSeconds(1),
          Duration.ofMinutes(5),
          Duration.ofDays(7));
    }
  }

  private record Claimed(
      UUID id, String natsSubject, String envelope, Instant createdAt, int attempts) {}

  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final EventTransport transport;
  private final EventMetrics metrics;
  private final Clock clock;
  private final Settings settings;
  private final String claimSql;
  private final String publishedSql;
  private final String failedSql;
  private final String backlogSql;
  private final String purgeSql;
  private final Semaphore signal = new Semaphore(0);
  private volatile boolean running;
  private volatile Thread worker;
  private Instant lastPurge = Instant.EPOCH;

  public OutboxForwarder(
      DataSource dataSource,
      String schema,
      EventTransport transport,
      EventMetrics metrics,
      Clock clock,
      Settings settings) {
    CommonMigrations.requireSchemaName(schema);
    this.jdbc = new JdbcTemplate(dataSource);
    this.tx = new TransactionTemplate(new DataSourceTransactionManager(dataSource));
    this.transport = Objects.requireNonNull(transport);
    this.metrics = Objects.requireNonNull(metrics);
    this.clock = Objects.requireNonNull(clock);
    this.settings = settings == null ? Settings.defaults() : settings;
    String table = schema + ".outbox_event";
    this.claimSql =
        "SELECT o.id, o.nats_subject, o.envelope, o.created_at, o.attempts FROM "
            + table
            + " o WHERE o.published_at IS NULL AND o.next_attempt_at <= ?"
            + " AND NOT EXISTS (SELECT 1 FROM "
            + table
            + " p WHERE p.subject_key = o.subject_key AND p.published_at IS NULL"
            + " AND p.seq < o.seq)"
            + " ORDER BY o.seq LIMIT ? FOR UPDATE OF o SKIP LOCKED";
    this.publishedSql = "UPDATE " + table + " SET published_at = ?, last_error = NULL WHERE id = ?";
    this.failedSql =
        "UPDATE "
            + table
            + " SET attempts = attempts + 1, next_attempt_at = ?, last_error = ? WHERE id = ?";
    this.backlogSql = "SELECT count(*) FROM " + table + " WHERE published_at IS NULL";
    this.purgeSql = "DELETE FROM " + table + " WHERE published_at IS NOT NULL AND published_at < ?";
  }

  /** Sends one batch; returns how many events were published. */
  public int forwardOnce() {
    Integer published =
        tx.execute(
            status -> {
              Instant now = clock.instant();
              List<Claimed> rows =
                  jdbc.query(
                      claimSql,
                      (rs, i) ->
                          new Claimed(
                              rs.getObject(1, UUID.class),
                              rs.getString(2),
                              rs.getString(3),
                              rs.getTimestamp(4).toInstant(),
                              rs.getInt(5)),
                      Timestamp.from(now),
                      settings.batchSize());
              int ok = 0;
              for (Claimed row : rows) {
                try {
                  transport.send(
                      row.natsSubject(),
                      row.id().toString(),
                      row.envelope().getBytes(StandardCharsets.UTF_8));
                  Instant done = clock.instant();
                  jdbc.update(publishedSql, Timestamp.from(done), row.id());
                  metrics.forwarded(Duration.between(row.createdAt(), done));
                  ok++;
                } catch (Exception e) {
                  metrics.publishFailed();
                  Duration delay = backoff(row.attempts() + 1);
                  log.warn(
                      "Outbox event {} to {} failed (attempt {}), retry in {}",
                      row.id(),
                      row.natsSubject(),
                      row.attempts() + 1,
                      delay,
                      e);
                  jdbc.update(
                      failedSql,
                      Timestamp.from(clock.instant().plus(delay)),
                      truncate(String.valueOf(e)),
                      row.id());
                }
              }
              return ok;
            });
    refreshBacklog();
    return published == null ? 0 : published;
  }

  public void refreshBacklog() {
    Long count = jdbc.queryForObject(backlogSql, Long.class);
    metrics.backlog(count == null ? 0 : count);
  }

  /** Deletes published rows older than the retention period. */
  public int purgePublished() {
    return jdbc.update(purgeSql, Timestamp.from(clock.instant().minus(settings.retention())));
  }

  Duration backoff(int attempts) {
    long base = settings.baseBackoff().toMillis();
    long max = settings.maxBackoff().toMillis();
    long delay = attempts >= 30 ? max : Math.min(max, base * (1L << Math.max(0, attempts - 1)));
    return Duration.ofMillis(delay);
  }

  /** Asks the background loop to run now (called after a publishing transaction commits). */
  public void wakeUp() {
    if (signal.availablePermits() == 0) signal.release();
  }

  private void loop() {
    Duration errorBackoff = settings.baseBackoff();
    while (running) {
      try {
        int sent = forwardOnce();
        errorBackoff = settings.baseBackoff();
        if (Duration.between(lastPurge, clock.instant()).toHours() >= 1) {
          lastPurge = clock.instant();
          purgePublished();
        }
        if (sent < settings.batchSize()) {
          signal.tryAcquire(settings.pollInterval().toMillis(), TimeUnit.MILLISECONDS);
          signal.drainPermits();
        }
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        return;
      } catch (RuntimeException e) {
        if (!running) return;
        log.error("Outbox forwarder cycle failed; retrying in {}", errorBackoff, e);
        try {
          Thread.sleep(errorBackoff);
        } catch (InterruptedException ie) {
          Thread.currentThread().interrupt();
          return;
        }
        errorBackoff =
            errorBackoff.multipliedBy(2).compareTo(settings.maxBackoff()) > 0
                ? settings.maxBackoff()
                : errorBackoff.multipliedBy(2);
      }
    }
  }

  @Override
  public synchronized void start() {
    if (running) return;
    running = true;
    worker = Thread.ofVirtual().name("auriga-outbox-forwarder").start(this::loop);
  }

  @Override
  public synchronized void stop() {
    running = false;
    Thread t = worker;
    signal.release();
    if (t != null) {
      try {
        if (!t.join(Duration.ofSeconds(10))) t.interrupt();
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
      }
    }
    worker = null;
  }

  @Override
  public boolean isRunning() {
    return running;
  }

  private static String truncate(String s) {
    return s.length() > 2000 ? s.substring(0, 2000) : s;
  }
}
