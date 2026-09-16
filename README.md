# zlib

一个 Go 单二进制 CLI，把 `todo_list/` 下两个 zlib 相关项目的能力合并成一条命令。

**范围**：登录 · 搜索 · 下载。零运行时依赖，不需要 Python、Node、uv 或任何外部二进制。

```bash
zlib login                                  # 登录（token 缓存，只跑一次）
zlib search "deep learning" --limit 5       # 搜索
zlib download 12345/abc123def -o ~/Books    # 下载
zlib doctor                                 # 体检：凭据、连通性、代理
```

---

## 为什么要合并

原本有两个项目，能力互补但各有硬伤：

| | `zlib-download-skill` | `zlibrary-mcp` | `zlib-go`（本项目） |
|---|---|---|---|
| 形态 | Claude Code skill 插件 | MCP server | 独立 CLI |
| 运行依赖 | Python 3 + `requests` | Node 22+ + Python 3.10+ + uv | **无** |
| 系统依赖 | **需外装 `annas-mcp` 二进制** | — | **无** |
| 搜索 | 1 个（关键词） | 7 个 | 1 个（带完整过滤） |
| 数据源 | Z-Library + Anna's Archive | Z-Library + LibGen + Anna's | Z-Library + Anna's Archive |
| 下载 | ✅ | ✅ 多源 failover | ✅ |

两个硬伤决定了合并方向：

1. **`annas-mcp` 外部二进制依赖**：skill 版的 Anna's Archive 后端靠 `subprocess` 调一个第三方 Go 二进制，再**解析它的人类可读文本输出**（`Title:` / `Authors:` 逐行抠）。既是额外的安装步骤，也是随时会碎的契约。本项目把 Anna's Archive 改成原生 HTTP 实现（HTML 刮取 + fast-download JSON API），依赖消失。
2. **运行时依赖链**：MCP 版工程质量很高（13 个 tool、完整 ADR、TDD 基建），但要跑起来得先装 Node 22 + Python 3.10 + uv 再 `uv sync`，规模也重（`uv.lock` 645 KB）。

保留下来的设计决策（来自两个源项目的实战结论）：

- **EAPI 域名探测**：用 `/eapi/info/domains` 探活，**绝不**用 `/eapi/user/login` 探测——登录接口限流约 10 次/小时/IP，用它探活会锁死真实凭据。
- **DiamWall 反爬识别**：307 自跳转、403/513/517、响应体含 `diamwall` 都归类为"反爬墙"，因为处理方式是**换域名**而不是重试。
- **搜索先行**：只有关键词一个入口，没有"按 ID 直接下载"的路径。
- **下载写入原子化**：先落临时文件，传输完成且非空后才 rename 到位。空响应视为失败——配额耗尽时站点回 HTTP 200 + 0 字节。
- **文件名安全**：`Content-Disposition` 由服务端控制，一律降到 basename 再拼接，两种路径分隔符都处理。

## 安装

```bash
cd zlib-go
make build          # 产出 ./zlib
make install        # 装到 /usr/local/bin/zlib
```

要求 Go 1.26+（仅编译期）。唯一的第三方依赖是 `golang.org/x/net/html`，用于解析 Anna's Archive 的搜索结果页。

## 配置

优先级：**命令行 flag > 环境变量 > 配置文件**。

配置文件 `~/.config/zlib/config.json`（权限 0600）：

```json
{
  "zlib":  { "email": "...", "password": "...", "remix_userid": "...", "remix_userkey": "..." },
  "annas": { "secret_key": "...", "base_url": "https://annas-archive.gl" },
  "download_dir": "~/Downloads",
  "default_source": "auto",
  "proxy": "socks5://127.0.0.1:1080"
}
```

环境变量（沿用 `zlibrary-mcp` 的命名，已配过的可以不改）：

| 变量 | 作用 |
|---|---|
| `ZLIBRARY_EMAIL` / `ZLIBRARY_PASSWORD` | Z-Library 账号 |
| `ZLIBRARY_EAPI_DOMAIN` | 钉死 EAPI 域名，跳过自动探测 |
| `ANNAS_SECRET_KEY` | Anna's Archive 下载密钥（搜索不需要） |
| `ANNAS_BASE_URL` | Anna's Archive 镜像 |
| `ZLIB_DOWNLOAD_DIR` | 下载目录 |
| `ZLIB_SOURCE` | 默认源：`auto` / `zlib` / `annas` |
| `ZLIB_PROXY` | 代理；也认标准的 `HTTPS_PROXY` / `ALL_PROXY` |
| `ZLIB_CONFIG_DIR` | 配置目录（默认 `~/.config/zlib`），便于隔离测试 |

