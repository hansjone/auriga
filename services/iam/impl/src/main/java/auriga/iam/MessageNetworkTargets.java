package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.iam.api.MessageNetworkAdmission;
import auriga.iam.api.MessageNetworkControlLedger;
import auriga.iam.api.NodeControlAuthority;
import java.nio.file.Path;
import java.util.*;

/** D's immutable local device-to-control mapping. Broker node numbers are resolved in IAM. */
final class MessageNetworkTargets {
  record Binding(UUID tenant, String realm, UUID device, String target, UUID host) {}

  private final List<Binding> bindings;

  static MessageNetworkTargets load(Path file) throws java.io.IOException {
    return new MessageNetworkTargets(InstallationPinFile.read(file));
  }

  MessageNetworkTargets(byte[] raw) {
    var root = InstallationJson.parse(raw);
    if (!root.isArray() || root.size() > 10000)
      throw new IllegalArgumentException("invalid target map");
    var values = new ArrayList<Binding>();
    var devices = new HashSet<UUID>();
    var targets = new HashSet<String>();
    for (var value : root) {
      var fields = new HashSet<String>();
      value.propertyNames().forEach(fields::add);
      if (!value.isObject()
          || !fields.containsAll(Set.of("tenant_id", "realm", "device_id", "target_id"))
          || !Set.of("tenant_id", "realm", "device_id", "target_id", "node_id").containsAll(fields))
        throw new IllegalArgumentException("invalid target fields");
      String realm = value.path("realm").asString();
      String target = value.path("target_id").asString();
      if (!realm.matches("[1-9][0-9]{0,19}")
          || new java.math.BigInteger(realm).bitLength() > 64
          || !target.matches("[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}"))
        throw new IllegalArgumentException("invalid target scope");
      var binding =
          new Binding(
              UUID.fromString(value.path("tenant_id").asString()),
              realm,
              UUID.fromString(value.path("device_id").asString()),
              target,
              value.has("node_id") ? UUID.fromString(value.path("node_id").asString()) : null);
      if (!devices.add(binding.device())
          || !targets.add(binding.tenant() + "/" + realm + "/" + target))
        throw new IllegalArgumentException("duplicate target binding");
      values.add(binding);
    }
    bindings = List.copyOf(values);
  }

  List<Binding> forHost(UUID tenant, String realm, UUID host) {
    return bindings.stream()
        .filter(b -> b.tenant().equals(tenant) && b.realm().equals(realm) && host.equals(b.host()))
        .toList();
  }

  List<Binding> bindings() {
    return bindings;
  }

  boolean ready(
      IamStore store,
      MessageNetworkControlLedger ledger,
      NodeControlAuthority nodes,
      UUID tenant,
      String realm,
      String node) {
    store.requireTransactionReleased();
    if (!tenant.equals(TenantContext.require()) || !node.matches("[1-9][0-9]{0,19}")) return false;
    var devices =
        store.tx(
            () ->
                store
                    .networkJdbc()
                    .query(
                        "SELECT n.device_id FROM iam.network_node n JOIN iam.network_realm r USING(tenant_id) "
                            + "WHERE n.tenant_id=? AND r.realm=? AND n.node=? AND NOT n.revoked",
                        (rs, i) -> rs.getObject(1, UUID.class),
                        tenant,
                        new java.math.BigDecimal(realm),
                        new java.math.BigDecimal(node)));
    if (devices.size() != 1) return false;
    var binding =
        bindings.stream()
            .filter(
                b ->
                    b.tenant().equals(tenant)
                        && b.realm().equals(realm)
                        && b.device().equals(devices.getFirst()))
            .findFirst()
            .orElse(null);
    if (binding == null) return false;
    var admission =
        MessageNetworkAdmission.fromLedger(
            (t, r, target) -> ledger.isolation(t.toString(), r, target));
    if (!admission.ready(tenant, realm, binding.target())) return false;
    if (binding.host() == null) return true;
    if (nodes == null) return false;
    var host = nodes.binding(tenant, binding.host());
    return realm.equals(host.realm()) && admission.ready(tenant, realm, host.pin().getTargetId());
  }
}
