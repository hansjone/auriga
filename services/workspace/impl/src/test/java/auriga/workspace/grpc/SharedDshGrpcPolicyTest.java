package auriga.workspace.grpc;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.contracts.node.v1.*;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.app.SharedDshEntryQueries;
import io.grpc.stub.StreamObserver;
import java.util.*;
import org.junit.jupiter.api.*;
import org.springframework.security.core.context.SecurityContextHolder;

class SharedDshGrpcPolicyTest {
  @Test
  void internalJwtListenerDoesNotImplementNodeTaskWrites() {
    var authority = mock(SharedDshAuthority.class);
    var adapter = new SharedDshGrpc(authority);
    var admit = mock(StreamObserver.class);
    var release = mock(StreamObserver.class);
    service("svc:agent");
    TenantContext.run(
        UUID.randomUUID(),
        () -> {
          adapter.admitSharedDshTask(AdmitSharedDshTaskRequest.getDefaultInstance(), admit);
          adapter.releaseSharedDshTask(ReleaseSharedDshTaskRequest.getDefaultInstance(), release);
        });
    var errors = org.mockito.ArgumentCaptor.forClass(Throwable.class);
    verify(admit).onError(errors.capture());
    verify(release).onError(errors.capture());
    for (var error : errors.getAllValues())
      assertEquals(
          io.grpc.Status.Code.UNIMPLEMENTED, io.grpc.Status.fromThrowable(error).getCode());
    verifyNoInteractions(authority);
  }

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
                    Set.of(),
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
    assertThrows(
        AurigaException.class,
        () ->
            TenantContext.run(
                TenantContext.PLATFORM_TENANT,
                () -> adapter.registerSharedDshSession(request, mock(StreamObserver.class))));
    SecurityContextHolder.clearContext();
    assertThrows(
        AurigaException.class,
        () ->
            TenantContext.run(
                tenant,
                () -> adapter.registerSharedDshSession(request, mock(StreamObserver.class))));
  }

  @Test
  void poolReadAllowsNodeAuthorityAndSessionLookupIsAuthenticatedRuntimeOnly() {
    var authority = mock(SharedDshAuthority.class);
    var queries = mock(SharedDshEntryQueries.class);
    var adapter = new SharedDshGrpc(authority, Set.of("svc:workspace", "svc:agent"), queries);
    UUID tenant = UUID.randomUUID(), pool = UUID.randomUUID(), session = UUID.randomUUID();
    var request =
        GetSharedDshPoolRequest.newBuilder()
            .setTenantId(tenant.toString())
            .setPhysicalInstanceId(pool.toString())
            .build();
    when(queries.pool(pool))
        .thenReturn(
            GetSharedDshPoolResponse.newBuilder()
                .setTenantId(tenant.toString())
                .setPhysicalInstanceId(pool.toString())
                .build());
    service("svc:edge");
    TenantContext.run(tenant, () -> adapter.getSharedDshPool(request, mock(StreamObserver.class)));
    verify(queries).pool(pool);
    service("svc:agent");
    TenantContext.run(tenant, () -> adapter.getSharedDshPool(request, mock(StreamObserver.class)));
    verify(queries, times(2)).pool(pool);
    service("svc:untrusted");
    assertThrows(
        AurigaException.class,
        () ->
            TenantContext.run(
                tenant, () -> adapter.getSharedDshPool(request, mock(StreamObserver.class))));
    service("svc:agent");
    var lookup =
        LookupSharedDshSessionRequest.newBuilder()
            .setTenantId(tenant.toString())
            .setPhysicalInstanceId(pool.toString())
            .setSessionId(session.toString())
            .build();
    when(queries.lookup(pool, session)).thenReturn(SharedDshSessionBinding.getDefaultInstance());
    TenantContext.run(
        tenant, () -> adapter.lookupSharedDshSession(lookup, mock(StreamObserver.class)));
    verify(queries).lookup(pool, session);
    service("svc:edge");
    assertThrows(
        AurigaException.class,
        () ->
            TenantContext.run(
                tenant, () -> adapter.lookupSharedDshSession(lookup, mock(StreamObserver.class))));
    service("svc:agent");
    assertThrows(
        AurigaException.class,
        () ->
            TenantContext.run(
                UUID.randomUUID(),
                () -> adapter.lookupSharedDshSession(lookup, mock(StreamObserver.class))));
  }
}
