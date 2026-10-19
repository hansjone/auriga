package auriga.platform;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

/** Combined deployment entry point. */
@SpringBootApplication(scanBasePackages = "auriga")
public class PlatformApplication {
  public static void main(String[] args) {
    SpringApplication.run(PlatformApplication.class, args);
  }
}
