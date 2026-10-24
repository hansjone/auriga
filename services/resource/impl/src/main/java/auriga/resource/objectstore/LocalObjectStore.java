package auriga.resource.objectstore;

import static java.nio.file.LinkOption.NOFOLLOW_LINKS;
import static java.nio.file.StandardOpenOption.*;

import auriga.common.error.*;
import io.grpc.Status;
import java.io.*;
import java.nio.ByteBuffer;
import java.nio.channels.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.security.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.locks.ReentrantReadWriteLock;
import java.util.function.Supplier;
import java.util.function.UnaryOperator;

/**
 * JDK local filesystem implementation; requires no-replace hard-link publication and file locks.
 */
public final class LocalObjectStore implements ObjectStore {
  private static final ErrorCode DIGEST_MISMATCH =
      ErrorCode.of("resource.digest_mismatch", 422, Status.Code.INVALID_ARGUMENT);
  private static final ErrorCode STORAGE_UNAVAILABLE =
      ErrorCode.of("resource.storage_unavailable", 503, Status.Code.UNAVAILABLE);
  private static final String MAGIC = "auriga-resource-stage-v1";
  private static final String NAMESPACE = ".auriga-resource-objects-v1";
  private static final String DATA = "payload";
  private static final String LEASE = "lease";
  // POSIX locks are process-associated: closing any channel for an active lease can drop its
  // OS lock. All instances coordinate BEFORE opening a lease and through channel close.
  private static final Object JVM_LEASE_MONITOR = new Object();
  private static final Set<Path> JVM_ACTIVE_LEASES = new HashSet<>();
  private final Path root;
  private final Clock clock;
  private final Duration stageTtl;
  private final UnaryOperator<OutputStream> output;
  private final Map<UUID, Stage> stages = new ConcurrentHashMap<>();
  private final ReentrantReadWriteLock lifecycle = new ReentrantReadWriteLock();
  private boolean closed;

  public LocalObjectStore(Path root) {
    this(root, Clock.systemUTC(), Duration.ofHours(24));
  }

  public LocalObjectStore(Path root, Clock clock, Duration stageTtl) {
    this(root, clock, stageTtl, UnaryOperator.identity());
  }

  // Package-local IO composition for controlled write/close failures on a real filesystem.
  LocalObjectStore(Path root, Clock clock, Duration stageTtl, UnaryOperator<OutputStream> output) {
    if (root == null || !root.isAbsolute() || !root.equals(root.normalize())) throw invalid();
    if (root.getFileSystem() != FileSystems.getDefault())
      throw storage(new UnsupportedOperationException("Default physical filesystem required"));
    String filesystemRoot = root.getRoot().toString();
    if (root.getFileSystem().supportedFileAttributeViews().contains("dos")
        && System.getProperty("os.name").startsWith("Windows")) {
      if (!filesystemRoot.matches("[A-Za-z]:\\\\")) throw invalid();
      for (Path name : root) if (name.toString().contains(":")) throw invalid();
    }
    this.root = root;
    this.clock = Objects.requireNonNull(clock);
    if (stageTtl == null || stageTtl.isNegative() || stageTtl.isZero()) throw invalid();
    this.stageTtl = stageTtl;
    this.output = Objects.requireNonNull(output);
    try {
      checkedDirectory(root);
      ensureDirectory(root.resolve(NAMESPACE));
    } catch (IOException | UnsupportedOperationException e) {
      throw storage(e);
    }
  }

  @Override
  public Staged putStaged(UUID tenant, InputStream input, long maxBytes) {
    // Input ownership begins even if validation or the closed-store check fails.
    Objects.requireNonNull(input);
    var guard = lifecycle.readLock();
    guard.lock();
    Stage stage = null;
    try {
      try (input) {
        checkOpen();
        checkTenant(tenant);
        if (maxBytes < 0 || maxBytes > MAX_BYTES) throw invalid();
        stage = newStage(tenant);
        var digest = sha256();
        long count = 0;
        Path data = stage.directory.resolve(DATA);
        try (FileChannel channel =
                FileChannel.open(data, Set.of(CREATE_NEW, WRITE, NOFOLLOW_LINKS), privateFile());
            OutputStream sink =
                Objects.requireNonNull(output.apply(Channels.newOutputStream(channel)))) {
          byte[] buffer = new byte[65536];
          while (true) {
            int read = input.read(buffer, 0, (int) Math.min(buffer.length, maxBytes - count + 1));
            if (read < 0) break;
            if (read == 0) {
              int single = input.read();
              if (single < 0) break;
              buffer[0] = (byte) single;
              read = 1;
            }
            if (read > maxBytes - count) throw invalid();
            sink.write(buffer, 0, read);
            digest.update(buffer, 0, read);
            count += read;
          }
          sink.flush();
          channel.force(true);
        }
        stage.object = new ObjectInfo("sha256:" + HexFormat.of().formatHex(digest.digest()), count);
      } catch (IOException | UnsupportedOperationException e) {
        cleanupFailedStage(stage, e);
        throw storage(e);
      } catch (RuntimeException e) {
        cleanupFailedStage(stage, e);
        throw e;
      }
      stages.put(stage.id, stage);
      return new Staged(new StageId(stage.id), stage.object);
    } finally {
      guard.unlock();
    }
  }

