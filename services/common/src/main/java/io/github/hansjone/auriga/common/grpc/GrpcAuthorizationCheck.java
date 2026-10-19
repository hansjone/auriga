package io.github.hansjone.auriga.common.grpc;

import io.github.hansjone.auriga.common.security.AuthenticatedOnly;
import io.github.hansjone.auriga.common.security.AuthorizationRules;
import io.github.hansjone.auriga.common.security.RequiresPermission;
import io.grpc.BindableService;
import io.grpc.MethodDescriptor;
import io.grpc.ServerMethodDefinition;
import java.lang.reflect.Method;
import java.lang.reflect.Modifier;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import org.springframework.aop.support.AopUtils;

/**
 * Default deny for gRPC: every implemented RPC method must declare {@link RequiresPermission} or
 * {@link AuthenticatedOnly} (on the method or the class). Methods listed as public (health checks)
 * are exempt. Fails fast at server start.
 */
public final class GrpcAuthorizationCheck {
  private GrpcAuthorizationCheck() {}

  public static void verify(List<BindableService> services, Set<String> publicMethods) {
    List<String> missing = new ArrayList<>();
    for (BindableService service : services) {
      Class<?> type = AopUtils.getTargetClass(service);
      for (ServerMethodDefinition<?, ?> definition : service.bindService().getMethods()) {
        MethodDescriptor<?, ?> descriptor = definition.getMethodDescriptor();
        if (publicMethods.contains(descriptor.getFullMethodName())) continue;
        String bare = descriptor.getBareMethodName();
        if (bare == null) continue;
        String javaName = bare.substring(0, 1).toLowerCase(Locale.ROOT) + bare.substring(1);
        Method impl = implementation(type, javaName);
        if (impl == null) continue; // not implemented: the stub answers UNIMPLEMENTED
        if (!declared(impl, type)) missing.add(descriptor.getFullMethodName());
      }
    }
    if (!missing.isEmpty()) {
      throw new IllegalStateException(
          "gRPC methods without @RequiresPermission, @AuthenticatedOnly, @AdminOnly or"
              + " @RequiresRecentStepUp: "
              + missing);
    }
  }

  private static Method implementation(Class<?> type, String name) {
    for (Class<?> c = type; c != null && c != Object.class; c = c.getSuperclass()) {
      // generated *ImplBase classes are abstract and only inherit the default stubs
      if (Modifier.isAbstract(c.getModifiers()) && c.getName().contains("Grpc$")) return null;
      for (Method m : c.getDeclaredMethods()) {
        if (m.getName().equals(name) && Modifier.isPublic(m.getModifiers()) && !m.isBridge()) {
          return m;
        }
      }
    }
    return null;
  }

  private static boolean declared(Method method, Class<?> type) {
    for (var element : List.of(method, (java.lang.reflect.AnnotatedElement) type)) {
      if (AuthorizationRules.declares(element)) return true;
    }
    return false;
  }
}
