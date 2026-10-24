package auriga.resource.archive;

import auriga.common.error.*;
import auriga.resource.objectstore.ObjectStore;
import auriga.resource.objectstore.ObjectStore.*;
import auriga.resource.web.model.UploadMetadataManifest.OsEnum;
import io.grpc.Status;
import java.io.*;
import java.security.*;
import java.util.*;
import java.util.zip.GZIPInputStream;
import org.apache.commons.compress.compressors.gzip.GzipCompressorInputStream;

/** Internal single-gzip/POSIX-ustar subset; no extraction, manifest invention or stage mutation. */
public final class TarGzipArchiveValidator {
  static final long MAX_EXPANDED = 256L * 1024 * 1024;
  static final int MAX_ENTRIES = 4096;
  static final int MAX_GZIP_HEADER = 65536;
  private static final ErrorCode DIGEST =
      ErrorCode.of("resource.digest_mismatch", 422, Status.Code.INVALID_ARGUMENT);
  private static final ErrorCode STORAGE =
      ErrorCode.of("resource.storage_unavailable", 503, Status.Code.UNAVAILABLE);
  private final ObjectStore store;

  public TarGzipArchiveValidator(ObjectStore store) {
    this.store = Objects.requireNonNull(store);
  }

  public record Result(
      ObjectInfo object,
      OsEnum targetOs,
      int entries,
      int files,
      int directories,
      long expandedBytes,
      long fileBytes) {}

  public Result validate(
      UUID tenant, StageId stage, ObjectInfo object, String expectedDigest, OsEnum targetOs) {
    if (tenant == null
        || stage == null
        || stage.value() == null
        || object == null
        || targetOs == null
        || object.sizeBytes() < 0
        || object.sizeBytes() > ObjectStore.MAX_BYTES
        || !digest(object.digest())
        || !digest(expectedDigest)) throw failure(CommonErrors.VALIDATION_FAILED);
    if (!object.digest().equals(expectedDigest)) throw failure(DIGEST);
    try (InputStream owned = store.openStaged(tenant, stage)) {
      RawInput raw = new RawInput(owned);
      try (BufferedInputStream buffered = new BufferedInputStream(raw, 8192)) {
        // Commons Compress 1.28 skips optional FHCRC. JDK verifies it, using the same bounded
        // marked header bytes. Neither decoder is replaced by application gzip parsing.
        buffered.mark(MAX_GZIP_HEADER + 1);
        try (var headerCheck = new GZIPInputStream(new HeaderInput(buffered), 8192)) {
          /* header only */
        }
        buffered.reset();
        HeaderInput header = new HeaderInput(buffered);
        try (var gzip =
            GzipCompressorInputStream.builder()
                .setInputStream(header)
                .setDecompressConcatenated(false)
                .get()) {
          header.complete = true;
          ExpandedInput expanded =
              new ExpandedInput(gzip, Math.min(MAX_EXPANDED, object.sizeBytes() * 100L));
          try (var tar = new StrictUstarInputStream(expanded, targetOs != OsEnum.LINUX)) {
            byte[] bytes = new byte[65536];
            long payload = 0;
            for (var entry = tar.getNextEntry(); entry != null; entry = tar.getNextEntry()) {
              long size = 0;
              for (int count; (count = tar.read(bytes)) != -1; ) {
                size += count;
                payload += count;
              }
              if (size != entry.getSize()) throw StrictUstarInputStream.bad();
              expanded.paddingRemaining =
                  (StrictUstarInputStream.RECORD - size % StrictUstarInputStream.RECORD)
                      % StrictUstarInputStream.RECORD;
            }
            if (tar.zeroRecords != 2 || expanded.paddingRemaining != 0)
              throw StrictUstarInputStream.bad();
            // Consume all tar zero padding and the gzip trailer, including CRC32 and ISIZE.
            for (int count; (count = expanded.read(bytes)) != -1; )
              for (int i = 0; i < count; i++) if (bytes[i] != 0) throw StrictUstarInputStream.bad();
            if (expanded.count % StrictUstarInputStream.RECORD != 0 || buffered.read() != -1)
              throw StrictUstarInputStream.bad();
            String actual = "sha256:" + HexFormat.of().formatHex(raw.sha.digest());
            if (raw.count != object.sizeBytes() || !actual.equals(expectedDigest))
              throw failure(DIGEST);
            if (expanded.count > raw.count * 100L) throw StrictUstarInputStream.bad();
            return new Result(
                object, targetOs, tar.entries, tar.files, tar.directories, expanded.count, payload);
          }
        }
      } catch (IOException e) {
        throw failure(storageRead(e) ? STORAGE : CommonErrors.VALIDATION_FAILED);
      } catch (IllegalArgumentException e) {
        throw failure(CommonErrors.VALIDATION_FAILED);
      }
    } catch (IOException e) {
      throw failure(STORAGE);
    }
  }

