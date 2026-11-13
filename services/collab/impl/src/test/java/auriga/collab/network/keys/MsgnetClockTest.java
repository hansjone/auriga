package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestFactory;

class MsgnetClockTest {
  final MutableClock wall = new MutableClock();
  long tick;
  final MsgnetClock clock = new MsgnetClock(wall, () -> tick);

  @TestFactory
  List<DynamicTest> frozenIssuerVectorsUseProductionClock() throws Exception {
    try (var input =
        getClass()
            .getClassLoader()
            .getResourceAsStream("network-fixtures/auto-renewal-v2-vectors.json")) {
      assertNotNull(input);
      var vectors = KeyTestMaterial.JSON.readTree(input).path("issuer_cases");
      assertEquals(8, vectors.size());
      var tests = new ArrayList<DynamicTest>();
      for (var vector : vectors) {
        tests.add(
            DynamicTest.dynamicTest(
                vector.path("id").asString(),
                () -> {
                  var fixture = new MsgnetClockTest();
                  var settings = vector.path("input");
                  if (settings.path("clock_error_persisted").asBoolean(false)) {
                    fixture.rejected(fixture.wall.now.minusSeconds(61));
                    if (settings.path("stable_samples").asBoolean(false)) {
                      fixture.advance(30);
                      fixture.rejected(fixture.wall.now);
                      fixture.advance(30);
                      fixture.clock.sample(fixture.wall.now);
                    }
                  }
                  var delta = settings.path("clock");
                  fixture.wall.now =
                      fixture.wall.now.plusSeconds(delta.path("wall_delta").asLong());
                  fixture.tick +=
                      Duration.ofSeconds(delta.path("monotonic_delta").asLong()).toNanos();
                  Instant db = fixture.wall.now.plusSeconds(delta.path("pg_skew").asLong());
                  if ("accept".equals(vector.path("expected").asString())) {
                    assertEquals(fixture.wall.now, fixture.clock.sample(db));
                  } else {
                    fixture.rejected(db);
                  }
                }));
      }
      return tests;
    }
  }

  void advance(long seconds) {
    wall.now = wall.now.plusSeconds(seconds);
    tick += Duration.ofSeconds(seconds).toNanos();
  }

  void rejected(Instant database) {
    var failure = assertThrows(AurigaException.class, () -> clock.sample(database));
    assertEquals("collab.msgnet_clock_error", failure.errorCode().code());
    assertTrue(clock.clockError());
  }

  @Test
  void pgSkewBoundaryAndTwoSpacedStableSamples() {
    assertEquals(wall.now, clock.sample(wall.now.plusSeconds(60)));
    rejected(wall.now.plusSeconds(61));
    rejected(wall.now);
    advance(29);
    rejected(wall.now);
    advance(1);
    rejected(wall.now);
    advance(30);
    assertEquals(wall.now, clock.sample(wall.now));
    assertFalse(clock.clockError());
  }

  @Test
  void synchronousPgAndAppJumpStillFailsAndCorrectionRecovers() {
    clock.sample(wall.now);
    wall.now = wall.now.plus(Duration.ofDays(365 * 5L));
    rejected(wall.now);
    wall.now = wall.now.minus(Duration.ofDays(365 * 5L));
    rejected(wall.now);
    advance(30);
    rejected(wall.now);
    advance(30);
    assertEquals(wall.now, clock.sample(wall.now));
  }

  @Test
  void backwardJumpAndMissingPgSampleFailClosed() {
    wall.now = wall.now.minusSeconds(61);
    rejected(wall.now);
    advance(30);
    rejected(null);
    advance(30);
    rejected(wall.now);
    advance(30);
    assertEquals(wall.now, clock.sample(wall.now));
  }

  @Test
  void smallRepeatedOffsetsCannotMoveTheBootBaseline() {
    advance(30);
    wall.now = wall.now.plusSeconds(200);
    clock.sample(wall.now);
    advance(30);
    wall.now = wall.now.plusSeconds(101);
    rejected(wall.now);
  }

  @Test
  void unstableRecoverySampleRestartsCountAndRestartHasNoFutureTimeFence() {
    rejected(wall.now.minusSeconds(61));
    advance(30);
    rejected(wall.now);
    advance(30);
    wall.now = wall.now.plusSeconds(6);
    rejected(wall.now);
    advance(30);
    rejected(wall.now);
    advance(30);
    assertEquals(wall.now, clock.sample(wall.now));
    var restarted = new MsgnetClock(wall, () -> tick);
    assertEquals(wall.now, restarted.sample(wall.now));
  }

  static final class MutableClock extends Clock {
    Instant now = Instant.parse("2026-11-14T00:00:00Z");

    @Override
    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    @Override
    public Clock withZone(ZoneId zone) {
      return this;
    }

    @Override
    public Instant instant() {
      return now;
    }
  }
}
