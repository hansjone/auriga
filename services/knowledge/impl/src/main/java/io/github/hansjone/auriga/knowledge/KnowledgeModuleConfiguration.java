package io.github.hansjone.auriga.knowledge;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Configuration;

/** Assembly point for the knowledge service. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.knowledge",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
public class KnowledgeModuleConfiguration {}
