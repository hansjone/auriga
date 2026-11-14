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
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

@RestController
@ConditionalOnExpression(
    "${auriga.services.collab.enabled:true} && ${auriga.collab.network.auto-renew.enabled:false} && ('${auriga.service.schema:}' == 'collab' || ${auriga.platform.combined:false})")
public class MsgnetRenewalController {
  private final java.util.function.Supplier<MsgnetRenewal> service;
  private final NetworkRenewal api;
  private final java.util.function.Supplier<String> publicControl;
  private final java.util.function.Supplier<MsgnetPki> pki;
  private final java.util.function.Supplier<MsgnetControlLedger> identities;

  public MsgnetRenewalController(
      MsgnetRenewal service, NetworkRenewal api, MsgnetPki pki, MsgnetControlLedger identities) {
    this.service = () -> service;
    this.api = api;
    this.publicControl = service::publicControl;
    this.pki = () -> pki;
    this.identities = () -> identities;
  }

  @org.springframework.beans.factory.annotation.Autowired
  public MsgnetRenewalController(
      org.springframework.beans.factory.ObjectProvider<MsgnetRenewal> service,
      NetworkRenewal api,
      org.springframework.beans.factory.ObjectProvider<MsgnetPki> pki,
      org.springframework.beans.factory.ObjectProvider<MsgnetControlLedger> identities,
      org.springframework.beans.factory.ObjectProvider<MsgnetPublicControl> publicControl) {
    this.service = service::getObject;
    this.api = api;
    this.publicControl = () -> MsgnetNetworkRenewal.available(publicControl::getObject).current();
    this.pki = pki::getObject;
    this.identities = identities::getObject;
  }

  @GetMapping(value = "/api/collab/v2/network/keys", produces = "application/json")
  @RequiresPermission("collab.msgnet.read")
  public ResponseEntity<String> publication() {
    return response(() -> MsgnetNetworkRenewal.available(service).publication());
  }

  @GetMapping(value = "/api/collab/v2/network/keys/announcement", produces = "application/json")
  @RequiresPermission("collab.msgnet.read")
  public ResponseEntity<String> announcement() {
    return response(() -> MsgnetNetworkRenewal.available(service).announcement());
  }

  @GetMapping(value = "/api/collab/v2/network/keys/status", produces = "application/json")
  @RequiresPermission("collab.msgnet.read")
  public ResponseEntity<String> status() {
    return response(() -> json(api.status()));
  }

  @PublicEndpoint
  @GetMapping(value = "/api/collab/v2/network/bootstrap/controls", produces = "application/json")
  public ResponseEntity<String> publicControl() {
    return publicResponse(publicControl.get());
  }

  @PublicEndpoint
  @GetMapping(value = "/api/collab/v2/network/bootstrap/keys", produces = "application/json")
  public ResponseEntity<String> publicKeys() {
    return publicResponse(MsgnetNetworkRenewal.available(service).publication());
  }

  @PublicEndpoint
  @GetMapping(value = "/api/collab/v2/network/bootstrap/transport", produces = "application/json")
  public ResponseEntity<String> publicTransport() {
    return publicResponse(MsgnetRenewal.canonical(MsgnetNetworkRenewal.available(pki).bundle()));
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
    var model = input(request, ProjectionRequest.class);
    mtls(http, model);
    return response(() -> json(api.committedProtocol(model)));
  }

  @RequiresPermission("collab.msgnet.projection.read")
  @PostMapping(
      value = "/api/collab/v2/network/projections/transport",
      produces = "application/json")
  public ResponseEntity<String> transport(@RequestBody byte[] request, HttpServletRequest http) {
    var model = input(request, ProjectionRequest.class);
    mtls(http, model);
    return response(() -> json(api.committedTransport(model)));
  }

