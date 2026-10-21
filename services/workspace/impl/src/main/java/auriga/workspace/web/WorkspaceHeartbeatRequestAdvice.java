package auriga.workspace.web;

import auriga.common.error.AurigaException;
import auriga.common.error.FieldViolation;
import auriga.workspace.app.WorkspaceService;
import auriga.workspace.web.model.HeartbeatRequest;
import java.io.*;
import java.lang.reflect.Type;
import java.util.List;
import org.springframework.core.MethodParameter;
import org.springframework.http.*;
import org.springframework.http.converter.*;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.servlet.mvc.method.annotation.RequestBodyAdviceAdapter;

/** Required nullable fields need presence validation before binding to generated Integer fields. */
@RestControllerAdvice(assignableTypes = WorkspacePluginController.class)
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
public class WorkspaceHeartbeatRequestAdvice extends RequestBodyAdviceAdapter {
  private static final int MAX_BODY_BYTES = 64 * 1024;

  @Override
  public boolean supports(
      MethodParameter parameter, Type type, Class<? extends HttpMessageConverter<?>> converter) {
    return type == HeartbeatRequest.class;
  }

  @Override
  public HttpInputMessage beforeBodyRead(
      HttpInputMessage input,
      MethodParameter parameter,
      Type type,
      Class<? extends HttpMessageConverter<?>> converter)
      throws IOException {
    byte[] body = input.getBody().readNBytes(MAX_BODY_BYTES + 1);
    if (body.length > MAX_BODY_BYTES)
      throw AurigaException.validation(List.of(new FieldViolation("/", "too_large", null)));
    final tools.jackson.databind.JsonNode tree;
    try {
      tree = WorkspaceService.JSON.readTree(body);
    } catch (tools.jackson.core.JacksonException error) {
      throw new HttpMessageNotReadableException("Invalid heartbeat JSON", error, input);
    }
    var activity = tree == null ? null : tree.get("activity");
    if (activity != null && activity.isObject()) {
      for (String field :
          List.of("runningTurns", "openTerminals", "activeImBots", "connectedClients")) {
        var value = activity.get(field);
        if (value == null)
          throw AurigaException.validation(
              List.of(new FieldViolation("/activity/" + field, "required", null)));
        if (!value.isNull()
            && (!value.isIntegralNumber() || !value.canConvertToInt() || value.intValue() < 0))
          throw AurigaException.validation(
              List.of(new FieldViolation("/activity/" + field, "non_negative_integer", null)));
      }
    }
    // Preserve the original bytes and headers: normal generated-model validation,
    // authentication and common malformed-body handling still execute afterward.
    return new HttpInputMessage() {
      public InputStream getBody() {
        return new ByteArrayInputStream(body);
      }

      public HttpHeaders getHeaders() {
        return input.getHeaders();
      }
    };
  }
}
