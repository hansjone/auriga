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
