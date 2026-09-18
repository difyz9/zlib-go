# zlib

[![CI](https://github.com/difyz9/zlib-go/actions/workflows/ci.yml/badge.svg)](https://github.com/difyz9/zlib-go/actions/workflows/ci.yml)
[![Release](https://github.com/difyz9/zlib-go/actions/workflows/release.yml/badge.svg)](https://github.com/difyz9/zlib-go/actions/workflows/release.yml)

一个 Go 单二进制 CLI，用于从 Z-Library 和 Anna's Archive 搜索、下载电子书。

**范围**：登录 · 搜索 · 下载。零运行时依赖——不需要 Python、Node 或任何外部二进制；单文件交叉编译，开箱即用。

## 微信

<img src="img/20250918172120_11_359.jpg" alt="我的微信二维码" width="180">

```bash
zlib login                                  # 登录（token 缓存，只跑一次）
zlib search "deep learning" --limit 5       # 搜索，每行结果带下载标识符
zlib download 12345/abc123def -o ~/Books    # 下载（带实时进度条）
zlib doctor                                 # 体检：凭据、连通性、代理
```

## 特性

- **双数据源自动切换**：Z-Library（EAPI，需账号）优先，失败自动回落 Anna's Archive（搜索无需任何凭据）
- **跨平台单二进制**：linux / macOS / Windows × amd64 / arm64 预编译发布，附 sha256 校验
- **下载进度条**：实时百分比、速率与大小；非终端环境自动关闭，脚本输出不受干扰
- **原子写入**：下载先落隐藏临时文件，传输完成且非空后才 rename，中断不留半截文件
- **CJK 友好的表格输出**：按显示宽度（非 rune 数）对齐，中文标题不错列
- **结构化 JSON 输出**：`--json` 供 `jq` 与脚本消费
- **分阶段网络诊断**：`zlib doctor` 区分 DNS / TCP / TLS / HTTP 四类失败并给出对应建议

## 安装

**Homebrew（macOS / Linux）：**

```bash
brew tap difyz9/tap https://github.com/difyz9/homebrew-tap
brew install difyz9/tap/zlib
```

新版本发布后：`brew update && brew upgrade difyz9/tap/zlib`。

**从 Releases 下载预编译二进制**（linux / macOS / Windows × amd64 / arm64，附 sha256）：

```bash
curl -LO "https://github.com/difyz9/zlib-go/releases/latest/download/zlib-$(uname -s | tr '[:upper:]' '[:lower:]')-$(case $(uname -m) in x86_64) echo amd64;; aarch64|arm64) echo arm64;; *) uname -m;; esac).tar.gz"
```

**源码构建**（要求 Go 1.26+，唯一的第三方依赖是 `golang.org/x/net/html`）：

```bash
make build          # 产出 ./zlib
make install        # 装到 /usr/local/bin/zlib
```

## 快速开始

```bash
# 1. 配置 Z-Library 凭据（只需一次；token 会被缓存）
zlib config set --zlib-email you@example.com --zlib-password '...'
zlib login

# 2. 搜索
zlib search "machine learning" --limit 10

# 3. 从结果的 Download ID 列复制标识符下载
zlib download 3647669/945552 -o ~/Books
```

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

环境变量：

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

登录成功后把 `remix_userid` / `remix_userkey` 缓存进配置文件，后续命令**不再碰登录接口**。这一点很关键：Z-Library 登录接口限流约 10 次/小时/IP，每次命令都登录会在几次操作内锁掉账号。

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

结果表格的最后一列 **Download ID** 就是每行结果的下载标识符——Z-Library 结果为 `id/hash`，Anna's Archive 结果为 MD5。想下载哪本，复制那一行的 ID 直接交给 `download`：

```
#  Title                        Author          Year  Lang  Fmt   Size     Download ID
1  Deep Learning with Python…   Jason Brownlee  2016  en    pdf   4.64 MB  3647669/945552
2  Deep Learning for Finance…   Sofien Kaabar   2024  en    pdf   8.72 MB  27434353/69152a
```

`--json` 模式下对应每个 book 的 `id` + `hash`（或 `hash`）字段。

### `download` — 下载

```bash
zlib download 12345/abc123def -o ~/Books        # Z-Library：id/hash，从搜索结果 Download ID 列直接复制
zlib download a1b2c3d4e5f6 --source annas      # Anna's Archive：裸 MD5
zlib download 12345/abc123def --name "自定义书名.pdf"
```

标识符的形状决定源：带 `/` 的是 Z-Library 的 `id/hash`，裸哈希只能来自 MD5 系源。下载先落临时文件、原子 rename，中断不会留下半截文件；空响应（配额耗尽时站点的典型表现）视为失败而非成功。

交互式终端上显示**实时进度条**（帧率节流到约 10fps）：

```
Downloading  [======================]   83%  7.2 MB / 8.7 MB  1.5 MB/s
```

服务器未返回 Content-Length 时降级为只显示已下载字节数和速率。进度条只在 stderr 为终端时启用：`--json` 模式或输出重定向到管道/文件时自动关闭，脚本和日志不受 `\r` 刷新干扰。

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
zlib config set --proxy 'socks5://user:password@154.222.46.2:62813'  # 带认证的 SOCKS5
# 或一次性：zlib --proxy socks5://127.0.0.1:1080 search "..."
# 或一次性：zlib --proxy 'socks5://user:password@154.222.46.2:62813' search "..."
# 或：export HTTPS_PROXY=socks5://127.0.0.1:1080
```

支持 `http` / `https` / `socks5` 代理。

带账号密码的代理请写成标准 URL 形式：`socks5://用户名:密码@服务器地址:端口`。例如 SOCKS5 代理服务器 `154.222.46.2:62813`，账号 `user`，密码 `password`，对应为 `socks5://user:password@154.222.46.2:62813`。命令行一次性 `--proxy` 优先级最高，适合临时测试；确认可用后再写入配置文件。

**关于 Anna's Archive**：站点部署了 DDoS-Guard，会返回一个需要执行 JavaScript 的挑战页。这不是凭据问题，纯 HTTP 客户端无法通过——需要代理或换网络。Z-Library 的 EAPI 没有这层挑战（但常被网络层阻断）。

## 设计决策

这些是踩过坑之后固化下来的行为，改动前请先了解背后的原因：

- **EAPI 域名探测用 `/eapi/info/domains`，绝不用 `/eapi/user/login`**——登录接口限流约 10 次/小时/IP，用它探活会锁死真实凭据。域名按存活与被墙状态自动选择，`ZLIBRARY_EAPI_DOMAIN` 可钉死。
- **DiamWall 反爬统一识别**：307 自跳转、403/513/517、响应体含 `diamwall` 都归类为"反爬墙"，因为正确的处理方式是**换域名**而不是重试。
- **下载写入原子化**：先落临时文件，传输完成且非空后才 rename 到位。空响应视为失败——配额耗尽时站点回 HTTP 200 + 0 字节，直接落盘会留下一个"看似成功"的空文件。
- **文件名安全**：`Content-Disposition` 由服务端控制，一律降到 basename 再拼接，两种路径分隔符都处理，防止路径穿越。
- **stdout 只放结果**：进度信息一律走 stderr，管道和 `jq` 不受干扰。进度条同理，只在 stderr 是终端时渲染。
- **表格按显示宽度对齐**，不是按 rune 数：中文标题每字占两列，按 rune 算会让所有含中文的行错位。
- **Anna's Archive 元数据按模式匹配，绝不按位置**：`·` 分隔条各段可选且顺序不保证（约 9% 的记录没有年份），按下标解析会把年份错当成扩展名。

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
├── annas/              Anna's Archive 客户端
│   ├── annas.go        搜索刮取 + fast-download API
│   └── html.go         无依赖的 DOM 遍历 + 元数据条解析
├── ui/                 表格（CJK 宽度）/ 颜色 / 下载进度条
├── model/              跨源统一的 Book
└── config/             文件 + 环境变量合并
```

退出码：`0` 成功 · `1` 运行失败 · `2` 用法错误。

## 测试与发布

```bash
make test        # 全量
make check       # vet + gofmt + test
make smoke       # 端到端冒烟（隔离配置目录，不碰网络）
```

同样的检查跑在 GitHub Actions 上（`.github/workflows/`）：

- **CI**：每次 push / PR，在 Linux、macOS、Windows 三平台跑检查
- **Release**：推 tag 触发——跑完整检查、交叉编译六个目标（linux / darwin / windows × amd64 / arm64，CGO 关闭，静态链接，tag 通过 ldflags 注入为版本号）、打 tar.gz 并附 sha256，自动创建 GitHub Release 并上传产物

发布新版本只需打 tag 推送，版本号自动跟随 tag：

```bash
git tag v0.2.0 && git push origin v0.2.0
```

本地 `make build` 的版本号取 `git describe`（如 `v0.1.0-3-g1a2b3c`），未注入时显示 `dev`。

覆盖的核心行为：

- Anna's Archive 解析跑在**真实抓取的搜索结果页**上（`internal/annas/testdata/`），包括缺年份的记录和内联 JS 里的 `·` 分隔符陷阱
- EAPI 全流程用 `httptest` 模拟：登录换 token、`languages[]` 数组编码、下载链接解析、配额算式
- DiamWall 五种信号（307/403/513/517 + 200 带挑战页）都断到 `ErrDomainWalled`
- 空响应下载被拒（配额耗尽的真实表现），临时文件不残留
- 文件名路径穿越防护（`../../etc/passwd`、`..\..\Windows\system32`、`C:\Windows\...`）
- 配置优先级、0600 权限、脱敏输出
- 参数重排：旗标写在位置参数之后仍生效、`--flag=value` 不被拆开、`--` 分隔符正确传递、布尔旗标不吃掉下一个参数
- 表格 CJK 对齐、进度条帧率节流与未知总大小降级、用法错误的退出码区分

## 已知限制

- Anna's Archive 的 DDoS-Guard 挑战无法用纯 HTTP 客户端通过，必须借助代理或换网络
- 暂不支持 LibGen（`internal/fetch` 与 `model.Book` 已为其预留形态，可作为后续数据源接入）
- Z-Library 免费账号每天约 10 次下载，`zlib quota` 可查
- 搜索为关键词入口；按作者、模糊匹配等高级检索暂不在范围内
- 登录限流 10 次/小时/IP：`config reset` 后需要重新 `login`，短时间反复重置会触发限流

## License

MIT
