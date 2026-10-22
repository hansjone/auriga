package auriga.contracts.deploymentconfig;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.stream.Stream;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestFactory;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

class DeploymentAccessTest {
  private static final Path FIXTURES = Path.of("../../contracts/deployment/fixtures");
  private static final JsonMapper MAPPER = JsonMapper.builder().build();

  private byte[] fixture(String name) throws Exception {
    return Files.readAllBytes(FIXTURES.resolve(name));
  }

  private byte[] changed(String base, String pointer, Object value) throws Exception {
    ObjectNode node = (ObjectNode) MAPPER.readTree(fixture(base));
    apply(node, pointer, MAPPER.valueToTree(value), false);
    return MAPPER.writeValueAsBytes(node);
  }

  private byte[] changed(String base, Map<String, Object> changes) throws Exception {
    ObjectNode node = (ObjectNode) MAPPER.readTree(fixture(base));
    changes.forEach((pointer, value) -> apply(node, pointer, MAPPER.valueToTree(value), false));
    return MAPPER.writeValueAsBytes(node);
  }

  private void apply(ObjectNode node, String pointer, JsonNode value, boolean remove) {
    String[] parts = pointer.split("\\.");
    for (int i = 0; i < parts.length - 1; i++) node = (ObjectNode) node.get(parts[i]);
    if (remove) node.remove(parts[parts.length - 1]);
    else node.set(parts[parts.length - 1], value);
  }

  private ValidationException rejected(byte[] data, String field) {
    var error = assertThrows(ValidationException.class, () -> DeploymentAccess.load(data));
    assertEquals("common.validation_failed", error.code());
    if (field != null) assertEquals(field, error.field());
    return error;
  }

  @TestFactory
  Stream<DynamicTest> originalPositiveFixtures() {
    return Stream.of(
            "ip-http.json",
            "domain-proxy.json",
            "domain-root-portal.json",
            "domain-nested-parent.json")
        .map(
            name ->
                DynamicTest.dynamicTest(
                    name,
                    () -> {
                      var config = DeploymentAccess.load(fixture(name));
                      assertEquals("019a0000-0000-7000-8000-000000000001", config.tenantId());
                      assertEquals(config.portalUrl(), config.issuer());
                      assertNotNull(config.mode());
                    }));
  }

  @TestFactory
  List<DynamicTest> originalNegativeFixtures() throws Exception {
    var tests = new ArrayList<DynamicTest>();
    var semanticFields =
        Map.ofEntries(
            Map.entry("issuer-differs", "/issuerOrigin"),
            Map.entry("same-origin-admin", "/adminOrigin"),
            Map.entry("different-ip-admin", "/adminOrigin"),
            Map.entry("http-unacknowledged", "/insecureHttpAcknowledged"),
            Map.entry("listener-collision", "/listeners/admin"),
            Map.entry("direct-tls-mismatch", "/listeners"),
            Map.entry("alias-origin-mismatch", "/instanceControl/origin/host"),
            Map.entry("internal-port-mismatch", "/instanceControl/origin"),
            Map.entry("portal-captured-by-instance-host", "/portalOrigin/host"),
            Map.entry("admin-captured-by-instance-host", "/adminOrigin/host"),
            Map.entry("proxy-cidr-invalid", "/ingress/peerCidrs/0"),
            Map.entry("proxy-cidr-all", "/ingress/peerCidrs/0"));
    for (String file : List.of("schema-negative.json", "startup-negative.json")) {
      JsonNode cases = MAPPER.readTree(fixture(file));
      assertEquals(file.startsWith("schema") ? 32 : 12, cases.size());
      for (JsonNode c : cases)
        tests.add(
            DynamicTest.dynamicTest(
                file + "/" + c.get("name").asString(),
                () -> {
                  ObjectNode value =
                      (ObjectNode) MAPPER.readTree(fixture(c.get("base").asString()));
                  apply(
                      value,
                      c.get("pointer").asString(),
                      c.get("value"),
                      c.path("remove").asBoolean());
                  for (JsonNode patch : c.path("additionalChanges"))
                    apply(
                        value,
                        patch.get("pointer").asString(),
                        patch.get("value"),
                        patch.path("remove").asBoolean());
                  if (file.startsWith("startup")) DeploymentAccess.validateStructure(value);
                  else
                    assertThrows(
                        ValidationException.class, () -> DeploymentAccess.validateStructure(value));
                  var error =
                      rejected(
                          MAPPER.writeValueAsBytes(value),
                          semanticFields.get(c.get("name").asString()));
                  assertFalse(error.field().isEmpty(), c.get("name").asString());
                }));
    }
    return tests;
  }

