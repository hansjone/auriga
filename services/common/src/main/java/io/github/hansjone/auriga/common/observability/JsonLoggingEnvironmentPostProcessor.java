package io.github.hansjone.auriga.common.observability;

import java.util.Map;
import org.springframework.boot.EnvironmentPostProcessor;
import org.springframework.boot.SpringApplication;
import org.springframework.core.env.ConfigurableEnvironment;
import org.springframework.core.env.MapPropertySource;

/**
 * Makes structured JSON console logs the default for every service. Opt out with {@code
 * auriga.logging.json=false} (e.g. local development); an explicit {@code
 * logging.structured.format.console} wins.
 */
public final class JsonLoggingEnvironmentPostProcessor implements EnvironmentPostProcessor {
  static final String FORMAT_PROPERTY = "logging.structured.format.console";

  @Override
  public void postProcessEnvironment(ConfigurableEnvironment environment, SpringApplication app) {
    if (!environment.getProperty("auriga.logging.json", Boolean.class, true)) return;
    if (environment.containsProperty(FORMAT_PROPERTY)) return;
    environment
        .getPropertySources()
        .addLast(
            new MapPropertySource(
                "aurigaJsonLogging", Map.of(FORMAT_PROPERTY, JsonLogFormatter.class.getName())));
  }
}
