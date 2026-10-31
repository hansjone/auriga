package auriga.workspace.api;

import auriga.contracts.workspace.v1.*;

/** Authoritative instance owner and current lifecycle generation, with no overlap. */
public interface ManagedInstanceBindings {
  GetManagedInstanceBindingResponse get(GetManagedInstanceBindingRequest request);
}
