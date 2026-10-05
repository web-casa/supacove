# 容量与性能报告（Phase 8）

> 测量口径如实声明：以下数字来自一次**固定测试机**上的真实测量，不是承诺。
> 你的硬件、网络、数据形态会得到不同结果。复现命令：`scripts/capacity-benchmark.sh`。

## 测试条件

| 项 | 值 |
|---|---|
| 测试机 | Apple Silicon (arm64) 宿主机，Docker Desktop VM |
| 数据库 | postgres:18-alpine 容器（限容器单核附近吞吐） |
| fixture | 2,000,000 行 × 180 字节**真随机** payload（`gen_random_bytes`，hex 编码）——**不可压** fixture，即 zlib 最坏情况 |
| 源库物理体积 | 873 MB（`pg_database_size`） |
| 客户端 | 宿主机 pg_dump 18.4，custom 格式（zlib） |
| 加密 | age X25519（与产品一致） |

## 实测结果（2026-10-05）

| 阶段 | 耗时 | 吞吐 | 峰值 RSS |
|---|---|---|---|
| pg_dump → 405 MB archive | 17.0 s | ~24 MB/s（对源库 873 MB） | 11.4 MB |
| age 加密 → 405 MB 密文 | 0.83 s | ~490 MB/s | 6.5 MB |
| initdb（内嵌实例） | 0.41 s | — | 9.1 MB |
| pg_restore（内嵌实例） | 6.2 s | ~65 MB/s（对 archive） | 5.7 MB |
| **端到端（备份→可恢复验证）** | **~24 s** | — | 峰值 < 12 MB |

要点：

- **三体积指标关系**（不可压数据）：源库 873 MB → archive 405 MB（46%，zlib 对随机字节几乎无能为力，仅剩行头/页开销压缩）→ 密文 405 MB（age 流式加密不做压缩，尺寸≈archive）。
- **内存占用极小**：所有阶段峰值 RSS < 12 MB。流式管道（pg_dump → age → 磁盘）不整体载入内存，**备份数据库的体积不决定进程内存**。
- **磁盘需求**：备份窗口内暂存 = archive + 密文 ≈ 2×archive 大小（协议 A 的原子提交链，提交后可配保留策略清理）。上例约 810 MB。
- age 加密吞吐远高于 dump 吞吐——加密**不是**瓶颈；瓶颈在 pg_dump 的服务端读取与压缩。

## 推算边界（用于调度规划）

以本机数据为参照（线性外推，需自测验证）：

- 每晚备份窗口内可支撑的总量 ≈ 窗口时长 × ~24 MB/s（dump 阶段主导）。
  例：4 小时窗口 ≈ 340 GB 源库（不可压）；可压数据更快。
- 恢复验证（异步，不占备份窗口）按 ~65 MB/s 追平积压：1 GB archive ≈ 15 s。
- 调度周期必须 ≥ 单库最慢备份时长 × 并发队列深度；多库排队会推迟 RPO（见下）。

## RPO 语义（如实声明）

supabackup 的 RPO（恢复点目标）**不是**一个固定数字，它由以下链条决定，任何一环故障都会拉长它：

```
RPO 实际上限 ≈ 调度间隔 + 排队等待 + dump 执行时长 + 远端提交时长
```

- **调度间隔**：cron 配置决定上限主体。每小时一次 → RPO 最坏 ≈ 1h + 执行时长。
- **排队**：全局备份并发为 1（v1 设计），多库同时到期时按顺序执行，后面的库等待。
- **新鲜度门控**：`max_age_hours` 超期会显式告警（overview/metrics/通知），不会静默漏备。
- **远端提交**：协议 C 完成前，本地密文已可恢复（artifact 先行提交）；远端只是异地副本。
- **不做 PITR**：逻辑备份不提供任意时间点恢复；WAL 归档不在范围内（见 `docs/disaster-recovery.md`）。

## 复现

```bash
ROWS=2000000 PAYLOAD=180 PATH="$PATH:/path/to/docker-shim" \
  ./scripts/capacity-benchmark.sh
```

前提：宿主机有 docker、pg_dump 18、age、age-keygen、/usr/lib/postgresql/18/bin（initdb/pg_ctl/pg_restore）。
脚本输出各阶段毫秒数与 VmHWM 峰值内存，可直接与本文对照。
