# Beta 原生入口

- [本机 Beta 管理台与后台边界](../../docs/dev/beta-ui.md)
- [双隔离原生 DSH 互发演示](../../docs/dev/beta-network-send.md)：受信公开配置预检、隔离 launcher 与人工验收；默认 Beta 缺完整 N1/根签/可信 HTTPS，预检不代表 READY 或收发成功。

中转站使用 `runner.mjs relay --origin <内网后端根地址> --admin-password-file <管理秘密文件> --member-password-file <成员秘密文件>` 配置，两份凭据必须独立。参见 [中转站架构](../../docs/architecture/relay-bifrost.zh.md)。
