# 源码迁入与许可

本轮仅从 LOCALPORT1 prepared 迁入 `utils/atomic-write.js` 和 `utils/secret-box.js`，它们改编自 `dsh-team@0.5.0` 的 `lib/utils/{atomic-write,secret-box}.js`。完整原 MIT 许可保留如下。其余 platform/vault/runtime/host/routes/team 为本批新增；登录视图复用 Auriga 已有原生步骤渲染器。

未迁入 prepared 会话 store、marker、legacy Desk 或占位 login503。新包没有跨仓 import、旧 API 或旧数据目录自动探测。密钥、文件格式和 Cookie 均属于 Auriga 新本机状态；本地文件加密不宣称能对抗已控制同一 OS 用户的攻击者。

MIT License

Copyright (c) 2026 dsh-team contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

# AUTH-D 企业 skill 与外壳状态适配

`enterprise.js` 按原型 `dsh-team/lib/agent-auth.js` 的请求/响应语义迁入，
保留 `/dsh-team/agent-credentials`、`/dsh-team/outbound` 及环境变量名。
企业真实主机和字段映射不迁入，改由受信私有配置提供；空主机清单拒绝全部。
`skills/uds-skill-auth/scripts/uds_skill_auth.py` 保留原 helper 的 import 名称和调用方式。
`enterprise-environment.js` 沿原型 `task-environment.js` 的公开 shellEnv、tools/result、
jobs 生命周期释放任务能力。以上模块当前尚未挂入 `plugin.js`。

`shell-state.js` 消费冻结的 ShellAuthCommand/Event/Snapshot；其 transport 参数只由
受信原生后端提供，不是网页配置或 DSH HTTP 服务。`send(command)` 返回同 requestId
事件，`subscribe(onEvent,onDisconnect)` 在同一已认证连接注册接收器并返回解除函数。
这是插件内部适配端口，不宣称已有 OS IPC 实现。C 的 OS 身份验证、制品 manifest
和真实连接适配完成后才能接入，缺失时禁止用普通 HTTP 或伪身份头替代。

当前契约缺少插件后端到 broker 的企业凭据读取 IPC：控制 action 无此操作，
固定业务 operation 无企业凭据读取，而 `/dsh-team/agent-credentials` 明确仅属
回环 HTTP + 任务能力端口。`createEnterpriseAuth` 的 `credentials()` 因此仍是
受信内部依赖，测试用替身；不得把旧平台令牌重新放回插件以绕过缺口。
