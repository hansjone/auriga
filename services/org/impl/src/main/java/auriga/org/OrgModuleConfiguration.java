package auriga.org;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.annotation.Import;

/** Assembly point for the org service. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.org",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
@Import(OrgRuntimeConfiguration.class)
public class OrgModuleConfiguration {}
