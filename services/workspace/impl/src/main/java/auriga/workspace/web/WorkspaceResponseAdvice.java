package auriga.workspace.web;

import java.util.Map;
import org.springframework.core.MethodParameter;
import org.springframework.http.MediaType;
import org.springframework.http.converter.HttpMessageConverter;
import org.springframework.http.server.*;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.servlet.mvc.method.annotation.ResponseBodyAdvice;

/** Preserve common problem mapping, adding the admission response's contract header. */
@RestControllerAdvice
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
public class WorkspaceResponseAdvice implements ResponseBodyAdvice<Object> {
  @Override
  public boolean supports(
      MethodParameter returnType, Class<? extends HttpMessageConverter<?>> converter) {
    return true;
  }

  @Override
  public Object beforeBodyWrite(
      Object body,
      MethodParameter returnType,
      MediaType contentType,
      Class<? extends HttpMessageConverter<?>> converter,
      ServerHttpRequest request,
      ServerHttpResponse response) {
    if (body instanceof Map<?, ?> problem && "workspace.admission_full".equals(problem.get("code")))
      response.getHeaders().set("Retry-After", "5");
    return body;
  }
}
