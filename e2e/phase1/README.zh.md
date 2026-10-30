# 第一阶段统一浏览器验收

统一入口为 [run.mjs](./run.mjs)，完整中文说明见 [README.md](./README.md)。
②③复用 cloud-ready.mjs 的真实邀请、MFA、空会话登录、本人云端 READY 与托管插件步骤；该文件仅作为模块，不再单独执行。
Playwright 依赖仅声明并锁定在 e2e/package.json 与 e2e/package-lock.json。

全新部署按 [公开 Compose 文档](../../docs/dev/compose-access.md) 和 [真实 DSH 配方](../../docs/dev/m2.md#真实-dsh-与托管插件w3)，显式设置 DSH_FIXTURE=real。
WSL 使用原生 Linux 私密目录；从 Windows 导出源码时使用 git -c tar.umask=0022 archive 保留执行位与合格目录权限。
部署自己的代码前按工作区共享栈规则领取 compose-stack 锁，用完更新 shared-stack.txt 并释放自己的锁。
接口复验直接复用共享栈，保持其他成员、实例与数据；不碰 NAS、系统 DNS/信任或付费模型。
