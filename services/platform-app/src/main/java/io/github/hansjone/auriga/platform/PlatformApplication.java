package io.github.hansjone.auriga.platform;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

/** Combined deployment entry point. */
@SpringBootApplication(scanBasePackages = "io.github.hansjone.auriga")
public class PlatformApplication {
  public static void main(String[] args) {
    SpringApplication.run(PlatformApplication.class, args);
  }
}
