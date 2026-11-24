// Self-extracting setup. The package zip is appended to this exe, followed by a
// 16-byte trailer: 8 bytes magic "AURZIP01" and the zip length (int64, LE).
// Interactive runs show a standard product wizard:
//   welcome -> license & notice -> location -> tasks -> ready -> progress -> finish.
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
using System.Security.Principal;
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
    public const string Publisher = "Auriga";
    public const string FolderName = "Auriga DSH";
    static readonly Regex SafeVersionId = new Regex("^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$");
    const int StdOutputHandle = -11;
    const int StdErrorHandle = -12;
    [DllImport("kernel32.dll")] static extern bool AttachConsole(int dwProcessId);
    [DllImport("kernel32.dll")] static extern IntPtr GetStdHandle(int nStdHandle);
    [DllImport("kernel32.dll")] static extern bool SetStdHandle(int nStdHandle, IntPtr hHandle);

    static bool HasStdHandle(IntPtr handle)
    {
        return handle != IntPtr.Zero && handle != new IntPtr(-1);
    }

    static void AttachParentConsoleIfNeeded()
    {
        try
        {
            IntPtr origOut = GetStdHandle(StdOutputHandle);
            IntPtr origErr = GetStdHandle(StdErrorHandle);
            bool hasOut = HasStdHandle(origOut);
            bool hasErr = HasStdHandle(origErr);
            if (hasOut && hasErr) return;
            if (!AttachConsole(-1)) return;
            // Preserve whichever stream was explicitly redirected by the caller.
            if (hasOut) SetStdHandle(StdOutputHandle, origOut);
            if (hasErr) SetStdHandle(StdErrorHandle, origErr);
        }
        catch (Exception) { }
    }

    [STAThread]
    static int Main(string[] args)
    {
        ConfigurePaths();
        return Start(args);
    }

    public static void ConfigurePaths()
    {
        // csc targets Framework 4.0 by default. On Windows' Framework 4.6.2+
        // opt into modern System.IO before any path API caches the old defaults.
        // This is process-local; no registry or TEMP changes are required.
        AppContext.SetSwitch("Switch.System.IO.UseLegacyPathHandling", false);
        AppContext.SetSwitch("Switch.System.IO.BlockLongPaths", false);
    }

    public static bool IsElevated()
    {
        try
        {
            using (var identity = WindowsIdentity.GetCurrent())
                return new WindowsPrincipal(identity).IsInRole(WindowsBuiltInRole.Administrator);
        }
        catch (Exception) { return false; }
    }

    public static bool IsSafeVersionId(string id)
    {
        return !string.IsNullOrEmpty(id) && SafeVersionId.IsMatch(id);
    }

    public static string DefaultInstallDir()
    {
        string programFiles = Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles);
        if (!string.IsNullOrEmpty(programFiles))
            return Path.Combine(programFiles, FolderName);
        return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Programs", "AurigaDesktop");
    }

    public static string DefaultDataHome()
    {
        string fromEnv = Environment.GetEnvironmentVariable("AURIGA_DATA_HOME");
        if (!string.IsNullOrEmpty(fromEnv)) return fromEnv;
        return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "AurigaDesktop", "home");
    }

    static int RelaunchElevated(string[] args)
    {
        try
        {
            string exe = Assembly.GetExecutingAssembly().Location;
            var forwarded = new List<string>();
            foreach (string a in args) forwarded.Add(Quote(a));
            forwarded.Add("/ELEVATED");
            var info = new ProcessStartInfo(exe, string.Join(" ", forwarded.ToArray()))
            {
                UseShellExecute = true,
                Verb = "runas",
            };
            using (var p = Process.Start(info))
            {
                if (p == null) return 1;
                p.WaitForExit();
                return p.ExitCode;
            }
        }
        catch (System.ComponentModel.Win32Exception e)
        {
            // ERROR_CANCELLED (1223): user declined the UAC prompt.
            if (e.NativeErrorCode == 1223) return 2;
            MessageBox.Show("无法请求管理员权限：" + e.Message, Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning);
            return 1;
        }
        catch (Exception e)
        {
            MessageBox.Show("无法请求管理员权限：" + e.Message, Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning);
            return 1;
        }
    }

    static int Start(string[] args)
    {
        bool silent = Array.Exists(args, a => a.Equals("/VERYSILENT", StringComparison.OrdinalIgnoreCase));
        if (!silent)
        {
            bool triedElevate = Array.Exists(args, a => a.Equals("/ELEVATED", StringComparison.OrdinalIgnoreCase));
            bool noElevate = Array.Exists(args, a => a.Equals("/NOELEVATE", StringComparison.OrdinalIgnoreCase));
            if (!IsElevated() && !triedElevate && !noElevate)
                return RelaunchElevated(args);
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            using (var wizard = new Wizard()) { Application.Run(wizard); return wizard.ExitCode; }
        }
        AttachParentConsoleIfNeeded();
        args = Array.FindAll(args, a =>
            !a.Equals("/VERYSILENT", StringComparison.OrdinalIgnoreCase) &&
            !a.Equals("/ELEVATED", StringComparison.OrdinalIgnoreCase) &&
            !a.Equals("/NOELEVATE", StringComparison.OrdinalIgnoreCase));
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
        if (name.StartsWith("/") || name.Contains(":") || Array.IndexOf(name.Split('/'), "..") >= 0) throw new IOException("unsafe package entry");
        if (name.Length == 0 || name.EndsWith("/")) return null;
        return name;
    }

    // Shared by the wizard and /VERYSILENT: validate before creating any file.
    public static string FileSystemPath(string path)
    {
        string full = Path.GetFullPath(path);
        if (full.StartsWith("\\\\?\\")) return full;
        return full.StartsWith("\\\\") ? "\\\\?\\UNC\\" + full.Substring(2) : "\\\\?\\" + full;
    }

    public static void Extract(ZipArchive zip, string package, Func<bool> cancelled, Action<string, long> progress)
    {
        foreach (var entry in zip.Entries) EntryPath(entry);
        long done = 0;
        foreach (var entry in zip.Entries)
        {
            if (cancelled()) throw new OperationCanceledException();
            string name = EntryPath(entry);
            if (name == null) continue;
            string target = Path.GetFullPath(Path.Combine(package, name.Replace('/', '\\')));
            if (!target.StartsWith(Path.GetFullPath(package).TrimEnd('\\') + "\\", StringComparison.OrdinalIgnoreCase))
                throw new IOException("unsafe package entry");
            // Explicit extended syntax also works when Windows LongPathsEnabled
            // is off. Only validated, canonical paths reach these file APIs.
            Directory.CreateDirectory(FileSystemPath(Path.GetDirectoryName(target)));
            entry.ExtractToFile(FileSystemPath(target), false);
            done += entry.Length;
            progress(name, done);
        }
    }

    /// Quotes one command-line argument using the Windows (CommandLineToArgv) rules.
    /// A trailing backslash must be doubled, otherwise it escapes the closing quote and
    /// the argument swallows the rest of the command line.
    public static string Quote(string argument)
    {
        if (argument.Length == 0) return "\"\"";
        if (argument.IndexOfAny(new[] { ' ', '\t', '\n', '\v', '"' }) < 0) return argument;
        var builder = new StringBuilder("\"");
        int backslashes = 0;
        foreach (char c in argument)
        {
            if (c == '\\') { backslashes++; continue; }
            if (c == '"')
            {
                builder.Append('\\', backslashes * 2 + 1).Append('"');
                backslashes = 0;
                continue;
            }
            builder.Append('\\', backslashes).Append(c);
            backslashes = 0;
        }
        builder.Append('\\', backslashes * 2).Append('"');
        return builder.ToString();
    }

    static int Install(string[] args)
    {
        string temp = Path.Combine(Path.GetTempPath(), "auriga-desktop-setup-" + Guid.NewGuid().ToString("N"));
        try
        {
            Directory.CreateDirectory(temp);
            string package = Path.Combine(temp, "pkg");
            Directory.CreateDirectory(package);
            Console.WriteLine("Unpacking " + Brand + " setup...");
            FileStream file;
            using (var payload = OpenPayload(out file))
            using (file)
            using (var zip = new ZipArchive(payload, ZipArchiveMode.Read))
                Extract(zip, package, () => false, (name, done) => { });
            Console.WriteLine("Installing " + Brand + "...");
            string forwarded = string.Join(" ", Array.ConvertAll(args, Quote));
            return Run(PowerShell, "-NoProfile -ExecutionPolicy Bypass -File \"" + Path.Combine(package, "install.ps1") + "\" " + forwarded, null);
        }
        catch (Exception e)
        {
            Console.Error.WriteLine(e.Message);
            return 1;
        }
        finally
        {
            try { Directory.Delete(FileSystemPath(temp), true); } catch (Exception) { }
        }
    }

    public static string PowerShell { get { return Path.Combine(Environment.SystemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe"); } }

    /// Stable per-user directory for setup logs, falling back to TEMP when it is unusable.
    /// A failed install must always leave a log the user can find and open.
    public static string LogDirectory()
    {
        try
        {
            string directory = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                "AurigaDesktop", "logs");
            Directory.CreateDirectory(directory);
            return directory;
        }
        catch (Exception) { return Path.GetTempPath(); }
    }

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
        // The installer core checks the legacy session and writer identities.
        return true;
    }

    public static bool OwnsStartupCommand(string command, string root)
    {
        if (string.IsNullOrEmpty(command) || string.IsNullOrEmpty(root)) return false;
        string canonical = Path.GetFullPath(root).TrimEnd('\\');
        string script = Path.Combine(canonical, "bin", "auriga-autostart.ps1");
        if (command.IndexOf("\"" + script + "\"", StringComparison.OrdinalIgnoreCase) >= 0) return true;
        string versions = Regex.Escape(Path.Combine(canonical, "versions").TrimEnd('\\'));
        return Regex.IsMatch(command, "^\"(" + versions + "\\\\[A-Za-z0-9._-]+\\\\auriga\\\\tray\\\\AurigaDesktop\\.exe)\" /autostart$", RegexOptions.IgnoreCase);
    }

    public static string LicenseAndNoticeText()
    {
        return string.Join("\r\n", new[]
        {
            "Auriga DSH 软件许可协议与使用声明",
            "============================================================",
            "",
            "在安装或使用 Auriga DSH 桌面客户端（以下简称“本软件”）前，请仔细阅读以下许可协议与产品声明。选择“我已阅读并同意”并继续安装，即表示您已阅读、理解并同意遵守以下条款。",
            "",
            "一、软件许可协议",
            "1. Auriga 项目采用双重许可模式：",
            "   - Auriga 主体代码依据 GNU Affero General Public License v3.0 (AGPL-3.0) 开源协议授权；",
            "   - 客户端插件（auriga-dsh）依据 MIT 开源协议授权；",
            "   - 如需用于商业闭源集成或定制交付，可联系版权方获取商业授权。",
            "2. 本安装包内嵌的第三方运行时及组件（包括 Node.js、只读 Python 运行环境、DSH 运行时等）分别遵循其各自的开源许可证，完整许可文本随安装包部署在程序目录中。",
            "",
            "二、管理员权限与系统变更说明",
            "1. 本安装程序以管理员权限运行，用于支持安装至系统程序目录（如 C:\\Program Files\\Auriga DSH）或您指定的其他本地磁盘路径。",
            "2. 安装过程中会执行以下操作：",
            "   - 将本软件程序文件及内置运行环境部署到您选择的目标目录，并为当前用户授予该目录的必要维护权限（内置 Python 运行时目录保持只读防篡改保护）；",
            "   - 创建开始菜单快捷方式（含卸载入口），并按您的选择创建桌面快捷方式或设置开机自动启动；",
            "   - 在 Windows“应用和功能”中登记卸载信息，方便随时完整卸载。",
            "3. 本软件不会修改系统全局 PATH 环境变量，也不会覆盖或改动您已有的 ~/.dsh 目录；您的个人配置、会话记录与凭据将独立保存在用户数据目录中。",
            "",
            "三、数据归属、网络通信与更新安全声明",
            "1. 您的本地工作空间配置与会话数据默认保存在本机用户数据目录内，卸载时默认保留，您可自行选择是否一并清除。",
            "2. 仅当您主动登录企业/团队平台或启用检查更新时，本软件才会与您所配置的平台地址通信。",
            "3. 客户端在线更新包均通过安装包内置的发行公钥（Ed25519）进行完整性与来源签名校验，并拒绝版本回退或篡改包。",
            "",
            "四、免责声明与版权信息",
            "本软件按“现状（AS IS）”提供，不附带任何明示或暗示的担保（包括但不限于适销性、特定用途适用性及不侵权担保）。在适用法律允许的最大范围内，版权持有人及贡献者不对因使用或无法使用本软件而导致的任何直接、间接、附带或后果性损失承担责任。",
            "",
            "Copyright (c) Auriga Contributors. All rights reserved."
        });
    }
}

