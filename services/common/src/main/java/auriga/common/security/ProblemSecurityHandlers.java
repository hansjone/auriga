package auriga.common.security;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.error.Problems;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.util.Objects;
import org.springframework.security.access.AccessDeniedException;
import org.springframework.security.authorization.AuthorizationDeniedException;
import org.springframework.security.authorization.AuthorizationResult;
import org.springframework.security.core.AuthenticationException;
import org.springframework.security.web.AuthenticationEntryPoint;
import org.springframework.security.web.access.AccessDeniedHandler;

/** 401/403 from the security filter chain rendered as problem details. */
public final class ProblemSecurityHandlers
    implements AuthenticationEntryPoint, AccessDeniedHandler {
  private final Problems problems;

  public ProblemSecurityHandlers(Problems problems) {
    this.problems = Objects.requireNonNull(problems);
  }

  @Override
  public void commence(
      HttpServletRequest request, HttpServletResponse response, AuthenticationException e)
      throws IOException {
    response.setHeader("WWW-Authenticate", "Bearer error=\"invalid_token\"");
    problems.write(request, response, new AurigaException(CommonErrors.UNAUTHENTICATED));
  }

  @Override
  public void handle(
      HttpServletRequest request, HttpServletResponse response, AccessDeniedException e)
      throws IOException {
    AurigaException problem =
        e instanceof AuthorizationDeniedException denied
                && denied.getAuthorizationResult() instanceof AdminContextDecision
            ? AurigaException.of(CommonErrors.FORBIDDEN, "reason", "admin_only")
            : new AurigaException(CommonErrors.FORBIDDEN);
    problems.write(request, response, problem);
  }

  /** Marker decision used by the admin path rule. */
  static final class AdminContextDecision implements AuthorizationResult {
    private final boolean granted;

    AdminContextDecision(boolean granted) {
      this.granted = granted;
    }

    @Override
    public boolean isGranted() {
      return granted;
    }
  }
}
