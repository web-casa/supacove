# ADR-001：技术选型

日期：2026-10-03 · 状态：已接受（来自 dev-plan v2 §0，不再重议的部分仅记录结论）

## 选型

| 层 | 选择 | 一句话理由 |
|---|---|---|
| 后端 | Go 1.24 + chi（标准库 net/http 兼容） | 单静态二进制、部署简单、进程编排成熟 |
| 元数据库 | SQLite（modernc.org/sqlite，无 CGO）+ goose 迁移 | 单容器、无外部依赖；只存控制信息 |
| 数据访问 | sqlc（P2 起）| 备份任务的关键 SQL 显式可审查 |
| 目标库访问 | pgx | 连接检查/版本/统计采集 |
| 备份引擎 | 官方 pg_dump / pg_restore / psql（镜像内置多主版本） | 不重写导出逻辑；格式长期稳定 |
| 加密 | age（备份文件）+ AES-GCM（SQLite 内凭据） | 密钥分离见 dev-plan §0.5 协议 B |
| 前端 | React + TS + Vite + TanStack Query + shadcn/ui | 登录后管理后台，无 SSR 需求；go:embed 单二进制 |
| 契约 | OpenAPI 先行；oapi-codegen + openapi-typescript | 前后端类型一致；CI 校验生成物同步 |
| 对象存储 | AWS SDK for Go v2（transfermanager，钉版本） | R2/S3/B2 共用 S3 接口，差异逐家声明（P3） |
| 调度 | SQLite 持久任务状态机 + robfig/cron 仅算时间 | cron 不是任务系统（P4 落地） |

## 明确不引入

Redis / 独立 Postgres / 消息队列 / 微服务 / Next.js 运行时 / K8s 前提 / LangChain。

## 修订记录

- 2026-10-03 初版。
