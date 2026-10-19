package io.github.hansjone.auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import io.github.hansjone.auriga.testsupport.PostgresExtension;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.ApplicationContext;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = "auriga.services.ops.enabled=false")
@ExtendWith(PostgresExtension.class)
@DirtiesContext
class PlatformApplicationTest {
  @Value("${local.server.port}")
  private int port;

  @Autowired private DataSource dataSource;
  @Autowired private ApplicationContext context;

  @DynamicPropertySource
  static void postgres(DynamicPropertyRegistry registry) {
    PostgresExtension.properties(PlatformApplicationTest.class, registry);
  }

  @Test
  void startsWithPostgresAndServesHealth() throws Exception {
    try (var connection = dataSource.getConnection();
        var query = connection.createStatement();
        var rows = query.executeQuery("SELECT current_database()")) {
      assertTrue(rows.next());
      assertEquals(PostgresExtension.database(getClass()).name(), rows.getString(1));
    }
    try (var client = HttpClient.newHttpClient()) {
      var response =
          client.send(
              HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/actuator/health"))
                  .GET()
                  .build(),
              HttpResponse.BodyHandlers.ofString());
      assertEquals(200, response.statusCode());
      assertTrue(response.body().contains("\"status\":\"UP\""), response.body());
    }
  }

  @Test
  void assemblesEnabledModulesOnly() {
    assertTrue(context.containsBean("iamModuleConfiguration"));
    assertTrue(context.containsBean("orgModuleConfiguration"));
    assertFalse(context.containsBean("opsModuleConfiguration"));
  }
}
