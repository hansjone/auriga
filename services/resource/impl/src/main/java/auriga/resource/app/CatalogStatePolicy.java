package auriga.resource.app;

import static auriga.resource.web.ResourceBody.*;

import auriga.common.error.*;
import auriga.contracts.resource.v1.*;
import com.networknt.schema.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Frozen K2 dispatch/CAS/acknowledgement semantics, shared by member and node paths. */
public final class CatalogStatePolicy {
  private static final Schema DISPATCH =
      SchemaRegistry.withDefaultDialect(
              SpecificationVersion.DRAFT_2020_12,
              b ->
                  b.schemaIdResolvers(
                          r ->
                              r.mapPrefix(
                                  "https://auriga.example.com/contracts/",
                                  "classpath:auriga/contracts/"))
                      .schemaRegistryConfig(
                          SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build()))
          .getSchema(
              SchemaLocation.of(
                  "classpath:auriga/contracts/resource/unified.schema.json#/$defs/UnifiedResourceDispatchRef"));

  private CatalogStatePolicy() {}

  public static AurigaException conflict() {
    return new AurigaException(
        ErrorCode.of("resource.state_conflict", 409, io.grpc.Status.Code.ABORTED));
  }

  public static long advance(long persisted, long expected) {
    if (persisted != expected) throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
    if (persisted < 0 || persisted >= ResourceWorkflow.MAX) throw conflict();
    return persisted + 1;
  }

  public static String desired(JsonNode resource) {
    if (!resource.has("desiredState"))
      return resource.path("enabled").asBoolean() ? "ENABLED" : "DISABLED";
    String value = resource.path("desiredState").asString();
    if (!java.util.Set.of("ENABLED", "DISABLED", "REMOVED").contains(value)) throw invalid();
    return value;
  }

  public static String desired(UnifiedResourceRef resource) {
    if (!resource.hasDesiredState()) return resource.getEnabled() ? "ENABLED" : "DISABLED";
    return switch (resource.getDesiredState()) {
      case UNIFIED_RESOURCE_DESIRED_STATE_ENABLED -> "ENABLED";
      case UNIFIED_RESOURCE_DESIRED_STATE_DISABLED -> "DISABLED";
      case UNIFIED_RESOURCE_DESIRED_STATE_REMOVED -> "REMOVED";
      default -> throw invalid();
    };
  }

  public static String stateForAction(String action) {
    return switch (action) {
      case "install", "update", "enable" -> "ENABLED";
      case "disable" -> "DISABLED";
      case "remove" -> "REMOVED";
      default -> throw invalid();
    };
  }

  public static String success(String desired) {
    return switch (desired) {
      case "ENABLED" -> "applied";
      case "DISABLED" -> "disabled";
      case "REMOVED" -> "removed";
      default -> throw invalid();
    };
  }

  public static ObjectNode dispatch(ObjectNode resource, long revision, String state) {
    assign(resource, revision, state);
    if (!DISPATCH.validate(resource).isEmpty()) throw invalid();
    return resource;
  }

  public static ObjectNode assign(ObjectNode resource, long revision, String state) {
    if (revision < 1 || revision > ResourceWorkflow.MAX) throw invalid();
    success(state);
    resource
        .put("installationRevision", revision)
        .put("desiredState", state)
        .put("enabled", state.equals("ENABLED"));
    return resource;
  }

  public static boolean validDispatch(JsonNode resource) {
    return DISPATCH.validate(resource).isEmpty();
  }

  public static boolean tuple(
      JsonNode resource, long selection, long generation, JsonNode report, String channel) {
    long revision = resource.path("installationRevision").asLong();
    return revision > 0
        && ResourceSigning.canonical(resource.path("identity"))
            .equals(ResourceSigning.canonical(report.path("identity")))
        && revision == report.path("installationRevision").asLong()
        && selection == report.path("selectionRevision").asLong()
        && generation == report.path("generation").asLong()
        && channel.equals(report.path("reportSource").asString());
  }

  public static boolean acknowledged(
      JsonNode resource, long selection, long generation, JsonNode report, String channel) {
    return tuple(resource, selection, generation, report, channel)
        && generation == report.path("observedGeneration").asLong()
        && success(desired(resource)).equals(report.path("state").asString());
  }

  public static boolean acknowledged(
      UnifiedResourceRef resource,
      long selection,
      long generation,
      UnifiedInstallationReport report,
      String channel) {
    return resource.getInstallationRevision() > 0
        && resource.getIdentity().equals(report.getIdentity())
        && resource.getInstallationRevision() == report.getInstallationRevision()
        && selection == report.getSelectionRevision()
        && generation == report.getGeneration()
        && generation == report.getObservedGeneration()
        && channel.equals(report.getReportSource())
        && success(desired(resource)).equals(report.getState());
  }

  public static boolean removal(
      JsonNode resource,
      long selection,
      long generation,
      JsonNode report,
      String channel,
      boolean contentCleared) {
    return desired(resource).equals("REMOVED")
        && contentCleared
        && acknowledged(resource, selection, generation, report, channel);
  }

  public static boolean collect(
      JsonNode resource,
      long selection,
      long generation,
      JsonNode report,
      String channel,
      boolean cleared,
      long nextGeneration) {
    return nextGeneration == generation + 1
        && removal(resource, selection, generation, report, channel, cleared);
  }
}
