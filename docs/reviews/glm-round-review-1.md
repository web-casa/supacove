# GLM 修复收敛评审（第 6 轮）

结论：**NEEDS_FIXES**。

评审日期：2026-10-07。提交范围：`6194e58..e77eddd2d7ca5faae6d13c9e59d6582690dc1b70`，包括 `c2cdf7f` 全量 diff 与 `e77eddd` CHANGELOG。基线采用本轮用户给出的九项发现及修复声明；未把旧提交消息中的“已通过”当成本轮执行证据。

发现 1 项 P1、2 项 P2，以及注释准确性问题。业务代码未由本评审修改；探针、变异及副本均在 `/tmp`。评审中途工作区出现其他来源对 `backend/internal/storage/s3.go`、`s3_pagination_test.go` 的未提交修改，已移除 marker；本报告仍评价指定提交快照，未覆盖或计入这些修改。以下行号均对应 **e77eddd 快照**。

## 逐项判定

`FIXED` 表示代码问题已修复；不代表下表明确列出的外部验收缺口已经完成。

| # | 判定 | 证据与限制 |
|---|---|---|
| 1 metrics 转义、门禁与改名 | **FIXED**（真实 promtool 验收未完成） | `promLabel` 只转义反斜杠、双引号和 LF；门禁白名单及解码全等比对有效。临时副本仅替换测试 HTTP transport 后，当前 handler 的格式测试与 phase7 测试通过；删除反斜杠转义后测试明确失败。官方 `prometheus/common/expfmt v0.66.1` 解析当前 handler 输出的 14 个 family 成功。四个旧 `_total` 名在执行代码、非历史文档及输出中无残留，phase7 期望已同步。没有取得真实 promtool exit 0，不能用解析库冒充。 |
| 2 MinIO 固定点重扫 | **REGRESSED** | 固定点方案落地，但每轮仍发送 `KeyMarker=key`，在 AWS 上把目标 key 全部排除。符合 AWS 文档的桩返回 **0/101 aborted**。新桩虽声称同时模拟 AWS/MinIO，实际忽略 markers，且第一页剩余数据时仍输出 `IsTruncated=false`。见 R6-01。真实 MinIO 101-session 测试本轮为 **SKIP**，未穿透。 |
| 3 govulncheck pin | **PARTIALLY** | CI 安装引用 `GOVULNCHECK_VERSION=v1.8.0`，本地真实二进制 `-version`/`go version -m` 均确认 v1.8.0；扫描进入漏洞库请求阶段，因网络沙箱失败。CI 注释将旧版本失败归因于 `go <1.26` 声明不准确：go.mod 的 go 指令是最低要求，不是最高支持版本。需描述扫描器构建工具链/分析依赖兼容性。 |
| 4 retention anchor | **FIXED** | 真实运行原测试通过。`KeepRemote=1, KeepDays=1`，两代 `uploaded_at` 回拨两天；删除远端 `i==0` 守卫后明确报“remote retention deleted the newest committed backup”。额外只读断言确认 older 远端对象消失、记录 `remote_state=deleted`，anchor 存活。原测试已有 older 本地文件回收断言，但没有 older 远端回收断言。两处 `keepDays=0` 注释错误，见准确性备注。 |
| 5 两上传测试对象键 | **FIXED** | 两处均使用真实 `BackupKey(backup_uuid)`。原测试通过；失败注入探针记录 `putCount=3, failPut remaining=96`，证明 99 次预算确实命中 3 次；校验不符测试已有 `verification mismatch`、真实 key 不存在及 `putCount>0` 断言，不再观察不存在的旧命名对象。 |
| 6 session retry | **FIXED** | 前端 4 文件、31 测试通过。401 总调用 1 次、500 总调用 2 次，默认 predicate 的 401/500 首次/500 第二次三种结果均被断言。调用 `getDefaultOptions()` 与 `fetchQuery()`，没有访问私有实现；锁文件安装的 TanStack 版本与当前失败计数从 0 开始的行为一致。 |
| 7 CI/Makefile/e2e P3 组合 | **PARTIALLY** | lint 开头安装依赖；无 node_modules 的快照 `make -n check` 首条即依赖检测/安装，早于 eslint/stylelint；Playwright locale、`.first()`、gitignore 正确。就绪始终失败的 shell 探针 exit 1。PyYAML 显式安装、Trivy action 升级已落地。**PR 基线守卫失效**，见 R6-02；不能整体判定修复完成。 |
| 8 MinIO 跨测试回收 | **PARTIALLY** | `minioOnce` 重置使第二个测试重新启动容器，但 `minioStopped` 没有复位，第二个容器不会回收。模拟启动、执行真实 cleanup 的两子测试探针仅记录 `rm -f container1`，见 R6-03。三次独立 `-count=1` 均尝试过，但真实 MinIO 均 skip，包随后因禁止监听端口失败，不能称为三跑稳定绿。 |
| 9 CHANGELOG | **FIXED** | 四个旧→新指标名完整准确，明确无旧 `_total` 别名、破坏性变更及标签转义变化；记录固定点重扫。`illegal _total` 是 promtool lint 命名规范意义，不能理解成 Prometheus 文本语法禁止 `_total`。历史“4/4”观测本轮无法独立复核；CHANGELOG 也不能抵消 R6-01 的实现缺陷。 |

