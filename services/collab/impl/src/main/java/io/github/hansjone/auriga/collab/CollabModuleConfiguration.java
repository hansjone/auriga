package io.github.hansjone.auriga.collab;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Configuration;

/** Assembly point for the collab service. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.collab",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
public class CollabModuleConfiguration {}