  @PublicEndpoint
  @PostMapping(value = "/api/collab/v2/network/bootstrap/materials", produces = "application/json")
  public ResponseEntity<String> sealed(@RequestBody byte[] request, HttpServletRequest http) {
    if (!http.isSecure()) throw new AurigaException(CommonErrors.FORBIDDEN);
    return publicResponse(json(api.sealedBootstrap(input(request, ProjectionRequest.class))));
  }

  @RequiresPermission("collab.msgnet.projection.ack")
  @PostMapping(value = "/api/collab/v2/network/projections/receipts", produces = "application/json")
  public ResponseEntity<String> projected(@RequestBody byte[] request) {
    return response(() -> json(api.confirmProjection(input(request, ProjectionReceipt.class))));
  }

  @RequiresPermission("collab.msgnet.read")
  @PostMapping(
      value = "/api/collab/v2/network/transport/confirmation",
      produces = "application/json")
  public ResponseEntity<String> confirmation(@RequestBody byte[] request, HttpServletRequest http) {
    internalMtls(http);
    return response(
        () -> json(api.transportConfirmation(input(request, TransportConfirmationQuery.class))));
  }

  @RequiresPermission("collab.msgnet.read")
  @GetMapping(
      value = "/api/collab/v2/network/transport/history/{digest}",
      produces = "application/json")
  public ResponseEntity<String> history(@PathVariable String digest, HttpServletRequest http) {
    internalMtls(http);
    return response(() -> json(api.transportHistory(digest)));
  }

  @RequiresPermission("collab.msgnet.read")
  @GetMapping(
      value = "/api/collab/v2/network/transport/transitions/{authority_digest}",
      produces = "application/json")
  public ResponseEntity<String> transition(
      @PathVariable("authority_digest") String digest, HttpServletRequest http) {
    internalMtls(http);
    return response(() -> json(api.transportTransition(digest)));
  }

  @RequiresPermission("collab.msgnet.read")
  @GetMapping(value = "/api/collab/v2/network/isolation/{target_id}", produces = "application/json")
  public ResponseEntity<String> isolation(
      @PathVariable("target_id") String target, HttpServletRequest http) {
    internalMtls(http);
    return response(() -> json(api.isolation(target)));
  }

  @RequiresPermission("collab.msgnet.read")
  @GetMapping(
      value = "/api/collab/v2/network/controls/pending/{target_id}",
      produces = "application/json")
  public ResponseEntity<String> pending(
      @PathVariable("target_id") String target, HttpServletRequest http) {
    internalMtls(http);
    return response(() -> json(api.pendingControl(target)));
  }

  private void internalMtls(HttpServletRequest request) {
    Object certificates = request.getAttribute("jakarta.servlet.request.X509Certificate");
    if (!request.isSecure()
        || !(certificates instanceof java.security.cert.X509Certificate[] chain)
        || chain.length == 0) throw new AurigaException(CommonErrors.FORBIDDEN);
    MsgnetNetworkRenewal.available(identities).mtlsReader(chain[0]);
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

  private void mtls(HttpServletRequest request, ProjectionRequest model) {
    Object certificates = request.getAttribute("jakarta.servlet.request.X509Certificate");
    if (!request.isSecure()
        || !(certificates instanceof java.security.cert.X509Certificate[] chain)
        || chain.length == 0) throw new AurigaException(CommonErrors.FORBIDDEN);
    MsgnetNetworkRenewal.available(identities).mtls(MsgnetNetworkRenewal.tree(model), chain[0]);
  }

  private ResponseEntity<String> response(java.util.function.Supplier<String> body) {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    try {
      return TenantContext.call(
          principal.tenantId(),
          () -> {
            MsgnetNetworkRenewal.available(service).requireCallerTenant();
            return ResponseEntity.ok().cacheControl(CacheControl.noStore()).body(body.get());
          });
    } catch (org.springframework.dao.DataAccessException unavailable) {
      throw MsgnetV2Verifier.failure("projection_pending");
    }
  }
}
