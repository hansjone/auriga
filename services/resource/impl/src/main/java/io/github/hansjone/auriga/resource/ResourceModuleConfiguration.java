package io.github.hansjone.auriga.resource;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Configuration;

/** Assembly point for the resource service. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.resource",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
public class ResourceModuleConfiguration {}
