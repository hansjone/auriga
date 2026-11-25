// Auriga DSH tray app. It owns the launcher (node cli.mjs launch --stdin-control):
// opening the local DSH or the platform sign-in page, status, update check and
// install, settings, logs, about and a clean exit. Single instance per install root.
// The same exe, copied to a temporary folder, is the interactive uninstaller.
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Reflection;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Web.Script.Serialization;
using System.Windows.Forms;
using Auriga.Windows;
using Microsoft.Win32;

static class Desktop
{
    public const string Brand = "Auriga DSH";
    public static readonly JavaScriptSerializer Json = new JavaScriptSerializer { MaxJsonLength = 8388608 };

    /// Names shared with the setup wizard: Local\AurigaDesktop-<16 hex of SHA-256(lower root)>.
    public static string Key(string root)
    {
        string normal = Path.GetFullPath(root).TrimEnd('\\').ToLowerInvariant();
        using (var sha = SHA256.Create())
        {
            byte[] hash = sha.ComputeHash(Encoding.UTF8.GetBytes(normal));
            return "Local\\AurigaDesktop-" + BitConverter.ToString(hash, 0, 8).Replace("-", "");
        }
    }

    public static Dictionary<string, object> ReadJson(string file)
    {
        if (!File.Exists(file)) return null;
        try { return Json.Deserialize<Dictionary<string, object>>(File.ReadAllText(file, Encoding.UTF8)); }
        catch (Exception) { return null; }
    }

    public static string Str(Dictionary<string, object> map, string key)
    {
        object value;
        return map != null && map.TryGetValue(key, out value) && value != null ? Convert.ToString(value) : null;
    }

    public static Dictionary<string, object> Obj(Dictionary<string, object> map, string key)
    {
        object value;
        return map != null && map.TryGetValue(key, out value) ? value as Dictionary<string, object> : null;
    }

    public static bool Flag(Dictionary<string, object> map, string key)
    {
        object value;
        return map != null && map.TryGetValue(key, out value) && value is bool && (bool)value;
    }

    public static string PythonFailureReason(string error)
    {
        var match = Regex.Match(error ?? "", @"\[(python_user\.(?:abi_mismatch|bootstrap_digest_mismatch|environment_failed|timeout|access_denied|file_missing|busy|invalid_state|path_alias|path_link|invalid_argument|unavailable))\]");
        return match.Success ? match.Groups[1].Value : "python_user.reset_failed";
    }

    public static string Current(string root)
    {
        return File.ReadAllText(Path.Combine(root, "current.txt")).Trim();
    }

    public static string VersionDir(string root) { return Path.Combine(root, "versions", Current(root)); }
    public static string Node(string root) { return Path.Combine(VersionDir(root), "upstream", "node", "node.exe"); }
    public static string Cli(string root) { return Path.Combine(VersionDir(root), "auriga", "runtime", "cli.mjs"); }

    public static string Quote(string value) { return "\"" + value.Replace("\"", "\\\"") + "\""; }

    public static ProcessStartInfo NodeStart(string root, string arguments)
    {
        return new ProcessStartInfo(Node(root), Quote(Cli(root)) + " " + arguments)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            StandardOutputEncoding = Encoding.UTF8,
            StandardErrorEncoding = Encoding.UTF8,
            WorkingDirectory = root,
        };
    }

    /// One-shot cli command; returns the last JSON line of stdout (or null).
    public static Dictionary<string, object> RunJson(string root, string arguments, out string error, Func<ProcessStartInfo, Process> start = null)
    {
        error = null;
        Dictionary<string, object> last = null;
        var errors = new StringBuilder();
        using (var p = (start ?? Process.Start)(NodeStart(root, arguments)))
        {
            p.ErrorDataReceived += delegate (object s, DataReceivedEventArgs e) { if (e.Data != null) lock (errors) errors.AppendLine(e.Data); };
            p.BeginErrorReadLine();
            for (string line; (line = p.StandardOutput.ReadLine()) != null;)
            {
                if (!line.StartsWith("{")) continue;
                try
                {
                    var value = Json.Deserialize<Dictionary<string, object>>(line);
                    last = value;
                }
                catch (Exception) { }
            }
            p.WaitForExit();
            if (p.ExitCode != 0) lock (errors) error = errors.ToString().Trim();
        }
        return error == null ? last : null;
    }

    public static void Open(string target)
    {
        // Tests record the hand-off instead of launching the user's real browser.
        var log = Environment.GetEnvironmentVariable("AURIGA_TEST_OPEN_LOG");
        if (!string.IsNullOrEmpty(log))
        {
            try { File.AppendAllText(log, target + Environment.NewLine); } catch (Exception) { }
            return;
        }
        try { Process.Start(new ProcessStartInfo(target) { UseShellExecute = true }); }
        catch (Exception) { }
    }

    public static void KillTree(int pid)
    {
        try
        {
            using (var kill = Process.Start(new ProcessStartInfo(Path.Combine(Environment.SystemDirectory, "taskkill.exe"), "/PID " + pid + " /T /F") { UseShellExecute = false, CreateNoWindow = true }))
                kill.WaitForExit(15000);
        }
        catch (Exception) { }
    }

    public static string RegistryName(string root)
    {
        string name = Str(ReadJson(Path.Combine(root, "shell.json")), "registryKey");
        return string.IsNullOrEmpty(name) || !Regex.IsMatch(name, "^[A-Za-z0-9._-]{1,64}$") ? "AurigaDesktop" : name;
    }

    public static bool SamePath(string left, string right)
    {
        if (string.IsNullOrEmpty(left) || string.IsNullOrEmpty(right) ||
            !Path.IsPathRooted(left) || !Path.IsPathRooted(right)) return false;
        try { return string.Equals(Path.GetFullPath(left).TrimEnd('\\'), Path.GetFullPath(right).TrimEnd('\\'), StringComparison.OrdinalIgnoreCase); }
        catch (Exception) { return false; }
    }

    public static string StartupCommand(string root)
    {
        string ps = Path.Combine(Environment.SystemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe");
        return Quote(ps) + " -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File " +
            Quote(Path.Combine(root, "bin", "auriga-autostart.ps1"));
    }

    public static bool OwnsStartup(string root, string command)
    {
        if (string.IsNullOrEmpty(command)) return false;
        if (string.Equals(command, StartupCommand(root), StringComparison.OrdinalIgnoreCase)) return true;
        var match = Regex.Match(command, @"^""([^""]+)"" /autostart$", RegexOptions.IgnoreCase);
        string versions = Regex.Escape(Path.Combine(root, "versions"));
        return match.Success && Regex.IsMatch(match.Groups[1].Value,
            "^" + versions + @"\\[A-Za-z0-9._-]+\\auriga\\tray\\AurigaDesktop\.exe$", RegexOptions.IgnoreCase);
    }

    public static void ShellChange(Action action)
    {
        string sid = WindowsIdentity.GetCurrent().User.Value;
        using (var mutex = new Mutex(false, "Global\\AurigaDesktop-ShellIntegration-" + sid))
        {
            bool held;
            try { held = mutex.WaitOne(15000); } catch (AbandonedMutexException) { held = true; }
            if (!held) return;
            try { action(); } finally { mutex.ReleaseMutex(); }
        }
    }

    public static string PlatformText(string state, string reason)
    {
        switch (state)
        {
            case "authenticated": return "平台已连接（已登录）";
            case "authenticating": return "平台：正在登录…";
            case "signed_out": return "平台未登录";
            case "reauth_required": return "平台登录已过期，请重新登录";
            case "unreachable": return "平台暂不可达（网络或平台服务不可用）";
            case null: return "平台：等待状态…";
        }
        switch (reason)
        {
            case "not_configured": return "平台未接入：安装时未配置平台地址";
            case "origin_rejected": return "平台未接入：平台地址不受信任（需要 HTTPS 或已确认的内网 IP）";
            case "helper_missing": return "平台未接入：缺少认证组件，请重新安装";
            case "acl_failed": return "平台未接入：无法保护本地凭据目录（权限不足）";
            case "reparse_rejected": return "平台未接入：数据目录含链接或重解析点";
            case "component_missing": return "平台未接入：此版本不含平台认证组件";
            default: return "平台连接暂不可用（" + (reason ?? "unknown") + "）";
        }
    }

    public static Icon AppIcon()
    {
        try { return Icon.ExtractAssociatedIcon(Assembly.GetExecutingAssembly().Location); }
        catch (Exception) { return SystemIcons.Application; }
    }
}

