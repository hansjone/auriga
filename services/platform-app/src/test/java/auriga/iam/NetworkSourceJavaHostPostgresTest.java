package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.contracts.iam.v1.*;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import java.util.UUID;
import org.junit.jupiter.api.Test;

/** Java bridge acceptance only: no Broker, WSS, browser login, native DSH witness or SEND. */
class NetworkSourceJavaHostPostgresTest {
  @Test
  void childProcessRefreshStopAndEof() throws Exception {
    NetworkSourceJavaProcess.verify(false);
    NetworkSourceJavaProcess.verify(true);
  }

  @Test
  void httpsRedeemOrgRevocationAndShutdown() throws Exception {
    var host = new NetworkSourceJavaHost();
    try (host) {
      host.start();
      host.assertRoleIsolation();
      assertEquals(401, host.http("/api/iam/v1/network/register", "{}", null).statusCode());
      assertEquals(401, host.http("/api/iam/v1/network/register", "{}", "invalid").statusCode());
      var device = host.seed("member");
      var registration = host.register(device);
      assertTrue(registration.path("created").asBoolean());
      assertFalse(host.register(device).path("created").asBoolean());
      var connection = host.redeem(device, registration);
      host.assertRedeemFacts(connection, device);
      assertEquals(
          200, host.http("/api/collab/v1/network/keys", null, device.access()).statusCode());
      host.assertPublishedKeys(device);
      host.export(device, host.seed("external"));
      var exported =
          auriga.collab.app.NetworkJson.parse(
              java.nio.file.Files.readString(host.directory.resolve("bridge.json")));
      NetworkSourceJavaHostTest.assertPublishedProtocolKey(
          exported,
          java.nio.file.Files.readString(
              java.nio.file.Path.of(
                  exported.path("protocolSigning").path("privateKeyFile").asString())),
          auriga.collab.app.NetworkJson.parse(
              host.http("/api/collab/v1/network/keys", null, device.access()).body()));
      var frames = new NetworkSourceJavaScenario(host, connection);
      var source = frames.register();
      assertEquals("registered", source.path("result").asString(), source.toString());
      assertEquals(1, host.sourceCount());
      var anonymous =
          assertThrows(
              auriga.common.error.AurigaException.class,
              () ->
                  TenantContext.run(
                      host.tenant, () -> host.org.deleteRoleBinding(device.binding())));
      assertEquals("org.not_found", anonymous.errorCode().code());
      host.revoke(device);
      assertTrue(auriga.common.context.RequestContext.currentPrincipal().isEmpty());
      assertEquals(
          host.revocationActor.toString(),
          host.org
              .store()
              .tenant(
                  host.tenant,
                  () ->
                      host.org
                          .store()
                          .jdbc()
                          .queryForObject(
                              "SELECT envelope->'data'->>'actorId' FROM org.outbox_event WHERE tenant_id=? AND envelope->>'type'='user.permissions_changed' ORDER BY id DESC LIMIT 1",
                              String.class,
                              host.tenant)));
      assertFalse(
          host.permissions
              .resolve(host.tenant, device.account().toString(), "device")
              .contains("auriga.connect.collab.use"));
      StatusRuntimeException denied = null;
      try {
        frames.registerFreshThroughIam(connection);
      } catch (StatusRuntimeException error) {
        denied = error;
      }
      assertEquals(1, host.sourceCount(), "revoked Org permission must not persist another source");
      assertNotNull(denied, "revoked Org permission must deny a new source");
      assertEquals(Status.Code.PERMISSION_DENIED, denied.getStatus().getCode());
      var external = host.seed("external");
      var externalConnection = host.redeem(external, host.register(external));
      assertEquals(
          "registered",
          new NetworkSourceJavaScenario(host, externalConnection)
              .register()
              .path("result")
              .asString());
    }
    host.assertClosed();
  }

  @Test
  void freshServiceTokensWrongServiceAndTenantAreRejected() throws Exception {
    try (var host = new NetworkSourceJavaHost()) {
      host.start();
      var first = host.serviceToken("collab");
      var second = host.serviceToken("collab");
      assertTrue(!first.equals(second), "issuer must issue a new JWT per RPC supplier invocation");
      assertEquals("svc:collab", host.verifier.verify(second).subject());
      var device = host.seed("member");
      var connection = host.redeem(device, host.register(device));
      var request =
          ResolveNetworkTargetRequest.newBuilder().setConnection(connection).setLocal("0").build();
      try (var wrong = host.rpc("ai-gateway")) {
        var error =
            assertThrows(
                StatusRuntimeException.class,
                () ->
                    TenantContext.call(
                        host.tenant,
                        () ->
                            NetworkAuthorityServiceGrpc.newBlockingStub(wrong.channel())
                                .resolveNetworkTarget(request)));
        assertEquals(Status.Code.PERMISSION_DENIED, error.getStatus().getCode());
      }
      try (var right = host.rpc("collab")) {
        var error =
            assertThrows(
                StatusRuntimeException.class,
                () ->
                    TenantContext.call(
                        UUID.randomUUID(),
                        () ->
                            NetworkAuthorityServiceGrpc.newBlockingStub(right.channel())
                                .resolveNetworkTarget(request)));
        assertEquals(Status.Code.PERMISSION_DENIED, error.getStatus().getCode());
        assertTrue(
            TenantContext.call(
                    host.tenant,
                    () ->
                        NetworkAuthorityServiceGrpc.newBlockingStub(right.channel())
                            .resolveNetworkTarget(request))
                .getAllowed());
      }
    }
  }
}
