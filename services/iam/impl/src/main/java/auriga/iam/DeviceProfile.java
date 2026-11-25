package auriga.iam;

import java.net.URI;
import tools.jackson.databind.node.ObjectNode;

/** Minimal account projection after the caller has checked device and source authority. */
final class DeviceProfile {
  static ObjectNode project(ObjectNode account, String accountId, String tenantId) {
    var projected =
        Json.object(
            "id",
            accountId,
            "tenantId",
            tenantId,
            "displayName",
            account.path("displayName").asString(""));
    String avatar = account.path("avatarUrl").asString("");
    if (safeAvatar(avatar)) projected.put("avatarUrl", avatar);
    var result = Json.object("account", projected);
    new ContractSchemas()
        .validate("auth/standard-device.schema.json#/$defs/StandardDeviceProfile", result);
    return result;
  }

  private static boolean safeAvatar(String value) {
    if (value.isEmpty() || value.length() > 2048 || value.matches(".*[\\s\\p{Cntrl}\"'<>\\\\].*"))
      return false;
    try {
      var uri = URI.create(value);
      return ("http".equals(uri.getScheme()) || "https".equals(uri.getScheme()))
          && uri.getHost() != null
          && uri.getRawUserInfo() == null
          && uri.getRawFragment() == null
          && (uri.getPort() == -1 || uri.getPort() > 0 && uri.getPort() <= 65535);
    } catch (IllegalArgumentException invalid) {
      return false;
    }
  }

  private DeviceProfile() {}
}
