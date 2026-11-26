package auriga.iam.api;

import auriga.common.security.AurigaPrincipal;

/** Department display name from the authenticated caller's verified UAC session. */
public interface VerifiedAuthorDepartment {
  /** The caller's tenant must already be bound. Returns an empty string when unavailable. */
  String authorDepartment(AurigaPrincipal actor);
}
