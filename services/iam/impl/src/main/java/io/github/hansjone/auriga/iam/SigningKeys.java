package io.github.hansjone.auriga.iam;

import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/**
 * JWKS publication is committed before activation; old verification keys survive token lifetime.
 */
public final class SigningKeys {
  public static final UUID PLATFORM = new UUID(0, 0);
  private final IamStore store;
  private final Secrets secrets;
  private final Clock clock;
  private final Duration propagation;

  public SigningKeys(IamStore store, Secrets secrets, Clock clock, Duration propagation) {
    this.store = store;
    this.secrets = secrets;
    this.clock = clock;
    this.propagation = propagation;
    if (propagation.compareTo(Duration.ofSeconds(30)) < 0)
      throw new IllegalArgumentException("JWKS propagation must be at least 30 seconds");
  }

  public String publishNext() {
    return store.isolated(
        PLATFORM,
        () -> {
          store.lock("signing-keys");
          try {
            String kid = io.github.hansjone.auriga.common.persistence.UuidV7.next().toString();
            ECKey key = new ECKeyGenerator(Curve.P_256).keyID(kid).generate();
            store.insert(
                "signing_key",
                kid,
                Json.object(
                    "kid",
                    kid,
                    "state",
                    "next",
                    "publishedAt",
                    clock.instant().toString(),
                    "private",
                    secrets.encrypt(key.toJSONString(), "signing:" + kid),
                    "public",
                    Json.read(key.toPublicJWK().toJSONString())),
                null);
            return kid;
          } catch (com.nimbusds.jose.JOSEException e) {
            throw new IllegalStateException(e);
          }
        });
  }

  public void activate(String kid) {
    store.isolated(
        PLATFORM,
        () -> {
          store.lock("signing-keys");
          ObjectNode next = store.get("signing_key", kid, true);
          IamError.require(
              next != null && next.path("state").asString().equals("next"),
              "invalid_key_state",
              409);
          IamError.require(
              !clock
                  .instant()
                  .isBefore(Instant.parse(next.path("publishedAt").asString()).plus(propagation)),
              "key_not_propagated",
              409);
          for (ObjectNode old : store.all("signing_key"))
            if (old.path("state").asString().equals("current")) {
              old.put("state", "retiring");
              old.put("retainUntil", clock.instant().plusSeconds(360).toString());
              store.save("signing_key", old);
            }
          next.put("state", "current");
          store.save("signing_key", next);
          return null;
        });
  }

  public ECKey current() {
    return store.isolated(
        PLATFORM,
        () ->
            store.all("signing_key").stream()
                .filter(k -> k.path("state").asString().equals("current"))
                .findFirst()
                .map(this::privateKey)
                .orElseThrow(() -> IamError.error("signing_key_unavailable", 503)));
  }

  private ECKey privateKey(ObjectNode row) {
    try {
      return ECKey.parse(
          secrets.decrypt(row.path("private").asString(), "signing:" + row.path("kid").asString()));
    } catch (java.text.ParseException e) {
      throw new IllegalStateException(e);
    }
  }

  public ObjectNode jwks() {
    return store.isolated(
        PLATFORM,
        () ->
            Json.object(
                "keys",
                store.all("signing_key").stream()
                    .filter(
                        k ->
                            !k.path("state").asString().equals("retiring")
                                || clock
                                    .instant()
                                    .isBefore(Instant.parse(k.path("retainUntil").asString())))
                    .map(k -> k.get("public"))
                    .toList()));
  }
}