  @Test
  void strictJsonAndExactNumbers() throws Exception {
    String base = new String(fixture("ip-http.json"), StandardCharsets.UTF_8);
    for (String text :
        List.of(
            base + "{}",
            base + " null",
            base + " garbage",
            "null",
            "[]",
            "",
            base.substring(0, base.length() - 3),
            base.replace("\"schemaVersion\": 1", "\"schemaVersion\": 1, \"schemaVersion\": 1"),
            base.replaceFirst(
                "\"scheme\": \"http\"", "\"scheme\": \"http\", \"sc\\\\u0068eme\": \"http\""),
            "[".repeat(65) + "]".repeat(65))) rejected(text.getBytes(StandardCharsets.UTF_8), null);
    rejected(new byte[] {'{', '"', (byte) 0xff, '"', ':', '1', '}'}, "");
    rejected(new byte[(1 << 20) + 1], "");
    for (String number :
        List.of(
            "8080.0000000000000000000001",
            "1e99999",
            "1e-99999",
            "1e2147483647",
            "1e-2147483647",
            "1e2147483648",
            "-1e99999",
            "0.9999999999999999999999999"))
      assertTimeoutPreemptively(
          Duration.ofSeconds(3),
          () ->
              rejected(
                  base.replace("\"port\": 8080", "\"port\": " + number)
                      .getBytes(StandardCharsets.UTF_8),
                  "/portalOrigin/port"));
    for (String number :
        List.of(
            "8.080e3", "8080.0000000000000000000000000", "808000e-2", "8080." + "0".repeat(2048)))
      assertEquals(
          "http://192.0.2.10:8080",
          DeploymentAccess.load(
                  base.replace("\"port\": 8080", "\"port\": " + number)
                      .getBytes(StandardCharsets.UTF_8))
              .portalUrl());
    assertEquals(
        "ip",
        DeploymentAccess.load(
                base.replace("\"schemaVersion\": 1", "\"schemaVersion\": 1e0")
                    .getBytes(StandardCharsets.UTF_8))
            .mode());
  }

  @Test
  void exactMinimumMaximumAndIntegerSchemaSemantics() throws Exception {
    ObjectNode schema =
        (ObjectNode)
            MAPPER.readTree(
                Files.readAllBytes(Path.of("../../contracts/deployment/access.schema.json")));
    // Isolate bounds from the integer rule: neither fractional boundary may round
    // into the accepted range. The original schema remains unchanged on disk.
    ((ObjectNode) schema.path("$defs").path("Port")).put("type", "number");
    var bounds = DeploymentAccess.compileSchema(schema);
    String base = new String(fixture("ip-http.json"), StandardCharsets.UTF_8);
    for (String number :
        List.of(
            "0.999999999999999999999999999999",
            "65535.000000000000000000000001",
            "1e2147483647",
            "-1e2147483647",
            "1e-2147483647")) {
      JsonNode value =
          DeploymentAccess.parse(
              base.replace("\"port\": 8080", "\"port\": " + number)
                  .getBytes(StandardCharsets.UTF_8));
      assertTimeoutPreemptively(
          Duration.ofSeconds(3), () -> assertFalse(bounds.validate(value).isEmpty()));
    }
    rejected(
        base.replace("\"port\": 8080", "\"port\": 8080.00000000000000000000001")
            .getBytes(StandardCharsets.UTF_8),
        "/portalOrigin/port");
    assertDoesNotThrow(
        () ->
            DeploymentAccess.parse(
                ("[".repeat(64) + "]".repeat(64)).getBytes(StandardCharsets.UTF_8)));
    assertThrows(
        ValidationException.class,
        () ->
            DeploymentAccess.parse(
                ("[".repeat(65) + "]".repeat(65)).getBytes(StandardCharsets.UTF_8)));
  }