sealed class TrayApp : ApplicationContext
{
    readonly string root;
    readonly bool autostart;
    readonly NotifyIcon icon = new NotifyIcon();
    readonly ContextMenuStrip menu = new ContextMenuStrip();
    readonly Control ui = new Control();
    readonly ToolStripMenuItem openItem, fallbackLoginItem, statusItem, platformItem, checkItem, updateItem, restartItem,
        autostartItem, autoCheckItem, autoDownloadItem, originItem, pythonResetItem, logsItem, aboutItem, exitItem;
    readonly System.Windows.Forms.Timer checkTimer = new System.Windows.Forms.Timer();
    readonly System.Windows.Forms.Timer pendingTimer = new System.Windows.Forms.Timer();
    Process launcher;
    string dshUrl, platformState, platformReason, availableVersion, logFile, updateStageText;
    readonly object updateLock = new object();
    Process updateWorker;
    ProgressForm updateForm;
    long lastUpdateActivityTicks;
    volatile bool updateApplying;
    bool openWhenReady, exiting, restarting, busy, restartRequired, updateEnabled = true, versionSyncing;
    DateTime nextSyncAttemptUtc = DateTime.MinValue;
    Dictionary<string, object> info;

    public TrayApp(string root, bool autostart)
    {
        this.root = root;
        this.autostart = autostart;
        ui.CreateControl();
        icon.Icon = Desktop.AppIcon();
        icon.Text = Desktop.Brand;
        openItem = new ToolStripMenuItem("打开 Auriga", null, delegate { OpenAuriga(); }) { Font = new Font(menu.Font, FontStyle.Bold) };
        fallbackLoginItem = new ToolStripMenuItem("\u6d4f\u89c8\u5668\u767b\u5f55\uff08\u5907\u7528\uff09", null, delegate { Send("login-fallback"); });
        statusItem = new ToolStripMenuItem("正在启动…") { Enabled = false };
        platformItem = new ToolStripMenuItem(Desktop.PlatformText(null, null)) { Enabled = false };
        checkItem = new ToolStripMenuItem("检查更新", null, delegate { CheckUpdate(true); });
        updateItem = new ToolStripMenuItem("立即更新", null, delegate { DownloadUpdate(); }) { Visible = false };
        restartItem = new ToolStripMenuItem("重启以完成更新", null, delegate { RestartForUpdate(); }) { Visible = false };
        autostartItem = new ToolStripMenuItem("开机自动启动", null, delegate { ToggleAutostart(); });
        autoCheckItem = new ToolStripMenuItem("自动检查更新", null, delegate { ToggleAutoCheck(); });
        autoDownloadItem = new ToolStripMenuItem("有更新时自动下载（安装前询问）", null, delegate { ToggleAutoDownload(); });
        originItem = new ToolStripMenuItem("平台地址：未配置", null, delegate { CopyOrigin(); });
        pythonResetItem = new ToolStripMenuItem("重置个人 Python 环境", null, delegate { ResetPython(); });
        var settings = new ToolStripMenuItem("设置");
        settings.DropDownItems.AddRange(new ToolStripItem[] { autostartItem, autoCheckItem, autoDownloadItem, new ToolStripSeparator(), originItem, pythonResetItem });
        logsItem = new ToolStripMenuItem("打开日志目录", null, delegate { OpenLogs(); });
        aboutItem = new ToolStripMenuItem("关于 " + Desktop.Brand, null, delegate { About(); });
        exitItem = new ToolStripMenuItem("退出", null, delegate { ExitApp(); });
        menu.Items.AddRange(new ToolStripItem[] {
            openItem, fallbackLoginItem, statusItem, platformItem, new ToolStripSeparator(),
            checkItem, updateItem, restartItem, new ToolStripSeparator(),
            settings, logsItem, aboutItem, new ToolStripSeparator(), exitItem });
        icon.ContextMenuStrip = menu;
        menu.Opening += delegate { SyncCurrentVersionIfChanged(); UpdateMenu(); };
        icon.MouseDoubleClick += delegate (object s, MouseEventArgs e) { if (e.Button == MouseButtons.Left) OpenAuriga(); };
        icon.BalloonTipClicked += delegate { if (restartRequired) RestartForUpdate(); else if (availableVersion != null) DownloadUpdate(); };
        icon.Visible = true;
        checkTimer.Tick += delegate { checkTimer.Interval = 6 * 3600 * 1000; CheckUpdate(false); };
        // An update downloaded from the DSH settings page also waits for a restart: offer it here.
        pendingTimer.Interval = 30 * 1000;
        pendingTimer.Tick += delegate
        {
            SyncCurrentVersionIfChanged();
            bool queued = File.Exists(Path.Combine(root, "desktop-pending", "release.json"));
            if (queued && !restartRequired && !busy && !updateApplying)
            {
                restartRequired = true;
                icon.ShowBalloonTip(10000, Desktop.Brand, "更新已下载。点击此处重启 Auriga 完成安装。", ToolTipIcon.Info);
            }
            else if (!queued && restartRequired && !busy && !updateApplying) restartRequired = false;
            UpdateMenu();
        };
        pendingTimer.Start();
        checkTimer.Interval = 60 * 1000;
        RefreshInfo();
        RefreshShellIntegration();
        openWhenReady = !autostart;
        StartLauncher();
        checkTimer.Start();
    }

    public void Post(Action action)
    {
        try { if (!ui.IsDisposed) ui.BeginInvoke(action); } catch (Exception) { }
    }

