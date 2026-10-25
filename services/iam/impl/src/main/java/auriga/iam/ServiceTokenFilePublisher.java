package auriga.iam;

import auriga.iam.api.InternalTokenIssuer;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.util.*;
import java.util.concurrent.*;
import org.slf4j.LoggerFactory;
import org.springframework.context.SmartLifecycle;

/**
 * Shared installation-only POSIX service JWT export, with atomic replacement and fixed subjects.
 */
public class ServiceTokenFilePublisher implements SmartLifecycle, AutoCloseable {
  private static final Set<PosixFilePermission> DIRECTORY =
      PosixFilePermissions.fromString("rwxr-x---");
  private static final Set<PosixFilePermission> FILE = PosixFilePermissions.fromString("rw-r-----");
  private final String service;
  private final InternalTokenIssuer issuer;
  private final Path secretFile;
  private final Path tokenFile;
  private final int readerGroup;
  private final int installationOwner;
  private final UserPrincipal processOwner;
  private ScheduledExecutorService scheduler;
  private boolean running;
  private long generation;

  ServiceTokenFilePublisher(
      String service,
      InternalTokenIssuer issuer,
      Path secretFile,
      Path tokenFile,
      int readerGroup,
      int installationOwner) {
    if (!Set.of("edge", "ai-gateway").contains(service)) throw invalid();
    this.service = service;
    this.issuer = Objects.requireNonNull(issuer);
    this.secretFile = absolute(secretFile);
    this.tokenFile = absolute(tokenFile);
    if (readerGroup < 0) throw invalid();
    this.readerGroup = readerGroup;
    if (installationOwner < -1) throw invalid();
    this.installationOwner = installationOwner;
    try {
      processOwner =
          tokenFile
              .getFileSystem()
              .getUserPrincipalLookupService()
              .lookupPrincipalByName(System.getProperty("user.name"));
    } catch (IOException e) {
      throw invalid();
    }
    if (this.secretFile.equals(this.tokenFile)) throw invalid();
  }

  private static Path absolute(Path path) {
    if (!path.isAbsolute() || !path.equals(path.normalize()) || path.getParent() == null)
      throw invalid();
    return path;
  }

  private static IllegalStateException invalid() {
    return new IllegalStateException("Invalid private service token installation files");
  }

  @Override
  public synchronized void start() {
    if (running) return;
    try {
      checkAncestors(tokenFile.getParent().getParent());
      if (!Files.exists(tokenFile.getParent(), LinkOption.NOFOLLOW_LINKS)) {
        Files.createDirectory(
            tokenFile.getParent(), PosixFilePermissions.asFileAttribute(DIRECTORY));
        Files.setAttribute(
            tokenFile.getParent(), "unix:gid", readerGroup, LinkOption.NOFOLLOW_LINKS);
        Files.setPosixFilePermissions(tokenFile.getParent(), DIRECTORY);
      }
      checkDirectory(tokenFile.getParent(), false);
      checkOutputGroup();
      checkDirectory(secretFile.getParent(), true);
    } catch (IOException | UnsupportedOperationException e) {
      throw invalid();
    }
    running = true;
    generation++;
    scheduler =
        Executors.newSingleThreadScheduledExecutor(
            Thread.ofPlatform().name("iam-" + service + "-token").daemon(true).factory());
    final long scheduledGeneration = generation;
    scheduler.scheduleWithFixedDelay(() -> refresh(scheduledGeneration), 0, 60, TimeUnit.SECONDS);
  }

  /**
   * Each attempt reauthenticates with the existing issuer; a failure never rewrites the old JWT.
   */
  boolean refresh() {
    final long activeGeneration;
    synchronized (this) {
      activeGeneration = generation;
    }
    return refresh(activeGeneration);
  }

  private boolean refresh(long activeGeneration) {
    synchronized (this) {
      if (!running || generation != activeGeneration) return false;
    }
    byte[] raw = null;
    Path temporary = null;
    try {
      checkDirectory(secretFile.getParent(), true);
      checkDirectory(tokenFile.getParent(), false);
      checkOutputGroup();
      try (var secrets = secure(secretFile.getParent());
          var output = secure(tokenFile.getParent())) {
        checkFile(secrets, secretFile, true, 128);
        try (var channel =
            secrets.newByteChannel(
                secretFile.getFileName(),
                Set.of(StandardOpenOption.READ, LinkOption.NOFOLLOW_LINKS))) {
          var bytes = ByteBuffer.allocate(129);
          while (bytes.hasRemaining() && channel.read(bytes) != -1) {}
          raw = Arrays.copyOf(bytes.array(), bytes.position());
          Arrays.fill(bytes.array(), (byte) 0);
        }
        String secret = new String(raw, java.nio.charset.StandardCharsets.US_ASCII);
        if (!secret.matches("[A-Za-z0-9_-]{43}")) throw invalid();
        String jwt = issuer.issue(service, secret);
        if (jwt == null
            || jwt.length() > 16 * 1024
            || !jwt.matches("[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+")) throw invalid();
        synchronized (this) {
          if (!running || generation != activeGeneration) return false;
          checkFile(output, tokenFile, false, 16 * 1024);
          temporary = Path.of("." + service + "-" + UUID.randomUUID() + ".tmp");
          try (var channel =
              output.newByteChannel(
                  temporary,
                  Set.of(
                      StandardOpenOption.CREATE_NEW,
                      StandardOpenOption.WRITE,
                      LinkOption.NOFOLLOW_LINKS),
                  PosixFilePermissions.asFileAttribute(FILE))) {
            byte[] encoded = jwt.getBytes(java.nio.charset.StandardCharsets.US_ASCII);
            try {
              var bytes = ByteBuffer.wrap(encoded);
              while (bytes.hasRemaining()) channel.write(bytes);
              if (channel instanceof FileChannel file) file.force(true);
            } finally {
              Arrays.fill(encoded, (byte) 0);
            }
          }
          Files.setAttribute(
              tokenFile.getParent().resolve(temporary),
              "unix:gid",
              readerGroup,
              LinkOption.NOFOLLOW_LINKS);
          Files.setPosixFilePermissions(tokenFile.getParent().resolve(temporary), FILE);
          checkFile(output, tokenFile.getParent().resolve(temporary), false, 16 * 1024);
          checkDirectory(tokenFile.getParent(), false);
          checkOutputGroup();
          checkFile(output, tokenFile, false, 16 * 1024);
          // SecureDirectoryStream.move is renameat: atomic replacement within this pinned
          // directory.
          output.move(temporary, output, tokenFile.getFileName());
          temporary = null;
        }
      }
      return true;
    } catch (RuntimeException | IOException e) {
      // Never log the exception: issuers/filesystem providers may include credentials or contents.
      LoggerFactory.getLogger(ServiceTokenFilePublisher.class)
          .warn("Service token refresh failed; previous token expiry is unchanged");
      return false;
    } finally {
      if (raw != null) Arrays.fill(raw, (byte) 0);
      if (temporary != null) {
        try (var output = secure(tokenFile.getParent())) {
          output.deleteFile(temporary);
        } catch (IOException | RuntimeException ignored) {
          // The private directory remains inaccessible to untrusted users.
        }
      }
    }
  }

