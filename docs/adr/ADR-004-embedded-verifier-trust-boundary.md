# ADR-004：嵌入式恢复验证的信任边界与运行形态

日期：2026-10-03 · 状态：已接受（基于 Spike 2 实测）

## 背景

dev-plan P6 的差异化能力：自动恢复验证不要求挂载 Docker socket、不要求特权。评审 P0-05 指出：非 root + 无 socket 是部署约束，不是安全隔离；恢复不可信 dump 时，临时 PG 与应用同 UID 即可触达应用凭据。本 ADR 记录实测结论与信任边界。

## 实测一：可行性（2026-10-03，宿主 OrbStack Linux arm64，Docker 29）

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

## 实测二：发布形态复测（2026-10-03，评审 P1-19 要求）

环境：本仓库 Dockerfile 的 `runtime-spike` target（Debian bookworm-slim + PGDG postgresql-18 服务端与客户端，UID 10001，无 socket、无特权），脚本 `scripts/spike2-embedded-pg.sh`。

| 检查 | arm64（原生） | amd64（QEMU 模拟） |
|---|---|---|
| `initdb` + `pg_ctl`（仅 Unix socket） | ✅ | ✅ |
| `pg_dump -Fc` + `pg_restore --exit-on-error` + 行数核对（10 万行） | ✅ | ✅（1 万行探针） |
| 截断归档被 `--exit-on-error` 拒绝 | ✅ | ✅ |
| 停止 + 全部残留清理 | ✅（4 秒全链路） | ✅ |
| root 0600 canary 对 UID 10001 不可读 | ✅（setpriv 实证） | 同内核保证 |
| 同 UID（应用自身 secret）可达性 | **可达**（即信任边界本身，见下） | 同左 |

## 决策

1. **运行形态**：验证器在应用容器内 `initdb` 临时实例；仅监听独占目录下的 Unix socket（禁 TCP）；实例配置关闭 fsync/synchronous_commit（临时、可丢弃、生命周期秒级）；用后 `pg_ctl stop` + 目录删除，启动与退出时回收残留。
2. **信任边界（v1 明确不支持不可信 dump）**：
   - 自动验证仅对**管理员信任的来源**启用，且须显式开启；UI 与文档明示"验证进程与本应用同 UID（10001），可读应用可读的一切文件（含 SQLite、secret.key、暂存密文）；root 属主 0600 文件不可读（实测），但这不构成对恶意 dump 的隔离"。
   - 实测证据：截断归档在验证中被拒绝；root 0600 canary 不可被 UID 10001 读取。
   - 验证环境变量白名单化；不注入平台/桶凭据到验证实例。
   - 禁止加载用户指定的任意扩展（仅 profile 声明的已测试扩展）。
   - 需要不可信来源隔离时，采用无共享 secrets/数据卷的独立受限验证环境（未来能力，v1 不做），不以 nice 冒充隔离。
3. **资源治理**按 dev-plan P6 执行（并发 1、有界队列、按解压后体积预算磁盘、wall-time 超时）。

## 后果

- 镜像需包含 PG 服务端二进制（体积 +~50MB）。发布镜像 P1 仅含客户端；`runtime-spike` target（含服务端）用于本 ADR 的实测，P6 引入正式版时直接复用该 target 的软件清单。
- 不可信来源的验证需要独立受限环境（无共享 secrets/数据卷），v1 不承诺。
- CI 中验证容器不得注入真实凭据（dev-plan P6 DoD）。
- "恢复已验证"状态措辞固定为"在 profile X 上按选项 Y 恢复成功"，不等于平台完整恢复演练。

## 参考

- 实测脚本：`scripts/spike2-embedded-pg.sh`（可重复执行）
- dev-plan §0.5 / P6、评审 P0-05、[pg_dump 官方安全警告](https://www.postgresql.org/docs/current/app-pgdump.html)
