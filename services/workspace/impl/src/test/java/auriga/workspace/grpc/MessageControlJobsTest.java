package auriga.workspace.grpc;

import static org.junit.jupiter.api.Assertions.*;

import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.Test;

class MessageControlJobsTest {
  @Test
  void messageFrameOverflowKeepsTheLifecycleStreamUsable() {
    var delivered = new java.util.concurrent.atomic.AtomicInteger();
    var failures = new java.util.concurrent.atomic.AtomicInteger();
    var output =
        new NodeOutput(
            new io.grpc.stub.StreamObserver<auriga.contracts.node.v1.SessionResponse>() {
              public void onNext(auriga.contracts.node.v1.SessionResponse response) {
                delivered.incrementAndGet();
              }

              public void onError(Throwable error) {
                failures.incrementAndGet();
              }

              public void onCompleted() {}
            },
            error -> failures.incrementAndGet(),
            () -> {});
    var tooLarge =
        auriga.contracts.node.v1.SessionResponse.newBuilder()
            .setMsgnetControlPackage(
                auriga.contracts.node.v1.MsgnetControlPackage.newBuilder()
                    .setCanonicalJson(
                        com.google.protobuf.ByteString.copyFrom(new byte[NodeOutput.MAX_BYTES])))
            .build();
    assertFalse(output.trySendMessageControl(tooLarge));
    assertEquals(0, failures.get());
    assertTrue(
        output.send(
            auriga.contracts.node.v1.SessionResponse.newBuilder()
                .setWelcome(auriga.contracts.node.v1.Welcome.getDefaultInstance())
                .build()));
    assertEquals(1, delivered.get());
  }

  @Test
  void stalledLedgerNeverBlocksTheCallerAndHasNoUnboundedQueue() throws Exception {
    var entered = new CountDownLatch(1);
    var interrupted = new CountDownLatch(1);
    try (var jobs = new MessageControlJobs(1)) {
      assertTrue(
          jobs.submit(
              () -> {
                entered.countDown();
                try {
                  new CountDownLatch(1).await();
                } catch (InterruptedException stopped) {
                  Thread.currentThread().interrupt();
                  interrupted.countDown();
                }
              }));
      assertTrue(entered.await(2, TimeUnit.SECONDS));
      var ran = new AtomicBoolean();
      assertFalse(jobs.submit(() -> ran.set(true)));
      assertFalse(ran.get());
    }
    assertTrue(interrupted.await(2, TimeUnit.SECONDS));
  }

  @Test
  void completedAttemptReleasesItsSlotAndClosedExecutorRejects() throws Exception {
    var jobs = new MessageControlJobs(1);
    var complete = new CountDownLatch(1);
    assertTrue(jobs.submit(complete::countDown));
    assertTrue(complete.await(2, TimeUnit.SECONDS));
    // Shutdown does not wait for arbitrary external I/O to return on a node thread.
    jobs.close();
    assertFalse(jobs.submit(() -> fail("closed executor accepted work")));
  }
}
