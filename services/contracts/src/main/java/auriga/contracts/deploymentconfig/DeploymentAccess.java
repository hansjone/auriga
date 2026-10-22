package auriga.contracts.deploymentconfig;

import auriga.contracts.deploymentconfig.model.Access;
import auriga.contracts.deploymentconfig.model.AccessIngressTrustedProxy;
import auriga.contracts.deploymentconfig.model.AccessInstanceControl;
import auriga.contracts.deploymentconfig.model.AccessInstancesSubdomain;
import auriga.contracts.deploymentconfig.model.AccessListeners;
import auriga.contracts.deploymentconfig.model.Listener;
import auriga.contracts.deploymentconfig.model.Origin;
import com.ibm.icu.text.IDNA;
import com.networknt.schema.Schema;
import com.networknt.schema.SchemaLocation;
import com.networknt.schema.SchemaRegistry;
import com.networknt.schema.SchemaRegistryConfig;
import com.networknt.schema.SpecificationVersion;
import java.io.IOException;
import java.math.BigDecimal;
import java.net.InetAddress;
import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import tools.jackson.core.JsonParser;
import tools.jackson.core.JsonToken;
import tools.jackson.core.StreamReadConstraints;
import tools.jackson.core.json.JsonFactory;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ArrayNode;
import tools.jackson.databind.node.JsonNodeFactory;
import tools.jackson.databind.node.ObjectNode;

/** Validated installation state. Obtain instances only through {@link #load(byte[])}. */
public final class DeploymentAccess {
  private static final String RESOURCE = "auriga/contracts/deployment/access.schema.json";
  private static final JsonMapper MAPPER =
      JsonMapper.builder(
              JsonFactory.builder()
                  .streamReadConstraints(
                      StreamReadConstraints.builder().maxNumberLength(1 << 20).build())
                  .build())
          .build();
  private static final JsonNodeFactory NODES = JsonNodeFactory.instance;
  private static final IDNA DNS =
      IDNA.getUTS46Instance(
          IDNA.USE_STD3_RULES
              | IDNA.CHECK_BIDI
              | IDNA.CHECK_CONTEXTJ
              | IDNA.NONTRANSITIONAL_TO_ASCII);
  private final Access access;

  private DeploymentAccess(Access access) {
    this.access = access;
  }

  public static DeploymentAccess load(byte[] data) {
    JsonNode value = parse(data);
    validateStructure(value);
    try {
      Access model = MAPPER.treeToValue(normalizeIntegers(value), Access.class);
      DeploymentAccess result = new DeploymentAccess(model);
      result.validateSemantics();
      return result;
    } catch (ValidationException e) {
      throw e;
    } catch (RuntimeException e) {
      throw invalid("", "cannot decode generated deployment model");
    }
  }

  public String tenantId() {
    return access.getTenantId().toString();
  }

  public String mode() {
    return access.getMode().getValue();
  }

  public String portalUrl() {
    return originUrl(access.getPortalOrigin());
  }

  public String adminUrl() {
    return originUrl(access.getAdminOrigin());
  }

  public String issuer() {
    return originUrl(access.getIssuerOrigin());
  }

  public Origin portalOrigin() {
    return copy(access.getPortalOrigin(), Origin.class);
  }

  public Origin adminOrigin() {
    return copy(access.getAdminOrigin(), Origin.class);
  }

  public AccessListeners listeners() {
    return copy(access.getListeners(), AccessListeners.class);
  }

  public AccessInstanceControl instanceControl() {
    return copy(access.getInstanceControl(), AccessInstanceControl.class);
  }

  public String ingressKind() {
    return access.getIngress() instanceof AccessIngressTrustedProxy ? "trusted_proxy" : "direct";
  }

  public List<String> proxyPeerCidrs() {
    return access.getIngress() instanceof AccessIngressTrustedProxy proxy
        ? List.copyOf(proxy.getPeerCidrs())
        : List.of();
  }

  public String instanceParentDomain() {
    return access.getInstances() instanceof AccessInstancesSubdomain subdomain
        ? subdomain.getParentDomain()
        : "";
  }

  private static <T> T copy(T model, Class<T> type) {
    return MAPPER.convertValue(model, type);
  }

  private static String originUrl(Origin origin) {
    String scheme = origin.getScheme().getValue();
    return scheme
        + "://"
        + origin.getHost()
        + ((scheme.equals("http") && origin.getPort() == 80
                || scheme.equals("https") && origin.getPort() == 443)
            ? ""
            : ":" + origin.getPort());
  }

