# 质量加固批次评审 4（定向确认）

日期：2026-10-07。基线：[第三轮报告](quality-batch-review-3.md)。实际范围：`git diff df68bcd..467bfee`，HEAD：`467bfeeab1f85df43f3f6da82c7d1856ceaac77d`。逐项读取 HEAD；使用 code-reviewer 技能。起始工作区干净，仅新增本报告；探针、变异及日志在 `/tmp/quality-review-4`，通过 Go overlay 加载，没有修改仓库业务代码或测试。

**结论：NEEDS_FIXES。** R2-1 实现及回归已补齐；本轮未关闭的是 Q9 理由准确性及 CHANGELOG 重复条目。监听限制和已登记的 MinIO 验证缺口不作为拒绝通过的原因。

## 简短判定表

| 第三轮遗留 | 判定 | HEAD 证据 |
|---|---|---|
| 1. R2-1 分页、回归、abort 警告 | **FIXED（仓库 HTTP 测试执行受限）** | `storage/s3.go:156` 起发送、更新双 marker；`s3_pagination_test.go:22` 起有 httptest 同 key 101 sessions 两页桩及 101 个 abort、u101 断言。无监听 SDK 探针 HEAD PASS，变异 FAIL。两处警告均改为条件性 lifecycle 说明，不再保证桶会回收。原 `s3.go:116` 附近的 lifecycle 注释仍可同步润色，但文件顶部已明确无规则时可能需人工清理。 |
| 2. fake parts 模型边界 | **FIXED** | `jobs/phase3_test.go:35` 明确 PER KEY、非 upload ID，只证明 MODEL，并指明真实 MinIO 残留检查未完成。原方法内简写应结合该模型声明阅读；不视为真实 session 清理证明。定向 fake 测试 PASS。 |
| 3. MinIO 残留 uploads 断言 | **仍登记遗留，不阻断** | `storage/minio_test.go` 仍无故障后 ListMultipartUploads 残留断言；第三轮报告及上述新注释均如实登记。未声称真实桶已验收。 |
| 4. metrics 特殊样本必现、parser | **FIXED（HTTP 测试执行受限）** | `server_metrics_format_test.go:201–210` 要求 last-success 样本包含转义引号；当前 fixture 中特殊 database 是该成功样本来源，消除了此前只检查任意样本的空断言。`:58` 起 parseSample 不再用 `[^}]*`，六组特殊标签探针 PASS。仍是有限 parser，不等同正式 Prometheus parser；本轮不新增替换要求。 |
| 5. Q9 四处理由 | **PARTIALLY，未关闭** | noctx、G702 已符合本轮范围；os.Remove 逐点说明及 exclude-functions 仍有不实说明，见下。常规 lint 的 0 issues 不证明被排除理由正确。 |
| 6. CI 安装顺序 | **FIXED** | `.github/workflows/ci.yml:145–150` 的 e2e job 在同一 frontend 工作目录先 npm ci，再 playwright install；actionlint PASS。 |
| 7. CHANGELOG 重复项 | **NOT FIXED** | `CHANGELOG.md:20` 与 `:46` 仍重复登记 SB_JOB_TIMEOUT；同一 Unreleased 中 `:7`、`:45` 仍各有一个 Added。该文件在指定 diff 内无变更。 |
| 8. Makefile check 注释 | **FIXED** | `Makefile:82` 已限定为本地静态检查、契约及单元测试门禁，并说明 CI 另跑 e2e、image smoke 和安全扫描。 |

## Q9 尚需关闭的说明

- **noctx / G702：通过。** noctx 继续区分 webhook/heartbeat Timeout 与 storage 的上层 deadline；不把约 200 处估算当验证证据。临时解除 G702 测试排除后，唯一命中仍为 `db/db_test.go:70` 自执行测试二进制，现注释准确，生产无命中。
- **os.Remove：部分修正。** `jobs/verify_lifecycle_test.go:471` 已承认测试前置条件，`db/backup.go:35` 已说明失败由后续操作暴露。但 `verifier/verifier_test.go:139,416` 仍把测试准备/后续断言前置删除写成“不影响结果”；`verifier/verifier.go:239` 仍未说明保留工作目录时 best-effort 明文删除可能失败。`db/backup.go:40,44,48` 又复制了“earlier attempt / next VACUUM”说明，实际是在当前尝试失败后删除当前临时文件并立即返回；应描述保留原错误和可能残留的取舍。
- **exclude-functions：未关闭。** `.golangci.yml:58–61` 仍声称持久化文件写入不在 Fprintf 豁免内，而实际是函数级全局排除。`:62–67` 仍称正则限制 digit run、生产解析失败由正则暴露；`\d+` 不限制 int64 范围，40 位数字探针正则匹配但 Sscanf 返回 `value out of range`、扫描数 0、jobID 0。应改为真实取舍，或收窄排除，不能用正则作防溢出理由。

临时配置只解除 G702 测试规则和 Sscanf/Fprintf/RemoveAll 三个函数排除，实测 **25 issues：24 errcheck + 1 gosec**。命中仍为 Sscanf 3 处（jobs.go:194,229、m1_test.go:170）、metrics.go 的 Fprintf 16 处、verifier 的 RemoveAll 5 处，以及上述 G702 1 处，与第三轮一致。RemoveAll 当前命中范围说明可接受。

## 验证记录

| 实际执行 | 结果 |
|---|---|
| `go test -count=1 ./backend/...`（GOCACHE 指向 /tmp） | 退出 1。jobs、outbox、server、storage 在 httptest 监听时被沙箱拒绝，`socket: operation not permitted`；其余输出为通过或无测试。不能称全量通过。 |
| `TestMetricsExpositionFormat` 单独执行 | 同样监听受限，未完成 HTTP scrape 断言。 |
| 仓库分页回归的直接变异：仅删除 `UploadIdMarker: uploadMarker` | 退出 1，`uploadMarker declared and not used`。这是编译失败，不冒充断言变红。原版仓库回归因监听受限未能跑完。 |
| 无监听真实 SDK transport 探针（复核并复用第三轮模型） | HEAD：两页清完 101 sessions，独立 abort deadline 检查 PASS；变异版仅额外 `_ = uploadMarker` 保持可编译，第二页缺 upload-id-marker，`unreclaimed=map[101:true] lists=2`，断言 FAIL。此证据是协议模型，不是仓库 httptest 或 MinIO 实测通过。 |
| parseSample + splitLabelPairs + labelPairRe overlay 测试 | 6/6 PASS：左右花括号、反斜杠后跟花括号、引号、逗号、组合含空格。输入由 `%q` 生成合法转义；花括号本身不需转义，不把非法 `\}` 当标准转义。 |
| fake 中断恢复测试 | `TestUploadInterruptLeavesNoPartialAndResumesIdempotently` PASS。 |
| `golangci-lint run ./...`，v2.14.0 | 退出 0，**0 issues**；仅 generated API 排除未命中 warning。临时解除排除的审计运行退出 1 属预期。 |
| `npm run lint` / `npm run stylelint` / `npm test` | 均退出 0；Vitest **4 文件、28 测试通过**。stylelint/Vitest 使用本机 Node 22.22.2。 |
| `actionlint` | 退出 0，无输出。 |

下一轮仅需纠正上述 Q9 不实理由并合并 CHANGELOG 重复条目；在可监听环境补跑仓库分页及 metrics HTTP 测试。MinIO 残留断言继续作为已登记的独立验证遗留。
