package auriga.collab;

import auriga.collab.app.NetworkJson;
import java.util.UUID;
import tools.jackson.databind.JsonNode;

/** Shared wire fixture: schema tests validate the exact receipt used by the PG integration test. */
public final class NetworkMailboxWireFrames {
  private NetworkMailboxWireFrames() {}

  public static String receipt(String node, UUID delivery, UUID recipient, JsonNode message) {
    return """
        {"app_version":"1.0","type":"receipt","request_id":"receipt-r","operation_id":"op-r","epoch":"1","delivery_id":"%s","mail_seq":"1","results":[{"recipient_id":"%s","target":{"version":1,"realm":"2","node":"%s","local":"0","type":"system"},"message_digest":"%s","host_request_id":"host-r","status":"host_accepted","expected_state_version":"1","accepted_by":"system","acceptance_mode":"normal","evidence_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}
        """
        .formatted(delivery, recipient, node, NetworkJson.digest(message));
  }
}