  @TestFactory
  List<DynamicTest> canonicalIdnaMatchesGoUnicode17() {
    // Expected results captured with x/net v0.60.0's production registration
    // profile on Go 1.27 / Unicode 17. Includes nontransitional, NFC, ContextJ,
    // Bidi, malformed A-labels, symbols and valid non-BMP labels.
    Map<String, Boolean> cases =
        Map.ofEntries(
            Map.entry("xn--fa-hia", true),
            Map.entry("xn--n3h", true),
            Map.entry("xn--ls8h", true),
            Map.entry("xn--bcher-kva", true),
            Map.entry("xn--3xa", true),
            Map.entry("xn--4xa", true),
            Map.entry("ab--cd", false),
            Map.entry("xn--a", false),
            Map.entry("xn--e-xbb", false),
            Map.entry("xn--9ca", true),
            Map.entry("xn--ab-m1t", false),
            Map.entry("xn--ab-j1t", false),
            Map.entry("xn--11b2ezcw70k", true),
            Map.entry("xn--11b2ezcs70k", true),
            Map.entry("xn--mgbc799q", false),
            Map.entry("xn--ngba799q", true),
            Map.entry("xn--abc-spe", false),
            Map.entry("xn--a-zhc", false),
            Map.entry("xn--1-zhc", true),
            Map.entry("xn--1-zhc94b", false));
    var tests = new ArrayList<DynamicTest>();
    cases.forEach(
        (label, valid) ->
            tests.add(
                DynamicTest.dynamicTest(
                    label,
                    () -> {
                      byte[] value =
                          changed("domain-proxy.json", "adminOrigin.host", label + ".example.com");
                      if (valid)
                        assertEquals(
                            "https://" + label + ".example.com",
                            DeploymentAccess.load(value).adminUrl());
                      else rejected(value, "/adminOrigin/host");
                    })));
    return tests;
  }

  @Test
  void requiredNullUnknownAndNoValueLeaks() throws Exception {
    JsonNode schema =
        MAPPER.readTree(
            Files.readAllBytes(Path.of("../../contracts/deployment/access.schema.json")));
    var fields = new ArrayList<String>();
    schema.get("required").forEach(field -> fields.add(field.asString()));
    fields.addAll(
        List.of(
            "portalOrigin.scheme",
            "portalOrigin.host",
            "portalOrigin.port",
            "listeners.portal.address",
            "listeners.portal.port",
            "listeners.portal.tls",
            "instances.prefix",
            "instances.trustedTeamAcknowledged",
            "ingress.kind",
            "instanceControl.dnsAlias",
            "instanceControl.origin",
            "instanceControl.edgeContainer"));
    for (String field : fields)
      for (boolean remove : List.of(false, true)) {
        ObjectNode value = (ObjectNode) MAPPER.readTree(fixture("ip-http.json"));
        apply(value, field, MAPPER.nullNode(), remove);
        rejected(MAPPER.writeValueAsBytes(value), null);
      }
    rejected(changed("ip-http.json", "listeners.portal.extra", true), "/listeners/portal/extra");
    rejected(changed("ip-http.json", "portalOrigin.Scheme", "http"), "/portalOrigin/Scheme");
    var error =
        rejected(
            changed("ip-http.json", "portalOrigin.host", "private-secret-value"),
            "/portalOrigin/host");
    assertFalse(error.getMessage().contains("private-secret-value"));
  }

