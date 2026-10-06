# 质量加固批次评审 2

日期：2026-10-07。基线：`docs/reviews/quality-batch-review-1.md`（2 P1 + 7 P2）。评审范围：`git diff b39d4ec..fa00114`，工作区起始干净。

**结论：NEEDS_FIXES。Q1–Q9 中 7 项 FIXED、2 项 PARTIALLY；两个 P1 均关闭。** 剩余为 Q5 的真实 multipart 清理证明及新增分页缺陷、Q9 的豁免理由和调用点说明不完整。没有将环境阻断或 SKIP 当成通过。

采用 code-reviewer 技能。仅新增本报告；生成、变异和补充测试位于 `/tmp/quality-review-2`，未修改仓库业务代码。实际祖先修复提交为 `e97f968`、`d69af0d`、`fa00114`；`86b8070` 对象存在，但 `git merge-base --is-ancestor 86b8070 HEAD` 返回 1，不重复计入修复链。

## 逐项关闭判定

| 项目 | 判定 | 证据和验收边界 |
|---|---|---|
| Q1 · P1 · schema 漂移 | **FIXED** | frontend 已安装版本和 package-lock 均为 openapi-typescript **7.13.0**。在 frontend 执行该本地生成器，输出 `/tmp/q2-schema.d.ts`；与提交版 `cmp` 返回 **0**。`.prettierignore:1` 精确覆盖 `src/api/schema.d.ts`。HEAD 导出副本建立生成文件基线后运行 `make api-check`，后端和前端生成无 diff，退出 **0**。 |
| Q2 · P1 · unlink 失败丢引用 | **FIXED** | `upload.go:408–439` 只在两个文件均删除成功或不存在时清引用；任一失败保留整组引用。`TestPruneKeepsReferenceWhenUnlinkFails` 普通及 race 均 PASS，**没有 SKIP**；本轮 UID=501，父目录确实 chmod 0500，首次 sweep 保留 path/state 和密文，恢复 0700 后二次 sweep 删除密文并清 path。单文件已删除、另一个失败的情形也能通过保留引用和 IsNotExist 分支重试。 |
| Q3 · P2 · 无 manifest 工件遗漏 | **FIXED** | SQL 已选取 `committed` 和 `committed_no_manifest`；`TestPruneReclaimsCommittedNoManifest` 实际 PASS（含 race），过期 failed 密文被删除。测试未独立断言该场景的所有 DB 字段，但生产代码共用清引用路径。 |
| Q4 · P2 · Makefile 门禁被覆盖 | **FIXED** | `make -n lint` 无 overriding/ignoring 警告，实际配方包含 gofmt、vet、golangci、eslint、stylelint；`check` 依赖包含 api-breaking，配方包含 `npx vitest run`；所有显式动作目标均在 `.PHONY`。注意 `check` 的“Everything CI runs”注释仍不准确：没有 e2e、安全扫描等，metrics-check 也仍是需运行实例的独立目标；本项按本轮指定门禁关闭，不声称本地 check 等同完整 CI。 |
| Q5 · P2 · multipart 故障证明 | **PARTIALLY** | fake 确实留下 `parts[key]`，测试断言非零，完成上传后消费为零；定向 race PASS。真实 S3 增加两条 best-effort 清理路径并脱离取消 context，有 deadline；但无 MinIO 残留 upload 检查，也无仓库内对应 storage 故障测试。新增 AbortIncomplete 分页存在确定遗漏，见下文。 |
| Q6 · P2 · resume 测试未进入路径 | **FIXED** | `ctxgone_test.go:302` 由首次 Put 钩子取消，`:319` 强制 putCount>0；普通和 race PASS。副本在 ResumeRemotePhase 首行插入 return 后测试 **FAIL**，报 `the upload never started: the resume repair path was not exercised`，是断言失败而非编译失败。 |
| Q7 · P2 · 篡改测试接受任意失败 | **FIXED** | 新断言同时要求 `*exec.ExitError`、退出 **1**、包含 **SHA-256**，并查询独立新目标库 public 表数为 0。`recovery/kit.go:160–163` 的 mismatch 确实 exit 1，早于临时明文、age 和 pg_restore。补充真实模板 shell/工具 spy 探针 PASS：exit=1，输出 `ciphertext SHA-256 mismatch`，age/pg_restore/psql 均未调用。原 PostgreSQL E2E 因 Docker 不可用 **SKIP**，不声称真实恢复已验收。 |
| Q8 · P2 · metrics fixture/parser/管道 | **FIXED** | fixture 新增 succeeded job，使用 io.ReadAll。无 TCP 的 handleMetrics 探针确认特殊 database 标签实际输出；副本仅替换 HTTP 测试载体为 Recorder 后，原格式断言 PASS。splitLabelPairs 对合法逗号、转义引号、反斜杠与引号组合均 PASS。用返回成功的 promtool spy 配合真实 curl 连接失败，make 返回 **2**（配方 curl 为 7），spy 未调用。完整 HTTP 版受监听限制，见验证记录。 |
| Q9 · P2 · 排除范围和理由 | **PARTIALLY** | v2.14.0 lint 为 **0 issues**；权限全局豁免已去除，两处生产目录 chmod 有局部理由；移除测试权限豁免后共 **22 条，全为测试，生产 0 条**。但 noctx 理由仍不实，G702 被加入无说明的测试豁免，os.Remove 的“全部 20 处有理由”与实际不符，详见下文。 |

