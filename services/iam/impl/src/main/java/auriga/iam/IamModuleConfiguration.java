package auriga.iam;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Configuration;

/** Assembly point for the iam service. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.iam",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
public class IamModuleConfiguration {}
