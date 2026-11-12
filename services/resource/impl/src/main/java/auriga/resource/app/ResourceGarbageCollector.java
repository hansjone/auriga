package auriga.resource.app;

import auriga.common.context.TenantContext;
import auriga.resource.objectstore.ObjectStore;
import auriga.resource.persistence.ResourceStore;
import auriga.resource.persistence.ResourceWorkflowStore;
import java.time.Clock;
import java.util.function.Supplier;
import org.springframework.scheduling.annotation.Scheduled;

/** The publication lock covers the last reference check and deletion across processes. */
public final class ResourceGarbageCollector {
  private final ResourceStore resources;
  private final ResourceWorkflowStore db;
  private final Supplier<ObjectStore> objects;
  private final Clock clock;

  public ResourceGarbageCollector(
      ResourceStore resources,
      ResourceWorkflowStore db,
      Supplier<ObjectStore> objects,
      Clock clock) {
    this.resources = resources;
    this.db = db;
    this.objects = objects;
    this.clock = clock;
  }

  @Scheduled(fixedDelayString = "${auriga.resource.gc-ms:3600000}")
  public void collect() {
    var store = objects.get();
    if (store == null) return;
    var cutoff = clock.instant().minusSeconds(86400);
    for (var candidate : store.expiredObjects(cutoff)) {
      try {
        TenantContext.run(
            candidate.tenant(),
            () ->
                resources.write(
                    () -> {
                      db.lock("object/" + candidate.digest());
                      return store.deleteExpired(
                          candidate,
                          cutoff,
                          () ->
                              db.rows(
                                      "SELECT id FROM resource.resource_version WHERE tenant_id=:tenant AND digest=:digest LIMIT 1",
                                      db.args("digest", candidate.digest()))
                                  .isEmpty());
                    }));
      } catch (RuntimeException e) {
        org.slf4j.LoggerFactory.getLogger(getClass()).warn("Resource object collection will retry");
      }
    }
  }
}
