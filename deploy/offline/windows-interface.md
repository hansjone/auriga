# Windows入口与客户端接口

OFFLINE-ONECLICK提供根auriga.ps1（PowerShell5.1）及发行源码内bin/auriga-cli.exe。服务端verify/unpack/changes/devkit verify/merge/doctor/bundle由Go实现。
服务端Windows构建选项为--backend auto|docker|wsl和--wsl-distro；传输通过参数数组/二进制tar流，不拼用户shell片段。

已fetch核对SITE-CLIENT-RELEASE远端ae9c57db（含06bbc6f1）。其实际入口为scripts/client-release/cli.mjs，已接受并剥离首参数client，auriga.cmd从AURIGA_CLIENT_DEVKIT/build-node/node.exe启动。
本框架据此直接挂接：auriga.ps1检测client，使用AURIGA_CLIENT_DEVKIT下build-node/node.exe（优先），其次AURIGA_CLIENT_NODE，最后PATH中的node.exe；把client及其余实参数组完整交给cli.mjs，透传退出码，不经过cmd /c，不引入新的客户端命令实现。
客户端扩展未合入时明确拒绝，服务端命令不依赖Node。PowerShell5.1回归已验证含空格参数与非零退出码透传。

客户端文件、依赖、keygen/build/sign/publish、权限与实测由SITE-CLIENT-RELEASE负责，本任务不复制或修改其实现。
主控先合本任务，再收客户端分支的auriga客户端三行挂接、auriga.cmd、desktop相关Go文件及安装器参数；MAINTAIN只取其“客户端发布”新章节，不能用整份旧文档覆盖当前无Git/Windows内容。

已发现需对方/主控接线的实际差异：该HEAD scripts/client-release/build.mjs:27-54仍强制git及.git，而服务端发行源码归档没有.git。客户端需接入auriga-source.json/规范化快照来源，或另明确提供其可用的离线历史；不能把本任务的服务端无Git验收当作客户端构建也已通过。
客户端真实打包/签名/发布/自动更新均不在本任务验收范围，未因此访问NAS或发布发行。