## 未关闭项与新发现

### Q5 / R2-1 · P2 · 新增 AbortIncomplete 漏传分页 upload ID，跳过同 key 的后续 sessions

位置：`backend/internal/storage/s3.go:148–176`，尤其 `:153`、`:176`。

当前只将 `NextKeyMarker` 传到下一页，不保存或传入 `NextUploadIdMarker`。本地锁定的 AWS SDK `service/s3@v1.114.0/api_op_ListMultipartUploads.go:205–215` 明确说明：一般桶只提供 key-marker 时，只返回 key 严格大于 marker 的 uploads；若要继续相同 key，必须同时提供 upload-id-marker。这里本来就是按精确 key 回收，并设置 MaxUploads=100，遗漏直接影响该功能。

**实测：** `/tmp/quality-review-2/backend/internal/storage/q2_probe_test.go` 的 `TestReviewAbortPagination` 使用真实 SDK 序列化和受控 HTTP transport，模拟同 key 的 101 个 sessions。第一页返回 100 个和双 marker；第二次实际请求为：

```text
key-marker=key&max-uploads=100&prefix=key&uploads=
unreclaimed=map[101:true] lists=2
```

断言失败，证明第 101 个被跳过。此为 API transport 模型验证，不冒充 MinIO 实测。建议同时传递两个 marker，补入跨页同 key 的回归测试。

其余 Q5 核实结果：

- `Put` 从 MultiUploadFailure 取得真实 upload ID 再 abort；无 ID 时调用 AbortIncomplete。两函数均用 `context.WithoutCancel(context.Background())` + 15/30 秒超时，失败仅记录 warning，不覆盖原上传错误。WithoutCancel(Background) 本身冗余，但上下文确实独立；原请求的 context values 也不会保留。
- `TestReviewDetachedAbort` 通过受控 SDK transport 核实：传入已取消父 context，list 和直接 abort 请求 context 仍有效且有 deadline。新增逻辑没有导入缺失或编译问题，context/errors/time 原本已有。
- fake 的 parts 以 key 计数，不以 upload ID 区分。`delete(f.parts,key)` 证明其模型中完成会消费 parts，**不能证明真实重新创建 session 会消费旧 session 的 parts**。fake 的 AbortMultipart/abortCount 也未被此测试调用。丢响应段仍直接调用 uploadAndCommitRemote，未走 ResumeRemotePhase，也未覆盖 manifest 响应丢失或版本桶版本数。
- `storage/minio_test.go` 仍只有成功 multipart 上传/读取，没有故障后 ListMultipartUploads/残留 upload 断言；本轮 MinIO 测试本身 SKIP。需补入中断后的残留检查，不能据 fake 宣称真实“不残留”。
- abort warning 的“bucket lifecycle will reclaim”是未经配置验证的承诺：用户桶不一定配置了该生命周期规则，宜改为条件性说明。

