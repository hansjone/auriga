package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.resource.app.*;
import auriga.resource.persistence.ResourceStore;
import java.time.Clock;
import org.junit.jupiter.api.Test;

class ResourceAdministrationTest {
  private final ResourceStore store = mock(ResourceStore.class);
  private final ResourceAccess access = mock(ResourceAccess.class);
  private final ResourceQueries queries =
      new ResourceQueries(store, access, new ResourceCursor(new byte[32], Clock.systemUTC()));

  @Test
  void invalidPhaseAndLimitsDoNotReachPersistence() {
    for (String phase : new String[] {"draft", "withdrawn", "", "REVIEW"}) {
      var error =
          assertThrows(AurigaException.class, () -> queries.governance(null, phase, 50, null));
      assertEquals(CommonErrors.VALIDATION_FAILED, error.errorCode());
    }
    assertThrows(AurigaException.class, () -> queries.governance(null, "review", 201, null));
    verifyNoInteractions(store);
  }

  @Test
  void eachQueueRequiresItsOwnLivePermissionBeforeReading() {
    when(access.administrator(null, "resource.versions.review"))
        .thenThrow(new AurigaException(CommonErrors.FORBIDDEN));
    when(access.administrator(null, "resource.versions.publish"))
        .thenThrow(new AurigaException(CommonErrors.FORBIDDEN));
    assertThrows(AurigaException.class, () -> queries.governance(null, "review", 50, null));
    assertThrows(AurigaException.class, () -> queries.governance(null, "published", 50, null));
    verify(access).administrator(null, "resource.versions.review");
    verify(access).administrator(null, "resource.versions.publish");
    verifyNoInteractions(store);
  }
}