  @Test
  void fullSchemaAndDeniedExternalRefs() throws Exception {
    JsonNode original =
        MAPPER.readTree(
            Files.readAllBytes(Path.of("../../contracts/deployment/access.schema.json")));
    var value = MAPPER.readTree(fixture("ip-http.json"));
    assertTrue(DeploymentAccess.compileSchema(original).validate(value).isEmpty());
    ObjectNode mutation = (ObjectNode) original.deepCopy();
    ((ObjectNode) mutation.get("properties").get("schemaVersion")).put("const", 2);
    assertFalse(DeploymentAccess.compileSchema(mutation).validate(value).isEmpty());
    for (String ref :
        List.of("file:///nonexistent-auriga-schema.json", "https://example.com/schema.json")) {
      ObjectNode external = MAPPER.createObjectNode().put("$ref", ref);
      assertThrows(RuntimeException.class, () -> DeploymentAccess.compileSchema(external));
    }
    rejected(changed("ip-http.json", "$schema", "https://example.com/schema.json"), "/$schema");
  }

  @Test
  void copiesAndNormalization() throws Exception {
    var config = DeploymentAccess.load(fixture("domain-proxy.json"));
    config.portalOrigin().setHost("attacker.example.com");
    config.adminOrigin().setPort(1);
    config.listeners().getPortal().setPort(1);
    config.instanceControl().getOrigin().setHost("attacker.internal");
    assertThrows(UnsupportedOperationException.class, () -> config.proxyPeerCidrs().clear());
    assertEquals("https://portal.example.com", config.portalUrl());
    assertEquals("https://admin.example.com", config.adminUrl());
    assertEquals(18080, config.listeners().getPortal().getPort());
    assertEquals("auriga-instance.internal", config.instanceControl().getOrigin().getHost());
    assertEquals("trusted_proxy", config.ingressKind());
    assertEquals("i.example.com", config.instanceParentDomain());
    assertEquals(List.of(), DeploymentAccess.load(fixture("ip-http.json")).proxyPeerCidrs());
    assertEquals(
        "019a0000-0000-7000-8000-000000000001",
        DeploymentAccess.load(
                changed("ip-http.json", "tenantId", "019A0000-0000-7000-8000-000000000001"))
            .tenantId());
  }

  @Test
  void independentSocketsWildcardCollisionsAndAliasNamespaces() throws Exception {
    rejected(
        changed(
            "ip-http.json",
            Map.of("listeners.admin.address", "127.0.0.1", "listeners.admin.port", 8080)),
        "/listeners/admin");
    rejected(
        changed(
            "ip-http.json",
            Map.of("listeners.portal.address", "127.0.0.1", "listeners.admin.port", 8080)),
        "/listeners/admin");
    rejected(
        changed("ip-http.json", "listeners.instanceControl.port", 8082),
        "/listeners/instanceControl");
    rejected(changed("domain-proxy.json", "listeners.admin.tls", false), "/listeners");
    for (String alias : List.of("admin.example.com", "i.example.com", "child.i.example.com"))
      rejected(
          changed(
              "domain-proxy.json",
              Map.of("instanceControl.dnsAlias", alias, "instanceControl.origin.host", alias)),
          "/instanceControl/dnsAlias");
    assertDoesNotThrow(
        () ->
            DeploymentAccess.load(
                changed(
                    "ip-http.json",
                    Map.of(
                        "listeners.portal.address",
                        "192.0.2.10",
                        "listeners.admin.address",
                        "192.0.2.11",
                        "listeners.admin.port",
                        8080))));
  }

