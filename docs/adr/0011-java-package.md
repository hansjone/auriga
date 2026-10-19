# 0011 Java 包名和 Maven groupId 用 io.github.hansjone.auriga

- 状态：已采纳
- 日期：2026-10-19

## 背景

包名写进契约（protobuf 的 `java_package`）和所有 Java 代码，以后改代价很大；暂不注册官网域名。

## 决定

Java 包名根和 Maven groupId 都用 `io.github.hansjone.auriga`。Go 模块路径用 `github.com/hansjone/auriga/...`。

## 后果

以后即使注册了官网域名也不改包名。