    void Log(string line)
    {
        if (logFile == null) return;
        try
        {
            line = Regex.Replace(line, "token=[A-Za-z0-9_\\-]+", "token=REDACTED");
            if (File.Exists(logFile) && new FileInfo(logFile).Length > 5 * 1024 * 1024)
            {
                File.Copy(logFile, logFile + ".1", true);
                File.WriteAllText(logFile, "");
            }
            File.AppendAllText(logFile, DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss ") + line + Environment.NewLine, Encoding.UTF8);
        }
        catch (Exception) { }
    }

    void SyncCurrentVersionIfChanged()
    {
        if (busy || restarting || updateApplying || exiting || versionSyncing) return;
        if (DateTime.UtcNow < nextSyncAttemptUtc) return;
        string active = null;
        try { active = Desktop.Current(root); } catch (Exception) { return; }
        string known = Desktop.Str(info, "packageId");
        if (string.IsNullOrEmpty(active) || string.Equals(active, known, StringComparison.Ordinal)) return;
        versionSyncing = true;
        ThreadPool.QueueUserWorkItem(delegate
        {
            Dictionary<string, object> next = null;
            string error = null;
            try { next = Desktop.RunJson(root, "status --root " + Desktop.Quote(root), out error); }
            catch (Exception e) { error = e.Message; }
            Post(delegate
            {
                versionSyncing = false;
                if (exiting) return;
                if (next == null)
                {
                    nextSyncAttemptUtc = DateTime.UtcNow.AddSeconds(30);
                    if (!string.IsNullOrEmpty(error)) Log("version sync failed: " + error);
                    return;
                }
                nextSyncAttemptUtc = DateTime.MinValue;
                ApplyInfo(next);
                var snapshot = next;
                ThreadPool.QueueUserWorkItem(delegate { RefreshShellIntegration(snapshot); });
            });
        });
    }

    void RefreshInfo()
    {
        Dictionary<string, object> next = null;
        string error = null;
        try { next = Desktop.RunJson(root, "status --root " + Desktop.Quote(root), out error); }
        catch (Exception e) { error = e.Message; }
        if (next == null)
        {
            if (!string.IsNullOrEmpty(error)) Log("status refresh failed: " + error);
            return;
        }
        ApplyInfo(next);
    }

    void ApplyInfo(Dictionary<string, object> next)
    {
        info = next;
        string dataHome = Desktop.Str(info, "dataHome");
        string logs = Desktop.Str(info, "logs");
        if (logs != null)
        {
            try { Directory.CreateDirectory(logs); logFile = Path.Combine(logs, "desktop-tray.log"); } catch (Exception) { }
        }
        var update = Desktop.Obj(info, "update");
        updateEnabled = update == null || !update.ContainsKey("enabled") || Desktop.Flag(update, "enabled");
        availableVersion = Desktop.Str(update, "available");
        restartRequired = Desktop.Flag(update, "restartRequired");
        if (Desktop.Flag(update, "manualRetryRequired"))
        {
            var failure = Desktop.Obj(update, "failure");
            restartItem.Text = "重试更新（" + (Desktop.Str(failure, "code") ?? "updateFailed") + "/" + (Desktop.Str(failure, "stage") ?? "unknown") + "）";
        }
        else if (!updateApplying)
        {
            restartItem.Text = "重启以完成更新";
        }
        string origin = Desktop.Str(info, "platformOrigin");
        originItem.Text = "平台地址：" + (origin ?? "未配置") + (origin != null ? "（点击复制）" : "");
        originItem.Enabled = origin != null;
        autostartItem.Checked = Desktop.OwnsStartup(root, Registry.GetValue(RunKey, Desktop.RegistryName(root), null) as string);
        autoCheckItem.Checked = updateEnabled;
        autoDownloadItem.Checked = AutoDownload;
        bool configured = Desktop.Flag(info, "updatesConfigured");
        checkItem.Enabled = configured;
        autoCheckItem.Enabled = configured;
        autoDownloadItem.Enabled = configured;
        if (!configured) checkItem.Text = "检查更新（此版本未配置更新，需手动安装新版一次）";
        UpdateMenu();
    }

    const string RunKey = "HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Run";
    const string Registration = "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\";

    /// After an in-app update the Apps & features entry and autostart must follow the new version.
    void RefreshShellIntegration() { RefreshShellIntegration(info); }

    void RefreshShellIntegration(Dictionary<string, object> snapshot)
    {
        try
        {
            Desktop.ShellChange(delegate
            {
                string name = Desktop.RegistryName(root);
                using (var key = Registry.CurrentUser.OpenSubKey(Registration + name, true))
                {
                    if (key == null || snapshot == null ||
                        !Desktop.SamePath(key.GetValue("InstallLocation") as string, root)) return;
                    if (Desktop.OwnsStartup(root, Registry.GetValue(RunKey, name, null) as string))
                        Registry.SetValue(RunKey, name, Desktop.StartupCommand(root));
                    key.SetValue("DisplayVersion", Desktop.Str(snapshot, "version") ?? "");
                    key.SetValue("Publisher", "Auriga");
                    string ico = Path.Combine(root, "bin", "auriga.ico");
                    if (File.Exists(ico)) key.SetValue("DisplayIcon", ico);
                }
                EnsureShortcuts();
            });
        }
        catch (Exception e) { Log("shell refresh failed: " + e.Message); }
    }

    /// Installs upgraded in-app from versions without an uninstall shortcut get one next to the
    /// recorded Start menu entry; it is added to shell.json so the uninstaller removes it.
    void EnsureShortcuts()
    {
        string file = Path.Combine(root, "shell.json");
        var shell = Desktop.ReadJson(file);
        if (shell == null) return;
        var links = new List<string>();
        object raw;
        shell.TryGetValue("shortcuts", out raw);
        if (raw is string) links.Add((string)raw);
        else if (raw is System.Collections.IEnumerable)
        {
            foreach (object item in (System.Collections.IEnumerable)raw)
                if (item != null) links.Add(Convert.ToString(item));
        }
        string launcher = Path.Combine(root, "bin", "auriga-desktop.cmd");
        string start = links.Find(l => File.Exists(l) && l.EndsWith(".lnk", StringComparison.OrdinalIgnoreCase) &&
            Desktop.SamePath(DesktopShortcut.GetTarget(l), launcher));
        if (start == null) return;
        string uninstall = Path.Combine(Path.GetDirectoryName(start), DesktopShortcut.UninstallLabel(Path.GetFileName(start)));
        string cmd = Path.Combine(root, "bin", "uninstall.cmd");
        string ico = Path.Combine(root, "bin", "auriga.ico");
        if (!File.Exists(cmd)) return;
        if (File.Exists(ico)) DesktopShortcut.SetIcon(start, launcher, ico);
        if (links.Exists(l => l.Equals(uninstall, StringComparison.OrdinalIgnoreCase)) && File.Exists(uninstall)) return;
        if (File.Exists(uninstall) && !Desktop.SamePath(DesktopShortcut.GetTarget(uninstall), cmd))
            return;
        DesktopShortcut.Save(uninstall, cmd, Environment.SystemDirectory, DesktopShortcut.UninstallLabel(Desktop.Brand), ico);
        if (!links.Exists(l => l.Equals(uninstall, StringComparison.OrdinalIgnoreCase))) links.Add(uninstall);
        shell["shortcuts"] = links.ToArray();
        File.WriteAllText(file + ".tmp", Desktop.Json.Serialize(shell), new UTF8Encoding(false));
        File.Copy(file + ".tmp", file, true);
        File.Delete(file + ".tmp");
    }

    bool AutoDownload
    {
        get { return Desktop.Flag(Desktop.ReadJson(Path.Combine(root, "desktop-tray.json")), "autoDownload"); }
        set
        {
            string file = Path.Combine(root, "desktop-tray.json");
            File.WriteAllText(file + ".tmp", Desktop.Json.Serialize(new Dictionary<string, object> { { "autoDownload", value } }), new UTF8Encoding(false));
            if (File.Exists(file)) File.Delete(file);
            File.Move(file + ".tmp", file);
        }
    }

    void UpdateMenu()
    {
        bool running = launcher != null && !launcher.HasExited;
        statusItem.Text = exiting ? "正在退出…"
            : updateApplying ? (updateStageText ?? "正在安装更新…")
            : restarting ? "正在安装更新…"
            : running ? "运行中 · 版本 " + (Desktop.Str(info, "version") ?? "?")
            : "未运行（点“打开 Auriga”启动）";
        platformItem.Text = running ? Desktop.PlatformText(platformState, platformReason) : "平台：未运行";
        updateItem.Visible = availableVersion != null && !restartRequired && !updateApplying;
        updateItem.Text = "立即更新到 " + availableVersion;
        updateItem.Enabled = !busy && !updateApplying && !exiting;
        restartItem.Visible = (restartRequired || updateApplying || restarting) && !exiting;
        if (updateApplying || restarting) restartItem.Text = "正在安装更新…（查看进度）";
        else restartItem.Text = "重启以完成更新";
        fallbackLoginItem.Enabled = launcher != null && !launcher.HasExited && platformState != "authenticated" && !exiting;
        checkItem.Enabled = !busy && !updateApplying && !exiting && Desktop.Flag(info, "updatesConfigured");
        pythonResetItem.Enabled = !busy && !restarting && !updateApplying && !exiting;
        exitItem.Enabled = !exiting;
        exitItem.Text = exiting ? "正在退出…" : "退出";
        string tip = Desktop.Brand + " · " + (exiting ? "正在退出" : updateApplying || restarting ? "正在更新" : running ? (platformState == "authenticated" ? "已连接" : "运行中") : "未运行");
        icon.Text = tip.Substring(0, Math.Min(63, tip.Length));
    }

    void StartLauncher()
    {
        if (launcher != null && !launcher.HasExited) return;
        dshUrl = null; platformState = null; platformReason = null;
        var start = Desktop.NodeStart(root, "launch --root " + Desktop.Quote(root) + " --stdin-control -- --no-open");
        start.RedirectStandardInput = true;
        try
        {
            launcher = Process.Start(start);
        }
        catch (Exception e)
        {
            Log("launcher start failed: " + e.Message);
            launcher = null;
            UpdateMenu();
            return;
        }
        launcher.EnableRaisingEvents = true;
        var mine = launcher;
        launcher.OutputDataReceived += delegate (object s, DataReceivedEventArgs e) { if (e.Data != null) Post(delegate { OnLine(e.Data); }); };
        launcher.ErrorDataReceived += delegate (object s, DataReceivedEventArgs e) { if (e.Data != null) Post(delegate { OnError(e.Data); }); };
        launcher.Exited += delegate { Post(delegate { OnLauncherExit(mine); }); };
        launcher.BeginOutputReadLine();
        launcher.BeginErrorReadLine();
        UpdateMenu();
    }

    static readonly Regex UrlPattern = new Regex("(https?://(?:127\\.0\\.0\\.1|localhost|\\[::1\\]):\\d+/\\S*token=[A-Za-z0-9_\\-]+)");

    void OnLine(string line)
    {
        if (line.StartsWith("@@auriga "))
        {
            Dictionary<string, object> e;
            try { e = Desktop.Json.Deserialize<Dictionary<string, object>>(line.Substring(9)); }
            catch (Exception) { return; }
            OnEvent(e);
            return;
        }
        Log(line);
        var match = UrlPattern.Match(line);
        if (match.Success && dshUrl == null)
        {
            dshUrl = match.Groups[1].Value;
            MaybeOpen();
        }
    }

    string lastError;
    void OnError(string line)
    {
        Log("[stderr] " + line);
        if (line.Contains("已在运行")) lastError = "already-running";
        if (line.Contains("desktop.python_runtime_modified")) lastError = "python-runtime-modified";
    }

    static string UpdateStageMessage(string phase, string stage)
    {
        if (phase == "retrying")
            return "正在重试：" + UpdateStageDetail(stage) + "（请稍候）";
        if (phase == "verifying" || phase == "downloading")
            return UpdateDownloadMessage(phase, stage);
        return UpdateStageDetail(stage);
    }

    static string UpdateDownloadMessage(string phase, string stage)
    {
        if (phase == "downloading" && (string.IsNullOrEmpty(stage) || stage == "download"))
            return null; // caller formats byte progress
        return UpdateStageDetail(string.IsNullOrEmpty(stage) ? "verify-archive" : stage);
    }

    static string UpdateStageDetail(string stage)
    {
        switch (stage)
        {
            case "download": return "正在下载更新包…";
            case "verify-current": return "正在读取当前安装信息…";
            case "verify-release": return "正在核验更新签名…";
            case "check-release": return "正在确认发布仍有效…";
            case "verify-archive": return "正在校验安装包完整性（整包摘要）…";
            case "extract": return "正在解压更新包…";
            case "verify-package":
            case "verify": return "正在校验包内文件清单…";
            case "install": return "正在准备写入新版本…";
            case "copy": return "正在复制程序文件…";
            case "verify-copy": return "正在校验已复制的文件…";
            case "profile": return "正在同步配置与插件…";
            case "smoke": return "正在验证新版本能否启动…";
            case "commit": return "正在切换至新版本…";
            case "done": return "更新步骤已完成…";
            default: return string.IsNullOrEmpty(stage)
                ? "正在安装更新，请稍候…"
                : "正在安装更新（" + stage + "）…";
        }
    }

    void ShowOrUpdateProgress(string message, string title = null)
    {
        if (updateForm != null && !updateForm.IsDisposed)
        {
            if (!string.IsNullOrEmpty(title)) updateForm.Text = title;
            updateForm.Report(message, -1);
            updateForm.BringToFront();
            updateForm.Activate();
            return;
        }
        updateForm = new ProgressForm(title ?? "正在安装更新", message, false);
        updateForm.Show();
        updateForm.BringToFront();
        updateForm.Activate();
    }

    void CloseUpdateProgress()
    {
        if (updateForm != null)
        {
            if (!updateForm.IsDisposed) updateForm.Close();
            updateForm = null;
        }
    }

    void OnEvent(Dictionary<string, object> e)
    {
        string kind = Desktop.Str(e, "event");
        if (kind == "dsh")
        {
            if (Desktop.Str(e, "state") == "stopped")
            {
                Interlocked.Exchange(ref lastUpdateActivityTicks, DateTime.UtcNow.Ticks);
                if (updateForm != null && !updateForm.IsDisposed)
                    updateForm.Report("Auriga 已停止，正在准备安装更新…", -1);
            }
            return;
        }
        if (kind == "update")
        {
            string phase = Desktop.Str(e, "phase"), stage = Desktop.Str(e, "stage"), code = Desktop.Str(e, "code");
            Interlocked.Exchange(ref lastUpdateActivityTicks, DateTime.UtcNow.Ticks);
            Log("update " + phase + " stage=" + stage + " code=" + code + " category=" + Desktop.Str(e, "pathCategory"));
            if (phase == "installing" || phase == "retrying")
            {
                updateApplying = true;
                updateStageText = UpdateStageMessage(phase, stage);
                if (updateForm != null && !updateForm.IsDisposed)
                    updateForm.Report(updateStageText, -1);
            }
            else if (phase == "failed" || phase == "awaiting-retry")
            {
                updateApplying = false;
                updateStageText = null;
                if (!restarting) CloseUpdateProgress();
                restartRequired = File.Exists(Path.Combine(root, "desktop-pending", "release.json"));
                restartItem.Text = "重试更新（" + (code ?? "updateFailed") + "/" + (stage ?? "unknown") + "）";
                icon.ShowBalloonTip(8000, Desktop.Brand, "更新失败，原版本已保留。" + (code ?? "updateFailed") + " / " + stage + "。详情见安装目录 desktop-update.log。", ToolTipIcon.Warning);
            }
            else if (phase == "installed")
            {
                updateApplying = false;
                updateStageText = null;
                restartRequired = false;
                restartItem.Text = "重启以完成更新";
                if (!restarting)
                {
                    CloseUpdateProgress();
                    RefreshInfo();
                    RefreshShellIntegration();
                    icon.ShowBalloonTip(6000, Desktop.Brand, "已更新到 " + (Desktop.Str(info, "version") ?? "新版本") + "。", ToolTipIcon.Info);
                }
            }
            UpdateMenu();
            return;
        }
        else if (kind == "platform")
        {
            string previous = platformState;
            platformState = Desktop.Str(e, "state");
            platformReason = Desktop.Str(e, "reason");
            Log("platform " + platformState + (platformReason != null ? " " + platformReason : ""));
            if (platformState == "authenticated" && previous != null && previous != "authenticated")
                icon.ShowBalloonTip(4000, Desktop.Brand, "已登录 Auriga 平台。", ToolTipIcon.Info);
            MaybeOpen();
        }
        if (kind == "fallback-login" && !Desktop.Flag(e, "opened"))
            icon.ShowBalloonTip(3000, Desktop.Brand, "\u5907\u7528\u767b\u5f55\u6682\u4e0d\u53ef\u7528", ToolTipIcon.Info);
        UpdateMenu();
    }

    /// Open DSH immediately; Auriga owns platform login inside shell.overlay.
    void MaybeOpen()
    {
        if (!openWhenReady || dshUrl == null || platformState == null) return;
        openWhenReady = false;
        var url = new UriBuilder(dshUrl);
        if (platformState != "authenticated") url.Fragment = "auriga=platform-login";
        Desktop.Open(url.Uri.AbsoluteUri);
    }

    void OpenAuriga()
    {
        if (launcher == null || launcher.HasExited)
        {
            openWhenReady = true;
            StartLauncher();
            return;
        }
        if (platformState == null)
            icon.ShowBalloonTip(3000, Desktop.Brand, "Auriga 正在启动，稍后自动打开。", ToolTipIcon.Info);
        openWhenReady = true;
        MaybeOpen();
    }

    void Send(string command)
    {
        try { launcher.StandardInput.WriteLine(command); launcher.StandardInput.Flush(); }
        catch (Exception) { }
    }

    void OnLauncherExit(Process which)
    {
        if (which != launcher) return;
        bool wasApplying = updateApplying;
        updateApplying = false;
        updateStageText = null;
        if (wasApplying)
        {
            var update = Desktop.Obj(info, "update");
            if (Desktop.Flag(update, "manualRetryRequired"))
            {
                var failure = Desktop.Obj(update, "failure");
                restartItem.Text = "重试更新（" + (Desktop.Str(failure, "code") ?? "updateFailed") + "/" + (Desktop.Str(failure, "stage") ?? "unknown") + "）";
            }
            else restartItem.Text = "重启以完成更新";
        }
        if (!restarting)
        {
            CloseUpdateProgress();
            SyncCurrentVersionIfChanged();
        }
        int code = -1;
        try { code = which.ExitCode; } catch (Exception) { }
        Log("launcher exited " + code);
        if (!exiting && !restarting)
        {
            if (lastError == "already-running")
                MessageBox.Show(Desktop.Brand + " 已在运行（可能是旧版窗口），请先关闭它后再从开始菜单打开。", Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Information);
            else if (lastError == "python-runtime-modified")
                icon.ShowBalloonTip(12000, Desktop.Brand, "Python 运行时校验失败。请退出客户端，重新运行同版本安装包修复。详情见日志目录。", ToolTipIcon.Error);
            else if (code != 0)
                icon.ShowBalloonTip(5000, Desktop.Brand, "Auriga 已停止（代码 " + code + "）。点“打开 Auriga”重新启动，详情见日志目录。", ToolTipIcon.Warning);
            lastError = null;
        }
        UpdateMenu();
    }

    /// Ask the launcher to stop DSH (it applies a queued update on the way out) and wait.
    /// While an update is actively reporting progress, extend the wait instead of killing mid-copy.
    bool StopLauncher(int stopSeconds, int idleUpdateSeconds = 300, int maxTotalSeconds = 1800)
    {
        var p = launcher;
        if (p == null || p.HasExited) return true;
        DateTime startedAt = DateTime.UtcNow;
        Send("stop");
        try { p.StandardInput.Close(); } catch (Exception) { }
        for (;;)
        {
            if (p.WaitForExit(1000)) { p.WaitForExit(); return p.ExitCode == 0; }
            DateTime now = DateTime.UtcNow;
            if ((now - startedAt).TotalSeconds >= maxTotalSeconds) break;
            long activityTicks = Interlocked.Read(ref lastUpdateActivityTicks);
            bool activeUpdate = updateApplying || activityTicks >= startedAt.Ticks;
            if (activeUpdate)
            {
                DateTime lastActivity = activityTicks > startedAt.Ticks ? new DateTime(activityTicks, DateTimeKind.Utc) : startedAt;
                if ((now - lastActivity).TotalSeconds >= idleUpdateSeconds) break;
            }
            else if ((now - startedAt).TotalSeconds >= stopSeconds)
            {
                break;
            }
        }
        Log("launcher did not stop in time; ending its process tree");
        Desktop.KillTree(p.Id);
        p.WaitForExit(15000);
        return false;
    }

    Process StartUpdateWorker(ProcessStartInfo start)
    {
        lock (updateLock)
        {
            if (exiting) throw new OperationCanceledException();
            updateWorker = Process.Start(start);
            return updateWorker;
        }
    }

    void StopUpdateWorker()
    {
        lock (updateLock)
        {
            try
            {
                if (updateWorker != null && !updateWorker.HasExited)
                    Desktop.KillTree(updateWorker.Id);
            }
            catch (InvalidOperationException) { }
            updateWorker = null;
        }
    }

    void CheckUpdate(bool manual)
    {
        if (busy || restartRequired) { if (restartRequired && manual) RestartForUpdate(); return; }
        if (!Desktop.Flag(info, "updatesConfigured")) return;
        if (!manual && !updateEnabled) return;
        busy = true; UpdateMenu();
        string arguments = "update check --root " + Desktop.Quote(root);
        ThreadPool.QueueUserWorkItem(delegate
        {
            string error;
            Dictionary<string, object> result = null;
            try { result = Desktop.RunJson(root, arguments, out error, StartUpdateWorker); }
            catch (Exception e) { error = e.Message; }
            Post(delegate
            {
                if (exiting) return;
                busy = false;
                if (result == null)
                {
                    Log("update check failed: " + error);
                    if (manual) MessageBox.Show("检查更新失败：无法连接平台或发行信息无效。请稍后重试。", Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning);
                }
                else
                {
                    availableVersion = Desktop.Str(result, "available");
                    restartRequired = Desktop.Flag(result, "restartRequired");
                    if (availableVersion != null)
                    {
                        bool required = Desktop.Flag(result, "required");
                        icon.ShowBalloonTip(8000, Desktop.Brand, (required ? "必须更新：" : "发现新版本 ") + availableVersion + "。点击此处或托盘菜单“立即更新”。", required ? ToolTipIcon.Warning : ToolTipIcon.Info);
                        if (AutoDownload && !manual) DownloadUpdate(true);
                    }
                    else if (manual) MessageBox.Show("当前已是最新版本（" + (Desktop.Str(info, "version") ?? "?") + "）。", Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Information);
                }
                UpdateMenu();
            });
        });
    }

    void DownloadUpdate() { DownloadUpdate(false); }

    void DownloadUpdate(bool background)
    {
        if (busy) return;
        busy = true; UpdateMenu();
        var form = new ProgressForm("正在下载更新 " + availableVersion, "正在下载更新包…", true);
        form.Cancelled += delegate { StopUpdateWorker(); };
        if (!background) form.Show();
        string arguments = "update download --root " + Desktop.Quote(root);
        ThreadPool.QueueUserWorkItem(delegate
        {
            Dictionary<string, object> last = null;
            string error = null;
            try
            {
                if (form.WasCancelled) throw new OperationCanceledException();
                using (var child = StartUpdateWorker(Desktop.NodeStart(root, arguments)))
                {
                    child.ErrorDataReceived += delegate (object s, DataReceivedEventArgs e) { if (e.Data != null) error = e.Data; };
                    child.BeginErrorReadLine();
                    for (string line; (line = child.StandardOutput.ReadLine()) != null;)
                    {
                        Dictionary<string, object> value;
                        try { value = Desktop.Json.Deserialize<Dictionary<string, object>>(line); } catch (Exception) { continue; }
                        last = value;
                        long done = Convert.ToInt64(value.ContainsKey("downloadedBytes") ? value["downloadedBytes"] : 0);
                        long total = Convert.ToInt64(value.ContainsKey("totalBytes") ? value["totalBytes"] : 0);
                        string phase = Desktop.Str(value, "phase");
                        string stage = Desktop.Str(value, "stage");
                        Post(delegate
                        {
                            string detail = UpdateDownloadMessage(phase, stage);
                            if (detail != null)
                                form.Report(detail, -1);
                            else
                                form.Report(
                                    string.Format("已下载 {0:0.0} / {1:0.0} MB", done / 1048576.0, total / 1048576.0),
                                    total > 0 ? (int)(done * 100 / total) : -1);
                        });
                    }
                    child.WaitForExit();
                    if (child.ExitCode != 0) last = null;
                }
            }
            catch (Exception e) { error = e.Message; last = null; }
            Post(delegate
            {
                busy = false;
                bool cancelled = form.WasCancelled;
                form.Close();
                if (exiting) return;
                if (last != null && Desktop.Flag(last, "queued"))
                {
                    restartRequired = true;
                    availableVersion = null;
                    UpdateMenu();
                    if (background)
                        icon.ShowBalloonTip(10000, Desktop.Brand, "更新已下载。点击此处重启 Auriga 完成安装。", ToolTipIcon.Info);
                    else if (MessageBox.Show("更新已下载并通过校验。现在重启 Auriga 完成安装吗？\n安装期间 DSH 会短暂停止，会话与数据会保留。", Desktop.Brand, MessageBoxButtons.YesNo, MessageBoxIcon.Question) == DialogResult.Yes)
                        RestartForUpdate();
                }
                else if (!cancelled)
                {
                    Log("update download failed: " + error);
                    if (!background) MessageBox.Show("下载更新失败，已下载的部分会保留以便续传。请稍后重试。", Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning);
                }
                UpdateMenu();
            });
        });
    }

    void RestartForUpdate()
    {
        // Never silently ignore a click: always surface the progress window or an explanation.
        if (updateApplying || restarting)
        {
            ShowOrUpdateProgress(updateStageText ?? (restarting ? "正在准备安装更新，请稍候…" : "正在安装更新，请稍候…"));
            return;
        }
        if (exiting) return;
        if (busy)
        {
            MessageBox.Show("当前有其他操作进行中（如下载或检查更新）。请稍候再点击“重启以完成更新”。",
                Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Information);
            return;
        }
        if (!File.Exists(Path.Combine(root, "desktop-pending", "release.json")))
        {
            restartRequired = false;
            UpdateMenu();
            MessageBox.Show("没有待安装的更新。", Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Information);
            return;
        }
        restarting = true; busy = true; UpdateMenu();
        string before = Desktop.Current(root);
        ShowOrUpdateProgress("正在准备安装更新，请稍候…");
        ThreadPool.QueueUserWorkItem(delegate
        {
            string error;
            Dictionary<string, object> approved = null;
            try { approved = Desktop.RunJson(root, "update approve --root " + Desktop.Quote(root), out error); }
            catch (Exception e) { error = e.Message; }
            if (!Desktop.Flag(approved, "approved"))
            {
                Post(delegate
                {
                    CloseUpdateProgress();
                    busy = false;
                    restarting = false;
                    if (exiting) return;
                    Log("update confirmation failed: " + error);
                    RefreshInfo();
                    UpdateMenu();
                    if (updateApplying)
                    {
                        ShowOrUpdateProgress(updateStageText ?? "正在安装更新，请稍候…");
                        return;
                    }
                    MessageBox.Show(
                        "当前无法启动更新安装（可能已有更新操作正在进行，或待安装更新已被清理）。请稍候再试或检查日志目录。",
                        Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning);
                });
                return;
            }
            Post(delegate
            {
                if (exiting) { CloseUpdateProgress(); busy = false; restarting = false; return; }
                ShowOrUpdateProgress("正在停止 Auriga 并安装更新，请稍候…");
                if (launcher == null || launcher.HasExited) StartLauncher();
                ThreadPool.QueueUserWorkItem(delegate
                {
                    // Keep the progress window open: applyPending reports stages over stdout while stopping.
                    bool stopped = StopLauncher(120, 120, 900);
                    Post(delegate
                    {
                        busy = false;
                        if (exiting) { restarting = false; CloseUpdateProgress(); return; }
                        string after = Desktop.Current(root);
                        if (stopped && after != before)
                        {
                            Log("update installed " + before + " -> " + after);
                            ShowOrUpdateProgress("更新完成，正在启动新版本…");
                            // Continue from the new version's tray (new menu, runtime and icon).
                            string next = Path.Combine(root, "versions", after, "auriga", "tray", "AurigaDesktop.exe");
                            if (File.Exists(next))
                            {
                                exiting = true;
                                UpdateMenu();
                                try
                                {
                                    Process.Start(new ProcessStartInfo(next, "/restarted /updated") { UseShellExecute = false });
                                }
                                catch (Exception e)
                                {
                                    Log("failed to start updated tray: " + e.Message);
                                    CloseUpdateProgress();
                                    restarting = false;
                                    exiting = false;
                                    restartRequired = false;
                                    RefreshInfo();
                                    StartLauncher();
                                    MessageBox.Show("新版本已安装，但无法自动启动托盘程序。请从开始菜单重新打开 " + Desktop.Brand + "。",
                                        Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning);
                                    UpdateMenu();
                                    return;
                                }
                                Quit();
                                return;
                            }
                            CloseUpdateProgress();
                            restarting = false;
                            restartRequired = false;
                            RefreshInfo();
                            StartLauncher();
                            icon.ShowBalloonTip(6000, Desktop.Brand, "已更新到 " + Desktop.Str(info, "version") + "。", ToolTipIcon.Info);
                        }
                        else
                        {
                            CloseUpdateProgress();
                            restarting = false;
                            RefreshInfo();
                            if (stopped) StartLauncher();
                            string reason = stopped
                                ? "更新安装失败，已保留原版本。详情见日志目录。"
                                : "更新或停止 Auriga 超时，本次未完成更新。请检查日志后重新打开。";
                            MessageBox.Show(reason, Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning);
                        }
                        UpdateMenu();
                    });
                });
            });
        });
    }

    void ToggleAutostart()
    {
        Desktop.ShellChange(delegate
        {
            string name = Desktop.RegistryName(root);
            using (var registration = Registry.CurrentUser.OpenSubKey(Registration + name))
            {
                if (registration == null || !Desktop.SamePath(registration.GetValue("InstallLocation") as string, root)) return;
                using (var key = Registry.CurrentUser.CreateSubKey("Software\\Microsoft\\Windows\\CurrentVersion\\Run"))
                {
                    var command = key.GetValue(name) as string;
                    if (Desktop.OwnsStartup(root, command)) key.DeleteValue(name, false);
                    else if (command == null) key.SetValue(name, Desktop.StartupCommand(root));
                    else return;
                    autostartItem.Checked = command == null;
                }
            }
        });
    }

    void ToggleAutoCheck()
    {
        bool enable = !autoCheckItem.Checked;
        string error;
        var result = Desktop.RunJson(root, "update configure --root " + Desktop.Quote(root) + " --enabled " + (enable ? "true" : "false"), out error);
        if (result == null)
        {
            MessageBox.Show("无法修改：当前版本低于平台要求的最低版本时必须保持更新检查。", Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Information);
            return;
        }
        updateEnabled = Desktop.Flag(result, "enabled");
        autoCheckItem.Checked = updateEnabled;
    }

    void ResetPython()
    {
        if (busy || restarting) return;
        if (MessageBox.Show("将停止 Auriga，删除个人安装的 Python 包并重建环境。索引设置会保留。是否继续？", Desktop.Brand,
            MessageBoxButtons.YesNo, MessageBoxIcon.Warning) != DialogResult.Yes) return;
        bool wasRunning = launcher != null && !launcher.HasExited;
        busy = true;
        restarting = true;
        UpdateMenu();
        ThreadPool.QueueUserWorkItem(delegate
        {
            string error = null;
            string failureReason = "python_user.reset_failed";
            Dictionary<string, object> result = null;
            try
            {
                if (!StopLauncher(20, 10, 45)) failureReason = "python_user.stop_failed";
                else
                {
                    result = Desktop.RunJson(root, "python reset --root " + Desktop.Quote(root), out error, StartUpdateWorker);
                    failureReason = Desktop.PythonFailureReason(error);
                }
            }
            catch (Exception) { failureReason = "python_user.tray_failed"; }
            Post(delegate
            {
                busy = false;
                restarting = false;
                if (result == null) Log("Python reset failed [" + failureReason + "]");
                if (exiting) return;
                RefreshInfo();
                if (wasRunning) StartLauncher();
                MessageBox.Show(result != null ? "个人 Python 环境已重置。" : "重置失败，请检查日志后重试。", Desktop.Brand,
                    MessageBoxButtons.OK, result != null ? MessageBoxIcon.Information : MessageBoxIcon.Warning);
            });
        });
    }

    void ToggleAutoDownload()
    {
        try { AutoDownload = !autoDownloadItem.Checked; autoDownloadItem.Checked = !autoDownloadItem.Checked; }
        catch (Exception) { }
    }

    void CopyOrigin()
    {
        string origin = Desktop.Str(info, "platformOrigin");
        if (origin != null) Clipboard.SetText(origin);
    }

    void OpenLogs()
    {
        string logs = Desktop.Str(info, "logs");
        if (logs != null) { Directory.CreateDirectory(logs); Desktop.Open(logs); }
    }

    void About()
    {
        RefreshInfo();
        var c = Desktop.Obj(info, "components");
        string text = Desktop.Brand + "\n\n版本：" + Desktop.Str(info, "version") +
            "\n包标识：" + Desktop.Str(info, "packageId") +
            "\n\nDSH：" + Desktop.Str(c, "dsh") + "\nNode.js：" + Desktop.Str(c, "node") +
            "\nAuriga 插件：" + Desktop.Str(c, "plugin") + "\npnpm：" + Desktop.Str(c, "pnpm") +
            "\n\n安装位置：" + root + "\n数据目录：" + Desktop.Str(info, "dataHome") +
            "\n\n基于开源 DSH（MIT），非 DeepSeek 官方产品。";
        MessageBox.Show(text, "关于 " + Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Information);
    }

    public void ExitApp()
    {
        if (exiting) return;
        exiting = true;
        UpdateMenu();
        ShowOrUpdateProgress(
            updateApplying || restarting ? "正在退出（等待更新步骤结束）…" : "正在退出 Auriga…",
            "正在退出");
        // Normal exit must not wait minutes for a graceful stop; force after a short grace.
        // Updates still get a longer window so an in-flight apply can finish or roll back cleanly.
        int stopSeconds = updateApplying || restarting ? 60 : 15;
        int idleUpdateSeconds = updateApplying || restarting ? 60 : 10;
        int maxTotalSeconds = updateApplying || restarting ? 180 : 30;
        ThreadPool.QueueUserWorkItem(delegate
        {
            StopUpdateWorker();
            StopLauncher(stopSeconds, idleUpdateSeconds, maxTotalSeconds);
            Post(Quit);
        });
    }

    public void ShowRequested() { OpenAuriga(); }

    void Quit()
    {
        CloseUpdateProgress();
        checkTimer.Stop();
        pendingTimer.Stop();
        icon.Visible = false;
        icon.Dispose();
        ExitThread();
    }

    public void AnnounceUpdated()
    {
        icon.ShowBalloonTip(6000, Desktop.Brand, "已更新到 " + Desktop.Str(info, "version") + "。", ToolTipIcon.Info);
    }
}

sealed class ProgressForm : Form
{
    readonly Label label = new Label { Left = 20, Top = 18, Width = 380, Height = 48 };
    readonly ProgressBar bar = new ProgressBar { Left = 20, Top = 72, Width = 380, Height = 18, Style = ProgressBarStyle.Marquee };
    readonly Button cancel = new Button { Text = "取消", Left = 310, Top = 104, Width = 90 };
    public event EventHandler Cancelled;
    public bool WasCancelled { get; private set; }

