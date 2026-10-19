package auriga.platform;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.ComponentScan;
import org.springframework.context.annotation.FilterType;

/**
 * Combined deployment entry point. Autoconfigure classes under {@code auriga.common.autoconfigure}
 * must load only via {@code AutoConfiguration.imports} (so {@code spring.autoconfigure.exclude}
 * works); scanning the whole {@code auriga} tree would register them twice and ignore excludes.
 */
@SpringBootApplication
@ComponentScan(
    basePackages = "auriga",
    excludeFilters =
        @ComponentScan.Filter(
            type = FilterType.REGEX,
            pattern = "auriga\\.common\\.autoconfigure\\..*"))
public class PlatformApplication {
  public static void main(String[] args) {
    SpringApplication.run(PlatformApplication.class, args);
  }
}
