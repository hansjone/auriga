package auriga.workspace.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.contracts.node.v1.DshInstanceMode;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.workspace.persistence.WorkspaceStore;
import io.nats.client.Connection;
import io.nats.client.api.StorageType;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Clock;
import java.time.Duration;
import java.util.Map;
import java.util.TreeMap;
import java.util.UUID;
import tools.jackson.databind.json.JsonMapper;

/** Production replay readback and distinct initial-admission/ready host-profile observations. */
public final class SharedDshEntryTransport
    implements SharedDshTickets.Entry,
        SharedDshPlacements.EntryProbe,
        SharedDshResourceMarketQueries.ReadyEntryProbe,
        AutoCloseable {
  public static final String PROFILE_PATH = "/_auriga/shared-profile";
  public static final String DEDICATED_PROFILE_PATH = "/_auriga/dedicated-profile";
  public static final String PROFILE_HEADER = "Auriga-Shared-Profile-Probe";
  private static final Map<String, Object> PROFILE =
      Map.ofEntries(
          Map.entry("shell", false),
          Map.entry("terminal", false),
          Map.entry("jobs", false),
          Map.entry("subagent", false),
          Map.entry("stdioMcp", false),
          Map.entry("pluginInstall", false),
          Map.entry("privateSkills", false),
          Map.entry("personalKeys", false),
          Map.entry("includeDefaultRoots", false),
          Map.entry("toolPresentation", "native"));
  private static final Map<String, Object> DEDICATED_PROFILE =
      Map.of(
          "pluginInstall",
          true,
          "privateSkills",
          true,
          "includeDefaultRoots",
          true,
          "toolPresentation",
          "native");
  private final DeploymentAccess access;
  private final Connection nats;
  private final WorkspaceStore store;
  private final SharedDshTickets.SigningKey signer;
  private final Clock clock;
  private final HttpClient http;
  private final JsonMapper json = JsonMapper.builder().build();

  public SharedDshEntryTransport(
      DeploymentAccess access,
      Connection nats,
      WorkspaceStore store,
      SharedDshTickets.SigningKey signer,
      Clock clock,
      HttpClient http) {
    if (!URI.create(access.portalUrl()).getScheme().equals("https")
        && !(access.mode().equals("ip")
            && access.insecureHttpAcknowledged()
            && URI.create(access.portalUrl()).getScheme().equals("http")))
      throw new IllegalArgumentException(
          "Shared entry requires HTTPS or explicitly acknowledged IP HTTP");
    this.access = access;
    this.nats = nats;
    this.store = store;
    this.signer = signer;
    this.clock = clock;
    this.http = http;
  }

  private URI root(SharedDshBinding binding) {
    if (!binding.getTenantId().equals(access.tenantId()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    UUID pool = UUID.fromString(binding.getPhysicalInstanceId());
    if (access.instanceParentDomain().isEmpty())
      return URI.create(access.portalUrl() + "/i/" + pool);
    int port = URI.create(access.portalUrl()).getPort();
    return URI.create(
        "https://" + pool + "." + access.instanceParentDomain() + (port < 0 ? "" : ":" + port));
  }

  @Override
  public URI exchangeUrl(SharedDshBinding binding) {
    return URI.create(root(binding) + "/_auriga/placement-ticket");
  }

  @Override
  public boolean allowInsecureExchange(SharedDshBinding binding, URI url) {
    return access.mode().equals("ip")
        && access.insecureHttpAcknowledged()
        && URI.create(access.portalUrl()).getScheme().equals("http")
        && exchangeUrl(binding).equals(url);
  }

  @Override
  public boolean consumed(UUID jti) {
    try {
      if (nats.getStatus() != Connection.Status.CONNECTED)
        throw new IllegalStateException("Shared entry ledger disconnected");
      var ledger = nats.keyValue("auriga-shared-entry");
      var status = ledger.getStatus();
      if (status.getStorageType() != StorageType.File
          || (!status.getTtl().isZero() && status.getTtl().compareTo(Duration.ofHours(12)) < 0))
        throw new IllegalStateException("Shared entry ledger retention invalid");
      return ledger.get("ticket." + jti) != null;
    } catch (InterruptedException interrupted) {
      Thread.currentThread().interrupt();
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (Exception unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  @Override
  public boolean ready(SharedDshBinding binding) {
    // This adapter is intentionally initial-only. Mode switches/recovery retain
    // K's separate challenge, normal HTML page and host-signed receipt protocol.
    boolean initial =
        store.tx(
            () -> {
              if (!binding.getTenantId().equals(store.tenant().toString())) return false;
              var rows =
                  store.jdbc.queryForList(
                      "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND id=? AND state='pending'",
                      store.tenant(),
                      UUID.fromString(binding.getPlacementId()));
              return rows.size() == 1
                  && SharedDshPlacements.binding(rows.getFirst()).equals(binding)
                  && store.jdbc.queryForObject(
                          "SELECT count(*) FROM shared_dsh_operation WHERE tenant_id=? AND placement_id=?",
                          Long.class,
                          store.tenant(),
                          UUID.fromString(binding.getPlacementId()))
                      == 0;
            });
    return initial && profile(binding);
  }

  @Override
  public boolean observed(SharedDshBinding binding) {
    if (profilePath(binding) == null) return false;
    return currentReady(binding) && profile(binding) && currentReady(binding);
  }

  private static String profilePath(SharedDshBinding binding) {
    return switch (binding.getMode()) {
      case DSH_INSTANCE_MODE_SHARED -> PROFILE_PATH;
      case DSH_INSTANCE_MODE_DEDICATED -> DEDICATED_PROFILE_PATH;
      default -> null;
    };
  }

  private boolean currentReady(SharedDshBinding binding) {
    return store.tx(
        () -> {
          if (!binding.getTenantId().equals(store.tenant().toString())
              || !binding.getUnknownFields().asMap().isEmpty()) return false;
          store.lock();
          var rows =
              store.jdbc.queryForList(
                  "SELECT x.* FROM shared_dsh_placement x"
                      + " JOIN shared_dsh_pool p ON p.tenant_id=x.tenant_id AND p.id=x.pool_id AND p.mode=x.mode"
                      + " JOIN shared_dsh_pool_node pn ON pn.tenant_id=p.tenant_id AND pn.pool_id=p.id"
                      + " JOIN node n ON n.tenant_id=pn.tenant_id AND n.id=pn.node_id AND NOT n.revoked"
                      + " WHERE x.tenant_id=? AND x.id=? AND x.state='ready' AND p.state='ready'",
                  store.tenant(),
                  UUID.fromString(binding.getPlacementId()));
          return rows.size() == 1 && SharedDshPlacements.binding(rows.getFirst()).equals(binding);
        });
  }

  private boolean profile(SharedDshBinding binding) {
    String path = profilePath(binding);
    if (path == null) return false;
    boolean dedicated = binding.getMode() == DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED;
    long now = clock.instant().getEpochSecond();
    var claims = new TreeMap<String, Object>();
    claims.put("schemaVersion", 1);
    claims.put("audience", "auriga.shared.profile-probe");
    claims.put("binding", new TreeMap<>(auriga.workspace.domain.SharedDshEvents.binding(binding)));
    claims.put("issuedAt", now);
    claims.put("expiresAt", now + 10);
    claims.put("jti", UUID.randomUUID().toString());
    String proof = SharedDshTickets.signProfileProbe(signer.kid(), claims, signer.key());
    try {
      var request =
          HttpRequest.newBuilder(URI.create(root(binding) + path))
              .timeout(Duration.ofSeconds(5))
              .header(PROFILE_HEADER, proof)
              .header("Accept", "application/json")
              .header("Accept-Encoding", "identity")
              .GET()
              .build();
      var response =
          http.send(
              request,
              HttpResponse.BodyHandlers.limiting(HttpResponse.BodyHandlers.ofByteArray(), 16384));
      if (response.statusCode() != 200
          || response.headers().firstValue("Set-Cookie").isPresent()
          || response.headers().firstValue("Location").isPresent()
          || !response
              .headers()
              .firstValue("Content-Type")
              .orElse("")
              .split(";", 2)[0]
              .trim()
              .equals("application/json")) return false;
      var profile = json.readTree(response.body());
      return profile.isObject()
          && profile.size() == 4
          && profile.path("schemaVersion").isIntegralNumber()
          && profile.path("schemaVersion").bigIntegerValue().equals(java.math.BigInteger.ONE)
          && profile.path("physicalInstanceId").asString().equals(binding.getPhysicalInstanceId())
          && profile
              .path("profile")
              .asString()
              .equals(dedicated ? "auriga.dedicated.v1" : "auriga.shared.v1")
          && profile
              .path("capabilities")
              .equals(json.valueToTree(dedicated ? DEDICATED_PROFILE : PROFILE));
    } catch (InterruptedException interrupted) {
      Thread.currentThread().interrupt();
      return false;
    } catch (Exception unavailable) {
      return false;
    }
  }

  @Override
  public void close() {
    http.close();
  }
}
