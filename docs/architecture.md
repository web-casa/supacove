# SupaCove 架构

本文用图解释 SupaCove 的组成与关键流程：组件总览、一次备份的完整流水线、
两条恢复路径、通知链路。部署拓扑与升级状态机见
[deployment.md](deployment.md)（含图），灾难场景操作见
[disaster-recovery.md](disaster-recovery.md)。

## 1. 组件总览

SupaCove 是**单二进制**产品：Web 控制台（SPA）、HTTP API、SQLite 控制面、
调度器与备份执行器全部内嵌；对象存储与被备份的数据库都在你自己手里。

```mermaid
flowchart LR
    subgraph browser["管理员浏览器"]
        SPA["Web 控制台（内嵌 SPA）"]
    end
    subgraph binary["supacove 单二进制 / 单容器（非 root, uid 10001）"]
        API["HTTP API + 会话鉴权<br/>:8080"]
        SCHED["调度器<br/>每库 cron + 时区 + 超期判定"]
        RUNNER["备份执行器"]
        OUTBOX["通知 outbox<br/>事务性 + 有界重试"]
        VERIFIER["恢复验证器（默认关闭）<br/>内嵌一次性 PostgreSQL 18"]
        METRICS["/metrics<br/>Prometheus 格式（需鉴权）"]
    end
    subgraph data["数据目录 /app/data（卷）"]
        DB[("supacove.db<br/>SQLite 控制面（WAL）")]
        SECRET[("secret.key<br/>32 字节主密钥")]
        STAGING[("staging/<br/>本地暂存 + 保留策略")]
    end
    SRC[("源数据库<br/>Supabase / Neon / Railway /<br/>任意 PostgreSQL")]
    BUCKET[("你自己的对象存储<br/>R2 / S3 / B2 / MinIO")]
    HOOK["Webhook 接收方"]
    HB["心跳监控"]
    PROM["Prometheus 抓取"]

    SPA --> API
    API --> DB
    SCHED --> RUNNER
    RUNNER --> SRC
    RUNNER --> SECRET
    RUNNER --> STAGING
    RUNNER --> BUCKET
    RUNNER --> DB
    VERIFIER --> STAGING
    VERIFIER --> DB
    OUTBOX --> HOOK
    RUNNER --> HB
    API --> OUTBOX
    METRICS --> PROM
```

要点：

- **控制面数据**（数据库注册、凭据、任务、工件路径、outbox）全部在
  `supacove.db`；凭据用 `secret.key`（AES-GCM）加密后落库，密钥与库同目录。
- **两种交付形态**：图中 PG 客户端矩阵与 PG 18 服务端（恢复验证器用）是
  **容器镜像**自带的；独立二进制不内嵌任何 PostgreSQL 工具——备份需要
  宿主机安装 `pg_dump` 客户端（14–18），要启用验证器（`SB_VERIFY_ENABLED=1`
  且提供 `SB_VERIFY_IDENTITY_FILE`）还需 PostgreSQL 服务端。
- **备份工件不在本工具里长期保存**：配置了目的地时，本地 `staging/` 只做
  有界暂存（`SB_LOCAL_KEEP` 份数 + `SB_STAGING_QUOTA_BYTES` 配额，默认 0
  即不限；保留锚点可能使实际份数超过 `SB_LOCAL_KEEP`），最终归宿是你自己
  的对象存储。也可以**不配置任何目的地**：此时跳过上传，任务以"本地成功"
  结束（心跳对该例外有明确语义）。
- 出站请求（webhook、心跳、对象存储、源库连接）统一经过 SSRF 拨号边界
  （netguard），链路本地与云元数据地址一律拒绝。
- `/metrics` 与控制台同样需要登录；指标为低基数标签。

## 2. 一次备份的流水线

```mermaid
flowchart TB
    CRON["调度器触发<br/>（每库 cron，时区感知）"] --> DUMP
    subgraph job["备份任务"]
        DUMP["pg_dump（custom 格式）<br/>客户端矩阵 14–18，自动匹配源库 major"] --> AGE
        AGE["流式 age 加密（接收者公钥）"] --> STAGE
        STAGE["写入本地暂存 staging/<br/>并生成 manifest（本地 JSON：表数等）"] --> UPLOAD
        UPLOAD["上传密文（分片 + 上传后读回哈希校验）"] --> PUB
        PUB["发布 manifest（远端提交标记）"] --> COMMIT
        COMMIT{"远端提交成功？"}
    end
    COMMIT -- 否 --> FAILPATH["任务标记失败（错误分类）<br/>outbox 通知 + 心跳 /fail"]
    COMMIT -- 是 --> RECORD
    COMMIT -- "未配置目的地：跳过上传" --> RECORD
    RECORD["控制面记录任务结果与三体积指标<br/>（源库物理体积 / dump 归档 / 密文；<br/>统计在成功后落 backup_stats）"]
    RECORD --> ANCHOR["保留锚点：最新与最后验证通过的备份<br/>不被保留策略删除"]
    RECORD --> DONE["任务标记成功，心跳 ping<br/>（绑定远端提交 + 快照年龄）"]
    DONE -.-> KIT["自动生成自包含恢复套件 restore.sh<br/>（本地保存，控制台可下载；不自动上传）"]
    DONE -. 可选 .-> VERIFY["恢复验证器（SB_VERIFY_ENABLED=1 + SB_VERIFY_IDENTITY_FILE）<br/>内嵌一次性 PG 真实恢复 + manifest 表数核对<br/>失败不影响备份可用性，仅如实标记"]
```

