using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Security.Principal;

// Test-only access to this process's default object owner. No machine policy changes.
public static class ResourceTestTokenOwner
{
    [DllImport("kernel32.dll")]
    private static extern IntPtr GetCurrentProcess();

    [DllImport("kernel32.dll")]
    private static extern bool CloseHandle(IntPtr handle);

    [DllImport("advapi32.dll", SetLastError = true)]
    private static extern bool OpenProcessToken(IntPtr process, uint access, out IntPtr token);

    [DllImport("advapi32.dll", SetLastError = true)]
    private static extern bool GetTokenInformation(IntPtr token, int kind, IntPtr data, int size, out int required);

    [DllImport("advapi32.dll", SetLastError = true)]
    private static extern bool SetTokenInformation(IntPtr token, int kind, IntPtr data, int size);

    public static string Get()
    {
        IntPtr token;
        if (!OpenProcessToken(GetCurrentProcess(), 0x0008, out token))
            throw new Win32Exception(Marshal.GetLastWin32Error());
        IntPtr data = IntPtr.Zero;
        try
        {
            int size;
            GetTokenInformation(token, 4, IntPtr.Zero, 0, out size);
            data = Marshal.AllocHGlobal(size);
            if (!GetTokenInformation(token, 4, data, size, out size))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            return new SecurityIdentifier(Marshal.ReadIntPtr(data)).Value;
        }
        finally
        {
            Marshal.FreeHGlobal(data);
            CloseHandle(token);
        }
    }

    public static void Set(string value)
    {
        IntPtr token;
        if (!OpenProcessToken(GetCurrentProcess(), 0x0080, out token))
            throw new Win32Exception(Marshal.GetLastWin32Error());
        SecurityIdentifier owner = new SecurityIdentifier(value);
        byte[] bytes = new byte[owner.BinaryLength];
        owner.GetBinaryForm(bytes, 0);
        IntPtr sid = Marshal.AllocHGlobal(bytes.Length);
        IntPtr data = Marshal.AllocHGlobal(IntPtr.Size);
        try
        {
            Marshal.Copy(bytes, 0, sid, bytes.Length);
            Marshal.WriteIntPtr(data, sid);
            if (!SetTokenInformation(token, 4, data, IntPtr.Size))
                throw new Win32Exception(Marshal.GetLastWin32Error());
        }
        finally
        {
            Marshal.FreeHGlobal(data);
            Marshal.FreeHGlobal(sid);
            CloseHandle(token);
        }
    }
}
