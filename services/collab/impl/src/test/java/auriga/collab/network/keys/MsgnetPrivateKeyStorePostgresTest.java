package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.PostgresExtension;
import java.util.Base64;
import java.util.Map;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class MsgnetPrivateKeyStorePostgresTest {
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  final UUID tenant = UUID.randomUUID();
  final byte[] oldKek = MsgnetKeyEncryptionTest.kek();
  final byte[] newKek = MsgnetKeyEncryptionTest.kek();

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            MsgnetPrivateKeyStorePostgresTest.class, "msgnet_owner", "msgnet_app", "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    manager = new TenantTransactionManager(roles.runtime().dataSource());
  }

  ObjectNode aad() {
    return MsgnetKeyEncryptionTest.aad().put("tenant_id", tenant.toString());
  }

  MsgnetPrivateKeyStore store(MsgnetKeyEncryption encryption) {
    return new MsgnetPrivateKeyStore(jdbc, manager, tenant, "2", encryption);
  }

  String encrypted() {
    return TenantContext.call(
        tenant,
        () ->
            new TransactionTemplate(manager)
                .execute(
                    s ->
                        jdbc.queryForObject(
                            "SELECT encrypted_key FROM collab.msgnet_private_key WHERE tenant_id = ?",
                            String.class,
                            tenant)));
  }

  @Test
  void idempotentRetryRejectsDifferentMaterialAndRewrapSurvivesRestart() {
    var pair = KeyTestMaterial.pair();
    String spki =
        Base64.getUrlEncoder().withoutPadding().encodeToString(pair.getPublic().getEncoded());
    byte[] secret = pair.getPrivate().getEncoded();
    try (var old = new MsgnetKeyEncryption("old", Map.of("old", oldKek));
        var rolling = new MsgnetKeyEncryption("new", Map.of("old", oldKek, "new", newKek));
        var completed = new MsgnetKeyEncryption("new", Map.of("new", newKek))) {
      store(old).install(aad(), spki, secret);
      String original = encrypted();
      store(old).install(aad(), spki, secret);
      assertEquals(original, encrypted());
      assertArrayEquals(secret, store(old).read(aad()));
      var other = KeyTestMaterial.pair();
      String otherSpki =
          Base64.getUrlEncoder().withoutPadding().encodeToString(other.getPublic().getEncoded());
      assertThrows(
          IllegalArgumentException.class,
          () -> store(old).install(aad(), otherSpki, other.getPrivate().getEncoded()));
      assertThrows(
          IllegalArgumentException.class,
          () -> store(old).install(aad(), spki, other.getPrivate().getEncoded()));
      assertThrows(
          IllegalArgumentException.class,
          () -> store(old).read(aad().put("purpose", "business_delivery")));
      store(rolling).rewrap(aad());
      assertArrayEquals(secret, store(completed).read(aad()));
      assertNotEquals(original, encrypted());
      assertThrows(IllegalArgumentException.class, () -> store(old).read(aad()));
    }
  }

  @Test
  void outerTransactionRollbackLeavesNoPartialMaterialAndRlsHidesOtherTenant() {
    var pair = KeyTestMaterial.pair();
    String spki =
        Base64.getUrlEncoder().withoutPadding().encodeToString(pair.getPublic().getEncoded());
    try (var encryption = new MsgnetKeyEncryption("old", Map.of("old", oldKek))) {
      TenantContext.call(
          tenant,
          () ->
              new TransactionTemplate(manager)
                  .execute(
                      status -> {
                        store(encryption).install(aad(), spki, pair.getPrivate().getEncoded());
                        status.setRollbackOnly();
                        return null;
                      }));
      assertThrows(IllegalArgumentException.class, () -> store(encryption).read(aad()));
      store(encryption).install(aad(), spki, pair.getPrivate().getEncoded());
      int visible =
          TenantContext.call(
              UUID.randomUUID(),
              () ->
                  new TransactionTemplate(manager)
                      .execute(
                          status ->
                              jdbc.queryForObject(
                                  "SELECT count(*) FROM collab.msgnet_private_key WHERE tenant_id = ?",
                                  Integer.class,
                                  tenant)));
      assertEquals(0, visible);
      assertThrows(
          IllegalArgumentException.class,
          () -> store(encryption).read(aad().put("tenant_id", UUID.randomUUID().toString())));
      assertThrows(
          RuntimeException.class,
          () ->
              TenantContext.call(
                  UUID.randomUUID(),
                  () ->
                      new TransactionTemplate(manager)
                          .execute(status -> store(encryption).read(aad()))));
    }
  }

  @Test
  void x25519MaterialMustMatchItsPublicKey() throws Exception {
    var generator = java.security.KeyPairGenerator.getInstance("X25519");
    var pair = generator.generateKeyPair();
    var expected = aad().put("alg", "X25519");
    String spki =
        Base64.getUrlEncoder().withoutPadding().encodeToString(pair.getPublic().getEncoded());
    try (var encryption = new MsgnetKeyEncryption("old", Map.of("old", oldKek))) {
      store(encryption).install(expected, spki, pair.getPrivate().getEncoded());
      assertArrayEquals(pair.getPrivate().getEncoded(), store(encryption).read(expected));
      assertThrows(
          IllegalArgumentException.class,
          () ->
              store(encryption)
                  .install(expected, spki, generator.generateKeyPair().getPrivate().getEncoded()));
    }
  }

  @Test
  void concurrentInstallProducesOneStableCiphertext() throws Exception {
    var pair = KeyTestMaterial.pair();
    String spki =
        Base64.getUrlEncoder().withoutPadding().encodeToString(pair.getPublic().getEncoded());
    try (var encryption = new MsgnetKeyEncryption("old", Map.of("old", oldKek));
        var workers = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var start = new java.util.concurrent.CountDownLatch(1);
      var first =
          workers.submit(
              () -> {
                start.await();
                store(encryption).install(aad(), spki, pair.getPrivate().getEncoded());
                return null;
              });
      var second =
          workers.submit(
              () -> {
                start.await();
                store(encryption).install(aad(), spki, pair.getPrivate().getEncoded());
                return null;
              });
      start.countDown();
      first.get(10, java.util.concurrent.TimeUnit.SECONDS);
      second.get(10, java.util.concurrent.TimeUnit.SECONDS);
      assertArrayEquals(pair.getPrivate().getEncoded(), store(encryption).read(aad()));
    }
  }

  @Test
  void wholeRingRotationRecordsCompletionAndFencesAnOldWriter() {
    var pair = KeyTestMaterial.pair();
    String spki =
        Base64.getUrlEncoder().withoutPadding().encodeToString(pair.getPublic().getEncoded());
    try (var old = new MsgnetKeyEncryption("old", Map.of("old", oldKek));
        var rolling = new MsgnetKeyEncryption("new", Map.of("old", oldKek, "new", newKek));
        var completed = new MsgnetKeyEncryption("new", Map.of("new", newKek))) {
      store(old).install(aad(), spki, pair.getPrivate().getEncoded());
      assertThrows(IllegalArgumentException.class, () -> store(completed).checkRing());
      assertTrue(store(rolling).rewrapAll().path("completed").asBoolean());
      store(completed).checkRing();
      assertArrayEquals(pair.getPrivate().getEncoded(), store(completed).read(aad()));
      var other = KeyTestMaterial.pair();
      assertThrows(
          IllegalArgumentException.class,
          () ->
              store(old)
                  .install(
                      aad().put("kid", "stale-writer"),
                      Base64.getUrlEncoder()
                          .withoutPadding()
                          .encodeToString(other.getPublic().getEncoded()),
                      other.getPrivate().getEncoded()));
      assertTrue(store(completed).rewrapAll().path("completed").asBoolean());
    }
  }
}
