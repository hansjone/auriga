package auriga.common.security;

import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletRequestWrapper;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Enumeration;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.SmartInitializingSingleton;
import org.springframework.security.web.util.matcher.RequestMatcher;
import org.springframework.web.method.HandlerMethod;
import org.springframework.web.servlet.HandlerExecutionChain;
import org.springframework.web.servlet.mvc.method.RequestMappingInfo;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;
import org.springframework.web.util.ServletRequestPathUtils;

/**
 * Default deny for HTTP: at startup every {@code @RequestMapping} handler (outside Spring's own
 * packages) must declare {@link RequiresPermission}, {@link AuthenticatedOnly} or {@link
 * PublicEndpoint}; {@code @PublicEndpoint} is refused on {@code /admin/} paths. Also provides the
 * request matcher that lets {@code @PublicEndpoint} handlers through the filter chain
 * unauthenticated.
 */
public final class EndpointAuthorizationCheck implements SmartInitializingSingleton {
  private final ObjectProvider<RequestMappingHandlerMapping> mappings;

  public EndpointAuthorizationCheck(ObjectProvider<RequestMappingHandlerMapping> mappings) {
    this.mappings = mappings;
  }

  @Override
  public void afterSingletonsInstantiated() {
    List<String> problems = new ArrayList<>();
    mappings
        .orderedStream()
        .forEach(
            mapping ->
                mapping
                    .getHandlerMethods()
                    .forEach((info, handler) -> check(info, handler, problems)));
    if (!problems.isEmpty()) {
      throw new IllegalStateException(
          "Endpoints without an authorization declaration (@RequiresPermission,"
              + " @AuthenticatedOnly or @PublicEndpoint), or public admin endpoints: "
              + problems);
    }
  }

  private static void check(RequestMappingInfo info, HandlerMethod handler, List<String> problems) {
    Class<?> type = handler.getBeanType();
    if (type.getName().startsWith("org.springframework.")) return;
    String name = type.getName() + "#" + handler.getMethod().getName() + " " + info;
    if (!AuthorizationRules.isDeclared(handler.getMethod(), type)) {
      problems.add(name);
    } else if (AuthorizationRules.isPublic(handler.getMethod(), type)
        && info.getPatternValues().stream().anyMatch(p -> p.contains("/admin"))) {
      problems.add("@PublicEndpoint on admin path: " + name);
    }
  }

  /** Matches requests whose handler is a {@code @PublicEndpoint} method. */
  public static RequestMatcher publicEndpoints(
      ObjectProvider<RequestMappingHandlerMapping> mappings) {
    return request -> {
      for (RequestMappingHandlerMapping mapping : mappings.orderedStream().toList()) {
        try {
          HttpServletRequest probe = new IsolatedAttributes(request);
          ServletRequestPathUtils.parseAndCache(probe);
          HandlerExecutionChain chain = mapping.getHandler(probe);
          if (chain != null
              && chain.getHandler() instanceof HandlerMethod hm
              && AuthorizationRules.isPublic(hm.getMethod(), hm.getBeanType())) {
            return true;
          }
        } catch (Exception e) {
          return false;
        }
      }
      return false;
    };
  }

  /** Lookup without leaking handler-mapping attributes into the real request. */
  private static final class IsolatedAttributes extends HttpServletRequestWrapper {
    private final Map<String, Object> attributes = new HashMap<>();
    private final Set<String> removed = new HashSet<>();

    IsolatedAttributes(HttpServletRequest request) {
      super(request);
    }

    @Override
    public Object getAttribute(String name) {
      if (attributes.containsKey(name)) return attributes.get(name);
      return removed.contains(name) ? null : super.getAttribute(name);
    }

    @Override
    public void setAttribute(String name, Object value) {
      attributes.put(name, value);
      removed.remove(name);
    }

    @Override
    public void removeAttribute(String name) {
      attributes.remove(name);
      removed.add(name);
    }

    @Override
    public Enumeration<String> getAttributeNames() {
      Set<String> names = new HashSet<>(Collections.list(super.getAttributeNames()));
      names.removeAll(removed);
      names.addAll(attributes.keySet());
      return Collections.enumeration(names);
    }
  }
}
