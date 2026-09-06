# 桌面版本发布：私有源码、公开清单与 OSS 安装包

下载页与桌面更新器共同读取：

`https://www.nexorai.com.cn/token-dance/releases/stable.json`

源码仓库保持私有。清单只包含版本、平台、发布日期、更新说明，以及安装包的永久 HTTPS 地址、字节数、SHA-256。下载不要求网站登录或 GitHub 登录。当前仅发布 `windows-x64`。MySQL 保存全部版本历史；公共 JSON 仅保留每个平台当前版本，安装包保留在 OSS 的独立版本目录。

## 数据库存储

迁移 `0008_desktop_releases.sql` 通过现有服务端迁移命令执行，包含三张表：

| 表 | 保存内容 |
| --- | --- |
| `desktop_releases` | 不可变版本记录：平台、版本号、main 来源和完整提交 SHA、创建时间；`manifest_json` 存发布日期、说明、EXE/ZIP 地址、字节数及 SHA-256 |
| `desktop_release_channels` | 每个平台当前发布的版本 ID，外键关联版本历史 |
| `desktop_release_publication` | 数据库发布序号、已生成清单序号、生成时间，用于检查未完成的清单写入 |

版本号与平台组合唯一。同版本不能替换包、说明或构建来源。历史记录不会随着发新版被删除。当前没有管理后台、下架或强制客户端降级功能；不要直接修改历史 JSON 来替换已发布的包。

发布器需要专用数据库账号：`desktop_releases` 仅 SELECT/INSERT，另外两表仅 SELECT/INSERT/UPDATE；不授予用户数据表或 DDL 权限。迁移仍使用既有迁移身份。

部署任务通过受限文件 `TOKENDANCE_RELEASE_DB_CONFIG_FILE` 提供连接配置，JSON 字段为 `host`、`port`、`user`、`password`、`database`，可选 `unix_socket`、`ssl_ca`。连接本机 MySQL 可用 `127.0.0.1:3307`；非回环地址必须配置 CA 并校验服务端身份。配置文件只允许发布账号读取，不能进入仓库、OSS 或日志，密码不通过命令参数传入。生产与开发使用各自数据库、配置文件及清单目录。

## 首次切换（由云端 CI/CD 执行）

1. 合并到 `main`，按现有发布流程执行服务端迁移、部署下载页和 `deploy/nginx-token-dance.conf`。不要从功能分支部署。安装 Python 3.11+ 发布依赖：`python3 -m pip install -r tools/releases/requirements.txt`，并由受控配置提供 `TOKENDANCE_RELEASE_DB_CONFIG_FILE`。
2. 创建专门的公共元数据目录；它与应用版本目录、私有运行数据目录分离：

```sh
install -d -m 0755 /var/www/token-dance-releases
if [ ! -e /var/www/token-dance-releases/stable.json ]; then
  install -m 0644 deploy/desktop-releases.empty.json /var/www/token-dance-releases/stable.json
fi
nginx -t
```

3. Nginx 配置加载后，确认固定清单地址返回 JSON 和 `Cache-Control: no-store`。文件缺失应返回 404，不能回退为 SPA HTML。初始化空清单只用于尚未发布的状态，不能覆盖已有清单。
4. 发布切换后的桌面安装包，并执行下面的清单发布步骤。旧客户端把 GitHub 地址写死在二进制中，需要用户从下载页手动更新一次；此后可正常检查更新、后台下载和启动时安装。

应用部署不能复制、重置或删除 `/var/www/token-dance-releases/stable.json`。正常发版只需更新清单，不需要重新部署网站来修改版本号。

## 每次发布

1. 从干净的 `main` 构建，确认 `HEAD == origin/main`。Rust 包、Cargo.lock、Tauri 配置中的版本号保持一致。
2. 生成 `TokenDance.exe`、可选 ZIP、`build-info.json` 和 UTF-8 更新说明文件。`build-info.json` 沿用现有字段：`branch` 必须为 `main`，`commit` 是完整 40 位 SHA，`version` 与包版本一致，`sha256` 是 EXE 实际哈希。构建信息保留在内部发布记录中，不写进公共清单。
3. 用现有 OSS 上传工具或 CI 步骤上传到不可变路径，例如 `token-dance/desktop/<version>/windows-x64/TokenDance.exe`。同版本文件不覆盖。仅安装包所在的专用 bucket/前缀提供匿名读取；不要把用户头像、数据导出等私有对象所在的整个 bucket 改为公开。OSS 写入凭据只通过 CI 的受控环境提供。
4. 将这些本地构建产物交给云端发布任务，然后运行发布工具（Python 3.11+，需先配置数据库）：