  @Override
  public InputStream openStaged(UUID tenant, StageId id) {
    return operation(
        () -> {
          Stage stage = requireStage(tenant, id);
          synchronized (stage) {
            if (stages.get(stage.id) != stage) throw missing();
            try {
              return Channels.newInputStream(verified(stage.directory.resolve(DATA), stage.object));
            } catch (IOException | UnsupportedOperationException e) {
              throw storage(e);
            }
          }
        });
  }

  @Override
  public ObjectInfo commitIfAbsent(UUID tenant, StageId id, ObjectInfo expected) {
    return operation(
        () -> {
          Stage stage = requireStage(tenant, id);
          synchronized (stage) {
            if (!stages.remove(stage.id, stage)) throw missing();
            RuntimeException failure = null;
            try {
              checkObject(expected);
              if (expected.sizeBytes() != stage.object.sizeBytes()) throw invalid();
              if (!expected.digest().equals(stage.object.digest())) throw mismatch();
              try (var ignored = verified(stage.directory.resolve(DATA), expected)) {
                Path target = objectPath(tenant, expected.digest(), true);
                try {
                  // Atomic no-replace publication. ATOMIC_MOVE can replace an existing Unix target.
                  Files.createLink(target, stage.directory.resolve(DATA));
                } catch (FileAlreadyExistsException exists) {
                  // Existing entries are never trusted or overwritten, even if the name matches.
                }
                try (var committed = verified(target, expected)) {
                  return expected;
                }
              }
            } catch (IOException | UnsupportedOperationException e) {
              failure = storage(e);
              throw failure;
            } catch (RuntimeException e) {
              failure = e;
              throw e;
            } finally {
              try {
                dispose(stage);
              } catch (IOException | RuntimeException e) {
                if (failure != null) failure.addSuppressed(e);
                else throw storage(e);
              }
            }
          }
        });
  }

  @Override
  public ObjectInfo stat(UUID tenant, String digest) {
    return operation(
        () -> {
          checkTenant(tenant);
          checkDigest(digest);
          try {
            Path path = objectPath(tenant, digest, false);
            long size = regular(path).size();
            if (size < 0 || size > MAX_BYTES) throw mismatch();
            var info = new ObjectInfo(digest, size);
            checkObject(info);
            try (var ignored = verified(path, info)) {
              return info;
            }
          } catch (NoSuchFileException e) {
            throw missing();
          } catch (IOException | UnsupportedOperationException e) {
            throw storage(e);
          }
        });
  }

  @Override
  public InputStream open(UUID tenant, ObjectInfo expected) {
    return operation(
        () -> {
          checkTenant(tenant);
          checkObject(expected);
          try {
            return Channels.newInputStream(
                verified(objectPath(tenant, expected.digest(), false), expected));
          } catch (NoSuchFileException e) {
            throw missing();
          } catch (IOException | UnsupportedOperationException e) {
            throw storage(e);
          }
        });
  }

  @Override
  public void abort(UUID tenant, StageId id) {
    operation(
        () -> {
          checkTenant(tenant);
          checkId(id);
          Stage stage = stages.get(id.value());
          if (stage == null || !stage.tenant.equals(tenant)) return null;
          synchronized (stage) {
            if (stages.remove(stage.id, stage)) {
              try {
                dispose(stage);
              } catch (IOException e) {
                throw storage(e);
              }
            }
          }
          return null;
        });
  }

