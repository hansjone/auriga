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
    readonly ToolStripMenuItem openItem, statusItem, platformItem, checkItem, updateItem, restartItem,
        autostartItem, autoCheckItem, autoDownloadItem, originItem, logsItem, aboutItem, exitItem;
    readonly System.Windows.Forms.Timer checkTimer = new System.Windows.Forms.Timer();
    readonly System.Windows.Forms.Timer pendingTimer = new System.Windows.Forms.Timer();
    Process launcher;
    string dshUrl, platformState, platformReason, availableVersion, logFile;
    readonly object updateLock = new object();
    Process updateWorker;
    bool openWhenReady, exiting, restarting, busy, restartRequired, updateEnabled = true;
    Dictionary<string, object> info;

    public TrayApp(string root, bool autostart)
    {
        this.root = root;
        this.autostart = autostart;
        ui.CreateControl();
        icon.Icon = Desktop.AppIcon();
        icon.Text = Desktop.Brand;
        openItem = new ToolStripMenuItem("打开 Auriga", null, delegate { OpenAuriga(); }) { Font = new Font(menu.Font, FontStyle.Bold) };
        statusItem = new ToolStripMenuItem("正在启动…") { Enabled = false };
        platformItem = new ToolStripMenuItem(Desktop.PlatformText(null, null)) { Enabled = false };
        checkItem = new ToolStripMenuItem("检查更新", null, delegate { CheckUpdate(true); });
        updateItem = new ToolStripMenuItem("立即更新", null, delegate { DownloadUpdate(); }) { Visible = false };
        restartItem = new ToolStripMenuItem("重启以完成更新", null, delegate { RestartForUpdate(); }) { Visible = false };
        autostartItem = new ToolStripMenuItem("开机自动启动", null, delegate { ToggleAutostart(); });
        autoCheckItem = new ToolStripMenuItem("自动检查更新", null, delegate { ToggleAutoCheck(); });
        autoDownloadItem = new ToolStripMenuItem("有更新时自动下载（安装前询问）", null, delegate { ToggleAutoDownload(); });
        originItem = new ToolStripMenuItem("平台地址：未配置", null, delegate { CopyOrigin(); });
        var settings = new ToolStripMenuItem("设置");
        settings.DropDownItems.AddRange(new ToolStripItem[] { autostartItem, autoCheckItem, autoDownloadItem, new ToolStripSeparator(), originItem });
        logsItem = new ToolStripMenuItem("打开日志目录", null, delegate { OpenLogs(); });
        aboutItem = new ToolStripMenuItem("关于 " + Desktop.Brand, null, delegate { About(); });
        exitItem = new ToolStripMenuItem("退出", null, delegate { ExitApp(); });
        menu.Items.AddRange(new ToolStripItem[] {
            openItem, statusItem, platformItem, new ToolStripSeparator(),
            checkItem, updateItem, restartItem, new ToolStripSeparator(),
            settings, logsItem, aboutItem, new ToolStripSeparator(), exitItem });
        icon.ContextMenuStrip = menu;
        icon.MouseDoubleClick += delegate (object s, MouseEventArgs e) { if (e.Button == MouseButtons.Left) OpenAuriga(); };
        icon.BalloonTipClicked += delegate { if (restartRequired) RestartForUpdate(); else if (availableVersion != null) DownloadUpdate(); };
        icon.Visible = true;
        checkTimer.Tick += delegate { checkTimer.Interval = 6 * 3600 * 1000; CheckUpdate(false); };
        // An update downloaded from the DSH settings page also waits for a restart: offer it here.
        pendingTimer.Interval = 30 * 1000;
        pendingTimer.Tick += delegate
        {
            bool queued = File.Exists(Path.Combine(root, "desktop-pending"));
            if (queued && !restartRequired && !busy)
            {
                restartRequired = true;
                icon.ShowBalloonTip(10000, Desktop.Brand, "更新已下载。点击此处重启 Auriga 完成安装。", ToolTipIcon.Info);
            }
            else if (!queued && restartRequired && !busy) restartRequired = false;
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

    void RefreshInfo()
    {
        string error;
        info = Desktop.RunJson(root, "status --root " + Desktop.Quote(root), out error);
        if (info == null) return;
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
    void RefreshShellIntegration()
    {
        try
        {
            Desktop.ShellChange(delegate
            {
                string name = Desktop.RegistryName(root);
                using (var key = Registry.CurrentUser.OpenSubKey(Registration + name, true))
                {
                    if (key == null || info == null ||
                        !Desktop.SamePath(key.GetValue("InstallLocation") as string, root)) return;
                    if (Desktop.OwnsStartup(root, Registry.GetValue(RunKey, name, null) as string))
                        Registry.SetValue(RunKey, name, Desktop.StartupCommand(root));
                    key.SetValue("DisplayVersion", Desktop.Str(info, "version") ?? "");
                    key.SetValue("Publisher", "Auriga");
                    string ico = Path.Combine(root, "bin", "auriga.ico");
                    if (File.Exists(ico)) key.SetValue("DisplayIcon", ico);
                }
                EnsureShortcuts();
            });
        }
        catch (Exception e) { Log("shell refresh failed: " + e.Message); }
    }

    static object Com(object target, string member, BindingFlags kind, params object[] args)
    {
        return target.GetType().InvokeMember(member, kind, null, target, args);
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
        object wsh = Activator.CreateInstance(Type.GetTypeFromProgID("WScript.Shell"));
        string launcher = Path.Combine(root, "bin", "auriga-desktop.cmd");
        string start = links.Find(l => File.Exists(l) && l.EndsWith(".lnk", StringComparison.OrdinalIgnoreCase) &&
            Desktop.SamePath(Convert.ToString(Com(Com(wsh, "CreateShortcut", BindingFlags.InvokeMethod, l),
                "TargetPath", BindingFlags.GetProperty)), launcher));
        if (start == null) return;
        string uninstall = Path.Combine(Path.GetDirectoryName(start), "卸载 " + Path.GetFileName(start));
        string cmd = Path.Combine(root, "bin", "uninstall.cmd");
        string ico = Path.Combine(root, "bin", "auriga.ico");
        if (!File.Exists(cmd)) return;
        if (File.Exists(start) && File.Exists(ico))
        {
            object link = Com(wsh, "CreateShortcut", BindingFlags.InvokeMethod, start);
            string target = Convert.ToString(Com(link, "TargetPath", BindingFlags.GetProperty));
            if (target.Equals(Path.Combine(root, "bin", "auriga-desktop.cmd"), StringComparison.OrdinalIgnoreCase))
            {
                Com(link, "IconLocation", BindingFlags.SetProperty, ico + ",0");
                Com(link, "Save", BindingFlags.InvokeMethod);
            }
        }
        if (links.Exists(l => l.Equals(uninstall, StringComparison.OrdinalIgnoreCase)) && File.Exists(uninstall)) return;
        object remove = Com(wsh, "CreateShortcut", BindingFlags.InvokeMethod, uninstall);
        if (File.Exists(uninstall) && !Desktop.SamePath(Convert.ToString(Com(remove, "TargetPath", BindingFlags.GetProperty)), cmd))
            return;
        Com(remove, "TargetPath", BindingFlags.SetProperty, cmd);
        Com(remove, "WorkingDirectory", BindingFlags.SetProperty, Environment.SystemDirectory);
        Com(remove, "WindowStyle", BindingFlags.SetProperty, 7);
        Com(remove, "Description", BindingFlags.SetProperty, "卸载 " + Desktop.Brand);
        if (File.Exists(ico)) Com(remove, "IconLocation", BindingFlags.SetProperty, ico + ",0");
        Com(remove, "Save", BindingFlags.InvokeMethod);
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
        statusItem.Text = restarting ? "正在重启…" : running ? "运行中 · 版本 " + (Desktop.Str(info, "version") ?? "?") : "未运行（点“打开 Auriga”启动）";
        platformItem.Text = running ? Desktop.PlatformText(platformState, platformReason) : "平台：未运行";
        updateItem.Visible = availableVersion != null && !restartRequired;
        updateItem.Text = "立即更新到 " + availableVersion;
        updateItem.Enabled = !busy;
        restartItem.Visible = restartRequired;
        checkItem.Enabled = !busy && Desktop.Flag(info, "updatesConfigured");
        icon.Text = (Desktop.Brand + " · " + (running ? (platformState == "authenticated" ? "已连接" : "运行中") : "未运行")).Substring(0, Math.Min(63, (Desktop.Brand + " · " + (running ? (platformState == "authenticated" ? "已连接" : "运行中") : "未运行")).Length));
    }

    void StartLauncher()
    {
        if (launcher != null && !launcher.HasExited) return;
        dshUrl = null; platformState = null; platformReason = null;
        loginRequested = false; useLocalDsh = false; localLogin = false;
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
    }

    void OnEvent(Dictionary<string, object> e)
    {
        string kind = Desktop.Str(e, "event");
        if (kind == "platform")
        {
            string previous = platformState;
            platformState = Desktop.Str(e, "state");
            platformReason = Desktop.Str(e, "reason");
            Log("platform " + platformState + (platformReason != null ? " " + platformReason : ""));
            if (platformState == "authenticated" && previous != null && previous != "authenticated")
                icon.ShowBalloonTip(4000, Desktop.Brand, "已登录 Auriga 平台。", ToolTipIcon.Info);
            MaybeOpen();
        }
        else if (kind == "login" && !Desktop.Flag(e, "opened"))
        {
            // A skipped or unavailable sign-in can continue into local DSH.
            useLocalDsh = true;
            localLogin = Desktop.Str(e, "state") == "local_login";
            MaybeOpen();
        }
        UpdateMenu();
    }

    bool loginRequested, useLocalDsh, localLogin;

    /// Startup and "Open" request the sign-in page first. DSH follows after sign-in.
    void MaybeOpen()
    {
        if (!openWhenReady || platformState == null) return;
        if (useLocalDsh || platformState == "authenticated" || platformState == "unavailable")
        {
            if (dshUrl == null) return;
            openWhenReady = false;
            var url = new UriBuilder(dshUrl);
            if (localLogin && string.IsNullOrEmpty(url.Fragment))
                url.Fragment = "auriga=login";
            Desktop.Open(url.Uri.AbsoluteUri);
        }
        else if (!loginRequested)
        {
            loginRequested = true;
            Send("login");
        }
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
        loginRequested = false;
        useLocalDsh = false;
        localLogin = false;
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
        int code = -1;
        try { code = which.ExitCode; } catch (Exception) { }
        Log("launcher exited " + code);
        if (!exiting && !restarting)
        {
            if (lastError == "already-running")
                MessageBox.Show(Desktop.Brand + " 已在运行（可能是旧版窗口），请先关闭它后再从开始菜单打开。", Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Information);
            else if (code != 0)
                icon.ShowBalloonTip(5000, Desktop.Brand, "Auriga 已停止（代码 " + code + "）。点“打开 Auriga”重新启动，详情见日志目录。", ToolTipIcon.Warning);
            lastError = null;
        }
        UpdateMenu();
    }

    /// Ask the launcher to stop DSH (it applies a queued update on the way out) and wait.
    bool StopLauncher(int seconds)
    {
        var p = launcher;
        if (p == null || p.HasExited) return true;
        Send("stop");
        try { p.StandardInput.Close(); } catch (Exception) { }
        if (p.WaitForExit(seconds * 1000)) { p.WaitForExit(); return p.ExitCode == 0; }
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
                        Post(delegate { form.Report(phase == "verifying" ? "正在校验签名和文件…" : string.Format("已下载 {0:0.0} / {1:0.0} MB", done / 1048576.0, total / 1048576.0), total > 0 ? (int)(done * 100 / total) : -1); });
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
        if (busy || restarting) return;
        string error;
        var approved = Desktop.RunJson(root, "update approve --root " + Desktop.Quote(root), out error);
        if (!Desktop.Flag(approved, "approved"))
        {
            Log("update confirmation failed: " + error);
            RefreshInfo();
            return;
        }
        restarting = true; busy = true; UpdateMenu();
        string before = Desktop.Current(root);
        var form = new ProgressForm("正在安装更新", "正在停止 Auriga 并安装更新，请稍候…", false);
        form.Show();
        if (launcher == null || launcher.HasExited) StartLauncher();
        ThreadPool.QueueUserWorkItem(delegate
        {
            bool stopped = StopLauncher(600);
            Post(delegate
            {
                form.Close();
                busy = false;
                string after = Desktop.Current(root);
                if (stopped && after != before)
                {
                    Log("update installed " + before + " -> " + after);
                    // Continue from the new version's tray (new menu, runtime and icon).
                    string next = Path.Combine(root, "versions", after, "auriga", "tray", "AurigaDesktop.exe");
                    if (File.Exists(next))
                    {
                        exiting = true;
                        Process.Start(new ProcessStartInfo(next, "/restarted /updated") { UseShellExecute = false });
                        Quit();
                        return;
                    }
                    restarting = false;
                    restartRequired = false;
                    RefreshInfo();
                    StartLauncher();
                    icon.ShowBalloonTip(6000, Desktop.Brand, "已更新到 " + Desktop.Str(info, "version") + "。", ToolTipIcon.Info);
                }
                else
                {
                    restarting = false;
                    RefreshInfo();
                    if (stopped) StartLauncher();
                    string reason = stopped
                        ? "更新安装失败，已保留原版本。详情见日志目录。"
                        : "DSH 未完成正常停止，本次未继续更新。请检查日志后重新打开。";
                    MessageBox.Show(reason, Desktop.Brand, MessageBoxButtons.OK, MessageBoxIcon.Warning);
                }
                UpdateMenu();
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
        icon.Text = Desktop.Brand + " · 正在退出";
        ThreadPool.QueueUserWorkItem(delegate { StopUpdateWorker(); StopLauncher(300); Post(Quit); });
    }

    public void ShowRequested() { OpenAuriga(); }

    void Quit()
    {
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
    readonly Label label = new Label { Left = 20, Top = 20, Width = 380, Height = 40 };
    readonly ProgressBar bar = new ProgressBar { Left = 20, Top = 66, Width = 380, Height = 18, Style = ProgressBarStyle.Marquee };
    readonly Button cancel = new Button { Text = "取消", Left = 310, Top = 98, Width = 90 };
    public event EventHandler Cancelled;
    public bool WasCancelled { get; private set; }

    public ProgressForm(string title, string text, bool cancellable)
    {
        Text = title;
        Icon = Desktop.AppIcon();
        ClientSize = new Size(420, cancellable ? 136 : 100);
        FormBorderStyle = FormBorderStyle.FixedDialog;
        MaximizeBox = false; MinimizeBox = false; ControlBox = false;
        StartPosition = FormStartPosition.CenterScreen;
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