  @Test
  void directHttpsAndAcknowledgedHttpProxy() throws Exception {
    var https =
        DeploymentAccess.load(
            changed(
                "ip-http.json",
                Map.ofEntries(
                    Map.entry("portalOrigin.scheme", "https"),
                    Map.entry("issuerOrigin.scheme", "https"),
                    Map.entry("adminOrigin.scheme", "https"),
                    Map.entry("portalOrigin.port", 443),
                    Map.entry("issuerOrigin.port", 443),
                    Map.entry("adminOrigin.port", 8443),
                    Map.entry("listeners.portal.tls", true),
                    Map.entry("listeners.admin.tls", true),
                    Map.entry("insecureHttpAcknowledged", false))));
    assertEquals("https://192.0.2.10", https.issuer());
    assertEquals("https://192.0.2.10:8443", https.adminUrl());
    var http =
        DeploymentAccess.load(
            changed(
                "domain-proxy.json",
                Map.ofEntries(
                    Map.entry("portalOrigin.scheme", "http"),
                    Map.entry("issuerOrigin.scheme", "http"),
                    Map.entry("adminOrigin.scheme", "http"),
                    Map.entry("instances.scheme", "http"),
                    Map.entry("portalOrigin.port", 80),
                    Map.entry("issuerOrigin.port", 80),
                    Map.entry("adminOrigin.port", 80),
                    Map.entry("instances.port", 80),
                    Map.entry("insecureHttpAcknowledged", true))));
    assertEquals("http://portal.example.com", http.portalUrl());
    assertTrue(http.listeners().getPortal().getTls());
    assertEquals("trusted_proxy", http.ingressKind());
  }

  @TestFactory
  List<DynamicTest> additionalSemantics() {
    var tests = new ArrayList<DynamicTest>();
    String[][] cases = {
      {"ip-http.json", "adminOrigin.scheme", "https", "/adminOrigin"},
      {"domain-proxy.json", "adminOrigin.host", "portal.example.com", "/adminOrigin/host"},
      {"domain-proxy.json", "adminOrigin.host", "i.example.com", "/adminOrigin/host"},
      {"domain-proxy.json", "adminOrigin.host", "not-a-uuid.i.example.com", "/adminOrigin/host"},
      {"domain-proxy.json", "instances.scheme", "http", "/instances"},
      {"domain-proxy.json", "adminOrigin.host", "xn--a.example.com", "/adminOrigin/host"},
      {"ip-http.json", "instanceControl.origin.scheme", "https", "/instanceControl/origin"}
    };
    for (String[] c : cases)
      tests.add(
          DynamicTest.dynamicTest(
              c[1] + "=" + c[2], () -> rejected(changed(c[0], c[1], c[2]), c[3])));
    for (String cidr :
        List.of(
            "192.0.2.1/24",
            "::/0",
            "2001:0db8::/32",
            "::ffff:192.0.2.0/120",
            "2001:db8::1/32",
            "192.0.2.0/024"))
      tests.add(
          DynamicTest.dynamicTest(
              cidr,
              () ->
                  rejected(
                      changed("domain-proxy.json", "ingress.peerCidrs", List.of(cidr)),
                      "/ingress/peerCidrs/0")));
    tests.add(
        DynamicTest.dynamicTest(
            "valid ipv6 CIDRs",
            () ->
                assertEquals(
                    List.of("192.0.2.0/24", "2001:db8::/32", "::/128"),
                    DeploymentAccess.load(
                            changed(
                                "domain-proxy.json",
                                "ingress.peerCidrs",
                                List.of("192.0.2.0/24", "2001:db8::/32", "::/128")))
                        .proxyPeerCidrs())));
    return tests;
  }

  @Test
  void originalResourceSingleAndExact() throws Exception {
    String name = "auriga/contracts/deployment/access.schema.json";
    assertEquals(
        1, java.util.Collections.list(getClass().getClassLoader().getResources(name)).size());
    try (var stream = getClass().getClassLoader().getResourceAsStream(name)) {
      assertNotNull(stream);
      assertArrayEquals(
          Files.readAllBytes(Path.of("../../contracts/deployment/access.schema.json")),
          stream.readAllBytes());
    }
    assertNotNull(getClass().getClassLoader().getResource("auriga/contracts/catalog/m3.json"));
  }
}