sealed class Wizard : Form
{
    const int W = 660, H = 470;
    const int SidebarW = 196;
    const int HeaderH = 76;
    const int FooterH = 54;

    const int PageWelcome = 0;
    const int PageLicense = 1;
    const int PageLocation = 2;
    const int PageTasks = 3;
    const int PageReady = 4;
    const int PageProgress = 5;
    const int PageFinish = 6;

    static readonly Color BrandDark = Color.FromArgb(22, 35, 58);
    static readonly Color BorderGray = Color.FromArgb(220, 224, 232);
    static readonly Color FooterBg = Color.FromArgb(245, 246, 248);
    static readonly Color TitleText = Color.FromArgb(24, 30, 42);
    static readonly Color BodyText = Color.FromArgb(52, 60, 72);
    static readonly Color MutedText = Color.FromArgb(92, 102, 118);
    static readonly Color DangerText = Color.FromArgb(196, 43, 28);

    public int ExitCode = 2;

    // Chrome panels
    readonly Panel sidebar = new Panel { Left = 0, Top = 0, Width = SidebarW, Height = H - FooterH, BackColor = BrandDark };
    readonly PictureBox sideLogo = new PictureBox { Left = 28, Top = 36, Width = 56, Height = 56, SizeMode = PictureBoxSizeMode.Zoom, BackColor = Color.Transparent };
    readonly Label sideTitle = new Label { Left = 28, Top = 104, Width = SidebarW - 44, Height = 30, ForeColor = Color.White, Font = new Font("Microsoft YaHei UI", 14, FontStyle.Bold), Text = Setup.Brand };
    readonly Label sideVersion = new Label { Left = 28, Top = 136, Width = SidebarW - 44, Height = 22, ForeColor = Color.FromArgb(176, 196, 226), Font = new Font("Microsoft YaHei UI", 9) };
    readonly Label sideDesc = new Label { Left = 28, Top = 176, Width = SidebarW - 44, Height = 140, ForeColor = Color.FromArgb(154, 174, 204), Font = new Font("Microsoft YaHei UI", 8.5f), Text = "桌面工作空间与插件运行时\n\n· 独立运行环境\n· 签名校验与安全更新\n· 原子安装与自动回滚" };

