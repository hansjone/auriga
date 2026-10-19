# 0002 技术栈：Java 业务服务、Go 基础设施、Vue 前端

- 状态：已采纳
- 日期：2026-10-19

## 背景

业务逻辑多、要长期维护和招人；网关、节点代理这类组件要贴近操作系统、处理大量长连接；DSH 本身是 Node。

## 决定

业务服务用 Java 25 + Spring Boot 4.x（虚拟线程）+ MyBatis-Plus + Flyway；edge、agent、ai-gateway、egress、ctl 用 Go 最新稳定版；前端只用 Vue 3 + TypeScript + Vite + Pinia + Element Plus；DSH 插件用 Node。不用 Spring Cloud 全家桶、Keycloak、Kafka、Redis。

## 后果

三种语言靠契约统一（见 0015）；单机版不依赖重型中间件，所有内置组件都能在 Windows 上原生运行。
