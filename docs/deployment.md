# SupaCove 部署指南

## 前提条件

- Docker 和 Docker Compose（推荐）
- 或者：Go 1.26.6+ 编译（与 go.mod 一致）+ PostgreSQL 客户端工具 14–18 + age 加密工具

## Docker Compose

> 仓库自带的 `compose.yaml` 是**开发环境**脚手架（内置 `SB_INSECURE_COOKIE=1`、
> 固定开发口令和本地 PG/MinIO），只用于本地试用。生产部署请以镜像 +
> 下文的环境变量表自建 compose（cookie 需要 HTTPS 反代），不要原样上生产。

```bash
git clone https://github.com/your-org/supabackup.git
cd supabackup
docker compose up -d
```

应用启动后访问 http://localhost:8080。

首次启动需要初始化管理员账号：

```bash
# 在宿主机上生成一次性 token
docker compose exec app /app/supabackup bootstrap
# 输出一次性 token → 在 Web UI 中输入，创建管理员账号
```

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `SB_DATA_DIR` | `/app/data` | SQLite/暂存/密钥文件目录 |
| `SB_ADDR` | `:8080` | HTTP 监听地址 |
| `SB_INSECURE_COOKIE` | `false` | 仅限本地 HTTP 开发 |
| `SB_LOG_LEVEL` | `info` | debug/warn/error |
| `SB_LOCAL_KEEP` | `5` | 每库本地保留的 artifact 数量 |
| `SB_STAGING_QUOTA_BYTES` | `0`（不限） | 暂存目录硬配额（字节） |
| `SB_JOB_TIMEOUT` | `6h` | 单个备份任务的墙钟预算（dump+上传+回读）；超时任务按网络类失败落库。`0` 关闭预算 |
| `SB_FAILED_ARTIFACT_TTL_HOURS` | `72` | failed/canceled/interrupted 任务的本地工件保留时长（小时）；到期自动回收暂存空间。`0` 永久保留 |
| `SB_HEARTBEAT_URL` | （空） | **回退**心跳监控 URL（未单独配置心跳的库继承；共享回退端点意味着多库共同消除同一个 silence，不是每库独立监控）。单独配置某库心跳用 `PUT /api/databases/{id}/schedule`；显式禁用某库填 `"-"`。成功 ping 要求 `heartbeatPeriodHours > 0` 且快照年龄 ≤ period+grace；失败 ping 发送到 `URL/fail`。beta 无 start 信号（v1.0）。 |
| `SB_PUBLIC_ORIGIN` | （空） | 反代部署时的外部 origin |
| `SB_TRUSTED_PROXIES` | （空） | 信任的代理 CIDR 列表 |
| `SB_SECRET_FILE` | `<data>/secret.key` | 主密钥文件路径 |
| `SB_VERIFY_ENABLED` | `false` | 自动恢复验证开关（见下文"恢复验证"，须显式启用） |
| `SB_VERIFY_IDENTITY_FILE` | （空） | age 私钥文件路径（启用验证时必填，权限须为 0600） |
| `SB_VERIFY_PGBIN` | 自动探测 | 验证用 PostgreSQL 服务端二进制目录（如 `/usr/lib/postgresql/18/bin`） |

## 恢复验证（可选，默认关闭）

每次备份成功后，SupaCove 可以把密文**解密并恢复进一个一次性的内嵌
PostgreSQL 实例**，比对 manifest 声明的表数量和扩展，从而证明"这份备份
真的能恢复"。结果以状态机形式呈现在任务 API 中：
`pending → running → verified / failed / unsupported`，无法执行时是带原因
的 `skipped`。

**这是管理员显式决定才启用的功能**（ADR-004）：

- 验证进程与 SupaCove 同 UID 运行，**不是沙箱**。恢复的是备份内容本身，
  它能读写应用可读的文件。只对您信任来源的备份启用。
- 启用必须提供 age 私钥（`SB_VERIFY_IDENTITY_FILE`）。私钥进入实例内存 =
  该实例可以解密所有备份。请权衡：验证带来"可恢复性证明"，代价是私钥
  不再完全离线。
- 需要宿主机安装 PostgreSQL **服务端**（镜像已内置 PG18；裸机部署需
  `postgresql-18` 包或用 `SB_VERIFY_PGBIN` 指向现有安装）。

启用方式（compose 示例）：

```yaml
services:
  app:
    environment:
      SB_VERIFY_ENABLED: "1"
      SB_VERIFY_IDENTITY_FILE: /run/secrets/age-identity
    secrets:
      - age-identity
```

启动日志会明确打印验证模式（`restore verification ENABLED` /
`restore verification disabled`）；验证失败不会影响备份本身的提交状态，
但会如实标记在任务上。

## 初始化流程

1. `docker compose up -d` → 应用启动，自动运行数据库迁移
2. `docker compose exec app /app/supabackup bootstrap` → 获取一次性 token
3. 在 Web UI 中输入 token + 管理员用户名/密码
4. `docker compose exec app /app/supabackup age init > age-identity.txt` → 生成 age 密钥对
   - **将 stdout 内容保存到离线安全位置**（私钥不再显示）
5. 在 Web UI 中注册数据库（需要连接串）
6. 在 Web UI 中注册存储目的地（需要 S3 兼容凭据）
7. 测试备份：点击"立即备份" → 等待 succeeded
8. 验证恢复：下载 artifact → `age --decrypt -i age-identity.txt` → `pg_restore`

## 恢复

### 前提

- 备份密文文件（从桶下载或本地暂存目录获取）
- age 私钥文件（离线保存的）
- `age` 和 `pg_restore` 工具

### 步骤

每个成功备份都会自动生成**恢复套件**（restore.sh），包含密文校验和、
密钥指纹与平台专属指引，可在 Web UI 的任务页下载（`GET /api/tasks/{id}/recovery-kit`）。
推荐直接使用它——它会校验密文哈希、拒绝非空目标、拒绝把密码放进命令行，
并在恢复后核对 manifest 声明的表数量：

```bash
AGE_IDENTITY_FILE=/secure/age-identity.txt PGPASSWORD='...' \
  sh restore.sh "postgresql://user@host:5432/newdb" backup.dump.age
```

手动等价流程：

```bash
# 1. 解密
age --decrypt -i age-identity.txt -o restored.dump backup.dump.age

# 2. 恢复到全新数据库
pg_restore --exit-on-error --no-owner -d "postgresql://user@host/target_db" restored.dump

# 3. 验证
psql -d "postgresql://user@host/target_db" -c "SELECT count(*) FROM pg_tables"
```

注意：连接串中**不要内嵌密码**（会暴露在 `ps` 输出里），用 `PGPASSWORD`
环境变量传递。恢复失败时目标库可能已被部分写入——从空库重试。

## 安全注意事项

- **age 私钥**：丢失 = 所有历史备份永久不可读。必须离线保存。
- **主密钥文件** (`secret.key`)：丢失 = 已存凭据不可读（需重新录入），但不影响备份可恢复性。
- **管理界面**：生产部署必须走 HTTPS（设置 `SB_PUBLIC_ORIGIN` + 反代 TLS 终端）。
- **/metrics**：默认需要认证。如果暴露给监控系统，配置反代 IP 白名单。
- **多人使用**：当前为单管理员设计。API Token 可用于自动化但不支持多人 RBAC。
