package auriga.iam;

import static auriga.iam.SharedTaskTokens.*;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.contracts.iam.v1.*;
import auriga.contracts.node.v1.SharedDshCapability;
import com.google.protobuf.ByteString;
import com.sun.net.httpserver.*;
import io.grpc.Context;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.mock.env.MockEnvironment;
import tools.jackson.databind.JsonNode;

class SharedOutboundTest {
  private static final String TOKEN_HEADER = "X-Enterprise-Token";
  private static final String EMPLOYEE_HEADER = "X-Employee-Id";

  @Test
  void rawDisabledStillExecutesOldAndNewOutboundTasksWithoutExposingMaterials() throws Exception {
    var f = new SharedTasksTest.Fixture();
    var calls = new AtomicInteger();
    var inTransaction = new AtomicBoolean();
    doAnswer(
            call -> {
              assertFalse(inTransaction.getAndSet(true));
              try {
                return ((Supplier<?>) call.getArgument(0)).get();
              } finally {
                inTransaction.set(false);
              }
            })
        .when(f.store)
        .tx(any());
    var server =
        server(
            exchange -> {
              assertFalse(inTransaction.get(), "business I/O retained IAM transaction");
              calls.incrementAndGet();
              assertEquals("000042", exchange.getRequestHeaders().getFirst(EMPLOYEE_HEADER));
              assertEquals("synthetic-v1", exchange.getRequestHeaders().getFirst(TOKEN_HEADER));
              assertNull(exchange.getRequestHeaders().getFirst("Cookie"));
              assertNull(exchange.getRequestHeaders().getFirst("Authorization"));
              assertEquals(
                  "{\"business\":true}",
                  new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
              exchange.getResponseHeaders().set("Content-Type", "application/json");
              exchange.getResponseHeaders().set("Set-Cookie", "session=synthetic-cookie; HttpOnly");
              exchange.getResponseHeaders().set("Authorization", "Bearer response-token");
              reply(
                  exchange,
                  200,
                  "{\"ok\":true,\"token\":\"replacement-secret\",\"nested\":{\"empNo\":\"000042\",\"authValue\":\"other-secret\"},\"echo\":\"synthetic-v1 synthetic-cookie 000042 replacement-secret other-secret response-token\"}");
            });
    try {
      var outbound = new SharedOutbound(f.tasks, settings(server), true);
      f.run(
          () -> {
            var old = f.begin(ENTERPRISE, List.of("credentials", "outbound")).getCapability();
            f.raw.set(false);
            var fresh =
                f.tasks
                    .begin(
                        BeginSharedTaskRequest.newBuilder()
                            .setBinding(f.binding)
                            .setTaskId(UUID.randomUUID().toString())
                            .setAudience(ENTERPRISE)
                            .addScopes("outbound")
                            .build())
                    .getCapability();
            assertNotEquals(old.getTaskId(), fresh.getTaskId());
            for (var capability : List.of(old, fresh)) {
              var response =
                  outbound.execute(
                      request(server, capability)
                          .putHeaders("Cookie", "forged")
                          .putHeaders("Authorization", "forged")
                          .putHeaders(TOKEN_HEADER, "forged")
                          .putHeaders(EMPLOYEE_HEADER, "forged")
                          .build());
              assertEquals(200, response.getStatusCode());
              assertTrue(response.getBody().contains("\"ok\":true"));
              for (var secret :
                  List.of(
                      "synthetic-v1",
                      "synthetic-cookie",
                      "000042",
                      "replacement-secret",
                      "other-secret",
                      "response-token")) assertFalse(response.toString().contains(secret));
              assertEquals(
                  Set.of("content-type", "content-length"), response.getHeadersMap().keySet());
              assertEquals(
                  response.getBody().getBytes(StandardCharsets.UTF_8).length,
                  Integer.parseInt(response.getHeadersOrThrow("content-length")));
              error(
                  "common.forbidden",
                  () ->
                      f.tasks.credentials(
                          ReadSharedCredentialsRequest.newBuilder()
                              .setCapability(capability)
                              .build()));
            }
            return null;
          });
      assertEquals(2, calls.get());
      verify(f.store, atLeast(4)).requireTransactionReleased();
    } finally {
      server.stop(0);
    }
  }

  @Test
  void endedLoggedOutChangedVersionAndCrossOwnerCapabilitiesNeverDispatch() throws Exception {
    for (String scenario :
        List.of(
            "ended",
            "logout",
            "credential-version",
            "member",
            "epoch",
            "auth-generation",
            "tenant",
            "jti",
            "credentials-only",
            "model")) {
      var f = new SharedTasksTest.Fixture();
      var calls = new AtomicInteger();
      var server =
          server(
              exchange -> {
                calls.incrementAndGet();
                reply(exchange, 200, "{}");
              });
      try {
        var outbound = new SharedOutbound(f.tasks, settings(server), true);
        f.run(
            () -> {
              var capability = f.begin(ENTERPRISE, List.of("outbound")).getCapability();
              String code = "common.forbidden";
              switch (scenario) {
                case "ended" ->
                    f.tasks.finish(
                        FinishSharedTaskRequest.newBuilder()
                            .setBinding(f.binding)
                            .setTaskId(f.task)
                            .build());
                case "logout" -> {
                  f.active.set(false);
                  code = "common.unauthenticated";
                }
                case "credential-version" -> f.credential.put("version", 2);
                case "member" -> {
                  f.currentOwnership.set(
                      f.binding.toBuilder().setMemberId(UUID.randomUUID().toString()).build());
                  code = "common.unauthenticated";
                }
                case "epoch" -> {
                  capability =
                      capability.toBuilder().setBinding(f.binding.toBuilder().setEpoch(2)).build();
                  code = "common.unauthenticated";
                }
                case "auth-generation" -> f.session.put("authGeneration", 2);
                case "tenant" -> {
                  capability =
                      capability.toBuilder()
                          .setBinding(
                              f.binding.toBuilder().setTenantId(UUID.randomUUID().toString()))
                          .build();
                  code = "common.not_found";
                }
                case "jti" ->
                    capability =
                        capability.toBuilder().setJti(UUID.randomUUID().toString()).build();
                case "credentials-only" ->
                    capability = f.begin(ENTERPRISE, List.of("credentials")).getCapability();
                case "model" ->
                    capability = f.begin(MODEL, List.of("model.invoke")).getCapability();
              }
              var request = request(server, capability).build();
              error(code, () -> outbound.execute(request));
              return null;
            });
        assertEquals(0, calls.get(), scenario);
      } finally {
        server.stop(0);
      }
    }
  }

  @Test
  void tokenRotationOrLogoutDuringBusinessCallDiscardsTheLateResponse() throws Exception {
    for (boolean logout : List.of(false, true)) {
      var f = new SharedTasksTest.Fixture();
      var server =
          server(
              exchange -> {
                if (logout) f.active.set(false);
                else f.credential.put("version", 2);
                reply(exchange, 200, "{\"late\":\"must-not-escape\"}");
              });
      try {
        var outbound = new SharedOutbound(f.tasks, settings(server), true);
        f.run(
            () -> {
              var cap = f.begin(ENTERPRISE, List.of("outbound")).getCapability();
              error("common.forbidden", () -> outbound.execute(request(server, cap).build()));
              return null;
            });
      } finally {
        server.stop(0);
      }
    }
  }

  @Test
  void targetMethodsHeadersAndBoundsAreValidatedBeforeMaterialRead() throws Exception {
    var f = new SharedTasksTest.Fixture();
    var calls = new AtomicInteger();
    var server =
        server(
            exchange -> {
              calls.incrementAndGet();
              reply(exchange, 200, "{}");
            });
    try {
      var outbound = new SharedOutbound(f.tasks, settings(server), true);
      f.run(
          () -> {
            var cap = f.begin(ENTERPRISE, List.of("outbound")).getCapability();
            for (var bad :
                List.of(
                    request(server, cap).setUrl("https://unapproved.example.com/action"),
                    request(server, cap).setUrl(origin(server) + "/#fragment"),
                    request(server, cap).setMethod("CONNECT"),
                    request(server, cap).setTimeoutMs(0),
                    request(server, cap).setTimeoutMs(60001),
                    request(server, cap)
                        .setBody(ByteString.copyFrom(new byte[SharedOutbound.MAX_BODY + 1])),
                    request(server, cap).putHeaders("accept", "ok\r\nforged: value")))
              error("common.validation_failed", () -> outbound.execute(bad.build()));
            verifyNoInteractions(f.uac);
            return null;
          });
      assertEquals(0, calls.get());
      assertThrows(
          IllegalArgumentException.class,
          () -> new SharedOutbound(f.tasks, settings(server), false));
      var wrongAddresses = settings(server).deepCopy();
      ((tools.jackson.databind.node.ObjectNode) wrongAddresses.path("allowedDestinations").get(0))
          .set("addresses", Json.MAPPER.valueToTree(List.of("192.0.2.10")));
      var pinned = new SharedOutbound(f.tasks, wrongAddresses, true);
      f.run(
          () -> {
            var cap = f.begin(ENTERPRISE, List.of("outbound")).getCapability();
            error("common.unavailable", () -> pinned.execute(request(server, cap).build()));
            return null;
          });
      assertEquals(0, calls.get(), "literal IP bypassed socket address allowlist");
    } finally {
      server.stop(0);
    }
  }

  @Test
  void redirectIsNotFollowedAndInvalidOrOversizedResponseIsNotReturned() throws Exception {
    for (String scenario : List.of("redirect", "oversize", "invalid-json", "plain-text")) {
      var f = new SharedTasksTest.Fixture();
      var calls = new AtomicInteger();
      var server =
          server(
              exchange -> {
                calls.incrementAndGet();
                if (scenario.equals("redirect")) {
                  exchange
                      .getResponseHeaders()
                      .set("Location", "https://unapproved.example.com/action");
                  reply(exchange, 302, "");
                } else if (scenario.equals("oversize"))
                  reply(exchange, 200, "x".repeat(SharedOutbound.MAX_BODY + 1));
                else if (scenario.equals("invalid-json")) {
                  exchange.getResponseHeaders().set("Content-Type", "application/json");
                  reply(exchange, 200, "{\"duplicate\":1,\"duplicate\":2}");
                } else {
                  exchange.getResponseHeaders().set("Content-Type", "text/plain");
                  reply(
                      exchange,
                      200,
                      "echo synthetic-v1 000042 token=another-value authorization: Bearer new-value");
                }
              });
      try {
        var outbound = new SharedOutbound(f.tasks, settings(server), true);
        f.run(
            () -> {
              var cap = f.begin(ENTERPRISE, List.of("outbound")).getCapability();
              var request = request(server, cap).build();
              if (Set.of("oversize", "invalid-json").contains(scenario))
                error("common.unavailable", () -> outbound.execute(request));
              else {
                var response = outbound.execute(request);
                assertEquals(scenario.equals("redirect") ? 302 : 200, response.getStatusCode());
                assertFalse(response.getHeadersMap().containsKey("location"));
                assertFalse(response.getBody().contains("synthetic-v1"));
                assertFalse(response.getBody().contains("another-value"));
                assertFalse(response.getBody().contains("new-value"));
              }
              return null;
            });
        assertEquals(1, calls.get(), "transport replayed or redirected the request");
      } finally {
        server.stop(0);
      }
    }
  }

  @Test
  void timeoutAndGrpcCancellationAbortTheBusinessRequest() throws Exception {
    for (boolean cancel : List.of(false, true)) {
      var f = new SharedTasksTest.Fixture();
      var entered = new CountDownLatch(1);
      var release = new CountDownLatch(1);
      var server =
          server(
              exchange -> {
                entered.countDown();
                try {
                  release.await(3, TimeUnit.SECONDS);
                  reply(exchange, 200, "late");
                } catch (InterruptedException error) {
                  Thread.currentThread().interrupt();
                } catch (java.io.IOException ignored) {
                } finally {
                  exchange.close();
                }
              });
      try (var rpc = Context.current().withCancellation();
          var executor = Executors.newVirtualThreadPerTaskExecutor()) {
        var outbound = new SharedOutbound(f.tasks, settings(server), true);
        var cap = f.run(() -> f.begin(ENTERPRISE, List.of("outbound")).getCapability());
        var request = request(server, cap).setTimeoutMs(cancel ? 60000 : 800).build();
        var result =
            executor.submit(
                () ->
                    rpc.call(
                        () ->
                            f.run(
                                () -> {
                                  error("common.unavailable", () -> outbound.execute(request));
                                  return true;
                                })));
        try {
          assertTrue(entered.await(2, TimeUnit.SECONDS));
          if (cancel) rpc.cancel(new IllegalStateException("synthetic cancellation"));
          assertTimeoutPreemptively(
              Duration.ofSeconds(2), () -> assertTrue(result.get(2, TimeUnit.SECONDS)));
        } finally {
          rpc.cancel(null);
          release.countDown();
        }
      } finally {
        release.countDown();
        server.stop(0);
      }
    }
  }

  @Test
  void trustedPrivateConfigurationIsOptionalButMalformedConfigurationFailsClosed()
      throws Exception {
    var f = new SharedTasksTest.Fixture();
    var env = new MockEnvironment();
    var missing = SharedOutbound.settings(env);
    var disabled = new SharedOutbound(f.tasks, missing, false);
    error(
        "common.unavailable",
        () -> disabled.execute(ExecuteSharedOutboundRequest.getDefaultInstance()));
    verifyNoInteractions(f.uac);
    var path = Files.createTempFile("synthetic-outbound-", ".json");
    try {
      Files.writeString(path, "{\"sharedOutbound\":{\"empNoHeader\":\"invalid header\"}}");
      env.withProperty("auriga.iam.uac.adapter-config-file", path.toString());
      assertThrows(
          IllegalArgumentException.class,
          () -> new SharedTaskRuntimeConfiguration().sharedOutbound(f.tasks, env));
      Files.writeString(path, "{\"sharedOutbound\":{},\"sharedOutbound\":{}}");
      assertThrows(IllegalArgumentException.class, () -> SharedOutbound.settings(env));
    } finally {
      Files.delete(path);
    }
  }

  private static void error(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  private static HttpServer server(HttpHandler handler) throws Exception {
    var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext("/", handler);
    server.start();
    return server;
  }

  private static String origin(HttpServer server) {
    return "http://127.0.0.1:" + server.getAddress().getPort();
  }

  private static JsonNode settings(HttpServer server) {
    return Json.object(
        "empNoHeader",
        EMPLOYEE_HEADER,
        "authValueHeader",
        TOKEN_HEADER,
        "allowedDestinations",
        List.of(Json.object("origin", origin(server), "addresses", List.of("127.0.0.1"))));
  }

  private static ExecuteSharedOutboundRequest.Builder request(
      HttpServer server, SharedDshCapability cap) {
    return ExecuteSharedOutboundRequest.newBuilder()
        .setCapability(cap)
        .setUrl(origin(server) + "/action")
        .setMethod("POST")
        .setBody(ByteString.copyFromUtf8("{\"business\":true}"))
        .setTimeoutMs(2000)
        .putHeaders("content-type", "application/json");
  }

  private static void reply(HttpExchange exchange, int status, String body)
      throws java.io.IOException {
    byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
    exchange.sendResponseHeaders(status, bytes.length == 0 ? -1 : bytes.length);
    try (var out = exchange.getResponseBody()) {
      out.write(bytes);
    } finally {
      exchange.close();
    }
  }
}
