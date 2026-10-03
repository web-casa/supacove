# Phase 3 实现评审（第一轮）

评审日期：2026-10-04。基线：`982d01c..c3341aa`；检查了 `94b7ddf` 与 `c3341aa`，行号均指当前 HEAD。依据为 `docs/dev-plan.md:54` 的协议 C/C.1、`:63` 的协议 D，以及 `:141` 的 Phase 3 任务和 DoD。本轮只提交评审文档，没有修改生产实现。

**结论：未通过。共 1 项 P0、12 项 P1、6 项 P2。** 主要阻断点是旧备份可能被覆盖、完整性锚点不成立、远端提交/删除无法在崩溃后收敛。现有测试通过的部分不足以证明 Phase 3 达标。

特别说明顺序：需求描述中列举的“upload → manifest → verify”是本次待审实现的顺序；规范 `docs/dev-plan.md:57` 要求的是 **intent → upload → verify → manifest → local commit**。本报告以规范为准。

## P0

### P0-01：对象身份依赖 SQLite 自增 ID，重建元数据会覆盖旧备份

- **位置：** `backend/internal/storage/storage.go:124`、`:130`；`backend/internal/jobs/upload.go:46`、`:74`；`backend/internal/jobs/jobs.go:594`。
- **问题：** 对象键仅由用户 prefix、`databaseID`、`jobID` 拼接，manifest 的 backup ID 也是 `job-N`。SQLite 丢失后重新配置原桶/原 prefix，新数据库和新任务会从同样的整数 ID 开始；上传没有远端身份核对或禁止覆盖条件。未启用版本化的桶中，第一次新备份即可永久覆盖原密文和 manifest；两个实例共用 prefix 也会发生。诊断 canary 成功不能证明这个 namespace 可安全写入。违反协议 C 的不可变 artifact 身份及 Phase 3 的 SQLite 丢失恢复场景。
- **证据：** `TestReviewFreshDatabaseReusesRemoteKeys` 使用两个独立 SQLite Store 和同一个 fake bucket，确认第二个实例成功覆盖 `dest/databases/1/backup-1.dump.age`。这是生产键构造和上传函数的实际调用。
- **可执行修复：** artifact 在首次生成前持久化全局唯一 backup ID，数据库/实例也使用稳定身份；远端键由这些不可复用身份构造，attempt 只引用既有 artifact。接入非空旧 namespace 时先只读发现与确认归属；对不可变提交标记采用 provider 支持的条件写入/冲突检查。不要依赖桶版本化来掩盖身份碰撞。
- **验收标准：** 两个全新元数据库、元数据库回滚及多实例接入同一桶/prefix，均不能修改旧对象或提交标记；重试只重用自己 artifact 的键；未知归属旧对象保持只读且不参与自动删除。

## P1

### P1-01：默认跳过 read-back，未通过完整性校验的备份仍可替换保护锚点

- **位置：** `backend/internal/server/api_phase3.go:77`、`:266`；`api/openapi.yaml:846`；`backend/internal/jobs/upload.go:105`、`:117`、`:222`、`:250`。
- **问题：** OpenAPI 声明 `verifyReadback` 默认 true，但生成模型是指针，handler 用 `derefBool(nil)` 得到 false。省略该字段的正常请求会关闭唯一实现的完整内容校验。关闭后没有等价 provider checksum 与本地完整密文哈希的比对，却仍写入 `remote_state='committed'`。保留策略只按 committed/id 排序，不检查 `remote_verified`，因此可删除上一份已验证备份。即使用户显式传 false，协议 D 允许关闭的是恢复验证，完整性锚点仍不可省略。
- **证据：** `TestReviewMissingVerifyReadbackDefaultsFalse` 确认默认值偏差；`TestReviewUncheckedAnchorDeletesVerifiedBackup` 在预置合格旧记录并关闭 read-back 的状态夹具中，确认新对象虽会返回同大小损坏内容，仍未经验证即提交，并删除旧的 `remote_verified=1` 备份。该夹具用于验证锚点资格判定，不表示当前 API 支持修改目的地开关；正常默认关闭的目的地同样会淘汰内容完好的旧副本而不验证新副本。SDK 默认 checksum 不能被视为这里已实现的 C.1 完整密文远端校验。
- **可执行修复：** 正确处理省略值；无等价 provider 校验时强制完整 GET/SHA-256。若保留开关，必须明确对应的替代校验协议，不得把“跳过校验”视为成功。锚点资格使用持久化完整性结果，并在事务中替换；没有合格锚点时禁止破坏性自动清理。
- **验收标准：** API 省略、true、false 三种输入均有明确测试；同大小替换、截断、缺 checksum、错误 checksum 都不能产生合格锚点；失败或未验证的新备份不能删除最后一份合格旧备份。

### P1-02：manifest 在校验前发布，失败后留下虚假的远端提交标记