    public ProgressForm(string title, string text, bool cancellable)
    {
        Text = title;
        Icon = Desktop.AppIcon();
        AutoScaleMode = AutoScaleMode.Dpi;
        ClientSize = new Size(420, cancellable ? 142 : 108);
        FormBorderStyle = FormBorderStyle.FixedDialog;
        MaximizeBox = false; MinimizeBox = false; ControlBox = false;
        StartPosition = FormStartPosition.CenterScreen;
        ShowInTaskbar = true;
        TopMost = true;
        Font = new Font("Microsoft YaHei UI", 9);
        label.Text = text;
        Controls.Add(label); Controls.Add(bar);
        if (cancellable)
        {
            cancel.Click += delegate { WasCancelled = true; cancel.Enabled = false; label.Text = "正在取消…（已下载部分保留，下次可续传）"; if (Cancelled != null) Cancelled(this, EventArgs.Empty); };
            Controls.Add(cancel);
        }
    }

    public void Report(string text, int percent)
    {
        if (IsDisposed) return;
        label.Text = text;
        if (percent < 0) bar.Style = ProgressBarStyle.Marquee;
        else { bar.Style = ProgressBarStyle.Continuous; bar.Value = Math.Max(0, Math.Min(100, percent)); }
    }
}

sealed class UninstallForm : Form
{
    readonly string root;
    readonly Label title = new Label { Left = 24, Top = 18, Width = 440, Height = 30, Font = new Font("Microsoft YaHei UI", 13) };
    readonly Label text = new Label { Left = 24, Top = 56, Width = 440, Height = 48 };
    readonly CheckBox purge = new CheckBox { Left = 24, Top = 108, Width = 440, Text = "同时删除用户数据（配置、会话和登录信息）" };
    readonly ProgressBar bar = new ProgressBar { Left = 24, Top = 140, Width = 440, Height = 18, Visible = false };
    readonly Button go = new Button { Text = "卸载", Left = 274, Top = 176, Width = 90 };
    readonly Button close = new Button { Text = "取消", Left = 374, Top = 176, Width = 90 };
    bool uninstalling;
    public int Result;

