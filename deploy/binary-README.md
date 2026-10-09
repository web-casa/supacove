# supabackup（SupaCove）独立二进制

单文件自包含：Web 控制台、SQLite 控制面数据库、迁移与调度器全部内嵌，
无需 Docker。同一份发布标签还提供多架构容器镜像
`ghcr.io/web-casa/supabackup`（自带 pg_dump 客户端矩阵 14–18，无需宿主机
安装）；两者功能一致，二进制方式才需要按下文准备本机 `pg_dump`。

## 运行

```bash
./supabackup serve            # 监听 :8080，数据写入 ./data
./supabackup bootstrap        # 打印一次性管理员初始化 token
./supabackup version          # 构建信息
```

常用环境变量（完整表见项目文档 docs/deployment.md）：`SB_ADDR`、
`SB_DATA_DIR`、`SB_PUBLIC_ORIGIN`、`SB_TRUSTED_PROXIES`、`SB_LOG_LEVEL`。

## pg_dump 前提（唯一的外部依赖）

备份内核调用本机 `pg_dump`（客户端矩阵 14–18，自动匹配源库 major，
要求客户端 major ≥ 源库 major）。发现顺序：`$PATH` →
`/usr/lib/postgresql/*/bin`。

- Debian/Ubuntu：`apt install postgresql-client-17`；配置 PGDG apt 源
  可安装完整版本矩阵
- macOS：`brew install postgresql@17`，并把
  `/opt/homebrew/opt/postgresql@17/bin` 加入 `PATH`（该 formula 默认
  keg-only）

可选的恢复验证（默认关闭）另需 PostgreSQL **服务端**二进制目录
（`SB_VERIFY_PGBIN`）。恢复端工具（`age`、`pg_restore`、`psql`）由恢复
套件的 `restore.sh` 在恢复机上使用，本机无需安装。

反代 HTTPS 与 cookie 要求、systemd 示例、升级步骤：
[docs/deployment.md](https://github.com/web-casa/supacove/blob/main/docs/deployment.md)。

项目：<https://github.com/web-casa/supacove> ·
文档站：<https://supacove.com> · 许可证：AGPL-3.0（见随包 LICENSE）
