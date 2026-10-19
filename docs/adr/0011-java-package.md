# 0011 Java 包名和 Maven groupId 用 auriga

- 状态：已采纳（2026-10-19 修订：由 `io.github.hansjone.auriga` 改为 `auriga`）
- 日期：2026-10-19

## 背景

包名写进契约（protobuf 的 `java_package`）和所有 Java 代码，以后改代价很大；暂不注册官网域名。
最初用 `io.github.hansjone.auriga`，但它把个人账号写进每个包和目录，路径也长，不利于开源后别人参与；项目未发布，趁早改。

## 决定

Java 包名根和 Maven groupId 都用 `auriga`（例如 `auriga.iam.api`、`auriga.contracts.iam.v1`）。Go 模块路径仍是仓库地址 `github.com/hansjone/auriga/...`（Go 要求模块路径可下载，跟着仓库走）。

## 后果

- 目录短：`services/iam/api/src/main/java/auriga/iam/api/`。
- 不发布到 Maven Central（那里要求反向域名 groupId）；以后真要发布再定 groupId，包名不变。
- `java_package` 只影响 Java 生成代码，不影响线上协议；`contracts/buf.yaml` 的兼容性检查排除 `FILE_SAME_JAVA_PACKAGE`。
