package auriga.common.example;

import auriga.common.audit.AuditChanges;
import auriga.common.audit.AuditEntry;
import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.events.EventHandler;
import auriga.common.events.EventPublisher;
import auriga.common.events.ReceivedEvent;
import auriga.common.persistence.UuidV7;
import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.InterceptorIgnore;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicInteger;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Select;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

/**
 * The minimal example service from the K2 task: writes business data and publishes an event in one
 * transaction, and consumes that event idempotently.
 */
public final class ExampleService {
  private ExampleService() {}

  @TableName("widget")
  public static class Widget {
    @TableId(type = IdType.INPUT)
    private UUID id;

    /** Filled by the MyBatis-Plus tenant line interceptor; never set by business code. */
    private UUID tenantId;

    private String name;

    public UUID getId() {
      return id;
    }

    public void setId(UUID id) {
      this.id = id;
    }

    public UUID getTenantId() {
      return tenantId;
    }

    public void setTenantId(UUID tenantId) {
      this.tenantId = tenantId;
    }

    public String getName() {
      return name;
    }

    public void setName(String name) {
      this.name = name;
    }
  }

  @Mapper
  public interface WidgetMapper extends BaseMapper<Widget> {
    /** Explicit system-level query: no tenant line from MyBatis-Plus (RLS still applies). */
    @InterceptorIgnore(tenantLine = "true")
    @Select("SELECT id, tenant_id, name FROM widget")
    List<Widget> selectIgnoringTenantLine();
  }

  public record WidgetCreated(UUID widgetId, String name) {}

  @Service
  public static class WidgetService {
    private final WidgetMapper widgets;
    private final EventPublisher events;
    private final AuditLogger audit;

    public WidgetService(WidgetMapper widgets, EventPublisher events, AuditLogger audit) {
      this.widgets = widgets;
      this.events = events;
      this.audit = audit;
    }

    @Transactional
    public UUID create(String name) {
      Widget widget = new Widget();
      widget.setId(UuidV7.next());
      widget.setName(name);
      widgets.insert(widget);
      events.publish(
          "widget.created", widget.getId().toString(), new WidgetCreated(widget.getId(), name));
      audit.record(
          AuditEntry.builder("example.widget.create")
              .target("widget", widget.getId().toString(), name)
              .changes(AuditChanges.create().change("name", null, name).change("apiKey", null, "k"))
              .build());
      return widget.getId();
    }

    @Transactional
    public UUID createThenFail(String name) {
      create(name);
      throw new IllegalStateException("business rule violated after publishing");
    }

    @Transactional(readOnly = true)
    public List<Widget> list() {
      return widgets.selectList(null);
    }

    @Transactional(readOnly = true)
    public List<Widget> listIgnoringTenantLine() {
      return widgets.selectIgnoringTenantLine();
    }
  }

  /** Consumer: records each widget once into widget_seen (RLS table, event tenant bound). */
  public static class WidgetSeenHandler implements EventHandler {
    private final JdbcTemplate jdbc;
    public final AtomicInteger invocations = new AtomicInteger();
    /** Invocations per event id: other tests' events are consumed concurrently. */
    private final java.util.concurrent.ConcurrentHashMap<String, AtomicInteger> byEvent =
        new java.util.concurrent.ConcurrentHashMap<>();

    public int invocationsOf(String eventId) {
      AtomicInteger n = byEvent.get(eventId);
      return n == null ? 0 : n.get();
    }

    public WidgetSeenHandler(JdbcTemplate jdbc) {
      this.jdbc = jdbc;
    }

    @Override
    public String name() {
      return "widget-seen";
    }

    @Override
    public Set<String> eventTypes() {
      return Set.of("widget.created");
    }

    @Override
    public void handle(ReceivedEvent event) {
      invocations.incrementAndGet();
      byEvent.computeIfAbsent(String.valueOf(event.id()), k -> new AtomicInteger()).incrementAndGet();
      WidgetCreated data = event.dataAs(WidgetCreated.class);
      if (data.name().startsWith("poison")) throw new IllegalStateException("cannot handle poison");
      jdbc.update(
          "INSERT INTO widget_seen (event_id, tenant_id, widget_id) VALUES (?, ?, ?)",
          event.id(),
          TenantContext.require(),
          data.widgetId());
    }
  }
}
