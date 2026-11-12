package auriga.resource.web;

import java.io.IOException;
import java.nio.file.*;
import java.nio.file.attribute.PosixFilePermissions;

/** Dedicated servlet multipart spool, separate from shared OS temporary files. */
public final class ResourceMultipartDirectory implements AutoCloseable {
  private final Path directory;

  public ResourceMultipartDirectory() throws IOException {
    directory =
        FileSystems.getDefault().supportedFileAttributeViews().contains("posix")
            ? Files.createTempDirectory(
                "auriga-resource-multipart-",
                PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")))
            : Files.createTempDirectory("auriga-resource-multipart-");
  }

  public String location() {
    return directory.toString();
  }

  @Override
  public void close() throws IOException {
    Files.deleteIfExists(directory);
  }
}
