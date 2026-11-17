using System;
using System.ComponentModel;
using System.Runtime.InteropServices;

// Native access probes distinguish ordinary file access from backup semantics.
// Privilege adjustments affect only the disposable test process and its children.
public static class PythonAccessProbe
{
    [DllImport("kernel32.dll")]
    static extern IntPtr GetCurrentProcess();

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern IntPtr OpenProcess(uint access, bool inherit, int processId);

    [DllImport("kernel32.dll")]
    static extern bool CloseHandle(IntPtr handle);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern IntPtr CreateFile(string path, uint access, uint share, IntPtr security,
        uint disposition, uint flags, IntPtr template);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern bool CreateDirectory(string path, IntPtr security);

    [DllImport("advapi32.dll", SetLastError = true)]
    static extern bool OpenProcessToken(IntPtr process, uint access, out IntPtr token);

    [DllImport("advapi32.dll", SetLastError = true)]
    static extern bool GetTokenInformation(IntPtr token, int kind, IntPtr data, int size, out int required);

    [StructLayout(LayoutKind.Sequential)]
    struct Luid { public uint Low; public int High; }

    [StructLayout(LayoutKind.Sequential)]
    struct TokenPrivilege { public uint Count; public Luid Id; public uint Attributes; }

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern bool LookupPrivilegeValue(string system, string name, out Luid id);

    [DllImport("advapi32.dll", SetLastError = true)]
    static extern bool AdjustTokenPrivileges(IntPtr token, bool disableAll, ref TokenPrivilege change,
        int size, IntPtr previous, IntPtr required);

    public static int OpenForWrite(string path, bool backup, bool create, bool append)
    {
        IntPtr handle = CreateFile(path, append ? 4u : 2u, 7, IntPtr.Zero,
            create ? 1u : 3u, backup ? 0x02000000u : 0x80u, IntPtr.Zero);
        if (handle == new IntPtr(-1)) return Marshal.GetLastWin32Error();
        CloseHandle(handle);
        return 0;
    }

    public static int MakeDirectory(string path)
    {
        return CreateDirectory(path, IntPtr.Zero) ? 0 : Marshal.GetLastWin32Error();
    }

    public static int Privilege(string name)
    {
        return PrivilegeForProcess(0, name);
    }

    public static int PrivilegeForProcess(int processId, string name)
    {
        Luid wanted;
        if (!LookupPrivilegeValue(null, name, out wanted))
            throw new Win32Exception(Marshal.GetLastWin32Error());
        IntPtr token;
        IntPtr process = processId == 0 ? GetCurrentProcess() : OpenProcess(0x1000, false, processId);
        if (process == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
        try
        {
            if (!OpenProcessToken(process, 8, out token))
                throw new Win32Exception(Marshal.GetLastWin32Error());
        }
        finally { if (processId != 0) CloseHandle(process); }
        IntPtr data = IntPtr.Zero;
        try
        {
            int size;
            GetTokenInformation(token, 3, IntPtr.Zero, 0, out size);
            data = Marshal.AllocHGlobal(size);
            if (!GetTokenInformation(token, 3, data, size, out size))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            int count = Marshal.ReadInt32(data);
            for (int index = 0; index < count; index++)
            {
                IntPtr row = IntPtr.Add(data, 4 + index * 12);
                if (unchecked((uint)Marshal.ReadInt32(row)) == wanted.Low &&
                    Marshal.ReadInt32(row, 4) == wanted.High)
                    return Marshal.ReadInt32(row, 8);
            }
            return -1;
        }
        finally { if (data != IntPtr.Zero) Marshal.FreeHGlobal(data); CloseHandle(token); }
    }

    public static void SetRestore(bool enabled)
    {
        ChangeRestore(enabled ? 2u : 0u);
    }

    public static void RemoveRestore()
    {
        ChangeRestore(4);
    }

    static void ChangeRestore(uint attributes)
    {
        IntPtr token;
        if (!OpenProcessToken(GetCurrentProcess(), 0x28, out token))
            throw new Win32Exception(Marshal.GetLastWin32Error());
        try
        {
            Luid id;
            if (!LookupPrivilegeValue(null, "SeRestorePrivilege", out id))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            TokenPrivilege change = new TokenPrivilege { Count = 1, Id = id, Attributes = attributes };
            if (!AdjustTokenPrivileges(token, false, ref change, 0, IntPtr.Zero, IntPtr.Zero))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            int error = Marshal.GetLastWin32Error();
            if (error != 0) throw new Win32Exception(error);
        }
        finally { CloseHandle(token); }
    }
}