    readonly Panel header = new Panel { Left = 0, Top = 0, Width = W, Height = HeaderH, BackColor = Color.White };
    readonly Label heading = new Label { Left = 24, Top = 14, Width = 540, Height = 26, Font = new Font("Microsoft YaHei UI", 11.5f, FontStyle.Bold), ForeColor = TitleText };
    readonly Label subheading = new Label { Left = 26, Top = 43, Width = 540, Height = 22, ForeColor = MutedText };
    readonly PictureBox logo = new PictureBox { Left = W - 62, Top = 18, Width = 36, Height = 36, SizeMode = PictureBoxSizeMode.Zoom, Anchor = AnchorStyles.Top | AnchorStyles.Right };
    readonly Panel headerLine = new Panel { Left = 0, Top = HeaderH, Width = W, Height = 1, BackColor = BorderGray };

    // Keep `body` (full-width pages 1..5) and `sidebarBody` (sidebar pages 0, 6) separate
    // so switching between sidebar and full-width pages never resizes hidden panels' Anchors.
    readonly Panel body = new Panel { Left = 0, Top = HeaderH + 1, Width = W, Height = H - HeaderH - 1 - FooterH, BackColor = SystemColors.Control };
    readonly Panel sidebarBody = new Panel { Left = SidebarW, Top = 0, Width = W - SidebarW, Height = H - FooterH, BackColor = Color.White, Visible = false };

    readonly Panel footerLine = new Panel { Left = 0, Top = H - FooterH, Width = W, Height = 1, BackColor = BorderGray };
    readonly Panel footerPanel = new Panel { Left = 0, Top = H - FooterH + 1, Width = W, Height = FooterH - 1, BackColor = FooterBg };
    readonly Label footerBrand = new Label { Left = 20, Top = 18, Width = 240, Height = 20, ForeColor = Color.FromArgb(140, 148, 160), Font = new Font("Microsoft YaHei UI", 8.5f), Text = Setup.Brand + " 安装向导" };
    readonly Button back = new Button { Text = "< 上一步(&B)", Width = 98, Height = 29, FlatStyle = FlatStyle.System };
    readonly Button next = new Button { Text = "下一步(&N) >", Width = 98, Height = 29, FlatStyle = FlatStyle.System };
    readonly Button cancel = new Button { Text = "取消", Width = 88, Height = 29, FlatStyle = FlatStyle.System };

    // All 7 page containers are pre-mounted inside `body` before Initialize ends so
    // WinForms AutoScaleMode.Dpi scales every page's controls consistently at 125%/150%/200% DPI.
    readonly Panel[] pagePanels = new Panel[7];

    // Welcome controls (hosted in a single-column auto-sizing TableLayoutPanel so text
    // wraps cleanly to the live panel width and never clips or overlaps at any DPI)
    readonly Label welcomeTitle = new Label { AutoSize = true, Dock = DockStyle.Fill, Margin = new Padding(0, 0, 0, 14), Font = new Font("Microsoft YaHei UI", 14, FontStyle.Bold), ForeColor = TitleText };
    readonly Label welcomeIntro = new Label { AutoSize = true, Dock = DockStyle.Fill, Margin = new Padding(0, 0, 0, 18), ForeColor = BodyText };
    readonly Label welcomeHint = new Label { AutoSize = true, Dock = DockStyle.Fill, Margin = new Padding(0, 0, 0, 0), ForeColor = MutedText };

