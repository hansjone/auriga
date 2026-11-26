// Lightweight native Win32 helper for Auriga Desktop installer & launcher.
// Replaces repeated PowerShell/WMI cold starts (~0.5-1.5s each) with direct
// Win32 / .NET API calls (~10-20ms each). Compiled with Windows' built-in
// .NET Framework 4 csc.exe (zero external dependencies).
using System;
using System.Collections.Generic;
using System.IO;
using System.Management;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text;
using System.Web.Script.Serialization;

static class WinHelper
{
    static readonly JavaScriptSerializer Json = new JavaScriptSerializer { MaxJsonLength = 33554432 };

    const uint DRIVE_FIXED = 3;
    const uint INVALID_FILE_ATTRIBUTES = 0xFFFFFFFF;
    const uint FILE_ATTRIBUTE_REPARSE_POINT = 0x400;
    const uint TH32CS_SNAPPROCESS = 0x00000002;
    const uint PROCESS_QUERY_LIMITED_INFORMATION = 0x1000;

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern uint GetDriveTypeW(string lpRootPathName);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern bool GetVolumeInformationW(
        string lpRootPathName,
        StringBuilder lpVolumeNameBuffer, int nVolumeNameSize,
        out uint lpVolumeSerialNumber,
        out uint lpMaximumComponentLength,
        out uint lpFileSystemFlags,
        StringBuilder lpFileSystemNameBuffer, int nFileSystemNameSize);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern uint GetFileAttributesW(string lpFileName);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern IntPtr CreateToolhelp32Snapshot(uint dwFlags, uint th32ProcessID);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern bool Process32FirstW(IntPtr hSnapshot, ref PROCESSENTRY32W lppe);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern bool Process32NextW(IntPtr hSnapshot, ref PROCESSENTRY32W lppe);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern IntPtr OpenProcess(uint dwDesiredAccess, bool bInheritHandle, uint dwProcessId);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool GetProcessTimes(IntPtr hProcess, out long creation, out long exit, out long kernel, out long user);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern bool QueryFullProcessImageNameW(IntPtr hProcess, uint dwFlags, StringBuilder lpExeName, ref int lpdwSize);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool CloseHandle(IntPtr hObject);

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct PROCESSENTRY32W
    {
        public uint dwSize;
        public uint cntUsage;
        public uint th32ProcessID;
        public IntPtr th32DefaultHeapID;
        public uint th32ModuleID;
        public uint cntThreads;
        public uint th32ParentProcessID;
        public int pcPriClassBase;
        public uint dwFlags;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 260)]
        public string szExeFile;
    }

    static int Main(string[] args)
    {
        Console.OutputEncoding = new UTF8Encoding(false);
        if (args.Length == 0)
        {
            Console.Error.WriteLine("usage: auriga-win-helper.exe <ntfs-check|boot-id|proc-list|proc-info|protect-python|grant-modify|startup-probe> ...");
            return 2;
        }
        try
        {
            switch (args[0])
            {
                case "ntfs-check":
                    if (args.Length < 2) return 2;
                    return NtfsCheck(args[1]);
                case "boot-id":
                    return BootId();
                case "proc-list":
                    return ProcList();
                case "proc-info":
                    if (args.Length < 2) return 2;
                    uint pid;
                    if (!uint.TryParse(args[1], out pid) || pid < 1) return 2;
                    return ProcInfo(pid);
                case "protect-python":
                    if (args.Length < 3) return 2;
                    return ProtectPython(args[1], args[2]);
                case "grant-modify":
                    if (args.Length < 2) return 2;
                    return GrantModify(args[1]);
                case "startup-probe":
                    if (args.Length < 2) return 2;
                    return StartupProbe(args[1]);
                default:
                    Console.Error.WriteLine("unknown command: " + args[0]);
                    return 2;
            }
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine(ex.Message);
            return 1;
        }
    }

    static string LongPath(string path)
    {
        string full = Path.GetFullPath(path);
        if (full.StartsWith("\\\\?\\")) return full;
        return full.StartsWith("\\\\") ? "\\\\?\\UNC\\" + full.Substring(2) : "\\\\?\\" + full;
    }

    static Dictionary<string, object> CheckNtfsData(string path)
    {
        string full = Path.GetFullPath(path);
        string root = Path.GetPathRoot(full);
        if (string.IsNullOrEmpty(root) || root.Length < 3 || root[1] != ':' || root[2] != '\\')
            throw new ArgumentException("invalid local drive");
        uint driveType = GetDriveTypeW(root);
        var fsBuf = new StringBuilder(64);
        uint serial, maxComp, flags;
        string fs = "";
        if (GetVolumeInformationW(root, null, 0, out serial, out maxComp, out flags, fsBuf, fsBuf.Capacity))
            fs = fsBuf.ToString();

        bool reparse = false;
        string reparsePath = null;
        string tail = full.Substring(root.Length);
        string[] parts = tail.Split(new[] { '\\', '/' }, StringSplitOptions.RemoveEmptyEntries);
        string current = root;
        for (int i = 0; i < parts.Length; i++)
        {
            current = Path.Combine(current, parts[i]);
            uint attr = GetFileAttributesW(LongPath(current));
            if (attr == INVALID_FILE_ATTRIBUTES)
            {
                int err = Marshal.GetLastWin32Error();
                if (err == 2 || err == 3) continue; // ERROR_FILE_NOT_FOUND / ERROR_PATH_NOT_FOUND
                throw new IOException("GetFileAttributesW failed (" + err + ") for " + current);
            }
            if ((attr & FILE_ATTRIBUTE_REPARSE_POINT) != 0)
            {
                reparse = true;
                reparsePath = current;
                break;
            }
        }

        return new Dictionary<string, object>
        {
            { "type", (int)driveType },
            { "fs", fs },
            { "reparse", reparse },
            { "reparsePath", reparsePath },
        };
    }

    static int NtfsCheck(string path)
    {
        Console.WriteLine(Json.Serialize(CheckNtfsData(path)));
        return 0;
    }

    static string QueryBootId()
    {
        // Query Win32_OperatingSystem.LastBootUpTime directly via WMI COM (no PowerShell
        // cold start) so the value is byte-identical to the plugin's host-lock bootId.
        using (var searcher = new ManagementObjectSearcher("SELECT LastBootUpTime FROM Win32_OperatingSystem"))
        using (var results = searcher.Get())
        {
            foreach (ManagementBaseObject item in results)
            {
                using (item)
                {
                    object raw = item["LastBootUpTime"];
                    if (raw == null) continue;
                    DateTime boot = ManagementDateTimeConverter.ToDateTime(raw.ToString()).ToUniversalTime();
                    return boot.ToString("o");
                }
            }
        }
        throw new InvalidOperationException("unable to read LastBootUpTime");
    }

    static int BootId()
    {
        Console.WriteLine(QueryBootId());
        return 0;
    }

    static bool QueryProcessDetails(uint pid, out string createdAt, out string executable)
    {
        createdAt = null;
        executable = null;
        IntPtr handle = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid);
        if (handle == IntPtr.Zero) return false;
        try
        {
            long creation, exit, kernel, user;
            if (!GetProcessTimes(handle, out creation, out exit, out kernel, out user) || creation <= 0)
                return false;
            // Truncate 100ns ticks to microsecond boundary to match WMI CreationDate.
            creation = (creation / 10) * 10;
            createdAt = DateTime.FromFileTimeUtc(creation).ToString("o");
            var exeBuf = new StringBuilder(1024);
            int size = exeBuf.Capacity;
            if (QueryFullProcessImageNameW(handle, 0, exeBuf, ref size) && size > 0)
                executable = exeBuf.ToString(0, size);
            return true;
        }
        finally
        {
            CloseHandle(handle);
        }
    }

    static List<Dictionary<string, object>> QueryProcList()
    {
        IntPtr snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
        if (snap == IntPtr.Zero || snap == new IntPtr(-1))
            throw new InvalidOperationException("CreateToolhelp32Snapshot failed");
        var list = new List<Dictionary<string, object>>();
        try
        {
            var entry = new PROCESSENTRY32W();
            entry.dwSize = (uint)Marshal.SizeOf(typeof(PROCESSENTRY32W));
            if (Process32FirstW(snap, ref entry))
            {
                do
                {
                    if (entry.th32ProcessID == 0) continue;
                    string createdAt, executable;
                    if (!QueryProcessDetails(entry.th32ProcessID, out createdAt, out executable))
                        continue;
                    list.Add(new Dictionary<string, object>
                    {
                        { "pid", (int)entry.th32ProcessID },
                        { "parent", (int)entry.th32ParentProcessID },
                        { "createdAt", createdAt },
                        { "executable", executable },
                    });
                }
                while (Process32NextW(snap, ref entry));
            }
        }
        finally
        {
            CloseHandle(snap);
        }
        return list;
    }

    static int ProcList()
    {
        Console.WriteLine(Json.Serialize(QueryProcList()));
        return 0;
    }

    static int StartupProbe(string homePath)
    {
        Console.WriteLine(Json.Serialize(new Dictionary<string, object>
        {
            { "ntfs", CheckNtfsData(homePath) },
            { "bootId", QueryBootId() },
            { "processes", QueryProcList() },
        }));
        return 0;
    }

    static int ProcInfo(uint pid)
    {
        string createdAt, executable;
        if (!QueryProcessDetails(pid, out createdAt, out executable))
            return 0; // empty output means process not found / inaccessible (matches PS behavior)
        Console.WriteLine(Json.Serialize(new Dictionary<string, object>
        {
            { "pid", (int)pid },
            { "createdAt", createdAt },
            { "executable", executable },
        }));
        return 0;
    }

    static int ProtectPython(string directory, string mode)
    {
        if (mode != "create" && mode != "seal")
        {
            Console.Error.WriteLine("invalid protect-python mode");
            return 2;
        }
        var sid = WindowsIdentity.GetCurrent().User;
        var acl = new DirectorySecurity();
        acl.SetAccessRuleProtection(true, false);
        var inherit = InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit;
        var propagate = PropagationFlags.None;
        var system = new SecurityIdentifier("S-1-5-18");
        acl.AddAccessRule(new FileSystemAccessRule(system, FileSystemRights.FullControl, inherit, propagate, AccessControlType.Allow));
        if (mode == "create")
        {
            acl.SetOwner(sid);
            if (Directory.Exists(directory))
            {
                Console.Error.WriteLine("Python runtime directory already exists");
                return 1;
            }
            acl.AddAccessRule(new FileSystemAccessRule(sid, FileSystemRights.FullControl, inherit, propagate, AccessControlType.Allow));
            new DirectoryInfo(directory).Create(acl);
        }
        else
        {
            var rights = FileSystemRights.ReadAndExecute | FileSystemRights.Delete | FileSystemRights.ChangePermissions | FileSystemRights.WriteAttributes;
            acl.AddAccessRule(new FileSystemAccessRule(sid, rights, inherit, propagate, AccessControlType.Allow));
            var denyWrite = FileSystemRights.WriteData | FileSystemRights.AppendData | FileSystemRights.WriteExtendedAttributes;
            acl.AddAccessRule(new FileSystemAccessRule(sid, denyWrite, inherit, propagate, AccessControlType.Deny));
            new DirectoryInfo(directory).SetAccessControl(acl);
        }
        return 0;
    }

    static int GrantModify(string directory)
    {
        if (!Directory.Exists(directory)) return 0;
        var identity = WindowsIdentity.GetCurrent();
        var dirInfo = new DirectoryInfo(directory);
        var acl = dirInfo.GetAccessControl(AccessControlSections.Access);
        var inherit = InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit;
        var propagate = PropagationFlags.None;
        acl.AddAccessRule(new FileSystemAccessRule(identity.User, FileSystemRights.Modify, inherit, propagate, AccessControlType.Allow));
        Directory.SetAccessControl(directory, acl);
        return 0;
    }
}
