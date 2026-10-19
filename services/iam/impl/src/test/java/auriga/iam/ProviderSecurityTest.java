package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.*;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.iam.api.IdentityConnector;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import tools.jackson.databind.node.ObjectNode;

class ProviderSecurityTest {
  @Test
  void issuerChangeRequiresFreshSecretAndAuditContainsOnlyFieldNames() {
    UUID tenant = UUID.randomUUID();
    TenantContext.run(
        tenant,
        () -> {
          IamStore store = mock(IamStore.class);
          ConnectorRegistry registry = mock(ConnectorRegistry.class);
          AuditLogger audit = mock(AuditLogger.class);
          byte[] raw = new byte[32];
          new java.security.SecureRandom().nextBytes(raw);
          Secrets secrets = new Secrets(Base64.getEncoder().encodeToString(raw));
          String id = UUID.randomUUID().toString(),
              oldSecret = Secrets.random(),
              freshSecret = Secrets.random();
          var row =
              Json.object(
                  "providerId",
                  id,
                  "connectorType",
                  "oidc",
                  "config",
                  secrets.encrypt(
                      Json.object(
                              "issuer",
                              "https://idp.example.com",
                              "clientId",
                              "client",
                              "clientSecret",
                              oldSecret)
                          .toString(),
                      "provider:" + tenant + ":" + id));
          when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
          when(store.get(eq("idp_provider"), eq(id), eq(true))).thenAnswer(i -> row.deepCopy());
          IdentityConnector connector = mock(IdentityConnector.class);
          when(registry.get("oidc")).thenReturn(connector);
          when(connector.descriptor()).thenReturn(new OidcConnector().descriptor());
          var providers =
              new ProviderManager(
                  store, registry, secrets, audit, Clock.systemUTC(), "https://portal.example.com");
          for (ObjectNode patch :
              List.of(
                  Json.object("issuer", "https://other.example.com"),
                  Json.object(
                      "issuer",
                      "https://other.example.com",
                      "clientSecret",
                      Json.object("x-secret-set", true)),
                  Json.object("issuer", "https://other.example.com", "clientSecret", ""),
                  Json.object("issuer", "https://other.example.com", "clientSecret", oldSecret))) {
            assertEquals(
                "common.validation_failed",
                assertThrows(
                        AurigaException.class,
                        () -> providers.update(id, Json.object("config", patch)))
                    .errorCode()
                    .code());
          }
          verify(registry, never()).validate(anyString(), any());
          verify(store, never()).save(anyString(), any());
          var result =
              providers.update(
                  id,
                  Json.object(
                      "config",
                      Json.object(
                          "issuer", "https://other.example.com", "clientSecret", freshSecret)));
          assertTrue(result.path("config").path("clientSecret").path("x-secret-set").asBoolean());
          var entry = ArgumentCaptor.forClass(AuditEntry.class);
          verify(audit).record(entry.capture());
          assertEquals(
              List.of("config.issuer", "config.clientSecret"),
              entry.getValue().details().get("fields"));
          assertFalse(entry.getValue().toString().contains(freshSecret));
          assertFalse(entry.getValue().toString().contains(oldSecret));
        });
  }
}
