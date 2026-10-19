package io.github.hansjone.auriga.org;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Configuration;

/** Assembly point for the org service. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.org",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
public class OrgModuleConfiguration {}
