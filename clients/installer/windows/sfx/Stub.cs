// Self-extracting setup. The package zip is appended to this exe, followed by a
// 16-byte trailer: 8 bytes magic "AURZIP01" and the zip length (int64, LE).
// Interactive runs show a wizard: welcome -> location -> progress -> finish.
// /VERYSILENT keeps the console path: unpack, run install.ps1 with the same
// arguments, forward the exit code and remove the temp directory.
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.IO.Compression;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Web.Script.Serialization;
using System.Windows.Forms;
using Microsoft.Win32;

/// Read-only window onto the zip appended to this exe.
sealed class Slice : Stream
{
    readonly Stream inner; readonly long start, length; long position;
    public Slice(Stream inner, long start, long length) { this.inner = inner; this.start = start; this.length = length; }
    public override bool CanRead { get { return true; } }
    public override bool CanSeek { get { return true; } }
    public override bool CanWrite { get { return false; } }
    public override long Length { get { return length; } }
    public override long Position { get { return position; } set { position = value; } }
    public override void Flush() { }
    public override int Read(byte[] buffer, int offset, int count)
    {
        long left = length - position;
        if (left <= 0) return 0;
        if (count > left) count = (int)left;
        inner.Seek(start + position, SeekOrigin.Begin);
        int read = inner.Read(buffer, offset, count);
        position += read;
        return read;
    }
    public override long Seek(long offset, SeekOrigin origin)
    {
        position = origin == SeekOrigin.Begin ? offset : origin == SeekOrigin.Current ? position + offset : length + offset;
        return position;
    }
    public override void SetLength(long value) { throw new NotSupportedException(); }
    public override void Write(byte[] buffer, int offset, int count) { throw new NotSupportedException(); }
}

static class Setup
{
    public const string Brand = "Auriga DSH";
    [DllImport("kernel32.dll")] static extern IntPtr GetConsoleWindow();
    [DllImport("user32.dll")] static extern bool ShowWindow(IntPtr window, int command);

    [STAThread]
    static int Main(string[] args)
    {
        bool silent = Array.Exists(args, a => a.Equals("/VERYSILENT", StringComparison.OrdinalIgnoreCase));
        if (!silent)
        {
            ShowWindow(GetConsoleWindow(), 0);
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            using (var wizard = new Wizard()) { Application.Run(wizard); return wizard.ExitCode; }
        }
        args = Array.FindAll(args, a => !a.Equals("/VERYSILENT", StringComparison.OrdinalIgnoreCase));
        return Install(args);
    }

    public static Slice OpenPayload(out FileStream file)
    {
        file = new FileStream(Assembly.GetExecutingAssembly().Location, FileMode.Open, FileAccess.Read, FileShare.Read);
        var trailer = new byte[16];
        file.Seek(-16, SeekOrigin.End);
        file.Read(trailer, 0, 16);
        if (Encoding.ASCII.GetString(trailer, 0, 8) != "AURZIP01") throw new IOException("Setup payload not found.");
        long length = BitConverter.ToInt64(trailer, 8);
        if (length < 1 || length > file.Length - 16) throw new IOException("Invalid setup payload length.");
        return new Slice(file, file.Length - 16 - length, length);
    }

    /// Entry name inside the package, or null for directories and unsafe names.
    public static string EntryPath(ZipArchiveEntry entry)
    {
        string name = entry.FullName.Replace('\\', '/');
        while (name.StartsWith("./")) name = name.Substring(2);
        if (name.Length == 0 || name.EndsWith("/")) return null;
        if (name.StartsWith("/") || name.Contains(":") || Array.IndexOf(name.Split('/'), "..") >= 0) throw new IOException("unsafe package entry");
        return name;
    }

