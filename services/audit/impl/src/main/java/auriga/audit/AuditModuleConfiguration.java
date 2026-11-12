package auriga.audit;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Configuration;

/** Assembly point for the audit service. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.audit",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
@org.springframework.context.annotation.Import(AuditRuntimeConfiguration.class)
public class AuditModuleConfiguration {}
