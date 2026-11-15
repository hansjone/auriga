using System;
using System.IO;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;
using System.Text;

namespace Auriga.Windows
{
    // WScript.Shell converts shortcut paths through the process ANSI code page.
    // Both IShellLinkW and IPersistFile use UTF-16 paths.
    public static class DesktopShortcut
    {
        const uint SLGP_RAWPATH = 4;
        const int SW_SHOWMINNOACTIVE = 7;

        public static string UninstallLabel(string name)
        {
            return "\u5378\u8f7d " + name;
        }

        public static string GetTarget(string path)
        {
            var link = (IShellLinkW)new ShellLink();
            try
            {
                ((IPersistFile)link).Load(path, 0);
                return PathFrom(link);
            }
            catch (COMException)
            {
                return "";
            }
            catch (IOException)
            {
                return "";
            }
            catch (UnauthorizedAccessException)
            {
                return "";
            }
            finally { Marshal.FinalReleaseComObject(link); }
        }

        public static void Save(string path, string target, string directory, string description, string icon)
        {
            var link = (IShellLinkW)new ShellLink();
            try
            {
                var file = (IPersistFile)link;
                if (File.Exists(path)) file.Load(path, 0);
                link.SetPath(target);
                link.SetWorkingDirectory(directory);
                link.SetShowCmd(SW_SHOWMINNOACTIVE);
                link.SetDescription(description);
                if (File.Exists(icon)) link.SetIconLocation(icon, 0);
                file.Save(path, true);
            }
            finally { Marshal.FinalReleaseComObject(link); }
        }

        public static void SetIcon(string path, string expectedTarget, string icon)
        {
            var link = (IShellLinkW)new ShellLink();
            try
            {
                var file = (IPersistFile)link;
                file.Load(path, 0);
                if (!String.Equals(PathFrom(link), expectedTarget, StringComparison.OrdinalIgnoreCase)) return;
                link.SetIconLocation(icon, 0);
                file.Save(path, true);
            }
            finally { Marshal.FinalReleaseComObject(link); }
        }

        static string PathFrom(IShellLinkW link)
        {
            var target = new StringBuilder(260);
            link.GetPath(target, target.Capacity, IntPtr.Zero, SLGP_RAWPATH);
            var path = target.ToString();
            return path.StartsWith("%", StringComparison.Ordinal) ? Environment.ExpandEnvironmentVariables(path) : path;
        }

        [ComImport, Guid("00021401-0000-0000-C000-000000000046")]
        class ShellLink { }

        // Keep the method order from the Windows SDK's IShellLinkW definition.
        [ComImport, Guid("000214F9-0000-0000-C000-000000000046")]
        [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
        interface IShellLinkW
        {
            void GetPath([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder path, int count, IntPtr data, uint flags);
            void GetIDList(out IntPtr idList);
            void SetIDList(IntPtr idList);
            void GetDescription([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder description, int count);
            void SetDescription([MarshalAs(UnmanagedType.LPWStr)] string description);
            void GetWorkingDirectory([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder path, int count);
            void SetWorkingDirectory([MarshalAs(UnmanagedType.LPWStr)] string path);
            void GetArguments([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder arguments, int count);
            void SetArguments([MarshalAs(UnmanagedType.LPWStr)] string arguments);
            void GetHotkey(out ushort hotkey);
            void SetHotkey(ushort hotkey);
            void GetShowCmd(out int command);
            void SetShowCmd(int command);
            void GetIconLocation([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder path, int count, out int index);
            void SetIconLocation([MarshalAs(UnmanagedType.LPWStr)] string path, int index);
            void SetRelativePath([MarshalAs(UnmanagedType.LPWStr)] string path, uint reserved);
            void Resolve(IntPtr window, uint flags);
            void SetPath([MarshalAs(UnmanagedType.LPWStr)] string path);
        }
    }
}