## 命令

### `login` — 登录

```bash
zlib login --email you@example.com --password '...'
# 或先存凭据再登录
zlib config set --zlib-email you@example.com --zlib-password '...'
zlib login
```

登录成功后把 `remix_userid` / `remix_userkey` 缓存进配置文件，后续命令**不再碰登录接口**。这一点很关键：Z-Library 登录限流约 10 次/小时/IP，每次命令都登录会在几次操作内锁掉账号。

登录时会顺带报告今天的剩余下载配额。

### `search` — 搜索

```bash
zlib search "machine learning" --limit 10
zlib search "莱姆 索利斯" --source zlib --lang chinese --ext pdf
zlib search "reinforcement learning" --source annas
zlib --json search "deep learning" | jq '.books[].title'
```

`--source auto`（默认）先试 Z-Library，失败再试 Anna's Archive——后者**搜索不需要 API key**，所以没凭据也能用。哪个源失败会记在 `errors` 里，不会整体中断。

过滤参数：`--limit` `--page` `--lang` `--ext` `--year-from` `--year-to` `--exact` `--order`。`--lang` 与 `--ext` 支持逗号分隔多值。

### `download` — 下载

```bash
zlib download 12345/abc123def -o ~/Books        # Z-Library：id/hash，可从搜索结果直接复制
zlib download a1b2c3d4e5f6 --source annas      # Anna's Archive：裸 MD5
zlib download 12345/abc123def --name "自定义书名.pdf"
```

标识符的形状决定源：带 `/` 的是 Z-Library 的 `id/hash`，裸哈希只能来自 MD5 系源。下载到临时文件后原子 rename，中断不会留下半截文件。

### 辅助命令

```bash
zlib info 12345/abc123   # 完整元数据（原名、作者、出版社、ISBN、页数、简介）
zlib quota               # 今天还剩多少次下载
zlib config show|set|reset
zlib doctor              # 体检
```

### 关于旗标位置

子命令的旗标**可以写在位置参数之后**，两种写法等价：

```bash
zlib search "deep learning" --limit 5     # 推荐
zlib search --limit 5 "deep learning"     # 同样可用
```

（Go 标准库的 `flag` 遇到第一个位置参数就停止解析，所以这里做了参数重排。不加这层，`search "query" --limit 5` 里的 `--limit` 会被**静默忽略**。）

全局旗标 `--json` / `--verbose` / `--no-color` / `--proxy` 必须写在**子命令之前**，写错时会给出明确提示：

```bash
zlib --json doctor      # ✓
zlib doctor --json      # ✗ → HINT: Global options go before the command name
```

## 网络限制与代理

这类站点在部分网络环境下不可达，`zlib doctor` 会做**分阶段诊断**并指出卡在哪一步：

```
! Z-Library EAPI domains   z-library.ec TCP connect ... timed out — the traffic is being dropped
                           (DNS resolved to 162.125.1.8, so this is a network-level block)
                           z-library.sk TLS handshake failed with a certificate error
                           (certificate is valid for *.facebook.com, not z-library.sk)
! Anna's Archive search    HTTP 403 — DDoS-Guard browser challenge, which a plain
                           HTTP client cannot pass
```

诊断分 DNS → TCP → TLS → HTTP 四段，因为 `net/http` 把这四种失败都报成一个笼统的 "request failed"，而它们的处理方式完全不同：

- **DNS 解析成功但 TCP 超时** → 流量被丢，需要代理
- **TLS 证书域名不匹配** → DNS 污染（解析到了别的站点）
- **HTTP 403 + DDoS-Guard 页面** → 浏览器 JS 挑战，命令行客户端过不去

配置代理：

```bash
zlib config set --proxy socks5://127.0.0.1:1080
# 或一次性：zlib --proxy socks5://127.0.0.1:1080 search "..."
# 或：export HTTPS_PROXY=socks5://127.0.0.1:1080
```

支持 `http` / `https` / `socks5` 代理。

**关于 Anna's Archive**：站点部署了 DDoS-Guard，会返回一个需要执行 JavaScript 的挑战页。这不是凭据问题，普通 HTTP 客户端无法通过——需要代理，或换网络。Z-Library 的 EAPI 没有这层挑战（但常被网络层阻断）。

## 架构

