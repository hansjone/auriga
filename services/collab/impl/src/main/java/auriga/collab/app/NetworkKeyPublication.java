package auriga.collab.app;

import java.io.IOException;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import tools.jackson.databind.node.ObjectNode;

/** Exclusive single-publisher metadata store. Contains public keys and lifecycle state only. */
final class NetworkKeyPublication implements AutoCloseable {
  private final Path file;
  private FileChannel channel;
  private FileLock lock;

  NetworkKeyPublication(Path file) {
    this.file = file.toAbsolutePath().normalize();
    try {
      Files.createDirectories(this.file.getParent());
      channel =
          FileChannel.open(
              this.file.resolveSibling(this.file.getFileName() + ".lock"),
              StandardOpenOption.CREATE,
              StandardOpenOption.WRITE);
      lock = channel.tryLock();
      if (lock == null)
        throw new IllegalStateException("Network key metadata already has a publisher");
    } catch (IOException | RuntimeException e) {
      close();
      throw new IllegalStateException("Cannot acquire network key publication storage");
    }
  }

  ObjectNode read() {
    try {
      return Files.exists(file) ? (ObjectNode) NetworkJson.parse(Files.readAllBytes(file)) : null;
    } catch (IOException | RuntimeException e) {
      throw new IllegalStateException("Cannot read network key metadata");
    }
  }

  void save(ObjectNode state) {
    Path temporary = null;
    try {
      byte[] bytes = NetworkJson.canonical(state).getBytes(java.nio.charset.StandardCharsets.UTF_8);
      temporary = Files.createTempFile(file.getParent(), "network-key-publication-", ".tmp");
      try (var output = FileChannel.open(temporary, StandardOpenOption.WRITE)) {
        java.nio.ByteBuffer buffer = java.nio.ByteBuffer.wrap(bytes);
        while (buffer.hasRemaining()) output.write(buffer);
        output.force(true);
      }
      Files.move(
          temporary, file, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
    } catch (IOException e) {
      throw new IllegalStateException("Cannot persist network key metadata");
    } finally {
      if (temporary != null) {
        try {
          Files.deleteIfExists(temporary);
        } catch (IOException e) {
          throw new IllegalStateException("Cannot finish network key publication");
        }
      }
    }
  }

  @Override
  public void close() {
    try {
      if (lock != null) lock.release();
    } catch (IOException e) {
      throw new IllegalStateException("Cannot release network key publication lock");
    } finally {
      try {
        if (channel != null) channel.close();
      } catch (IOException e) {
        throw new IllegalStateException("Cannot close network key publication storage");
      }
    }
  }
}