- **位置：** `backend/internal/jobs/upload.go:96`、`:105`、`:109`。
- **问题：** manifest PUT 先于 `verifyRemote`。在发布后、校验前崩溃，桶里已经有被协议定义为“提交完成”的标记，但内容尚未验证；校验失败时只删密文，不删 manifest，确定留下悬空提交标记。且任意 GET 网络/权限错误都走“corrupt”删除分支，暂时读失败也会删除可能完好的密文。
- **证据：** `TestReviewManifestSurvivesFailedVerification` 确认失败后 manifest 存在而密文不存在。规范顺序见 `docs/dev-plan.md:57`；文件自身开头注释声称 verify 在 manifest 前，实际代码相反。
- **可执行修复：** 将提交标记发布移到完整校验之后；对网络读失败保留待定对象并重试，只对已确认损坏且归属明确的版本安排清理。若历史版本已有提前发布的标记，恢复流程必须重新验证，不能因标记存在直接晋升可用。
- **验收标准：** 用记录调用顺序的 backend 断言 intent → ciphertext PUT → 完整 GET/校验 → manifest PUT → commit；在每个边界终止进程，校验失败/未完成时都不能出现有效远端提交标记；读权限暂时失败不触发未经确认的破坏性删除。

### P1-03：没有协议 C 的重启续传/补提交，uploading 会永久滞留

- **位置：** `backend/internal/jobs/jobs.go:103`、`:233`、`:381`；`backend/internal/jobs/upload.go:53`、`:116`；`backend/internal/jobs/reconcile.go:100`；`backend/cmd/supabackup/main.go:212`。
- **问题：** 启动恢复沿用 Phase 2，仅把 running 改为 interrupted 并对账本地文件。没有按上传意图读取远端 manifest、校验既有对象、复用本地密文重传或补齐本地提交的路径；worker 只领取 pending。只读 Reconcile 也不会收敛状态。上传完成/manifest 发布后、本地 commit 前崩溃会永久保持 uploading；即使 remote commit 已写、最终 job success 尚未写，也只会成为 interrupted。手动再创建任务则重新走 dump。旧 uploading 还会被 `DeleteDestination` 永久当作在途上传。
- **证据：** `TestReviewRecoveryDoesNotResumeRemoteIntent` 调用真实 `RecoverInterrupted` 和 `claimAndRun`，结果仍为 interrupted/uploading、没有远端调用，目的地删除被拒绝。三次同进程 PUT 重试确实复用同一文件，但不能替代重启恢复。
- **可执行修复：** 持久化 artifact 与 attempt 的关联、目的地快照、manifest 路径/内容摘要和分阶段进度。恢复时按持久意图及远端版本核对：已验证并发布的补本地提交；未完成的重新上传同一密文；不明归属的隔离。恢复与取消需要明确仲裁，不能统一标 interrupted 后无人处理。
- **验收标准：** intent 后、multipart 中、上传后、校验前后、manifest 前后、远端 commit 与 job success 之间强制杀进程再启动；最终状态幂等收敛，`pg_dump` 调用次数保持 1，未提交密文不被清理，恢复失败有可重试状态及明确原因。

### P1-04：手动 multipart 与 Uploader 使用不同 upload ID，成功也泄漏；取消时无法 abort

- **位置：** `backend/internal/storage/s3.go:93`、`:103`、`:116`、`:134`；`backend/internal/jobs/upload.go:74`。
- **问题：** 手动 `CreateMultipartUpload` 得到 A，随后 `manager.Uploader.Upload` 自己创建并完成 B，完全未使用 A。成功设置 `completed=true` 后 A 不会被 abort；小对象上传传入 size=-1 时甚至是“创建 A + Uploader 单 PUT”，A 同样遗留。失败时外层 abort 的也是 A，不能兜底 B。内外 abort 都使用已取消的 ctx，取消/超时后请求不能发出。进程被杀时 defer 根本不执行，也没有持久 upload ID 或恢复回收流程。
- **证据：** 实际 SDK v1.23.11 传输探针：3 字节上传 `created=[upload-1], completed=[], aborted=[]`；17 MiB 上传 `created=[upload-1 upload-2], completed=[upload-2], aborted=[]`。取消探针观察到创建 2 个 ID、abort 请求为 0。SDK 源码 `feature/s3/manager/upload.go:663` 自行创建 ID，`:874` 的失败 abort 使用原 ctx。官方也说明失败 ID 应从 `manager.MultiUploadFailure` 获取：[AWS Go v2 S3 utilities](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/sdk-utilities-s3.html)。
- **可执行修复：** 只保留一种 multipart 所有权：使用 Uploader 时去掉额外 Create，通过真实失败 upload ID 做兜底 abort；或者完整手动管理分片。清理使用独立、短超时的 context，并持久化/对账无法清理的真实 ID。已知文件大小传 `artifactSize`，让声明的 64 MiB 阈值真正生效；补充崩溃遗留回收和 lifecycle 指导。
- **验收标准：** 小对象、分片成功、读错误、part/complete 失败、取消后，`ListMultipartUploads` 无不应存在的 ID；无法 abort 的真实 ID 有可重试记录；杀进程残留经重启或明确生命周期机制收敛。不能只检查 GetObject 能读到内容。

### P1-05：manifest 使用非 seekable reader，HTTP S3 目的地必然在提交阶段失败

