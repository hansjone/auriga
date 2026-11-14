package auriga.resource.app;

import auriga.common.events.EventPublisher;
import com.networknt.schema.*;
import java.util.Map;
import java.util.UUID;
import tools.jackson.databind.json.JsonMapper;

/** Explicit frozen event-to-schema mapping; legacy EventSchemas uses convention-based names. */
public final class CatalogEvents implements EventPublisher {
  private final EventPublisher outbox;
  private final Map<String, Schema> schemas;

  public CatalogEvents(EventPublisher outbox) {
    this.outbox = outbox;
    var registry =
        SchemaRegistry.withDefaultDialect(
            SpecificationVersion.DRAFT_2020_12,
            b ->
                b.schemaIdResolvers(
                        r ->
                            r.mapPrefix(
                                "https://auriga.example.com/contracts/",
                                "classpath:auriga/contracts/"))
                    .schemaRegistryConfig(
                        SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build()));
    String base = "classpath:auriga/contracts/events/resource.schema.json#/$defs/";
    schemas =
        Map.of(
            "resource.catalog_version_published",
            registry.getSchema(SchemaLocation.of(base + "UnifiedResourceVersionPublished")),
            "resource.catalog_version_withdrawn",
            registry.getSchema(SchemaLocation.of(base + "UnifiedResourceVersionWithdrawn")),
            "resource.catalog_distribution_requested",
            registry.getSchema(SchemaLocation.of(base + "UnifiedResourceDistributionRequested")));
  }

  @Override
  public UUID publish(String type, String subject, Object data, String dataVersion) {
    var schema = schemas.get(type);
    if (schema == null
        || !schema.validate(JsonMapper.builder().build().valueToTree(data)).isEmpty())
      throw new IllegalArgumentException("Invalid catalog event");
    return outbox.publish(type, subject, data, dataVersion);
  }
}
