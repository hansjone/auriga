package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import java.util.List;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

class NetworkSubmitFrameBoundaryTest {
  @Test
  void completeSignedFrameAt65536BytesIsAccepted() throws Exception {
    boundary(65536);
  }

  @Test
  void completeSignedFrameAt65537BytesIsRejected() throws Exception {
    boundary(65537);
  }

  private void boundary(int bytes) throws Exception {
    try (var f = NetworkMailboxEnvelopeFactoryTest.fixture()) {
      var source = NetworkSourceStampTest.source("协作会话");
      var message =
          NetworkSourceStampTest.stamp(
              NetworkSourceStampTest.request("2", "10", "1"),
              source,
              UUID.randomUUID(),
              NetworkEnvelopeSigningTest.NOW,
              null,
              null);
      for (int i = 0; i < 4; i++) message.put("optional_padding_" + i, "");
      var context = NetworkSourceStamp.context(source, message);
      int padding = bytes - size(fullFrame(f.factory(), message, context));
      assertTrue(padding > 48000 && padding < 64000);
      for (int i = 0; i < 4; i++) {
        int length = Math.min(16000, padding);
        message.put("optional_padding_" + i, "x".repeat(length));
        padding -= length;
      }
      context = NetworkSourceStamp.context(source, message);
      NetworkSchemas.validate("Message", message);
      assertTrue(
          NetworkJson.canonical(message).getBytes(java.nio.charset.StandardCharsets.UTF_8).length
              < 65536);
      assertEquals(
          bytes,
          size(fullFrame(f.factory(), message, context)),
          "budget includes both signatures, SourceContext, recipient and continuation page header");
      var jdbc = mock(JdbcTemplate.class);
      var mailbox =
          new NetworkMailboxService(
              jdbc,
              new TenantTransactionManager(mock(javax.sql.DataSource.class)),
              null,
              f.keys(),
              NetworkEnvelopeSigningTest.CLOCK);
      var frozenContext = context;
      if (bytes == 65536) assertDoesNotThrow(() -> mailbox.preflightSubmit(message, frozenContext));
      else
        assertEquals(
            "network.protocol_error",
            assertThrows(
                    AurigaException.class, () -> mailbox.preflightSubmit(message, frozenContext))
                .errorCode()
                .code());
      verifyNoInteractions(jdbc);
    }
  }

  private ObjectNode fullFrame(
      NetworkMailboxEnvelopeFactory factory, ObjectNode message, ObjectNode source) {
    var maximum = "x".repeat(128);
    var context =
        new NetworkEnvelopeSigner.Context("2", "20", maximum, Long.MAX_VALUE, maximum, maximum);
    var recipient =
        new NetworkMailboxService.RecipientView(UUID.randomUUID(), "3", "pending", Long.MAX_VALUE);
    var item =
        new NetworkMailboxService.FetchItem(
            UUID.randomUUID(),
            Long.MAX_VALUE,
            UUID.fromString(message.path("message_id").asString()),
            "hello",
            "message",
            "10",
            "1",
            UUID.fromString(message.path("thread_id").asString()),
            NetworkEnvelopeSigningTest.NOW,
            List.of(recipient));
    var deadline = java.time.Instant.parse(message.path("expires_at").asString());
    var delivery = factory.delivery(item, message, source, null, null, context, deadline);
    return factory.page(
        List.of(delivery),
        new NetworkMailboxService.NodeRoute(
            "2", "20", Long.MAX_VALUE, Long.MAX_VALUE, Long.MAX_VALUE),
        Long.MAX_VALUE,
        context,
        deadline,
        Long.MAX_VALUE,
        "x".repeat(32));
  }

  private int size(ObjectNode frame) {
    // The independent serializer also measures the over-limit frame, which JCS correctly rejects.
    return NetworkJson.JSON.writeValueAsBytes(frame).length;
  }
}
