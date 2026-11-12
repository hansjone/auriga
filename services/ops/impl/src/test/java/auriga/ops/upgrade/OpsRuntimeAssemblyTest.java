package auriga.ops.upgrade;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.support.DefaultListableBeanFactory;
import org.springframework.context.annotation.ClassPathBeanDefinitionScanner;
import org.springframework.core.type.filter.AssignableTypeFilter;
import org.springframework.mock.env.MockEnvironment;

class OpsRuntimeAssemblyTest {
  @Test
  void combinedScanRegistersAlertsAndBackupConfigurationsTogether() {
    var beans = new DefaultListableBeanFactory();
    var scanner = new ClassPathBeanDefinitionScanner(beans, false);
    scanner.setEnvironment(
        new MockEnvironment()
            .withProperty("auriga.platform.combined", "true")
            .withProperty("auriga.services.ops.enabled", "true")
            .withProperty("auriga.ops.runtime.enabled", "true"));
    scanner.addIncludeFilter(new AssignableTypeFilter(auriga.ops.OpsRuntimeConfiguration.class));
    scanner.addIncludeFilter(new AssignableTypeFilter(OpsRuntimeConfiguration.class));
    scanner.scan("auriga.ops");

    assertEquals(
        auriga.ops.OpsRuntimeConfiguration.class.getName(),
        beans.getBeanDefinition("opsRuntimeConfiguration").getBeanClassName());
    assertEquals(
        OpsRuntimeConfiguration.class.getName(),
        beans.getBeanDefinition("opsUpgradeRuntimeConfiguration").getBeanClassName());
  }
}
