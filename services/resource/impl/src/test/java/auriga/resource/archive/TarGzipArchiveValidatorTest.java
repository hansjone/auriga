package auriga.resource.archive;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.resource.objectstore.*;
import auriga.resource.objectstore.ObjectStore.*;
import auriga.resource.web.model.UploadMetadataManifest.OsEnum;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.zip.Deflater;
import org.apache.commons.compress.archivers.tar.*;
import org.apache.commons.compress.compressors.gzip.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;

class TarGzipArchiveValidatorTest {
  static final UUID TENANT = UUID.fromString("01900000-0000-7000-8000-000000000001");
  static final int PREFIX = 345;
  @TempDir Path root;
  LocalObjectStore store;
  TarGzipArchiveValidator validator;

  @BeforeEach
  void setup() {
    store = new LocalObjectStore(root);
    validator = new TarGzipArchiveValidator(store);
  }

  @AfterEach
  void close() {
    store.close();
  }

  record Part(String name, boolean directory, byte[] bytes) {}

  static Part file(String name) {
    return new Part(name, false, new byte[] {1, 2, 3});
  }

  static Part dir(String name) {
    return new Part(name, true, new byte[0]);
  }

  static TarArchiveEntry entry(String name, boolean directory, long size) {
    var entry =
        new TarArchiveEntry(name, directory ? TarConstants.LF_DIR : TarConstants.LF_NORMAL, true);
    entry.setUserId(0);
    entry.setGroupId(0);
    entry.setModTime(0);
    entry.setSize(size);
    return entry;
  }

  static byte[] tar(Part... parts) throws IOException {
    var bytes = new ByteArrayOutputStream();
    try (var tar = new TarArchiveOutputStream(bytes, 512, "UTF-8")) {
      for (var part : parts) {
        tar.putArchiveEntry(entry(part.name(), part.directory(), part.bytes().length));
        tar.write(part.bytes());
        tar.closeArchiveEntry();
      }
    }
    return bytes.toByteArray();
  }

  static byte[] gzip(byte[] tar) throws IOException {
    return gzip(tar, null, false);
  }

  static byte[] gzip(byte[] tar, String comment, boolean headerCrc) throws IOException {
    var bytes = new ByteArrayOutputStream();
    var parameters = new GzipParameters();
    parameters.setModificationTime(0);
    parameters.setComment(comment);
    parameters.setHeaderCRC(headerCrc);
    try (var gzip = new GzipCompressorOutputStream(bytes, parameters)) {
      gzip.write(tar);
    }
    return bytes.toByteArray();
  }

  Staged stage(byte[] bytes) {
    return store.putStaged(TENANT, new ByteArrayInputStream(bytes), ObjectStore.MAX_BYTES);
  }

  TarGzipArchiveValidator.Result validate(byte[] bytes, OsEnum os) {
    var stage = stage(bytes);
    return validator.validate(TENANT, stage.id(), stage.object(), stage.object().digest(), os);
  }

  static AurigaException failure(String code, org.junit.jupiter.api.function.Executable action) {
    var failure = assertThrows(AurigaException.class, action);
    assertEquals(code, failure.errorCode().code());
    assertTrue(failure.params().isEmpty());
    assertEquals(code, failure.getMessage());
    return failure;
  }

  void invalid(byte[] bytes) {
    failure("common.validation_failed", () -> validate(bytes, OsEnum.LINUX));
  }

  static byte[] concat(byte[]... values) throws IOException {
    var bytes = new ByteArrayOutputStream();
    for (var value : values) bytes.write(value);
    return bytes.toByteArray();
  }

  static byte[] header(String name, boolean directory, long size) throws IOException {
    byte[] header = new byte[512];
    entry("safe", directory, size).writeEntryHeader(header);
    Arrays.fill(header, 0, 100, (byte) 0);
    byte[] encoded = name.getBytes(StandardCharsets.UTF_8);
    assertTrue(encoded.length <= 100);
    System.arraycopy(encoded, 0, header, 0, encoded.length);
    checksum(header);
    return header;
  }

