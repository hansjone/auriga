package auriga.workspace.grpc;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.contracts.node.v1.*;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.api.SharedDshAuthority;
import io.grpc.stub.StreamObserver;
import java.util.*;
import org.junit.jupiter.api.*;
import org.springframework.security.core.context.SecurityContextHolder;

class SharedDshGrpcPolicyTest {
  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  void service(String name) {
    SecurityContextHolder.getContext()
        .setAuthentication(
            new AurigaAuthentication(
                new AurigaPrincipal(
                    name,
                    TenantContext.PLATFORM_TENANT,
                    PrincipalType.INTERNAL_SERVICE,
                    TokenContext.SERVICE,
                    null,
                    null,
                    null,
                    List.of(),
                    Set.of("auriga.internal"),
                    null,
                    null,
                    UUID.randomUUID().toString())));
  }

  @Test
  void authenticatedAgentRelayCanRegisterButPluginAndWrongTenantCannot() {
    var authority = mock(SharedDshAuthority.class);
    var adapter = new SharedDshGrpc(authority);
    UUID tenant = UUID.randomUUID();
    var binding = SharedDshSessionBinding.newBuilder().setTenantId(tenant.toString()).build();
    when(authority.register(binding, "")).thenReturn(binding);
    var request = RegisterSharedDshSessionRequest.newBuilder().setBinding(binding).build();
    service("svc:agent");
    TenantContext.run(
        tenant, () -> adapter.registerSharedDshSession(request, mock(StreamObserver.class)));
    verify(authority).register(binding, "");
    service("svc:plugin");
    assertThrows(
        AurigaException.class,
        () ->
            TenantContext.run(
                tenant,
                () -> adapter.registerSharedDshSession(request, mock(StreamObserver.class))));
    service("svc:agent");
    assertThrows(
        AurigaException.class,
        () ->
            TenantContext.run(
                UUID.randomUUID(),
                () -> adapter.registerSharedDshSession(request, mock(StreamObserver.class))));
    assertThrows(
        IllegalArgumentException.class, () -> new SharedDshGrpc(authority, Set.of("svc:plugin")));
  }
}
