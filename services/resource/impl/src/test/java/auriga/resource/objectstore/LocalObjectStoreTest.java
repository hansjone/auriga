package auriga.resource.objectstore;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.resource.objectstore.ObjectStore.*;
import java.io.*;
import java.nio.channels.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.nio.file.attribute.BasicFileAttributes;
import java.security.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.condition.*;
import org.junit.jupiter.api.io.TempDir;

class LocalObjectStoreTest {
  static final UUID TENANT = UUID.fromString("01900000-0000-7000-8000-000000000001");
  static final UUID OTHER = UUID.fromString("01900000-0000-7000-8000-000000000002");
  static final Instant START = Instant.parse("2026-01-01T00:00:00Z");
  static final byte[] BYTES = new byte[] {0, 1, 31, (byte) 139, (byte) 255, 10, 42};
  @TempDir Path root;

  static String digest(byte[] bytes) {
    try {
      return "sha256:"
          + HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  static ObjectInfo info(byte[] bytes) {
    return new ObjectInfo(digest(bytes), bytes.length);
  }

  static Staged stage(ObjectStore store, UUID tenant) {
    return store.putStaged(tenant, new ByteArrayInputStream(BYTES), ObjectStore.MAX_BYTES);
  }

  static AurigaException fails(String code, org.junit.jupiter.api.function.Executable action) {
    var failure = assertThrows(AurigaException.class, action);
    assertEquals(code, failure.errorCode().code());
    assertTrue(failure.params().isEmpty());
    return failure;
  }

  Path tenant(UUID tenant) {
    return root.resolve(".auriga-resource-objects-v1").resolve(tenant.toString());
  }

  Path staged(UUID tenant, StageId id) {
    return tenant(tenant)
        .resolve("staging")
        .resolve("stage-" + id.value().toString().replace("-", ""));
  }

  Path committed(UUID tenant, String digest) {
    return tenant(tenant).resolve("objects").resolve(digest.substring(7));
  }

  long stages(UUID tenant) throws IOException {
    Path directory = tenant(tenant).resolve("staging");
    if (!Files.exists(directory)) return 0;
    try (var entries = Files.list(directory)) {
      return entries.count();
    }
  }

  @Test
  void stagesCommitsAndReadsExactUninterpretedBytesAndClosesInput() throws Exception {
    var source = new TrackingInput(BYTES);
    try (ObjectStore store = new LocalObjectStore(root)) {
      var staged = store.putStaged(TENANT, source, 100);
      assertTrue(source.closed);
      assertEquals(info(BYTES), staged.object());
      assertTrue(staged.id().value().version() == 4);
      fails("common.not_found", () -> store.stat(TENANT, staged.object().digest()));
      try (var raw = store.openStaged(TENANT, staged.id())) {
        assertArrayEquals(BYTES, raw.readAllBytes());
      }
      assertEquals(staged.object(), store.commitIfAbsent(TENANT, staged.id(), staged.object()));
      assertEquals(staged.object(), store.stat(TENANT, staged.object().digest()));
      try (var raw = store.open(TENANT, staged.object())) {
        assertArrayEquals(BYTES, raw.readAllBytes());
      }
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  void zeroByteObjectsAreBoundedRawStorageAndNotArchiveValidation() throws Exception {
    try (var store = new LocalObjectStore(root)) {
      var stage = store.putStaged(TENANT, new ByteArrayInputStream(new byte[0]), 0);
      assertEquals(info(new byte[0]), stage.object());
      store.commitIfAbsent(TENANT, stage.id(), stage.object());
      try (var read = store.open(TENANT, stage.object())) {
        assertEquals(-1, read.read());
      }
      fails(
          "common.validation_failed",
          () -> store.putStaged(TENANT, new ByteArrayInputStream(BYTES), 0));
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  void boundsAreStreamedWithoutTrustingAvailableOrReadingBeyondTheLimit() throws Exception {
    try (var store = new LocalObjectStore(root)) {
      var exact = new PatternInput(ObjectStore.MAX_BYTES);
      var stage = store.putStaged(TENANT, exact, ObjectStore.MAX_BYTES);
      assertEquals(ObjectStore.MAX_BYTES, stage.object().sizeBytes());
      assertTrue(exact.closed);
      store.abort(TENANT, stage.id());
      var large = new PatternInput(ObjectStore.MAX_BYTES + 1000);
      fails(
          "common.validation_failed", () -> store.putStaged(TENANT, large, ObjectStore.MAX_BYTES));
      assertTrue(large.closed);
      assertEquals(ObjectStore.MAX_BYTES + 1, large.readBytes);
      assertTrue(large.maxRequest <= 65536);
      for (long max : new long[] {-1, ObjectStore.MAX_BYTES + 1}) {
        var source = new TrackingInput(BYTES);
        fails("common.validation_failed", () -> store.putStaged(TENANT, source, max));
        assertTrue(source.closed);
      }
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  void sizeAndDigestMismatchConsumeHandleWithoutPublishing() throws Exception {
    try (var store = new LocalObjectStore(root)) {
      var wrongSize = stage(store, TENANT);
      fails(
          "common.validation_failed",
          () ->
              store.commitIfAbsent(
                  TENANT, wrongSize.id(), new ObjectInfo(digest(BYTES), BYTES.length - 1)));
      fails(
          "common.not_found",
          () -> store.commitIfAbsent(TENANT, wrongSize.id(), wrongSize.object()));
      var wrongDigest = stage(store, TENANT);
      fails(
          "resource.digest_mismatch",
          () ->
              store.commitIfAbsent(
                  TENANT,
                  wrongDigest.id(),
                  new ObjectInfo("sha256:" + "0".repeat(64), BYTES.length)));
      store.abort(TENANT, wrongDigest.id());
      fails("common.not_found", () -> store.stat(TENANT, digest(BYTES)));
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  void invalidNamesSizesAndTenantNeverBecomePaths() throws Exception {
    try (var store = new LocalObjectStore(root)) {
      for (String name :
          List.of(
              "../outside",
              "/tmp/outside",
              "C:\\outside",
              "\\\\server\\share",
              "sha256:../../outside",
              "sha256:" + "A".repeat(64),
              "SHA256:" + "a".repeat(64),
              "sha256:" + "a".repeat(63),
              "sha256:" + "a".repeat(64) + "/x")) {
        fails("common.validation_failed", () -> store.stat(TENANT, name));
        fails("common.validation_failed", () -> store.open(TENANT, new ObjectInfo(name, 0)));
        var stage = stage(store, TENANT);
        fails(
            "common.validation_failed",
            () -> store.commitIfAbsent(TENANT, stage.id(), new ObjectInfo(name, 0)));
      }
      for (long size : new long[] {-1, ObjectStore.MAX_BYTES + 1})
        fails(
            "common.validation_failed",
            () -> store.open(TENANT, new ObjectInfo(digest(BYTES), size)));
      var source = new TrackingInput(BYTES);
      fails("common.validation_failed", () -> store.putStaged(null, source, 10));
      assertTrue(source.closed);
      fails("common.validation_failed", () -> store.abort(TENANT, new StageId(null)));
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  void inputReadAndCloseFailureLeaveNoPartialOrCommittableObject() throws Exception {
    for (boolean closeFailure : List.of(false, true)) {
      try (var store = new LocalObjectStore(root)) {
        var source =
            new InputStream() {
              int calls;
              boolean closed;

              @Override
              public int read() throws IOException {
                throw new IOException("Injected input failure");
              }

              @Override
              public int read(byte[] bytes, int offset, int length) throws IOException {
                if (calls++ == 0) {
                  bytes[offset] = 42;
                  return 1;
                }
                if (closeFailure) return -1;
                throw new IOException("Injected input failure");
              }

              @Override
              public void close() throws IOException {
                closed = true;
                if (closeFailure) throw new IOException("Injected input close failure");
              }
            };
        fails("resource.storage_unavailable", () -> store.putStaged(TENANT, source, 10));
        assertTrue(source.closed);
        assertEquals(0, stages(TENANT));
      }
    }
  }

  @Test
  void realPartialDiskWritesAndOutputCloseFailureAreCleaned() throws Exception {
    for (boolean closeFailure : List.of(false, true)) {
      try (var store =
          new LocalObjectStore(
              root,
              Clock.fixed(START, ZoneOffset.UTC),
              Duration.ofHours(24),
              sink ->
                  new FilterOutputStream(sink) {
                    @Override
                    public void write(byte[] bytes, int offset, int length) throws IOException {
                      out.write(bytes, offset, Math.min(2, length));
                      if (!closeFailure)
                        throw new IOException("Injected disk full after partial write");
                      if (length > 2) out.write(bytes, offset + 2, length - 2);
                    }

                    @Override
                    public void close() throws IOException {
                      super.close();
                      if (closeFailure) throw new IOException("Injected output close failure");
                    }
                  })) {
        var source = new TrackingInput(BYTES);
        fails("resource.storage_unavailable", () -> store.putStaged(TENANT, source, 10));
        assertTrue(source.closed);
        assertEquals(0, stages(TENANT));
        fails("common.not_found", () -> store.stat(TENANT, digest(BYTES)));
      }
    }
  }

  @Test
  void corruptionOfStagedContentIsReverifiedAndCannotBeCommitted() throws Exception {
    try (var store = new LocalObjectStore(root)) {
      var stage = stage(store, TENANT);
      byte[] wrong = BYTES.clone();
      wrong[0] = 99;
      Files.write(staged(TENANT, stage.id()).resolve("payload"), wrong);
      fails("resource.digest_mismatch", () -> store.openStaged(TENANT, stage.id()));
      fails(
          "resource.digest_mismatch",
          () -> store.commitIfAbsent(TENANT, stage.id(), stage.object()));
      assertEquals(0, stages(TENANT));
      fails("common.not_found", () -> store.stat(TENANT, digest(BYTES)));
    }
  }

  @Test
  void commitRetryAbortAndCleanupCannotChangeOrDeleteThePublishedInode() throws Exception {
    var clock = new MutableClock(START);
    try (var store = new LocalObjectStore(root, clock, Duration.ofHours(24))) {
      var first = stage(store, TENANT);
      store.commitIfAbsent(TENANT, first.id(), first.object());
      Path path = committed(TENANT, first.object().digest());
      Path witness = Files.createLink(root.resolve("published-identity"), path);
      fails("common.not_found", () -> store.openStaged(TENANT, first.id()));
      fails("common.not_found", () -> store.commitIfAbsent(TENANT, first.id(), first.object()));
      store.abort(TENANT, first.id());
      var duplicate = stage(store, TENANT);
      store.commitIfAbsent(TENANT, duplicate.id(), duplicate.object());
      clock.now = START.plus(Duration.ofDays(2));
      assertEquals(0, store.cleanupExpiredStaging(TENANT));
      assertTrue(Files.isSameFile(witness, path));
      assertArrayEquals(BYTES, Files.readAllBytes(path));
    }
  }

  @Test
  void writeChannelIsClosedBeforeHandleAndCannotMutatePublishedObject() throws Exception {
    var sink = new java.util.concurrent.atomic.AtomicReference<OutputStream>();
    try (var store =
        new LocalObjectStore(
            root,
            Clock.fixed(START, ZoneOffset.UTC),
            Duration.ofHours(24),
            stream -> {
              sink.set(stream);
              return stream;
            })) {
      var stage = stage(store, TENANT);
      assertThrows(IOException.class, () -> sink.get().write(8));
      store.commitIfAbsent(TENANT, stage.id(), stage.object());
      assertThrows(IOException.class, () -> sink.get().write(9));
      store.abort(TENANT, stage.id());
      fails("common.not_found", () -> store.commitIfAbsent(TENANT, stage.id(), stage.object()));
      assertArrayEquals(BYTES, Files.readAllBytes(committed(TENANT, stage.object().digest())));
    }
  }

  @Test
  void twoIndependentStoresConvergeOnOneCorrectObjectUnderConcurrentCommit() throws Exception {
    try (var first = new LocalObjectStore(root);
        var second = new LocalObjectStore(root);
        var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var a = stage(first, TENANT);
      var b = stage(second, TENANT);
      var start = new CountDownLatch(1);
      var ready = new CountDownLatch(2);
      var one =
          executor.submit(
              () -> {
                ready.countDown();
                assertTrue(start.await(10, TimeUnit.SECONDS));
                return first.commitIfAbsent(TENANT, a.id(), a.object());
              });
      var two =
          executor.submit(
              () -> {
                ready.countDown();
                assertTrue(start.await(10, TimeUnit.SECONDS));
                return second.commitIfAbsent(TENANT, b.id(), b.object());
              });
      assertTrue(ready.await(10, TimeUnit.SECONDS));
      start.countDown();
      assertEquals(a.object(), one.get(10, TimeUnit.SECONDS));
      assertEquals(a.object(), two.get(10, TimeUnit.SECONDS));
      assertEquals(0, stages(TENANT));
      try (var files = Files.list(tenant(TENANT).resolve("objects"))) {
        assertEquals(1, files.count());
      }
      try (var raw = second.open(TENANT, a.object())) {
        assertArrayEquals(BYTES, raw.readAllBytes());
      }
    }
  }

  @Test
  void existingDamagedObjectIsRejectedAndNeverReplacedOrDeleted() throws Exception {
    try (var store = new LocalObjectStore(root)) {
      var first = stage(store, TENANT);
      store.commitIfAbsent(TENANT, first.id(), first.object());
      Path path = committed(TENANT, first.object().digest());
      Path witness = Files.createLink(root.resolve("damaged-identity"), path);
      byte[] wrong = BYTES.clone();
      wrong[0] = 77;
      Files.write(path, wrong);
      fails("resource.digest_mismatch", () -> store.stat(TENANT, first.object().digest()));
      fails("resource.digest_mismatch", () -> store.open(TENANT, first.object()));
      var duplicate = stage(store, TENANT);
      fails(
          "resource.digest_mismatch",
          () -> store.commitIfAbsent(TENANT, duplicate.id(), duplicate.object()));
      assertTrue(Files.isSameFile(witness, path));
      assertArrayEquals(wrong, Files.readAllBytes(path));
      assertEquals(0, stages(TENANT));
      Files.write(path, new byte[0]);
      fails("resource.digest_mismatch", () -> store.open(TENANT, first.object()));
      Files.delete(path);
      fails("common.not_found", () -> store.stat(TENANT, first.object().digest()));
    }
  }

  @Test
  void tenantsAndLocalHandlesRemainIsolatedEvenForIdenticalDigests() throws Exception {
    try (var first = new LocalObjectStore(root);
        var second = new LocalObjectStore(root)) {
      var one = stage(first, TENANT);
      fails("common.not_found", () -> first.commitIfAbsent(OTHER, one.id(), one.object()));
      fails("common.not_found", () -> second.commitIfAbsent(TENANT, one.id(), one.object()));
      first.abort(OTHER, one.id());
      first.commitIfAbsent(TENANT, one.id(), one.object());
      fails("common.not_found", () -> first.open(OTHER, one.object()));
      var two = stage(second, OTHER);
      second.commitIfAbsent(OTHER, two.id(), two.object());
      assertFalse(
          Files.isSameFile(
              committed(TENANT, one.object().digest()), committed(OTHER, two.object().digest())));
      assertArrayEquals(BYTES, Files.readAllBytes(committed(OTHER, two.object().digest())));
    }
  }

  @Test
  void activeReadyAndInProgressStagesAreProtectedAcrossIndependentInstances() throws Exception {
    var clock = new MutableClock(START);
    try (var first = new LocalObjectStore(root, clock, Duration.ofHours(24));
        var second = new LocalObjectStore(root, clock, Duration.ofHours(24))) {
      var ready = stage(first, TENANT);
      clock.now = START.plus(Duration.ofDays(2));
      assertEquals(0, second.cleanupExpiredStaging(TENANT));
      try (var raw = first.openStaged(TENANT, ready.id())) {
        assertArrayEquals(BYTES, raw.readAllBytes());
      }
      var source =
          new TrackingInput(BYTES) {
            @Override
            public synchronized int read(byte[] bytes, int offset, int length) {
              clock.now = clock.now.plus(Duration.ofDays(2));
              assertEquals(0, second.cleanupExpiredStaging(TENANT));
              return super.read(bytes, offset, length);
            }
          };
      var streaming = first.putStaged(TENANT, source, 100);
      first.abort(TENANT, streaming.id());
      first.abort(TENANT, ready.id());
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  void localCleanupPreservesTheOsLeaseBeforeAndAfterOtherJvmCleanup() throws Exception {
    var clock = new MutableClock(START);
    try (var owner = new LocalObjectStore(root, clock, Duration.ofHours(24));
        var other = new LocalObjectStore(root, clock, Duration.ofHours(24))) {
      for (var cleaner : List.of(owner, other)) {
        var stage = stage(owner, TENANT);
        clock.now = clock.now.plus(Duration.ofDays(2));
        assertEquals(0, cleanupInChild(clock.now), "BEFORE_LOCAL");
        assertEquals(0, cleaner.cleanupExpiredStaging(TENANT), "LOCAL_CLEANED");
        assertEquals(0, cleanupInChild(clock.now), "AFTER_LOCAL");
        try (var raw = owner.openStaged(TENANT, stage.id())) {
          assertArrayEquals(BYTES, raw.readAllBytes());
        }
        assertEquals(stage.object(), owner.commitIfAbsent(TENANT, stage.id(), stage.object()));
        assertEquals(stage.object(), owner.stat(TENANT, stage.object().digest()));
      }
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  void activeLeaseIdentityAlsoProtectsAnAliasWithoutOpeningAnotherChannel() throws Exception {
    var clock = new MutableClock(START);
    try (var owner = new LocalObjectStore(root, clock, Duration.ofHours(24));
        var cleaner = new LocalObjectStore(root, clock, Duration.ofHours(24))) {
      var stage = stage(owner, TENANT);
      Path aliasDirectory =
          tenant(TENANT)
              .resolve("staging")
              .resolve("stage-" + UUID.randomUUID().toString().replace("-", ""));
      Files.createDirectory(aliasDirectory);
      Path alias =
          Files.createLink(
              aliasDirectory.resolve("lease"), staged(TENANT, stage.id()).resolve("lease"));
      assertTrue(Files.isSameFile(alias, staged(TENANT, stage.id()).resolve("lease")));
      clock.now = clock.now.plus(Duration.ofDays(2));
      assertEquals(0, cleanupInChild(clock.now), "ALIAS_BEFORE_LOCAL");
      assertEquals(0, cleaner.cleanupExpiredStaging(TENANT), "ALIAS_LOCAL_CLEANED");
      assertEquals(0, cleanupInChild(clock.now), "ALIAS_AFTER_LOCAL");
      try (var raw = owner.openStaged(TENANT, stage.id())) {
        assertArrayEquals(BYTES, raw.readAllBytes());
      }
      owner.commitIfAbsent(TENANT, stage.id(), stage.object());
      assertEquals(stage.object(), owner.stat(TENANT, stage.object().digest()));
      Files.delete(alias);
      Files.delete(aliasDirectory);
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  void stageStillBeingCopiedKeepsOsLeaseAfterLocalCleanup() throws Exception {
    var clock = new MutableClock(START);
    try (var owner = new LocalObjectStore(root, clock, Duration.ofHours(24));
        var cleaner = new LocalObjectStore(root, clock, Duration.ofHours(24))) {
      var checked = new java.util.concurrent.atomic.AtomicBoolean();
      var input =
          new FilterInputStream(new ByteArrayInputStream(BYTES)) {
            @Override
            public int read(byte[] bytes, int offset, int length) throws IOException {
              if (checked.compareAndSet(false, true)) {
                clock.now = clock.now.plus(Duration.ofDays(2));
                try {
                  assertEquals(0, cleanupInChild(clock.now), "COPY_BEFORE_LOCAL");
                  assertEquals(0, cleaner.cleanupExpiredStaging(TENANT), "COPY_LOCAL_CLEANED");
                  assertEquals(0, cleanupInChild(clock.now), "COPY_AFTER_LOCAL");
                } catch (Exception | AssertionError failure) {
                  throw new IOException("In-progress lease regression", failure);
                }
              }
              return super.read(bytes, offset, length);
            }
          };
      var stage = owner.putStaged(TENANT, input, ObjectStore.MAX_BYTES);
      assertTrue(checked.get());
      try (var raw = owner.openStaged(TENANT, stage.id())) {
        assertArrayEquals(BYTES, raw.readAllBytes());
      }
      owner.commitIfAbsent(TENANT, stage.id(), stage.object());
      assertEquals(stage.object(), owner.stat(TENANT, stage.object().digest()));
    }
  }

  @Test
  void crossProcessLeaseProtectsExpiredStageThenOrphanExpiresAtTheExactBoundary() throws Exception {
    var clock = new MutableClock(START);
    try (var store = new LocalObjectStore(root, clock, Duration.ofHours(24))) {
      Process child = stageProcess();
      try {
        var reader =
            new BufferedReader(
                new InputStreamReader(child.getInputStream(), StandardCharsets.UTF_8));
        var line =
            CompletableFuture.supplyAsync(
                    () -> {
                      try {
                        return reader.readLine();
                      } catch (IOException e) {
                        throw new UncheckedIOException(e);
                      }
                    })
                .get(15, TimeUnit.SECONDS);
        var id = new StageId(UUID.fromString(line));
        clock.now = START.plus(Duration.ofDays(2));
        assertEquals(0, store.cleanupExpiredStaging(TENANT));
        assertTrue(Files.exists(staged(TENANT, id).resolve("payload")));
        // Simulate exit between atomic publication and stage unlink: deleting a stage link
        // must preserve the committed name and its inode.
        Files.createDirectory(tenant(TENANT).resolve("objects"));
        Files.createLink(committed(TENANT, digest(BYTES)), staged(TENANT, id).resolve("payload"));
        child.getOutputStream().write(10);
        child.getOutputStream().flush();
        assertTrue(child.waitFor(15, TimeUnit.SECONDS));
        assertEquals(
            0,
            child.exitValue(),
            new String(child.getErrorStream().readAllBytes(), StandardCharsets.UTF_8));
        clock.now = START.plus(Duration.ofHours(24)).minusNanos(1);
        assertEquals(0, store.cleanupExpiredStaging(TENANT));
        clock.now = START.plus(Duration.ofHours(24));
        assertEquals(1, store.cleanupExpiredStaging(TENANT));
        assertEquals(0, stages(TENANT));
        assertEquals(info(BYTES), store.stat(TENANT, digest(BYTES)));
      } finally {
        if (child.isAlive()) {
          child.destroyForcibly();
          assertTrue(child.waitFor(15, TimeUnit.SECONDS));
        }
      }
    }
  }

  @Test
  void cleanupIgnoresUnrecognizedMaterialsAndNeverGarbageCollectsCommittedBytes() throws Exception {
    var clock = new MutableClock(START);
    try (var store = new LocalObjectStore(root, clock, Duration.ofHours(24))) {
      var object = stage(store, TENANT);
      store.commitIfAbsent(TENANT, object.id(), object.object());
      Path unknown =
          tenant(TENANT)
              .resolve("staging")
              .resolve("stage-" + UUID.randomUUID().toString().replace("-", ""));
      Files.createDirectory(unknown);
      Files.writeString(unknown.resolve("lease"), "not this component");
      Files.writeString(unknown.resolve("payload"), "preserve");
      Path other = Files.createDirectory(tenant(TENANT).resolve("staging").resolve("unrelated"));
      Files.writeString(other.resolve("sentinel"), "preserve");
      clock.now = START.plus(Duration.ofDays(2));
      assertEquals(0, store.cleanupExpiredStaging(TENANT));
      assertEquals("preserve", Files.readString(unknown.resolve("payload")));
      assertEquals("preserve", Files.readString(other.resolve("sentinel")));
      assertEquals(object.object(), store.stat(TENANT, object.object().digest()));
      assertEquals(0, store.cleanupExpiredStaging(OTHER));
    }
  }

  @Test
  void closeAbortsStagesRejectsReuseAndStillClosesNewInput() throws Exception {
    var store = new LocalObjectStore(root);
    var stage = stage(store, TENANT);
    store.close();
    store.close();
    assertEquals(0, stages(TENANT));
    var source = new TrackingInput(BYTES);
    assertThrows(IllegalStateException.class, () -> store.putStaged(TENANT, source, 100));
    assertTrue(source.closed);
    assertThrows(
        IllegalStateException.class,
        () -> store.commitIfAbsent(TENANT, stage.id(), stage.object()));
  }

  @Test
  void nonRegularCommittedEntryIsNotAcceptedAndCannotBeOverwritten() throws Exception {
    try (var store = new LocalObjectStore(root)) {
      var stage = stage(store, TENANT);
      Files.createDirectory(tenant(TENANT).resolve("objects"));
      Path existing = Files.createDirectory(committed(TENANT, stage.object().digest()));
      Files.writeString(existing.resolve("sentinel"), "preserve");
      fails(
          "resource.storage_unavailable",
          () -> store.commitIfAbsent(TENANT, stage.id(), stage.object()));
      assertEquals("preserve", Files.readString(existing.resolve("sentinel")));
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  void unsupportedNonPhysicalFilesystemIsExplicitlyRejected() throws Exception {
    var uri = java.net.URI.create("jar:" + root.resolve("unsupported.zip").toUri());
    try (var filesystem = FileSystems.newFileSystem(uri, Map.of("create", "true"))) {
      fails("resource.storage_unavailable", () -> new LocalObjectStore(filesystem.getPath("/")));
    }
  }

  @Test
  void rejectsRelativeOrUnnormalizedRootWithoutCreatingCallerDirectories() {
    fails("common.validation_failed", () -> new LocalObjectStore(Path.of("relative")));
    fails("common.validation_failed", () -> new LocalObjectStore(root.resolve("x").resolve("..")));
    assertFalse(Files.exists(root.resolve("x")));
    fails(
        "common.validation_failed",
        () -> new LocalObjectStore(root, Clock.systemUTC(), Duration.ZERO));
  }

  @Test
  @EnabledOnOs(OS.WINDOWS)
  void windowsJunctionRootAndObjectDirectoryAreRejectedWithoutPrivilegeChanges() throws Exception {
    Path outside = Files.createDirectory(root.resolve("outside"));
    Files.writeString(outside.resolve("sentinel"), "preserve");
    Path link = root.resolve("junction");
    junction(link, outside);
    try {
      fails("resource.storage_unavailable", () -> new LocalObjectStore(link));
    } finally {
      Files.delete(link);
    }
    try (var store = new LocalObjectStore(root)) {
      var stage = stage(store, TENANT);
      Path objects = tenant(TENANT).resolve("objects");
      junction(objects, outside);
      try {
        fails(
            "resource.storage_unavailable",
            () -> store.commitIfAbsent(TENANT, stage.id(), stage.object()));
      } finally {
        Files.delete(objects);
      }
      assertEquals("preserve", Files.readString(outside.resolve("sentinel")));
      assertEquals(0, stages(TENANT));
    }
  }

  @Test
  @EnabledOnOs(OS.LINUX)
  void linuxSymlinkDirectoriesAndFilesAreRejectedWithoutFollowingTargets() throws Exception {
    Path outside = Files.createDirectory(root.resolve("outside"));
    Path sentinel = Files.write(outside.resolve("sentinel"), BYTES);
    Path link = Files.createSymbolicLink(root.resolve("symlink"), outside);
    fails("resource.storage_unavailable", () -> new LocalObjectStore(link));
    try (var store = new LocalObjectStore(root)) {
      var stage = stage(store, TENANT);
      Files.createDirectory(tenant(TENANT).resolve("objects"));
      Files.createSymbolicLink(committed(TENANT, stage.object().digest()), sentinel);
      fails(
          "resource.storage_unavailable",
          () -> store.commitIfAbsent(TENANT, stage.id(), stage.object()));
      fails("resource.storage_unavailable", () -> store.stat(TENANT, digest(BYTES)));
      assertArrayEquals(BYTES, Files.readAllBytes(sentinel));
      assertEquals(0, stages(TENANT));
    }
  }

  Process stageProcess() throws IOException {
    String executable =
        Path.of(
                System.getProperty("java.home"),
                "bin",
                System.getProperty("os.name").startsWith("Windows") ? "java.exe" : "java")
            .toString();
    return new ProcessBuilder(
            executable,
            "-cp",
            System.getProperty("surefire.test.class.path", System.getProperty("java.class.path")),
            StageProcess.class.getName(),
            root.toString(),
            TENANT.toString(),
            START.toString())
        .start();
  }

  int cleanupInChild(Instant now) throws Exception {
    String executable =
        Path.of(
                System.getProperty("java.home"),
                "bin",
                System.getProperty("os.name").startsWith("Windows") ? "java.exe" : "java")
            .toString();
    Process child =
        new ProcessBuilder(
                executable,
                "-cp",
                System.getProperty(
                    "surefire.test.class.path", System.getProperty("java.class.path")),
                CleanerProcess.class.getName(),
                root.toString(),
                TENANT.toString(),
                now.toString())
            .start();
    try {
      assertTrue(child.waitFor(15, TimeUnit.SECONDS), "Cleaner process timeout");
      assertEquals(
          0,
          child.exitValue(),
          new String(child.getErrorStream().readAllBytes(), StandardCharsets.UTF_8));
      String result =
          new String(child.getInputStream().readAllBytes(), StandardCharsets.UTF_8).trim();
      assertTrue(result.matches("CLEANED=[0-9]+"), result);
      return Integer.parseInt(result.substring("CLEANED=".length()));
    } finally {
      if (child.isAlive()) {
        child.destroyForcibly();
        assertTrue(child.waitFor(15, TimeUnit.SECONDS));
      }
    }
  }

  static void junction(Path link, Path target) throws Exception {
    Process process =
        new ProcessBuilder("cmd.exe", "/c", "mklink", "/J", link.toString(), target.toString())
            .redirectErrorStream(true)
            .start();
    try {
      assertTrue(process.waitFor(10, TimeUnit.SECONDS));
      assertEquals(
          0,
          process.exitValue(),
          new String(process.getInputStream().readAllBytes(), StandardCharsets.UTF_8));
      assertTrue(
          Files.readAttributes(link, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS)
              .isOther());
    } finally {
      if (process.isAlive()) {
        process.destroyForcibly();
        assertTrue(process.waitFor(10, TimeUnit.SECONDS));
      }
    }
  }

  public static class StageProcess {
    public static void main(String[] args) throws Exception {
      var store =
          new LocalObjectStore(
              Path.of(args[0]),
              Clock.fixed(Instant.parse(args[2]), ZoneOffset.UTC),
              Duration.ofHours(24));
      var stage = stage(store, UUID.fromString(args[1]));
      System.out.println(stage.id().value());
      System.out.flush();
      System.in.read();
      // Simulate an exited uploader: no application close; OS releases the lease, disk stage
      // remains.
    }
  }

  public static class CleanerProcess {
    public static void main(String[] args) {
      try (var store =
          new LocalObjectStore(
              Path.of(args[0]),
              Clock.fixed(Instant.parse(args[2]), ZoneOffset.UTC),
              Duration.ofHours(24))) {
        System.out.println("CLEANED=" + store.cleanupExpiredStaging(UUID.fromString(args[1])));
      }
    }
  }

  static class TrackingInput extends ByteArrayInputStream {
    boolean closed;

    TrackingInput(byte[] bytes) {
      super(bytes);
    }

    @Override
    public void close() throws IOException {
      closed = true;
      super.close();
    }
  }

  static class PatternInput extends InputStream {
    final long total;
    long readBytes;
    int maxRequest;
    boolean closed;

    PatternInput(long total) {
      this.total = total;
    }

    @Override
    public int available() {
      return 0;
    }

    @Override
    public int read() {
      if (readBytes == total) return -1;
      readBytes++;
      return 42;
    }

    @Override
    public int read(byte[] bytes, int offset, int length) {
      maxRequest = Math.max(maxRequest, length);
      if (readBytes == total) return -1;
      int count = (int) Math.min(length, total - readBytes);
      Arrays.fill(bytes, offset, offset + count, (byte) 42);
      readBytes += count;
      return count;
    }

    @Override
    public void close() {
      closed = true;
    }
  }

  static class MutableClock extends Clock {
    volatile Instant now;

    MutableClock(Instant now) {
      this.now = now;
    }

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