  @Override
  public int cleanupExpiredStaging(UUID tenant) {
    return operation(
        () -> {
          checkTenant(tenant);
          try {
            Path parent = tenantPath(tenant, false).resolve("staging");
            checkedDirectory(parent);
            int removed = 0;
            Instant cutoff = clock.instant().minus(stageTtl);
            try (DirectoryStream<Path> entries = Files.newDirectoryStream(parent)) {
              for (Path entry : entries) {
                String name = entry.getFileName().toString();
                if (!name.matches("stage-[0-9a-f]{32}")) continue;
                checkedDirectory(entry);
                Path lease = entry.resolve(LEASE);
                if (!Files.exists(lease, NOFOLLOW_LINKS)) continue;
                synchronized (JVM_LEASE_MONITOR) {
                  if (locallyActiveLease(lease)) continue;
                  regular(lease);
                  boolean eligible = false;
                  try (FileChannel channel = FileChannel.open(lease, READ, WRITE, NOFOLLOW_LINKS)) {
                    FileLock lock;
                    try {
                      lock = channel.tryLock();
                    } catch (OverlappingFileLockException active) {
                      // Cooperating holders were excluded without opening a second channel.
                      throw new IOException("Uncoordinated in-process lease lock", active);
                    }
                    if (lock == null) continue;
                    try (lock) {
                      if (!expiredMarker(channel, tenant, name, cutoff)) continue;
                      if (!recognizedContents(entry)) continue;
                      Path data = entry.resolve(DATA);
                      if (Files.exists(data, NOFOLLOW_LINKS)) {
                        regular(data);
                        Files.delete(data);
                      }
                      eligible = true;
                    }
                  } catch (NoSuchFileException raced) {
                    continue;
                  }
                  if (eligible) {
                    // No recursive deletion, no following links, and no committed-object GC.
                    Files.deleteIfExists(lease);
                    if (Files.deleteIfExists(entry)) removed++;
                  }
                }
              }
            }
            return removed;
          } catch (NoSuchFileException absent) {
            return 0;
          } catch (IOException | UnsupportedOperationException e) {
            throw storage(e);
          }
        });
  }

  @Override
  public void close() {
    var guard = lifecycle.writeLock();
    guard.lock();
    try {
      if (closed) return;
      closed = true;
      RuntimeException failure = null;
      for (Stage stage : stages.values()) {
        stages.remove(stage.id);
        try {
          dispose(stage);
        } catch (IOException | RuntimeException e) {
          if (failure == null) failure = storage(e);
          else failure.addSuppressed(e);
        }
      }
      if (failure != null) throw failure;
    } finally {
      guard.unlock();
    }
  }

  private Stage newStage(UUID tenant) throws IOException {
    Path parent = tenantPath(tenant, true).resolve("staging");
    ensureDirectory(parent);
    synchronized (JVM_LEASE_MONITOR) {
      UUID id = UUID.randomUUID();
      Path directory = parent.resolve("stage-" + id.toString().replace("-", ""));
      Files.createDirectory(directory, privateDirectory());
      FileChannel lease = null;
      FileLock lock = null;
      Path leasePath = directory.resolve(LEASE);
      JVM_ACTIVE_LEASES.add(leasePath);
      try {
        lease =
            FileChannel.open(
                leasePath, Set.of(CREATE_NEW, READ, WRITE, NOFOLLOW_LINKS), privateFile());
        lock = lease.tryLock();
        if (lock == null) throw new IOException("Exclusive stage lease unavailable");
        byte[] marker =
            (MAGIC + "\n" + tenant + "\n" + id + "\n" + clock.instant() + "\n")
                .getBytes(StandardCharsets.US_ASCII);
        ByteBuffer bytes = ByteBuffer.wrap(marker);
        while (bytes.hasRemaining()) lease.write(bytes);
        lease.force(true);
        return new Stage(tenant, id, directory, lease, lock);
      } catch (IOException | RuntimeException e) {
        if (lock != null)
          try {
            lock.close();
          } catch (IOException close) {
            e.addSuppressed(close);
          }
        if (lease == null) JVM_ACTIVE_LEASES.remove(leasePath);
        else
          try {
            lease.close();
            JVM_ACTIVE_LEASES.remove(leasePath);
          } catch (IOException close) {
            e.addSuppressed(close);
          }
        try {
          Files.deleteIfExists(directory.resolve(LEASE));
          Files.delete(directory);
        } catch (IOException cleanup) {
          e.addSuppressed(cleanup);
        }
        throw e;
      }
    }
  }

