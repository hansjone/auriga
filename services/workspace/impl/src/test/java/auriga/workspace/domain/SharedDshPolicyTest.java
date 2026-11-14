package auriga.workspace.domain;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.node.v1.*;
import auriga.workspace.app.SharedDshTickets;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.time.Instant;
import java.util.*;
import org.junit.jupiter.api.Test;

class SharedDshPolicyTest {
  final SharedDshBinding binding =
      SharedDshBinding.newBuilder()
          .setTenantId(UUID.randomUUID().toString())
          .setMemberId(UUID.randomUUID().toString())
          .setPlacementId(UUID.randomUUID().toString())
          .setPhysicalInstanceId(UUID.randomUUID().toString())
          .setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
          .setEpoch(1)
          .setRevision(1)
          .build();

  SharedDshRuntimeIntent intent(String action) {
    return SharedDshPolicy.intent(UUID.randomUUID().toString(), binding, action);
  }

  SharedDshRuntimeReceipt.Builder receipt(SharedDshRuntimeIntent intent) {
    return SharedDshRuntimeReceipt.newBuilder()
        .setSchemaVersion(1)
        .setOperationId(intent.getOperationId())
        .setBinding(binding)
        .setReceiptId(UUID.randomUUID().toString())
        .setAction(intent.getAction())
        .setOutcome("confirmed")
        .setObservedAt(1000);
  }

  @Test
  void stopRequiresExternalExecutionTreeProof() {
    var i = intent("stop-pool");
    var r = receipt(i);
    assertFalse(SharedDshPolicy.confirms(i, r.build(), 1000));
    assertTrue(SharedDshPolicy.confirms(i, r.setExecutionTreeStopped(true).build(), 1000));
    assertFalse(SharedDshPolicy.confirms(i, r.setOutcome("blocked").build(), 1000));
  }

  @Test
  void rejectsLateWrongMemberGenerationActionAndOperation() {
    var i = intent("cooperative-cancel");
    var r = receipt(i);
    assertTrue(SharedDshPolicy.confirms(i, r.build(), 1000));
    for (var wrong :
        List.of(
            binding.toBuilder().setMemberId(UUID.randomUUID().toString()).build(),
            binding.toBuilder().setEpoch(2).build(),
            binding.toBuilder().setRevision(2).build(),
            binding.toBuilder().setTenantId(UUID.randomUUID().toString()).build()))
      assertFalse(SharedDshPolicy.confirms(i, r.clone().setBinding(wrong).build(), 1000));
    assertFalse(
        SharedDshPolicy.confirms(
            i, r.clone().setOperationId(UUID.randomUUID().toString()).build(), 1000));
    assertFalse(SharedDshPolicy.confirms(i, r.clone().setAction("stop-pool").build(), 1000));
    assertFalse(SharedDshPolicy.confirms(i, r.clone().setObservedAt(1001).build(), 1000));
    assertFalse(SharedDshPolicy.confirms(i, r.clone().setSchemaVersion(2).build(), 1000));
  }

  @Test
  void archiveRequiresThirtyDaysAndEmptyDirectory() {
    var i = intent("archive-reset");
    var r = receipt(i);
    assertFalse(SharedDshPolicy.confirms(i, r.build(), 1000));
    r.setArchiveId(UUID.randomUUID().toString()).setArchiveExpiresAt(2593000);
    assertFalse(SharedDshPolicy.confirms(i, r.build(), 1000));
    assertTrue(SharedDshPolicy.confirms(i, r.setWorkspaceEmpty(true).build(), 1000));
    assertFalse(SharedDshPolicy.confirms(i, r.setArchiveExpiresAt(2592999).build(), 1000));
  }

  @Test
  void discardRequiresExplicitConfirmationAndPrepareRequiresEmptyDirectory() {
    var i = intent("discard-sessions");
    var r = receipt(i);
    assertFalse(SharedDshPolicy.confirms(i, r.build(), 1000));
    assertTrue(SharedDshPolicy.confirms(i, r.setSessionsDiscarded(true).build(), 1000));
    i = intent("prepare-empty");
    r = receipt(i);
    assertFalse(SharedDshPolicy.confirms(i, r.build(), 1000));
    assertTrue(SharedDshPolicy.confirms(i, r.setWorkspaceEmpty(true).build(), 1000));
  }

  @Test
  void stepUpBoundaryAndUnknownModesFailClosed() {
    var now = Instant.ofEpochSecond(1000);
    assertTrue(SharedDshPolicy.stepUp(Instant.ofEpochSecond(401), now));
    assertFalse(SharedDshPolicy.stepUp(Instant.ofEpochSecond(400), now));
    assertFalse(SharedDshPolicy.stepUp(Instant.ofEpochSecond(1001), now));
    assertFalse(SharedDshPolicy.stepUp(null, now));
    assertThrows(RuntimeException.class, () -> SharedDshPolicy.mode("root"));
    assertThrows(RuntimeException.class, () -> SharedDshPolicy.mode(DshInstanceMode.UNRECOGNIZED));
    assertThrows(RuntimeException.class, () -> intent("probe-entry"));
  }

