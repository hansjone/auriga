package auriga.collab.network.keys;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AuthenticatedOnly;
import auriga.common.security.AuthorizationRules;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.http.CacheControl;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

/** Fixed configured realm only; no caller-selected realm, URL, root or private-key mutation. */
@RestController
@ConditionalOnExpression(
    "${auriga.services.collab.enabled:true} && ${auriga.collab.network.key-operations.enabled:false} && '${auriga.service.schema:}' == 'collab'")
@AuthenticatedOnly
public class NetworkKeysController {
  private final NetworkKeyOperations operations;
  private final UUID tenant;

  public NetworkKeysController(NetworkKeyOperations operations, NetworkKeySettings settings) {
    this.operations = operations;
    this.tenant = settings.tenant();
  }

  @GetMapping(value = "/api/collab/v1/network/keys", produces = "application/json")
  public ResponseEntity<String> keys() {
    authorize();
    return response(operations.current().signedKeyset());
  }

  @GetMapping(value = "/api/collab/v1/network/keys/announcement", produces = "application/json")
  public ResponseEntity<String> announcement() {
    authorize();
    return response(operations.current().announcement());
  }

  private void authorize() {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!tenant.equals(principal.tenantId())) throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private static ResponseEntity<String> response(String bytes) {
    return ResponseEntity.ok().cacheControl(CacheControl.noStore()).body(bytes);
  }
}
