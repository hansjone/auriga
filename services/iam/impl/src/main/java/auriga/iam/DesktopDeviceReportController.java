package auriga.iam;

import auriga.common.error.*;
import auriga.common.security.AuthenticatedOnly;
import jakarta.servlet.http.*;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import org.springframework.web.bind.annotation.*;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.databind.DeserializationFeature;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
public class DesktopDeviceReportController {
  private final OAuthProvider oauth;
  private final RequestIdentity requests;
  private final DesktopDeviceReports reports;
  private static final JsonMapper JSON =
      JsonMapper.builder()
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
          .build();

  DesktopDeviceReportController(
      OAuthProvider oauth,
      RequestIdentity requests,
      IamStore store,
      SessionManager sessions,
      Clock clock) {
    this.oauth = oauth;
    this.requests = requests;
    this.reports = new DesktopDeviceReports(store, sessions, clock);
  }

  @PostMapping(value = "/api/iam/v1/me/desktop-report", consumes = "application/json")
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void report(HttpServletRequest request, HttpServletResponse response) {
    IamError.validate(request.getParameterMap().isEmpty());
    response.setHeader("Cache-Control", "no-store");
    requests.call(
        request,
        () -> {
          String raw = StandardDeviceController.bearer(request);
          var identity =
              oauth.nativeDevice(raw)
                  ? oauth.nativeIdentity(raw, false)
                  : oauth.standardDevices().identity(raw);
          ObjectNode report;
          try {
            byte[] bytes = request.getInputStream().readNBytes(4097);
            IamError.validate(bytes.length > 0 && bytes.length <= 4096);
            var body = JSON.readTree(new String(bytes, StandardCharsets.UTF_8));
            IamError.validate(body instanceof ObjectNode);
            report = (ObjectNode) body;
          } catch (AurigaException e) {
            throw e;
          } catch (Exception e) {
            throw new AurigaException(CommonErrors.VALIDATION_FAILED);
          }
          reports.record(identity, report);
          return null;
        });
  }
}
