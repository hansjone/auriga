package auriga.resource.archive;

import java.io.*;
import java.nio.ByteBuffer;
import java.nio.charset.*;
import java.util.Arrays;
import org.apache.commons.compress.archivers.tar.*;
import org.apache.commons.compress.archivers.zip.ZipEncoding;
import org.apache.commons.compress.utils.ArchiveUtils;

/** A policy gate at the library's raw-record hook, BEFORE its PAX/GNU/sparse processing. */
final class StrictUstarInputStream extends TarArchiveInputStream {
  static final int RECORD = 512;
  private static final int PREFIX =
      TarConstants.MAGIC_OFFSET
          + TarConstants.MAGICLEN
          + TarConstants.VERSIONLEN
          + TarConstants.UNAMELEN
          + TarConstants.GNAMELEN
          + 2 * TarConstants.DEVLEN;
  private static final ZipEncoding UTF8 =
      new ZipEncoding() {
        @Override
        public boolean canEncode(String name) {
          return StandardCharsets.UTF_8.newEncoder().canEncode(name);
        }

        @Override
        public ByteBuffer encode(String name) throws IOException {
          return StandardCharsets.UTF_8
              .newEncoder()
              .onMalformedInput(CodingErrorAction.REPORT)
              .onUnmappableCharacter(CodingErrorAction.REPORT)
              .encode(java.nio.CharBuffer.wrap(name));
        }

        @Override
        public String decode(byte[] data) throws IOException {
          return StandardCharsets.UTF_8
              .newDecoder()
              .onMalformedInput(CodingErrorAction.REPORT)
              .onUnmappableCharacter(CodingErrorAction.REPORT)
              .decode(ByteBuffer.wrap(data))
              .toString();
        }
      };
  private final ArchivePaths paths;
  int entries, files, directories, zeroRecords;

  StrictUstarInputStream(InputStream input, boolean windows) {
    // A one-record block disables the library's blind skip of a final multi-record block.
    super(input, RECORD, RECORD, "UTF-8", false);
    paths = new ArchivePaths(windows);
  }

  @Override
  protected byte[] readRecord() throws IOException {
    byte[] record = super.readRecord();
    if (record == null) throw bad(); // The library otherwise accepts missing/partial EOF records.
    if (ArchiveUtils.isArrayZero(record, RECORD)) {
      if (++zeroRecords > 2) throw bad();
      return record;
    }
    if (zeroRecords != 0 || ++entries > TarGzipArchiveValidator.MAX_ENTRIES) throw bad();
    byte flag = record[TarConstants.LF_OFFSET];
    if (flag != TarConstants.LF_NORMAL
        && flag != TarConstants.LF_OLDNORM
        && flag != TarConstants.LF_DIR) throw bad();
    if (!ArchiveUtils.matchAsciiBuffer(
            TarConstants.MAGIC_POSIX, record, TarConstants.MAGIC_OFFSET, TarConstants.MAGICLEN)
        || !ArchiveUtils.matchAsciiBuffer(
            TarConstants.VERSION_POSIX,
            record,
            TarConstants.VERSION_OFFSET,
            TarConstants.VERSIONLEN)
        || !ArchiveUtils.isArrayZero(
            Arrays.copyOfRange(record, PREFIX + TarConstants.PREFIXLEN, RECORD),
            RECORD - PREFIX - TarConstants.PREFIXLEN)) throw bad();
    checkNamePadding(record, 0, TarConstants.NAMELEN);
    checkNamePadding(record, PREFIX, TarConstants.PREFIXLEN);
    int offset = TarConstants.NAMELEN;
    for (int length :
        new int[] {
          TarConstants.MODELEN,
          TarConstants.UIDLEN,
          TarConstants.GIDLEN,
          TarConstants.SIZELEN,
          TarConstants.MODTIMELEN,
          TarConstants.CHKSUMLEN
        }) {
      checkNumeric(record, offset, length);
      offset += length;
    }
    checkNumeric(record, PREFIX - 2 * TarConstants.DEVLEN, TarConstants.DEVLEN);
    checkNumeric(record, PREFIX - TarConstants.DEVLEN, TarConstants.DEVLEN);
    TarArchiveEntry entry = new TarArchiveEntry(record, UTF8, false);
    if (!entry.isCheckSumOK()
        || entry.getSize() > TarGzipArchiveValidator.MAX_EXPANDED
        || !entry.getLinkName().isEmpty()) throw bad();
    byte[] checksumRecord = record.clone();
    Arrays.fill(
        checksumRecord,
        TarConstants.CHKSUM_OFFSET,
        TarConstants.CHKSUM_OFFSET + TarConstants.CHKSUMLEN,
        (byte) ' ');
    if (TarUtils.parseOctal(record, TarConstants.CHKSUM_OFFSET, TarConstants.CHKSUMLEN)
        != TarUtils.computeCheckSum(checksumRecord)) throw bad();
    boolean directory = flag == TarConstants.LF_DIR;
    if (directory && entry.getSize() != 0) throw bad();
    if (TarUtils.parseName(record, 0, TarConstants.NAMELEN, UTF8).isEmpty()) throw bad();
    paths.accept(entry.getName(), directory);
    if (directory) directories++;
    else files++;
    return record;
  }

  private static void checkNamePadding(byte[] record, int offset, int length) throws IOException {
    boolean ended = false;
    for (int i = offset; i < offset + length; i++) {
      if (record[i] == 0) ended = true;
      else if (ended) throw bad(); // No hidden suffix after a path's NUL terminator.
    }
  }

  private static void checkNumeric(byte[] record, int offset, int length) throws IOException {
    boolean ended = false;
    for (int i = offset; i < offset + length; i++) {
      int value = record[i] & 255;
      if (value == 0) ended = true;
      else if (value != ' ' && (ended || value < '0' || value > '7')) throw bad();
    }
    // Numeric interpretation remains with the library; binary GNU fields aren't in this subset.
    TarUtils.parseOctal(record, offset, length);
  }

  static IOException bad() {
    return new IOException("Invalid archive");
  }
}
