# ADR-004：嵌入式恢复验证的信任边界与运行形态

日期：2026-10-03 · 状态：已接受（基于 Spike 2 实测）

## 背景

dev-plan P6 的差异化能力：自动恢复验证不要求挂载 Docker socket、不要求特权。评审 P0-05 指出：非 root + 无 socket 是部署约束，不是安全隔离；恢复不可信 dump 时，临时 PG 与应用同 UID 即可触达应用凭据。本 ADR 记录实测结论与信任边界。

## Spike 2 实测（2026-10-03，宿主 OrbStack Linux arm64，Docker 29）

环境：`postgres:18-alpine`，`--user 70:70`（镜像内非 root postgres 用户），无 Docker socket、无特权、无额外 capability。

| 步骤 | 结果 |
|---|---|
| `initdb -D /tmp/pgdata -A trust -U verifier` | ✅（非 root 可执行） |
| `pg_ctl -o "-c listen_addresses= -c unix_socket_directories=/tmp/pgsock"` | ✅ 仅 Unix socket，无 TCP 监听 |
| `pg_dump -Fc`（10 万行 → 2.1MB 归档） | ✅ |
| `pg_restore --exit-on-error` 回灌 + 行数核对 | ✅ rows=100000 |
| `pg_ctl stop` + 目录清理 | ✅ |
| 全链路耗时 | **2 秒**（fsync=off 的临时实例配置） |
| 数据目录基线体积 | 63.8MB（含系统目录，衡量暂存预算时需计入） |

## 决策

1. **运行形态**：验证器在应用容器内 `initdb` 临时实例；仅监听独占目录下的 Unix socket（禁 TCP）；实例配置关闭 fsync/synchronous_commit（临时、可丢弃、生命周期秒级）；用后 `pg_ctl stop` + 目录删除，启动与退出时回收残留。
2. **信任边界（v1 明确不支持不可信 dump）**：
   - 自动验证仅对**管理员信任的来源**启用，且须显式开启；UI 与文档明示"验证进程与本应用同 UID，可读应用可读的文件；不隔离恶意 dump"。
   - 验证环境变量白名单化；不注入平台/桶凭据到验证实例。
   - 禁止加载用户指定的任意扩展（仅 profile 声明的已测试扩展）。
   - 需要不可信来源隔离时，采用无共享 secrets/数据卷的独立受限验证环境（未来能力，v1 不做），不以 nice 冒充隔离。
3. **资源治理**按 dev-plan P6 执行（并发 1、有界队列、按解压后体积预算磁盘、wall-time 超时）。

## 后果

- 镜像需包含 PG 服务端二进制（体积 +~50MB），P1 的镜像仅含客户端，服务端二进制在 P6 引入并重测本 ADR 场景。
- CI 中验证容器不得注入真实凭据（dev-plan P6 DoD）。
- "恢复已验证"状态措辞固定为"在 profile X 上按选项 Y 恢复成功"，不等于平台完整恢复演练。

## 参考

- 实测脚本：`scripts/spike2-embedded-pg.sh`（可重复执行）
- dev-plan §0.5 / P6、评审 P0-05、[pg_dump 官方安全警告](https://www.postgresql.org/docs/current/app-pgdump.html)
