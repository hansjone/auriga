package auriga.collab.network.keys;

import auriga.collab.api.NetworkRenewal;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AuthorizationRules;
import auriga.common.security.PublicEndpoint;
import auriga.common.security.RequiresPermission;
import auriga.contracts.msgnet.model.*;
import jakarta.servlet.http.HttpServletRequest;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.http.CacheControl;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

@RestController
@ConditionalOnExpression(
    "${auriga.services.collab.enabled:true} && ${auriga.collab.network.auto-renew.enabled:false} && '${auriga.service.schema:}' == 'collab'")
public class MsgnetRenewalController {
  private final MsgnetRenewal service;
  private final NetworkRenewal api;
  private final MsgnetPki pki;

  public MsgnetRenewalController(MsgnetRenewal service, NetworkRenewal api, MsgnetPki pki) {
    this.service = service;
    this.api = api;
    this.pki = pki;
  }

  @GetMapping(value = "/api/collab/v2/network/keys", produces = "application/json")
  @RequiresPermission("collab.msgnet.read")
  public ResponseEntity<String> publication() {
    return response(service::publication);
  }

  @GetMapping(value = "/api/collab/v2/network/keys/announcement", produces = "application/json")
  @RequiresPermission("collab.msgnet.read")
  public ResponseEntity<String> announcement() {
    return response(service::announcement);
  }

  @GetMapping(value = "/api/collab/v2/network/keys/status", produces = "application/json")
  @RequiresPermission("collab.msgnet.read")
  public ResponseEntity<String> status() {
    return response(service::status);
  }

  @PublicEndpoint
  @GetMapping(value = "/api/collab/v2/network/bootstrap/controls", produces = "application/json")
  public ResponseEntity<String> publicControl() {
    return publicResponse(service.publicControl());
  }

  @PublicEndpoint
  @GetMapping(value = "/api/collab/v2/network/bootstrap/keys", produces = "application/json")
  public ResponseEntity<String> publicKeys() {
    return publicResponse(service.publication());
  }

  @PublicEndpoint
  @GetMapping(value = "/api/collab/v2/network/bootstrap/transport", produces = "application/json")
  public ResponseEntity<String> publicTransport() {
    return publicResponse(MsgnetRenewal.canonical(pki.bundle()));
  }

  @RequiresPermission("collab.msgnet.control.install")
  @PostMapping(value = "/api/collab/v2/network/controls/packages", produces = "application/json")
  public ResponseEntity<String> create(@RequestBody byte[] request) {
    return response(() -> json(api.createControlPackage(input(request, ControlPackage.class))));
  }

  @RequiresPermission("collab.msgnet.control.install")
  @PostMapping(value = "/api/collab/v2/network/controls/receipts", produces = "application/json")
  public ResponseEntity<String> confirm(@RequestBody byte[] request) {
    return response(() -> json(api.confirmControl(input(request, InstallationReceipt.class))));
  }

  @RequiresPermission("collab.msgnet.isolation.enforce")
  @PostMapping(value = "/api/collab/v2/network/isolation", produces = "application/json")
  public ResponseEntity<String> isolate(@RequestBody byte[] request) {
    return response(() -> json(api.enforceIsolation(input(request, IsolationRecord.class))));
  }

  @RequiresPermission("collab.msgnet.projection.read")
  @PostMapping(value = "/api/collab/v2/network/projections/protocol", produces = "application/json")
  public ResponseEntity<String> protocol(@RequestBody byte[] request, HttpServletRequest http) {
    mtls(http);
    return response(() -> json(api.committedProtocol(input(request, ProjectionRequest.class))));
  }

  @RequiresPermission("collab.msgnet.projection.read")
  @PostMapping(
      value = "/api/collab/v2/network/projections/transport",
      produces = "application/json")
  public ResponseEntity<String> transport(@RequestBody byte[] request, HttpServletRequest http) {
    mtls(http);
    return response(() -> json(api.committedTransport(input(request, ProjectionRequest.class))));
  }

  @RequiresPermission("collab.msgnet.projection.read")
  @PostMapping(value = "/api/collab/v2/network/bootstrap/materials", produces = "application/json")
  public ResponseEntity<String> sealed(@RequestBody byte[] request) {
    return response(() -> json(api.sealedBootstrap(input(request, ProjectionRequest.class))));
  }

  @RequiresPermission("collab.msgnet.projection.ack")
  @PostMapping(value = "/api/collab/v2/network/projections/receipts", produces = "application/json")
  public ResponseEntity<String> projected(@RequestBody byte[] request) {
    return response(() -> json(api.confirmProjection(input(request, ProjectionReceipt.class))));
  }

  private static String json(Object value) {
    return MsgnetRenewal.canonical(MsgnetNetworkRenewal.tree(value));
  }

  private static <T> T input(byte[] bytes, Class<T> type) {
    try {
      if (bytes.length > 1048576) throw MsgnetV2Verifier.failure("invalid_chain");
      var decoded =
          java.nio.charset.StandardCharsets.UTF_8
              .newDecoder()
              .onMalformedInput(java.nio.charset.CodingErrorAction.REPORT)
              .onUnmappableCharacter(java.nio.charset.CodingErrorAction.REPORT)
              .decode(java.nio.ByteBuffer.wrap(bytes));
      var node = auriga.collab.app.NetworkJson.parse(decoded.toString(), 1048576);
      MsgnetV2Verifier.schema(type.getSimpleName(), node, "invalid_chain");
      return tools.jackson.databind.json.JsonMapper.builder().build().treeToValue(node, type);
    } catch (java.nio.charset.CharacterCodingException e) {
      throw MsgnetV2Verifier.failure("invalid_chain");
    }
  }

  private static ResponseEntity<String> publicResponse(String body) {
    return ResponseEntity.ok().cacheControl(CacheControl.noStore()).body(body);
  }

  private static void mtls(HttpServletRequest request) {
    Object certificates = request.getAttribute("jakarta.servlet.request.X509Certificate");
    if (!request.isSecure()
        || !(certificates instanceof java.security.cert.X509Certificate[] chain)
        || chain.length == 0) throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private ResponseEntity<String> response(java.util.function.Supplier<String> body) {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    return TenantContext.call(
        principal.tenantId(),
        () -> ResponseEntity.ok().cacheControl(CacheControl.noStore()).body(body.get()));
  }
}
