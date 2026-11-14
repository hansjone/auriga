package auriga.collab.network.keys;

import auriga.collab.api.NetworkRenewal;
import auriga.collab.app.NetworkJson;
import auriga.contracts.msgnet.model.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/** Generated contract model adapter used by merged services and authenticated HTTP adapters. */
public final class MsgnetNetworkRenewal implements NetworkRenewal {
  private final java.util.function.Supplier<MsgnetRenewal> renewal;
  private final java.util.function.Supplier<MsgnetControlLedger> controls;
  private final java.util.function.Supplier<MsgnetMaterials> materials;
  private static final JsonMapper JSON = JsonMapper.builder().build();

  public MsgnetNetworkRenewal(
      MsgnetRenewal renewal, MsgnetControlLedger controls, MsgnetMaterials materials) {
    this(() -> renewal, () -> controls, () -> materials);
  }

  public MsgnetNetworkRenewal(
      java.util.function.Supplier<MsgnetRenewal> renewal,
      java.util.function.Supplier<MsgnetControlLedger> controls,
      java.util.function.Supplier<MsgnetMaterials> materials) {
    this.renewal = renewal;
    this.controls = controls;
    this.materials = materials;
  }

  static <T> T available(java.util.function.Supplier<T> provider) {
    try {
      return provider.get();
    } catch (org.springframework.beans.BeansException failure) {
      throw MsgnetV2Verifier.failure("projection_pending");
    }
  }

  static JsonNode tree(Object value) {
    JsonNode node = JSON.valueToTree(value);
    omitOptionalNulls(node);
    return node;
  }

  private static void omitOptionalNulls(JsonNode node) {
    if (node instanceof tools.jackson.databind.node.ObjectNode object) {
      var names = new java.util.ArrayList<String>();
      object.propertyNames().forEach(names::add);
      for (String name : names) {
        if (object.path(name).isNull()) object.remove(name);
        else omitOptionalNulls(object.path(name));
      }
    } else if (node.isArray()) node.forEach(MsgnetNetworkRenewal::omitOptionalNulls);
  }

  private static <T> T model(JsonNode value, Class<T> type) {
    return JSON.treeToValue(value, type);
  }

  @Override
  public Publication currentPublication() {
    available(renewal).requireCallerTenant();
    return model(NetworkJson.parse(available(renewal).publication(), 1048576), Publication.class);
  }

  @Override
  public RenewalStatusOutput status() {
    available(renewal).requireCallerTenant();
    return new StatusOutput(NetworkJson.parse(available(renewal).status(), 1048576));
  }

  // The generated oneOf model narrows the state enum to its unpublished branch.
  // Preserve the frozen union on the wire without modifying generated sources.
  static final class StatusOutput extends RenewalStatusOutput {
    private final JsonNode output;

    StatusOutput(JsonNode output) {
      this.output = output;
      var common = (tools.jackson.databind.node.ObjectNode) output.deepCopy();
      common.remove("state");
      org.springframework.beans.BeanUtils.copyProperties(
          JSON.treeToValue(common, RenewalStatusOutput.class), this);
      try {
        setState(RenewalStatusOutput.StateEnum.fromValue(output.path("state").asString()));
      } catch (IllegalArgumentException unsupportedPublishedState) {
        // The exact published state remains available in the frozen JSON union above.
      }
    }

    @com.fasterxml.jackson.annotation.JsonValue
    public JsonNode output() {
      return output;
    }
  }

  @Override
  public ControlPackage createControlPackage(ControlPackage request) {
    available(renewal).requireCallerTenant();
    return model(available(controls).create(tree(request)), ControlPackage.class);
  }

  @Override
  public IsolationRecord confirmControl(InstallationReceipt receipt) {
    available(renewal).requireCallerTenant();
    return model(available(controls).confirm(tree(receipt)), IsolationRecord.class);
  }

  @Override
  public IsolationRecord enforceIsolation(IsolationRecord request) {
    available(renewal).requireCallerTenant();
    return model(available(controls).enforce(tree(request)), IsolationRecord.class);
  }

  @Override
  public ProtocolMaterial committedProtocol(ProjectionRequest request) {
    available(renewal).requireCallerTenant();
    return model(available(materials).protocol(tree(request)), ProtocolMaterial.class);
  }

  @Override
  public ProjectionMaterial committedTransport(ProjectionRequest request) {
    available(renewal).requireCallerTenant();
    return model(available(materials).material(tree(request)), ProjectionMaterial.class);
  }

  @Override
  public SealedMaterial sealedBootstrap(ProjectionRequest request) {
    return model(available(materials).sealed(tree(request)), SealedMaterial.class);
  }

  @Override
  public ProjectionReceipt confirmProjection(ProjectionReceipt receipt) {
    available(renewal).requireCallerTenant();
    return model(available(materials).confirm(tree(receipt)), ProjectionReceipt.class);
  }
}