- **位置：** `backend/internal/jobs/upload.go:97`、`:193`；`backend/internal/storage/s3.go:78`。
- **问题：** 自制 `byteReader` 仅实现 `io.Reader`，manifest 小对象直接进入 SDK `PutObject`。当前 SDK 在 HTTP endpoint 上计算 payload hash 需要 seek；`ContentLength` 不能解决此要求。诊断使用 `strings.NewReader`，所以可以先通过创建目的地测试，再在每次正式备份的 manifest 阶段失败。HTTP MinIO 正是项目 compose 支持的配置。
- **证据：** 用真实 SDK、无网络的 HTTPClient 模拟和等价非 seekable reader 调用 `Store.Put`，请求数为 0，返回 `failed to compute payload hash: failed to seek body to start, request stream is not seekable`。现有 MinIO 测试没有调用 jobs 的 manifest 上传路径。
- **可执行修复：** 删除自制 reader，使用已有 `bytes.NewReader(result.manifestBytes)`；必要时对通用 Backend 的 reader 能力和 SDK 重试要求作明确约束。无需关闭签名或校验来绕过。
- **验收标准：** HTTP MinIO 的完整 `uploadAndCommitRemote` 能成功；manifest PUT 的 SDK 重试能够重读完整内容；HTTPS S3/R2/B2 同样通过，manifest 与本地产物信息一致。

### P1-06：忽略 version ID，B2/版本化桶的删除只是隐藏，保留与对账均失真

