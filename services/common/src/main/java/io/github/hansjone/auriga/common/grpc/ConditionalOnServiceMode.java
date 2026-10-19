package io.github.hansjone.auriga.common.grpc;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;
import java.util.Locale;
import org.springframework.context.annotation.Condition;
import org.springframework.context.annotation.ConditionContext;
import org.springframework.context.annotation.Conditional;
import org.springframework.core.type.AnnotatedTypeMetadata;

/**
 * Selects how another service's {@code api} interface is provided (ADR 0003):
 *
 * <pre>
 * auriga.remote.org.mode=local   # default: the org impl module is in this JVM, use its bean
 * auriga.remote.org.mode=grpc    # split deployment: gRPC client adapter
 * auriga.remote.org.target=org.internal.example.com:9090
 * </pre>
 *
 * Put {@code @ConditionalOnServiceMode(service = "org", mode = GRPC)} on the remote adapter bean;
 * the local implementation is registered by the org module itself.
 */
@Target({ElementType.TYPE, ElementType.METHOD})
@Retention(RetentionPolicy.RUNTIME)
@Documented
@Conditional(ConditionalOnServiceMode.OnServiceMode.class)
public @interface ConditionalOnServiceMode {
  String service();

  Mode mode();

  enum Mode {
    LOCAL,
    GRPC
  }

  final class OnServiceMode implements Condition {
    static String property(String service) {
      return "auriga.remote." + service + ".mode";
    }

    @Override
    public boolean matches(ConditionContext context, AnnotatedTypeMetadata metadata) {
      var attrs = metadata.getAnnotationAttributes(ConditionalOnServiceMode.class.getName());
      if (attrs == null) return false;
      String service = (String) attrs.get("service");
      Mode wanted = (Mode) attrs.get("mode");
      String configured =
          context.getEnvironment().getProperty(property(service), Mode.LOCAL.name());
      Mode actual;
      try {
        actual = Mode.valueOf(configured.trim().toUpperCase(Locale.ROOT));
      } catch (IllegalArgumentException e) {
        throw new IllegalStateException(
            property(service) + " must be local or grpc, was " + configured);
      }
      return actual == wanted;
    }
  }
}
