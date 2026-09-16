# Fork 自有改动记录（Codex 网络指纹伪装）

> **这份文档的用途**：本仓库是 fork，需要持续合并官方上游更新。这里完整记录**我们自己加的东西**，
> 以便合并官方代码时能快速判断「这块是我们的还是官方的」、冲突时如何正确重新落地。
>
> **维护约定**：任何对本 fork 自有功能的修改，都必须同步更新本文档。

---

## 1. 基本信息

| 项 | 值 |
|---|---|
| 自有改动基点（fork point） | `bdb42e22f` (`Merge pull request #7064 ...`) |
| 自有改动分支 | `my` |
| 官方跟随分支 | `origin/main` |
| 自有改动规模 | 36 个文件（11 个新增 + 25 个插桩）；其中代码部分 +1376 / −22 行，其余为本文档与 `AGENTS.md` |
| **新增第三方依赖** | **无**（复用已有的 `imroc/req/v3`、`refraction-networking/utls`、`golang.org/x/net`；`go.mod` / `go.sum` 未改动） |

随时重新生成自有改动清单：

```bash
git diff --stat bdb42e22f..my
```

### 自有改动的 commit

| Commit | 说明 |
|---|---|
| `31308bbb6` | feat: Codex CLI 网络指纹伪装 + UI 设置（主体） |
| `7f4d7e13f` | 优化伪装（新增 `openai_codex_persona.go`，当时未接线） |
| `ad0a60752` | fix: 修 golangci-lint；把 persona 接线并做成 UI 开关 |
| `5ddb76af8` | test: 把新设置补进 admin settings 契约测试基线 |

---

## 2. 我们解决的问题

官方代码发往 ChatGPT / OpenAI Codex 上游的请求，**只在 HTTP 请求头层做了伪装**（User-Agent / originator / version 改写），
但 **TLS 握手与 HTTP/2 层完全是 Go 标准库的原样特征**，可被上游一眼识别为非官方客户端：

- TLS ClientHello 是 Go 的（JA3/JA4 为 Go 指纹，与真实 Codex 的 rustls 完全不同）
- HTTP/2 SETTINGS / WINDOW_UPDATE 是 Go 的默认值
- HTTP/2 伪头顺序是 Go 的 `:authority :method :path :scheme`（真实 Codex 是 `:method :scheme :authority :path`）
- Go 会自动加 `Accept-Encoding: gzip`（真实 Codex 不发）

**矛盾点**：请求头伪装成 Codex（Rust 客户端），TLS 却是 Go 的 —— 这种「头与握手对不上」本身就是强识别信号。

真实 Codex 的技术栈（已对照官方源码 `codex-rs` 确认）：
- HTTP：`reqwest` + `hyper` + `rustls`
- WebSocket：`tokio-tungstenite` + `rustls`，HTTP/1.1 Upgrade（**不是** HTTP/2 Extended CONNECT）

---

## 3. 实测基线（回归时用来校验）

参考客户端：**codex-cli 0.154.0**（Windows，API-key 模式与 ChatGPT OAuth 模式均抓取）。
验证方法：本地起一个抓包服务器解析 ClientHello / HTTP2 帧，分别让真实 codex 与本网关打进去对比。

| 指标 | 真实 Codex | 本 fork 实现 |
|---|---|---|
| **JA4（HTTP，h2）** | `t13d1011h2_61a7ad8aa9b6_f9531d972513` | **完全一致** |
| **JA4（WS，h1）** | ALPN 仅 `http/1.1` | `t13d1011h1_61a7ad8aa9b6_f9531d972513` |
| H2 SETTINGS（顺序与值） | `ENABLE_PUSH=0`, `INITIAL_WINDOW_SIZE=2097152`, `MAX_FRAME_SIZE=16384`, `MAX_HEADER_LIST_SIZE=16384` | 一致 |
| 连接级 WINDOW_UPDATE | `5177345` | 一致 |
| H2 伪头顺序 | `:method :scheme :authority :path` | 一致 |
| key shares | `X25519MLKEM768`(1216B) + `X25519`(32B) | 一致 |
| PSK modes | `[1]` (psk_dhe_ke) | 一致 |
| legacy session id | 空（rustls 不发兼容性 session id） | 一致 |
| 扩展顺序 | **每连接随机打乱**（JA3 变、JA4 稳定） | 一致（同样打乱） |
| `accept-encoding` | 不发 | 不发 |
| WS `Sec-WebSocket-Extensions` | 不发（tungstenite 默认不开 permessage-deflate） | 不发 |

