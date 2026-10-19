package auriga.iam;

import auriga.iam.api.IdentityConnector;
import java.io.IOException;
import java.net.*;
import java.nio.file.*;
import java.util.*;
import tools.jackson.databind.JsonNode;

/** Plugins are trusted executable code; each jar has its own loader, closed with the registry. */
public final class ConnectorRegistry implements AutoCloseable {
  private final Map<String, IdentityConnector> connectors = new LinkedHashMap<>();
  private final List<URLClassLoader> loaders = new ArrayList<>();
  private final Map<String, String> loadErrors = new LinkedHashMap<>();
  private final ContractSchemas schemas;

  public ConnectorRegistry(ContractSchemas schemas, Collection<IdentityConnector> builtins) {
    this.schemas = schemas;
    builtins.forEach(this::register);
  }

  public synchronized void register(IdentityConnector connector) {
    JsonNode descriptor = connector.descriptor();
    schemas.validate("identity/connector.schema.json#/$defs/ConnectorDescriptor", descriptor);
    String type = descriptor.path("type").asString();
    IamError.require(!connectors.containsKey(type), "duplicate_connector", 409);
    connectors.put(type, connector);
  }

  public void load(Path directory) throws IOException {
    if (!Files.isDirectory(directory)) return;
    try (var files = Files.list(directory)) {
      for (Path jar : files.filter(p -> p.toString().endsWith(".jar")).sorted().toList()) {
        URLClassLoader loader =
            new URLClassLoader(
                new URL[] {jar.toUri().toURL()}, IdentityConnector.class.getClassLoader());
        loaders.add(loader);
        Map<String, IdentityConnector> previous = new LinkedHashMap<>(connectors);
        try {
          ServiceLoader.load(IdentityConnector.class, loader).forEach(this::register);
        } catch (ServiceConfigurationError | RuntimeException error) {
          connectors.clear();
          connectors.putAll(previous);
          loadErrors.put(jar.getFileName().toString(), "iam.connector_load_failed");
          org.slf4j.LoggerFactory.getLogger(ConnectorRegistry.class)
              .warn(
                  "Identity plugin rejected: {} ({})",
                  jar.getFileName(),
                  error.getClass().getSimpleName());
        }
      }
    }
  }

  public IdentityConnector get(String type) {
    IdentityConnector c = connectors.get(type);
    IamError.require(c != null, "unknown_connector", 400);
    return c;
  }

  public Collection<IdentityConnector> all() {
    return List.copyOf(connectors.values());
  }

  public Map<String, String> loadErrors() {
    return Map.copyOf(loadErrors);
  }

  public void validate(String type, JsonNode config) {
    IdentityConnector c = get(type);
    schemas.config(c.descriptor().path("configSchema"), config);
    c.validateConfig(config);
  }

  public JsonNode result(JsonNode result) {
    schemas.validate("identity/connector.schema.json#/$defs/ConnectorResult", result);
    IamError.require(
        result.path("state").toString().getBytes(java.nio.charset.StandardCharsets.UTF_8).length
            <= 16384,
        "connector_state_too_large",
        400);
    return result;
  }

  @Override
  public void close() throws IOException {
    for (URLClassLoader loader : loaders) loader.close();
  }
}