### Q9 · P2 · 范围已收窄，但说明与实际调用仍不完全一致

位置：`.golangci.yml:27–30`、`:41–53`、`:89–94`；`backend/internal/jobs/durable.go:18`、`backend/internal/jobs/verify.go:365`。

已完成的修复：G301/G302/G306 不再全局禁用；生产 `db.go:73` 和 `dumper.go:346` 的 0700 目录 chmod 使用局部 gosec 注释；测试权限豁免命中的是宽权限夹具、可执行 shell stub 和目录 chmod，本轮实跑 22 条均在测试，范围合理。G124 理由已补入刻意非 HttpOnly 的 double-submit CSRF cookie。os.Remove 已移出 errcheck 全局排除。

剩余事项：

1. noctx 说明仍声称每个 outbound HTTP client 都有显式 Timeout 和 netguard。`storage.New` 的 SDK client 并非如此；上层 job context 和本次清理 deadline 不等于 HTTP client Timeout/netguard。首轮已经指出，当前文本未修正。
2. 测试路径规则 `text: "G30[126]|G702"` 把 G702 命令注入检查混进“测试文件权限”理由。生产确已重新启用 G702，但不能称全树已启用，也没有解释当前为何需要测试豁免。
3. 精确检索 `_ = os.Remove` 得 **19 处**（不是提交说明的 20 处）。其中 durable.go:18 和 verify.go:365 的 defer 没有调用点理由；其余 **17 处**有行内注释。大多数复用同一句 `cleanup on a path whose outcome cannot change the result`，不足以解释每种取舍。例如 db/backup.go:35 是 VACUUM 前删除旧临时文件，删除失败可导致后续 VACUUM 失败；verify_lifecycle_test.go:471 是主动构造 manifest 缺失的测试前置条件，并非清理。应写明真正的 best-effort 边界或检查错误，避免将“忽略了错误”表述为“不会影响结果”。
4. exclude-functions 的统一说明仍将 fmt.Sscanf 归为已经报告失败的 cleanup/output；该函数是解析，不符合这个理由。os.RemoveAll 仍全局排除。本轮未据此认定新增安全漏洞，但尚未达到“排除理由与真实调用一致”的关闭条件。

## 新回归与一致性检查

- **fake 钩子：** 仅 Q6 设置 onFirstPut；默认 nil。Put 在 mutex 下取出并清空 hook，解锁调用，再加锁计数，避免 hook 在锁内取消带来的耦合。其他六个 phase3 测试逐项 race PASS，未发现钩子影响原测试。完整 jobs race 因 httptest 监听被禁止而中断，不能据此声称全包通过。
- **S3：** build/vet/lint 通过，无导入回归。确证新增分页缺陷 R2-1；脱离取消的清理可能额外耗时至自身 deadline，是 best-effort 的实际边界。
- **新增 E2E CI job：** `fa00114` 已加入 e2e，依赖 backend/contract/frontend，image 的 needs 也加入 e2e；actionlint 通过。流程会安装 Chromium 并执行 make e2e，脚本构建嵌入前端的二进制再启动测试。首个 npx playwright install 发生在 npm ci 之前，无法保证该次解析使用锁文件版本；随后 make e2e 会 npm ci 并再次安装锁定版本，故不据此断言必红，但建议先装依赖再装浏览器。当前环境无法监听 TCP，未运行 Playwright，未声称 CI 已绿。
- **CHANGELOG：** Added 的工具链、故障矩阵、独立恢复条目与仓库功能相符，新增 e2e job 补上了调用链；它不是本轮集成执行成功的证据。“pinned tool versions”不等于 Actions、浏览器安装入口和镜像全部不可变。`SB_JOB_TIMEOUT` 在两个 Added 段重复，属文档整理问题。提交说明“空目标库证明没 decrypt/restore”过强：空库只直接证明未写入 public 表；本轮额外 spy 才证明 hash 拒绝时工具未调用。Q9 的“20 sites”数量及“with reasons”也不准确。
- **Q8 的关闭范围：** 修复了本轮指定的特殊 fixture、完整读取、合法逗号/转义引号和 curl 失败传播。该自制 parser 仍不是完整 Prometheus parser；例如 sampleRe 用 `[^}]*` 不支持合法标签值中的 `}`，且没有独立断言特殊样本必须存在。后续宜替换为正式 parser 或加强测试；不把有限语法门禁称为全语法验证。

