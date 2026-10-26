package auriga.iam;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AuthenticatedOnly;
import auriga.iam.api.NetworkAuthority;
import auriga.iam.web.model.NetworkClientTicket;
import auriga.iam.web.model.NetworkRegistration;
import auriga.iam.web.model.RegisterResultSchema;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.time.ZoneOffset;
import java.util.Collections;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

@RestController
@ConditionalOnExpression("${auriga.services.iam.enabled:true}")
@ConditionalOnProperty(prefix = "auriga.iam", name = "issuer")
@RequestMapping("/api/iam/v1/network")
public class IamNetworkClientController {
  private final IamNetworkClient client;
  private final ContractSchemas schemas;

  public IamNetworkClientController(IamNetworkClient client, ContractSchemas schemas) {
    this.client = client;
    this.schemas = schemas;
  }

  @PostMapping("/register")
  @AuthenticatedOnly
  public RegisterResultSchema register(
      @RequestBody(required = false) ObjectNode body,
      HttpServletRequest request,
      HttpServletResponse response) {
    noStore(response);
    validate("network/register.schema.json", body == null ? Json.object() : body);
    var result = client.register(bearer(request));
    var node = result.identity();
    return new RegisterResultSchema()
        .tenant(auriga.common.context.TenantContext.require())
        .realm(node.realm())
        .node(node.node())
        .device(java.util.UUID.fromString(node.device()))
        .systemAddress(node.systemAddress())
        .created(result.created());
  }

  @PostMapping("/tickets")
  @AuthenticatedOnly
  public NetworkClientTicket prepare(
      @RequestBody ObjectNode body, HttpServletRequest request, HttpServletResponse response) {
    noStore(response);
    validate("network/ticket-request.schema.json", body);
    var ticket = client.prepare(bearer(request), body.path("node").asString());
    String ingress = IamNetworkAuthority.ingressForTicket(ticket.ticket());
    // Match the gateway's 32-bucket routing hint; only the up subject includes a shard.
    char shard =
        "abcdefghijklmnopqrstuvwxyz234567"
            .charAt(Integer.parseInt(Secrets.hash(ingress).substring(0, 2), 16) & 31);
    return new NetworkClientTicket()
        .identity(identity(ticket.identity()))
        .ticket(ticket.ticket())
        .serverAudience(ticket.serverAudience())
        .expiresAt(ticket.expiresAt().atOffset(ZoneOffset.UTC))
        .connectionExpiresAt(ticket.connectionExpiresAt().atOffset(ZoneOffset.UTC))
        .ingress(ingress)
        .upSubject("auriga.net.up." + shard + "." + ingress)
        .downSubject("auriga.net.down." + ingress)
        .replyPrefix("auriga.net.reply." + ingress)
        .protocolVersion(NetworkClientTicket.ProtocolVersionEnum.NUMBER_1);
  }

  private void validate(String schema, ObjectNode body) {
    try {
      schemas.validate(schema, body);
    } catch (AurigaException invalid) {
      if (!invalid.errorCode().code().equals("iam.contract_violation")) throw invalid;
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }

  private static String bearer(HttpServletRequest request) {
    var headers = Collections.list(request.getHeaders("Authorization"));
    if (headers.size() != 1) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    String header = headers.getFirst();
    if (!header.regionMatches(true, 0, "Bearer ", 0, 7)
        || header.length() <= 7
        || header.length() > 16391
        || header.substring(7).contains(" "))
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    return header.substring(7);
  }

  private static NetworkRegistration identity(NetworkAuthority.Node node) {
    return new NetworkRegistration()
        .tenant(auriga.common.context.TenantContext.require())
        .realm(node.realm())
        .node(node.node())
        .device(java.util.UUID.fromString(node.device()))
        .systemAddress(node.systemAddress());
  }

  private static void noStore(HttpServletResponse response) {
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("Pragma", "no-cache");
  }
}
