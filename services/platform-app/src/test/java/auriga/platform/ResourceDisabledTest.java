package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.resource.ResourceModuleConfiguration;
import auriga.resource.config.ResourceRuntimeConfiguration;
import auriga.resource.web.ResourceController;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.support.DefaultListableBeanFactory;
import org.springframework.context.annotation.AnnotatedBeanDefinitionReader;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.context.annotation.ConfigurationClassPostProcessor;
import org.springframework.core.env.MapPropertySource;
import org.springframework.mock.env.MockEnvironment;

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

  @Test
  void resourceForwarderRequiresCombinedResourceAndNats() {
    assertFalse(hasResourceForwarder(false, true, "nats://127.0.0.1:1"));
    assertFalse(hasResourceForwarder(true, false, "nats://127.0.0.1:1"));
    assertFalse(hasResourceForwarder(true, true, ""));
    assertTrue(hasResourceForwarder(true, true, "nats://127.0.0.1:1"));
  }

  private boolean hasResourceForwarder(boolean combined, boolean resource, String natsUrl) {
    var environment =
        new MockEnvironment()
            .withProperty("auriga.platform.combined", Boolean.toString(combined))
            .withProperty("auriga.services.resource.enabled", Boolean.toString(resource))
            .withProperty("auriga.events.nats-url", natsUrl);
    var registry = new DefaultListableBeanFactory();
    new AnnotatedBeanDefinitionReader(registry, environment)
        .register(PlatformResourceConfiguration.class);
    var processor = new ConfigurationClassPostProcessor();
    processor.setEnvironment(environment);
    processor.postProcessBeanDefinitionRegistry(registry);
    return registry.containsBeanDefinition("resourceOutboxForwarder");
  }
}
