# zlib-go —— Z-Library / Anna's Archive 电子书搜索与下载技能

**一句话**：让 AI 智能体能够调用 `zlib` 单二进制 CLI，在 Z-Library（EAPI，需账号）与 Anna's Archive（搜索无需凭据）之间自动切换，搜索、查看元信息、下载电子书，并管理多账号配额与网络诊断。

**项目**：`github.com/difyz9/zlib-go`（Go 编写，零运行时依赖，单文件交叉编译）

## 1. 适用场景

- 关键词搜索电子书，获取书名、作者、年份、语言、格式、大小、**Download ID**
- 根据 Download ID（Z-Library `id/hash`）或 MD5（Anna's Archive）下载书籍到指定目录
- 查看书籍完整元数据（原名、作者、出版社、ISBN、页数、简介）
- 多账号管理与每日配额（免费账号约 10 次/天）自动轮换
- 网络体检（DNS → TCP → TLS → HTTP 分阶段诊断）与代理配置

## 2. 前置条件

1. 已安装 `zlib` 二进制并位于 `$PATH`：
   - Homebrew：`brew tap difyz9/tap https://github.com/difyz9/homebrew-tap && brew install difyz9/tap/zlib`
   - 或从 GitHub Releases 下载预编译二进制（linux/darwin/windows × amd64/arm64）
   - 或源码构建：`cd zlib-go && make build`（要求 Go 1.26+）
2. 目标站点网络可达；部分网络需要代理（见 §6）
3. Z-Library 需要账号（`zlib account add`），Anna's Archive **搜索无需凭据**，下载需要 `ANNAS_SECRET_KEY`

## 3. 智能体调用约定

- 通过 shell 执行 `zlib` 子命令，全部通过标准输入/输出交互。
- **机器可读输出**：在子命名前加全局 `--json`（如 `zlib --json search "..."`），stdout 输出 JSON，供智能体解析；进度与日志走 stderr，不干扰管道。
- **全局旗标必须写在子命令之前**：`zlib --json search "q"`（✓），`zlib search "q" --json`（✗，会报错并提示）。
- 子命令旗标可写在位置参数之后：`zlib search "deep learning" --limit 5`。
- 退出码：`0` 成功、`1` 运行失败、`2` 用法错误。

## 4. 子命令参考

### 4.1 search —— 搜索

```bash
zlib [--json] search "关键词" [--limit N] [--source auto|zlib|annas] \
     [--lang 语言] [--ext 格式] [--year-from Y] [--year-to Y] \
     [--exact] [--order 排序] [--no-pager]
```

示例：
```bash
zlib --json search "machine learning" --limit 5
zlib search "莱姆 索利斯" --source zlib --lang chinese --ext pdf
zlib search "reinforcement learning" --source annas
```

关键字段：
- `books[].id` / `books[].hash`：Download ID，Z-Library 形如 `3647669/945552`，Anna's Archive 为 MD5
- `books[].title`、`author`、`year`、`lang`、`ext`、`size`
- `errors[]`：各源失败原因，不会整体中断

> 交互式终端下首次输出后进入分页（`n`/`p`/页码/`q`）；管道、`--json`、`--no-pager` 下关闭分页。

### 4.2 download —— 下载

```bash
zlib [--json] download <下载标识符> [-o 目录] [--name "自定义书名.pdf"] [--source annas]
```

示例：
```bash
zlib download 3647669/945552 -o ~/Books
zlib download a1b2c3d4e5f6 --source annas
zlib download 12345/abc123def --name "深度学习.pdf"
```

行为：
- 带 `/` 的标识符当作 Z-Library `id/hash`；裸哈希当作 Anna's Archive MD5
- 先写隐藏临时文件，传输完成且非空后原子 rename，中断不留半截文件
- 终端 stderr 显示实时进度条；非终端/管道自动关闭
- 空响应（配额耗尽站点回 HTTP 200 + 0 字节）视为失败，不落盘空文件
- 多账号自动轮换：配额耗尽时自动切换到还有余额的账号重试

### 4.3 info —— 元数据

```bash
zlib [--json] info 12345/abc123
```

返回完整元数据：原名、作者、出版社、ISBN、页数、简介等。

### 4.4 account —— 多账号管理

