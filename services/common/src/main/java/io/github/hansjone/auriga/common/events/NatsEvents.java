package io.github.hansjone.auriga.common.events;

import io.nats.client.AuthHandler;
import io.nats.client.Connection;
import io.nats.client.JetStream;
import io.nats.client.JetStreamApiException;
import io.nats.client.JetStreamManagement;
import io.nats.client.NKey;
import io.nats.client.Nats;
import io.nats.client.Options;
import io.nats.client.api.StorageType;
import io.nats.client.api.StreamConfiguration;
import io.nats.client.impl.Headers;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.Objects;
import javax.net.ssl.SSLContext;

/** NATS JetStream plumbing: streams from contracts/README.md and the outbox transport. */
public final class NatsEvents {
  public static final String EVENTS_STREAM = "AURIGA_EVENTS";
  public static final String AUDIT_STREAM = "AURIGA_AUDIT";
  public static final String MSG_ID_HEADER = "Nats-Msg-Id";

  private NatsEvents() {}

  /** Creates the two platform streams if they do not exist (idempotent, safe on every start). */
  public static void ensureStreams(Connection connection, Duration duplicateWindow)
      throws IOException, JetStreamApiException {
    JetStreamManagement jsm = connection.jetStreamManagement();
    ensure(jsm, EVENTS_STREAM, "auriga.evt.>", duplicateWindow);
    ensure(jsm, AUDIT_STREAM, "auriga.audit.>", duplicateWindow);
  }

  private static void ensure(
      JetStreamManagement jsm, String name, String subjects, Duration duplicateWindow)
      throws IOException, JetStreamApiException {
    if (jsm.getStreamNames().contains(name)) return;
    try {
      jsm.addStream(
          StreamConfiguration.builder()
              .name(name)
              .subjects(subjects)
              .storageType(StorageType.File)
              .duplicateWindow(duplicateWindow)
              .build());
    } catch (JetStreamApiException e) {
      // Another replica created it concurrently.
      if (!jsm.getStreamNames().contains(name)) throw e;
    }
  }

  /**
   * Client options: credentials file (JWT + nkey) or bare nkey seed file, optional TLS (JVM trust
   * store; a {@code tls://} URL implies it). Each service should get its own NATS user whose
   * publish permissions only allow {@code auriga.evt.<its own types>} and {@code
   * auriga.audit.<service>} (docs/dev/common.md).
   */
  public static Options options(
      String url, String connectionName, String credsFile, String nkeySeedFile, boolean tls) {
    Options.Builder builder =
        Options.builder().server(url).connectionName(connectionName).maxReconnects(-1);
    if (credsFile != null && !credsFile.isBlank()) {
      builder.authHandler(Nats.credentials(credsFile));
    } else if (nkeySeedFile != null && !nkeySeedFile.isBlank()) {
      builder.authHandler(new NkeySeedAuthHandler(Path.of(nkeySeedFile)));
    }
    if (tls) {
      try {
        builder.sslContext(SSLContext.getDefault());
      } catch (java.security.NoSuchAlgorithmException e) {
        throw new IllegalStateException("No default TLS context", e);
      }
    }
    return builder.build();
  }

  /** nkey authentication from a seed file (read on each connect, never logged). */
  static final class NkeySeedAuthHandler implements AuthHandler {
    private final Path seedFile;

    NkeySeedAuthHandler(Path seedFile) {
      this.seedFile = seedFile;
    }

    private NKey key() {
      try {
        return NKey.fromSeed(Files.readString(seedFile).trim().toCharArray());
      } catch (Exception e) {
        throw new IllegalStateException("Cannot read NATS nkey seed file", e);
      }
    }

    @Override
    public byte[] sign(byte[] nonce) {
      try {
        return key().sign(nonce);
      } catch (Exception e) {
        throw new IllegalStateException("Cannot sign NATS nonce", e);
      }
    }

    @Override
    public char[] getID() {
      try {
        return key().getPublicKey();
      } catch (Exception e) {
        throw new IllegalStateException("Cannot derive NATS public key", e);
      }
    }

    @Override
    public char[] getJWT() {
      return null;
    }
  }

  /** Publishes and waits for the JetStream ack; {@code Nats-Msg-Id} = event id. */
  public static EventTransport transport(Connection connection) {
    Objects.requireNonNull(connection);
    JetStream js;
    try {
      js = connection.jetStream();
    } catch (IOException e) {
      throw new IllegalStateException("JetStream unavailable", e);
    }
    return (subject, messageId, payload) -> {
      Headers headers = new Headers();
      headers.put(MSG_ID_HEADER, messageId);
      js.publish(subject, headers, payload);
    };
  }
}
