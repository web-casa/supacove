# 质量加固批次评审 3

日期：2026-10-07。基线：[第二轮报告](quality-batch-review-2.md)。评审范围：`git diff fa00114..HEAD`，HEAD 为 `df68bcddcff5e2eb6fac3d4e467910509a13f627`。起始工作区干净；采用 code-reviewer 技能，只新增本报告，探针及导出副本位于 `/tmp/quality-review-3`。

**结论：NEEDS_FIXES。** R2-1 分页缺陷仍存在，SDK transport 探针再次复现同 key 的第 101 个上传未被回收。题述及提交说明中的双 marker、警告文案、metrics 必现断言、CI 安装顺序修复均未进入当前提交。此结论不是因 MinIO 缺少断言或环境阻断而拒绝通过。

## 逐项判定

| 项目 | 判定 | 证据与边界 |
|---|---|---|
| R2-1：双 marker 分页 | **NOT FIXED，P2 运行缺陷** | `storage/s3.go:148–176` 仍只有 `token`、`KeyMarker`、`NextKeyMarker`，没有 `UploadIdMarker`。锁定 SDK 注释及本轮 transport 探针均确认遗漏同 key 后续 sessions。 |
| abort 警告不承诺 lifecycle | **NOT FIXED，文档/日志准确性** | `storage/s3.go:137,169` 仍是 `multipart abort failed; bucket lifecycle will reclaim`；`:110–113` 注释也仍假定 lifecycle 会运行。桶未配置规则时不能作此承诺。 |
| Q5：fake 与真实清理边界 | **PARTIALLY，测试说明遗留** | fake 确实记录并断言 parts，但按 key 删除计数，不能证明不同 upload ID 的旧 session 已清理。代码注释仍有 `test can assert nothing leaked` 和完成会消费中断 parts 的表述；第二轮报告已经诚实写明边界。 |
| Q5：MinIO 残留 upload 断言 | **已声明遗留，未完成验证** | `storage/minio_test.go` 仍只有成功 multipart 流程，没有故障后 ListMultipartUploads 残留断言；仓库内第二轮报告“其余 Q5 核实结果”和“下一轮关闭所需”已明确登记。因此不是未登记文档债，不另列新增运行缺陷。 |
| Q8：特殊标签必须出现 | **NOT FIXED，测试覆盖遗留** | `server_metrics_format_test.go:58–73` 有特殊数据库和成功 job fixture，但 `:173–180` 仅要求任意样本存在及 TYPE 有 HELP，没有要求该数据库标签出现。 |
| Q8：花括号解析 / 正式 parser | **有限门禁，建议后续替换** | `:16` 仍使用 `[^}]*`，合法标签值中的 `}` 会被拒绝，转义反斜杠后跟 `}` 也一样。建议改为正式 Prometheus parser，再保留特殊样本必现断言；本轮不要求实现替换。 |
| Q9：noctx 理由 | **主要错误已修正** | `.golangci.yml:27–34` 已区分 webhook/heartbeat 的 Timeout、netguard 与 storage 的上层 context 预算。`jobs.go:404–406,719–721` 有 resume/job deadline；`s3.go:129,146` 有独立清理 deadline。不再声称 SDK HTTP client 也有 netguard/显式 Timeout。约 200 处改动的估算未重新证实，不把估算视为验收证据。 |
| Q9：G702 独立理由 | **PARTIALLY** | 已拆成独立测试规则，生产未豁免；但实际唯一命中是 `db/db_test.go:70` 的 `exec.Command(os.Args[0], …)`，并非注释所称测试写出的命令字面量、docker 或 TempDir argv。应说明测试自启动当前测试二进制的信任边界。 |
| Q9：os.Remove / `_ =` 逐点理由 | **PARTIALLY** | 共 19 处，均已有行内文字，两处新增说明属实；仍有将测试前置条件或 VACUUM 前置删除称为“不影响结果的清理”的错误，详见逐点表。 |
| Q9：exclude-functions 理由 | **PARTIALLY** | RemoveAll 当前命中范围符合 verifier 临时工作目录；Fprintf 当前实际 errcheck 命中为 HTTP 输出，但全局豁免不保证未来持久化文件写入不被豁免；Sscanf 正则防溢出的说法错误，且漏述测试命中。 |
| golangci 常规门禁 | **PASS** | v2.14.0 `run ./...` 输出 **0 issues**，退出 0；仅 generated API 排除未命中 warning。不能据此证明被排除项的解释正确。 |
| CI：安装顺序 | **NOT FIXED，锁定版本入口遗留** | `.github/workflows/ci.yml:145–149` 仍先 `npx playwright install --with-deps chromium`，后 `make e2e`；npm ci 在后者配方内。frontend job 的 npm ci 不会共享 node_modules 到独立 e2e runner。 |
| CI：needs 链 / YAML / actionlint | **PASS** | e2e needs `[backend, contract, frontend]`；image needs `[backend, contract, frontend, security, e2e]`。actionlint、YAML 解析、23 个 run 步骤的 `bash -n` 均通过。 |
| CHANGELOG / 提交说明 | **提交说明不符；CHANGELOG 基本相符** | 实际 diff 只有 4 个文件：lint 配置、durable.go、verify.go、第二轮报告。S3、metrics、CI、CHANGELOG 均未改。提交说明却列出这些修复已完成，必须纠正交付内容与说明的差异。 |

