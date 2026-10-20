package auriga.workspace.app;

import auriga.common.error.*;
import auriga.common.security.AurigaPrincipal;
import auriga.workspace.web.model.*;
import java.util.*;

public final class WorkspaceQueries {
  public final WorkspaceService service;
  public final WorkspaceViews views;

  public WorkspaceQueries(WorkspaceService service) {
    this.service = service;
    this.views = new WorkspaceViews(service);
  }

  public Instance owned(UUID id, AurigaPrincipal actor) {
    return service.store.tx(() -> views.instance(service.owned(id, actor)));
  }

  public List<Instance> instances(UUID owner, UUID node, String phase, int limit, String cursor) {
    if (limit < 1 || limit > 200) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    UUID after = null;
    try {
      if (cursor != null && !cursor.isBlank()) after = UUID.fromString(cursor);
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
    final UUID key = after;
    return service.store.tx(
        () ->
            service
                .store
                .jdbc
                .queryForList(
                    "SELECT * FROM instance WHERE tenant_id=? AND NOT deleted AND (?::uuid IS NULL OR owner_id=?) AND (?::uuid IS NULL OR node_id=?) AND (?::text IS NULL OR phase=?) AND (?::uuid IS NULL OR id>?) ORDER BY id LIMIT ?",
                    service.store.tenant(),
                    owner,
                    owner,
                    node,
                    node,
                    phase,
                    phase,
                    key,
                    key,
                    limit)
                .stream()
                .map(views::instance)
                .toList());
  }

  public List<Node> nodes() {
    return service.store.tx(
        () ->
            service
                .store
                .jdbc
                .queryForList(
                    "SELECT * FROM node WHERE tenant_id=? ORDER BY id", service.store.tenant())
                .stream()
                .map(views::node)
                .toList());
  }

  public Node node(UUID id) {
    return service.store.tx(() -> views.node(service.store.node(id)));
  }

  public List<JoinToken> tokens() {
    return service.store.tx(
        () ->
            service
                .store
                .jdbc
                .queryForList(
                    "SELECT * FROM join_token WHERE tenant_id=? AND used_at IS NULL AND expires_at>? ORDER BY id",
                    service.store.tenant(),
                    auriga.workspace.persistence.WorkspaceStore.time(service.clock.instant()))
                .stream()
                .map(
                    row -> {
                      Map<String, Object> out = new HashMap<>();
                      out.put("id", row.get("id"));
                      out.put("name", row.get("name"));
                      out.put("createdBy", row.get("created_by"));
                      out.put(
                          "createdAt",
                          auriga.workspace.persistence.WorkspaceStore.instant(row, "created_at")
                              .toString());
                      out.put(
                          "expiresAt",
                          auriga.workspace.persistence.WorkspaceStore.instant(row, "expires_at")
                              .toString());
                      return WorkspaceService.JSON.convertValue(out, JoinToken.class);
                    })
                .toList());
  }

  public void deleteToken(UUID id) {
    service.store.tx(
        () ->
            service.store.jdbc.update(
                "DELETE FROM join_token WHERE tenant_id=? AND id=?", service.store.tenant(), id));
  }

  public ListRuntimes200Response runtimes() {
    return service.store.tx(
        () -> {
          Map<String, List<auriga.contracts.node.v1.InstanceSpec>> versions = new TreeMap<>();
          for (var template : service.templates.all())
            versions
                .computeIfAbsent(template.getDsh().getVersion(), k -> new ArrayList<>())
                .add(template);
          List<Object> items = new ArrayList<>();
          for (var entry : versions.entrySet()) {
            List<Object> artifacts = new ArrayList<>();
            for (var template : entry.getValue()) {
              var d = template.getDsh();
              var statement = d.getSigned().getStatement();
              artifacts.add(
                  Map.of(
                      "artifactId",
                      d.getArtifactId(),
                      "os",
                      statement.getOs(),
                      "arch",
                      statement.getArch(),
                      "digest",
                      d.getDigest(),
                      "image",
                      d.getImage()));
            }
            long count =
                service
                    .store
                    .jdbc
                    .queryForList(
                        "SELECT * FROM instance WHERE tenant_id=? AND NOT deleted",
                        service.store.tenant())
                    .stream()
                    .filter(
                        r -> WorkspaceService.spec(r).getDsh().getVersion().equals(entry.getKey()))
                    .count();
            items.add(
                Map.of(
                    "version",
                    entry.getKey(),
                    "artifacts",
                    artifacts,
                    "instances",
                    count,
                    "createdAt",
                    entry
                                .getValue()
                                .getFirst()
                                .getDsh()
                                .getSigned()
                                .getStatement()
                                .getIssuedAt()
                                .getSeconds()
                            == 0
                        ? java.time.Instant.EPOCH.toString()
                        : java.time.Instant.ofEpochSecond(
                                entry
                                    .getValue()
                                    .getFirst()
                                    .getDsh()
                                    .getSigned()
                                    .getStatement()
                                    .getIssuedAt()
                                    .getSeconds())
                            .toString()));
          }
          String version =
              service.defaultVersion(versions.isEmpty() ? "" : versions.keySet().iterator().next());
          return WorkspaceService.JSON.convertValue(
              Map.of("defaultVersion", version, "items", items), ListRuntimes200Response.class);
        });
  }
}