  private FileChannel verified(Path path, ObjectInfo expected) throws IOException {
    checkedDirectory(path.getParent());
    BasicFileAttributes before = regular(path);
    if (before.size() != expected.sizeBytes()) throw mismatch();
    FileChannel channel = FileChannel.open(path, READ, NOFOLLOW_LINKS);
    try {
      var hash = sha256();
      ByteBuffer buffer = ByteBuffer.allocate(65536);
      long count = 0;
      while (true) {
        int read = channel.read(buffer);
        if (read < 0) break;
        count += read;
        if (count > expected.sizeBytes() || count > MAX_BYTES) throw mismatch();
        buffer.flip();
        hash.update(buffer);
        buffer.clear();
      }
      BasicFileAttributes after = regular(path);
      if (count != expected.sizeBytes()
          || channel.size() != count
          || after.size() != count
          || !Objects.equals(before.fileKey(), after.fileKey())
          || !expected.digest().equals("sha256:" + HexFormat.of().formatHex(hash.digest())))
        throw mismatch();
      channel.position(0);
      return channel;
    } catch (IOException | RuntimeException e) {
      try {
        channel.close();
      } catch (IOException close) {
        e.addSuppressed(close);
      }
      throw e;
    }
  }

  private Path tenantPath(UUID tenant, boolean create) throws IOException {
    checkedDirectory(root);
    Path namespace = root.resolve(NAMESPACE);
    checkedDirectory(namespace);
    Path path = namespace.resolve(tenant.toString());
    if (create) ensureDirectory(path);
    else checkedDirectory(path);
    return path;
  }

  private Path objectPath(UUID tenant, String digest, boolean create) throws IOException {
    Path directory = tenantPath(tenant, create).resolve("objects");
    if (create) ensureDirectory(directory);
    else checkedDirectory(directory);
    return directory.resolve(digest.substring(7));
  }

  private void checkedDirectory(Path path) throws IOException {
    Path current = path.getRoot();
    checkDirectoryEntry(current);
    for (Path part : path) {
      current = current.resolve(part);
      checkDirectoryEntry(current);
    }
  }

  private void checkDirectoryEntry(Path path) throws IOException {
    var attributes = Files.readAttributes(path, BasicFileAttributes.class, NOFOLLOW_LINKS);
    if (!attributes.isDirectory() || attributes.isSymbolicLink() || attributes.isOther())
      throw new IOException("Non-physical directory rejected");
  }

  private void ensureDirectory(Path path) throws IOException {
    checkedDirectory(path.getParent());
    try {
      Files.createDirectory(path, privateDirectory());
    } catch (FileAlreadyExistsException existing) {
      /* validate below */
    }
    checkedDirectory(path);
  }

  private BasicFileAttributes regular(Path path) throws IOException {
    var attributes = Files.readAttributes(path, BasicFileAttributes.class, NOFOLLOW_LINKS);
    if (!attributes.isRegularFile() || attributes.isSymbolicLink() || attributes.isOther())
      throw new IOException("Non-regular file rejected");
    return attributes;
  }

  private FileAttribute<?>[] privateFile() {
    return root.getFileSystem().supportedFileAttributeViews().contains("posix")
        ? new FileAttribute<?>[] {
          PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rw-------"))
        }
        : new FileAttribute<?>[0];
  }

  private FileAttribute<?>[] privateDirectory() {
    return root.getFileSystem().supportedFileAttributeViews().contains("posix")
        ? new FileAttribute<?>[] {
          PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------"))
        }
        : new FileAttribute<?>[0];
  }

  private boolean expiredMarker(FileChannel channel, UUID tenant, String name, Instant cutoff)
      throws IOException {
    if (channel.size() < 1 || channel.size() > 256) return false;
    ByteBuffer bytes = ByteBuffer.allocate((int) channel.size());
    while (bytes.hasRemaining()) if (channel.read(bytes) < 0) return false;
    String text = new String(bytes.array(), StandardCharsets.US_ASCII);
    String[] fields = text.split("\n", -1);
    if (fields.length != 5
        || !fields[0].equals(MAGIC)
        || !fields[1].equals(tenant.toString())
        || !fields[4].isEmpty()) return false;
    try {
      UUID id = UUID.fromString(fields[2]);
      if (!fields[2].equals(id.toString())
          || !name.equals("stage-" + id.toString().replace("-", ""))) return false;
      Instant created = Instant.parse(fields[3]);
      return fields[3].equals(created.toString()) && !created.isAfter(cutoff);
    } catch (IllegalArgumentException | DateTimeException invalid) {
      return false;
    }
  }

