plugin-ready.json 由本仓库公开的 clients/dsh-plugin/src/network/session/frames.js 的 encodeReady 实际生成。
输入为零恢复状态与固定合成 requestId/channelNonce/checkpoint；未提供 adapterCapabilities 时保留该构造器的公开默认值。
NetworkWireSecurityTest 验证该正式帧及逐侧缺项，Wire PG 正向也使用同一帧；这不是实际 DSH 运行证据。