  static void checksum(byte[] header) {
    Arrays.fill(
        header,
        TarConstants.CHKSUM_OFFSET,
        TarConstants.CHKSUM_OFFSET + TarConstants.CHKSUMLEN,
        (byte) ' ');
    TarUtils.formatCheckSumOctalBytes(
        TarUtils.computeCheckSum(header),
        header,
        TarConstants.CHKSUM_OFFSET,
        TarConstants.CHKSUMLEN);
  }

  static byte[] rawTar(byte[] header, byte[] body, int eofRecords) throws IOException {
    return concat(
        header, body, new byte[(512 - body.length % 512) % 512], new byte[eofRecords * 512]);
  }

  @Test
  void verifiesRealStagedBytesWithoutCommitAbortOrManifestLayoutRules() throws Exception {
    byte[] bytes =
        gzip(
            tar(
                dir("./"),
                dir("code/"),
                new Part(
                    "code/plugin.js", false, "never executed".getBytes(StandardCharsets.UTF_8))));
    var staged = stage(bytes);
    var result =
        validator.validate(
            TENANT, staged.id(), staged.object(), staged.object().digest(), OsEnum.ANY);
    assertEquals(staged.object(), result.object());
    assertEquals(3, result.entries());
    assertEquals(1, result.files());
    assertEquals(2, result.directories());
    assertEquals(14, result.fileBytes());
    assertEquals(
        tar(
                dir("./"),
                dir("code/"),
                new Part(
                    "code/plugin.js", false, "never executed".getBytes(StandardCharsets.UTF_8)))
            .length,
        result.expandedBytes());
    failure("common.not_found", () -> store.stat(TENANT, staged.object().digest()));
    try (var raw = store.openStaged(TENANT, staged.id())) {
      assertArrayEquals(bytes, raw.readAllBytes());
    }
    // Only the caller, after its other checks, can perform publication.
    assertEquals(staged.object(), store.commitIfAbsent(TENANT, staged.id(), staged.object()));
  }

  @Test
  void acceptsEmptyUstarAndCompleteZeroRecordPadding() throws Exception {
    var empty = validate(gzip(new byte[1024]), OsEnum.LINUX);
    assertEquals(0, empty.entries());
    assertEquals(1024, empty.expandedBytes());
    byte[] tar = tar(file("a"));
    assertEquals(
        tar.length + 1024,
        validate(gzip(concat(tar, new byte[1024])), OsEnum.LINUX).expandedBytes());
  }

  @Test
  void libraryHeaderGateRejectsExtensionsBeforeAnyMetadataBodyRead() throws Exception {
    for (byte flag :
        new byte[] {'x', 'g', 'X', 'L', 'K', 'S', '1', '2', '3', '4', '6', '7', 'D', 'Z'}) {
      byte[] record = header("extension", false, TarGzipArchiveValidator.MAX_EXPANDED);
      record[TarConstants.LF_OFFSET] = flag;
      checksum(record);
      var input =
          new ByteArrayInputStream(record) {
            @Override
            public synchronized int read(byte[] b, int off, int len) {
              if (pos >= 512) throw new AssertionError("Extension body must not be read");
              return super.read(b, off, len);
            }
          };
      try (var tar = new StrictUstarInputStream(input, false)) {
        assertThrows(IOException.class, tar::getNextEntry);
      }
      invalid(gzip(concat(record, new byte[1024])));
    }
  }

  @Test
  void requiresPosixMagicVersionOctalFieldsChecksumAndNoStarTail() throws Exception {
    for (int offset : new int[] {0, 100, 108, 116, 124, 136, 148, 257, 263, 329, 337, 508}) {
      byte[] record = header("valid", false, 0);
      record[offset] = (byte) (offset == 124 ? 0x80 : '9');
      if (offset != 0 && offset != 148) checksum(record);
      invalid(gzip(rawTar(record, new byte[0], 2)));
    }
    byte[] record = header("valid", false, 0);
    TarUtils.formatLongOctalBytes(TarGzipArchiveValidator.MAX_EXPANDED + 1, record, 124, 12);
    checksum(record);
    invalid(gzip(rawTar(record, new byte[0], 2)));
    record = header("valid", true, 1);
    invalid(gzip(rawTar(record, new byte[] {0}, 2)));
  }

