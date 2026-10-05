# 容量与性能报告（Phase 8）

> 测量口径如实声明：以下数字来自一次**固定测试机**上的真实测量，不是承诺。
> 你的硬件、网络、数据形态会得到不同结果。复现命令：`scripts/capacity-benchmark.sh`。

## 测试条件

| 项 | 值 |
|---|---|
| 测试机 | Apple Silicon 宿主机上的 **Linux arm64 虚拟机**（Docker Desktop VM 内；脚本依赖 /proc 与 Debian PG 布局，宿主机不可直接运行） |
| 数据库 | postgres:18-alpine 容器（限容器单核附近吞吐） |
| fixture | 2,000,000 行 × 360 字符**随机 hex 文本**（`encode(gen_random_bytes(180),'hex')`）。**两个不同的比不要混淆**：payload 自身的 zlib 压缩比实测 ≈0.57；而 archive/源库物理体积 ≈ 0.464（分母含表/索引/页开销，非同一口径） |
| 源库物理体积 | 873 MB（`pg_database_size`） |
| 客户端 | 宿主机 pg_dump 18.4，custom 格式（zlib） |
| 加密 | age X25519（与产品一致） |
| 测量方法边界 | 各阶段为独立 shell 步骤（非产品流式管道）；峰值 RSS 每 0.2s 采样 /proc VmHWM（短进程末段峰值可能被低估；postgres 服务端与子进程**不在**统计内）；计时含 ≤0.2s 轮询尾差 |

## 实测结果（2026-10-05）

| 阶段 | 耗时 | 产出 | 峰值 RSS（采样，见边界） |
|---|---|---|---|
| pg_dump（源库 873 MB） | 17.0 s | 405 MB archive（**archive 口径 ~24 MiB/s；源库口径 ~51 MB/s**） | 11.4 MB（pg_dump 客户端进程） |
| age 加密（对落盘 archive） | 0.83 s | 405 MB 密文 | 6.5 MB |
| initdb（模拟内嵌实例） | 0.41 s | — | 9.1 MB（initdb 进程） |
| pg_restore（模拟验证恢复） | 6.2 s | — | 5.7 MB（pg_restore 进程） |

**上表不是产品的端到端结果**：各阶段独立测量后求和 ≈ 24 s，只给出量级参考。
产品的真实管道是 pg_dump → age **流式**写出密文（不落盘 archive），验证在
一次性实例中运行，两端之间还有对象存储上传与读回校验（本次未测量——需要
真实桶凭据）。**

要点：

- **三体积指标关系**（随机 hex fixture，压缩比 ≈0.57）：源库 873 MB →
  archive 405 MB → 密文 405 MB（age 不压缩，尺寸≈archive）。
- **客户端进程内存极小**：pg_dump/age/pg_restore 采样峰值 RSS 均 < 12 MB。
  流式管道不整体载入内存，备份体积不决定进程内存；但 postgres 服务端
  （源库与验证实例）的内存**未计入**，规划时按数据库自身的
  shared_buffers/work_mem 另算。
- **磁盘需求**：本脚本落盘 archive + 密文 ≈ 2×archive。产品实际暂存只有
  密文（流式）；**验证**阶段另需明文临时文件 + 一次性实例数据目录
  （≈ 明文 dump + 数据展开，由验证磁盘预算把关，见 ADR-004 实现注释）。

## 推算边界（用于调度规划）

以本机数据为参照（线性外推，需自测验证；hex fixture 的压缩比≈0.57，随机
bytea 会更慢更接近 1:1）：

- **dump-only 理想外推上界**：备份窗口 × 源库吞吐（本测 ~51 MB/s）。
  例：4 小时窗口，hex fixture ≈ 700 GB 源库。这只是 dump 阶段；同一 worker
  还有远端上传等阶段，实际支撑总量低于此值。
- 恢复验证（异步，不占备份 worker 但共享 CPU/磁盘）按 pg_restore ~65 MB/s
  追平积压：405 MB archive ≈ 6 s。
- 多库稳定调度条件按利用率写：Σ(单库服务时长_i / 调度周期_i) ≪ 1（各库
  周期不同）；同窗多库则比较总服务时长与窗口。调度到期的去重与游标推进
  见 scheduler 实现；失败重试与恢复时间不计入此近似。

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

前提：**Linux** 宿主机（脚本依赖 /proc VmHWM、GNU date、/usr/lib/postgresql/18/bin
的 Debian 布局——macOS 不能直接运行），加 docker、pg_dump 18、age、age-keygen。
脚本输出各阶段毫秒数与 VmHWM 峰值内存，可直接与本文对照。
