package auriga.iam;

import auriga.common.persistence.UuidV7;
import java.security.SecureRandom;
import java.time.*;
import tools.jackson.databind.node.ObjectNode;

/** Built-in QR login challenges with two-digit match codes. */
public final class QrChallenges {
  private final IamStore store;
  private final Clock clock;
  private final SecureRandom random = new SecureRandom();

  public QrChallenges(IamStore store, Clock clock) {
    this.store = store;
    this.clock = clock;
  }

  public ObjectNode create(
      String txId,
      String providerId,
      String purpose,
      String ip,
      String userAgent,
      String portalBase) {
    String challengeId = UuidV7.next().toString();
    String matchCode = String.format("%02d", random.nextInt(100));
    Instant expires = clock.instant().plusSeconds(300);
    ObjectNode row =
        store.insert(
            "qr_challenge",
            challengeId,
            Json.object(
                "challengeId",
                challengeId,
                "txId",
                txId,
                "providerId",
                providerId,
                "purpose",
                purpose,
                "matchCode",
                matchCode,
                "status",
                "waiting",
                "ip",
                ip == null ? "" : ip,
                "userAgent",
                userAgent == null ? "" : userAgent,
                "expiresAt",
                expires.toString()),
            expires);
    String content =
        (portalBase.endsWith("/") ? portalBase.substring(0, portalBase.length() - 1) : portalBase)
            + "/login/qr/"
            + challengeId;
    return Json.object(
        "challengeId",
        challengeId,
        "matchCode",
        matchCode,
        "content",
        content,
        "expiresAt",
        expires.toString(),
        "row",
        row);
  }

  public ObjectNode get(String challengeId) {
    ObjectNode row = store.get("qr_challenge", challengeId, false);
    IamError.require(row != null && !expired(row), "not_found", 404);
    return Json.object(
        "challengeId",
        challengeId,
        "initiator",
        Json.object("ip", row.path("ip").asString(), "userAgent", row.path("userAgent").asString()),
        "purpose",
        row.path("purpose").asString("login"),
        "expiresAt",
        row.path("expiresAt").asString());
  }

  public ObjectNode row(String challengeId, boolean lock) {
    return store.get("qr_challenge", challengeId, lock);
  }

  public void markScanned(String challengeId, String accountId) {
    ObjectNode row = store.get("qr_challenge", challengeId, true);
    IamError.require(row != null && !expired(row), "not_found", 404);
    if (row.path("status").asString().equals("waiting")) {
      row.put("status", "scanned");
      row.put("scannerAccountId", accountId);
      store.save("qr_challenge", row);
    }
  }

  public void confirm(
      String challengeId, String matchCode, String accountId, String purposeAccountId) {
    ObjectNode row = store.get("qr_challenge", challengeId, true);
    IamError.require(row != null && !expired(row), "not_found", 404);
    String purpose = row.path("purpose").asString("login");
    if (!purpose.equals("login"))
      IamError.require(
          purposeAccountId != null && purposeAccountId.equals(accountId), "forbidden", 403);
    if (!Secrets.same(row.path("matchCode").asString(), matchCode)) {
      row.put("status", "failed");
      store.save("qr_challenge", row);
      throw IamError.error("qr_match_mismatch", 409);
    }
    row.put("status", "confirmed");
    row.put("confirmedAccountId", accountId);
    store.save("qr_challenge", row);
  }

  public void reject(String challengeId, String accountId) {
    ObjectNode row = store.get("qr_challenge", challengeId, true);
    IamError.require(row != null && !expired(row), "not_found", 404);
    row.put("status", "rejected");
    row.put("scannerAccountId", accountId);
    store.save("qr_challenge", row);
  }

  private boolean expired(ObjectNode row) {
    return !clock.instant().isBefore(Instant.parse(row.path("expiresAt").asString()));
  }
}