> 注：JA4 前缀里的 `d`/`i` 与扩展计数取决于是否发 SNI。连 IP（无 SNI）会得到 `t13i1010`，
> 连域名（有 SNI，真实场景）得到 `t13d1011`。三段哈希相同即表示套件/扩展集合完全一致。

---

## 4. 新增文件（自成一体，几乎不会与官方冲突）

| 文件 | 作用 |
|---|---|
| `backend/internal/pkg/codexfp/tls.go` | rustls 形态的 uTLS ClientHello（套件/扩展/曲线/签名算法顺序均照抄实测）、每连接打乱扩展、空 session id、进程级总开关 `Enabled()/SetEnabled()`、目标主机白名单 `IsFingerprintHost()` |
| `backend/internal/pkg/codexfp/transport.go` | 基于 `imroc/req` 的 HTTP RoundTripper：hyper 形态 H2 SETTINGS / 连接流控 / 伪头顺序、关闭自动 gzip、`ApplyHeaderOrder()` 固定请求头顺序 |
| `backend/internal/pkg/codexfp/ws.go` | WebSocket 用的 h1 TLS dialer（ALPN 仅 http/1.1），支持直连 / HTTP CONNECT / SOCKS5 |
| `backend/internal/pkg/codexfp/codexfp_test.go` | ClientHello 形状与扩展打乱、伪头顺序单测 |
| `backend/internal/service/openai_codex_transport_fingerprint.go` | 判定是否启用指纹（`shouldUseCodexFingerprint`）、给请求打 context 标记并固定请求头顺序（`applyCodexFingerprintTransport`）、Codex 出站请求头顺序表 |
| `backend/internal/service/openai_codex_timezone.go` | `environment_context` 的 `<timezone>` / `<current_date>` 改写与 IANA 校验 |
| `backend/internal/service/openai_codex_timezone_test.go` | 时区改写单测 |
| `backend/internal/service/openai_codex_persona.go` | 每账号 UA persona：按凭据确定性派生稳定的 OS/终端指纹段 |
| `.github/workflows/docker-my.yml` | 自有 CI：push 到 `my` 分支即构建镜像并推 **ghcr.io**（仅用内置 `GITHUB_TOKEN`，不需要 Docker Hub 凭据） |

---

## 5. 对官方文件的改动（合并时重点看这里）

下表的「锚点」是改动所在的函数/结构体，合并冲突时按锚点重新落地。

### 5.1 传输层

| 文件 | 锚点 | 改动 |
|---|---|---|
| `backend/internal/repository/http_upstream.go` | `import` | 加 `pkg/codexfp` |
| | `const (...)` | 加协议模式 `upstreamProtocolModeOpenAICodexFP = "openai_codexfp"` |
| | `Do()` | 读 context 标记算出 `codexFP`，透传给 `acquireClientWithProfile` |
| | `acquireClient()` / `acquireClientWithProfile()` / `getOrCreateClient()` | **签名增加 `codexFP bool` 参数** |
| | `getClientEntry()` | **签名增加 `codexFP bool`**；`codexFP` 为真时把 protocolMode 强制为 codexfp 模式；按模式选择 `buildCodexFingerprintTransport` 或原有 `buildUpstreamTransport`；client 的 Transport 类型放宽为 `http.RoundTripper` |
| | `enableHTTP2KeepAlive()` 之后 | 新增 `buildCodexFingerprintTransport()` |
| `backend/internal/repository/http_upstream_test.go` | 8 处 `getClientEntry(...)` 调用 | 补上新增的 `codexFP` 实参（`false`） |
| `backend/internal/service/http_upstream_profile.go` | `httpUpstreamPublicHostsOnlyContextKey` 之后 | 新增 `WithHTTPUpstreamCodexFingerprint()` / `HTTPUpstreamCodexFingerprint()` context 标记 |