```sh
python3 tools/releases/publish_manifest.py \
  --version "$RELEASE_VERSION" \
  --exe "$ARTIFACT_DIR/TokenDance.exe" \
  --exe-url "$PUBLIC_EXE_URL" \
  --zip "$ARTIFACT_DIR/TokenDance-windows-x64.zip" \
  --zip-url "$PUBLIC_ZIP_URL" \
  --build-info "$ARTIFACT_DIR/build-info.json" \
  --notes-file "$ARTIFACT_DIR/release-notes.txt" \
  --manifest /var/www/token-dance-releases/stable.json
```

ZIP 可不提供，但路径和 URL 必须成对提供。标记预览版时追加 `--prerelease`；版本号仍为三段数字，网页显示预览标记，客户端依照数值版本比较。当前不区分 stable/beta 更新通道。

`PUBLIC_EXE_URL` 和 `PUBLIC_ZIP_URL` 必须是永久、无凭据、无查询参数、无重定向的公开 HTTPS OSS/CDN 地址，不能使用会过期的预签名地址。HTTPS 默认端口为 443。发布工具没有 OSS 上传权限，也不需要 OSS 凭据。

工具核对 Windows x64 PE 头、构建来源信息、EXE/ZIP 一致性，并匿名下载 OSS 上的完整文件比对字节数及 SHA-256。全部验证成功后，才在 MySQL 事务内保存版本、切换当前版本、增加发布序号；事务提交后，通过同目录临时文件和原子替换生成清单。数据库会话锁覆盖提交和文件生成，串行处理并发发布；进程退出后自动释放。工具拒绝版本倒退和同版本修改。

5. 从外部网络验证固定清单地址与下载页，再用已切换的旧版本客户端验证“检查更新 → 下载 → 校验 → 重启后版本变化”。离线时保留现有客户端，不影响本地采集。首次切换的跨版本验收必须使用真实发布包，不能把旧二进制标成新版本。

## 数据库与清单的一致性和恢复

MySQL 是版本记录的唯一依据，JSON 是可重建的读取副本。事务失败不会发布版本；事务提交后写文件失败，旧清单继续服务，数据库保留新版本且 `revision > rendered_revision`。这时发布命令非零退出，CI 必须保留失败状态并告警，不能把它当成功。

修复磁盘或权限后，重试同一发布命令可恢复；也可以不重新上传包，仅从数据库重建：

```sh
python3 tools/releases/publish_manifest.py --reconcile \
  --manifest /var/www/token-dance-releases/stable.json
```

由发布 CI 的重试任务及服务器现有任务调度每分钟执行一次上述恢复命令。首次没有任何发布记录时恢复命令会报错，应在首次发版后启用。该命令即使序号相同也重新生成文件，用于修复意外删除或文件损坏。数据库不可用时不覆盖现有清单。日常客户端下载和更新检查不查询 MySQL。

同一环境必须只有一个目标清单路径，所有发布与恢复任务都使用同一 MySQL 主库和这套发布器；不要运行旧版直接写 JSON 的脚本。多 Web 节点需统一共享清单存储或由额外分发流程确认各节点，当前实现不提供跨节点文件分发。已有 JSON 不自动导入数据库：如上线前已有版本清单，需要用原始安装包和可信 build-info 重新登记，不能凭文件猜测来源提交。首次发布前确认不会让客户端收到低于已发布版本的清单。

## 清单契约与校验

格式见 `schemas/desktop-release-manifest.schema.json`。`schemas/fixtures/desktop-release-manifest.json` 是前端、原生端和发布工具测试共用的示例，包含虚构下载域名和微型测试数据，禁止直接作为线上清单发布。

客户端信任固定的第一方 HTTPS 清单，按其记录访问 OSS/CDN 地址，保留下载大小、SHA-256、PE 标记检查及安装前重新验证。禁止 HTTP、URL 凭据、IP/本地主机、临时签名链接及下载重定向。SHA-256 用于确认与可信清单一致，不替代代码签名；修改公共清单的权限等同于发布软件的权限。

```sh
python tools/releases/test_publish_manifest.py
# 启动并清理独立的本机 MySQL 测试容器，需 Docker 和 Go，不访问云端数据库
python tools/releases/test_release_registry.py
npm --prefix web run typecheck
npm --prefix web test -- src/test/download-docs.test.tsx src/test/navigation.test.tsx
cargo test --locked --manifest-path collector/apps/desktop/src-tauri/Cargo.toml updates::tests -- --test-threads=1
```
