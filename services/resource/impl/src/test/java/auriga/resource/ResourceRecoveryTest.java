package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.*;
import auriga.resource.app.ResourceGarbageCollector;
import auriga.resource.objectstore.*;
import auriga.resource.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.nio.file.*;
import java.time.*;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class ResourceRecoveryTest {
  @TempDir Path root;

  @Test
  void upgradesDuplicateSignedListsAndCollectsOnlyOldUnreferencedObjects() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(getClass(), "resource_owner", "resource_app", "resource");
    CommonMigrations.migrate(roles.owner().dataSource(), "resource");
    var config =
        Flyway.configure()
            .dataSource(roles.owner().dataSource())
            .schemas("resource")
            .defaultSchema("resource")
            .locations("classpath:db/resource");
    CommonMigrations.serviceFlyway(config.target("3").load()).migrate();
    var admin = new JdbcTemplate(PostgresExtension.database(getClass()).dataSource());
    UUID tenant = UUID.randomUUID(),
        pkg = UUID.randomUUID(),
        version = UUID.randomUUID(),
        instance = UUID.randomUUID();
    String item =
        "{\"packageId\":\""
            + pkg
            + "\",\"versionId\":\""
            + version
            + "\",\"digest\":\"sha256:"
            + "a".repeat(64)
            + "\"}";
    admin.update(
        "INSERT INTO resource.resource_revocations(id,tenant_id,revision,document) VALUES(?,?,7,CAST(? AS jsonb))",
        UUID.randomUUID(),
        tenant,
        "{\"revision\":7,\"revokedArtifacts\":[" + item + "," + item + "],\"revokedKids\":[]}");
    admin.update(
        "INSERT INTO resource.resource_distribution(id,tenant_id,actor_id,instance_id,owner_id,selection_revision,selections,state,created_at) VALUES(?,?,?,?,?,1,'[]','applied',now())",
        UUID.randomUUID(),
        tenant,
        UUID.randomUUID(),
        instance,
        UUID.randomUUID());
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("resource")
                .defaultSchema("resource")
                .locations("classpath:db/resource")
                .load())
        .migrate();
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT jsonb_array_length(document->'revokedArtifacts') FROM resource.resource_revocations",
            Integer.class));
    assertEquals(
        true,
        admin.queryForObject(
            "SELECT needs_resign FROM resource.resource_revocations", Boolean.class));
    assertEquals(
        1,
        admin.queryForObject(
            "SELECT count(*) FROM resource.resource_revoked_artifact", Integer.class));
    assertEquals(
        7L,
        admin.queryForObject(
            "SELECT revision FROM resource.resource_revocation_delivery WHERE completed_revision=0",
            Long.class));
    var jdbc = new JdbcTemplate(roles.runtime().dataSource());
    var resources =
        new ResourceStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
    var db = new ResourceWorkflowStore(jdbc);
    var generator = java.security.KeyPairGenerator.getInstance("Ed25519");
    var release = generator.generateKeyPair();
    var revocation = generator.generateKeyPair();
    var signing =
        new auriga.resource.app.ResourceSigning(
            "release", release.getPrivate(), "revoke", revocation.getPrivate());
    var distributions =
        new auriga.resource.app.ResourceDistributions(
            resources,
            db,
            org.mockito.Mockito.mock(auriga.resource.app.ResourceAccess.class),
            org.mockito.Mockito.mock(auriga.resource.app.ResourceWorkflow.class),
            () -> null,
            () -> signing,
            () -> null,
            () -> "",
            new auriga.resource.app.ResourceCursor(new byte[32], Clock.systemUTC()),
            org.mockito.Mockito.mock(auriga.common.events.EventPublisher.class),
            Clock.systemUTC());
    var repaired = TenantContext.call(tenant, () -> distributions.revocationDocument(null));
    assertEquals(8, repaired.path("revision").asLong());
    var unsigned = (tools.jackson.databind.node.ObjectNode) repaired.deepCopy();
    unsigned.remove("signature");
    var verifier = java.security.Signature.getInstance("Ed25519");
    verifier.initVerify(revocation.getPublic());
    verifier.update(
        auriga.resource.app.ResourceSigning.canonical(unsigned)
            .getBytes(java.nio.charset.StandardCharsets.UTF_8));
    assertTrue(
        verifier.verify(
            java.util.Base64.getUrlDecoder()
                .decode(repaired.path("signature").path("value").asString())));
    assertEquals(
        false,
        admin.queryForObject(
            "SELECT needs_resign FROM resource.resource_revocations", Boolean.class));
    try (var objects = new LocalObjectStore(root)) {
      var referenced =
          objects.putStaged(
              tenant, new java.io.ByteArrayInputStream(new byte[] {1}), ObjectStore.MAX_BYTES);
      var orphan =
          objects.putStaged(
              tenant, new java.io.ByteArrayInputStream(new byte[] {2}), ObjectStore.MAX_BYTES);
      objects.commitIfAbsent(tenant, referenced.id(), referenced.object());
      objects.commitIfAbsent(tenant, orphan.id(), orphan.object());
      admin.update(
          "INSERT INTO resource.resource_package(id,tenant_id,owner_id,name,level,visibility,enabled,revision,created_at) VALUES(?,?,?,'example','l3','tenant',false,1,now())",
          pkg,
          tenant,
          UUID.randomUUID());
      admin.update(
          "INSERT INTO resource.resource_version(id,tenant_id,package_id,author_id,version,digest,size_bytes,state,revision,created_at) VALUES(?,?,?,?,'1.0.0',?,1,'draft',1,now())",
          version,
          tenant,
          pkg,
          UUID.randomUUID(),
          referenced.object().digest());
      try (var paths = Files.walk(root)) {
        for (Path path :
            paths.filter(p -> p.getFileName().toString().matches("[0-9a-f]{64}")).toList())
          Files.setLastModifiedTime(
              path, java.nio.file.attribute.FileTime.from(Instant.now().minusSeconds(86401)));
      }
      var collector = new ResourceGarbageCollector(resources, db, () -> objects, Clock.systemUTC());
      collector.collect();
      assertEquals(referenced.object(), objects.stat(tenant, referenced.object().digest()));
      assertThrows(
          auriga.common.error.AurigaException.class,
          () -> objects.stat(tenant, orphan.object().digest()));
      assertEquals(1, objects.expiredObjects(Instant.now()).size());
      assertEquals(
          1,
          TenantContext.call(
              tenant,
              () ->
                  resources.read(
                      () ->
                          db.rows(
                                  "SELECT * FROM resource.resource_version WHERE tenant_id=:tenant",
                                  db.args())
                              .size())));
    }
  }
}