> ⚠️ **合并注意**：`getClientEntry` 等函数签名被我们改过。若官方也改了这些函数或其调用方，
> 合并后务必确认所有调用点都带上 `codexFP` 参数（`go build ./...` 会直接报错，不会静默出错）。

### 5.2 出站请求构造

| 文件 | 锚点 | 改动 |
|---|---|---|
| `backend/internal/service/openai_gateway_forward.go` | `buildUpstreamRequest()`（`normalizeDeepSeekResponsesRequestBody` 之后、建 request 之前） | 按全局设置改写请求体时区 |
| | `buildUpstreamRequest()` 末尾 `return req, nil` 之前 | `req = s.applyCodexFingerprintTransport(req, account)`（**必须在所有请求头改写之后**） |
| | `codexIdentityOverrideUA()` | 由 `account.GetOpenAIUserAgent()` 改为 `codexAccountOverrideUA(account)`（接入 persona） |
| `backend/internal/service/openai_gateway_passthrough.go` | `buildUpstreamRequestOpenAIPassthrough()` 同上两处位置 | 同上（时区改写 + 指纹收口） |
| `backend/internal/service/openai_gateway_service.go` | `const (...)` | `codexCLIVersion` 由 `0.146.0` 提到 `0.154.0` |
| | `NewOpenAIGatewayService()` | 去掉原先按 viper 配置置指纹开关的逻辑，改为注释说明「运行时以 DB 设置为准」 |
| `backend/internal/service/openai_codex_identity.go` | `buildCodexCLIUserAgent()` | 改为委托给新增的 `buildCodexCLIUserAgentWithOriginator()` |
| | 同上附近 | 新增 `buildCodexCLIUserAgentWithOriginator()`、`NormalizeCodexOriginator()` |
| `backend/internal/service/openai_ws_client.go` | `import` | 加 `pkg/codexfp` |
| | `coderOpenAIWSClientDialer` 结构体 | 加 `codexMu` / `codexClients` 缓存字段；新增 `codexFingerprintClient()` |
| | `Dial()` | Codex 主机走 codexfp 客户端，并强制 `CompressionMode = CompressionDisabled` |

### 5.3 设置系统（4 个新设置，贯穿 9 个文件）

| 文件 | 锚点 | 改动 |
|---|---|---|
| `backend/internal/service/domain_constants.go` | 设置键 `const` 块 | 新增 4 个 `SettingKey*` |
| `backend/internal/service/settings_view.go` | `type SystemSettings struct` | 新增 4 个字段 |
| `backend/internal/service/setting_parse.go` | `InitializeDefaultSettings()` 默认值 map | 4 个键的默认值 |
| | `parseSettings()` | 4 个键的解析（含默认值兜底与归一化） |
| `backend/internal/service/setting_update.go` | `import` | 加 `pkg/codexfp` |
| | `buildSystemSettingsUpdates()` | 4 个键写库 |
| | `refreshCachedSettings()` | 热更新：`codexfp.SetEnabled()`、`SetCodexAccountPersonaEnabled()`、失效 originator/timezone 缓存 |
| `backend/internal/service/setting_service.go` | `type SettingService struct` | 新增 originator / timezone 的缓存与 singleflight 字段 |
| `backend/internal/service/setting_gateway_runtime.go` | `import` | 加 `pkg/codexfp` |
| | `openAICodexClientVersionSFKey` 附近 | 新增两个缓存类型与 TTL 常量 |
| | `GetOpenAICodexUserAgent()` 之后 | 新增 `GetOpenAICodexOriginator()`、`GetOpenAICodexTimezone()`、`WarmOpenAICodexFingerprintEnabled()` |
| | `GetOpenAICodexCanonicalUserAgent()` | 面板 UA 为空时，按配置的 originator 拼规范 UA |
| `backend/internal/service/wire.go` | `ProvideOpsService()` | 启动时 `WarmOpenAICodexFingerprintEnabled()` |
| `backend/internal/handler/dto/settings.go` | `type SystemSettings struct` | 4 个 JSON 字段 |
| `backend/internal/handler/admin/setting_handler.go` | `GetSettings()` | 4 个字段出参映射 |
| `backend/internal/handler/admin/setting_handler_update.go` | `type UpdateSettingsRequest struct` | 4 个指针字段 |
| | `UpdateSettings()`（合并 previous 的那段） | 4 个字段的 merge |
| | `UpdateSettings()`（返回响应那段） | 4 个字段回填 |
| `backend/internal/handler/admin/setting_handler_audit.go` | `diffSettings()` | 4 个字段的审计 diff |
| `backend/internal/server/api_contract_test.go` | `TestAPIContracts`（**两处**期望 map） | 补 4 个字段的期望值 |

