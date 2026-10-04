# supabackup 部署指南

## 前提条件

- Docker 和 Docker Compose（推荐）
- 或者：Go 1.24+ 编译 + PostgreSQL 客户端工具 14–18 + age 加密工具

## Docker Compose（推荐）

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
| `SB_HEARTBEAT_URL` | （空） | 死人开关外部心跳 URL |
| `SB_PUBLIC_ORIGIN` | （空） | 反代部署时的外部 origin |
| `SB_TRUSTED_PROXIES` | （空） | 信任的代理 CIDR 列表 |
| `SB_SECRET_FILE` | `<data>/secret.key` | 主密钥文件路径 |

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

```bash
# 1. 解密
age --decrypt -i age-identity.txt -o restored.dump backup.dump.age

# 2. 恢复到全新数据库
pg_restore --exit-on-error --no-owner -d "postgresql://user:pass@host/target_db" restored.dump

# 3. 验证
psql -d "postgresql://user:pass@host/target_db" -c "SELECT count(*) FROM pg_tables"
```

## 安全注意事项

- **age 私钥**：丢失 = 所有历史备份永久不可读。必须离线保存。
- **主密钥文件** (`secret.key`)：丢失 = 已存凭据不可读（需重新录入），但不影响备份可恢复性。
- **管理界面**：生产部署必须走 HTTPS（设置 `SB_PUBLIC_ORIGIN` + 反代 TLS 终端）。
- **/metrics**：默认需要认证。如果暴露给监控系统，配置反代 IP 白名单。
- **多人使用**：当前为单管理员设计。API Token 可用于自动化但不支持多人 RBAC。
