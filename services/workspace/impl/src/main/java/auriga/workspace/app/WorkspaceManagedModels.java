package auriga.workspace.app;

import auriga.common.context.RequestContext;
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
  private final boolean enabled;
  private final javax.net.ssl.SSLContext tls;

  public WorkspaceManagedModels(
      WorkspaceService service, WorkspaceCrypto crypto, ObjectNode installation) {
    this.service = service;
    this.crypto = crypto;
    this.baseUrl =
        installation
            .path("modelBaseUrl")
            .asString(installation.path("platformOrigin").asString() + "/ai/v1");
    this.enabled = validBaseUrl(baseUrl);
    String caFile = installation.path("modelCaFile").asString("");
    this.tls = caFile.isEmpty() ? null : WorkspaceManagedInstallation.tls(caFile);
    if (!enabled)
      org.slf4j.LoggerFactory.getLogger(getClass())
          .warn(
              "Managed model capability state=disabled_invalid_installation reason=invalid_model_base_url");
  }

  public boolean enabled() {
    return enabled;
  }

  public String state() {
    return enabled ? "enabled" : "disabled_invalid_installation";
  }

  private static boolean validBaseUrl(String value) {
    try {
      URI uri = URI.create(value);
      return ("https".equals(uri.getScheme()) || "http".equals(uri.getScheme()))
          && uri.getHost() != null
          && uri.getUserInfo() == null
          && uri.getRawQuery() == null
          && uri.getRawFragment() == null
          && "/ai/v1".equals(uri.getRawPath());
    } catch (IllegalArgumentException invalid) {
      return false;
    }
  }

  public void reconcile(Map<String, Object> row, IssueManagedDeviceResponse response) {
    boolean hasCredential = enabled && !response.getModelCredential().isEmpty();
    if (enabled && !hasCredential) {
      org.slf4j.LoggerFactory.getLogger(getClass())
          .info(
              "Managed model capability instance={} managedCredentialVersion={} generation={} stage=credential reason=model_credential_not_issued",
              row.get("id"),
              row.get("managed_credential_version"),
              row.get("generation"));
    }
    if (!hasCredential && row.get("managed_model_credential") == null) return;
    if (hasCredential && hasModelLimits(row)) return;
    UUID id = (UUID) row.get("id");
    long version = ((Number) row.get("managed_credential_version")).longValue();
    byte[] material =
        !hasCredential
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
          if (material == null && current.get("managed_model_credential") == null) return null;
          // Upgrade legacy id-only material through the normal catalog and seal
          // path. A racing reconciliation that already installed limits wins.
          if (material != null && hasModelLimits(current)) return null;
          store.jdbc.update(
              "UPDATE instance SET managed_model_credential=? WHERE tenant_id=? AND id=?",
              material,
              store.tenant(),
              id);
          current = store.instance(id);
          var spec = WorkspaceService.spec(current);
          service.saveStartupSpec(
              current, spec.toBuilder().setGeneration(spec.getGeneration() + 1).build());
          if ("running".equals(current.get("phase"))) service.queueManagedMaterialRestart(id);
          return null;
        });
  }

  private boolean hasModelLimits(Map<String, Object> row) {
    if (!(row.get("managed_model_credential") instanceof byte[] material)) return false;
    byte[] plaintext =
        crypto.decrypt(material, service.store.tenant() + "/managed-model/" + row.get("id"));
    try {
      return ManagedModelCatalog.hasLimits(WorkspaceService.JSON.readTree(plaintext));
    } finally {
      Arrays.fill(plaintext, (byte) 0);
    }
  }

  private ObjectNode catalog(IssueManagedDeviceResponse response, Map<String, Object> row) {
    long started = System.nanoTime();
    String stage = "connect", reason = "transport";
    int httpStatus = 0;
    String upstreamTrace = "";
    try {
      var builder =
          HttpClient.newBuilder()
              .connectTimeout(Duration.ofSeconds(5))
              .followRedirects(HttpClient.Redirect.NEVER);
      if (tls != null) builder.sslContext(tls);
      try (var client = builder.build()) {
        var requestBuilder =
            HttpRequest.newBuilder(URI.create(baseUrl + "/models"))
                .timeout(Duration.ofSeconds(10))
                .header("Authorization", "Bearer " + response.getModelCredential())
                .GET();
        RequestContext.current()
            .ifPresent(
                c -> requestBuilder.header("traceparent", c.trace().child().toTraceparent()));
        var request = requestBuilder.build();
        stage = "http";
        var reply =
            client.send(
                request,
                HttpResponse.BodyHandlers.limiting(
                    HttpResponse.BodyHandlers.ofByteArray(), 1 << 20));
        httpStatus = reply.statusCode();
        String observedTrace = reply.headers().firstValue("X-Trace-Id").orElse("");
        if (observedTrace.matches("[a-f0-9]{32}")) upstreamTrace = observedTrace;
        byte[] raw = reply.body();
        reason = "http_status";
        if (reply.statusCode() != 200) throw new IllegalArgumentException();
        stage = "catalog";
        reason = "invalid_json";
        var data = WorkspaceService.JSON.readTree(raw).path("data");
        reason =
            !data.isArray()
                ? "invalid_catalog"
                : data.isEmpty() ? "empty_catalog" : "catalog_too_large";
        if (!data.isArray() || data.isEmpty() || data.size() > 256)
          throw new IllegalArgumentException();
        var file = WorkspaceService.JSON.createObjectNode();
        file.put("tenantId", service.store.tenant().toString());
        file.put("ownerId", row.get("owner_id").toString());
        file.put("instanceId", row.get("id").toString());
        file.put("credentialVersion", ((Number) row.get("managed_credential_version")).longValue());
        file.put("credential", response.getModelCredential());
        file.put("baseUrl", baseUrl);
        try {
          file.set("models", ManagedModelCatalog.fromGateway(data));
        } catch (IllegalArgumentException invalid) {
          reason = invalid.getMessage();
          throw invalid;
        }
        return file;
      }
    } catch (InterruptedException interrupted) {
      ManagedModelDiagnostic.failure(
          row,
          stage,
          "interrupted",
          httpStatus,
          started,
          interrupted,
          response.getModelCredential(),
          upstreamTrace);
      Thread.currentThread().interrupt();
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (Exception unavailable) {
      ManagedModelDiagnostic.failure(
          row,
          stage,
          reason,
          httpStatus,
          started,
          unavailable,
          response.getModelCredential(),
          upstreamTrace);
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }
}