> ⚠️ **最容易被漏的坑**：`api_contract_test.go` 里的 settings 期望 map 是**全量精确比对**的 golden 基线，
> 而且**有两份**（正常用例 + config 兜底用例）。官方新增任何设置也会改这里 → 极易冲突。
> 合并后如果 `test` job 报 `TestAPIContracts` 失败，先确认我们这 4 个键还在两份 map 里。

### 5.4 前端

| 文件 | 锚点 | 改动 |
|---|---|---|
| `frontend/src/api/admin/settings.ts` | `SystemSettings` / `UpdateSettingsRequest` 接口 | 各加 4 个字段 |
| `frontend/src/views/admin/SettingsView.vue` | 「网关转发」区块，Codex 版本自动同步开关之后 | 4 个控件（2 开关 + 1 下拉 + 1 输入） |
| | `const form = reactive<SettingsForm>({...})` | 4 个默认值 |
| | `saveSettings()` 提交载荷 | 4 个字段 |
| `frontend/src/i18n/locales/en/admin/settings.ts` | `gatewayForwarding` | 9 条文案 |
| `frontend/src/i18n/locales/zh/admin/settings.ts` | `gatewayForwarding` | 9 条文案 |

> ⚠️ 前端有 **i18n 键完整性测试**（`src/i18n/__tests__/localeKeyCompleteness.spec.ts`），
> 中英文案必须成对增删，否则该测试失败。

### 5.5 `.gitignore`

官方的 `.gitignore` 有两条策略会把本 fork 的文档挡在版本库外：

- 第 ~130 行逐名忽略 `AGENTS.md`（与 `CLAUDE.md` 一起，避免贡献者的本地 agent 文件被提交）
- 第 ~135 行 `docs/*` 默认忽略整个 docs 目录，再用 `!docs/xxx.md` 白名单逐个放行官方文档

因此我们新增了两条放行规则（都带 `# [fork]` 注释标记，便于合并时识别）：

```gitignore
!AGENTS.md
!docs/FORK_CHANGES.md
```

> ⚠️ **合并注意**：官方若在同一处白名单追加新文档，这里会产生冲突，但属于「两边各加几行」的
> 平凡冲突，两边都保留即可。**如果合并后 `AGENTS.md` 或本文档「凭空消失」，第一件事就是检查这两条
> 放行规则是否被覆盖掉了。**

---

## 6. 新增的 4 个设置项

| 设置键 | 类型 | 默认 | 作用 |
|---|---|---|---|
| `openai_codex_fingerprint_enabled` | bool | `true` | 总开关。对真实 Codex 端点启用 TLS/H2 伪装（HTTP 与 WS 共用）。关闭即回退 Go 标准库传输 |
| `openai_codex_originator` | string | `codex-tui` | 出站 originator（UA 首段与其同源）。仅接受官方一方值，非法值回退默认 |
| `openai_codex_timezone` | string | `""` | IANA 时区名。改写请求体 `environment_context` 的 `<timezone>` 与 `<current_date>`；空=不改写 |
| `openai_codex_account_persona_enabled` | bool | `false` | 每账号 UA persona：按凭据派生稳定的 OS/终端段 |