  @Test
  void canonicalTicketSignatureUsesRuntimeKeysAndDedicatedPurpose() throws Exception {
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var claims = new TreeMap<String, Object>();
    claims.put("z", 60);
    claims.put("a", Map.of("member", "test"));
    String token = SharedDshTickets.sign("runtime-test", claims, pair.getPrivate());
    var parts = token.split("\\.");
    var decoder = Base64.getUrlDecoder();
    assertEquals(
        "{\"alg\":\"EdDSA\",\"kid\":\"runtime-test\",\"typ\":\"auriga-shared-ticket+jwt\"}",
        new String(decoder.decode(parts[0]), StandardCharsets.UTF_8));
    assertEquals(
        "{\"a\":{\"member\":\"test\"},\"z\":60}",
        new String(decoder.decode(parts[1]), StandardCharsets.UTF_8));
    var verifier = Signature.getInstance("Ed25519");
    verifier.initVerify(pair.getPublic());
    verifier.update((parts[0] + "." + parts[1]).getBytes(StandardCharsets.US_ASCII));
    assertTrue(verifier.verify(decoder.decode(parts[2])));
  }

  @Test
  void ticketInteroperatesWithFrozenKOracle() throws Exception {
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var b = new TreeMap<String, Object>();
    b.put("tenantId", binding.getTenantId());
    b.put("memberId", binding.getMemberId());
    b.put("placementId", binding.getPlacementId());
    b.put("physicalInstanceId", binding.getPhysicalInstanceId());
    b.put("mode", "shared");
    b.put("epoch", 1);
    b.put("revision", 1);
    String session = UUID.randomUUID().toString();
    var claims = new TreeMap<String, Object>();
    claims.put("schemaVersion", 1);
    claims.put("binding", b);
    claims.put("platformSessionId", session);
    claims.put("audience", "auriga.placement.exchange");
    claims.put("issuedAt", 1000);
    claims.put("expiresAt", 1060);
    claims.put("jti", UUID.randomUUID().toString());
    String ticket = SharedDshTickets.sign("test", claims, pair.getPrivate());
    var root = java.nio.file.Path.of("").toAbsolutePath();
    while (!java.nio.file.Files.exists(root.resolve("contracts/shared-dsh/ports.mjs")))
      root = root.getParent();
    var executable =
        root.resolve(
            "services/contracts/target/node/"
                + (System.getProperty("os.name").startsWith("Windows") ? "node.exe" : "node"));
    String script =
        "import {createPublicKey} from 'node:crypto';import {readFileSync} from 'node:fs';"
            + "const {EntryDouble,exchangeTicket}=await import('./contracts/shared-dsh/ports.mjs');"
            + "const v=JSON.parse(readFileSync(0,'utf8'));const e=new EntryDouble(v.binding,v.session);"
            + "const keys=new Map([['test',createPublicKey({key:Buffer.from(v.key,'base64'),format:'der',type:'spki'})]]);"
            + "const c={now:1000,physicalInstanceId:v.binding.physicalInstanceId,ready:true,ipMode:true};"
            + "if(exchangeTicket({ticket:v.ticket},e,keys,c).status!==302)process.exit(2);"
            + "let rejected=false;try{exchangeTicket({ticket:v.ticket},e,keys,c)}catch{rejected=true}"
            + "if(!rejected)process.exit(3);";
    var process =
        new ProcessBuilder(executable.toString(), "--input-type=module", "-e", script)
            .directory(root.toFile())
            .redirectErrorStream(true)
            .start();
    try {
      var json = tools.jackson.databind.json.JsonMapper.builder().build();
      try (var stdin = process.getOutputStream()) {
        stdin.write(
            json.writeValueAsBytes(
                Map.of(
                    "binding",
                    b,
                    "session",
                    session,
                    "ticket",
                    ticket,
                    "key",
                    Base64.getEncoder().encodeToString(pair.getPublic().getEncoded()))));
      }
      assertTrue(process.waitFor(15, java.util.concurrent.TimeUnit.SECONDS));
      assertEquals(
          0,
          process.exitValue(),
          new String(process.getInputStream().readAllBytes(), StandardCharsets.UTF_8));
    } finally {
      process.destroyForcibly();
    }
  }

  @Test
  void receiptAndArchiveIdsMustBeCanonicalUuids() {
    var upper = UUID.randomUUID().toString().toUpperCase(Locale.ROOT);
    assertTrue(SharedDshPolicy.uuid(upper));
    var i = intent("cooperative-cancel");
    assertFalse(SharedDshPolicy.confirms(i, receipt(i).setReceiptId("not-a-uuid").build(), 1000));
    var a = intent("archive-reset");
    assertFalse(
        SharedDshPolicy.confirms(
            a,
            receipt(a)
                .setWorkspaceEmpty(true)
                .setArchiveId("bad")
                .setArchiveExpiresAt(2593000)
                .build(),
            1000));
  }
}