  @Test
  void validatesUstarPrefixAndUtf8WithoutReplacingBytes() throws Exception {
    String leaf = "文档.txt", prefix = "目录";
    byte[] record = header(leaf, false, 0);
    TarUtils.formatNameBytes(prefix, record, PREFIX, 155);
    checksum(record);
    assertEquals(1, validate(gzip(rawTar(record, new byte[0], 2)), OsEnum.LINUX).files());
    record = header("safe", false, 0);
    record[0] = (byte) 0xc3;
    record[1] = 0x28;
    checksum(record);
    invalid(gzip(rawTar(record, new byte[0], 2)));
    record = header("a", false, 0);
    record[2] = 'b';
    checksum(record);
    invalid(gzip(rawTar(record, new byte[0], 2)));
    record = header("safe", false, 0);
    TarUtils.formatNameBytes("../outside", record, PREFIX, 155);
    checksum(record);
    invalid(gzip(rawTar(record, new byte[0], 2)));
  }

  @Test
  void rejectsUnsafePathsWithoutHostNormalizationOrInputDisclosure() throws Exception {
    for (String name :
        List.of(
            "../secret",
            "a/../b",
            "/outside",
            "C:/outside",
            "./C:/outside",
            "\\\\server\\share",
            "a\\b",
            "a/./../b",
            "x\nvalue",
            "x\u0001value",
            "x\u007fvalue",
            "x\u0085value",
            "")) {
      byte[] bytes = gzip(rawTar(header(name, false, 0), new byte[0], 2));
      var error = failure("common.validation_failed", () -> validate(bytes, OsEnum.LINUX));
      assertNull(error.getCause());
    }
    invalid(gzip(rawTar(header("file/", false, 0), new byte[0], 2)));
    invalid(gzip(rawTar(header(".", false, 0), new byte[0], 2)));
  }

  @Test
  void normalizesDotSlashesAndNfcForDuplicateAndPrefixConflictChecks() throws Exception {
    for (Part[] parts :
        List.of(
            new Part[] {file("a"), file("./a")},
            new Part[] {file("a/b"), file("a//b")},
            new Part[] {file("caf\u00e9"), file("cafe\u0301")},
            new Part[] {dir("a/"), dir("./a/")},
            new Part[] {file("a"), file("a/b")},
            new Part[] {file("a/b"), file("a")},
            new Part[] {dir("./"), dir(".")})) invalid(gzip(tar(parts)));
    assertEquals(2, validate(gzip(tar(file("./a/b"), dir("a"))), OsEnum.LINUX).entries());
    assertEquals(2, validate(gzip(tar(dir("a/"), file("a/b"))), OsEnum.LINUX).entries());
    assertEquals(1, validate(gzip(tar(file("a//./b"))), OsEnum.LINUX).entries());
  }

  @Test
  void windowsAndAnyRejectDeviceAdsTrailingAndCaseAliasesWhileLinuxKeepsCase() throws Exception {
    for (OsEnum os : List.of(OsEnum.WINDOWS, OsEnum.ANY)) {
      for (String name :
          List.of(
              "CON",
              "con.txt",
              "NUL .txt",
              "COM1.js",
              "LPT9",
              "COM¹",
              "CONOUT$",
              "dir/file:stream",
              "a.",
              "a ",
              "a<b",
              "a|b",
              "a?b",
              "a*b")) {
        byte[] bytes = gzip(rawTar(header(name, false, 0), new byte[0], 2));
        failure("common.validation_failed", () -> validate(bytes, os));
      }
      byte[] aliases = gzip(tar(file("Readme"), file("README")));
      failure("common.validation_failed", () -> validate(aliases, os));
    }
    assertEquals(2, validate(gzip(tar(file("Readme"), file("README"))), OsEnum.LINUX).files());
    assertEquals(
        3,
        validate(gzip(tar(file("AUX"), file("dir/file:stream"), file("a."))), OsEnum.LINUX)
            .files());
  }