    static int Install(string[] args)
    {
        string temp = Path.Combine(Path.GetTempPath(), "auriga-desktop-setup-" + Guid.NewGuid().ToString("N"));
        try
        {
            Directory.CreateDirectory(temp);
            string zip = Path.Combine(temp, "package.zip");
            FileStream file;
            using (var payload = OpenPayload(out file))
            using (file)
            using (var output = File.Create(zip)) payload.CopyTo(output, 1 << 20);
            string package = Path.Combine(temp, "pkg");
            Directory.CreateDirectory(package);
            Console.WriteLine("Unpacking " + Brand + " setup...");
            int rc = Run(Path.Combine(Environment.SystemDirectory, "tar.exe"), "-xf \"" + zip + "\" -C \"" + package + "\"", null);
            if (rc != 0) return rc;
            Console.WriteLine("Installing " + Brand + "...");
            string forwarded = string.Join(" ", Array.ConvertAll(args, a => "\"" + a.Replace("\"", "\\\"") + "\""));
            return Run(PowerShell, "-NoProfile -ExecutionPolicy Bypass -File \"" + Path.Combine(package, "install.ps1") + "\" " + forwarded, null);
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

    public static string PowerShell { get { return Path.Combine(Environment.SystemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe"); } }

    public static int Run(string file, string arguments, Action<string> onLine)
    {
        var info = new ProcessStartInfo(file, arguments)
        {
            UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true, RedirectStandardError = true,
            StandardOutputEncoding = Encoding.UTF8, StandardErrorEncoding = Encoding.UTF8,
        };
        using (var p = Process.Start(info))
        {
            p.OutputDataReceived += delegate (object sender, DataReceivedEventArgs e) { if (e.Data == null) return; if (onLine != null) onLine(e.Data); else Console.WriteLine(e.Data); };
            p.ErrorDataReceived += delegate (object sender, DataReceivedEventArgs e) { if (e.Data == null) return; if (onLine != null) onLine("[stderr] " + e.Data); else Console.Error.WriteLine(e.Data); };
            p.BeginOutputReadLine();
            p.BeginErrorReadLine();
            p.WaitForExit();
            return p.ExitCode;
        }
    }

    /// Same instance names as the tray: Local\AurigaDesktop-<16 hex of SHA-256(lower root)>.
    public static string Key(string root)
    {
        string normal = Path.GetFullPath(root).TrimEnd('\\').ToLowerInvariant();
        using (var sha = SHA256.Create())
            return "Local\\AurigaDesktop-" + BitConverter.ToString(sha.ComputeHash(Encoding.UTF8.GetBytes(normal)), 0, 8).Replace("-", "");
    }

    /// Ask a running tray of this root to exit (it stops DSH) and wait for its lock.
    public static bool StopRunning(string root, int seconds)
    {
        string key = Key(root);
        EventWaitHandle exit;
        if (EventWaitHandle.TryOpenExisting(key + "-exit", out exit)) using (exit) exit.Set();
        DateTime until = DateTime.UtcNow.AddSeconds(seconds);
        for (;;)
        {
            bool created;
            using (var mutex = new Mutex(false, key, out created))
            {
                bool owned;
                try { owned = mutex.WaitOne(0); } catch (AbandonedMutexException) { owned = true; }
                if (owned) { mutex.ReleaseMutex(); break; }
            }
            if (DateTime.UtcNow >= until) return false;
            Thread.Sleep(250);
        }
        // Older versions without tray run in a console window and keep a session file.
        try
        {
            var session = new JavaScriptSerializer().Deserialize<Dictionary<string, object>>(File.ReadAllText(Path.Combine(root, "desktop-session.json")));
            using (var p = Process.GetProcessById(Convert.ToInt32(session["pid"])))
                if (!p.HasExited && p.ProcessName.Equals("node", StringComparison.OrdinalIgnoreCase)) return false;
        }
        catch (Exception) { }
        return true;
    }
}

sealed class Wizard : Form
{
    const int W = 620, H = 450;
    public int ExitCode = 2;
    readonly Panel header = new Panel { Dock = DockStyle.Top, Height = 72, BackColor = Color.White };
    readonly Label heading = new Label { Left = 24, Top = 14, Width = 480, Height = 26, Font = new Font("Microsoft YaHei UI", 12, FontStyle.Bold) };
    readonly Label subheading = new Label { Left = 26, Top = 42, Width = 480, Height = 20, ForeColor = Color.DimGray };
    readonly PictureBox logo = new PictureBox { Left = W - 72, Top = 12, Width = 48, Height = 48, SizeMode = PictureBoxSizeMode.Zoom };
    readonly Panel body = new Panel { Left = 0, Top = 73, Width = W, Height = H - 73 - 56 };
    readonly Button back = new Button { Text = "< 上一步", Width = 96, Height = 28 };
    readonly Button next = new Button { Text = "下一步 >", Width = 96, Height = 28 };
    readonly Button cancel = new Button { Text = "取消", Width = 96, Height = 28 };
    readonly TextBox folder = new TextBox { Left = 32, Top = 70, Width = 440 };
    readonly Button browse = new Button { Text = "浏览…", Left = 482, Top = 68, Width = 100, Height = 26 };
    readonly CheckBox desktopLink = new CheckBox { Left = 32, Top = 150, Width = 520, Text = "创建桌面快捷方式" };
    readonly Label stepLabel = new Label { Left = 32, Top = 40, Width = 550, Height = 22 };
    readonly ProgressBar bar = new ProgressBar { Left = 32, Top = 70, Width = 550, Height = 22, Maximum = 1000 };
    readonly Label percentLabel = new Label { Left = 32, Top = 100, Width = 550, Height = 22, ForeColor = Color.DimGray };
    readonly CheckBox launch = new CheckBox { Left = 32, Top = 120, Width = 520, Text = "立即启动 Auriga DSH", Checked = true };
    readonly Label finishText = new Label { Left = 32, Top = 30, Width = 550, Height = 84 };
    int page;
    string version = "?", existingRoot, existingVersion, logFile, cancelFile, temp;
    long unpackedBytes;
    volatile bool cancelRequested, canCancel = true;
    bool installing, succeeded;

    public Wizard()
    {
        Text = Setup.Brand + " 安装程序";
        ClientSize = new Size(W, H);
        FormBorderStyle = FormBorderStyle.FixedDialog;
        MaximizeBox = false;
        StartPosition = FormStartPosition.CenterScreen;
        Font = new Font("Microsoft YaHei UI", 9);
        try { Icon = Icon.ExtractAssociatedIcon(Assembly.GetExecutingAssembly().Location); logo.Image = new Icon(Icon, 48, 48).ToBitmap(); } catch (Exception) { }
        header.Controls.AddRange(new Control[] { heading, subheading, logo });
        var line = new Label { Left = 0, Top = 72, Width = W, Height = 1, BorderStyle = BorderStyle.Fixed3D };
        var footer = new Label { Left = 0, Top = H - 56, Width = W, Height = 1, BorderStyle = BorderStyle.Fixed3D };
        back.Location = new Point(W - 330, H - 42);
        next.Location = new Point(W - 226, H - 42);
        cancel.Location = new Point(W - 116, H - 42);
        back.Click += delegate { Show(page - 1); };
        next.Click += delegate { Next(); };
        cancel.Click += delegate { Cancel(); };
        Controls.AddRange(new Control[] { header, line, body, footer, back, next, cancel });
        AcceptButton = next;
        browse.Click += delegate
        {
            using (var dialog = new FolderBrowserDialog { Description = "选择 " + Setup.Brand + " 的安装位置" })
            {
                dialog.SelectedPath = Directory.Exists(folder.Text) ? folder.Text : Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
                if (dialog.ShowDialog(this) == DialogResult.OK)
                    folder.Text = Directory.Exists(dialog.SelectedPath) && Directory.GetFileSystemEntries(dialog.SelectedPath).Length > 0 && !File.Exists(Path.Combine(dialog.SelectedPath, ".auriga-desktop-install"))
                        ? Path.Combine(dialog.SelectedPath, "AurigaDesktop") : dialog.SelectedPath;
            }
        };
        FormClosing += delegate (object s, FormClosingEventArgs e) { if (installing) { e.Cancel = true; Cancel(); } };
        ReadPackage();
        DetectExisting();
        folder.Text = existingRoot ?? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Programs", "AurigaDesktop");
        Show(0);
    }

    void ReadPackage()
    {
        try
        {
            FileStream file;
            using (var payload = Setup.OpenPayload(out file))
            using (file)
            using (var zip = new ZipArchive(payload, ZipArchiveMode.Read))
            {
                foreach (var entry in zip.Entries)
                {
                    unpackedBytes += entry.Length;
                    if (Setup.EntryPath(entry) == "payload/manifest.json")
                        using (var reader = new StreamReader(entry.Open(), Encoding.UTF8))
                        {
                            var match = Regex.Match(reader.ReadToEnd(), "\"bundleVersion\"\\s*:\\s*\"([^\"]+)\"");
                            if (match.Success) version = match.Groups[1].Value;
                        }
                }
            }
        }
        catch (Exception) { }
    }

    static string RegistryName
    {
        get
        {
            string name = Environment.GetEnvironmentVariable("AURIGA_REGISTRY_KEY");
            return string.IsNullOrEmpty(name) ? "AurigaDesktop" : name;
        }
    }

    void DetectExisting()
    {
        try
        {
            using (var key = Registry.CurrentUser.OpenSubKey("Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\" + RegistryName))
            {
                if (key == null) return;
                string location = Convert.ToString(key.GetValue("InstallLocation", ""));
                if (location.Length > 0 && File.Exists(Path.Combine(location, ".auriga-desktop-install")))
                {
                    existingRoot = Path.GetFullPath(location);
                    existingVersion = Convert.ToString(key.GetValue("DisplayVersion", ""));
                }
            }
        }
        catch (Exception) { }
    }

    void Header(string title, string sub) { heading.Text = title; subheading.Text = sub; }

    Label Text2(string text, int top, int height)
    {
        return new Label { Left = 32, Top = top, Width = 550, Height = height, Text = text };
    }

    void Show(int target)
    {
        page = target;
        body.Controls.Clear();
        back.Visible = page == 1;
        next.Enabled = true; cancel.Enabled = true;
        if (page == 0)
        {
            Header("欢迎使用 " + Setup.Brand + " 安装向导", "版本 " + version);
            body.Controls.Add(Text2(existingRoot != null
                ? "检测到此电脑已安装 " + Setup.Brand + (existingVersion.Length > 0 ? " " + existingVersion : "") + "。\n\n本向导会把它升级到 " + version + "，保留您的配置、会话和登录信息。安装前会自动退出正在运行的 " + Setup.Brand + "。"
                : "本向导将在此电脑上安装 " + Setup.Brand + " " + version + "。\n\n" + Setup.Brand + " 包含本地 DSH 工作空间、Auriga 插件和运行所需的 Node.js 环境，装好后从开始菜单或托盘图标使用。", 24, 120));
            body.Controls.Add(Text2("仅为当前 Windows 用户安装，无需管理员权限。点击“下一步”继续。", 170, 40));
            next.Text = "下一步 >";
        }
        else if (page == 1)
        {
            Header("选择安装位置", existingRoot != null ? "升级会保留在原安装位置" : "仅当前用户使用，无需管理员权限");
            body.Controls.Add(Text2(existingRoot != null ? "将升级以下位置的现有安装：" : "安装到以下文件夹（需要可写权限，例如“本地应用数据”或您自己的文件夹）：", 40, 24));
            folder.Enabled = browse.Enabled = existingRoot == null;
            body.Controls.Add(folder); body.Controls.Add(browse);
            body.Controls.Add(Text2(string.Format("所需磁盘空间约 {0:0} MB。开始菜单快捷方式会自动创建。", unpackedBytes / 1048576.0 * 1.05), 104, 22));
            desktopLink.Enabled = existingRoot == null;
            if (existingRoot != null) desktopLink.Text = "桌面快捷方式：沿用现有设置";
            body.Controls.Add(desktopLink);
            next.Text = existingRoot != null ? "升级(&U)" : "安装(&I)";
        }
        else if (page == 2)
        {
            Header(existingRoot != null ? "正在升级" : "正在安装", "请稍候，" + Setup.Brand + " 正在" + (existingRoot != null ? "升级" : "安装"));
            body.Controls.AddRange(new Control[] { stepLabel, bar, percentLabel });
            back.Visible = false; next.Enabled = false;
        }
        else
        {
            Header(succeeded ? "安装完成" : "安装未完成", succeeded ? Setup.Brand + " " + version + " 已就绪" : "未做任何更改");
            body.Controls.Add(finishText);
            if (succeeded) body.Controls.Add(launch);
            next.Text = "完成(&F)"; next.Enabled = true; cancel.Enabled = false; back.Visible = false;
        }
    }

    void Next()
    {
        if (page == 0) { Show(1); return; }
        if (page == 1) { if (Validate()) StartInstall(); return; }
        if (page == 3)
        {
            if (succeeded && launch.Checked) LaunchInstalled(folder.Text);
            Close();
        }
    }

    bool Fail(string message) { MessageBox.Show(this, message, Setup.Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning); return false; }

    new bool Validate()
    {
        string path = folder.Text.Trim();
        try
        {
            if (path.Length == 0 || !Path.IsPathRooted(path)) return Fail("请选择一个完整的安装路径。");
            path = Path.GetFullPath(path);
            if (Path.GetPathRoot(path).TrimEnd('\\').Equals(path.TrimEnd('\\'), StringComparison.OrdinalIgnoreCase)) return Fail("不能直接安装到磁盘根目录，请选择一个文件夹。");
            if (path.StartsWith("\\\\")) return Fail("不支持网络路径，请选择本机磁盘上的文件夹。");
            if (Directory.Exists(path) && Directory.GetFileSystemEntries(path).Length > 0 && !File.Exists(Path.Combine(path, ".auriga-desktop-install")))
                return Fail("该文件夹不是空的，也不是 " + Setup.Brand + " 的安装位置。请选择空文件夹或新文件夹。");
            // Per-user install (asInvoker): a folder that needs administrator rights is refused up front.
            string probeDir = path;
            while (!Directory.Exists(probeDir)) probeDir = Path.GetDirectoryName(probeDir);
            string probe = Path.Combine(probeDir, ".auriga-write-test-" + Guid.NewGuid().ToString("N"));
            File.WriteAllText(probe, "");
            File.Delete(probe);
            var drive = new DriveInfo(Path.GetPathRoot(path));
            if (drive.AvailableFreeSpace < unpackedBytes * 2) return Fail(string.Format("磁盘空间不足：至少需要 {0:0} MB 可用空间。", unpackedBytes * 2 / 1048576.0));
        }
        catch (UnauthorizedAccessException)
        {
            return Fail(Setup.Brand + " 按当前用户安装，不申请管理员权限。所选位置需要管理员权限才能写入（例如 C:\\Program Files）。\n\n请选择您有写入权限的位置，例如默认的：\n" + Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Programs", "AurigaDesktop"));
        }
        catch (Exception e) { return Fail("无法使用该位置：" + e.Message); }
        folder.Text = path;
        if (existingRoot != null && !Setup.StopRunning(existingRoot, 120))
            return Fail(Setup.Brand + " 正在运行，无法自动退出。请关闭 " + Setup.Brand + "（托盘菜单“退出”，旧版本请关闭其命令行窗口）后再点“升级”。");
        return true;
    }

    void Report(string step, double fraction)
    {
        if (IsDisposed) return;
        BeginInvoke((Action)delegate
        {
            stepLabel.Text = step;
            int value = (int)Math.Round(Math.Max(0, Math.Min(1, fraction)) * 1000);
            if (value > bar.Value) bar.Value = value;
            percentLabel.Text = (bar.Value / 10) + "%";
        });
    }

    void Log(string line)
    {
        try { File.AppendAllText(logFile, DateTime.Now.ToString("HH:mm:ss ") + Regex.Replace(line, "token=[A-Za-z0-9_\\-]+", "token=REDACTED") + Environment.NewLine, Encoding.UTF8); } catch (Exception) { }
    }

    static readonly Dictionary<string, double[]> Stages = new Dictionary<string, double[]>
    {
        { "verify", new[] { 0.36, 0.40 } }, { "copy", new[] { 0.40, 0.70 } }, { "verify-copy", new[] { 0.70, 0.82 } },
        { "profile", new[] { 0.82, 0.85 } }, { "smoke", new[] { 0.85, 0.95 } }, { "commit", new[] { 0.95, 0.97 } }, { "done", new[] { 0.97, 0.97 } },
    };
    static readonly Dictionary<string, string> StageText = new Dictionary<string, string>
    {
        { "verify", "正在校验安装包签名清单与文件摘要…" }, { "copy", "正在复制程序文件…" }, { "verify-copy", "正在校验已复制的文件…" },
        { "profile", "正在配置用户环境与 Auriga 插件…" }, { "smoke", "正在进行启动自检…" }, { "commit", "正在完成安装…" },
        { "done", "正在创建快捷方式并登记到“应用和功能”…" },
    };

    void StartInstall()
    {
        temp = Path.Combine(Path.GetTempPath(), "auriga-desktop-setup-" + Guid.NewGuid().ToString("N"));
        try { Directory.CreateDirectory(temp); }
        catch (Exception e) { Fail("无法准备安装目录：" + e.Message); return; }
        cancelFile = Path.Combine(temp, "decision");
        installing = true;
        logFile = Path.Combine(Path.GetTempPath(), "AurigaDSH-Setup-" + DateTime.Now.ToString("yyyyMMdd-HHmmss") + ".log");
        Show(2);
        string root = folder.Text;
        bool desktop = desktopLink.Checked;
        bool upgrade = existingRoot != null;
        var worker = new Thread(() =>
        {
            int code = 1;
            string failure = null;
            try
            {
                string package = Path.Combine(temp, "pkg");
                Directory.CreateDirectory(package);
                Log("setup " + version + " -> " + root + (upgrade ? " (upgrade)" : ""));
                FileStream file;
                using (var payload = Setup.OpenPayload(out file))
                using (file)
                using (var zip = new ZipArchive(payload, ZipArchiveMode.Read))
                {
                    long done = 0;
                    foreach (var entry in zip.Entries)
                    {
                        if (cancelRequested) throw new OperationCanceledException();
                        string name = Setup.EntryPath(entry);
                        if (name == null) continue;
                        string target = Path.Combine(package, name.Replace('/', '\\'));
                        Directory.CreateDirectory(Path.GetDirectoryName(target));
                        entry.ExtractToFile(target, false);
                        done += entry.Length;
                        Report("正在解包安装文件… " + Path.GetFileName(name), 0.35 * done / Math.Max(1, unpackedBytes));
                    }
                }
                if (cancelRequested) throw new OperationCanceledException();
                string arguments = "-NoProfile -ExecutionPolicy Bypass -File \"" + Path.Combine(package, "install.ps1") + "\" -InstallDir \"" + root + "\" -Progress -CancelFile \"" + cancelFile + "\"" + (desktop || upgrade ? "" : " -NoDesktopShortcut");
                string lastError = null;
                code = Setup.Run(Setup.PowerShell, arguments, line =>
                {
                    Log(line);
                    if (line.StartsWith("@@progress "))
                    {
                        var m = Regex.Match(line, "\"stage\":\"([a-z-]+)\",\"fraction\":([0-9.eE-]+)");
                        if (!m.Success) return;
                        string stage = m.Groups[1].Value;
                        double fraction = double.Parse(m.Groups[2].Value, System.Globalization.CultureInfo.InvariantCulture);
                        double[] range;
                        if (!Stages.TryGetValue(stage, out range)) return;
                        if (stage == "commit" || stage == "done") canCancel = false;
                        Report(StageText[stage] + (stage == "copy" ? " " + (int)(fraction * 100) + "%" : ""), range[0] + (range[1] - range[0]) * fraction);
                    }
                    else if (line.StartsWith("[stderr] ") && line.Trim().Length > 9) lastError = line.Substring(9);
                });
                if (code != 0) failure = lastError ?? "安装程序退出代码 " + code;
                else Report("安装完成", 1);
            }
            catch (OperationCanceledException) { code = 2; }
            catch (Exception e) { failure = e.Message; code = 1; Log("error " + e); }
            finally
            {
                canCancel = false;
                try { Directory.Delete(temp, true); } catch (Exception) { }
            }
            BeginInvoke((Action)delegate { Finished(code, failure); });
        }) { IsBackground = true };
        worker.Start();
    }

    void Finished(int code, string failure)
    {
        installing = false;
        ExitCode = code;
        succeeded = code == 0;
        Log("exit " + code);
        if (code == 2)
            finishText.Text = "安装已取消，已回滚所有更改" + (existingRoot != null ? "，原版本保持可用" : "") + "。";
        else if (!succeeded)
            finishText.Text = "安装失败：" + failure + "\n\n已自动回滚，" + (existingRoot != null ? "原版本保持可用，数据未受影响。" : "没有留下半装的文件。") + "\n安装日志：" + logFile;
        else
            finishText.Text = Setup.Brand + " " + version + " 已" + (existingRoot != null ? "升级" : "安装") + "到：\n" + folder.Text + "\n\n可从开始菜单“" + Setup.Brand + "”打开；运行时右下角托盘图标提供打开、检查更新、设置和退出。";
        Show(3);
    }

    void Cancel()
    {
        if (!installing) { ExitCode = 2; Close(); return; }
        if (cancelRequested) return;
        if (!canCancel) { MessageBox.Show(this, "正在完成最后的步骤，无法取消，请稍候。", Setup.Brand); return; }
        if (MessageBox.Show(this, "确定要取消安装吗？已完成的步骤会被回滚。", Setup.Brand, MessageBoxButtons.YesNo, MessageBoxIcon.Question) != DialogResult.Yes) return;
        try
        {
            using (File.Open(cancelFile, FileMode.CreateNew, FileAccess.Write)) { }
        }
        catch (Exception e)
        {
            string message = !canCancel || File.Exists(cancelFile)
                ? "正在完成最后的步骤，无法取消，请稍候。"
                : "未能提交取消请求：" + e.Message;
            MessageBox.Show(this, message, Setup.Brand);
            return;
        }
        cancelRequested = true;
        cancel.Enabled = false;
        stepLabel.Text = "正在取消并回滚，请稍候…";
    }

    static void LaunchInstalled(string root)
    {
        try
        {
            string current = File.ReadAllText(Path.Combine(root, "current.txt")).Trim();
            string tray = Path.Combine(root, "versions", current, "auriga", "tray", "AurigaDesktop.exe");
            if (File.Exists(tray)) Process.Start(new ProcessStartInfo(tray) { UseShellExecute = false, WorkingDirectory = root });
            else Process.Start(new ProcessStartInfo(Path.Combine(root, "bin", "auriga-desktop.cmd")) { UseShellExecute = true, WindowStyle = ProcessWindowStyle.Minimized });
        }
        catch (Exception) { }
    }
}
