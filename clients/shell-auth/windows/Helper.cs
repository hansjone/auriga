using System;
using System.IO;
using System.IO.Pipes;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Security.Cryptography;
using System.Diagnostics;
using System.Collections.Generic;
using System.Threading;
using System.Runtime.InteropServices;
using System.Text;
using System.Web.Script.Serialization;
using Microsoft.Win32.SafeHandles;

// No network listener. Secrets are read from inherited standard-input pipes.
public static class Helper {
  static readonly JavaScriptSerializer Json = new JavaScriptSerializer { MaxJsonLength = 4194304, RecursionLimit = 64 };
  static readonly object OutputLock = new object(), StateLock = new object();
  static readonly string Sid = WindowsIdentity.GetCurrent().User.Value;
  static readonly Dictionary<string, StreamWriter> Pending = new Dictionary<string, StreamWriter>();
  static readonly Dictionary<int, string> Children = new Dictionary<int, string>();
  static readonly TextReader Input = new StreamReader(Console.OpenStandardInput(), new UTF8Encoding(false, true));
  static readonly TextWriter Output = new StreamWriter(Console.OpenStandardOutput(), new UTF8Encoding(false)) { AutoFlush = true };
  static string Capability;
  static bool Closing;
  [StructLayout(LayoutKind.Sequential)] struct SA { public int length; public IntPtr descriptor; public int inherit; }
  [DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool ConvertStringSecurityDescriptorToSecurityDescriptor(string s, uint revision, out IntPtr descriptor, out uint size);
  [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr value);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern SafePipeHandle CreateNamedPipe(string name, uint open, uint mode, uint max, uint outSize, uint inSize, uint timeout, ref SA security);
  [DllImport("kernel32.dll", SetLastError=true)] static extern bool ConnectNamedPipe(SafePipeHandle pipe, IntPtr overlapped);
  [DllImport("kernel32.dll", SetLastError=true)] static extern bool GetNamedPipeClientProcessId(SafePipeHandle pipe, out uint pid);
  [StructLayout(LayoutKind.Sequential)] struct PBI { public IntPtr r1, peb, r2a, r2b, pid, parent; }
  [DllImport("ntdll.dll")] static extern int NtQueryInformationProcess(IntPtr process, int kind, ref PBI data, int size, out int returned);
  static string Line(TextReader reader, int max) {
    var b = new StringBuilder(); int c;
    while ((c = reader.Read()) != -1) { if (c == '\n') return b.ToString().TrimEnd('\r'); if (b.Length >= max) throw new IOException("limit"); b.Append((char)c); }
    return b.Length == 0 ? null : b.ToString();
  }
  static Dictionary<string, object> Decode(string line) { return Json.Deserialize<Dictionary<string, object>>(line); }
  static void Emit(object value) { lock(OutputLock) { Output.WriteLine(Json.Serialize(value)); Output.Flush(); } }
  static bool Controlled(int pid) {
    DateTime descendant = DateTime.MaxValue;
    for (int n = 0; n < 32 && pid > 0; n++) {
      using (var p = Process.GetProcessById(pid)) {
        DateTime started = p.StartTime.ToUniversalTime();
        if (started > descendant) return false;
        lock(StateLock) { string stamp; if (Children.TryGetValue(pid, out stamp)) return stamp == started.Ticks.ToString(); }
        descendant = started;
        PBI info = new PBI(); int length;
        if (NtQueryInformationProcess(p.Handle, 0, ref info, Marshal.SizeOf(info), out length) != 0) return false;
        pid = info.parent.ToInt32();
      }
    }
    return false;
  }
  static NamedPipeServerStream Pipe(string name) {
    IntPtr descriptor; uint size;
    if (!ConvertStringSecurityDescriptorToSecurityDescriptor("D:P(A;;GA;;;" + Sid + ")(A;;GA;;;SY)", 1, out descriptor, out size)) throw new IOException("security");
    try {
      var sa = new SA { length = Marshal.SizeOf(typeof(SA)), descriptor = descriptor, inherit = 0 };
      // PIPE_REJECT_REMOTE_CLIENTS, synchronous byte stream, current-user DACL.
      var handle = CreateNamedPipe(@"\\.\pipe\" + name, 3 | 0x40000000, 8, 8, 65536, 65536, 0, ref sa);
      if (handle.IsInvalid) throw new IOException("pipe");
      var stream = new NamedPipeServerStream(PipeDirection.InOut, true, false, handle);
      stream.WaitForConnection(); return stream;
    } finally { LocalFree(descriptor); }
  }
  static void Serve(NamedPipeServerStream pipe) {
    StreamWriter connectionWriter = null;
    string connectionId = null;
    Timer peerLease = null;
    try {
       string peer = null;
      pipe.RunAsClient(() => { peer = WindowsIdentity.GetCurrent().User.Value; });
      uint pid;
      if (peer != Sid || !GetNamedPipeClientProcessId(pipe.SafePipeHandle, out pid) || !Controlled((int)pid)) return;
       using (var reader = new StreamReader(pipe, new UTF8Encoding(false, true), false, 4096, true))
      using (var writer = new StreamWriter(pipe, new UTF8Encoding(false), 4096, true) { AutoFlush = true }) {
        connectionWriter = writer;
         var hello = Decode(Line(reader, 1024));
        if (!hello.ContainsKey("capability") || !Object.Equals(hello["capability"], Capability)) return;
        connectionId = Guid.NewGuid().ToString();
        using(var process = Process.GetProcessById((int)pid)) Emit(new { opened = connectionId, peerPid = pid, sid = peer, processStart = process.StartTime.ToUniversalTime().Ticks.ToString(), role = "plugin-backend" });
        peerLease = new Timer(_ => { try { if (Controlled((int)pid)) return; } catch {} try { pipe.Dispose(); } catch {} }, null, 500, 500);
        writer.WriteLine("{\"ready\":true}");
        for (string line; !Closing && (line = Line(reader, 2097152)) != null;) {
          if (!Controlled((int)pid)) return;
          var id = Guid.NewGuid().ToString();
          lock(StateLock) { if (Pending.Count >= 128) return; Pending[id] = writer; }
          Emit(new { id = id, connectionId = connectionId, payload = Decode(line) });
        }
      }
    } catch { /* Never echo raw input, exceptions or credentials. */ }
    finally {
      if(peerLease != null) peerLease.Dispose();
      var remove = new List<string>();
      lock(StateLock) { foreach(var item in Pending) if (Object.ReferenceEquals(item.Value, connectionWriter)) remove.Add(item.Key); foreach(var id in remove) Pending.Remove(id); }
      foreach(var id in remove) Emit(new { closed = id });
      if(connectionId != null) Emit(new { disconnected = connectionId });
      pipe.Dispose();
    }
  }
  static void Server(string name) {
    var setup = Decode(Line(Input, 4096)); Capability = (string)setup["capability"];
    if (Capability.Length != 43) throw new IOException("capability");
    var input = new Thread(() => {
      try {
        for (string line; (line = Line(Input, 4194304)) != null;) {
          var message = Decode(line);
          if (message.ContainsKey("register")) {
            int pid = Convert.ToInt32(message["register"]);
            using (var p = Process.GetProcessById(pid)) { lock(StateLock) Children[pid] = p.StartTime.ToUniversalTime().Ticks.ToString(); }
            Emit(new { registered = pid });
          } else {
            StreamWriter writer; string id = (string)message["id"];
            lock(StateLock) { if (!Pending.TryGetValue(id, out writer)) continue; if (!message.ContainsKey("keep") || !(bool)message["keep"]) Pending.Remove(id); }
            try { lock(writer) { writer.WriteLine(Json.Serialize(message["payload"])); } } catch { lock(StateLock) Pending.Remove(id); Emit(new { closed = id }); }
          }
        }
      } catch {} finally { Closing = true; Environment.Exit(0); }
    }); input.IsBackground = true; input.Start();
    Emit(new { listening = name, sid = Sid });
    int active = 0;
    while (!Closing) {
      var pipe = Pipe(name);
      if (Interlocked.Increment(ref active) > 8) { Interlocked.Decrement(ref active); pipe.Dispose(); continue; }
      var thread = new Thread(() => { try { Serve(pipe); } finally { Interlocked.Decrement(ref active); } }); thread.IsBackground = true; thread.Start();
    }
  }
  static void Client(string name) {
    var hello = Decode(Line(Input, 4096));
    using (var pipe = new NamedPipeClientStream(".", name, PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Impersonation)) {
      pipe.Connect(5000);
      var writer = new StreamWriter(pipe, new UTF8Encoding(false)) { AutoFlush = true };
      var reader = new StreamReader(pipe, new UTF8Encoding(false, true));
      writer.WriteLine(Json.Serialize(hello));
      string ready = Line(reader, 1024); if (ready != "{\"ready\":true}") throw new IOException("denied");
      Output.WriteLine(ready); Output.Flush();
      var input = new Thread(() => { try { for (string line; (line = Line(Input, 2097152)) != null;) {  writer.WriteLine(line); } } catch {} finally { pipe.Dispose(); } }); input.IsBackground = true; input.Start();
      for (string line; (line = Line(reader, 4194304)) != null;) { Output.WriteLine(line); Output.Flush(); }
    }
  }
  static void Secure(string path) {
    if ((File.GetAttributes(path) & FileAttributes.ReparsePoint) != 0) throw new IOException("reparse");
    var security = new DirectorySecurity(); security.SetAccessRuleProtection(true, false);
    security.SetOwner(new SecurityIdentifier(Sid));
    security.AddAccessRule(new FileSystemAccessRule(new SecurityIdentifier(Sid), FileSystemRights.FullControl, InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit, PropagationFlags.None, AccessControlType.Allow));
    Directory.SetAccessControl(path, security);
  }
  public static int Main(string[] args) {
    try {
      if (args.Length == 2 && args[0] == "server") Server(args[1]);
      else if (args.Length == 2 && args[0] == "client") Client(args[1]);
      else if (args.Length == 2 && args[0] == "secure") { Secure(args[1]); Emit(new { sid = Sid, protectedAcl = Directory.GetAccessControl(args[1]).AreAccessRulesProtected }); }
      else if (args.Length == 1 && (args[0] == "protect" || args[0] == "unprotect")) {
        byte[] value = Convert.FromBase64String(Line(Input, 2097152));
        byte[] entropy = Encoding.UTF8.GetBytes("auriga.shell-auth.v1");
        byte[] result = args[0] == "protect" ? ProtectedData.Protect(value, entropy, DataProtectionScope.CurrentUser) : ProtectedData.Unprotect(value, entropy, DataProtectionScope.CurrentUser);
        Output.WriteLine(Convert.ToBase64String(result)); Array.Clear(value, 0, value.Length); Array.Clear(result, 0, result.Length);
      } else return 2;
      return 0;
    } catch { Console.Error.WriteLine("shell helper unavailable"); return 1; }
  }
}