- **位置：** `backend/internal/storage/storage.go:107`；`backend/internal/storage/s3.go:79`、`:123`、`:164`、`:176`；`backend/internal/db/migrations/0006_destinations.sql:29`；`backend/internal/jobs/upload.go:293`。
- **问题：** Put 丢弃版本信息，Backend 的 Get/Delete/Presign 都只接受 key，SQLite 也没有 version ID。B2 无 versionId 的 DELETE 创建 delete marker，并不永久删除原版本；普通 ListObjectsV2 又看不到隐藏历史版本，代码却清空引用并标 deleted。上传重试的历史版本同样无法追踪，保留数量/费用可能持续增加；锁定版本也不能以“当前 key 不见了”判定已清理。
- **依据：** [Backblaze S3 Delete Object](https://www.backblaze.com/apidocs/s3-delete-object) 明确区分带 versionId 的永久删除与不带 versionId 的 delete marker。已核对本地 SDK 的 `PutObjectOutput.VersionId`、Uploader `UploadOutput.VersionID` 和 `DeleteObjectInput.VersionId`，均有现成支持。
- **可执行修复：** 上传结果返回并持久化密文/manifest 的版本；校验、下载、删除绑定对应版本。对版本化目的地使用 `ListObjectVersions` 的 key/version 双游标盘点已确认归属的历史版本及 delete marker，逐版本删除并确认；R2 等无相同行为的 provider 明确声明能力，不能统一假设 DELETE key 即永久回收。
- **验收标准：** B2 和开启版本化的 S3 桶中，重试产生多版本后 prune，确认目标版本确实消失、陌生版本不受影响；对象锁/权限不足留在待清理状态；不允许 ListObjectsV2 空结果成为唯一删除成功依据。

### P1-07：删除没有 deleting 状态，部分删除后仍被当作可用备份

- **位置：** `backend/internal/jobs/upload.go:266`、`:271`、`:293`；`backend/internal/db/migrations/0006_destinations.sql:30`；`backend/internal/server/api_phase3.go:189`。
- **问题：** 开始外部删除前没有落库删除意图。先删密文，再删 manifest；第二步失败或任一步骤后崩溃，行仍为 committed，下载 API 继续发放已不存在对象的预签名 URL。没有每个对象的删除进度、确认结果或独立持久化 cleanup error；仅在下一次成功备份后才可能再试。日志与备份失败确实分开了，但“已部分删掉却仍可用”的状态不成立。
- **证据：** `TestReviewPartialDeleteAndLogLeak` 注入 manifest 删除失败，确认密文已不存在而 remote_state 仍为 committed。当前 schema/实现不存在 deleting 分支。
- **可执行修复：** 原子检查锚点/活动引用并进入 deleting 后再做外部删除；记录每对象/版本结果，确认完成后才进入 deleted。独立清理任务持久化错误、次数和重试时间，重启可续做；下载/锚点查询排除 deleting。备份 status 保留原成功结果，同时 API 明确暴露清理状态。
- **验收标准：** 第一个 DELETE 成功、第二个失败，以及删除前后、确认前后、最终 SQLite 更新前后崩溃，均不会留下“committed 但部分缺失”的可用声明；自动重试不依赖再产生新成功备份，重复执行幂等，清理失败不改写备份结果。

### P1-08：新增存储错误未统一脱敏，可进入日志与任务错误 API

- **位置：** `backend/internal/jobs/jobs.go:480`、`:524`、`:659`；`backend/internal/jobs/upload.go:275`；`backend/internal/server/api_phase3.go:145`、`:195`、`:201`。
- **问题：** runJob 的 knownSecrets 只有数据库密码，目的地 AccessKey/SecretKey 从未加入，上传、verify、manifest 的 provider error 可未经这些值的脱敏写入 jobs.error_message，进而从任务 API 返回。retention、reconcile、presign 错误直接交给 slog。创建/诊断 API 调用 `dest.Secrets()` 的做法没有覆盖这些路径。provider/代理错误正文属于不可信输入，不能假定永远不会回显凭据或签名参数。
- **证据：** 将目的地 secret 设置为测试标记，注入含同一标记的删除错误，`TestReviewPartialDeleteAndLogLeak` 确认原文出现在生产 retention 日志。静态确认 upload 的最终 redact 只掌握 PG 密码。没有发现成功返回的 presigned URL 被主动写日志；缺陷是错误出口未受同等保护。
- **可执行修复：** 在持有目的地凭据的存储边界统一对外错误脱敏，供 upload/retention/reconcile/presign 复用；对外日志/持久化不要再附原始 error。签名 URL 整体按 bearer secret 处理，不能只删除 AccessKey 而留下可用签名。复用现有 `redact.Secrets` 的转义处理，不另造简单字符串替换。
- **验收标准：** 分别在 build/PUT/GET/manifest/DELETE/LIST/PRESIGN 注入含原值、URL 编码、JSON 转义凭据和签名 URL 的错误；扫描原始日志、SQLite error_message 及 API 响应，秘密值和有效 bearer URL 均不存在。

### P1-09：backendFactory 首次初始化有可复现 data race

- **位置：** `backend/internal/jobs/destinations.go:222`、`:230`、`:235`、`:246`；`backend/internal/jobs/jobs.go:82`。
- **问题：** destBackends map 本身受锁保护，但 miss 后在锁外读写 `r.backendFactory`。生产默认 factory 为 nil；worker 上传与 reconcile/test/download 并发首次构建时会竞争。锁外构建后也没有二次检查，会返回/缓存不同实例；测试 setter 同样无同步。单 worker 不能排除并发 HTTP handler。
- **证据：** `go test -race` 的 `TestReviewConcurrentBackendFactory` 报出 `destinations.go:230` 读与 `:231` 写的数据竞争。原有 phase3 fake fixture 预先设置 factory，绕开了这个生产首次初始化分支。
- **可执行修复：** 在 NewRunner 初始化不可变默认 factory；测试覆写限定启动前或统一锁保护。构建可在锁外进行，但发布时二次检查缓存；如要求同目的地只建一次，可使用按目的地的构建合并。删除/退役策略同步处理缓存凭据生命周期。
- **验收标准：** 默认 factory 下并发 upload/reconcile/test/presign 的首次访问，用 `-race -count=100` 无竞争；同目的地缓存发布一致，构建失败可重试，无死锁。

### P1-10：目的地删除只检查 uploading，破坏活动任务与历史备份引用

- **位置：** `backend/internal/jobs/destinations.go:131`、`:180`、`:205`、`:253`；`backend/internal/jobs/upload.go:33`、`:53`；`backend/internal/jobs/jobs.go:701`。
- **问题：** pending/dump 阶段的 jobs 尚未记录 destination_id/uploading，删除因此成功；随后的远端阶段才解析数据库目的地，任务失败。即使 upload 已解析目的地，删除也能在落 intent 前插入，缺少原子引用保护。删除不解除数据库绑定，后续备份继续失败；历史已提交任务经 `BuildBackendByID → GetDestination` 也被 deleted_at 过滤，无法再从 API 下载。AssignDestination 的活动任务 SQL guard 是有效的，但目标存活检查在另一条语句中，仍可能绑定刚被软删除的目的地。成功状态落库后又重新解析当前绑定来执行 retention，也没有坚持使用该 job 的目的地快照。
- **证据：** `TestReviewDeleteDestinationDuringDump` 在 running、尚未 uploading 的任务上成功删除目的地，随后真实 uploadAndCommitRemote 返回 ErrDestinationNotFound。其余窗口由查询/更新次序静态确认。
- **可执行修复：** 在入队/领取时固定目的地引用与配置快照；删除和分配在事务/条件语句中检查全部活动引用及目标存活。明确“停用新写入”和“移除历史可访问配置”的区别，保留历史下载/清理所需配置；retention 使用本次 job 的快照，不重新读取可变绑定。
- **验收标准：** 使用 barrier 覆盖 enqueue/dump、目的地读取后意图写入前、assign 检查后写入前、success 后 retention 前的删除/改绑竞争；不出现悬空引用，既有任务继续使用原配置或在任何外部副作用前明确拒绝，历史备份仍可访问。

### P1-11：失败/未提交对象没有宽限期和回收预算，连续失败会积压至停止备份

- **位置：** `backend/internal/jobs/upload.go:222`、`:328`、`:332`；`backend/internal/jobs/jobs.go:654`、`:699`；`backend/internal/db/migrations/0006_destinations.sql:29`。
- **问题：** remote retention 只选择 committed，local prune 只选择 succeeded，而且均只在新备份成功后运行。manifest 发布失败、校验失败、上传失败留下的本地密文/远端对象永久不进回收候选；没有明确宽限时间、失败 artifact 数量/字节预算或预算冲突告警。现有 staging 硬配额能阻止继续写满，但结果是后续备份被拒绝，不等于实现协议 D 的失败对象治理。反复手动重试/创建新任务即可累积。
- **可执行修复：** 为失败/待定 artifact 建立独立清理队列、宽限期限及数量/字节预算；先完成归属确认和恢复尝试，再选择非锚点、非活动引用对象。预算和保护冲突时持久告警并停止删除，不能为了腾空间突破保护。清理调度不依赖下一次成功备份。
- **验收标准：** 连续 30 次上传/manifest/校验失败后，待定对象数量与字节有明确上界或明确的“保护冲突而暂停”状态；锚点和待重传引用不被删除；错误原因、预算占用、重试时间可查询。

### P1-12：桶对账不读取 manifest，SQLite 丢失后的只读发现路径尚未实现

- **位置：** `backend/internal/jobs/reconcile.go:50`、`:64`、`:110`；`backend/internal/jobs/jobs.go:619`；`backend/internal/jobs/upload.go:97`。
- **问题：** Reconcile 只是 LIST key 与 jobs 两个字符串的比较，从未 GET/解析远端 manifest。清空 SQLite 后重新配置目的地，所有旧备份只成为 orphaned 字符串，无法呈现完整备份身份/哈希/目的地快照及对象归属。发布的 manifest 原封不动使用本地 `backup-jobN.dump.age` 文件名，远端实际 key 却是 `.../backup-N.dump.age`，没有准确的远端对象/版本引用。Phase 3 第 5 项要求的 manifest 目录发现并未完成。只读且不自动删 orphan 是正确的保护，但不代表发现能力已达标。
- **可执行修复：** 发布携带真实远端引用、不可变 backup ID 和版本信息的自描述 manifest；只读扫描 manifest 目录并验证格式/引用，区分“可发现”“已确认归属”“已校验”。SQLite 缺失时也可列出候选备份，但未知归属仍不自动导入为锚点或清理。
- **验收标准：** 完全移除元数据库后，仅凭桶与合法读取凭据发现既有备份，准确指向密文及可用版本，并能下载进行 SHA-256 验证；损坏/陌生 manifest 显示明确原因，不自动删除或赋予可信锚点身份。

## P2

### P2-01：清空目的地写入 0 而非 NULL，违反外键

- **位置：** `backend/internal/jobs/destinations.go:252`、`:259`；`backend/internal/server/api_phase3.go:161`；`backend/internal/db/migrations/0006_destinations.sql:27`；`api/openapi.yaml:864`。
- **问题：** API 约定 0/null 清空，但 SQL 参数直接传 int64(0)。生产 SQLite 启用 foreign_keys，destinations 没有 id=0，解绑必然失败并返回 500；即便禁用外键，解析逻辑也把非 NULL 的 0 当作真实目的地读取。
- **证据：** `TestReviewClearDestinationForeignKey` 返回 `FOREIGN KEY constraint failed (787)`。
- **可执行修复：** destID=0 时绑定 SQL NULL（nil 或无效 sql.NullInt64），负数作输入错误；保留现有原子活动任务 guard。
- **验收标准：** null、0 按 OpenAPI 成功解绑，之后 DestinationForDatabase 返回 nil 且可做本地备份；pending/running 时仍拒绝解绑；数据库不存在时给出契约定义错误。

### P2-02：远端保留隐式覆盖本地保留，unlink 失败还会丢失待清理引用

- **位置：** `backend/internal/jobs/upload.go:280`、`:293`、`:365`、`:369`；`backend/internal/jobs/jobs.go:701`。
- **问题：** localKeep=10、keepRemote=1 时，本地 prune 刚决定保留的旧文件随后被 remote retention 无条件删除，本地策略实际受远端策略覆盖。两条清理路径中，os.Remove 失败后仍清空 artifact_path/manifest_path，文件未释放而正常清理流程已失去引用；查询失败/未满足路径条件也可能直接抹掉引用。重启扫描可能重新发现部分文件，但不能替代持久清理结果。
- **证据：** `TestReviewRemoteRetentionOverridesLocalKeep` 复现 10/1 配置；`TestReviewLocalPruneDropsFailedUnlinkReference` 用不可移除的非空目录注入删除错误，确认路径仍存在但数据库引用已清空。
- **可执行修复：** 分别计算本地与远端副本候选，明确独立保留语义；每个副本只有删除成功或确认不存在后才能清空引用，其余保留独立 cleanup error 和重试信息。不要让一个副本删除结果隐式销毁另一个副本。
- **验收标准：** localKeep/keepRemote 为 10/1、1/10，以及 keepDays 组合时行为符合各自策略；权限/磁盘/IO 错误后仍可定位并重试剩余文件，无假释放容量、无丢失引用。

### P2-03：下载没有活动引用保护，刚发放的预签名 URL 可被 retention 立即失效

- **位置：** `backend/internal/server/api_phase3.go:189`、`:198`、`:224`、`:244`；`backend/internal/jobs/upload.go:250`、`:354`。
- **问题：** 预签名 URL 承诺 15 分钟有效，但没有为对象建立保护租约；下一份备份完成后可能立即删除刚签名的旧备份。该对象正在分段/重试下载时也不受保护。本地下载在查路径到 os.Open 之间可能被 prune，返回意外 404。已经打开的普通文件描述符在 Linux unlink 后通常仍能读取，此处不把它误报成所有本地流都会中断。
- **可执行修复：** 签名前原子校验副本状态并登记到 URL 过期时刻的保护租约；prune 排除未过期租约。流式下载使用打开/引用登记与清理状态的协调，流结束释放引用，重启时有过期回收机制。
- **验收标准：** 获取旧备份 URL 后立即运行 keep=1 retention，URL 在承诺窗口内仍可开始/继续下载；活动本地下载与 prune 并发不产生路径查询后的删除窗口；过期引用能被回收。

### P2-04：Reconcile 分类重叠、诊断过滤错误，且把并发快照差异当成确定缺失

- **位置：** `backend/internal/jobs/reconcile.go:50`、`:58`、`:82`、`:100`、`:114`。
- **问题：** uploading 的 key 被加入 Uncommitted，却不加入 expected，随后又成为 Orphaned。诊断过滤按任意 `"/diagnostic/"` 子串：空 prefix 的 `diagnostic/...` 过滤不掉，prefix 自带该段时正式备份反而被过滤。LIST 在前、SQLite 在后，worker 在间隙提交的新对象可能被报告 missing；缺少复查/“进行中”状态，也未检查 rows.Err，查询迭代中断可能生成不完整报告。
- **证据：** `TestReviewReconcileOverlappingCategories` 确认同一个已有上传意图的对象同时出现在两个分类；并发窗口和过滤问题为静态确认。当前没有自动删除，所以这里主要是诊断误报，不是已发生的对账误删。
- **可执行修复：** 所有已知意图均计入引用集合，分类互斥；诊断 namespace 按规范化 prefix 精确匹配。记录扫描边界/任务代次，对变化中的引用标待定，对 missing 做远端复查；检查 rows.Err，出错不返回完整成功报告。
- **验收标准：** 空 prefix、含 diagnostic 段的合法 prefix、上传中的对象均分类正确；在 LIST 和 SQL 之间用 barrier 注入上传提交/删除，报告不声称确定缺失；迭代出错返回失败且不触发清理。

### P2-05：本地下载只有词法路径校验，符号链接可以越出 staging

- **位置：** `backend/internal/server/api_phase3.go:232`、`:240`、`:244`、`:249`。
- **问题：** filepath.Abs 和前缀判断只验证字符串；os.Open 会跟随 staging 内符号链接，能读取 staging 外的文件。当前请求不能直接提交文件路径，利用前提是 staging/其父目录链接或元数据受到本地写入者影响；不能据此声称存在匿名任意文件读取。不过它违背了注释中“resolve under staging”的边界，也可能跨越共享 staging 挂载与应用 secret 的权限边界。仅排除目录也没有排除 FIFO 等特殊文件。
- **证据：** `TestReviewDownloadFollowsSymlinkOutsideStaging` 建立合法 staging 路径到外部测试文件的 symlink，直接调用真实 DownloadTask，返回 200 并读出外部内容。
- **可执行修复：** 用项目 Go 版本支持的 `os.OpenRoot`/Root.Open 建立受限根，或等效基于目录 fd 的安全打开，避免 EvalSymlinks 后再 Open 的竞态；校验已打开 fd 是普通文件。本地删除路径也统一使用受限根操作。
- **验收标准：** 文件 symlink、父目录 symlink、路径替换竞争、FIFO 均不能返回 staging 外内容或阻塞 worker；正常大文件流式下载仍能关闭 fd，不能为了防逃逸整文件读入内存。

### P2-06：诊断读失败/内容不匹配后不清理 canary

- **位置：** `backend/internal/storage/s3.go:217`、`:223`、`:235`、`:238`。
- **问题：** 只有读成功且内容匹配才执行 DELETE。成功写入后遇到读权限不足、读取错误或内容不匹配，会遗留随机 canary；创建目的地失败时甚至没有 destination 记录来定位它。取消后也没有独立清理 context。诊断 namespace 的隔离已经具备，但失败清理未完成。
- **证据：** `TestReviewDiagnosticReadFailureLeavesCanary` 用实际 SDK 注入 GET 403，观察到 PUT=1、DELETE=0。
- **可执行修复：** 写成功后立即登记 defer 清理，用独立短超时 context；保留主错误，并将清理错误作为脱敏后的独立结果。对 PUT 响应不确定的情形也按自己生成的 canary key 尝试清理；版本化目的地按已知版本处理。
- **验收标准：** GET 403、读取中断、同大小错误内容及调用取消后均尝试删除；无权限删除时同时返回诊断与清理失败信息，不把 canary 混入备份对账结果。

## 崩溃点核对

以下表格是当前代码路径分析；本轮做了恢复状态探针，没有把它冒充为真实进程强杀/三桶集成测试。

| 崩溃位置 | 当前持久状态/副作用 | 不满足之处 |
| --- | --- | --- |
| intent 落库后、PUT 前 | uploading + 本地密文 | 重启只变 interrupted，不续传（P1-03）。 |
| multipart 中 | uploading + 未完成 upload ID | 没有持久真实 ID；defer 无法处理进程退出（P1-04）。 |
| 密文 PUT 后、manifest 前 | 上传对象存在、无提交标记 | 隔离是合理状态，但没有复用 artifact 的恢复执行路径（P1-03）。 |
| manifest 后、verify 前 | 提交标记存在、内容未经校验 | 发布顺序已违约（P1-02）。 |
| verify 失败、清理中 | 可能仅剩 manifest，或因删除失败两者都在 | 标记不能证明可用，且没有持久清理状态（P1-02/07）。 |
| verify 成功后、本地 remote commit 前 | 远端完整对象与标记，SQLite uploading | 重启不能补提交（P1-03）。 |
| remote commit 后、job success 前 | committed + running | 重启变 interrupted，任务状态不能幂等完成（P1-03）。 |
| 任一 DELETE 后、最终状态更新前 | committed 但部分/全部对象已删除 | 缺 deleting 和逐对象确认（P1-07）。 |

## 已确认的正确部分及边界

- `verifyRemote`（`upload.go:126`）确实流式读取到 EOF，对实际读取字节数及完整密文 SHA-256 比较；不信任 HEAD、ETag 或 metadata。本轮正向/反向探针确认同大小内容替换能被发现。问题在于它可被默认跳过、调用次序错误，以及 GET/下载未固定对象版本，而非哈希实现失效。
- 上传循环（`upload.go:66`）每次重新打开同一路径，没有调用 dumper，因此同进程三次 PUT 重试复用密文。manifest/校验/重启后的完整恢复重试尚未形成协议闭环。
- `List`（`s3.go:176`）使用 SDK paginator，循环正确。本轮真实 SDK 双页传输探针确认 continuation-token 被使用；现有 MinIO 测试并未证明 >1000 对象或版本列表分页。
- SecretKey 使用已有 AES-256-GCM、随机 nonce 加密（`destinations.go:106`；`crypto/crypto.go:22`）；DestinationView/API 不含 AccessKey/SecretKey。没有发现新增 SQL 拼接用户输入的注入路径。错误出口问题见 P1-08。
- 成功的 presigned URL 仅返回给已认证调用者；requestLogger 只记录请求路径，不记录响应体或 query（`server.go:215`），API guard 设置 no-store（`:293`）；生成下载响应的代码会关闭 io.ReadCloser（`api.gen.go:2334`）。未发现正常成功路径直接记录 bearer URL。
- Prefix 校验拒绝 NUL、`..` 及非允许字符，统一尾斜杠，键里的数据库/任务 ID 是数字，未发现通过正常 API 直接注入任意路径的证据。endpoint 正则排除 userinfo、路径、query，HTTP 自定义 endpoint 用于本地 MinIO；不能仅因允许内网地址就认定为未授权 SSRF。它仍不完整支持 IPv6、合法端口范围等，应增加边界测试；namespace 身份冲突是 P0-01 的独立问题。
- R2 默认 region=auto 与官方要求一致：[R2 S3 API compatibility](https://developers.cloudflare.com/r2/api/s3/api/)。B2 要求显式 region、R2/B2 要求 endpoint，作为基础配置合理；但共用 SDK 默认配置没有完成逐 provider checksum/版本/删除能力声明，不能据此宣称三桶均验收通过。
- 协议 D 当前按数据库×目的地查询，并跳过列表第一项；正常成功路径的“最新 committed 不删”已有单测。但 qualified anchor、活动引用、失败预算和删除恢复都缺失。beta 只有 full 模式，本轮不要求提前实现 v1.0 的结构/数据模式分域；将来增加恢复域配置时必须持久化域身份。
- Phase 3 第 6 项并非完全空白：已有 `db.Store.BackupNow`（`db/backup.go:20`）使用 VACUUM INTO，迁移前会调用；本次提交未扩展自身状态在线备份的运维入口/执行流程。应在 Phase 3 验收中明确可操作流程及主密钥/age 私钥独立保管说明，不能把迁移前快照当作日常状态备份已验收。

## 测试缺口与应补用例

| 现有函数/覆盖位置 | 缺口 | 具体补充目标 |
| --- | --- | --- |
| `TestUploadProtocolCCommittedPipeline`（`phase3_test.go:306`） | 注释称驱动真实 runJob，实际仅调用 uploadAndCommitRemote，既未用真实 S3，也未验证顺序。 | `TestRunJobRemoteCommitEndToEnd`、`TestRemoteCommitCrashMatrix`；断言 dumper 调用次数、阶段顺序、重启收敛。 |
| `TestUploadVerifyMismatchDeletesRemote`（`:362`） | 使用错误 wantSize/wantHash；没有启用 fake.corrupt，没有检查残留 manifest。 | `TestVerifyReadbackSameSizeCorruption`、`TestNoManifestBeforeVerification`、`TestReadFailureIsNotCorruption`，覆盖截断和缺 checksum。 |
| `TestUploadFailureRetainsArtifact`（`:397`） | 没有断言三次传输内容完全相同、没有模拟恢复/manifest 失败。 | `TestUploadRetryReusesArtifact`、`TestResumeUploadWithoutDump`、`TestManifestPutHTTPSeekable`。 |
| `TestRetentionPolicyKeepsAnchor`（`:432`） | 只有 keep=1 成功删除，seed 的 committed 行甚至没设置 remote_verified。 | `TestRetentionRequiresVerifiedAnchor`、`TestRetentionDeleteCrashMatrix`、`TestRetentionPartialDeleteResume`、`TestRetentionFailedArtifactBudget30`、`TestKeepDaysAndLocalRemotePolicies`。 |
| `TestReconcileClassification`（`:480`） | 只检查包含期望条目；对象数量不符仅 Log，不检查分类互斥/SQL 错误/并发。 | `TestReconcileDisjointCategories`、`TestReconcileConcurrentCommit`、`TestReconcileDiagnosticPrefix`、`TestDiscoverAfterSQLiteLoss`。 |
| `TestCreateDestinationEncryptedAndValidated`（`:257`） | 没有 API 默认值、解绑、软删除生命周期或并发 factory。 | `TestCreateDestinationReadbackDefault`、`TestAssignDestinationClear`、`TestDestinationLifecycleAtomicity`、`TestBuildBackendConcurrentFirstUse`。 |
| `TestMinIOEndToEnd`（`minio_test.go:97`） | 大对象只比长度，算出的 hash 没与输入比较；列表约 3 个对象；预签名只检查 URL 含 bucket；不查遗留 multipart。 | 比较完整 SHA-256；真实 >1000 个 key/多页；通过返回 URL 实际 GET；检查 ListMultipartUploads；取消/Complete 失败和 abort 失败注入。 |
| `Store.New/Put/Get/Delete`、provider 预设 | 没有 AWS S3、R2、B2 真桶矩阵，缺版本/锁定对象/权限和 SDK checksum 配置证据。 | `TestProviderChecksumMatrix`、`TestB2VersionRetention`、`TestVersionedS3DeleteAndLock`；记录真实 provider、SDK 版本、请求 checksum 设置和预期删除语义。 |
| `DownloadTask`、`GetTaskDownloadURL` 及新路由 | 没有 Phase 3 的认证/CSRF、bearer 日志、租约、symlink 和流中断回归。 | `TestPhase3DefaultDeny`、`TestDownloadLeaseBlocksPrune`、`TestDownloadContainedOpen`、`TestStorageErrorsRedactedAtAllSinks`。 |

上述测试名称是建议新增的回归测试，不是声称仓库中已有。provider 测试应固定可复核版本与环境；无法取得凭据时标 SKIP/未验收，不能靠 MinIO 的绿色结果代替。

## 本轮实际验证

环境为 `go1.26.0 linux/arm64`。默认 Go build cache 不可写，改用 `/tmp/sb-phase3-review-go-cache` 后继续。探针通过 Go overlay 注入包内，生产文件与既有测试均未编辑；SDK 测试使用自定义 HTTPClient，走真实 SDK 序列化/签名/分页逻辑，不监听端口，也不冒充 MinIO。

| 检查 | 结果 |
| --- | --- |
| `GOCACHE=/tmp/sb-phase3-review-go-cache go build ./backend/...` | 通过。 |
| 同一 GOCACHE 下 `go test -race ./backend/...` | 整体退出 1。jobs、storage、db 等已执行包通过；server 在首个 httptest listener 因 `socket: operation not permitted` 中断。不能写成全量通过。 |
| `go test ./backend/internal/storage -run TestMinIO -v` | TestMinIOEndToEnd 明确 SKIP；Docker socket 无权限，sudo 受 no-new-privileges 限制。本轮未做真实 MinIO/S3/R2/B2 验收。 |
| overlay 定向探针，`-race -count=1`，jobs/storage/server | 18 个顶层测试通过。其中多数断言的是缺陷可复现，另有 read-back 和 paginator 正向控制；不是修复后的验收通过。 |
| overlay `TestReviewConcurrentBackendFactory`，`-race -count=1` | 失败，race detector 指向 `destinations.go:230/231`；这是代码缺陷，与环境限制无关。 |

本地临时证据目录：`/tmp/sb-phase3-review/`。包含三个 `*_review_test.go`、`overlay.json`、`probes-race.log`、`race.log`、`minio.log` 等；这些是本轮工作区证据，不作为已入库的永久测试。

复现定向检查：

```sh
GOCACHE=/tmp/sb-phase3-review-go-cache go test -race -count=1 \
  -overlay=/tmp/sb-phase3-review/overlay.json \
  ./backend/internal/jobs ./backend/internal/storage ./backend/internal/server \
  -run 'TestReview(Manifest|Unchecked|Recovery|Clear|Delete|Partial|Remote|Local|Fresh|Reconcile|Multipart|NonSeekable|Diagnostic|Paginator|Canceled|Missing|Download|Readback)' -v

GOCACHE=/tmp/sb-phase3-review-go-cache go test -race -count=1 \
  -overlay=/tmp/sb-phase3-review/overlay.json \
  ./backend/internal/jobs -run '^TestReviewConcurrentBackendFactory$' -v
```

## 总体结论与最关键的三件事

**总体结论：未通过。** 这不是仅补文档或补几条测试即可关闭的评审：已有可复现的覆盖旧备份、提前提交、未验证锚点、multipart 遗留、协议恢复缺失及数据竞争。应完成实现修正，再按真实副作用崩溃矩阵和 provider 矩阵重新评审。

1. **先守住备份身份与锚点：** 使用不可复用 artifact 身份，禁止覆盖旧 namespace；强制完整性门槛，校验完成才发布 manifest，失败候选不能替换合格锚点。
2. **补齐 C/D 的持久状态机：** 重启复用原密文并幂等补提交；删除先进入 deleting，记录逐对象/版本结果，保护活动引用并独立重试清理，失败对象有宽限期和预算。
3. **把真实存储路径跑通并设门禁：** 修复 multipart 所有权/取消 abort、HTTP manifest reader、B2 version ID、错误脱敏和 factory race；加入真实 runJob、强杀恢复、分页/版本化及 S3/R2/B2 实测。