    // License controls
    readonly TextBox licenseBox = new TextBox
    {
        Left = 28, Top = 16, Width = W - 56, Height = 224,
        Multiline = true, ReadOnly = true, ScrollBars = ScrollBars.Vertical,
        BackColor = Color.White, BorderStyle = BorderStyle.FixedSingle,
        Font = new Font("Microsoft YaHei UI", 9), TabStop = false,
        Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right | AnchorStyles.Bottom
    };
    readonly RadioButton acceptLicense = new RadioButton { Left = 28, Top = 252, Width = W - 56, Height = 28, Text = "我已阅读并同意上述许可协议与使用声明(&A)", Anchor = AnchorStyles.Bottom | AnchorStyles.Left | AnchorStyles.Right };
    readonly RadioButton declineLicense = new RadioButton { Left = 28, Top = 284, Width = W - 56, Height = 28, Text = "我不同意此协议(&D)", Checked = true, Anchor = AnchorStyles.Bottom | AnchorStyles.Left | AnchorStyles.Right };

    // Location controls (keep field name `folder` for test harness compatibility)
    readonly Label locationPrompt = new Label { Left = 28, Top = 24, Width = W - 56, Height = 28, ForeColor = TitleText, Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly TextBox folder = new TextBox { Left = 28, Top = 58, Width = 492, Height = 24, Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly Button browse = new Button { Text = "浏览(&R)…", Left = 530, Top = 56, Width = 102, Height = 28, FlatStyle = FlatStyle.System, Anchor = AnchorStyles.Top | AnchorStyles.Right };
    readonly Label spaceInfo = new Label { Left = 28, Top = 98, Width = W - 56, Height = 56, ForeColor = MutedText, Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly Label dataHomeInfo = new Label { Left = 28, Top = 164, Width = W - 56, Height = 84, ForeColor = MutedText, Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };

    // Tasks controls
    readonly Label tasksShortcutHeading = new Label { Left = 28, Top = 26, Width = W - 56, Height = 24, Font = new Font("Microsoft YaHei UI", 9, FontStyle.Bold), ForeColor = TitleText, Text = "快捷方式：", Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly CheckBox desktopLink = new CheckBox { Left = 44, Top = 58, Width = W - 72, Height = 28, Text = "创建桌面快捷方式(&D)", Checked = true, Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly CheckBox startMenuLink = new CheckBox { Left = 44, Top = 92, Width = W - 72, Height = 28, Text = "创建开始菜单快捷方式及卸载入口（默认启用）", Checked = true, Enabled = false, Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly Label tasksStartupHeading = new Label { Left = 28, Top = 136, Width = W - 56, Height = 24, Font = new Font("Microsoft YaHei UI", 9, FontStyle.Bold), ForeColor = TitleText, Text = "启动选项：", Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly CheckBox autostartBox = new CheckBox { Left = 44, Top = 168, Width = W - 72, Height = 28, Text = "开机时自动启动 " + Setup.Brand + "(&S)", Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly Label tasksHint = new Label { Left = 28, Top = 216, Width = W - 56, Height = 36, ForeColor = MutedText, Text = "以上选项在安装完成后均可随时通过系统托盘菜单调整。", Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };

    // Ready summary controls
    readonly Label readyPrompt = new Label { Left = 28, Top = 18, Width = W - 56, Height = 26, ForeColor = TitleText, Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly TextBox summaryBox = new TextBox
    {
        Left = 28, Top = 50, Width = W - 56, Height = 244,
        Multiline = true, ReadOnly = true, ScrollBars = ScrollBars.Vertical,
        BackColor = Color.White, BorderStyle = BorderStyle.FixedSingle,
        Font = new Font("Microsoft YaHei UI", 9), TabStop = false,
        Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right | AnchorStyles.Bottom
    };

    // Progress controls (keep field names `bar` for test harness compatibility)
    readonly Label stepLabel = new Label { Left = 28, Top = 24, Width = W - 56, Height = 26, Font = new Font("Microsoft YaHei UI", 9.5f, FontStyle.Bold), Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly ProgressBar bar = new ProgressBar { Left = 28, Top = 56, Width = W - 56, Height = 22, Maximum = 1000, Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly Label percentLabel = new Label { Left = 28, Top = 86, Width = W - 56, Height = 22, ForeColor = MutedText, Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right };
    readonly TextBox progressDetail = new TextBox
    {
        Left = 28, Top = 118, Width = W - 56, Height = 196,
        Multiline = true, ReadOnly = true, ScrollBars = ScrollBars.Vertical,
        BackColor = Color.White, BorderStyle = BorderStyle.FixedSingle,
        Font = new Font("Consolas", 8.5f), TabStop = false,
        Anchor = AnchorStyles.Top | AnchorStyles.Left | AnchorStyles.Right | AnchorStyles.Bottom
    };

    // Finish controls (keep field names `finishText`, `openLog` for test harness compatibility)
    readonly Label finishTitle = new Label { AutoSize = true, Dock = DockStyle.Fill, Margin = new Padding(0, 0, 0, 14), Font = new Font("Microsoft YaHei UI", 14, FontStyle.Bold), ForeColor = TitleText };
    readonly Label finishText = new Label { AutoSize = true, Dock = DockStyle.Fill, Margin = new Padding(0, 0, 0, 16), ForeColor = BodyText };
    readonly CheckBox launch = new CheckBox { AutoSize = true, Margin = new Padding(0, 0, 0, 8), Text = "立即启动 " + Setup.Brand + "(&L)", Checked = true };
    readonly LinkLabel openLog = new LinkLabel { AutoSize = true, Margin = new Padding(0, 0, 0, 8), Text = "打开安装日志", Visible = false };

    readonly object logLock = new object();
    int page;
    string version = "?", existingRoot, existingVersion, logFile, cancelFile, temp;
    long unpackedBytes;
    volatile bool cancelRequested, canCancel = true;
    bool installing, succeeded;
    string lastDetailStage;

    public Wizard()
    {
        SuspendLayout();
        Text = Setup.Brand + " 安装程序";
        AutoScaleDimensions = new SizeF(96F, 96F);
        AutoScaleMode = AutoScaleMode.Dpi;
        ClientSize = new Size(W, H);
        FormBorderStyle = FormBorderStyle.FixedDialog;
        MaximizeBox = false;
        StartPosition = FormStartPosition.CenterScreen;
        Font = new Font("Microsoft YaHei UI", 9);

        try
        {
            Icon = Icon.ExtractAssociatedIcon(Assembly.GetExecutingAssembly().Location);
            var bmp48 = new Icon(Icon, 48, 48).ToBitmap();
            logo.Image = bmp48;
            sideLogo.Image = bmp48;
        }
        catch (Exception) { }

        sidebar.Controls.AddRange(new Control[] { sideLogo, sideTitle, sideVersion, sideDesc });
        header.Controls.AddRange(new Control[] { heading, subheading, logo });

        back.Location = new Point(W - 322, 12);
        next.Location = new Point(W - 214, 12);
        cancel.Location = new Point(W - 106, 12);
        footerPanel.Controls.AddRange(new Control[] { footerBrand, back, next, cancel });

        for (int i = 0; i < pagePanels.Length; i++)
        {
            bool withSidebar = i == PageWelcome || i == PageFinish;
            pagePanels[i] = new Panel
            {
                Size = withSidebar ? new Size(W - SidebarW, H - FooterH) : new Size(W, H - HeaderH - 1 - FooterH),
                Dock = DockStyle.Fill,
                BackColor = withSidebar ? Color.White : SystemColors.Control,
                Visible = false
            };
        }

        pagePanels[PageWelcome].Controls.Add(MakeVerticalStack(welcomeTitle, welcomeIntro, welcomeHint));
        pagePanels[PageLicense].Controls.AddRange(new Control[] { licenseBox, acceptLicense, declineLicense });
        pagePanels[PageLocation].Controls.AddRange(new Control[] { locationPrompt, folder, browse, spaceInfo, dataHomeInfo });
        pagePanels[PageTasks].Controls.AddRange(new Control[] { tasksShortcutHeading, desktopLink, startMenuLink, tasksStartupHeading, autostartBox, tasksHint });
        pagePanels[PageReady].Controls.AddRange(new Control[] { readyPrompt, summaryBox });
        pagePanels[PageProgress].Controls.AddRange(new Control[] { stepLabel, bar, percentLabel, progressDetail });
        pagePanels[PageFinish].Controls.Add(MakeVerticalStack(finishTitle, finishText, openLog, launch));
        sidebarBody.Controls.AddRange(new Control[] { pagePanels[PageWelcome], pagePanels[PageFinish] });
        body.Controls.AddRange(new Control[] { pagePanels[PageLicense], pagePanels[PageLocation], pagePanels[PageTasks], pagePanels[PageReady], pagePanels[PageProgress] });

        back.Click += delegate { if (page > PageWelcome && page <= PageReady) Show(page - 1); };
        next.Click += delegate { Next(); };
        cancel.Click += delegate { Cancel(); };

        acceptLicense.CheckedChanged += delegate { if (page == PageLicense) next.Enabled = acceptLicense.Checked; };
        declineLicense.CheckedChanged += delegate { if (page == PageLicense) next.Enabled = acceptLicense.Checked; };
        licenseBox.Text = Setup.LicenseAndNoticeText();

        folder.TextChanged += delegate { if (page == PageLocation) UpdateSpaceInfo(); };
        browse.Click += delegate
        {
            using (var dialog = new FolderBrowserDialog { Description = "选择 " + Setup.Brand + " 的安装位置", ShowNewFolderButton = true })
            {
                string current = folder.Text.Trim();
                dialog.SelectedPath = Directory.Exists(current)
                    ? current
                    : Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles);
                if (dialog.ShowDialog(this) == DialogResult.OK)
                {
                    string selected = dialog.SelectedPath;
                    bool nonEmptyOther = false;
                    try
                    {
                        nonEmptyOther = Directory.Exists(selected)
                            && Directory.GetFileSystemEntries(selected).Length > 0
                            && !File.Exists(Path.Combine(selected, ".auriga-desktop-install"));
                    }
                    catch (Exception) { nonEmptyOther = true; }
                    folder.Text = nonEmptyOther ? Path.Combine(selected, Setup.FolderName) : selected;
                }
            }
        };

        Controls.AddRange(new Control[] { sidebar, sidebarBody, header, headerLine, body, footerLine, footerPanel });
        AcceptButton = next;
        CancelButton = cancel;
        FormClosing += delegate (object s, FormClosingEventArgs e) { if (installing) { e.Cancel = true; Cancel(); } };
        openLog.LinkClicked += delegate { OpenLog(); };

        ReadPackage();
        DetectExisting();
        sideVersion.Text = "版本 " + version;
        folder.Text = existingRoot ?? Setup.DefaultInstallDir();

        ResumeLayout(false);
        PerformLayout();
        Show(PageWelcome);
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
            if (existingRoot != null)
            {
                using (var run = Registry.CurrentUser.OpenSubKey("Software\\Microsoft\\Windows\\CurrentVersion\\Run"))
                {
                    if (run != null)
                    {
                        string val = Convert.ToString(run.GetValue(RegistryName, ""));
                        if (Setup.OwnsStartupCommand(val, existingRoot))
                            autostartBox.Checked = true;
                    }
                }
            }
        }
        catch (Exception) { }
    }

    static TableLayoutPanel MakeVerticalStack(params Control[] controls)
    {
        var table = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            AutoScroll = true,
            ColumnCount = 1,
            RowCount = controls.Length + 1,
            Padding = new Padding(24, 24, 24, 16),
            BackColor = Color.White
        };
        table.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        for (int i = 0; i < controls.Length; i++)
        {
            table.RowStyles.Add(new RowStyle(SizeType.AutoSize));
            table.Controls.Add(controls[i], 0, i);
        }
        table.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));
        return table;
    }

    protected override void OnLoad(EventArgs e)
    {
        base.OnLoad(e);
        ApplyChrome(page == PageWelcome || page == PageFinish);
    }

    protected override void OnResize(EventArgs e)
    {
        base.OnResize(e);
        if (IsHandleCreated) ApplyChrome(page == PageWelcome || page == PageFinish);
    }

    void Header(string title, string sub) { heading.Text = title; subheading.Text = sub; }

    void ApplyChrome(bool useSidebar)
    {
        sidebar.Visible = useSidebar;
        sidebarBody.Visible = useSidebar;
        header.Visible = !useSidebar;
        headerLine.Visible = !useSidebar;
        body.Visible = !useSidebar;
        // Derive bounds from live scaled chrome controls so 125%/150%/200% DPI never clips.
        int contentBottom = footerLine.Top;
        int sideLeft = sidebar.Right;
        sidebarBody.Bounds = new Rectangle(sideLeft, 0, Math.Max(0, ClientSize.Width - sideLeft), Math.Max(0, contentBottom));
        headerLine.Top = header.Bottom;
        int fullTop = headerLine.Bottom;
        body.Bounds = new Rectangle(0, fullTop, ClientSize.Width, Math.Max(0, contentBottom - fullTop));
    }

    long RequiredBytes()
    {
        return Math.Max(unpackedBytes * 2, 1024 * 1024);
    }

    void UpdateSpaceInfo()
    {
        long neededBytes = RequiredBytes();
        double requiredMb = Math.Max(1.0, neededBytes / 1048576.0);
        string path = folder.Text.Trim();
        try
        {
            if (path.Length > 0 && Path.IsPathRooted(path) && !path.StartsWith("\\\\"))
            {
                string root = Path.GetPathRoot(Path.GetFullPath(path));
                var drive = new DriveInfo(root);
                if (drive.IsReady)
                {
                    double availGb = drive.AvailableFreeSpace / 1073741824.0;
                    bool enough = drive.AvailableFreeSpace >= neededBytes;
                    spaceInfo.ForeColor = enough ? MutedText : DangerText;
                    spaceInfo.Text = string.Format(
                        "至少需要可用磁盘空间：约 {0:0} MB\n目标磁盘（{1}）当前可用空间：{2:0.00} GB{3}",
                        requiredMb, root.TrimEnd('\\'), availGb, enough ? "" : "（空间不足）");
                    return;
                }
            }
        }
        catch (Exception) { }
        spaceInfo.ForeColor = MutedText;
        spaceInfo.Text = string.Format("至少需要可用磁盘空间：约 {0:0} MB", requiredMb);
    }

    string BuildSummary()
    {
        var lines = new List<string>();
        lines.Add("安装模式：");
        lines.Add(existingRoot != null
            ? "    升级现有安装（" + (existingVersion.Length > 0 ? existingVersion + " -> " : "") + version + "）"
            : "    全新安装 " + Setup.Brand + " " + version);
        lines.Add("");
        lines.Add("目标位置：");
        lines.Add("    " + folder.Text.Trim());
        lines.Add("");
        lines.Add("用户数据目录（配置、会话与登录状态）：");
        lines.Add("    " + Setup.DefaultDataHome());
        lines.Add("");
        lines.Add("附加任务：");
        lines.Add("    · 创建开始菜单快捷方式及卸载入口");
        if (existingRoot != null)
            lines.Add("    · 桌面快捷方式：沿用现有设置");
        else if (desktopLink.Checked)
            lines.Add("    · 创建桌面快捷方式");
        else
            lines.Add("    · 不创建桌面快捷方式");
        lines.Add(autostartBox.Checked
            ? "    · 开机时自动启动 " + Setup.Brand
            : "    · 不开启开机自动启动");
        lines.Add("");
        lines.Add(string.Format("至少需要可用磁盘空间：约 {0:0} MB", Math.Max(1.0, RequiredBytes() / 1048576.0)));
        return string.Join("\r\n", lines.ToArray());
    }

    void Show(int target)
    {
        page = target;
        back.Visible = page >= PageLicense && page <= PageReady;
        next.Enabled = true;
        cancel.Enabled = true;

        bool useSidebar = page == PageWelcome || page == PageFinish;
        ApplyChrome(useSidebar);

        for (int i = 0; i < pagePanels.Length; i++)
            pagePanels[i].Visible = (i == page);

        if (page == PageWelcome)
        {
            welcomeTitle.Text = existingRoot != null ? "欢迎升级 " + Setup.Brand : "欢迎使用 " + Setup.Brand + " 安装向导";
            welcomeIntro.Text = existingRoot != null
                ? "检测到您的计算机已安装 " + Setup.Brand + (existingVersion.Length > 0 ? " " + existingVersion : "") + "。\n\n本向导将把现有安装升级至 " + version + "，并完整保留您的个人配置、历史会话和登录状态。安装开始前会自动安全退出正在运行的 " + Setup.Brand + "。"
                : "本向导将在您的计算机上安装 " + Setup.Brand + " " + version + "。\n\n" + Setup.Brand + " 内置本地 DSH 工作空间、Auriga 插件、Node.js 以及只读保护的 Python 运行环境，安装完成后可通过开始菜单、桌面快捷方式或系统托盘启动。";
            welcomeHint.Text = "建议在继续之前关闭其他正在运行的 " + Setup.Brand + " 窗口。\n\n单击“下一步”继续，或单击“取消”退出安装向导。";
            next.Text = "下一步(&N) >";
        }
        else if (page == PageLicense)
        {
            Header("许可协议与使用声明", "在继续安装前，请仔细阅读以下重要信息。");
            next.Text = "下一步(&N) >";
            next.Enabled = acceptLicense.Checked;
        }
        else if (page == PageLocation)
        {
            Header("选择目标位置", existingRoot != null
                ? "升级将沿用当前安装位置，以保持配置与快捷方式一致。"
                : "选择 " + Setup.Brand + " 的安装目录，支持安装在系统盘或其他本地磁盘。");
            locationPrompt.Text = existingRoot != null
                ? "将升级以下位置的现有安装："
                : "将 " + Setup.Brand + " 安装到以下文件夹（点击“浏览”可选择其他盘符或目录）：";
            folder.Enabled = browse.Enabled = existingRoot == null;
            UpdateSpaceInfo();
            dataHomeInfo.Text = "用户数据目录：" + Setup.DefaultDataHome()
                + "\n（个人配置、会话与登录凭据独立存放于此，不与程序目录混淆，卸载时默认保留。）";
            next.Text = "下一步(&N) >";
        }
        else if (page == PageTasks)
        {
            Header("选择附加任务", "选择安装 " + Setup.Brand + " 时希望同时执行的附加选项。");
            desktopLink.Enabled = existingRoot == null;
            desktopLink.Text = existingRoot != null ? "桌面快捷方式（升级时沿用现有设置）" : "创建桌面快捷方式(&D)";
            next.Text = "下一步(&N) >";
        }
        else if (page == PageReady)
        {
            Header("准备安装", "安装向导现已准备好在您的计算机上" + (existingRoot != null ? "升级" : "安装") + " " + Setup.Brand + "。");
            readyPrompt.Text = "请确认以下安装设置。单击“" + (existingRoot != null ? "升级" : "安装") + "”开始，或单击“上一步”修改设置：";
            summaryBox.Text = BuildSummary();
            next.Text = existingRoot != null ? "升级(&U)" : "安装(&I)";
        }
        else if (page == PageProgress)
        {
            Header(existingRoot != null ? "正在升级" : "正在安装", "请稍候，安装向导正在将 " + Setup.Brand + " 部署到您的计算机。");
            back.Visible = false;
            next.Enabled = false;
            next.Text = "下一步(&N) >";
        }
        else
        {
            finishTitle.Text = succeeded
                ? "已完成 " + Setup.Brand + " 安装向导"
                : (ExitCode == 2 ? "安装已取消" : "安装未能完成");
            openLog.Visible = !succeeded && ExitCode != 2 && !string.IsNullOrEmpty(logFile) && File.Exists(logFile);
            launch.Visible = succeeded;
            next.Text = "完成(&F)";
            next.Enabled = true;
            cancel.Enabled = false;
            back.Visible = false;
        }
    }

    void Next()
    {
        if (page == PageWelcome) { Show(PageLicense); return; }
        if (page == PageLicense)
        {
            if (!acceptLicense.Checked) return;
            Show(PageLocation);
            return;
        }
        if (page == PageLocation)
        {
            if (ValidateLocation(false)) Show(PageTasks);
            return;
        }
        if (page == PageTasks) { Show(PageReady); return; }
        if (page == PageReady)
        {
            if (ValidateLocation(true)) StartInstall();
            return;
        }
        if (page == PageFinish)
        {
            if (succeeded && launch.Checked) LaunchInstalled(folder.Text);
            Close();
        }
    }

    bool Fail(string message) { MessageBox.Show(this, message, Setup.Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning); return false; }

    bool ValidateLocation(bool stopRunning)
    {
        string path = folder.Text.Trim();
        try
        {
            if (path.Length == 0 || !Path.IsPathRooted(path)) return Fail("请选择一个完整的本机安装路径。");
            if (path.StartsWith("\\\\")) return Fail("不支持网络路径，请选择本机磁盘上的文件夹。");
            path = Path.GetFullPath(path);
            if (path.StartsWith("\\\\")) return Fail("不支持网络路径，请选择本机磁盘上的文件夹。");
            if (Path.GetPathRoot(path).TrimEnd('\\').Equals(path.TrimEnd('\\'), StringComparison.OrdinalIgnoreCase))
                return Fail("不能直接安装到磁盘根目录，请选择或输入一个子文件夹（例如 D:\\Auriga DSH）。");

            var drive = new DriveInfo(Path.GetPathRoot(path));
            if (drive.DriveType == DriveType.Network)
                return Fail("不支持网络映射盘，请选择本机固定磁盘上的文件夹。");

            if (Directory.Exists(path) && Directory.GetFileSystemEntries(path).Length > 0 && !File.Exists(Path.Combine(path, ".auriga-desktop-install")))
                return Fail("该文件夹不是空的，也不是 " + Setup.Brand + " 的安装位置。请选择空文件夹或新文件夹。");

            string driveRoot = Path.GetPathRoot(path);
            if (string.IsNullOrEmpty(driveRoot) || !Directory.Exists(driveRoot))
                return Fail("目标驱动器不存在或不可访问，请检查路径是否正确。");

            // Do not write a synthetic dot-file probe from the unsigned setup stub:
            // enterprise EDR / controlled-folder policies often block unsigned stubs from
            // creating hidden dot-files on secondary drives while allowing the elevated
            // install.ps1 (PowerShell + signed Node) transactional install to succeed.
            long needed = RequiredBytes();
            if (drive.AvailableFreeSpace < needed)
                return Fail(string.Format("目标磁盘空间不足：至少需要 {0:0} MB 可用空间。", needed / 1048576.0));
        }
        catch (UnauthorizedAccessException e)
        {
            return Fail(Setup.IsElevated()
                ? "当前已以管理员身份运行，但无法访问目标位置：" + path + "\n\n（" + e.Message + "）请检查目录权限或更换其他路径。"
                : "没有足够权限访问目标位置：" + path + "\n\n请以管理员身份重新运行安装程序，或更换其他路径。");
        }
        catch (Exception e) { return Fail("无法使用该安装位置：" + e.Message); }

        folder.Text = path;
        if (stopRunning && existingRoot != null && !Setup.StopRunning(existingRoot, 120))
            return Fail(Setup.Brand + " 正在运行，无法自动退出。请先关闭 " + Setup.Brand + "（托盘菜单“退出”，旧版本请关闭其命令行窗口）后再点击“升级”。");
        return true;
    }

    void AppendDetail(string text)
    {
        if (IsDisposed || string.IsNullOrEmpty(text)) return;
        BeginInvoke((Action)delegate
        {
            if (progressDetail.TextLength > 0) progressDetail.AppendText("\r\n");
            progressDetail.AppendText(DateTime.Now.ToString("HH:mm:ss ") + text);
        });
    }

    void Report(string step, double fraction)
    {
        if (IsDisposed) return;
        BeginInvoke((Action)delegate
        {
            stepLabel.Text = step;
            int value = (int)Math.Round(Math.Max(0, Math.Min(1, fraction)) * 1000);
            if (value > bar.Value) bar.Value = value;
            percentLabel.Text = "总进度：" + (bar.Value / 10) + "%";
        });
    }

    void Log(string line)
    {
        lock (logLock)
        {
            try { File.AppendAllText(logFile, DateTime.Now.ToString("HH:mm:ss ") + Regex.Replace(line, "token=[A-Za-z0-9_\\-]+", "token=REDACTED") + Environment.NewLine, Encoding.UTF8); } catch (Exception) { }
        }
    }

    static readonly Dictionary<string, double[]> Stages = new Dictionary<string, double[]>
    {
        { "verify", new[] { 0.36, 0.40 } }, { "copy", new[] { 0.40, 0.70 } }, { "verify-copy", new[] { 0.70, 0.82 } },
        { "profile", new[] { 0.82, 0.85 } }, { "smoke", new[] { 0.85, 0.95 } }, { "commit", new[] { 0.95, 0.97 } }, { "done", new[] { 0.97, 0.97 } },
    };
    static readonly Dictionary<string, string> StageText = new Dictionary<string, string>
    {
        { "verify", "正在校验安装包签名清单与文件摘要…" }, { "copy", "正在复制程序文件…" }, { "verify-copy", "正在校验已复制的文件…" },
        { "profile", "正在配置用户环境与 Auriga 插件…" }, { "smoke", "正在进行启动自检…" }, { "commit", "正在提交安装事务…" },
        { "done", "正在创建快捷方式并登记到“应用和功能”…" },
    };

    void StartInstall()
    {
        temp = Path.Combine(Path.GetTempPath(), "auriga-desktop-setup-" + Guid.NewGuid().ToString("N"));
        try { Directory.CreateDirectory(temp); }
        catch (Exception e) { Fail("无法准备临时安装目录：" + e.Message); return; }
        cancelFile = Path.Combine(temp, "decision");
        installing = true;
        lastDetailStage = null;
        progressDetail.Clear();
        // Logs go to a stable per-user directory, not TEMP: TEMP may be redirected, cleaned by
        // policy, or already unwritable -- exactly when a log matters. Fall back to TEMP only if
        // the stable location cannot be created.
        logFile = Path.Combine(Setup.LogDirectory(), "setup-" + DateTime.Now.ToString("yyyyMMdd-HHmmss") + ".log");
        Log("setup " + version + " -> " + folder.Text + " elevated=" + Setup.IsElevated());
        Show(PageProgress);
        AppendDetail("开始" + (existingRoot != null ? "升级" : "安装") + " " + Setup.Brand + " " + version);
        AppendDetail("目标位置：" + folder.Text);

        string root = folder.Text;
        bool desktop = desktopLink.Checked;
        bool autostart = autostartBox.Checked;
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
                AppendDetail("正在解包安装文件…");
                FileStream file;
                using (var payload = Setup.OpenPayload(out file))
                using (file)
                using (var zip = new ZipArchive(payload, ZipArchiveMode.Read))
                {
                    Setup.Extract(zip, package, () => cancelRequested, (name, done) =>
                        Report("正在解包安装文件… " + Path.GetFileName(name), 0.35 * done / Math.Max(1, unpackedBytes)));
                }
                if (cancelRequested) throw new OperationCanceledException();
                string arguments = "-NoProfile -ExecutionPolicy Bypass -File " + Setup.Quote(Path.Combine(package, "install.ps1"))
                    + " -InstallDir " + Setup.Quote(root)
                    + " -Progress -CancelFile " + Setup.Quote(cancelFile)
                    + (desktop || upgrade ? "" : " -NoDesktopShortcut")
                    + (autostart ? " -Autostart" : " -NoAutostart");
                string lastError = null;
                string firstError = null;
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
                        if (lastDetailStage != stage)
                        {
                            lastDetailStage = stage;
                            AppendDetail(StageText[stage]);
                        }
                        Report(StageText[stage] + (stage == "copy" ? " " + (int)(fraction * 100) + "%" : ""), range[0] + (range[1] - range[0]) * fraction);
                    }
                    else if (line.StartsWith("[stderr] ") && line.Trim().Length > 9)
                    {
                        // Keep the FIRST meaningful stderr line: PowerShell prints the exception
                        // message first and the "+ FullyQualifiedErrorId" bookkeeping last, so
                        // keeping the last line showed users a useless tail of the error.
                        string error = line.Substring(9).Trim();
                        if (firstError == null && !error.StartsWith("+")) firstError = error;
                        if (!error.StartsWith("+")) lastError = error;
                    }
                });
                if (code != 0) failure = firstError ?? lastError ?? "安装程序退出代码 " + code;
                else
                {
                    AppendDetail("安装完成。");
                    Report("安装完成", 1);
                }
            }
            catch (OperationCanceledException) { code = 2; }
            catch (Exception e) { failure = e.Message; code = 1; Log("error " + e); }
            finally
            {
                canCancel = false;
                try { Directory.Delete(Setup.FileSystemPath(temp), true); } catch (Exception) { }
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
        {
            // Show where the log actually is; an empty path left the user with nothing to send us.
            string where = string.IsNullOrEmpty(logFile) ? "（未能创建日志文件）" : logFile;
            finishText.Text = "安装失败：" + (string.IsNullOrEmpty(failure) ? "未知错误" : failure)
                + "\n\n已自动回滚，" + (existingRoot != null ? "原版本保持可用，数据未受影响。" : "没有留下半装的文件。")
                + "\n安装日志：" + where;
            openLog.Visible = !string.IsNullOrEmpty(logFile) && File.Exists(logFile);
        }
        else
            finishText.Text = Setup.Brand + " " + version + " 已" + (existingRoot != null ? "升级" : "安装") + "到：\n" + folder.Text
                + "\n\n您可从开始菜单或桌面快捷方式启动 " + Setup.Brand + "；运行时右下角系统托盘图标提供打开、检查更新、设置和退出功能。";
        Show(PageFinish);
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
        AppendDetail("用户取消安装，正在回滚…");
    }

    void OpenLog()
    {
        if (string.IsNullOrEmpty(logFile) || !File.Exists(logFile)) return;
        try { Process.Start(new ProcessStartInfo(logFile) { UseShellExecute = true }); }
        catch (Exception e) { MessageBox.Show(this, "未能打开日志：" + e.Message + "\n\n请手动打开：\n" + logFile, Setup.Brand); }
    }

    static void LaunchInstalled(string root)
    {
        try
        {
            string current = File.ReadAllText(Path.Combine(root, "current.txt")).Trim();
            string tray = Setup.IsSafeVersionId(current)
                ? Path.Combine(root, "versions", current, "auriga", "tray", "AurigaDesktop.exe")
                : "";
            string target = tray.Length > 0 && File.Exists(tray) ? tray : Path.Combine(root, "bin", "auriga-desktop.cmd");
            if (Setup.IsElevated())
            {
                // Delegate launch through explorer.exe so the tray runs at the desktop user's
                // standard (non-elevated) integrity level rather than inheriting setup elevation.
                // Always wrap in quotes so paths with commas are not parsed as explorer switches.
                string explorer = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows), "explorer.exe");
                if (File.Exists(explorer))
                {
                    Process.Start(new ProcessStartInfo(explorer, "\"" + target + "\"") { UseShellExecute = false });
                    return;
                }
            }
            if (tray.Length > 0 && File.Exists(tray))
                Process.Start(new ProcessStartInfo(tray) { UseShellExecute = false, WorkingDirectory = root });
            else
                Process.Start(new ProcessStartInfo(target) { UseShellExecute = true, WindowStyle = ProcessWindowStyle.Minimized });
        }
        catch (Exception) { }
    }
}