  private static boolean digest(String value) {
    return value != null && value.matches("sha256:[0-9a-f]{64}");
  }

  private static AurigaException failure(ErrorCode code) {
    return new AurigaException(code);
  }

  private static boolean storageRead(Throwable error) {
    for (int i = 0; error != null && i < 16; i++, error = error.getCause())
      if (error instanceof StorageRead) return true;
    return false;
  }

  private static final class StorageRead extends IOException {
    StorageRead() {
      super("Storage read failed");
    }
  }

  /** Counts/hashes each original byte once, below the decoder's bounded mark/reset buffer. */
  static final class RawInput extends InputStream {
    final InputStream source;
    final MessageDigest sha;
    long count;

    RawInput(InputStream source) {
      this.source = source;
      try {
        sha = MessageDigest.getInstance("SHA-256");
      } catch (NoSuchAlgorithmException e) {
        throw new IllegalStateException(e);
      }
    }

    @Override
    public int read() throws IOException {
      byte[] one = new byte[1];
      return read(one) == -1 ? -1 : one[0] & 255;
    }

    @Override
    public int read(byte[] bytes, int offset, int length) throws IOException {
      if (length == 0) return 0;
      int read;
      try {
        read =
            source.read(bytes, offset, (int) Math.min(length, ObjectStore.MAX_BYTES - count + 1));
      } catch (IOException e) {
        throw new StorageRead();
      }
      if (read > 0) {
        count += read;
        if (count > ObjectStore.MAX_BYTES) throw StrictUstarInputStream.bad();
        sha.update(bytes, offset, read);
      }
      return read;
    }
    // close deliberately does not close the outer owned stream a second time.
  }

  /** Bounds both libraries' header processing without interpreting header fields. */
  private static final class HeaderInput extends InputStream {
    final InputStream source;
    long headerBytes;
    boolean complete;

    HeaderInput(InputStream source) {
      this.source = source;
    }

    @Override
    public int read() throws IOException {
      int value = source.read();
      if (value >= 0) add(1);
      return value;
    }

    @Override
    public int read(byte[] bytes, int offset, int length) throws IOException {
      int read =
          source.read(
              bytes,
              offset,
              complete ? length : (int) Math.min(length, MAX_GZIP_HEADER - headerBytes + 1));
      if (read > 0) add(read);
      return read;
    }

    private void add(int n) throws IOException {
      if (!complete && (headerBytes += n) > MAX_GZIP_HEADER) throw StrictUstarInputStream.bad();
    }

    @Override
    public boolean markSupported() {
      return source.markSupported();
    }

    @Override
    public void mark(int limit) {
      source.mark(limit);
    }

    @Override
    public void reset() throws IOException {
      source.reset();
    }
    // InputStream.skip reads through this wrapper, so headers can't bypass its cap.
  }

  /** Counts all inflated bytes, including library skip; only alignment padding may be skipped. */
  static final class ExpandedInput extends InputStream {
    final InputStream source;
    final long limit;
    long count;
    long paddingRemaining;

    ExpandedInput(InputStream source, long limit) {
      this.source = source;
      this.limit = limit;
    }

    @Override
    public int read() throws IOException {
      byte[] one = new byte[1];
      return read(one) == -1 ? -1 : one[0] & 255;
    }

    @Override
    public int read(byte[] bytes, int offset, int length) throws IOException {
      if (length == 0) return 0;
      int read = source.read(bytes, offset, (int) Math.min(length, limit - count + 1));
      if (read > 0 && (count += read) > limit) throw StrictUstarInputStream.bad();
      if (read > 0 && paddingRemaining > 0) {
        int padding = (int) Math.min(read, paddingRemaining);
        for (int i = 0; i < padding; i++)
          if (bytes[offset + i] != 0) throw StrictUstarInputStream.bad();
        paddingRemaining -= padding;
      }
      return read;
    }

    @Override
    public long skip(long size) throws IOException {
      if (size <= 0) return 0;
      byte[] bytes = new byte[(int) Math.min(size, 8192)];
      long skipped = 0;
      while (skipped < size) {
        int read = read(bytes, 0, (int) Math.min(bytes.length, size - skipped));
        if (read < 0) break;
        skipped += read;
      }
      return skipped;
    }
  }
}