## 必须修复的发现

### R6-01 / P1：重扫在 AWS 上不清理任何目标 key 的 multipart session

位置：`backend/internal/storage/s3.go:164-169`；配套测试 `s3_pagination_test.go:18-52`。

新实现每次 `ListMultipartUploads` 同时发送 `Prefix=key`、`KeyMarker=key`，不发送 `UploadIdMarker`。AWS 通用桶的规定是：这种请求只返回字典序**大于** key-marker 的 key，不包括相等的目标 key。因此即便目标有一个或 101 个未完成上传，也不会进入 abort；`aborted==0` 立即返回。失败上传遗留分片可能持续占用并计费，直到外部生命周期规则清理。这是修复 MinIO 时引入的 AWS 行为退化。

依据：[AWS ListMultipartUploads 的 key-marker 定义](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListMultipartUploads.html#API_ListMultipartUploads_RequestParameters)。

复现没有改动生产方法：在 `/tmp/r6-copy` 将原测试的 socket transport 换为内存 HTTP transport，原桩先通过；随后仅恢复桩中 AWS 的过滤规则：当 `key-marker` 等于目标且无 `upload-id-marker` 时返回空列表。结果：

```text
aborted 0 of 101 in-flight sessions; same-key page 2 was skipped (R2-1)
FAIL
```

新桩的行为声明不属实：它既不检查 markers，也不模拟 marker 失效，截断位还固定为 false。它证明的只是“反复返回未删除项时客户端会重扫”。

建议：每轮仅按 Prefix 重扫，不传两个 marker；分别锁定 AWS 的严格 marker 语义及 MinIO 的实际行为。更正桩注释与截断响应，并重新跑真实 MinIO 101-session 回归。

### R6-02 / P2：基线守卫在标准 PR 浅克隆中失败放行

位置：`.github/workflows/ci.yml:55,70-76`。

contract job 的 checkout 没有配置 fetch-depth，默认只取一个提交；base SHA 不保证存在。`git diff baseSHA HEAD` 报错放在 `if ... | grep -q ...` 条件中，即使 Bash 开了 `-e -o pipefail`，也只是条件为假，整个 if 最终成功退出。PR 同时修改契约和基线时，前面的 oasdiff 比较的是两份 PR 内文件，该守卫本应负责拦截基线变更，却会放行。

依据：[actions/checkout 默认只取单个提交，fetch-depth: 0 获取历史](https://github.com/actions/checkout)。

本地临时 Git 仓库，用与 Actions 一致的 `bash --noprofile --norc -eo pipefail` 执行原守卫：

```text
完整历史、普通基线修改：exit 1，blocked（正向对照）
--depth=1 克隆：fatal: bad object <base-sha>，守卫 exit 0
完整历史、R100 改名：--name-only 只有新路径，守卫 exit 0
```

改名探针仅证明该守卫可绕过；若基线文件被直接移走，前一个 oasdiff 步骤仍可能报缺文件，不能把这个单独探针描述成整条 CI 必然通过。浅克隆失败放行本身已足以阻塞。

建议：显式获取 base SHA/完整历史，先独立验证基线比较命令成功，再检查变化；使用禁用 rename detection 的路径 diff 或按 blob 比较。若政策真的是“仅 release tag 可更新”，还应明确 push/merge_group 的覆盖范围，当前条件只针对 PR。

### R6-03 / P2：第二个 MinIO 容器泄漏

位置：`backend/internal/storage/minio_test.go:82-91`，以及初始化分支没有设置 `minioStopped=false`。

第一个测试 cleanup 把 `minioStopped` 设为 true 并重置 Once/client。第二个测试启动新容器，但 stopped 保持 true，因此第二次 cleanup 条件不成立。每个包含两个 MinIO 测试的独立进程可能遗留一个容器；反复本地测试会累积后台容器，耗用资源。若同一进程继续复用，则也不再具有注释宣称的每测试全新实例语义。

探针以临时 docker shell 记录调用，只模拟成功启动状态，**调用原 `requireMinIO` 注册并执行真实 cleanup**。两个子测试后：

```text
actual cleanup calls: "rm -f container1\n"
second container never removed
FAIL
```

这是生命周期逻辑探针，不是真实 Docker 集成成功的证据。建议在成功创建新实例时复位全部相关状态，或统一由 TestMain 管理共享容器；测试容器数与删除数应一致。还应避免忽略 `docker rm` 失败后直接将状态视为已清理。

## 无方向扫描与准确性备注

- **control 字符不是本次新缺陷。** Prometheus 文本格式允许任意 UTF-8 标签内容，只要求转义反斜杠、双引号、LF。[官方格式说明](https://prometheus.io/docs/instrumenting/exposition_formats/#text-based-format) 与实际 `expfmt` 解析一致：NUL、TAB、CR、U+001F、DEL 原样放入引号内都解析成功并全等回读。没有理由把它们改成非法 `\t`/`\xNN`。
- **固定点 abort 失败重试会放大，但不是无限循环。** 每轮至少有一次成功 abort 才继续；全部失败时当轮结束。混合失败时同一失败 upload 可以在后续轮重试，最多 100 轮；服务端遵守 MaxUploads=100 时最多 100 次 List、10,000 次逻辑 Abort（SDK 内部重试另计）。共享 30 秒 context 限制 SDK 网络操作，原调用方取消不会终止清理。不能把“10k”解释成必定完成 10k 次回收；deadline、失败项占页、持续 churn 都可能提前结束。未发现需要另立阻塞项的无界重试，但应保留 best-effort 表述。
- **KeepDays=0 注释错误，执行值正确。** `faultmatrix_test.go:228-231,258-259` 写 0 会使全部过期；`upload.go:215-217` 实际是 0 禁用天数限制。测试执行 1 天并回拨 2 天，变异已证明 anchor 守卫有效。前一段注释应改成当前真实配置；后一段检查的是本地 keep 数量，不能归因于 KeepDays。建议把本轮 older 远端回收断言补回正式测试。
- **govulncheck 注释错误。** `.github/workflows/ci.yml:91` 不应把 Go 最低版本指令写成旧扫描器拒绝 Go 1.26 的充分原因。缓存中的 v1.1.4 go.mod 是 `go 1.22.0`，这不代表最大只支持 1.22。v1.8.0 的 pin 本身无误。
- **session 测试无不合理私有 API 耦合。** 它有意约束升级后仍需保持的重试次数。当前 `query-core/src/retryer.ts` 的 failureCount 从 0 起，调用 predicate 后才加一。三种结果分支覆盖成立，但不等于所有输入类型的条件覆盖；普通非 ApiError 尚无独立断言，不另列阻塞项。
- **e2e 与 CHANGELOG 的范围。** 就绪守卫的失败路径已实测；不把该结果推断成完整浏览器 E2E 通过。提交消息声称的 bootstrap token 提取失败必红，在本次 diff 没有新增显式非空检查，不计为本轮已验证的修复。直接增加 `.first()`、locale、gitignore 未见新缺陷。

## 验证记录

环境：Linux arm64，Go 1.26.6；所有 Go 验证使用 `GOCACHE=/tmp/r6-cache`。提交快照保存在 `/tmp/r6-pristine`；可变探针副本在 `/tmp/r6-copy`；执行日志和脚本在 `/tmp/r6-evidence`。临时日志不是版本化交付物，本报告已摘录关键结果。

| 验证 | 结果 |
|---|---|
| 快照 `go build -buildvcs=false ./backend/...` | exit 0；archive 副本无 Git 元数据，故禁用 VCS stamping。 |
| 快照 `go vet ./backend/...` | exit 0。 |
| 快照 golangci-lint v2.14.0 `run ./...` | exit 0，0 issues；缓存另设 `/tmp/r6-lint-cache`。 |
| `actionlint .github/workflows/ci.yml`、`git diff 6194e58..HEAD --check` | exit 0。静态语法检查不能发现 R6-02 的运行语义问题。 |
| 原仓库 `go test -count=1 -timeout=10m ./backend/...` | **失败（环境）**：jobs/outbox/server/storage 等 httptest 无法监听，`socket: operation not permitted`。其余多个包通过，不宣称全量绿。 |
| 原仓库 storage 连续三次 `go test -v -count=1 ./backend/internal/storage` | **均未验收**：两个 MinIO 测试 skip，后续 HTTP 桩监听失败。日志 `storage-1.log` 至 `storage-3.log`。 |
| 原仓库三个定向 jobs 测试 | retention anchor、verify mismatch、upload failure 全部通过，约 3 秒。 |
| metrics/phase7 内存 transport | 两测试通过；handler/路由/DB 生产代码未改；网络测试夹具替换，不能视作真实 HTTP 网络验收。 |
| metrics 漏反斜杠变异 | exit 1，全等回读断言失败，证明门禁有效。 |
| retention 去除 `i==0` 变异 | exit 1，anchor 被删断言失败。 |
| older 远端对象/状态补充断言 | 通过，对象消失且状态 deleted，anchor 幸存。 |
| 上传失败命中计数补充断言 | 通过，putCount=3、failPut=96。 |
| AWS marker 桩 | 原桩内存版本绿；补 AWS 正确规则后红，0/101。 |
| MinIO cleanup 模拟探针 | 红，两个实例只删除一个。 |
| CI 守卫三探针 | 普通修改红；浅历史错误及 rename 均错误放行。 |
| 无依赖快照 `make -n check` | 安装依赖分支早于 lint 命令；未用假安装结果冒充 npm ci 成功。 |
| e2e 就绪失败探针 | 构建/服务以临时 shell 替身隔离，curl 始终失败；原脚本打印 `server never became ready on :36470`、exit 1。 |
| 前端 `npm exec vitest run` | 4 文件、31 测试通过，包括新增 3 个 session 测试。 |
| 前端 `npm run lint`、`npm run stylelint` | 均 exit 0。 |
| Playwright artifacts `git check-ignore` | 两个目录均匹配。 |
| 官方 expfmt v0.66.1 | handler 的 14 个 family 解析成功；五类控制字符全等回读成功。 |
| 真实 promtool / MinIO Docker | **受阻**：docker socket permission denied；`sudo -n` 被 no-new-privileges 拒绝。没有请求越过本会话禁止提权的边界。发现 `/tmp/q2-bin/promtool` 只是写标记后 exit 0 的 shell 替身，已排除，不作为真实 promtool 证据。 |
| govulncheck v1.8.0 | 版本确认；扫描因访问 vuln.go.dev 的 DNS/socket 被禁失败，未得到漏洞扫描结论。 |

## 重新验收条件

修复 R6-01、R6-02、R6-03，并同步不准确注释。随后在允许 Docker/监听端口的环境中，对新的提交运行真实 handler 输出的 `promtool check metrics`，执行非 skip 的 MinIO 101-session 测试与 storage 三次独立 `-count=1`，确认无遗留测试容器，并补跑后端全量测试和 govulncheck。当前既有已复现的生产退化，也有尚未完成的真实服务验收，因此不能 APPROVED。
