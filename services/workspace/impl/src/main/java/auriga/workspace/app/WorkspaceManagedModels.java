package auriga.workspace.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.iam.v1.IssueManagedDeviceResponse;
import auriga.contracts.node.v1.DesiredState;
import auriga.workspace.domain.WorkspaceCrypto;
import java.net.URI;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/**
 * Fetch the member's allowed pool outside workspace transactions; persist only encrypted material.
 */
public final class WorkspaceManagedModels {
  private final WorkspaceService service;
  private final WorkspaceCrypto crypto;
  private final String baseUrl;

  public WorkspaceManagedModels(
      WorkspaceService service, WorkspaceCrypto crypto, ObjectNode installation) {
    this.service = service;
    this.crypto = crypto;
    this.baseUrl =
        installation
            .path("modelBaseUrl")
            .asString(installation.path("platformOrigin").asString() + "/ai/v1");
  }

  public void reconcile(Map<String, Object> row, IssueManagedDeviceResponse response) {
    if (response.getModelCredential().isEmpty() && row.get("managed_model_credential") == null)
      return;
    if (!response.getModelCredential().isEmpty() && row.get("managed_model_credential") != null)
      return;
    UUID id = (UUID) row.get("id");
    long version = ((Number) row.get("managed_credential_version")).longValue();
    byte[] material =
        response.getModelCredential().isEmpty()
            ? null
            : crypto.encrypt(
                catalog(response, row).toString().getBytes(StandardCharsets.UTF_8),
                service.store.tenant() + "/managed-model/" + id);
    var store = service.store;
    store.tx(
        () -> {
          store.lock();
          var current =
              store.one(
                  "SELECT * FROM instance WHERE tenant_id=? AND id=? FOR UPDATE",
                  store.tenant(),
                  id);
          if (((Number) current.get("managed_credential_version")).longValue() != version
              || (boolean) current.get("deleted")
              || WorkspaceService.spec(current).getDesired() != DesiredState.DESIRED_STATE_RUNNING)
            return null;
          if ((current.get("managed_model_credential") == null) == (material == null)) return null;
          store.jdbc.update(
              "UPDATE instance SET managed_model_credential=? WHERE tenant_id=? AND id=?",
              material,
              store.tenant(),
              id);
          current = store.instance(id);
          var spec = WorkspaceService.spec(current);
          service.saveSpec(
              current, spec.toBuilder().setGeneration(spec.getGeneration() + 1).build());
          if ("running".equals(current.get("phase"))) service.queueManagedMaterialRestart(id);
          return null;
        });
  }

  private ObjectNode catalog(IssueManagedDeviceResponse response, Map<String, Object> row) {
    try {
      URI uri = URI.create(baseUrl);
      if (!("https".equals(uri.getScheme()) || "http".equals(uri.getScheme()))
          || uri.getHost() == null
          || uri.getUserInfo() != null
          || uri.getQuery() != null
          || uri.getFragment() != null
          || !"/ai/v1".equals(uri.getPath())) throw new IllegalArgumentException();
      try (var client =
          HttpClient.newBuilder()
              .connectTimeout(Duration.ofSeconds(5))
              .followRedirects(HttpClient.Redirect.NEVER)
              .build()) {
        var request =
            HttpRequest.newBuilder(URI.create(baseUrl + "/models"))
                .timeout(Duration.ofSeconds(10))
                .header("Authorization", "Bearer " + response.getModelCredential())
                .GET()
                .build();
        var reply =
            client.send(
                request,
                HttpResponse.BodyHandlers.limiting(
                    HttpResponse.BodyHandlers.ofByteArray(), 1 << 20));
        byte[] raw = reply.body();
        if (reply.statusCode() != 200) throw new IllegalArgumentException();
        var data = WorkspaceService.JSON.readTree(raw).path("data");
        if (!data.isArray() || data.isEmpty() || data.size() > 256)
          throw new IllegalArgumentException();
        var file = WorkspaceService.JSON.createObjectNode();
        file.put("tenantId", service.store.tenant().toString());
        file.put("ownerId", row.get("owner_id").toString());
        file.put("instanceId", row.get("id").toString());
        file.put("credentialVersion", ((Number) row.get("managed_credential_version")).longValue());
        file.put("credential", response.getModelCredential());
        file.put("baseUrl", baseUrl);
        var models = file.putArray("models");
        Set<String> seen = new HashSet<>();
        for (var model : data) {
          String name = model.path("id").asString();
          if (name.isBlank()
              || name.length() > 200
              || name.chars().anyMatch(Character::isISOControl)
              || !seen.add(name)) throw new IllegalArgumentException();
          models.addObject().put("id", name);
        }
        return file;
      }
    } catch (InterruptedException interrupted) {
      Thread.currentThread().interrupt();
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (Exception unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }
}
