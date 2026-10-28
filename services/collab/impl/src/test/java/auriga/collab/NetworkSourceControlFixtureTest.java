package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.time.*;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class NetworkSourceControlFixtureTest {
  @Test
  void nativeWitnessUsesSingleClockSampleAcrossSecondBoundary() {
    var fixture =
        new NetworkSourceControlPostgresTest.Fixture(
            NetworkAuthorityGrpcTest.connection(UUID.randomUUID()), null, null);
    var clock = mock(Clock.class);
    var boundary = Instant.parse("2026-01-01T00:00:00.999999999Z");
    when(clock.instant()).thenReturn(boundary, boundary.plusNanos(1));
    var witness = NetworkSourceControlPostgresTest.witness(fixture, clock);
    assertEquals(
        Duration.ofSeconds(30),
        Duration.between(
            Instant.parse(witness.path("observed_at").asString()),
            Instant.parse(witness.path("valid_until").asString())));
    verify(clock).instant();
  }
}
