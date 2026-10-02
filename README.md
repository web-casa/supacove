# supabackup

> 为 Supabase / Neon / Railway 数据库建立**独立于数据库平台的逻辑备份**。自部署，备份存入你自己的对象存储（R2 / S3 / B2），不依赖备份脚本。

**状态：Phase 1 开发中**（工程底座：骨架、认证、密钥协议、关键 spike）。产品与开发方案见 [docs/dev-plan.md](docs/dev-plan.md)。

## 定位

- 纯开源、自部署 Web 应用，单容器交付；PostgreSQL 逻辑备份（官方 pg_dump）。
- 备份文件使用 age 加密，凭备份文件 + 离线保存的私钥即可在**没有本工具**的情况下恢复。
- 与通用备份平台（Databasus、PG Back Web）的差异：对 Supabase / Neon / Railway 更友好的接入向导、对备份覆盖范围更诚实的说明（基于实际归档 TOC）、更强的恢复说明包，以及**不要求 Docker socket 的嵌入式恢复验证**（部署约束优势，非安全隔离承诺）。

## 开发

```bash
make dev          # 启动开发环境（应用 + PostgreSQL + MinIO）
make check        # 契约同步 + lint + 全部测试
```

## 许可证

[AGPL-3.0](LICENSE)。选择理由见 [docs/adr/ADR-000-license.md](docs/adr/ADR-000-license.md)。