  @Test
  void requiresCompleteDataTwoZeroEofRecordsAndZeroAlignedPadding() throws Exception {
    byte[] tar = tar(file("a"));
    for (int remove : new int[] {1, 512, 1024, 1025, tar.length - 512})
      invalid(gzip(Arrays.copyOf(tar, tar.length - remove)));
    byte[] nonzeroPadding = tar.clone();
    nonzeroPadding[515] = 1;
    invalid(gzip(nonzeroPadding));
    byte[] nonzeroEof = tar.clone();
    nonzeroEof[tar.length - 512] = 1;
    invalid(gzip(nonzeroEof));
    invalid(gzip(concat(tar, new byte[] {1})));
    invalid(gzip(concat(tar, new byte[1])));
    invalid(gzip(new byte[512]));
    invalid(gzip(new byte[0]));
  }

  @Test
  void rejectsGzipCrcIsizeTruncationExtraMembersAndRawTrailingBytes() throws Exception {
    byte[] bytes = gzip(tar(file("a")));
    for (int length : new int[] {1, bytes.length / 2, bytes.length - 1, bytes.length - 8})
      invalid(Arrays.copyOf(bytes, length));
    byte[] crc = bytes.clone();
    crc[crc.length - 8] ^= 1;
    invalid(crc);
    byte[] size = bytes.clone();
    size[size.length - 4] ^= 1;
    invalid(size);
    invalid(concat(bytes, bytes));
    invalid(concat(bytes, gzip(new byte[0])));
    invalid(concat(bytes, new byte[] {0}));
    invalid(concat(bytes, new byte[] {42}));
    byte[] reserved = bytes.clone();
    reserved[3] = (byte) 0xe0;
    invalid(reserved);
    byte[] method = bytes.clone();
    method[2] = 0;
    invalid(method);
  }

  @Test
  void jdkChecksOptionalHeaderCrcAndBothHeaderPassesHaveBoundedMetadata() throws Exception {
    byte[] tar = tar(file("a"));
    byte[] crc = gzip(tar, null, true);
    assertEquals(1, validate(crc, OsEnum.LINUX).files());
    crc = crc.clone();
    crc[10] ^= 1;
    invalid(crc);
    assertEquals(
        1,
        validate(
                gzip(tar, "p".repeat(TarGzipArchiveValidator.MAX_GZIP_HEADER - 11), false),
                OsEnum.LINUX)
            .files());
    invalid(gzip(tar, "p".repeat(TarGzipArchiveValidator.MAX_GZIP_HEADER - 10), false));
  }

  @Test
  void compressionRatioAllowsExact100AndRejectsOneCompressedByteLess() throws Exception {
    byte[] tar = tar(new Part("a", false, new byte[25600 - 1536]));
    assertEquals(25600, tar.length);
    int base = gzip(tar).length;
    assertTrue(base < 255);
    byte[] exact = gzip(tar, "p".repeat(256 - base - 1), false);
    assertEquals(256, exact.length);
    assertEquals(25600, validate(exact, OsEnum.LINUX).expandedBytes());
    byte[] less = gzip(tar, "p".repeat(255 - base - 1), false);
    assertEquals(255, less.length);
    invalid(less);
  }

  @Test
  void physicalEntryLimitCountsDirectoriesAndAcceptsExactly4096() throws Exception {
    byte[] noise = new byte[65536];
    new Random(7).nextBytes(noise);
    var parts = new ArrayList<Part>();
    parts.add(new Part("noise", false, noise));
    for (int i = 1; i < 4096; i++) parts.add(dir("d" + i));
    assertEquals(4096, validate(gzip(tar(parts.toArray(Part[]::new))), OsEnum.LINUX).entries());
    parts.add(dir("overflow"));
    invalid(gzip(tar(parts.toArray(Part[]::new))));
  }

  @Test
  void expandedBudgetCountsTheWholeStreamIncludingHeadersAndEof() throws Exception {
    Path exact =
        largeGzip(
            "expanded-exact.gz",
            TarGzipArchiveValidator.MAX_EXPANDED,
            Deflater.DEFAULT_COMPRESSION,
            null,
            3 * 1024 * 1024);
    var staged = store.putStaged(TENANT, Files.newInputStream(exact), ObjectStore.MAX_BYTES);
    assertEquals(
        TarGzipArchiveValidator.MAX_EXPANDED,
        validator
            .validate(TENANT, staged.id(), staged.object(), staged.object().digest(), OsEnum.LINUX)
            .expandedBytes());
    Path over =
        largeGzip(
            "expanded-over.gz",
            TarGzipArchiveValidator.MAX_EXPANDED + 512,
            Deflater.DEFAULT_COMPRESSION,
            null,
            3 * 1024 * 1024);
    var invalid = store.putStaged(TENANT, Files.newInputStream(over), ObjectStore.MAX_BYTES);
    failure(
        "common.validation_failed",
        () ->
            validator.validate(
                TENANT, invalid.id(), invalid.object(), invalid.object().digest(), OsEnum.LINUX));
  }