**生效机制**：总开关与 persona 是**进程级 flag**（启动时由 `WarmOpenAICodexFingerprintEnabled` 从 DB 同步，
保存设置时在 `refreshCachedSettings` 里热更新）；originator 与 timezone 走 60s TTL 的进程内缓存。
因此**改设置无需重启**。

**生效范围**：仅 `chatgpt.com` / `api.openai.com` / `auth.openai.com`（见 `codexfp.FingerprintHosts`）。
第三方 OpenAI 兼容服务商（DeepSeek / Kimi 等）**不受影响**，仍走原有传输。

---

## 7. 出站身份的优先级（新旧功能如何叠加）

官方原有的「OpenAI Codex UA / Codex 客户端版本号 / 自动同步」与我们新增的项是**分层叠加**，不是互相覆盖出错：

**UA 来源优先级（高 → 低）**
1. 账号级自定义 UA（账号编辑里的 `user_agent`）
2. 每账号 persona（若开启）
3. 全局「OpenAI Codex UA」面板设置（若填写）
4. `originator` 下拉 + 版本号拼出的规范 UA

**版本号**：无论命中哪一层，version 段都由「Codex 客户端版本号 / 自动同步」重建，
所以 `UA = originator/版本(...)` 是正交组合。

**originator**：由最终生效 UA 的首段配对推导（`PairCodexClientIdentity`），保证 originator 与 UA 自洽。

### ⚠️ 已知不一致（待定）

`openai_codex_persona.go` 里把 originator **硬编码为 `codex-tui`**。
因此**开启 persona 时，`originator` 下拉设置不生效**（persona 赢）。

两个可选修法（尚未实施）：
- 让 persona 使用配置的 originator（推荐，两个控件语义一致）；
- 或保持硬编码，在面板文案里注明「开启 persona 时 originator 不生效」。

---

## 8. 与官方上游合并的流程

### 当前状态
`origin/main` 已领先我们的基点 `bdb42e22f` **56 个 commit / 106 个文件**，但与我们改动的**重叠只有 1 个文件**：

| 重叠文件 | 官方改的位置 | 我们改的位置 | 结论 |
|---|---|---|---|
| `backend/internal/service/wire.go` | `ProvideRateLimitService()`（加 ollama 参数） | `ProvideOpsService()`（加 warm 调用） | **不同函数，git 可自动合并** |

随时重新评估冲突面：

```bash
git fetch origin
comm -12 <(git diff --name-only bdb42e22f..my | sort) \
         <(git diff --name-only bdb42e22f..origin/main | sort)
```

### 合并步骤

```bash
git fetch origin
git checkout my
git merge origin/main          # 冲突面很小，优先用 merge 保留历史
```

### 合并后必须验证

```bash
# 后端
cd backend
go build ./...
go test -tags=unit ./...         # 注意 TestAPIContracts（见 5.3 的坑）
go test -tags=integration ./...
golangci-lint run                # CI 用 v2.13；unused 检查器会因死代码直接失败

# 前端
cd frontend
pnpm install --frozen-lockfile
./node_modules/.bin/vue-tsc --noEmit
./node_modules/.bin/vitest run src/i18n/__tests__/localeKeyCompleteness.spec.ts \
                               src/views/admin/__tests__/SettingsView.spec.ts
```

#### 本地验证的两个已知坑（都不是代码问题，别浪费时间排查）

**1. `pnpm install --frozen-lockfile` 在 pnpm 10+ 会失败**

报错 `ERR_PNPM_LOCKFILE_CONFIG_MISMATCH: The current "overrides" configuration doesn't match the value found in the lockfile`。
根因：新版 pnpm **不再读取 `package.json` 里的 `pnpm.overrides` 字段**（它会先警告
`The "pnpm" field in package.json is no longer read by pnpm`），于是认为当前 overrides 为空，
而 lockfile 里有 4 条（`js-cookie` / `form-data` / `postcss` / `dompurify`）→ 判定不匹配。