```bash
zlib account add --email a@x.com --password '...'        # 新增/更新账号（登录并缓存 token）
zlib account list                                        # 列表，* 标记 active
zlib account use b@x.com                                 # 切换 active 账号
zlib account remove b@x.com                              # 删除账号
```

> 登录接口限流约 **10 次/小时/IP**，token 登录后缓存，后续命令不再碰登录接口。**不要在脚本里对每个命令都先 `login` 或 `account add`**。

### 4.5 quota —— 配额查询

```bash
zlib quota        # 当前账号今天还剩多少次下载
zlib quota --all  # 所有账号的今日配额一览
```

### 4.6 config —— 配置

```bash
zlib config show                         # 显示配置（账号脱敏）
zlib config set --proxy socks5://127.0.0.1:1080
zlib config reset
```

配置文件：`~/.config/zlib/config.json`（权限 0600）。优先级：**命令行 flag > 环境变量 > 配置文件**。

### 4.7 doctor —— 网络体检

```bash
zlib [--verbose] doctor
```

分 DNS → TCP → TLS → HTTP 四段诊断，指出卡在哪一步（流量被丢/ DNS 污染/ DDoS-Guard 挑战 等），并给建议。

### 4.8 login —— 单次登录（不推荐常规使用）

```bash
zlib login --email you@example.com --password '...'
```

优先使用 `account add`（支持多账号与 token 缓存）。

## 5. 环境变量（可选）

| 变量 | 作用 |
|---|---|
| `ZLIBRARY_EMAIL` / `ZLIBRARY_PASSWORD` | Z-Library 账号（优先于配置文件） |
| `ZLIBRARY_EAPI_DOMAIN` | 钉死 EAPI 域名，跳过自动探测 |
| `ANNAS_SECRET_KEY` | Anna's Archive 下载密钥（搜索不需要） |
| `ANNAS_BASE_URL` | Anna's Archive 镜像 |
| `ZLIB_DOWNLOAD_DIR` | 默认下载目录 |
| `ZLIB_SOURCE` | 默认源：`auto` / `zlib` / `annas` |
| `ZLIB_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` | 代理，支持 `http`/`https`/`socks5` |
| `ZLIB_CONFIG_DIR` | 配置目录（默认 `~/.config/zlib`） |

带账号密码的代理写法：`socks5://用户名:密码@服务器:端口`

## 6. 注意事项与限制（智能体必读）

1. **登录限流**：Z-Library 登录接口约 10 次/小时/IP。短时间反复 `config reset` / `account add` / `login` 会触发限流锁账号。多账号登录要错峰。
2. **Anna's Archive DDoS-Guard**：搜索页部署了需要执行 JavaScript 的挑战，纯 HTTP 客户端无法通过。**搜索通常可用，但下载页可能被 403 挑战拦住**，此时需要代理或换网络；这不是凭据问题。
3. **版权合规**：Z-Library / Anna's Archive 涉及版权敏感内容。智能体仅应在用户明确授权、且遵守所在国家/地区法律与版权规定的前提下调用本技能；不得用于批量盗版下载或传播。
4. **stdout 只放结果**：进度、日志、错误一律走 stderr，`--json` 时 stdout 为纯 JSON，适合 `jq` / 脚本消费。
5. **标识符形状**：Z-Library 为 `id/hash`（带 `/`），Anna's Archive 为裸 MD5，下载时按形状自动选源，也可显式 `--source annas`。
6. **免费账号每天约 10 次下载**，`zlib quota` 查看；存多个账号后 `download` 碰到限额自动轮换，全部耗尽才报错并列出各账号用量。

## 7. 示例工作流（供智能体参考）

```bash
# 1. 准备账号（可选，Anna's Archive 搜索可跳过）
zlib account add --email user@example.com --password 'pass'

# 2. 搜索（机器可读）
zlib --json search "deep learning" --limit 5 --source auto

# 3. 从结果取 Download ID，下载
zlib download 3647669/945552 -o ~/Books

# 4. 查看配额与体检
zlib quota --all
zlib --verbose doctor
```

## 8. 安装到 pi 技能库

将本文件放至 `~/.agents/skills/zlib-go/SKILL.md`（本项目副本见 `.agents/skills/zlib-go/SKILL.md`），即可被 pi 框架中的智能体发现与使用。如需本地开发，在项目根运行 `make check`（vet + gofmt + test）。
