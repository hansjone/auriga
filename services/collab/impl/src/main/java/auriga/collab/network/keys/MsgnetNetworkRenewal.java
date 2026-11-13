package auriga.collab.network.keys;

import auriga.collab.api.NetworkRenewal;
import auriga.collab.app.NetworkJson;
import auriga.contracts.msgnet.model.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/** Generated contract model adapter used by merged services and authenticated HTTP adapters. */
public final class MsgnetNetworkRenewal implements NetworkRenewal {
  private final MsgnetRenewal renewal;
  private final MsgnetControlLedger controls;
  private final MsgnetMaterials materials;
  private static final JsonMapper JSON = JsonMapper.builder().build();

  public MsgnetNetworkRenewal(
      MsgnetRenewal renewal, MsgnetControlLedger controls, MsgnetMaterials materials) {
    this.renewal = renewal;
    this.controls = controls;
    this.materials = materials;
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
    return model(NetworkJson.parse(renewal.publication(), 1048576), Publication.class);
  }

  @Override
  public RenewalStatus status() {
    return model(NetworkJson.parse(renewal.status()), RenewalStatus.class);
  }

  @Override
  public ControlPackage createControlPackage(ControlPackage request) {
    return model(controls.create(tree(request)), ControlPackage.class);
  }

  @Override
  public IsolationRecord confirmControl(InstallationReceipt receipt) {
    return model(controls.confirm(tree(receipt)), IsolationRecord.class);
  }

  @Override
  public IsolationRecord enforceIsolation(IsolationRecord request) {
    return model(controls.enforce(tree(request)), IsolationRecord.class);
  }

  @Override
  public ProtocolMaterial committedProtocol(ProjectionRequest request) {
    return model(materials.protocol(tree(request)), ProtocolMaterial.class);
  }

  @Override
  public ProjectionMaterial committedTransport(ProjectionRequest request) {
    return model(materials.material(tree(request)), ProjectionMaterial.class);
  }

  @Override
  public SealedMaterial sealedBootstrap(ProjectionRequest request) {
    return model(materials.sealed(tree(request)), SealedMaterial.class);
  }

  @Override
  public ProjectionReceipt confirmProjection(ProjectionReceipt receipt) {
    return model(materials.confirm(tree(receipt)), ProjectionReceipt.class);
  }
}