三体积指标让"源库多大 / 归档多大 / 密文多大"可以逐任务对比，是容量规划
（见 [capacity.md](capacity.md)）与异常发现（如压缩比突然塌陷）的基础。

## 3. 两条恢复路径

恢复永远是**你主动、显式**的操作；工具不做"一键恢复到生产库"。

```mermaid
flowchart TB
    subgraph pathA["路径 A：控制台下载（需要本工具在线）"]
        DL["控制台生成短期预签名下载 URL"] --> DECRYPT["age 解密（离线私钥）"]
        DECRYPT --> RESTOREA["pg_restore 到空目标库<br/>（自行选择版本与参数）"]
    end
    subgraph pathB["路径 B：恢复套件（不依赖本工具，推荐演练用）"]
        KITFILE["密文（对象存储或本地暂存）<br/>+ restore.sh（每个成功备份自动生成，控制台下载）"] --> PRECHECK["前置检查：工具齐备 / 密文哈希 / 目标为空库<br/>（拒绝非空目标，SUPACOVE_ALLOW_NONEMPTY=1 可覆盖）"]
        PRECHECK --> PROFILE{"恢复模式<br/>SUPACOVE_PROFILE（旧名回退）"}
        PROFILE -- generic --> GENERIC["按归档原样恢复"]
        PROFILE -- supabase --> SUPA["先查超级用户与扩展可安装性（写入前）<br/>再按编辑过的目录恢复，跳过已知的 Supabase 平台项"]
        GENERIC --> COUNT["结尾表数核对；失败即非零退出 + 部分写入警告"]
        SUPA --> COUNT
    end
```

路径 B 是灾难场景的主角：只要**密文 + 离线私钥 + 一台装了 age/pg_restore/
psql 的机器**就能恢复，SupaCove 本身（及其数据库、其对象存储凭据）都不需要。
`supabase` 模式的完整步骤见
[备份 Supabase 数据库指南](https://supacove.com/docs/guides/supabase-backup)。

## 4. 通知与心跳（两条独立链路）

**Webhook（事务性 outbox）**——事件类型为 `backup_failed`、`backup_expired`、
`verification_failed`；业务事务与"待通知"在同一 SQLite 事务里落库，进程崩溃
不丢通知；投递 at-least-once，接收方按稳定事件 ID 去重。

```mermaid
sequenceDiagram
    participant J as 备份任务
    participant DB as supacove.db
    participant O as outbox 投递器
    participant W as webhook 接收方
    J->>DB: 业务写入 + outbox 事件（同一事务）
    O->>DB: 轮询待投递（有界重试）
    O->>W: POST 事件载荷
    Note over O,W: 携带 X-Supacove-Event(-ID) 头；改名过渡期同时携带等值的旧 X-Supabackup-Event(-ID)
    W-->>O: 2xx/3xx
    O->>DB: 标记成功
    Note over O,W: 响应 ≥400 或网络失败 → 退避重试，超限标记 dead（supacove_outbox_dead 指标可见）
```

**心跳（dead-man switch）**——与 outbox 无关的独立异步 GET：成功路径 ping
心跳 URL（**每库配置优先**，`SB_HEARTBEAT_URL` 仅为全局回退；语义绑定远端
提交与快照年龄，未配置目的地的本地成功也算），失败路径立即 ping `…/fail`
（healthchecks.io 惯例）。心跳不进入持久化投递队列（成功仅更新控制面的
`last_heartbeat_at`），可靠性由对端缺省（未按时收到 ping 即告警）保证。

## 5. 相关文档

| 主题 | 文档 |
|---|---|
| 部署拓扑、环境变量、升级（含数据迁移状态机图） | [deployment.md](deployment.md) |
| 灾难场景操作（SQLite 损坏 / 主密钥丢失 / 仅剩桶+私钥 / 升级失败） | [disaster-recovery.md](disaster-recovery.md) |
| 容量实测与 RPO 语义 | [capacity.md](capacity.md) |
| 架构决策记录（许可证、恢复验证器等） | [adr/](adr/) |
