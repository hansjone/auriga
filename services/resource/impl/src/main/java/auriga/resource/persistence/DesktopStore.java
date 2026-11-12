package auriga.resource.persistence;

import static auriga.common.error.CommonErrors.*;

import auriga.common.error.AurigaException;
import java.io.IOException;
import java.nio.channels.FileChannel;
import java.nio.file.*;
import java.util.concurrent.locks.ReentrantLock;
import java.util.function.Function;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Durable installation-local control state. Host worker and API share a POSIX record lock. */
public final class DesktopStore {
  public static final JsonMapper JSON = JsonMapper.builder().build();
  private static final ReentrantLock JVM_LOCK = new ReentrantLock();
  private final Path root;

  public DesktopStore(Path root) throws IOException {
    this.root = root.toAbsolutePath().normalize();
    Files.createDirectories(this.root);
    for (Path p = this.root; p != null; p = p.getParent()) {
      if (Files.isSymbolicLink(p)) throw new IOException("Symlink control directory");
    }
  }

  public Path root() {
    return root;
  }

  public ObjectNode read() {
    return change(ObjectNode::deepCopy);
  }

  public <T> T change(Function<ObjectNode, T> operation) {
    JVM_LOCK.lock();
    try (var channel =
            FileChannel.open(
                root.resolve("state.lock"),
                StandardOpenOption.CREATE,
                StandardOpenOption.WRITE,
                LinkOption.NOFOLLOW_LINKS);
        var lock = channel.lock()) {
      Path file = root.resolve("state.json");
      if (Files.isSymbolicLink(file)) throw new IOException("Symlink state");
      ObjectNode state =
          Files.exists(file)
              ? (ObjectNode) JSON.readTree(Files.readAllBytes(file))
              : JSON.createObjectNode();
      byte[] before = JSON.writeValueAsBytes(state);
      T result = operation.apply(state);
      byte[] after = JSON.writeValueAsBytes(state);
      if (!java.util.Arrays.equals(before, after)) {
        Path temporary = Files.createTempFile(root, ".state-", ".json");
        try {
          try (var output = FileChannel.open(temporary, StandardOpenOption.WRITE)) {
            var bytes = java.nio.ByteBuffer.wrap(after);
            while (bytes.hasRemaining()) output.write(bytes);
            output.force(true);
          }
          Files.move(
              temporary, file, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
        } finally {
          Files.deleteIfExists(temporary);
        }
      }
      return result;
    } catch (IOException | ClassCastException e) {
      throw new AurigaException(UNAVAILABLE);
    } finally {
      JVM_LOCK.unlock();
    }
  }
}