## R2-1：仍然阻断收敛的运行缺陷

锁定依赖为 `github.com/aws/aws-sdk-go-v2/service/s3 v1.114.0`。已读取本机模块源码 `api_op_ListMultipartUploads.go:203–226` 的 KeyMarker 注释：一般桶只传 key-marker 时，后续结果只包含字典序严格大于该 key 的对象；同时传 upload-id-marker 才能继续相同 key 的上传。目录桶是不同语义，不能用其不需要 upload-id-marker 的规则解释一般 S3 桶。

本轮将 HEAD 导出到 `/tmp/quality-review-3/repo`，复用并检查第二轮的受控 HTTP transport 探针，运行真实锁定 SDK 的请求序列化。模型含同 key 的 101 个 sessions，第一页返回 100 个及双 marker。当前代码的第二次实际请求与结果：

```text
page2 query=key-marker=key&max-uploads=100&prefix=key&uploads=
unreclaimed=map[101:true] lists=2
--- FAIL: TestReviewAbortPagination
```

`TestReviewDetachedAbort` 同次 PASS，确认取消父 context 后仍有有效的独立 deadline。这只能证明清理请求可执行，不能弥补分页漏项。探针是 SDK transport 模型，不是 MinIO 集成实测。

关闭要求：将响应的 `NextKeyMarker` 与 `NextUploadIdMarker` 一并传到下一请求，并补入同 key 跨页回归测试。日志改为条件性生命周期说明或直接提示清理失败，不能保证用户桶会自动回收。

## Q5 / Q8：测试能证明什么

`jobs/phase3_test.go:79–98` 的 fake 在中断时增加 `parts[key]`，后续 Put 成功时直接 `delete(f.parts,key)`。`faultmatrix_test.go:115–147` 对两者分别断言非零、归零，且本轮原测试实际 PASS；但成功重传可能创建新的真实 upload session，不会天然完成另一个 upload ID 的旧 parts。fake 的 `AbortMultipart` 也不是该测试所调用的真实 Store 清理路径。应在这些注释附近写明“只验证 fake 模型/远端状态协议，不证明真实 session 无残留”。第二轮报告已登记真实 MinIO 残留检查欠缺，本轮继续保留，不把模型测试通过写成真实清理通过。

Q8 的 sampleRe 独立 Go 探针结果：普通标签匹配；`m{database="a}b"} 1` 和 `m{database="a\\}b"} 1` 均不匹配。后者的双反斜杠表示合法的字面反斜杠，后续花括号本身无需转义。直接写 `\}` 不是应被支持的标准转义，不能用“容忍转义花括号”作为语法正确性的标准。当前 parser 同时缺乏完整转义合法性校验，宜由正式 parser 接管语法验证。原 HTTP 格式测试本轮定向运行仍因 httptest 监听被禁止而失败，未宣称通过。

## Q9：实际排除命中与逐点清理复核

在 `/tmp/quality-review-3/exclusions.yml` 中仅移除 G702 测试排除和 `fmt.Sscanf` / `fmt.Fprintf` / `os.RemoveAll` 三项函数排除，保持仓库配置不动。运行同版本 golangci 得 **25 issues**：24 errcheck、1 gosec，均可定位到现有调用。