  static JsonNode parse(byte[] data) {
    if (data == null || data.length > 1 << 20)
      throw invalid("", "configuration exceeds 1 MiB or is absent");
    try {
      String text =
          StandardCharsets.UTF_8
              .newDecoder()
              .onMalformedInput(CodingErrorAction.REPORT)
              .onUnmappableCharacter(CodingErrorAction.REPORT)
              .decode(ByteBuffer.wrap(data))
              .toString();
      try (JsonParser parser = MAPPER.createParser(text)) {
        JsonNode value = read(parser, parser.nextToken(), "", 0);
        if (parser.nextToken() != null) throw invalid("", "trailing JSON content");
        return value;
      }
    } catch (ValidationException e) {
      throw e;
    } catch (CharacterCodingException e) {
      throw invalid("", "invalid UTF-8");
    } catch (RuntimeException e) {
      throw invalid("", "invalid JSON");
    }
  }

  private static JsonNode read(JsonParser parser, JsonToken token, String field, int depth) {
    try {
      if (token == JsonToken.START_OBJECT || token == JsonToken.START_ARRAY) {
        if (depth >= 64) throw invalid(field, "JSON nesting exceeds 64");
        if (token == JsonToken.START_OBJECT) {
          ObjectNode object = NODES.objectNode();
          HashSet<String> keys = new HashSet<>();
          while ((token = parser.nextToken()) != JsonToken.END_OBJECT) {
            if (token != JsonToken.PROPERTY_NAME) throw invalid(field, "invalid object key");
            String key = parser.currentName();
            String child = field + "/" + escape(key);
            if (!keys.add(key)) throw invalid(child, "duplicate JSON key");
            object.set(key, read(parser, parser.nextToken(), child, depth + 1));
          }
          return object;
        }
        ArrayNode array = NODES.arrayNode();
        while ((token = parser.nextToken()) != JsonToken.END_ARRAY) {
          array.add(read(parser, token, field + "/" + array.size(), depth + 1));
        }
        return array;
      }
      if (token == null) throw invalid(field, "missing JSON value");
      return switch (token) {
        case VALUE_STRING -> NODES.stringNode(parser.getString());
        case VALUE_TRUE -> NODES.booleanNode(true);
        case VALUE_FALSE -> NODES.booleanNode(false);
        case VALUE_NULL -> NODES.nullNode();
        case VALUE_NUMBER_INT, VALUE_NUMBER_FLOAT ->
            NODES.numberNode(new BigDecimal(parser.getText()));
        default -> throw invalid(field, "invalid JSON value");
      };
    } catch (ValidationException e) {
      throw e;
    } catch (RuntimeException e) {
      throw invalid(field, "invalid JSON value");
    }
  }

  private static JsonNode normalizeIntegers(JsonNode node) {
    if (node.isNumber()) return NODES.numberNode(node.decimalValue().toBigIntegerExact());
    if (node instanceof ObjectNode object) {
      for (String key : new ArrayList<>(object.propertyNames()))
        object.set(key, normalizeIntegers(object.get(key)));
    } else if (node instanceof ArrayNode array) {
      for (int i = 0; i < array.size(); i++) array.set(i, normalizeIntegers(array.get(i)));
    }
    return node;
  }

  private static final class SchemaHolder {
    private static final Schema SCHEMA = originalSchema();
  }

  private static Schema originalSchema() {
    try (var stream = DeploymentAccess.class.getClassLoader().getResourceAsStream(RESOURCE)) {
      if (stream == null) throw invalid("", "missing embedded deployment schema");
      return compileSchema(parse(stream.readAllBytes()));
    } catch (IOException | RuntimeException e) {
      throw invalid("", "embedded deployment schema cannot compile");
    }
  }

  // Package-private for verifying that even a future resource with an external ref fails closed.
  static Schema compileSchema(JsonNode source) {
    SchemaRegistry registry =
        SchemaRegistry.withDefaultDialect(
            SpecificationVersion.DRAFT_2020_12,
            builder ->
                builder
                    .schemaRegistryConfig(
                        SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build())
                    .resourceLoaders(
                        loaders ->
                            loaders.values(
                                values -> {
                                  values.clear();
                                  values.add(
                                      iri -> {
                                        throw new IllegalArgumentException(
                                            "external schema resources are disabled");
                                      });
                                })));
    // networknt's integer-bound optimization calls asLong on decimal instances.
    // Exact DecimalNode bounds select its BigDecimal comparator, preserving the
    // original schema semantics even for huge exponents and fractional values.
    Schema schema =
        registry.getSchema(
            SchemaLocation.of("urn:auriga:deployment:access:v1"), decimalNumbers(source));
    schema.initializeValidators();
    return schema;
  }

