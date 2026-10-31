package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import tools.jackson.databind.JsonNode;

/** Real child Java lifetime test; credentials are read privately and never included in failures. */
final class NetworkSourceJavaProcess {
  private NetworkSourceJavaProcess() {}

  static void verify(boolean eof) throws Exception {
    var executable =
        Path.of(
            System.getProperty("java.home"),
            "bin",
            System.getProperty("os.name").startsWith("Windows") ? "java.exe" : "java");
    var builder =
        new ProcessBuilder(
                executable.toString(),
                "-Djava.io.tmpdir=" + System.getProperty("java.io.tmpdir"),
                "-cp",
                System.getProperty("java.class.path"),
                NetworkSourceJavaHost.class.getName())
            .redirectErrorStream(true);
    builder.environment().put("AURIGA_SOURCE_FAULTS", "1");
    var child = builder.start();
    var lines = new LinkedBlockingQueue<String>();
    var captured = new StringBuffer();
    var reader =
        Thread.ofVirtual()
            .start(
                () -> {
                  try (var input = child.inputReader(StandardCharsets.UTF_8)) {
                    for (String line; (line = input.readLine()) != null; ) {
                      captured.append(line).append('\n');
                      lines.add(line);
                    }
                  } catch (IOException ignored) {
                    /* Exit/ready assertions below report transport failure. */
                  } finally {
                    lines.add("<child-eof>");
                  }
                });
    var owned = new LinkedHashMap<ProcessHandle, Instant>();
    var credentials = new ArrayList<String>();
    Path root = null;
    try (var input = child.outputWriter(StandardCharsets.UTF_8)) {
      var ready = stage(lines, "ready");
      assertEquals(child.pid(), ready.path("pid").asLong());
      assertEquals(ProcessHandle.current().pid(), ready.path("parentPid").asLong());
      root = Path.of(ready.path("config").asString()).getParent();
      assertTrue(root.isAbsolute());
      var config = NetworkJson.parse(Files.readString(root.resolve("bridge.json")));
      var protocolPath = Path.of(config.path("protocolSigning").path("privateKeyFile").asString());
      assertEquals(root, protocolPath.getParent());
      String protocolPem = Files.readString(protocolPath);
      assertTrue(protocolPem.startsWith("-----BEGIN PRIVATE KEY-----"));
      credentials.add(protocolPem);
      credentials.add(protocolPem.replaceAll("-----[^-]+-----|\\s", ""));
      NetworkSourceJavaHostTest.assertPublishedProtocolKey(
          config, protocolPem, NetworkJson.parse(Files.readString(root.resolve("signed.json"))));
      var gatewayPath = Path.of(config.path("gatewayToken").asString());
      var collabPath = Path.of(config.path("collabToken").asString());
      var memberPath = Path.of(config.path("device").asString());
      var externalPath = Path.of(config.path("externalDevice").asString());
      String gateway = Files.readString(gatewayPath);
      String collab = Files.readString(collabPath);
      var member = NetworkJson.parse(Files.readString(memberPath));
      var external = NetworkJson.parse(Files.readString(externalPath));
      credentials.add(gateway);
      credentials.add(collab);
      collect(credentials, member);
      collect(credentials, external);
      input.write("refresh\n");
      input.flush();
      stage(lines, "refreshed");
      String refreshed = Files.readString(gatewayPath);
      String collabNext = Files.readString(collabPath);
      var memberNext = NetworkJson.parse(Files.readString(memberPath));
      var externalNext = NetworkJson.parse(Files.readString(externalPath));
      assertTrue(!gateway.equals(refreshed), "Service JWT file did not refresh");
      assertTrue(!collab.equals(collabNext), "Collab service JWT file did not refresh");
      assertTrue(
          !member.path("access_token").equals(memberNext.path("access_token")),
          "Member device access did not refresh");
      assertTrue(
          !external.path("access_token").equals(externalNext.path("access_token")),
          "External device access did not refresh");
      credentials.add(refreshed);
      credentials.add(collabNext);
      collect(credentials, memberNext);
      collect(credentials, externalNext);
      input.write("revoke-member\n");
      input.flush();
      stage(lines, "member-revoked");
      input.write("fault {\"id\":\"revocation-regression\",\"op\":\"revoke-external\"}\n");
      input.flush();
      var revoked = stage(lines, "fault-control");
      assertEquals("revocation-regression", revoked.path("id").asString());
      assertTrue(revoked.path("revoked").asBoolean());
      child.descendants().forEach(p -> p.info().startInstant().ifPresent(t -> owned.put(p, t)));
      if (eof) input.close();
      else {
        input.write("stop\n");
        input.flush();
      }
      var closed = stage(lines, "closed");
      assertEquals(
          List.of("https", "mailbox", "channels", "iam", "keys", "pg", "files"),
          Json.MAPPER.convertValue(closed.path("order"), List.class));
      assertTrue(child.waitFor(30, TimeUnit.SECONDS), "Child Java did not exit");
      assertEquals(0, child.exitValue());
      reader.join(Duration.ofSeconds(5));
      assertFalse(reader.isAlive());
      assertFalse(Files.exists(root), "Child private root survived normal shutdown");
      for (var credential : credentials)
        assertFalse(captured.toString().contains(credential), "Child output leaked a credential");
      for (var process : owned.keySet())
        assertFalse(
            sameProcess(process, owned.get(process)), "Own child lineage survived shutdown");
      // Only public lifecycle metadata is retained as evidence.
      var evidence = Files.createTempDirectory(Path.of("target"), "java-bridge-lifecycle-");
      Files.writeString(
          evidence.resolve("lifecycle.json"),
          Json.object("eof", eof, "ready", ready, "closed", closed, "exit", child.exitValue())
              .toString());
    } finally {
      // Only this newly spawned child and descendants captured from its live process tree.
      child.descendants().forEach(p -> p.info().startInstant().ifPresent(t -> owned.put(p, t)));
      if (child.isAlive()) {
        child.destroy();
        child.waitFor(30, TimeUnit.SECONDS);
      }
      for (var entry : owned.entrySet())
        if (sameProcess(entry.getKey(), entry.getValue())) entry.getKey().destroyForcibly();
      if (child.isAlive()) child.destroyForcibly();
      assertTrue(child.waitFor(10, TimeUnit.SECONDS), "Own Java child survived cleanup");
      for (var entry : owned.entrySet())
        if (sameProcess(entry.getKey(), entry.getValue()))
          entry.getKey().onExit().get(10, TimeUnit.SECONDS);
      reader.join(Duration.ofSeconds(5));
    }
  }

  private static boolean sameProcess(ProcessHandle p, Instant created) {
    return p.isAlive() && p.info().startInstant().filter(created::equals).isPresent();
  }

  private static void collect(List<String> credentials, JsonNode device) {
    for (var field : List.of("access_token", "refresh_token")) {
      var credential = device.path(field).asString();
      assertFalse(credential.isBlank(), "Device credential is missing");
      credentials.add(credential);
    }
  }

  private static JsonNode stage(BlockingQueue<String> lines, String expected) throws Exception {
    long deadline = System.nanoTime() + TimeUnit.MINUTES.toNanos(3);
    for (; ; ) {
      var line = lines.poll(Math.max(0, deadline - System.nanoTime()), TimeUnit.NANOSECONDS);
      if (line == null || line.equals("<child-eof>"))
        throw new AssertionError("Child did not emit lifecycle stage: " + expected);
      if (line.startsWith("{\"stage\":")) {
        var json = NetworkJson.parse(line);
        if (expected.equals(json.path("stage").asString())) return json;
      }
    }
  }
}