| 排除项 | 实际命中 | 判定 |
|---|---|---|
| G702 | `db/db_test.go:70` 一处，测试重启自身；生产 0 | 独立规则与测试范围合理，理由需改成真实的自启动测试场景。 |
| Sscanf | `jobs/jobs.go:194,229`，`jobs/m1_test.go:170`，共 3 处 | `\d+` 只限制字符，不限制 int64 数值范围；测试处解析 psql 输出，并无文件名正则。探针长数字匹配正则，却返回 `value out of range`、扫描数量 0、jobID 保持 0。配置 `:61–63` 的理由不成立。未据此额外声称已存在可利用漏洞。 |
| RemoveAll | `verifier/verifier.go:196,201,205,210,243`，共 5 处 | 确为工作目录或短 socket 临时目录的失败清理/defer。忽略错误是 best-effort 策略，不能证明敏感明文/目录必然删除；其他显式检查错误的 RemoveAll 调用无需此豁免。 |
| Fprintf | `server/metrics.go` 共 16 处 | 本轮真实 errcheck 命中均为 HTTP ResponseWriter，忽略 mid-response 写失败有合理边界。源码还包含 dumper 的 strings.Builder 和 stderr 输出；配置是函数级全局豁免，`:59` 的“persistent file writes are NOT exempt”并无配置约束，应改为当前调用审计事实或收窄范围。 |

`_ = os.Remove` 精确检索共 **19 处**，逐点复核如下（路径均相对 `backend/internal/`；合并行列出的每个调用均已检查）：

| 调用点 | 实际用途与理由核对 |
|---|---|
| `jobs/durable.go:18` | 本轮新增原子写临时文件说明；成功 rename 后旧路径消失，提前失败时为 best-effort 清理。理由可接受，不保证失败时无残留。 |
| `jobs/verify.go:234,238` | 下载密文 copy / close 已失败，删除部分副本；保留原失败的取舍合理。 |
| `jobs/verify.go:274,278` | 下载 manifest copy / close 已失败，删除部分副本；同上。 |
| `jobs/verify.go:349` | 远端下载的密文验证副本，defer 清理不改变验证计算结果；不等于保证文件删除。 |
| `jobs/verify.go:365` | 本轮新增下载 manifest 临时副本说明，理由可接受。 |
| `jobs/verify_lifecycle_test.go:471` | 构造 manifest 缺失的测试前置条件，并非结果无关的清理；现注释不实，应检查删除错误。 |
| `db/backup.go:35` | VACUUM INTO 前移除旧 `.inprogress`；删除失败可能使 VACUUM 失败，不能说不影响结果。后续 SQL 会报错，未发现静默发布成功。 |
| `db/backup.go:40,44,48` | VACUUM、chmod、rename 失败后的临时文件清理，返回原失败合理。 |
| `dumper/dumper.go:368` | 仅未 committed 时清理临时密文，保留原 dump/提交失败合理。 |
| `verifier/verifier.go:239` | 优先删除敏感明文；可能随后保留工作目录作证据。忽略错误可能留下明文，通用“不影响结果”不能表达残留边界，应说明 best-effort 与后续清理机制。 |
| `verifier/verifier.go:572` | 解密失败后清理部分明文、返回解密/取消错误合理，但同样不保证无明文残留。 |
| `verifier/verifier_test.go:139` | 为错误密钥场景删除上次成功明文，属于测试准备，不是普通 teardown。理由需改为实际场景，宜检查错误。 |
| `verifier/verifier_test.go:416` | 删除 stub 保持进程标记以允许后续 stop/sweep，是下一断言的前置条件；邻近注释有说明，但行内“不影响结果”不准确。 |
| `config/config.go:246,269` | 原子发布 secret 后清理临时硬链接，defer 也覆盖提前失败；不影响已发布 secret 的内容，best-effort 理由可接受，不保证临时 secret 路径无残留。 |

因此 Q9 已有进展，但不能称所有解释均与真实调用一致。`check-blank: false` 仍按设计存在，本轮结论只覆盖所要求的清理调用，不将其扩展为全仓库每一处 `_ =` 都已审计。

## CI 与发布说明抽查

e2e 与 image 的依赖链正确，但安装顺序未修复。后续 `make e2e` 会执行 npm ci 并再次安装锁定 Playwright，因此本轮不推断 CI 必红；问题是首次安装入口仍不受本地 lockfile 安装结果约束。

