# 共用 collab 安装输入

`Read` 读取显式批准的安装 JSON，校验租户、realm、版本、有效期、独立 Ed25519 SPKI
指纹、三个用途的 PKCS#8 私钥对应关系以及 Linux 属主/链接/权限。`CheckState` 比对批准
keyset 与安装状态；`Clear` 清除输入材料的内存快照。公开 JSON 字段见 NAS README。

compose 的 HTTPS 门禁仍在其配方入口，NAS 的 HTTP 基线和 HTTPS 呈现来源由 NAS
配方验证。地址、数据库角色、mTLS、收据和运行副本各由自己的配方负责。
此包不签名、不生成网络根、不请求外网、不宣称 READY。最终根签/schema、发布与数据库
防回滚仍由生产 Java `RootKeyPublication`、`SigningKeyLoader` 和 `NetworkKeyOperations` 执行。