  private static JsonNode decimalNumbers(JsonNode source) {
    if (source.isNumber()) return NODES.numberNode(source.decimalValue());
    if (source instanceof ObjectNode object) {
      ObjectNode result = NODES.objectNode();
      for (String key : object.propertyNames()) result.set(key, decimalNumbers(object.get(key)));
      return result;
    }
    if (source instanceof ArrayNode array) {
      ArrayNode result = NODES.arrayNode();
      array.forEach(child -> result.add(decimalNumbers(child)));
      return result;
    }
    return source;
  }

  static void validateStructure(JsonNode value) {
    var errors = SchemaHolder.SCHEMA.validate(value);
    if (!errors.isEmpty()) {
      var issue = errors.getFirst();
      StringBuilder field = new StringBuilder();
      var location = issue.getInstanceLocation();
      for (int i = 0; i < location.getNameCount(); i++)
        field.append('/').append(escape(String.valueOf(location.getElement(i))));
      if ((issue.getKeyword().equals("required")
              || issue.getKeyword().equals("additionalProperties"))
          && issue.getProperty() != null) {
        field.append('/').append(escape(issue.getProperty()));
      }
      throw invalid(field.toString(), "does not satisfy deployment schema");
    }
  }

  private void validateSemantics() {
    canonicalHost("/portalOrigin/host", access.getPortalOrigin().getHost(), mode().equals("ip"));
    canonicalHost("/adminOrigin/host", access.getAdminOrigin().getHost(), mode().equals("ip"));
    canonicalHost("/issuerOrigin/host", access.getIssuerOrigin().getHost(), mode().equals("ip"));
    var control = access.getInstanceControl();
    canonicalHost("/instanceControl/dnsAlias", control.getDnsAlias(), false);
    canonicalHost("/instanceControl/origin/host", control.getOrigin().getHost(), false);
    Origin portal = access.getPortalOrigin();
    Origin admin = access.getAdminOrigin();
    if (!portal.equals(access.getIssuerOrigin()))
      throw invalid("/issuerOrigin", "must equal portalOrigin");
    if (mode().equals("ip")) {
      if (!portal.getHost().equals(admin.getHost())
          || portal.getScheme() != admin.getScheme()
          || portal.getPort().equals(admin.getPort()))
        throw invalid(
            "/adminOrigin", "IP entries require the same IPv4 and scheme with distinct ports");
    } else {
      if (portal.getHost().equals(admin.getHost()))
        throw invalid("/adminOrigin/host", "domain entries require distinct hosts");
      var instances = (AccessInstancesSubdomain) access.getInstances();
      canonicalHost("/instances/parentDomain", instances.getParentDomain(), false);
      if (inNamespace(portal.getHost(), instances.getParentDomain()))
        throw invalid("/portalOrigin/host", "reserved instance namespace");
      if (inNamespace(admin.getHost(), instances.getParentDomain()))
        throw invalid("/adminOrigin/host", "reserved instance namespace");
      if (!instances.getScheme().getValue().equals(portal.getScheme().getValue())
          || !instances.getPort().equals(portal.getPort()))
        throw invalid("/instances", "scheme and port must equal portalOrigin");
    }
    var listeners = access.getListeners();
    List<Listener> roles =
        List.of(listeners.getPortal(), listeners.getAdmin(), listeners.getInstanceControl());
    List<String> names = List.of("portal", "admin", "instanceControl");
    for (int i = 0; i < roles.size(); i++) {
      Listener left = roles.get(i);
      canonicalHost("/listeners/" + names.get(i) + "/address", left.getAddress(), true);
      for (int j = 0; j < i; j++) {
        Listener right = roles.get(j);
        if (left.getPort().equals(right.getPort())
            && (left.getAddress().equals(right.getAddress())
                || left.getAddress().equals("0.0.0.0")
                || right.getAddress().equals("0.0.0.0")))
          throw invalid("/listeners/" + names.get(i), "socket overlaps another listener role");
      }
    }
    if (access.getIngress() instanceof AccessIngressTrustedProxy proxy) {
      if (!listeners.getPortal().getTls() || !listeners.getAdmin().getTls())
        throw invalid("/listeners", "trusted proxy requires TLS and configured mTLS");
      int i = 0;
      for (String cidr : proxy.getPeerCidrs()) canonicalCidr(cidr, "/ingress/peerCidrs/" + i++);
    } else if (listeners.getPortal().getTls() != (portal.getScheme() == Origin.SchemeEnum.HTTPS)
        || listeners.getAdmin().getTls() != (admin.getScheme() == Origin.SchemeEnum.HTTPS)) {
      throw invalid("/listeners", "direct listener TLS must match corresponding browser scheme");
    }
    if (!access.getInsecureHttpAcknowledged()
        && (portal.getScheme() == Origin.SchemeEnum.HTTP
            || admin.getScheme() == Origin.SchemeEnum.HTTP
            || access.getInstances() instanceof AccessInstancesSubdomain sub
                && sub.getScheme().getValue().equals("http")))
      throw invalid("/insecureHttpAcknowledged", "browser HTTP requires explicit acknowledgement");
    if (!control.getOrigin().getHost().equals(control.getDnsAlias()))
      throw invalid("/instanceControl/origin/host", "must equal dnsAlias");
    if (!control.getOrigin().getPort().equals(listeners.getInstanceControl().getPort())
        || (control.getOrigin().getScheme() == Origin.SchemeEnum.HTTPS)
            != listeners.getInstanceControl().getTls())
      throw invalid("/instanceControl/origin", "port and TLS must match instanceControl listener");
    if (control.getDnsAlias().equals(portal.getHost())
        || control.getDnsAlias().equals(admin.getHost())
        || !instanceParentDomain().isEmpty()
            && inNamespace(control.getDnsAlias(), instanceParentDomain()))
      throw invalid(
          "/instanceControl/dnsAlias", "conflicts with browser or reserved instance host");
  }