  private boolean recognizedContents(Path directory) throws IOException {
    try (DirectoryStream<Path> entries = Files.newDirectoryStream(directory)) {
      for (Path path : entries)
        if (!Set.of(DATA, LEASE).contains(path.getFileName().toString())) return false;
    }
    return true;
  }

  private void dispose(Stage stage) throws IOException {
    synchronized (JVM_LEASE_MONITOR) {
      IOException failure = null;
      try {
        checkedDirectory(stage.directory);
        regular(stage.directory.resolve(LEASE));
        Path data = stage.directory.resolve(DATA);
        if (Files.exists(data, NOFOLLOW_LINKS)) {
          regular(data);
          Files.delete(data);
        }
      } catch (IOException e) {
        failure = e;
      }
      try {
        stage.lock.close();
      } catch (IOException e) {
        if (failure == null) failure = e;
        else failure.addSuppressed(e);
      }
      try {
        stage.lease.close();
        JVM_ACTIVE_LEASES.remove(stage.directory.resolve(LEASE));
      } catch (IOException e) {
        if (failure == null) failure = e;
        else failure.addSuppressed(e);
      }
      if (failure != null) throw failure;
      Files.deleteIfExists(stage.directory.resolve(LEASE));
      Files.delete(stage.directory);
    }
  }

  /** Called only under JVM_LEASE_MONITOR; no FileChannel is opened for an active inode. */
  private static boolean locallyActiveLease(Path lease) throws IOException {
    if (JVM_ACTIVE_LEASES.contains(lease)) return true;
    // Also cover physical aliases. On the supported default Unix provider this uses stat,
    // not a lease FileChannel; Windows does not have POSIX close-any-descriptor semantics.
    for (Path active : JVM_ACTIVE_LEASES) {
      if (Files.isSameFile(active, lease)) return true;
    }
    return false;
  }

  private void cleanupFailedStage(Stage stage, Throwable failure) {
    if (stage != null)
      try {
        dispose(stage);
      } catch (IOException | RuntimeException cleanup) {
        failure.addSuppressed(cleanup);
      }
  }

  private Stage requireStage(UUID tenant, StageId id) {
    checkTenant(tenant);
    checkId(id);
    Stage stage = stages.get(id.value());
    if (stage == null || !stage.tenant.equals(tenant)) throw missing();
    return stage;
  }

  private <T> T operation(Supplier<T> action) {
    var guard = lifecycle.readLock();
    guard.lock();
    try {
      checkOpen();
      return action.get();
    } finally {
      guard.unlock();
    }
  }

  private void checkOpen() {
    if (closed) throw new IllegalStateException("Object store closed");
  }

  private static void checkTenant(UUID tenant) {
    if (tenant == null) throw invalid();
  }

  private static void checkId(StageId id) {
    if (id == null || id.value() == null) throw invalid();
  }

  private static void checkObject(ObjectInfo info) {
    if (info == null || info.sizeBytes() < 0 || info.sizeBytes() > MAX_BYTES) throw invalid();
    checkDigest(info.digest());
  }

  private static void checkDigest(String digest) {
    if (digest == null || !digest.matches("sha256:[0-9a-f]{64}")) throw invalid();
  }

  private static MessageDigest sha256() {
    try {
      return MessageDigest.getInstance("SHA-256");
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  private static AurigaException invalid() {
    return new AurigaException(CommonErrors.VALIDATION_FAILED);
  }

  private static AurigaException missing() {
    return new AurigaException(CommonErrors.NOT_FOUND);
  }

  private static AurigaException mismatch() {
    return new AurigaException(DIGEST_MISMATCH);
  }

  private static AurigaException storage(Throwable cause) {
    return new AurigaException(STORAGE_UNAVAILABLE, Map.of(), List.of(), cause);
  }

  private static final class Stage {
    final UUID tenant, id;
    final Path directory;
    final FileChannel lease;
    final FileLock lock;
    ObjectInfo object;

    Stage(UUID tenant, UUID id, Path directory, FileChannel lease, FileLock lock) {
      this.tenant = tenant;
      this.id = id;
      this.directory = directory;
      this.lease = lease;
      this.lock = lock;
    }
  }
}