    public UninstallForm(string root)
    {
        this.root = root;
        Text = "卸载 " + Desktop.Brand;
        Icon = Desktop.AppIcon();
        ClientSize = new Size(490, 216);
        FormBorderStyle = FormBorderStyle.FixedDialog;
        MaximizeBox = false; MinimizeBox = false;
        StartPosition = FormStartPosition.CenterScreen;
        Font = new Font("Microsoft YaHei UI", 9);
        title.Text = "卸载 " + Desktop.Brand;
        text.Text = "将从此电脑删除 " + Desktop.Brand + "（安装位置：" + root + "）。\n默认保留用户数据，重新安装后可继续使用。";
        go.Click += delegate { Run(); };
        close.Click += delegate { Close(); };
        FormClosing += delegate (object sender, FormClosingEventArgs e) { e.Cancel = uninstalling; };
        Controls.AddRange(new Control[] { title, text, purge, bar, go, close });
        AcceptButton = go; CancelButton = close;
    }

    void Step(string message, int percent)
    {
        text.Text = message;
        bar.Value = percent;
        Refresh();
    }

    void Run()
    {
        uninstalling = true;
        go.Enabled = false; close.Enabled = false; purge.Enabled = false; bar.Visible = true;
        bool purgeData = purge.Checked;
        Step("正在退出 " + Desktop.Brand + "…", 10);
        if (!Program.StopRunning(root, 300))
        {
            uninstalling = false;
            Result = 1;
            Step("无法退出正在运行的 " + Desktop.Brand + "。请先从托盘菜单退出后重试。", 10);
            close.Enabled = true; close.Text = "关闭";
            return;
        }
        Step("正在删除程序文件、快捷方式和应用登记…", 40);
        bar.Style = ProgressBarStyle.Marquee;
        int code = -1;
        bool dataKept = true;
        string script = Path.Combine(root, "bin", "uninstall.ps1");
        var thread = new Thread(() =>
        {
            try
            {
                var start = new ProcessStartInfo(Path.Combine(Environment.SystemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe"),
                    "-NoProfile -ExecutionPolicy Bypass -File " + Desktop.Quote(script) + " -Quiet -ReportResult" + (purgeData ? " -PurgeData" : ""))
                { UseShellExecute = false, CreateNoWindow = true, WorkingDirectory = Path.GetTempPath(), RedirectStandardOutput = true };
                using (var p = Process.Start(start))
                {
                    for (string line; (line = p.StandardOutput.ReadLine()) != null;)
                        if (line.StartsWith("{")) dataKept = Desktop.Flag(Desktop.Json.Deserialize<Dictionary<string, object>>(line), "dataKept");
                    p.WaitForExit(); code = p.ExitCode;
                }
            }
            catch (Exception) { code = -1; }
        });
        thread.Start();
        while (thread.IsAlive) { Application.DoEvents(); Thread.Sleep(100); }
        bar.Style = ProgressBarStyle.Continuous;
        if (code == 0 || code == 3010)
        {
            Result = code;
            string done = !dataKept ? "已卸载。该安装独占的用户数据已删除。"
                : purgeData ? "已卸载。无法确认数据目录独占或部分数据仍在使用，未完成数据删除。"
                : "已卸载。用户数据已保留，重新安装后可继续使用。";
            if (code == 3010) done += "\n仍被占用的文件会在下次登录 Windows 时清理。";
            Step(done, 100);
        }
        else
        {
            Result = 1;
            Step("卸载未完成（代码 " + code + "）。请关闭所有 " + Desktop.Brand + " 窗口后重试。", bar.Value);
        }
        uninstalling = false;
        close.Text = "完成"; close.Enabled = true;
    }
}

static class Program
{
    /// Ask the tray of this root to exit (it stops DSH); wait until its instance lock is free.
    public static bool StopRunning(string root, int seconds)
    {
        string key = Desktop.Key(root);
        EventWaitHandle exit;
        if (EventWaitHandle.TryOpenExisting(key + "-exit", out exit)) using (exit) exit.Set();
        DateTime until = DateTime.UtcNow.AddSeconds(seconds);
        while (DateTime.UtcNow < until)
        {
            bool created;
            using (var mutex = new Mutex(false, key, out created))
            {
                bool owned;
                try { owned = mutex.WaitOne(0); } catch (AbandonedMutexException) { owned = true; }
                if (owned) { mutex.ReleaseMutex(); break; }
            }
            Thread.Sleep(250);
        }
        if (DateTime.UtcNow >= until) return false;
        // The installer core checks the legacy session and writer identities.
        return true;
    }

