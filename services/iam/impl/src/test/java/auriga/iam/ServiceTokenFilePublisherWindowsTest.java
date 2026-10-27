package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.file.*;
import java.nio.file.attribute.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledOnOs;
import org.junit.jupiter.api.condition.OS;
import org.junit.jupiter.api.io.TempDir;

@EnabledOnOs(OS.WINDOWS)
class ServiceTokenFilePublisherWindowsTest {
  @TempDir Path dir;

  void restrict(Path path) throws Exception {
    Path root = Path.of("").toAbsolutePath();
    while (root != null && !Files.isRegularFile(root.resolve("scripts/beta-ui/private.ps1")))
      root = root.getParent();
    assertNotNull(root, "repository private ACL installer must be available");
    var command =
        new ProcessBuilder(
                "powershell.exe",
                "-NoProfile",
                "-NonInteractive",
                "-File",
                root.resolve("scripts/beta-ui/private.ps1").toString(),
                "-Path",
                path.toString())
            .redirectErrorStream(true);
    var child = command.start();
    var output =
        new String(child.getInputStream().readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
    assertEquals(0, child.waitFor(), output);
  }

  UserPrincipal everyone() throws Exception {
    var child =
        new ProcessBuilder(
                "powershell.exe",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); ([Security.Principal.SecurityIdentifier]::new('S-1-1-0')).Translate([Security.Principal.NTAccount]).Value")
            .start();
    var name =
        new String(child.getInputStream().readAllBytes(), java.nio.charset.StandardCharsets.UTF_8)
            .trim();
    assertEquals(0, child.waitFor());
    return dir.getFileSystem().getUserPrincipalLookupService().lookupPrincipalByName(name);
  }

  Path setup() throws Exception {
    restrict(dir);
    Path secretDir = Files.createDirectory(dir.resolve("secret"));
    restrict(secretDir);
    Path output = Files.createDirectory(dir.resolve("output"));
    restrict(output);
    Path secret = Files.writeString(secretDir.resolve("secret"), Secrets.random());
    restrict(secret);
    return secret;
  }

  @Test
  void realIssuerRenewsAndFailurePreservesOldTokenAndStopCleans() throws Exception {
    Path secret = setup();
    Path token = dir.resolve("output/token");
    var time = new EdgeTokenFilePublisherTest.Time();
    var key =
        new com.nimbusds.jose.jwk.gen.ECKeyGenerator(com.nimbusds.jose.jwk.Curve.P_256)
            .keyID(UUID.randomUUID().toString())
            .generate();
    var issuer = EdgeTokenFilePublisherTest.issuer(time, Files.readString(secret), key);
    var verifier = EdgeTokenFilePublisherTest.verifier(time, key);
    try (var publisher = new ServiceTokenFilePublisher("edge", issuer, secret, token, 1, -1)) {
      publisher.start();
      EdgeTokenFilePublisherTest.awaitToken(token);
      String first = Files.readString(token);
      assertEquals("svc:edge", verifier.verify(first).subject());
      time.now = time.now.plusSeconds(240);
      assertTrue(publisher.refresh());
      String second = Files.readString(token);
      assertNotEquals(first, second);
      assertEquals("svc:edge", verifier.verify(second).subject());
      Files.writeString(secret, Secrets.random());
      assertFalse(publisher.refresh());
      assertEquals(second, Files.readString(token));
      publisher.stop();
      publisher.stop();
      assertFalse(Files.exists(token));
      assertFalse(publisher.refresh());
    }
  }

  @Test
  void unsafeAclAndHardLinkFailWithoutReplacingToken() throws Exception {
    Path secret = setup();
    Path token = dir.resolve("output/token");
    try (var publisher =
        new ServiceTokenFilePublisher("edge", (s, c) -> "a.b.c", secret, token, 1, -1)) {
      publisher.start();
      EdgeTokenFilePublisherTest.awaitToken(token);
      String old = Files.readString(token);
      Path alias = Files.createLink(dir.resolve("alias"), secret);
      assertFalse(publisher.refresh());
      assertEquals(old, Files.readString(token));
      Files.delete(alias);
      var view = Files.getFileAttributeView(secret, AclFileAttributeView.class);
      var acl = new ArrayList<>(view.getAcl());
      acl.add(
          AclEntry.newBuilder()
              .setType(AclEntryType.ALLOW)
              .setPrincipal(everyone())
              .setPermissions(AclEntryPermission.READ_DATA)
              .build());
      view.setAcl(acl);
      assertFalse(publisher.refresh());
      assertEquals(old, Files.readString(token));
    }
  }

  @Test
  void junctionInstallationIsRejected() throws Exception {
    Path secret = setup();
    Path junction = dir.resolve("junction");
    var command =
        new ProcessBuilder(
                "cmd.exe",
                "/d",
                "/c",
                "mklink",
                "/J",
                junction.toString(),
                secret.getParent().toString())
            .redirectErrorStream(true)
            .start();
    command.getInputStream().readAllBytes();
    assertEquals(0, command.waitFor());
    try {
      try (var publisher =
          new ServiceTokenFilePublisher(
              "edge",
              (s, c) -> "a.b.c",
              junction.resolve("secret"),
              dir.resolve("output/token"),
              1,
              -1)) {
        assertThrows(IllegalStateException.class, publisher::start);
      }
    } finally {
      Files.delete(junction);
    }
  }

  @Test
  void pinnedDirectoriesCannotBeRenamedAndRestartRejectsUnsafeDirectory() throws Exception {
    Path secret = setup();
    Path output = dir.resolve("output");
    try (var publisher =
        new ServiceTokenFilePublisher(
            "edge", (s, c) -> "a.b.c", secret, output.resolve("token"), 1, -1)) {
      publisher.start();
      EdgeTokenFilePublisherTest.awaitToken(output.resolve("token"));
      assertThrows(java.io.IOException.class, () -> Files.move(output, dir.resolve("renamed")));
      publisher.stop();
      Files.move(output, dir.resolve("renamed"));
      Files.createDirectory(output);
      var view = Files.getFileAttributeView(output, AclFileAttributeView.class);
      var acl = new ArrayList<>(view.getAcl());
      acl.add(
          AclEntry.newBuilder()
              .setType(AclEntryType.ALLOW)
              .setPrincipal(everyone())
              .setPermissions(AclEntryPermission.READ_DATA)
              .build());
      view.setAcl(acl);
      assertThrows(IllegalStateException.class, publisher::start);
    }
  }
}