  @Test
  void acceptsExactly64MiBCompressedAndRawCounterRejectsAnyExtraByte() throws Exception {
    long expanded = ObjectStore.MAX_BYTES - 65536;
    Path initial = largeGzip("raw-initial.gz", expanded, Deflater.NO_COMPRESSION, null, 0);
    long extra = ObjectStore.MAX_BYTES - Files.size(initial);
    assertTrue(extra > 0 && extra < 65536);
    Path exact =
        largeGzip(
            "raw-exact.gz", expanded, Deflater.NO_COMPRESSION, "p".repeat((int) extra - 1), 0);
    assertEquals(ObjectStore.MAX_BYTES, Files.size(exact));
    Files.delete(initial);
    var staged = store.putStaged(TENANT, Files.newInputStream(exact), ObjectStore.MAX_BYTES);
    assertEquals(
        expanded,
        validator
            .validate(TENANT, staged.id(), staged.object(), staged.object().digest(), OsEnum.LINUX)
            .expandedBytes());
    byte[] buffer = new byte[65536];
    var bounded = new TarGzipArchiveValidator.RawInput(new ZeroInput(ObjectStore.MAX_BYTES));
    while (bounded.read(buffer) != -1) {}
    assertEquals(ObjectStore.MAX_BYTES, bounded.count);
    var overflow = new TarGzipArchiveValidator.RawInput(new ZeroInput(ObjectStore.MAX_BYTES + 1));
    assertThrows(
        IOException.class,
        () -> {
          while (overflow.read(buffer) != -1) {}
        });
  }

  @Test
  void digestSizeAndInputValidationCannotCommitOrConsumeTheStage() throws Exception {
    var staged = stage(gzip(tar(file("a"))));
    String other = "sha256:" + "0".repeat(64);
    failure(
        "resource.digest_mismatch",
        () -> validator.validate(TENANT, staged.id(), staged.object(), other, OsEnum.LINUX));
    failure(
        "resource.digest_mismatch",
        () ->
            validator.validate(
                TENANT,
                staged.id(),
                new ObjectInfo(other, staged.object().sizeBytes()),
                other,
                OsEnum.LINUX));
    failure(
        "resource.digest_mismatch",
        () ->
            validator.validate(
                TENANT,
                staged.id(),
                new ObjectInfo(staged.object().digest(), staged.object().sizeBytes() + 1),
                staged.object().digest(),
                OsEnum.LINUX));
    for (String bad : List.of("../secret", "sha256:" + "A".repeat(64), ""))
      failure(
          "common.validation_failed",
          () -> validator.validate(TENANT, staged.id(), staged.object(), bad, OsEnum.LINUX));
    failure(
        "common.validation_failed",
        () ->
            validator.validate(
                TENANT,
                staged.id(),
                new ObjectInfo(staged.object().digest(), ObjectStore.MAX_BYTES + 1),
                staged.object().digest(),
                OsEnum.LINUX));
    failure(
        "common.validation_failed",
        () ->
            validator.validate(
                TENANT, staged.id(), staged.object(), staged.object().digest(), null));
    try (var raw = store.openStaged(TENANT, staged.id())) {
      assertEquals(31, raw.read());
    }
    failure("common.not_found", () -> store.stat(TENANT, staged.object().digest()));
  }

