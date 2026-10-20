package auriga.workspace.grpc;

import auriga.common.error.*;
import auriga.contracts.node.v1.SessionResponse;
import io.grpc.stub.*;
import java.util.ArrayDeque;
import java.util.function.Consumer;

/**
 * One serialized writer and a hard bound on queued frames and bytes, including non-reading peers.
 */
public final class NodeOutput {
  public static final int MAX_FRAMES = 128, MAX_BYTES = 4 * 1024 * 1024;
  private final StreamObserver<SessionResponse> output;
  private final Consumer<Throwable> failure;
  private final ArrayDeque<SessionResponse> queue = new ArrayDeque<>();
  private int bytes;
  private boolean draining, closed, terminalDelivered;
  private Throwable terminal;
  private final java.time.Clock clock;
  private java.time.Instant blockedSince;

  public NodeOutput(
      StreamObserver<SessionResponse> output, Consumer<Throwable> failure, Runnable cancelled) {
    this(output, failure, cancelled, java.time.Clock.systemUTC());
  }

  public NodeOutput(
      StreamObserver<SessionResponse> output,
      Consumer<Throwable> failure,
      Runnable cancelled,
      java.time.Clock clock) {
    this.clock = clock;
    this.output = output;
    this.failure = failure;
    if (output instanceof ServerCallStreamObserver<SessionResponse> server) {
      server.setOnReadyHandler(this::drain);
      server.setOnCancelHandler(cancelled);
    }
  }

  public boolean send(SessionResponse response) {
    boolean overflow;
    synchronized (this) {
      if (closed) return false;
      overflow = queue.size() >= MAX_FRAMES || response.getSerializedSize() > MAX_BYTES - bytes;
      if (!overflow) {
        queue.add(response);
        bytes += response.getSerializedSize();
      }
    }
    if (overflow) {
      failure.accept(new AurigaException(CommonErrors.RATE_LIMITED));
      return false;
    }
    drain();
    return true;
  }

  public void terminate(Throwable error) {
    synchronized (this) {
      if (closed) return;
      closed = true;
      terminal = error;
      queue.clear();
      bytes = 0;
    }
    drain();
  }

  public void checkStalled() {
    boolean stalled;
    synchronized (this) {
      stalled =
          !closed
              && blockedSince != null
              && !blockedSince.isAfter(clock.instant().minusSeconds(30));
    }
    if (stalled) failure.accept(new AurigaException(CommonErrors.UNAVAILABLE));
  }

  private void drain() {
    synchronized (this) {
      if (draining) return;
      draining = true;
    }
    while (true) {
      SessionResponse next;
      Throwable error;
      boolean end;
      synchronized (this) {
        end = closed && !terminalDelivered;
        error = terminal;
        if (end) {
          terminalDelivered = true;
          next = null;
        } else if (closed || queue.isEmpty()) {
          blockedSince = null;
          draining = false;
          return;
        } else if (output instanceof ServerCallStreamObserver<?> server && !server.isReady()) {
          if (blockedSince == null) blockedSince = clock.instant();
          draining = false;
          return;
        } else {
          blockedSince = null;
          next = queue.remove();
          bytes -= next.getSerializedSize();
        }
      }
      try {
        if (end) {
          if (error == null) output.onCompleted();
          else output.onError(error);
          synchronized (this) {
            draining = false;
          }
          return;
        }
        output.onNext(next);
      } catch (RuntimeException e) {
        synchronized (this) {
          draining = false;
          closed = true;
          terminalDelivered = true;
          queue.clear();
          bytes = 0;
        }
        failure.accept(e);
        return;
      }
    }
  }
}
