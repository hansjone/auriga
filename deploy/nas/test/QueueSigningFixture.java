import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.AclEntry;
import java.nio.file.attribute.AclEntryFlag;
import java.nio.file.attribute.AclEntryPermission;
import java.nio.file.attribute.AclEntryType;
import java.nio.file.attribute.AclFileAttributeView;
import java.security.KeyPairGenerator;
import java.security.MessageDigest;
import java.util.Base64;
import java.util.HexFormat;
import java.util.List;

/** Generates an ephemeral test root; never uses deployment credentials. */
class QueueSigningFixture {
  public static void main(String[] args) throws Exception {
    var directory = Path.of(args[0]);
    if (args.length == 2) {
      try (var paths = Files.walk(directory)) {
        for (var path : paths.toList()) {
          var view = Files.getFileAttributeView(path, AclFileAttributeView.class);
          for (var entry : view.getAcl()) {
            if (entry.type() == AclEntryType.ALLOW && !entry.principal().equals(view.getOwner()))
              throw new AssertionError("Generated artifact grants access to a non-owner");
          }
        }
      }
      return;
    }
    var acl = Files.getFileAttributeView(directory, AclFileAttributeView.class);
    var ownerOnly = AclEntry.newBuilder()
        .setType(AclEntryType.ALLOW).setPrincipal(acl.getOwner())
        .setPermissions(AclEntryPermission.values())
        .setFlags(AclEntryFlag.FILE_INHERIT, AclEntryFlag.DIRECTORY_INHERIT).build();
    acl.setAcl(List.of(ownerOnly));
    var root = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    Files.write(directory.resolve("private.der"), root.getPrivate().getEncoded());
    Files.write(directory.resolve("public.der"), root.getPublic().getEncoded());
    var business = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    System.out.println(HexFormat.of().formatHex(
        MessageDigest.getInstance("SHA-256").digest(root.getPublic().getEncoded())));
    System.out.println(Base64.getUrlEncoder().withoutPadding()
        .encodeToString(business.getPublic().getEncoded()));
  }
}