    static string RootFromExe()
    {
        // <root>\versions\<id>\auriga\tray\AurigaDesktop.exe
        string dir = Path.GetDirectoryName(Assembly.GetExecutingAssembly().Location);
        return Path.GetFullPath(Path.Combine(dir, "..", "..", "..", ".."));
    }

    [STAThread]
    static int Main(string[] args)
    {
        Application.EnableVisualStyles();
        Application.SetCompatibleTextRenderingDefault(false);
        if (args.Length == 2 && args[0].Equals("/stop", StringComparison.OrdinalIgnoreCase))
            return StopRunning(Path.GetFullPath(args[1]), 300) ? 0 : 1;
        int u = Array.FindIndex(args, a => a.Equals("/uninstall", StringComparison.OrdinalIgnoreCase));
        if (u >= 0)
        {
            string target = u + 1 < args.Length ? Path.GetFullPath(args[u + 1]) : RootFromExe();
            if (!File.Exists(Path.Combine(target, ".auriga-desktop-install"))) return 2;
            using (var form = new UninstallForm(target)) { Application.Run(form); return form.Result; }
        }
        string root = RootFromExe();
        if (!File.Exists(Path.Combine(root, ".auriga-desktop-install")) || !File.Exists(Path.Combine(root, "current.txt")))
        {
            MessageBox.Show("未找到 " + Desktop.Brand + " 安装。", Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Error);
            return 2;
        }
        bool restarted = Array.Exists(args, a => a.Equals("/restarted", StringComparison.OrdinalIgnoreCase));
        bool autostart = Array.Exists(args, a => a.Equals("/autostart", StringComparison.OrdinalIgnoreCase));
        string key = Desktop.Key(root);
        bool created;
        using (var mutex = new Mutex(false, key, out created))
        {
            bool owned;
            try { owned = mutex.WaitOne(restarted ? 30000 : 0); }
            catch (AbandonedMutexException) { owned = true; }
            if (!owned)
            {
                // Second start: hand the "open" request to the running tray and leave.
                EventWaitHandle show;
                if (!autostart && !restarted && EventWaitHandle.TryOpenExisting(key + "-show", out show)) using (show) show.Set();
                return 0;
            }
            using (var show = new EventWaitHandle(false, EventResetMode.AutoReset, key + "-show"))
            using (var exit = new EventWaitHandle(false, EventResetMode.ManualReset, key + "-exit"))
            {
                var app = new TrayApp(root, autostart || restarted);
                if (Array.Exists(args, a => a.Equals("/updated", StringComparison.OrdinalIgnoreCase))) app.AnnounceUpdated();
                var watcher = new Thread(() =>
                {
                    var handles = new WaitHandle[] { show, exit };
                    for (;;)
                    {
                        int which = WaitHandle.WaitAny(handles);
                        if (which == 0) app.Post(app.ShowRequested);
                        else { app.Post(app.ExitApp); return; }
                    }
                }) { IsBackground = true };
                watcher.Start();
                Application.Run(app);
                mutex.ReleaseMutex();
            }
        }
        return 0;
    }
}
