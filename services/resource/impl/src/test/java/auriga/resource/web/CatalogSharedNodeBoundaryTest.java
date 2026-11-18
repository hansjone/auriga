package auriga.resource.web;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.error.*;
import auriga.resource.app.*;
import java.io.ByteArrayInputStream;
import java.security.cert.X509Certificate;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.mock.web.MockHttpServletRequest;

class CatalogSharedNodeBoundaryTest {
  @Test
  void directMtlsSelectsTheNodeTenantAndSharedAuthorizationNeverFallsBackAfterFailure()
      throws Exception {
    var shared = mock(CatalogSharedResources.class);
    var legacy = mock(CatalogDelivery.class);
    var identity = mock(ResourceDistributions.class);
    var controller = new CatalogNodeController(legacy, identity, shared);
    UUID tenant = UUID.randomUUID(), node = UUID.randomUUID(), lease = UUID.randomUUID();
    var request =
        new MockHttpServletRequest(
            "GET", "/internal/resource/v1/catalog/downloads/" + lease + "/manifest");
    request.addHeader("Authorization", "Bearer never-a-node-identity");
    request.addHeader("X-Auriga-Node-Id", node);
    assertThrows(AurigaException.class, () -> controller.manifest(lease, request));
    verifyNoInteractions(identity, shared, legacy);
    request.setSecure(true);
    var chain = new X509Certificate[] {mock(X509Certificate.class)};
    request.setAttribute("jakarta.servlet.request.X509Certificate", chain);
    when(identity.authenticate(chain)).thenReturn(node);
    when(identity.nodeTenant(node)).thenReturn(tenant);
    when(shared.nodeDownload(tenant, node, lease, null, false))
        .thenReturn(Optional.of(new ByteArrayInputStream("manifest".getBytes())));
    var response = controller.manifest(lease, request);
    assertEquals("no-store", response.getHeaders().getCacheControl());
    assertEquals("manifest", new String(response.getBody().getInputStream().readAllBytes()));
    verifyNoInteractions(legacy);
    when(shared.nodeDownload(tenant, node, lease, null, false))
        .thenThrow(new AurigaException(CommonErrors.NOT_FOUND));
    assertThrows(AurigaException.class, () -> controller.manifest(lease, request));
    verifyNoInteractions(legacy);
    doReturn(Optional.empty()).when(shared).nodeDownload(tenant, node, lease, null, false);
    when(legacy.nodeDownload(tenant, node, lease, null, false))
        .thenReturn(new ByteArrayInputStream(new byte[0]));
    controller.manifest(lease, request);
    verify(legacy).nodeDownload(tenant, node, lease, null, false);
  }
}
