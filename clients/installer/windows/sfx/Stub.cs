// Self-extracting setup stub. The package zip is appended to this exe, followed
// by a 16-byte trailer: 8 bytes magic "AURZIP01" and the zip length (int64, LE).
// It unpacks to a temp directory, runs install.ps1 with the same arguments,
// forwards the exit code and removes the temp directory.
using System;
using System.Diagnostics;
using System.IO;
using System.Reflection;
using System.Text;
using System.Windows.Forms;
using System.Drawing;
using System.Runtime.InteropServices;

static class Setup
{
    [DllImport("kernel32.dll")] static extern IntPtr GetConsoleWindow();
    [DllImport("user32.dll")] static extern bool ShowWindow(IntPtr window, int command);
    [STAThread]
    static int Main(string[] args)
    {
        bool silent = Array.Exists(args, a => a.Equals("/VERYSILENT", StringComparison.OrdinalIgnoreCase));
        if (!silent) {
            ShowWindow(GetConsoleWindow(), 0);
            Application.EnableVisualStyles();
            return Wizard();
        }
        args = Array.FindAll(args, a => !a.Equals("/VERYSILENT", StringComparison.OrdinalIgnoreCase));
        return Install(args);
    }

    static int Wizard()
    {
        using (var form = new Form()) {
            form.Text = "Auriga DSH 安装程序";
            form.ClientSize = new Size(560, 290);
            form.FormBorderStyle = FormBorderStyle.FixedDialog;
            form.MaximizeBox = false;
            form.StartPosition = FormStartPosition.CenterScreen;
            form.Icon = Icon.ExtractAssociatedIcon(Assembly.GetExecutingAssembly().Location);
            var title = new Label { Text = "安装 Auriga DSH", Left = 24, Top = 20, Width = 500, Height = 35, Font = new Font("Microsoft YaHei UI", 16) };
            var description = new Label { Text = "仅为当前用户安装，无需管理员权限。可在 Windows 应用中卸载。", Left = 24, Top = 65, Width = 510, Height = 40 };
            var folder = new TextBox { Text = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Programs", "AurigaDesktop"), Left = 24, Top = 110, Width = 410 };
            var browse = new Button { Text = "浏览…", Left = 444, Top = 108, Width = 90 };
            browse.Click += delegate { using (var dialog = new FolderBrowserDialog()) { dialog.SelectedPath = folder.Text; if (dialog.ShowDialog(form) == DialogResult.OK) folder.Text = dialog.SelectedPath; } };
            var desktop = new CheckBox { Text = "创建桌面快捷方式", Checked = false, Left = 24, Top = 150, Width = 400 };
            var launch = new CheckBox { Text = "安装完成后启动 Auriga DSH", Checked = true, Left = 24, Top = 180, Width = 400 };
            var status = new Label { Text = "将创建开始菜单快捷方式；升级会保留用户数据。", Left = 24, Top = 220, Width = 375, Height = 50 };
            var install = new Button { Text = "安装", Left = 424, Top = 230, Width = 110 };
            int result = 2;
            install.Click += async delegate {
                install.Enabled = false; browse.Enabled = false; folder.Enabled = false;
                form.ControlBox = false; status.Text = "正在校验和安装，请稍候…"; form.Refresh();
                var installArgs = desktop.Checked ? new string[] { "-InstallDir", folder.Text } : new string[] { "-InstallDir", folder.Text, "-NoDesktopShortcut" };
                result = await System.Threading.Tasks.Task.Run(() => Install(installArgs));
                if (result == 0) {
                    if (launch.Checked) Process.Start(new ProcessStartInfo(Path.Combine(folder.Text, "bin", "auriga-desktop.cmd")) { UseShellExecute = true, WindowStyle = ProcessWindowStyle.Hidden });
                    MessageBox.Show(form, "安装完成。您可以从开始菜单打开 Auriga DSH。", "Auriga DSH");
                    form.Close();
                } else { status.Text = "安装失败（" + result + "），请检查安装路径后重试。"; form.ControlBox = true; install.Enabled = true; browse.Enabled = true; folder.Enabled = true; }
            };
            form.Controls.AddRange(new Control[] { title, description, folder, browse, desktop, launch, status, install });
            form.ShowDialog();
            return result;
        }
    }

    static int Install(string[] args)
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
                if (length < 1 || length > fs.Length - 16) throw new IOException("Invalid setup payload length.");
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
            Console.WriteLine("Unpacking Auriga DSH setup...");
            int rc = Run(Path.Combine(Environment.SystemDirectory, "tar.exe"), "-xf \"" + zip + "\" -C \"" + package + "\"");
            if (rc != 0) return rc;
            Console.WriteLine("Installing Auriga DSH...");
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
        var info = new ProcessStartInfo(file, arguments) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true, RedirectStandardError = true };
        using (var p = Process.Start(info))
        {
            p.OutputDataReceived += delegate(object sender, DataReceivedEventArgs e) { if (e.Data != null) Console.WriteLine(e.Data); };
            p.ErrorDataReceived += delegate(object sender, DataReceivedEventArgs e) { if (e.Data != null) Console.Error.WriteLine(e.Data); };
            p.BeginOutputReadLine();
            p.BeginErrorReadLine();
            p.WaitForExit();
            return p.ExitCode;
        }
    }
}
