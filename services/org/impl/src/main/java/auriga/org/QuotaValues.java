package auriga.org;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.math.BigInteger;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Known fields of the OpenAPI Quota schema; additional properties remain untouched. */
final class QuotaValues {
  static ObjectNode validate(JsonNode value) {
    require(value != null && value.isObject());
    require(!value.has("model"));
    JsonNode instance = value.get("instance");
    if (instance != null) {
      require(instance.isObject());
      integer(instance.get("cpuMillis"), 100, false);
      integer(instance.get("memoryMb"), 256, false);
      integer(instance.get("diskGb"), 1, false);
      integer(instance.get("maxInstances"), 0, true);
      JsonNode alwaysOn = instance.get("alwaysOnAllowed");
      require(alwaysOn == null || alwaysOn.isBoolean());
    }
    return (ObjectNode) value;
  }

  static ObjectNode read(String json) {
    return validate(OrgJson.MAPPER.readTree(json));
  }

  /** The HTTP JSON integer has no protocol maximum; gRPC's optional int64 does. */
  static long int64(JsonNode value) {
    require(value.isIntegralNumber() && value.canConvertToLong());
    return value.longValue();
  }

  private static void integer(JsonNode value, long minimum, boolean nullable) {
    if (value == null || (nullable && value.isNull())) return;
    require(value.isIntegralNumber());
    require(value.bigIntegerValue().compareTo(BigInteger.valueOf(minimum)) >= 0);
  }

  private static void require(boolean condition) {
    if (!condition) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }

  private QuotaValues() {}
}
