package auriga.common.observability;

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
    String level = environment.getProperty("AURIGA_LOG_LEVEL", "INFO");
    if ("DEBUG".equalsIgnoreCase(level)) {
      environment
          .getPropertySources()
          .addLast(
              new MapPropertySource("aurigaLogLevel", Map.of("logging.level.auriga", "DEBUG")));
    }
    if (!environment.getProperty("auriga.logging.json", Boolean.class, true)) return;
    if (environment.containsProperty(FORMAT_PROPERTY)) return;
    environment
        .getPropertySources()
        .addLast(
            new MapPropertySource(
                "aurigaJsonLogging", Map.of(FORMAT_PROPERTY, JsonLogFormatter.class.getName())));
  }
}
