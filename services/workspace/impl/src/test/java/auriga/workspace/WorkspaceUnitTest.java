package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.common.persistence.UuidV7;
import auriga.workspace.domain.*;
import java.nio.charset.StandardCharsets;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class WorkspaceUnitTest {
  final Instant now = Instant.parse("2026-10-20T00:00:00Z");

  @Test
  void interactionTimeIsMonotonicAndCannotExtendIntoTheFuture() {
    Instant previous = now.minusSeconds(1800), recent = now.minusSeconds(5);
    assertEquals(recent, Lifecycle.latestActivity(previous, now, false, recent));
    assertEquals(now, Lifecycle.latestActivity(previous, now, false, now.plusSeconds(86400)));
    assertEquals(
        previous, Lifecycle.latestActivity(previous, now, false, previous.minusSeconds(1)));
    assertEquals(previous, Lifecycle.latestActivity(previous, now, false, null));
    assertEquals(now, Lifecycle.latestActivity(previous, now, true, null));
  }

  @Test
  void idleRequiresBothFreshSourcesAndNoActivity() {
    assertTrue(
        Lifecycle.idle(
            now, now.minusSeconds(1800), now, false, now, false, false, Duration.ofMinutes(30)));
    assertFalse(
        Lifecycle.idle(
            now, now.minusSeconds(1800), now, true, now, false, false, Duration.ofMinutes(30)));
    assertFalse(
        Lifecycle.idle(
            now, now.minusSeconds(1800), now, false, now, true, false, Duration.ofMinutes(30)));
    assertFalse(
        Lifecycle.idle(
            now, now.minusSeconds(1800), null, false, now, false, false, Duration.ofMinutes(30)));
    assertFalse(
        Lifecycle.idle(
            now,
            now.minusSeconds(1800),
            now.minusSeconds(91),
            false,
            now,
            false,
            false,
            Duration.ofMinutes(30)));
    assertFalse(
        Lifecycle.idle(
            now,
            now.minusSeconds(1800),
            now,
            false,
            now.minusSeconds(61),
            false,
            false,
            Duration.ofMinutes(30)));
  }

  @Test
  void idleTimeoutDoesNotReclaimOneMicrosecondEarly() {
    Instant activity = now.minusSeconds(1800).plusNanos(123457000);
    Instant boundary = activity.plusSeconds(1800);
    assertFalse(
        Lifecycle.idle(
            boundary.minusNanos(1000),
            activity,
            boundary,
            false,
            boundary,
            false,
            false,
            Duration.ofMinutes(30)));
    assertTrue(
        Lifecycle.idle(
            boundary, activity, boundary, false, boundary, false, false, Duration.ofMinutes(30)));
    // A nanosecond clock can still be before the boundary after the activity was rounded in DB.
    Instant linuxNow = now.plusNanos(123456789);
    assertFalse(
        Lifecycle.idle(
            linuxNow, activity, linuxNow, false, linuxNow, false, false, Duration.ofMinutes(30)));
  }

  @Test
  void alwaysOnAndRecentActivityPreventStop() {
    assertFalse(
        Lifecycle.idle(
            now, now.minusSeconds(1800), now, false, now, false, true, Duration.ofMinutes(30)));
    assertFalse(
        Lifecycle.idle(
            now, now.minusSeconds(1799), now, false, now, false, false, Duration.ofMinutes(30)));
  }

  @Test
  void movingAlarmsHaveAFixedFourMinuteIdleBudget() {
    Instant activity = now.minusSeconds(1800);
    Duration timeout = Duration.ofMinutes(30);
    for (int seconds = 0; seconds < 240; seconds += 30) {
      Instant tick = now.plusSeconds(seconds);
      assertTrue(Lifecycle.alarmSoon(tick, tick.plusSeconds(240), activity, timeout));
    }
    Instant deadline = now.plusSeconds(240);
    assertFalse(Lifecycle.alarmSoon(deadline, deadline.plusSeconds(1), activity, timeout));
    assertFalse(
        Lifecycle.alarmSoon(
            deadline.plusSeconds(30), deadline.plusSeconds(240), activity, timeout));
    // Real activity starts a new idle period; changing only the alarm does not.
    assertTrue(Lifecycle.alarmSoon(deadline, deadline.plusSeconds(120), now, timeout));
  }

  @Test
  void alarmProtectionRejectsStaleMissingAndDistantSchedules() {
    Instant activity = now.minusSeconds(1800);
    Duration timeout = Duration.ofMinutes(30);
    assertFalse(Lifecycle.alarmSoon(now, null, activity, timeout));
    assertFalse(Lifecycle.alarmSoon(now, now.minusSeconds(1), activity, timeout));
    assertFalse(Lifecycle.alarmSoon(now, now, activity, timeout));
    assertFalse(Lifecycle.alarmSoon(now, now.plusSeconds(241), activity, timeout));
    assertTrue(Lifecycle.alarmSoon(now, now.plusSeconds(240), activity, timeout));
  }

  @Test
  void wakeHasTwoMinuteLead() {
    assertTrue(Lifecycle.wake(now, now.plusSeconds(120)));
    assertFalse(Lifecycle.wake(now, now.plusSeconds(121)));
    assertFalse(Lifecycle.wake(now, null));
  }

  @Test
  void encryptedTokensBindTenantAndInstance() {
    var crypto = new WorkspaceCrypto(WorkspaceCrypto.random(32));
    byte[] token = WorkspaceCrypto.random(32);
    byte[] sealed = crypto.encrypt(token, "tenant/instance");
    assertArrayEquals(token, crypto.decrypt(sealed, "tenant/instance"));
    assertThrows(IllegalStateException.class, () -> crypto.decrypt(sealed, "other/instance"));
    sealed[sealed.length - 1] ^= 1;
    assertThrows(IllegalStateException.class, () -> crypto.decrypt(sealed, "tenant/instance"));
  }

  @Test
  void hpkeMatchesContractInfoAndGeneration() throws Exception {
    var hpke = WorkspaceCrypto.hpke();
    var recipient = hpke.generatePrivateKey();
    byte[] publicKey = hpke.serializePublicKey(recipient.getPublic());
    String id = UuidV7.next().toString();
    byte[] secret = WorkspaceCrypto.random(32);
    var sealed = WorkspaceCrypto.seal(publicKey, id, "instance_token", 7, secret);
    var context =
        hpke.setupBaseR(
            sealed.getEnc().toByteArray(),
            recipient,
            ("auriga/sealed/v1/" + id + "/instance_token").getBytes(StandardCharsets.UTF_8));
    assertArrayEquals(
        secret,
        context.open("7".getBytes(StandardCharsets.UTF_8), sealed.getCiphertext().toByteArray()));
    var wrong =
        hpke.setupBaseR(
            sealed.getEnc().toByteArray(),
            recipient,
            ("auriga/sealed/v1/" + id + "/instance_token").getBytes(StandardCharsets.UTF_8));
    assertThrows(
        Exception.class,
        () ->
            wrong.open("8".getBytes(StandardCharsets.UTF_8), sealed.getCiphertext().toByteArray()));
    assertEquals("hpke-x25519-sha256-aes256gcm", sealed.getAlg());
    assertEquals(16, sealed.getKeyId().length());
  }

  @Test
  void caVerifiesCsrAndControlsSan() throws Exception {
    var key = TestPki.key();
    var root = TestPki.ca(key, now);
    var ca = new NodeCa(root, key.getPrivate(), Clock.fixed(now, ZoneOffset.UTC));
    var node = UuidV7.next();
    var client = TestPki.key();
    var certificate = ca.issue(TestPki.csr(client), node);
    certificate.verify(root.getPublicKey());
    assertArrayEquals(client.getPublic().getEncoded(), certificate.getPublicKey().getEncoded());
    assertEquals(
        "auriga://node/" + node, certificate.getSubjectAlternativeNames().iterator().next().get(1));
    assertEquals(now.plus(Duration.ofDays(30)), certificate.getNotAfter().toInstant());
    ca.verify(certificate);
    assertEquals(
        HexFormat.of().formatHex(WorkspaceCrypto.hash(root.getPublicKey().getEncoded())),
        ca.fingerprint());
    assertNotEquals(
        HexFormat.of().formatHex(WorkspaceCrypto.hash(root.getEncoded())), ca.fingerprint());
  }

  @Test
  void rejectsForgedCsrAndForeignCa() throws Exception {
    var rootKey = TestPki.key();
    var ca =
        new NodeCa(
            TestPki.ca(rootKey, now), rootKey.getPrivate(), Clock.fixed(now, ZoneOffset.UTC));
    byte[] csr = TestPki.csr(TestPki.key());
    csr[csr.length - 1] ^= 1;
    assertEquals(
        "common.validation_failed",
        assertThrows(AurigaException.class, () -> ca.issue(csr, UuidV7.next())).errorCode().code());
    var otherKey = TestPki.key();
    var other =
        new NodeCa(
            TestPki.ca(otherKey, now), otherKey.getPrivate(), Clock.fixed(now, ZoneOffset.UTC));
    var foreign = other.issue(TestPki.csr(TestPki.key()), UuidV7.next());
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> ca.verify(foreign)).errorCode().code());
  }

  @Test
  void expiredNodeCertificateRejected() throws Exception {
    var key = TestPki.key();
    var root = TestPki.ca(key, now);
    var ca = new NodeCa(root, key.getPrivate(), Clock.fixed(now, ZoneOffset.UTC));
    var certificate = ca.issue(TestPki.csr(TestPki.key()), UuidV7.next());
    var later =
        new NodeCa(
            root, key.getPrivate(), Clock.fixed(now.plus(Duration.ofDays(31)), ZoneOffset.UTC));
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> later.verify(certificate)).errorCode().code());
  }
}
