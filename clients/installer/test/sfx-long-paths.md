# Windows 向导长路径回归

安装器使用 Windows 自带的 .NET Framework 4.6.2 或更新版本。在任何路径 API 调用前设置进程内 System.IO 兼容开关，文件解包与递归清理使用经过校验的绝对扩展路径。无需修改系统长路径注册表或用户 TEMP。图形和静默入口共用包内路径检查与解包逻辑，随后仍调用原 `install.ps1` 安装事务。

在 Windows 上运行（先把当前进程 TEMP/TMP 设到 D 盘测试目录）：

```powershell
node --test clients/installer/test/sfx-long-paths.test.mjs clients/installer/test/setup-archive.test.mjs
```

测试会在 TEMP 下再创建更长的隔离 TEMP，编译真实 Stub.cs 与测试入口，用同一份深目录包验证：旧 API 首错、现代路径解包、解包中取消、越界目录和文件拒绝、静默退出码转发、真实 Wizard 进度完成、失败页面和临时文件清理。向导的 install.ps1 是隔离夹具，不写注册表、快捷方式或真实安装，也不启动 DSH。

额外验证精确 0.1.5/110 的完整 39646 文件依赖树：

```powershell
$env:AURIGA_SFX_RELEASE_EXE = '<只读发布包路径>\AurigaDSH-Setup-0.1.5.exe'
node --test clients/installer/test/sfx-long-paths.test.mjs
```

此项先断言正式 EXE SHA-256 为 `73c042a53f22c4852e03051c78724709cd6e0adbd8e212cee5f1505ffdbaf0de`，仅将其作为归档数据读取，不执行旧 EXE。成功应输出 `files=39646 bytes=729098089`、最长实际路径长度及 `cleaned=True`。未提供环境变量时，常规回归仍执行全部合成包检查。

这不替代新发行包的完整图形升级验收；旧版本桥接、托盘、插件、卸载和回退属于后续隔离端到端验证。
