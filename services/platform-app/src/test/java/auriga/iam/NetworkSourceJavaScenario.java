package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.persistence.UuidV7;
import auriga.contracts.collab.v1.*;
import auriga.contracts.iam.v1.*;
import com.google.protobuf.ByteString;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.function.Function;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Generated mailbox RPC driver. Native witnesses here are Java fixtures, never real DSH evidence.
 */
final class NetworkSourceJavaScenario {
  final NetworkSourceJavaHost host;
  final NetworkMailboxConnection connection;
  final String boot = "bridge-" + UuidV7.next();
  final String epoch;
  final List<ObjectNode> registrations = new ArrayList<>();
  int next;

  NetworkSourceJavaScenario(NetworkSourceJavaHost host, NetworkAuthorityConnection c)
      throws Exception {
    this.host = host;
    connection =
        NetworkMailboxConnection.newBuilder()
            .setTenantId(c.getTenantId())
            .setRealm(c.getRealm())
            .setNode(c.getNode())
            .setIngress(c.getIngress())
            .setServerId(c.getServerId())
            .setClientId(c.getClientId())
            .setClientAccount(c.getClientAccount())
            .build();
    var ready =
        base("ready")
            .put("channel_nonce", "nonce-" + UuidV7.next())
            .put("boot_id", boot)
            .put("keyset_version", "1")
            .putNull("pending_registration")
            .put("resume_cursor", "0")
            .put("local_high_water", "0")
            .put("registration_revision", "0");
    for (var field : List.of("features", "adapter_capabilities"))
      ready.putArray(field).add("server_auth_v1").add("source_attestation_v1").add("inbox_only_v1");
    ready
        .putObject("receipt_checkpoint")
        .put("checkpoint_seq", "0")
        .put("checkpoint_revision", "0")
        .put("summary_digest", "sha256:" + "a".repeat(64));
    var reply =
        call(
            stub ->
                stub.ready(
                        NetworkMailboxServiceReadyRequest.newBuilder()
                            .setConnection(connection)
                            .setFrameJson(bytes(ready))
                            .build())
                    .getFrameJson());
    assertEquals("ready", reply.path("status").asString(), reply.toString());
    epoch = reply.path("epoch").asString();
    // Prepare both locals while authorized, so the post-revocation source test reaches IAM's
    // real current-Org authorization instead of failing on local allocation or revision.
    for (int i = 1; i <= 2; i++) {
      var local =
          base("local_register")
              .put("epoch", epoch)
              .put("boot_id", boot)
              .put("candidate_local", Integer.toString(i))
              .put("address_type", "session")
              .put("allocation_op_id", "allocation-" + UuidV7.next())
              .put("allocation_fingerprint", "sha256:" + "a".repeat(64))
              .put("expected_registration_revision", Integer.toString(i - 1));
      var registered =
          call(
              stub ->
                  stub.localRegister(
                          NetworkMailboxServiceLocalRegisterRequest.newBuilder()
                              .setConnection(connection)
                              .setFrameJson(bytes(local))
                              .build())
                      .getFrameJson());
      assertEquals("registered", registered.path("result").asString(), registered.toString());
      var registration = Json.object();
      for (var field :
          List.of(
              "registration_id",
              "allocation_op_id",
              "registration_revision",
              "local",
              "address_type")) registration.set(field, registered.path(field));
      registration.put("allocation_fingerprint", "sha256:" + "a".repeat(64));
      registrations.add(registration);
    }
  }

  JsonNode register() throws Exception {
    int index = next++;
    var frame =
        base("source_register")
            .put("operation_id", "source-" + UuidV7.next())
            .put("epoch", epoch)
            .put("boot_id", boot)
            .put("mode", "issue")
            .put("scope", "durable_unicast_send")
            .put("expected_revision", "0")
            .put("predecessor_binding", "");
    frame.set("registration", registrations.get(index));
    var observed = host.clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    frame
        .putObject("native_witness")
        .put("witness_id", "witness-" + UuidV7.next())
        .put("binding_id", "java-fixture-" + index)
        .put("binding_revision", "1")
        .put("observed_at", observed.toString())
        .put("valid_until", observed.plusSeconds(30).toString());
    return call(
        stub ->
            stub.sourceRegister(
                    NetworkMailboxServiceSourceRegisterRequest.newBuilder()
                        .setConnection(connection)
                        .setFrameJson(bytes(frame))
                        .build())
                .getFrameJson());
  }

  RegisterAgentSourceResponse registerFreshThroughIam(NetworkAuthorityConnection c)
      throws Exception {
    var registration = registrations.get(1);
    var request =
        RegisterAgentSourceRequest.newBuilder()
            .setConnection(c)
            .setRegistration(
                NetworkAgentSourceRegistration.newBuilder()
                    .setRegistrationId(registration.path("registration_id").asString())
                    .setAllocationOpId(registration.path("allocation_op_id").asString())
                    .setAllocationFingerprint(
                        registration.path("allocation_fingerprint").asString())
                    .setRegistrationRevision(
                        Long.parseLong(registration.path("registration_revision").asString()))
                    .setRealm(c.getRealm())
                    .setNode(c.getNode())
                    .setLocal(registration.path("local").asString())
                    .setAddressType("session"))
            .setOperationId("denied-" + UuidV7.next())
            .setScope(NetworkAgentSourceScope.NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND)
            .build();
    try (var rpc = host.rpc("collab")) {
      return TenantContext.call(
          host.tenant,
          () ->
              NetworkSourceAuthorityServiceGrpc.newBlockingStub(rpc.channel())
                  .registerAgentSource(request));
    }
  }

  private ObjectNode base(String type) {
    return Json.object()
        .put("app_version", "1.0")
        .put("type", type)
        .put("request_id", "request-" + UuidV7.next());
  }

  private static ByteString bytes(ObjectNode frame) {
    return ByteString.copyFrom(NetworkJson.canonical(frame), StandardCharsets.UTF_8);
  }

  private JsonNode call(
      Function<NetworkMailboxServiceGrpc.NetworkMailboxServiceBlockingStub, ByteString> work)
      throws Exception {
    try (var rpc = host.mailboxRpc()) {
      return TenantContext.call(
          host.tenant,
          () ->
              NetworkJson.parse(
                  work.apply(NetworkMailboxServiceGrpc.newBlockingStub(rpc.channel()))
                      .toByteArray()));
    }
  }
}