  private static SecureDirectoryStream<Path> secure(Path directory) throws IOException {
    var stream = Files.newDirectoryStream(directory);
    if (stream instanceof SecureDirectoryStream<Path> secure) return secure;
    stream.close();
    throw invalid();
  }

  private void checkAncestors(Path directory) throws IOException {
    if (directory == null) throw invalid();
    for (Path p = directory; p != null; p = p.getParent()) {
      var a = Files.readAttributes(p, PosixFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
      if (!a.isDirectory() || a.isSymbolicLink()) throw invalid();
      int uid = (int) Files.getAttribute(p, "unix:uid", LinkOption.NOFOLLOW_LINKS);
      if (uid != 0 && !a.owner().equals(processOwner) && uid != installationOwner) throw invalid();
      // A shared sticky ancestor (e.g. /tmp) cannot rename another owner's private child.
      boolean writable =
          a.permissions().contains(PosixFilePermission.GROUP_WRITE)
              || a.permissions().contains(PosixFilePermission.OTHERS_WRITE);
      if (writable
          && (((int) Files.getAttribute(p, "unix:mode", LinkOption.NOFOLLOW_LINKS)) & 01000) == 0)
        throw invalid();
    }
  }

  private void checkDirectory(Path directory, boolean secret) throws IOException {
    checkAncestors(directory.getParent());
    var a = Files.readAttributes(directory, PosixFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
    Set<PosixFilePermission> allowed =
        secret ? PosixFilePermissions.fromString("rwx------") : DIRECTORY;
    if (!a.isDirectory()
        || !allowed.containsAll(a.permissions())
        || !a.permissions().containsAll(PosixFilePermissions.fromString("rwx------")))
      throw invalid();
    if (secret) {
      if (installationOwner == -1
          ? !a.owner().equals(processOwner)
          : !Files.getAttribute(directory, "unix:uid", LinkOption.NOFOLLOW_LINKS)
              .equals(installationOwner)) throw invalid();
    }
  }

  private void checkOutputGroup() throws IOException {
    var a =
        Files.readAttributes(
            tokenFile.getParent(), PosixFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
    if (!a.owner().equals(processOwner)
        || !a.permissions().equals(DIRECTORY)
        || !Files.getAttribute(tokenFile.getParent(), "unix:gid", LinkOption.NOFOLLOW_LINKS)
            .equals(readerGroup)) throw invalid();
  }

  private static void checkFile(
      SecureDirectoryStream<Path> directory, Path path, boolean required, int limit)
      throws IOException {
    Path file = path.getFileName();
    PosixFileAttributes a;
    try {
      a =
          directory
              .getFileAttributeView(file, PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS)
              .readAttributes();
    } catch (NoSuchFileException e) {
      if (required) throw e;
      return;
    }
    var parent = directory.getFileAttributeView(PosixFileAttributeView.class).readAttributes();
    var allowed = required ? PosixFilePermissions.fromString("rw-------") : FILE;
    if (!a.isRegularFile()
        || a.size() < 1
        || a.size() > limit
        || !allowed.containsAll(a.permissions())
        || !a.owner().equals(parent.owner())
        || !a.group().equals(parent.group())) throw invalid();
    var linked = Files.readAttributes(path, "unix:nlink", LinkOption.NOFOLLOW_LINKS);
    if (!Integer.valueOf(1).equals(linked.get("nlink"))
        || !a.fileKey()
            .equals(
                Files.readAttributes(path, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS)
                    .fileKey())) throw invalid();
  }

  @Override
  public synchronized void stop() {
    running = false;
    generation++;
    if (scheduler != null) {
      scheduler.shutdownNow();
      scheduler = null;
    }
    try {
      checkDirectory(tokenFile.getParent(), false);
      checkOutputGroup();
      try (var output = secure(tokenFile.getParent())) {
        checkFile(output, tokenFile, false, 16 * 1024);
        output.deleteFile(tokenFile.getFileName());
      }
    } catch (IOException | RuntimeException ignored) {
      // A damaged/missing path fails closed at the reader; an old signed JWT still expires
      // normally.
    }
  }

  @Override
  public synchronized boolean isRunning() {
    return running;
  }

  @Override
  public void close() {
    stop();
  }
}
