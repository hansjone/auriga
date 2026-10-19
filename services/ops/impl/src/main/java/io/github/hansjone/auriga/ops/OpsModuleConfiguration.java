package io.github.hansjone.auriga.ops;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Configuration;

/** Assembly point for the ops service. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.ops",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
public class OpsModuleConfiguration {}
