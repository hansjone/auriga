// Self-extracting setup stub. The package zip is appended to this exe, followed
// by a 16-byte trailer: 8 bytes magic "AURZIP01" and the zip length (int64, LE).
// It unpacks to a temp directory, runs install.ps1 with the same arguments,
// forwards the exit code and removes the temp directory.
using System;
using System.Diagnostics;
using System.IO;
using System.Reflection;
using System.Text;

static class Setup
{
    static int Main(string[] args)
    {
        string self = Assembly.GetExecutingAssembly().Location;
        string temp = Path.Combine(Path.GetTempPath(), "auriga-desktop-setup-" + Guid.NewGuid().ToString("N"));
        try
        {
            Directory.CreateDirectory(temp);
            string zip = Path.Combine(temp, "package.zip");
            using (var fs = new FileStream(self, FileMode.Open, FileAccess.Read, FileShare.Read))
            {
                var trailer = new byte[16];
                fs.Seek(-16, SeekOrigin.End);
                fs.Read(trailer, 0, 16);
                if (Encoding.ASCII.GetString(trailer, 0, 8) != "AURZIP01")
                {
                    Console.Error.WriteLine("Setup payload not found.");
                    return 2;
                }
                long length = BitConverter.ToInt64(trailer, 8);
                fs.Seek(-16 - length, SeekOrigin.End);
                using (var output = File.Create(zip))
                {
                    var buffer = new byte[1 << 20];
                    long left = length;
                    while (left > 0)
                    {
                        int read = fs.Read(buffer, 0, (int)Math.Min(buffer.Length, left));
                        if (read <= 0) throw new IOException("Setup payload is truncated.");
                        output.Write(buffer, 0, read);
                        left -= read;
                    }
                }
            }
            string package = Path.Combine(temp, "pkg");
            Directory.CreateDirectory(package);
            Console.WriteLine("Unpacking Auriga Desktop setup...");
            int rc = Run(Path.Combine(Environment.SystemDirectory, "tar.exe"), "-xf \"" + zip + "\" -C \"" + package + "\"");
            if (rc != 0) return rc;
            Console.WriteLine("Installing Auriga Desktop...");
            string forwarded = string.Join(" ", Array.ConvertAll(args, a => "\"" + a.Replace("\"", "\\\"") + "\""));
            return Run(
                Path.Combine(Environment.SystemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe"),
                "-NoProfile -ExecutionPolicy Bypass -File \"" + Path.Combine(package, "install.ps1") + "\" " + forwarded);
        }
        catch (Exception e)
        {
            Console.Error.WriteLine(e.Message);
            return 1;
        }
        finally
        {
            try { Directory.Delete(temp, true); } catch (Exception) { }
        }
    }

    static int Run(string file, string arguments)
    {
        var info = new ProcessStartInfo(file, arguments) { UseShellExecute = false };
        using (var p = Process.Start(info))
        {
            p.WaitForExit();
            return p.ExitCode;
        }
    }
}
