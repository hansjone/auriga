package io.github.hansjone.auriga.testsupport;

import io.zonky.test.db.postgres.embedded.EmbeddedPostgres;
import java.io.IOException;
import java.io.UncheckedIOException;
import java.sql.SQLException;
import java.util.HashMap;
import java.util.Map;
import java.util.UUID;
import javax.sql.DataSource;
import org.junit.jupiter.api.extension.BeforeAllCallback;
import org.junit.jupiter.api.extension.ExtensionContext;
import org.junit.jupiter.api.extension.ParameterContext;
import org.junit.jupiter.api.extension.ParameterResolver;
import org.springframework.test.context.DynamicPropertyRegistry;

/** One native PostgreSQL process per JUnit JVM, one isolated database per test class. */
public final class PostgresExtension implements BeforeAllCallback, ParameterResolver {
  private static final ExtensionContext.Namespace NAMESPACE =
      ExtensionContext.Namespace.create(PostgresExtension.class);
  private static Server server;

  @Override
  public void beforeAll(ExtensionContext context) {
    context.getRoot().getStore(NAMESPACE).getOrComputeIfAbsent(Server.class, key -> sharedServer());
    database(context.getRequiredTestClass());
  }

  private static synchronized Server sharedServer() {
    if (server == null) {
      try {
        server = new Server(EmbeddedPostgres.builder().setPort(0).start());
      } catch (IOException e) {
        throw new UncheckedIOException("Cannot start embedded PostgreSQL", e);
      }
    }
    return server;
  }

  /** Usable from @DynamicPropertySource; always pass the concrete test class. */
  public static void properties(Class<?> testClass, DynamicPropertyRegistry registry) {
    Database database = database(testClass);
    registry.add("spring.datasource.url", database::jdbcUrl);
    registry.add("spring.datasource.username", () -> "postgres");
    registry.add("spring.datasource.password", () -> "");
  }

  public static Database database(Class<?> testClass) {
    return sharedServer().database(testClass);
  }

  @Override
  public boolean supportsParameter(ParameterContext parameter, ExtensionContext context) {
    return parameter.getParameter().getType() == Database.class
        || parameter.getParameter().getType() == DataSource.class;
  }

  @Override
  public Object resolveParameter(ParameterContext parameter, ExtensionContext context) {
    Database database = database(context.getRequiredTestClass());
    return parameter.getParameter().getType() == DataSource.class
        ? database.dataSource()
        : database;
  }

  public record Database(String name, String jdbcUrl, DataSource dataSource) {}

  private static final class Server implements ExtensionContext.Store.CloseableResource {
    private final EmbeddedPostgres postgres;
    private final Map<Class<?>, Database> databases = new HashMap<>();

    private Server(EmbeddedPostgres postgres) {
      this.postgres = postgres;
    }

    private synchronized Database database(Class<?> testClass) {
      return databases.computeIfAbsent(
          testClass,
          key -> {
            String name = "test_" + UUID.randomUUID().toString().replace("-", "");
            try (var connection = postgres.getPostgresDatabase().getConnection();
                var statement = connection.createStatement()) {
              statement.execute("CREATE DATABASE " + name);
            } catch (SQLException e) {
              throw new IllegalStateException("Cannot create isolated test database", e);
            }
            return new Database(
                name,
                postgres.getJdbcUrl("postgres", name),
                postgres.getDatabase("postgres", name));
          });
    }

    @Override
    public void close() throws IOException {
      try {
        postgres.close();
      } finally {
        synchronized (PostgresExtension.class) {
          server = null;
        }
      }
    }
  }
}
