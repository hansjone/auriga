# 0022 域名与单 IPv4 部署的入口、会话和实例路径边界

- 状态：IP0 契约，待独立审查；运行时实现和真实验收由 IP1/IP2/IP3 交付
- 日期：2026-10-22
- 补充：ADR0020 的可信团队同源路径例外、ADR0021 的管理来源和内部身份通道

## 决定

1. 公众应能按公开流程部署。NAS 只是用户提供的真实验收场景，不能用现场私有手改代替通用功能。
   域名模式保留门户、管理台及每实例分源；纯 IP 首批选择一个 IPv4，门户/实例固定端口、管理台另一固定端口，
   实例`/i/<id>/`，不要求多IP、外部DNS或hosts。IP路径模式仅面向互信团队；同源实例仍能使用门户权限。
2. 安装配置是完整scheme/host/有效port、tenant和listener角色的唯一可信输入，issuer固定为门户Origin。
   authority不能只比hostname；管理audience不能由用户头/JSON选择。代理必须受认证且保留外部authority，
   Forwarded头不覆盖安装映射。规范和拒绝矩阵见`contracts/deployment/README.md`；结构schema之外仍需启动语义验证。
3. 两端口是两个Origin，cookie仍按host/path发送。管理session保持HttpOnly，仅管理入口消费；
   edge转实例前剥全部Auriga cookie/认证头，实例响应不能覆盖保留cookie。不能承诺浏览器按端口不发送管理cookie。
4. IP管理CSRF由精确同源POST登记并仅留每tab内存，不下发可读cookie；32字节安全随机token由浏览器提交，
   IAM只存hash/期限，JSON回显本次token，避免从csrfHash恢复明文及丢响应重试困境。
   精确Origin+JSON+非简单固定头+当前admin浏览器会话才允许该一个bootstrap操作免旧CSRF；固定头不是秘密。
   每会话最多32条/每条最多15分钟，同hash重试不延期，多tab不互相全局轮换；满额429，不驱逐有效token。
   管理写通过仅svc:edge的内部JWT/mTLS RPC逐次校IAM权威状态并绑定实际sid/account/tid/admin audience；
   KV为hash投影，不单独授予管理写权限。控制面不可用失败关闭，吊销不能被并发touch/登记复活。
   domain原按源cookie流程和门户csrfHash行为不变。新增接口/RPC仅契约，当前运行时尚不支持。
5. portal/admin事务按audience分名并绑定规范Origin/tenant。所有`__Host-`cookie必须Secure、Path=/、无Domain，
   修正旧事务cookie非根Path冲突；HTTP去前缀且无Secure。双会话共存、各自完成/取消/登出不影响另一入口。
6. IP实例票据保持60秒/一次性/nonce及session-instance绑定，兑票回该mount有尾斜杠根；实例cookie按mount Path选择，
   HTTPS用`__Secure-auriga_inst`而非`__Host-`，domain原根cookie不变。代理仅剥一次前缀，
   上游非Auriga cookie限制为host-only并重写到mount；Cookie Path不构成同源恶意代码隔离承诺。
7. 浏览器IP Origin与内部实例心跳入口解耦。受信Docker DNS alias无需外部DNS，不能是IP，保留runtime现DNS校验。
   alias专用listener只放行既有self/heartbeat精确方法路径与真实ait认证，不作为门户别名；实例仍只加入自身隔离网。
   不复用节点控制mTLS地址或把实例接进组件控制网；公开issuer/renderer浏览器Origin不能改成内部alias。
8. HTTP IP首验口令/TOTP；Passkey首批不支持，不能以IP冒充RP域名。IP2保持门锁、scheme+完整Origin和精确trustedHosts，
   不放宽CORS或删fence。浏览器WS没有任意自定义头能力，管理WS新能力须先定正式握手协议，不能擅自开放。

## 后果与验收

新配置schema、IAM管理CSRF登记、KV附加hash投影和校验RPC是先行小契约，生成Java/Go/TS类型，
不表示安装器、edge/IAM/agent、前端已消费。后续实现必须按deployment说明补启动验证、权威事务、精确路由和浏览器流程。
IP1入口/会话、IP2实例路径/renderer/plugin/内部心跳、IP3公开安装及真实浏览器双模式矩阵分别独立验收。
真实地址登录、刷新、多tab、A/B实例HTTP/资产/双向WS、撤销原连接、Origin/authority/nonce/重放负例、
上游无管理凭据及实例脚本不能读管理CSRF/执行管理写，都需要现场证据。schema、源码和伪Host探针不能替代。

HTTP本身无传输保密；用户明确选择它并接受可信网络前提，不以此降低内部mTLS、令牌用途/租户绑定和容器隔离。
域名与IP两分支同等回归，不能因纯IP接线使domain强分源退化。
