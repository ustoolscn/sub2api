# AGENTS.md

给在本仓库工作的 AI 代理 / 协作者的必读说明。

---

## ⚠️ 首要事项：这是一个带自有改动的 fork

本仓库是 fork，会**持续合并官方上游更新**，同时带有一套**自有功能改动**。

> **动手前先读 [`docs/FORK_CHANGES.md`](docs/FORK_CHANGES.md)。**
> 那份文档完整记录了哪些文件是我们自己改的、改在哪个函数、以及合并官方代码时的冲突处理方式。
> 不读就改，很容易把自有功能当成官方代码删掉，或者在合并时静默丢失。

### 分支模型

| 分支 | 含义 |
|---|---|
| `main` / `origin/main` | 跟随官方上游，**不要**往这里放自有改动 |
| `my` | 自有改动分支，所有 fork 专属功能都在这里 |

自有改动的 fork point 是 `bdb42e22f`。查看自有改动全貌：

```bash
git diff --stat bdb42e22f..my
```

### 自有功能一句话概览

**Codex CLI 网络指纹伪装**：让发往 ChatGPT / OpenAI Codex 上游的请求在 TLS ClientHello、
HTTP/2 preface、伪头顺序上与真实 Codex CLI（rustls + hyper）完全一致，而不是暴露 Go 标准库指纹。
配套 4 个可在管理后台热改的设置项。

涉及的自有文件（**不要误删**）：

```
backend/internal/pkg/codexfp/                              # 指纹伪装核心包
backend/internal/service/openai_codex_transport_fingerprint.go
backend/internal/service/openai_codex_timezone.go
backend/internal/service/openai_codex_persona.go
.github/workflows/docker-my.yml                            # 自有 CI（仅推 ghcr.io）
```

另有 25 个官方文件被**小幅插桩**（传输层、请求构造、设置系统、前端设置页、`.gitignore`），
逐一锚点见 `docs/FORK_CHANGES.md` 第 5 节。

> 说明：官方 `.gitignore` 默认忽略 `AGENTS.md` 与 `docs/*`，所以本文件和
> `docs/FORK_CHANGES.md` 是靠 `.gitignore` 里新增的两条 `!` 放行规则才进版本库的。
> 合并官方更新后若这两份文档「消失」，先检查那两条放行规则是否还在。

---

## 合并官方更新的流程

```bash
git fetch origin
git checkout my
git merge origin/main
```

合并后**必须**跑完验证并逐项过一遍 `docs/FORK_CHANGES.md` 第 8 节的自检清单。
几个最容易踩的坑：

1. **`internal/server/api_contract_test.go`** 里 admin settings 的期望 map 是**全量精确比对**的
   golden 基线，而且**有两份**。官方新增设置也会改这里 → 高冲突概率。
   合并后若 `TestAPIContracts` 失败，先确认我们的 4 个设置键还在两份 map 里。
2. **`repository/http_upstream.go`** 的 `getClientEntry` / `acquireClientWithProfile` 等函数
   被我们加了 `codexFP bool` 参数。官方若改动这些函数或调用方，合并后 `go build` 会报错——照错误补参数即可。
3. **前端 i18n 中英文案必须成对**，否则 `localeKeyCompleteness.spec.ts` 失败。
4. **`buildUpstreamRequest` / passthrough 末尾的 `applyCodexFingerprintTransport`
   必须保持在所有请求头改写之后**，否则请求头顺序固定会失效。
5. **`TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort` 是官方自带的 flaky 测试**
   （时间竞态，纯 `origin/main` 上同样随机失败，且不支持 `-count>1`）。
   合并后若只有它变红，重跑即可，**不要**当成合并引入的问题。详见
   `docs/FORK_CHANGES.md` 第 8 节「本地验证的两个已知坑」。

---

## 验证命令

CI（`.github/workflows/backend-ci.yml`）会跑 `shell` / `test` / `frontend` / `golangci-lint` 四个 job。
本地等价命令：

```bash
# 后端
cd backend
go build ./...
go test -tags=unit ./...          # 等价于 make test-unit
go test -tags=integration ./...   # 等价于 make test-integration
golangci-lint run                 # CI 用 v2.13

# 前端
cd frontend
pnpm install --frozen-lockfile
./node_modules/.bin/vue-tsc --noEmit
./node_modules/.bin/vitest run src/i18n/__tests__/localeKeyCompleteness.spec.ts \
                               src/views/admin/__tests__/SettingsView.spec.ts
```

注意事项：

- **`golangci-lint` 开了 `unused` 检查器**：新增但没接线的函数/变量会**直接让 CI 失败**。
  写了新 helper 就要真正接上调用方，否则不要提交。
- **`go test` 需要构建标签**：`go test ./...` 不带 `-tags=unit` 会漏掉相当一部分测试
  （曾因此在本地漏过 CI 才暴露的失败）。
- 新版 pnpm 可能因 ignored build scripts 让 `pnpm typecheck` 的前置检查失败，
  直接调 `./node_modules/.bin/` 下的二进制绕过，或执行一次 `pnpm approve-builds`。

---

## 改动自有功能时的约定

1. **同步更新 `docs/FORK_CHANGES.md`**。那是合并官方代码时唯一可靠的地图，过期即失效。
2. **新增设置项要贯穿 9 个文件**（设置键 → SystemSettings → parse → update/写库 → 缓存刷新 →
   DTO → handler 出参/入参/审计 → 契约测试 golden map → 前端类型/表单/载荷/中英 i18n）。
   照 `docs/FORK_CHANGES.md` 第 5.3 节的清单逐项来，漏一处就会在 CI 或运行时暴露。
3. **指纹相关常量不要凭感觉改**。TLS 套件/扩展顺序、H2 SETTINGS、WINDOW_UPDATE
   都是照实测抓包抄的，改动前请按 `docs/FORK_CHANGES.md` 第 3 节的基线重新抓包验证。
4. **伪装范围限定在真实 Codex 端点**（`codexfp.FingerprintHosts`）。
   不要把 Codex 指纹应用到第三方 OpenAI 兼容服务商。
5. **不要把自有改动合进 `main`**。

---

## 自有 CI

`.github/workflows/docker-my.yml`：push 到 `my` 即构建镜像推到 **ghcr.io**
（`ghcr.io/<owner>/sub2api`，标签 `my` / `my-<shortsha>`），仅用内置 `GITHUB_TOKEN`，
**不需要配置任何 Docker Hub 凭据**。官方自带的 workflow 未改动。