`git show df68bcd` 承诺的 S3 markers、abort 文案、metrics label gate、CI npm ci 顺序均未出现在 diff。实际只有 lint 理由修改、两处 Remove 注释和新增第二轮报告，提交说明显著超出代码交付。CHANGELOG 中质量工具、故障矩阵、恢复测试条目与已有功能基本一致；它不是集成验证成功的证明。重复的 `SB_JOB_TIMEOUT` Added 条目仍是文档整理遗留。“Multipart upload with automatic abort on failure”应理解为尝试 abort，不是必然无残留。

## 验证记录

环境：Go 1.26.6 linux/arm64，Node 22.22.2；`GOCACHE=/tmp/final-cache`，`GOLANGCI_LINT_CACHE=/tmp/quality-lint-cache`。不修改仓库业务代码、依赖、配置或生成文件。

| 验证 | 本轮实际结果 |
|---|---|
| `go test -json -count=1 -timeout=10m ./backend/...` | 已实跑，退出 1。151 个已启动测试中 141 PASS、7 SKIP、3 FAIL；16 个包 PASS、3 个包 FAIL、7 个包无测试。jobs / server / outbox 在 httptest 启动时触发 `listen tcp6 [::1]:0: socket: operation not permitted`，后续测试未完整执行。不是全量通过，也不归为产品回归。日志：`/tmp/quality-review-3/go-test.json`。 |
| Docker 集成 | MinIO 1 项及 PostgreSQL 6 项因 docker run 失败 SKIP；没有真实残留 uploads 验收结果。 |
| 原 fake multipart 测试定向执行 | `TestUploadInterruptLeavesNoPartialAndResumesIdempotently` PASS，无 SKIP；仅在上述模型边界内有效。 |
| 原 metrics 格式测试定向执行 | 环境阻断：同样禁止监听，退出 1；日志 `/tmp/quality-review-3/metrics.log`。 |
| SDK 双 marker / detached context 探针 | 分页 FAIL，detached PASS；在 HEAD 导出副本中运行，探针源文件为 `repo/backend/internal/storage/q3_probe_test.go`。 |
| metrics 正则 / Sscanf 溢出独立探针 | 已实跑，复现含 `}` 合法标签被拒及纯数字溢出；源码 `/tmp/quality-review-3/syntax.go`。 |
| golangci-lint v2.14.0 `run ./...` | PASS，0 issues；日志 `/tmp/q3-lint.log`。 |
| 临时配置移除指定排除 | 预期退出 1，25 issues；全部命中已分析，日志 `/tmp/quality-review-3/exclusions.log`。 |
| `npm run lint` / `npm run stylelint` | PASS。 |
| `npm test`（Vitest 5.0.3） | PASS，4 文件、28 测试。 |
| TypeScript | `tsc -p tsconfig.json --noEmit` PASS。首次尝试 `tsc -b` 搭配临时 tsBuildInfoFile 被 TS5094 拒绝，随后按当前单 tsconfig 项目改用无输出类型检查；未把失败调用记作通过。 |
| `make -n lint` / `make -n check` | PASS，无配方覆盖 warning；lint 保留 gofmt/vet/golangci/eslint/stylelint；check 计划含 api-breaking、Vitest、后端测试。仅 dry-run，不运行生成器或覆盖业务文件。 |
| actionlint / YAML / Bash | 本机 actionlint v1.7.12 PASS（CI 声明固定版本 v1.7.7，本轮不是同版本复跑）；PyYAML 解析 PASS，所有 23 个 run 步骤 `bash -n` PASS。 |
| `git diff --check fa00114..HEAD` | PASS。 |
| Playwright、完整 CI、真实 MinIO 故障回收 | 未实跑，不声称通过。 |

## 收敛条件与非阻断遗留

本轮阻断项仍是 **R2-1 实际分页缺陷**，须交付真实代码修复及回归验证。提交说明需要与交付一致；当前不能用提交标题替代文件证据。

其余保留项：fake 边界注释、MinIO 故障后残留 uploads 断言（已登记）、metrics 特殊样本必现断言及正式 parser、Q9 上述不准确理由、CI 首次 Playwright 安装顺序、CHANGELOG 重复项及 Makefile “Everything CI runs”过宽的描述。仅剩这些文档/测试改进且无运行缺陷时，可按本轮约定批准并列遗留；当前尚不满足。