  @Test
  void closesOnlyItsObtainedStreamAndKeepsFormatSeparateFromStorageFailures() throws Exception {
    var staged = stage(gzip(tar(file("a"))));
    for (boolean closeFailure : List.of(false, true)) {
      var observed = new ObservedStore(store, !closeFailure, closeFailure);
      failure(
          "resource.storage_unavailable",
          () ->
              new TarGzipArchiveValidator(observed)
                  .validate(
                      TENANT, staged.id(), staged.object(), staged.object().digest(), OsEnum.ANY));
      assertEquals(1, observed.opened);
      assertEquals(1, observed.closed);
      assertEquals(0, observed.mutations);
      try (var raw = store.openStaged(TENANT, staged.id())) {
        assertEquals(31, raw.read());
      }
    }
    var bad = stage(new byte[] {1, 2, 3});
    var observed = new ObservedStore(store, false, false);
    failure(
        "common.validation_failed",
        () ->
            new TarGzipArchiveValidator(observed)
                .validate(TENANT, bad.id(), bad.object(), bad.object().digest(), OsEnum.LINUX));
    assertEquals(1, observed.closed);
    assertEquals(0, observed.mutations);
  }

  Path largeGzip(String name, long expanded, int level, String comment, int noisyBytes)
      throws Exception {
    Path path = root.resolve(name);
    var parameters = new GzipParameters();
    parameters.setCompressionLevel(level);
    parameters.setModificationTime(0);
    parameters.setComment(comment);
    try (var gzip = new GzipCompressorOutputStream(Files.newOutputStream(path), parameters);
        var tar = new TarArchiveOutputStream(gzip, 512, "UTF-8")) {
      long size = expanded - 1536;
      tar.putArchiveEntry(entry("a", false, size));
      byte[] buffer = new byte[65536];
      var random = new Random(13);
      for (long written = 0; written < size; ) {
        int n = (int) Math.min(buffer.length, size - written);
        if (written < noisyBytes) random.nextBytes(buffer);
        else Arrays.fill(buffer, (byte) 0);
        tar.write(buffer, 0, n);
        written += n;
      }
      tar.closeArchiveEntry();
    }
    return path;
  }

  static final class ZeroInput extends InputStream {
    long remaining;

    ZeroInput(long size) {
      remaining = size;
    }

    @Override
    public int read() {
      if (remaining == 0) return -1;
      remaining--;
      return 0;
    }

    @Override
    public int read(byte[] b, int off, int len) {
      if (remaining == 0) return -1;
      int n = (int) Math.min(len, remaining);
      Arrays.fill(b, off, off + n, (byte) 0);
      remaining -= n;
      return n;
    }
  }

  static final class ObservedStore implements ObjectStore {
    final ObjectStore delegate;
    final boolean failRead, failClose;
    int opened, closed, mutations;

    ObservedStore(ObjectStore delegate, boolean failRead, boolean failClose) {
      this.delegate = delegate;
      this.failRead = failRead;
      this.failClose = failClose;
    }

    @Override
    public InputStream openStaged(UUID tenant, StageId id) {
      opened++;
      return new FilterInputStream(delegate.openStaged(tenant, id)) {
        @Override
        public int read(byte[] b, int off, int len) throws IOException {
          if (failRead) throw new IOException("Private source detail");
          return super.read(b, off, len);
        }

        @Override
        public void close() throws IOException {
          closed++;
          super.close();
          if (failClose) throw new IOException("Private close detail");
        }
      };
    }

    @Override
    public Staged putStaged(UUID t, InputStream in, long max) {
      throw new AssertionError("Validator cannot stage");
    }

    @Override
    public ObjectInfo commitIfAbsent(UUID t, StageId id, ObjectInfo info) {
      mutations++;
      throw new AssertionError("Validator cannot commit");
    }

    @Override
    public void abort(UUID t, StageId id) {
      mutations++;
      throw new AssertionError("Validator cannot abort");
    }

    @Override
    public ObjectInfo stat(UUID t, String digest) {
      throw new AssertionError("Validator cannot stat committed data");
    }

    @Override
    public InputStream open(UUID t, ObjectInfo info) {
      throw new AssertionError("Validator cannot open committed data");
    }

    @Override
    public int cleanupExpiredStaging(UUID t) {
      throw new AssertionError("Validator cannot clean");
    }

    @Override
    public void close() {
      mutations++;
      throw new AssertionError("Validator cannot close the store");
    }
  }
}
