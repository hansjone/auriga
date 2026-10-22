package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.testsupport.NatsExtension;
import io.nats.client.*;
import io.nats.client.api.*;
import java.lang.reflect.*;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(NatsExtension.class)
class NatsSessionKvTest {
  private ObjectNode value(String access) {
    Instant now = Instant.now();
    return Json.object(
        "sessionId",
        UUID.randomUUID(),
        "accountId",
        UUID.randomUUID(),
        "tenantId",
        UUID.randomUUID(),
        "audience",
        "admin",
        "accessToken",
        access,
        "accessTokenExpiresAt",
        now.plusSeconds(300).toString(),
        "expiresAt",
        now.plusSeconds(1200).toString(),
        "idleExpiresAt",
        now.plusSeconds(600).toString(),
        "csrfHash",
        Secrets.hash(Secrets.random()),
        "mfa",
        true,
        "authTime",
        now.toString());
  }

  @Test
  void lateWritesCannotUndoNewProjectionOrRevocationIncludingInitiallyMissingKey()
      throws Exception {
    try (var connection = Nats.connect(NatsExtension.url())) {
      try {
        connection
            .keyValueManagement()
            .create(KeyValueConfiguration.builder().name(NatsSessionKv.BUCKET).build());
      } catch (JetStreamApiException exists) {
        assertNotNull(connection.keyValue(NatsSessionKv.BUCKET));
      }
      var broker = connection.keyValue(NatsSessionKv.BUCKET);
      var current = new NatsSessionKv(connection, new ContractSchemas());
      for (boolean revoke : List.of(false, true))
        for (boolean initiallyPresent : List.of(false, true)) {
          String hash = Secrets.hash(Secrets.random());
          if (initiallyPresent) current.put(hash, value("initial"));
          var ready = new CountDownLatch(1);
          var release = new CountDownLatch(1);
          KeyValue delayed =
              (KeyValue)
                  Proxy.newProxyInstance(
                      getClass().getClassLoader(),
                      new Class<?>[] {KeyValue.class},
                      (proxy, method, args) -> {
                        if (Set.of("put", "update", "create").contains(method.getName())) {
                          ready.countDown();
                          if (!release.await(10, TimeUnit.SECONDS))
                            throw new IllegalStateException("timeout");
                        }
                        try {
                          return method.invoke(broker, args);
                        } catch (InvocationTargetException e) {
                          throw e.getCause();
                        }
                      });
          Connection delayedConnection = mock(Connection.class);
          when(delayedConnection.keyValue(NatsSessionKv.BUCKET)).thenReturn(delayed);
          var stale = new NatsSessionKv(delayedConnection, new ContractSchemas());
          try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
            Future<?> old = pool.submit(() -> stale.put(hash, value("stale")));
            try {
              assertTrue(ready.await(10, TimeUnit.SECONDS));
              if (initiallyPresent || !revoke) current.put(hash, value("new"));
              if (revoke) current.delete(hash);
            } finally {
              release.countDown();
            }
            var failure =
                assertThrows(ExecutionException.class, () -> old.get(10, TimeUnit.SECONDS));
            assertInstanceOf(IllegalStateException.class, failure.getCause());
            if (revoke) assertNull(current.get(hash));
            else assertEquals("new", current.get(hash).path("accessToken").asString());
          } finally {
            release.countDown();
          }
        }
    }
  }

  @Test
  void liveProjectionCanRecoverFromAnExistingCacheTombstone() throws Exception {
    try (var connection = Nats.connect(NatsExtension.url())) {
      try {
        connection
            .keyValueManagement()
            .create(KeyValueConfiguration.builder().name(NatsSessionKv.BUCKET).build());
      } catch (JetStreamApiException exists) {
        assertNotNull(connection.keyValue(NatsSessionKv.BUCKET));
      }
      var adapter = new NatsSessionKv(connection, new ContractSchemas());
      String hash = Secrets.hash(Secrets.random());
      adapter.delete(hash);
      adapter.put(hash, value("current"));
      assertEquals("current", adapter.get(hash).path("accessToken").asString());
    }
  }
}
