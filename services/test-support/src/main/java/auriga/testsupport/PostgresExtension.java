package auriga.testsupport;

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

  /**
   * Creates (idempotently) a login role that is neither superuser nor BYPASSRLS, owning {@code
   * schema} in the test class's database, with {@code search_path} set to that schema: the shape of
   * a production service role. Needed to test row level security, which superusers bypass.
   */
  public static ServiceRole serviceRole(Class<?> testClass, String role, String schema) {
    if (!role.matches("[a-z][a-z0-9_]*") || !schema.matches("[a-z][a-z0-9_]*")) {
      throw new IllegalArgumentException("role and schema must be plain identifiers");
    }
    Database database = database(testClass);
    try (var connection = database.dataSource().getConnection();
        var statement = connection.createStatement()) {
      statement.execute(
          "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '"
              + role
              + "') THEN CREATE ROLE "
              + role
              + " LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE; END IF; END $$");
      statement.execute("CREATE SCHEMA IF NOT EXISTS " + schema + " AUTHORIZATION " + role);
      statement.execute("ALTER ROLE " + role + " SET search_path = " + schema);
    } catch (SQLException e) {
      throw new IllegalStateException("Cannot create service role " + role, e);
    }
    String url = database.jdbcUrl().replace("user=postgres", "user=" + role);
    if (url.equals(database.jdbcUrl())) {
      url = url + (url.contains("?") ? "&" : "?") + "user=" + role;
    }
    var dataSource = new org.postgresql.ds.PGSimpleDataSource();
    dataSource.setURL(url);
    return new ServiceRole(role, schema, url, dataSource);
  }

  /** Spring datasource properties connecting as a {@link #serviceRole}. */
  public static void serviceRoleProperties(
      Class<?> testClass, String role, String schema, DynamicPropertyRegistry registry) {
    ServiceRole serviceRole = serviceRole(testClass, role, schema);
    registry.add("spring.datasource.url", serviceRole::jdbcUrl);
    registry.add("spring.datasource.username", () -> role);
    registry.add("spring.datasource.password", () -> "");
  }

  public record ServiceRole(String role, String schema, String jdbcUrl, DataSource dataSource) {}

  /**
   * Production shape with separate roles: {@code owner} owns the schema and runs migrations; {@code
   * runtime} (what the service connects as) may only use the schema and read/write rows
   * (SELECT/INSERT/UPDATE/DELETE on the owner's tables, sequences), no DDL, never table owner.
   */
  public record ServiceRoles(ServiceRole owner, ServiceRole runtime) {}

  public static ServiceRoles serviceRoles(
      Class<?> testClass, String owner, String runtime, String schema) {
    ServiceRole ownerRole = serviceRole(testClass, owner, schema);
    if (!runtime.matches("[a-z][a-z0-9_]*")) {
      throw new IllegalArgumentException("runtime role must be a plain identifier");
    }
    Database database = database(testClass);
    try (var connection = database.dataSource().getConnection();
        var statement = connection.createStatement()) {
      statement.execute(
          "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '"
              + runtime
              + "') THEN CREATE ROLE "
              + runtime
              + " LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE; END IF; END $$");
      statement.execute("GRANT USAGE ON SCHEMA " + schema + " TO " + runtime);
      statement.execute(
          "ALTER DEFAULT PRIVILEGES FOR ROLE "
              + owner
              + " IN SCHEMA "
              + schema
              + " GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "
              + runtime);
      statement.execute(
          "ALTER DEFAULT PRIVILEGES FOR ROLE "
              + owner
              + " IN SCHEMA "
              + schema
              + " GRANT USAGE, SELECT ON SEQUENCES TO "
              + runtime);
      statement.execute("ALTER ROLE " + runtime + " SET search_path = " + schema);
    } catch (SQLException e) {
      throw new IllegalStateException("Cannot create runtime role " + runtime, e);
    }
    String url = database.jdbcUrl().replace("user=postgres", "user=" + runtime);
    if (url.equals(database.jdbcUrl())) {
      url = url + (url.contains("?") ? "&" : "?") + "user=" + runtime;
    }
    var dataSource = new org.postgresql.ds.PGSimpleDataSource();
    dataSource.setURL(url);
    return new ServiceRoles(ownerRole, new ServiceRole(runtime, schema, url, dataSource));
  }

  /** Datasource as the runtime role, Flyway as the owner role. */
  public static void serviceRolesProperties(
      Class<?> testClass,
      String owner,
      String runtime,
      String schema,
      DynamicPropertyRegistry registry) {
    ServiceRoles roles = serviceRoles(testClass, owner, runtime, schema);
    registry.add("spring.datasource.url", roles.runtime()::jdbcUrl);
    registry.add("spring.datasource.username", () -> runtime);
    registry.add("spring.datasource.password", () -> "");
    registry.add("spring.flyway.url", roles.owner()::jdbcUrl);
    registry.add("spring.flyway.user", () -> owner);
    registry.add("spring.flyway.password", () -> "");
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