  private static boolean inNamespace(String host, String parent) {
    return host.equals(parent) || host.endsWith("." + parent);
  }

  private static void canonicalHost(String field, String host, boolean ipv4) {
    if (ipv4) {
      ipv4(host, field);
    } else {
      IDNA.Info info = new IDNA.Info();
      StringBuilder ascii = new StringBuilder();
      DNS.nameToASCII(host, ascii, info);
      if (info.hasErrors() || !ascii.toString().equals(host))
        throw invalid(field, "requires canonical lowercase ASCII DNS A-labels");
    }
  }

  private static byte[] ipv4(String text, String field) {
    String[] parts = text.split("\\.", -1);
    if (parts.length != 4) throw invalid(field, "requires canonical IPv4");
    byte[] bytes = new byte[4];
    for (int i = 0; i < 4; i++) {
      if (!parts[i].matches("0|[1-9][0-9]{0,2}") || Integer.parseInt(parts[i]) > 255)
        throw invalid(field, "requires canonical IPv4");
      bytes[i] = (byte) Integer.parseInt(parts[i]);
    }
    return bytes;
  }

  private static void canonicalCidr(String text, String field) {
    try {
      String[] parts = text.split("/", -1);
      if (parts.length != 2 || !parts[1].matches("[1-9][0-9]{0,2}"))
        throw invalid(field, "requires canonical network CIDR excluding /0");
      byte[] bytes;
      String address;
      if (parts[0].contains(":")) {
        if (!parts[0].matches("[0-9a-f:]+"))
          throw invalid(field, "requires canonical network CIDR");
        bytes = InetAddress.getByName(parts[0]).getAddress();
        if (bytes.length != 16) throw invalid(field, "mapped IPv4 is forbidden");
        address = ipv6Text(bytes);
      } else {
        bytes = ipv4(parts[0], field);
        address = parts[0];
      }
      int bits = Integer.parseInt(parts[1]);
      if (bits > bytes.length * 8 || !address.equals(parts[0]))
        throw invalid(field, "requires canonical network CIDR");
      for (int i = bits; i < bytes.length * 8; i++)
        if ((bytes[i / 8] & (1 << (7 - i % 8))) != 0)
          throw invalid(field, "CIDR must use its network boundary");
    } catch (ValidationException e) {
      throw e;
    } catch (Exception e) {
      throw invalid(field, "requires canonical network CIDR");
    }
  }

  private static String ipv6Text(byte[] bytes) {
    int[] words = new int[8];
    for (int i = 0; i < 8; i++) words[i] = (bytes[i * 2] & 255) * 256 + (bytes[i * 2 + 1] & 255);
    int start = -1, length = 1;
    for (int i = 0; i < 8; ) {
      if (words[i] != 0) {
        i++;
        continue;
      }
      int end = i;
      while (end < 8 && words[end] == 0) end++;
      if (end - i > length) {
        start = i;
        length = end - i;
      }
      i = end;
    }
    StringBuilder result = new StringBuilder();
    for (int i = 0; i < 8; i++) {
      if (i == start) {
        result.append("::");
        i += length - 1;
        continue;
      }
      if (!result.isEmpty() && result.charAt(result.length() - 1) != ':') result.append(':');
      result.append(Integer.toHexString(words[i]).toLowerCase(Locale.ROOT));
    }
    return result.toString();
  }

  private static String escape(String value) {
    return value.replace("~", "~0").replace("/", "~1");
  }

  private static ValidationException invalid(String field, String message) {
    return new ValidationException(field, message);
  }
}
