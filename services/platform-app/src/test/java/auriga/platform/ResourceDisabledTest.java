package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.resource.ResourceModuleConfiguration;
import auriga.resource.config.ResourceRuntimeConfiguration;
import auriga.resource.web.ResourceController;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.core.env.MapPropertySource;

class ResourceDisabledTest {
  @Test
  void disabledResourceDoesNotRegisterEndpointsOrRequireDbCredentials() {
    try (var context = new AnnotationConfigApplicationContext()) {
      context
          .getEnvironment()
          .getPropertySources()
          .addFirst(
              new MapPropertySource(
                  "test",
                  Map.of(
                      "auriga.platform.combined",
                      "true",
                      "auriga.services.resource.enabled",
                      "false")));
      context.register(
          PlatformResourceConfiguration.class,
          ResourceModuleConfiguration.class,
          ResourceRuntimeConfiguration.class,
          ResourceController.class);
      context.refresh();
      assertTrue(context.getBeansOfType(ResourceController.class).isEmpty());
      assertFalse(context.containsBean("resourceDataSource"));
      assertFalse(context.containsBean("combinedResourceInputs"));
    }
  }
}