**CI 不受影响**：workflow 固定用 pnpm 9（`pnpm/action-setup@v6` + `version: 9`），pnpm 9 会正常读取该字段。
本地直接调 `./node_modules/.bin/vue-tsc`、`./node_modules/.bin/vitest` 绕过安装前置检查即可。
（另外新版 pnpm 还会因 `esbuild` / `vue-demi` 的 ignored build scripts 让 `pnpm typecheck` 前置检查失败，同样绕过或执行一次 `pnpm approve-builds`。）

**2. 官方自带的 flaky 测试：`TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort`**

位置 `backend/internal/service/ratelimit_service_ollama_429_test.go:405`，
断言 `stale long callback must not pass the CAS`（`Should be zero, but was 1`）。

实测（独立进程各采样 3 次）：

| | run1 | run2 | run3 |
|---|---|---|---|
| 合并后 `my` | FAIL | FAIL | PASS |
| 纯官方 `origin/main` | PASS | FAIL | FAIL |

**两边都随机失败**，即这是官方测试自身的时间竞态（用 `time.Now().Add(5s)` 与 `handle429` 的
CAS generation 比较），**与本 fork 的改动无关**。该测试也不支持 `-count>1`（第二遍必挂）。

> ⚠️ 合并后若只有这一个 unit 测试变红，**不要**当成合并引入的问题，重跑即可。
> 排查前请先在纯 `origin/main` 上用独立进程多跑几次对照——单次结果会给出相反的错误结论
> （已排除包级副作用：`internal/service` 无 `TestMain`，本 fork 新增/修改的文件均无 `init()`）。

### 合并后的自检清单

- [ ] `go build` 通过（签名类冲突会在这里暴露）
- [ ] `TestAPIContracts` 的**两份** settings 期望 map 里仍有我们 4 个键
- [ ] `codexfp` 包未被误删；`openai_codex_*.go` 4 个文件仍在
- [ ] `getClientEntry` / `acquireClientWithProfile` 的 `codexFP` 参数仍在且调用点齐全
- [ ] `buildUpstreamRequest` / passthrough 末尾的 `applyCodexFingerprintTransport` 仍在**最后**
- [ ] 中英 i18n 文案成对存在
- [ ] `codexCLIVersion` 取较新值（官方若也提了版本，用官方的）
- [ ] `go.mod` / `go.sum` 若有变动，确认 `imroc/req`、`utls`、`x/net` 仍在

---

## 9. 已知的残留差异（未做，非 bug）

1. **WS 的 HTTP/1.1 请求头顺序/大小写**仍是 Go 的（字母序 + Title-Case），
   真实 Codex 是 Rust `http::HeaderMap` 的哈希表顺序。
   由于该顺序**在真实 Codex 侧本身就不稳定**（随头集合变化），且 Go 的 `http.Header` 必然排序，
   做到逐字节一致需要手写 WS 客户端（替换 `coder/websocket`），性价比低，故未做。
   强信号（TLS / permessage-deflate / gzip）已全部消除。
2. **HTTP 普通请求头顺序**按实测的 /responses 请求定了一版，但缺少真实 OAuth 模式 /responses 的干净抓包校准（需有效 token）。
3. **TLS 会话恢复（session resumption）** 行为未与真实客户端逐项比对。
4. **codexfp 路径没有接官方原有的 h2→h1 回退机制**（代理不支持 h2 时不会自动降级）。
   直连部署无影响；若将来要挂代理需补上。

---

## 10. 自有 CI

`.github/workflows/docker-my.yml`：
- 触发：push 到 `my` 分支 / 手动 `workflow_dispatch`
- 构建：仓库根 `Dockerfile`（前端 + 后端多阶段），`linux/amd64`，带 GHA 缓存
- 推送：**仅 ghcr.io**，镜像 `ghcr.io/<owner>/sub2api`，标签 `my` 与 `my-<shortsha>`
- 鉴权：内置 `GITHUB_TOKEN`（`permissions: packages: write`），**无需任何 Docker Hub 凭据**

官方自带的 `release.yml` 等 workflow 未改动。
