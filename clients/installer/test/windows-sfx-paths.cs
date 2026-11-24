// Integration harness compiled with the production stub under Framework csc.
using System;
using System.IO;
using System.IO.Compression;
using System.Reflection;
using System.Windows.Forms;

static class SfxPaths
{
    [STAThread]
    static int Main(string[] args)
    {
        if (args[0] != "legacy") Setup.ConfigurePaths();
        if (args[0] == "wizard") return WizardRun(args[1]);
        if (args[0] == "unsafe") return UnsafeRun(args[1]);
        string directory = args[2];
        int files = 0; long bytes = 0; int max = 0;
        try
        {
            Directory.CreateDirectory(directory);
            using (var file = File.OpenRead(args[1]))
            {
                file.Seek(-8, SeekOrigin.End);
                var length = new byte[8]; file.Read(length, 0, 8);
                using (var slice = new Slice(file, file.Length - 16 - BitConverter.ToInt64(length, 0), BitConverter.ToInt64(length, 0)))
                using (var zip = new ZipArchive(slice, ZipArchiveMode.Read))
                {
                    if (args[0] == "legacy")
                    {
                        foreach (var entry in zip.Entries)
                        {
                            string name = Setup.EntryPath(entry);
                            if (name == null) continue;
                            string target = Path.Combine(directory, name.Replace('/', '\\'));
                            Directory.CreateDirectory(Path.GetDirectoryName(target));
                            entry.ExtractToFile(target, false);
                        }
                        return 0;
                    }
                    Setup.Extract(zip, directory, () => args[0] == "cancel" && files == 10,
                        (name, done) => { files++; bytes = done; max = Math.Max(max, Path.Combine(directory, name).Length); });
                }
            }
            Console.WriteLine("files={0} bytes={1} maxPath={2}", files, bytes, max);
            return 0;
        }
        catch (OperationCanceledException) { Console.WriteLine("cancelled files=" + files); return 2; }
        catch (Exception e) { Console.WriteLine(e.GetType().Name + " HRESULT=" + e.HResult + " " + e.Message); return 1; }
        finally { Directory.Delete(args[0] == "legacy" ? directory : Setup.FileSystemPath(directory), true); Console.WriteLine("cleaned=" + !Directory.Exists(directory)); }
    }

    static int UnsafeRun(string root)
    {
        foreach (string name in new[] { "../escape", "../escape/", "/absolute", "C:/absolute", "safe/../../escape", "safe\\..\\escape", "file:stream" })
        using (var stream = new MemoryStream())
        {
            using (var zip = new ZipArchive(stream, ZipArchiveMode.Create, true)) { zip.CreateEntry("valid"); zip.CreateEntry(name); }
            stream.Position = 0;
            using (var zip = new ZipArchive(stream, ZipArchiveMode.Read))
            {
                try { Setup.Extract(zip, root, () => false, (n, d) => { }); return 1; }
                catch (IOException) { if (Directory.Exists(root)) return 1; }
            }
        }
        Console.WriteLine("unsafe entries rejected before writes");
        return 0;
    }

    static object Field(object o, string name) { return o.GetType().GetField(name, BindingFlags.Instance | BindingFlags.NonPublic).GetValue(o); }
    static int WizardRun(string root)
    {
        Application.EnableVisualStyles();
        using (var wizard = new Wizard())
        using (var timer = new Timer { Interval = 50 })
        {
            ((TextBox)Field(wizard, "folder")).Text = root;
            var next = typeof(Wizard).GetMethod("Next", BindingFlags.Instance | BindingFlags.NonPublic);
            int peak = 0;
            wizard.Shown += delegate
            {
                // Welcome -> License
                next.Invoke(wizard, null);
                // License must block advancing until accepted
                next.Invoke(wizard, null);
                if ((int)Field(wizard, "page") != 1)
                {
                    Console.WriteLine("licenseGateFailed=True");
                    wizard.ExitCode = 98;
                    wizard.Close();
                    return;
                }
                ((RadioButton)Field(wizard, "acceptLicense")).Checked = true;
                // License -> Location -> Tasks -> Ready -> Progress
                next.Invoke(wizard, null);
                next.Invoke(wizard, null);
                next.Invoke(wizard, null);
                next.Invoke(wizard, null);
                timer.Start();
            };
            timer.Tick += delegate
            {
                peak = Math.Max(peak, ((ProgressBar)Field(wizard, "bar")).Value);
                if ((int)Field(wizard, "page") != 6) return;
                Console.WriteLine("wizardExit=" + wizard.ExitCode + " progress=" + peak + " openLog=" + ((Control)Field(wizard, "openLog")).Visible + " finish=" + ((Label)Field(wizard, "finishText")).Text);
                timer.Stop(); wizard.Close();
            };
            Application.Run(wizard);
            return wizard.ExitCode;
        }
    }
}