## 验证记录

环境：UID=501；Go 使用 `GOCACHE=/tmp/final-cache`，golangci 使用 `GOLANGCI_LINT_CACHE=/tmp/quality-lint-cache`；前端 Node 22.22.2。本报告所称 PASS 均区分实际执行、静态审阅和副本探针。

| 验证 | 结果 |
|---|---|
| `go build ./backend/...` + `go vet ./...` | PASS；build 有只读 module stat cache warning，退出仍为 0 |
| golangci-lint v2.14.0 `run ./...` | PASS，0 issues；只有 generated api exclusion 未命中 warning |
| 去除测试 G301/G302/G306 豁免的 lint 配置 | 22 issues，全部 `_test.go`，生产 0；保留两处有理由的目录 chmod nolint |
| Q2/Q3/Q6/multipart 原仓库定向普通测试 | 4 项 PASS，无 SKIP；同次独立恢复 E2E SKIP |
| jobs 定向 `-race -v -count=1` | 11 PASS；另外 `TestConcurrentEnqueueExactlyOne` 因 Docker SKIP |
| 其他 fake phase3 六项 `-race -v -count=1` | 全部 PASS：destination、protocol C、hash mismatch、upload failure、retention、reconcile |
| 完整 jobs `-race -count=1 -timeout=10m` | 环境阻断：TestHeartbeatFreshnessGate httptest 监听 `socket: operation not permitted`；不是全绿 |
| Q6 首行 return 变异（副本） | KILLED，putCount 路径断言失败 |
| 恢复 hash 原测试 + 额外真实模板/spy 探针 | PASS；明确 exit 1、SHA-256 mismatch，age/pg_restore/psql 调用均为零 |
| PostgreSQL standalone restore / MinIO E2E | SKIP，docker run 失败；真实数据恢复及真实残留 upload 未验收 |
| TestMetricsExpositionFormat 原版 | 环境阻断：httptest 监听被禁止 |
| metrics Recorder 替代载体 + fixture/splitLabelPairs 探针 | PASS；原格式断言可通过，特殊 database 标签确实出现，三组合法复杂标签可正确拆分 |
| curl 失败 / promtool spy | PASS：curl 7、make 2、promtool 未调用；未把 spy 当作真实 promtool 解析 |
| S3 detached cleanup transport 探针 | PASS，请求 context 有效且有 deadline |
| S3 同 key 101 uploads 分页探针 | FAIL，剩 1 个 session，R2-1 |
| frontend openapi-typescript 7.13.0 生成 + cmp | PASS，cmp 0 |
| HEAD 副本 `make api-check` | PASS，后端/前端均无生成漂移；工作区业务文件未被生成器改写 |
| `.prettierignore` 覆盖 | 静态确认精确路径；`npx prettier --file-info` 因本机未安装 prettier、registry DNS EAI_AGAIN 未能执行，未影响生成/cmp 结果 |
| `make -n lint` / `make -n check` | PASS，无重复 recipe 警告，指定 lint/vitest/api-breaking 均在执行计划中 |
| npm lint / stylelint / test | PASS，Vitest 4 文件 28 测试 |
| actionlint / `git diff --check b39d4ec..HEAD` | PASS |
| Playwright / 完整 CI / 真实 promtool / oasdiff | 本轮未实跑；未计作已通过 |

下一轮关闭所需：修正 S3 双 marker 分页并补入 storage 故障/残留 uploads 检查；修正 Q9 排除理由、G702 测试豁免和无说明的 Remove 调用点。在具备 Docker/TCP 的环境补跑实际 PostgreSQL、MinIO 和新增 Playwright CI。
