package auriga.workspace;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Configuration;

/** Assembly point for the workspace service. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.workspace",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
@org.springframework.context.annotation.Import(
    auriga.workspace.config.WorkspaceRuntimeConfiguration.class)
public class WorkspaceModuleConfiguration {}
