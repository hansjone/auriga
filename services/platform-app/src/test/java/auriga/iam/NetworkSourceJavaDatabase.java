package auriga.iam;

import auriga.common.persistence.*;
import com.zaxxer.hikari.*;
import io.zonky.test.db.postgres.embedded.EmbeddedPostgres;
import java.nio.file.*;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.TimeUnit;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.springframework.jdbc.core.JdbcTemplate;

/** Owns this host's native PG, independent of the JUnit-global PostgresExtension. */
final class NetworkSourceJavaDatabase implements AutoCloseable {
  EmbeddedPostgres pg;
  final Map<String, HikariDataSource> runtime = new LinkedHashMap<>();
  final Path data;
  ProcessHandle root;
  Instant created;
  final Map<ProcessHandle, Instant> owned = new LinkedHashMap<>();

  NetworkSourceJavaDatabase(Path directory) {
    data = directory.resolve("pg");
  }

  void start() throws Exception {
    pg = EmbeddedPostgres.builder().setPort(0).setDataDirectory(data).start();
    long pid = Long.parseLong(Files.readAllLines(data.resolve("postmaster.pid")).getFirst());
    root = ProcessHandle.of(pid).orElseThrow();
    created = root.info().startInstant().orElseThrow();
    snapshot();
    var admin = new JdbcTemplate(pg.getPostgresDatabase());
    admin.execute("CREATE DATABASE bridge");
    var database = new JdbcTemplate(pg.getDatabase("postgres", "bridge"));
    for (var schema : List.of("iam", "org", "collab")) {
      var owner = "bridge_" + schema + "_owner";
      var app = "bridge_" + schema + "_app";
      database.execute(
          "CREATE ROLE " + owner + " LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE");
      database.execute(
          "CREATE ROLE " + app + " LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE");
      database.execute("CREATE SCHEMA " + schema + " AUTHORIZATION " + owner);
      database.execute("GRANT USAGE ON SCHEMA " + schema + " TO " + app);
      database.execute(
          "ALTER DEFAULT PRIVILEGES FOR ROLE "
              + owner
              + " IN SCHEMA "
              + schema
              + " GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO "
              + app);
      database.execute(
          "ALTER DEFAULT PRIVILEGES FOR ROLE "
              + owner
              + " IN SCHEMA "
              + schema
              + " GRANT USAGE,SELECT ON SEQUENCES TO "
              + app);
      database.execute("ALTER ROLE " + owner + " SET search_path=" + schema);
      database.execute("ALTER ROLE " + app + " SET search_path=" + schema);
      DataSource migration = pg.getDatabase(owner, "bridge");
      CommonMigrations.migrate(migration, schema);
      CommonMigrations.serviceFlyway(
              Flyway.configure()
                  .dataSource(migration)
                  .schemas(schema)
                  .defaultSchema(schema)
                  .locations("classpath:db/" + schema)
                  .load())
          .migrate();
      var config = new HikariConfig();
      config.setDataSource(pg.getDatabase(app, "bridge"));
      config.setPoolName("bridge-" + schema);
      config.setMaximumPoolSize(4);
      config.setMinimumIdle(0);
      runtime.put(schema, new HikariDataSource(config));
    }
  }

  private void snapshot() {
    if (root == null) return;
    owned.put(root, created);
    root.descendants().forEach(p -> p.info().startInstant().ifPresent(t -> owned.put(p, t)));
  }

  List<Map<String, Object>> lineage() {
    snapshot();
    return owned.entrySet().stream()
        .map(
            e ->
                Map.<String, Object>of(
                    "pid",
                    e.getKey().pid(),
                    "parentPid",
                    e.getKey().parent().map(ProcessHandle::pid).orElse(0L),
                    "created",
                    e.getValue().toString()))
        .toList();
  }

  void assertIsolation() {
    for (var entry : runtime.entrySet()) {
      var jdbc = new JdbcTemplate(entry.getValue());
      if (!Boolean.TRUE.equals(
          jdbc.queryForObject(
              "SELECT NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND NOT rolcreaterole FROM pg_roles WHERE rolname=current_user",
              Boolean.class))) throw new AssertionError("Runtime role is privileged");
      if (Boolean.TRUE.equals(
          jdbc.queryForObject(
              "SELECT pg_has_role(current_user,?, 'MEMBER') OR has_schema_privilege(current_user,?,'CREATE')",
              Boolean.class,
              "bridge_" + entry.getKey() + "_owner",
              entry.getKey()))) throw new AssertionError("Runtime has migration authority");
      var tables =
          switch (entry.getKey()) {
            case "iam" ->
                List.of(
                    "account",
                    "session",
                    "device",
                    "network_ingress",
                    "network_connection_anchor",
                    "network_source",
                    "network_source_operation",
                    "network_source_chain");
            case "org" -> List.of("tenant", "member", "role", "role_binding");
            default ->
                List.of(
                    "network_node",
                    "network_local_registration",
                    "network_source_operation",
                    "network_source_binding",
                    "network_key_publication");
          };
      for (var table : tables) {
        if (!Boolean.TRUE.equals(
            jdbc.queryForObject(
                "SELECT relrowsecurity AND relforcerowsecurity AND relowner<>(SELECT oid FROM pg_roles WHERE rolname=current_user) FROM pg_class WHERE oid=?::regclass",
                Boolean.class,
                entry.getKey() + "." + table)))
          throw new AssertionError("Business table lacks runtime FORCE RLS: " + table);
      }
    }
  }

  @Override
  public void close() throws Exception {
    snapshot();
    runtime.values().forEach(HikariDataSource::close);
    if (pg != null) {
      pg.close();
      pg = null;
    }
    for (var entry : owned.entrySet()) {
      var process = entry.getKey();
      if (process.isAlive()
          && process.info().startInstant().filter(entry.getValue()::equals).isPresent()) {
        process.onExit().get(5, TimeUnit.SECONDS);
        if (process.isAlive())
          throw new IllegalStateException("Own PostgreSQL lineage survived close");
      }
    }
  }
}