```
main.go
cmd/                    子命令分发（手写 switch，不引 CLI 框架）
├── root.go             全局旗标、参数重排、退出码约定
├── search.go           search
├── download.go         download + 标识符解析
├── info.go             info
├── config.go           config / login / quota
├── doctor.go           分阶段连通性诊断
└── clients.go          客户端构造 + token 缓存策略
internal/
├── fetch/              共享 HTTP 层：受限读取、JSON 取值、文件名安全、原子下载、代理与诊断
├── zlib/               Z-Library EAPI 客户端
│   ├── domain.go       域名探测与选择、DiamWall 识别
│   ├── client.go       登录 / 搜索 / 详情 / 下载链接 / 配额
│   └── errors.go       ErrDomainWalled 等分类错误
├── annas/              Anna's Archive（原生实现）
│   ├── annas.go        搜索刮取 + fast-download API
│   └── html.go         无依赖的 DOM 遍历 + 元数据条解析
├── ui/                 表格（CJK 宽度）/ 颜色
├── model/              跨源统一的 Book
└── config/             文件 + 环境变量合并
```

退出码：`0` 成功 · `1` 运行失败 · `2` 用法错误。

几个刻意的取舍：

- **表格按显示宽度对齐**，不是按 rune 数。中文标题每字占两列，按 rune 算会让所有含中文的行错位。
- **元数据条按模式匹配，绝不按位置**。Anna's Archive 的 `·` 分隔条各段可选且顺序不保证（约 9% 的记录没有年份），按下标解析会把年份错当成扩展名。
- **stdout 只放结果**，进度信息一律走 stderr，这样管道和 `jq` 不受干扰。

## 测试

```bash
make test        # 全量
make check       # vet + gofmt + test
make smoke       # 端到端冒烟（隔离配置目录，不碰网络）
```

覆盖的核心行为：

- Anna's Archive 解析跑在**真实抓取的搜索结果页**上（`internal/annas/testdata/`），包括那条故意缺年份的记录，以及内联 JS 里的 `·` 分隔符陷阱
- EAPI 全流程用 `httptest` 模拟：登录换 token、`languages[]` 数组编码、下载链接解析、配额算式
- DiamWall 五种信号（307/403/513/517 + 200 带挑战页）都断到 `ErrDomainWalled`
- 空响应下载被拒（配额耗尽的真实表现），临时文件不残留
- 文件名路径穿越防护（`../../etc/passwd`、`..\..\Windows\system32`、`C:\Windows\...`）
- 配置优先级、0600 权限、脱敏输出
- 参数重排：旗标写在位置参数之后仍生效、`--flag=value` 不被拆开、`--` 分隔符正确传递、布尔旗标不吃掉下一个参数
- 表格 CJK 对齐、用法错误的退出码区分

## 与源项目的关系

两个源项目**原样保留**在 `todo_list/` 下，未做任何改动。本项目是独立实现，只在两处与它们有硬关联：

1. `internal/annas/testdata/search_results.html` —— 从 `zlibrary-mcp` 复制来的真实页面 fixture（注释里保留了原始出处说明）
2. 设计决策与命名约定（EAPI 域名列表、错误分类、环境变量名）参照两个项目的既有实现

## 不在范围内

以下能力存在于源项目或相关工具中，本项目**有意不做**：

- **RAG 文本提取**（EPUB/PDF → Markdown）：`zlibrary-mcp` 有，但需要 PDF/OCR 依赖链，会破坏"单二进制、零依赖"的定位
- **NotebookLM 上传**：[zlibrary-to-notebooklm](https://github.com/zstmfhy/zlibrary-to-notebooklm) 有，需要 Playwright + `notebooklm` CLI
- **浏览器自动化下载**：上述项目用 Playwright 绕过反爬，代价是引入 Node + Python 运行时；本项目靠代理解决网络层问题，保持二进制自足

## 已知限制

- Anna's Archive 的 DDoS-Guard 挑战无法用纯 HTTP 客户端通过，必须借助代理或换网络
- 不支持 LibGen（`zlibrary-mcp` 有，免账号且有镜像 failover；可作为后续源接入，`internal/fetch` 与 `model.Book` 已为其预留形态）
- Z-Library 免费账号每天约 10 次下载，用 `zlib quota` 查
- 搜索只有关键词入口，没有 MCP 版的全文检索 / 按作者 / 模糊匹配等高级检索
- 登录限流 10 次/小时/IP：`config reset` 后需要重新 `login`，短时间反复重置会触发限流
