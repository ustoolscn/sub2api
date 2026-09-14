package service

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// codexAccountPersonaPlatforms are real Codex TUI User-Agent platform/terminal
// segments ({os} {os_version}; {arch}) {terminal}. A ChatGPT account normally
// runs Codex from one or a few machines, so every OAuth credential is pinned to
// one stable segment instead of the whole fleet sharing a single identity.
var codexAccountPersonaPlatforms = []string{
	" (Mac OS 15.6.1; arm64) iTerm.app/3.6.1",
	" (Mac OS 15.5.0; arm64) Apple_Terminal/464",
	" (Mac OS 26.0.0; arm64) ghostty/1.2.0",
	" (Mac OS 15.6.1; arm64) vscode/1.104.1",
	" (Windows 10.0.26100; x86_64) WindowsTerminal",
	" (Windows 10.0.22631; x86_64) vscode/1.104.1",
	" (Ubuntu 24.4.0; x86_64) xterm-256color",
	" (Ubuntu 22.4.0; x86_64) tmux/3.2a",
}

// codexAccountPersonaEnabled is published from gateway.codex_account_persona
// at service construction; the identity helpers are pure functions shared by
// HTTP, passthrough, WS and probe paths.
var codexAccountPersonaEnabled atomic.Bool

// SetCodexAccountPersonaEnabled publishes the per-account UA persona switch.
func SetCodexAccountPersonaEnabled(enabled bool) {
	codexAccountPersonaEnabled.Store(enabled)
}

// codexAccountPersonaUA returns the stable per-credential Codex TUI User-Agent.
// Its version segments are rebuilt from the effective client version by
// resolveCodexOutboundIdentity, so the persona never pins a stale version.
func codexAccountPersonaUA(account *Account) string {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return ""
	}
	seed := codexAccountIdentityNamespace(account)
	if seed == "" {
		if account.ID <= 0 {
			return ""
		}
		seed = fmt.Sprintf("account:%d", account.ID)
	}
	sum := sha256.Sum256([]byte("sub2api:codex-persona:v1:" + seed))
	platform := codexAccountPersonaPlatforms[binary.BigEndian.Uint32(sum[:4])%uint32(len(codexAccountPersonaPlatforms))]
	version := CodexCanonicalClientVersion()
	originator := openai.CodexDefaultOriginator
	return originator + "/" + version + platform + " (" + originator + "; " + version + ")"
}

// codexAccountOverrideUA is the outbound UA override for an OAuth credential:
// the admin-configured account UA wins, otherwise the per-account persona when
// enabled, otherwise "" (gateway canonical identity).
func codexAccountOverrideUA(account *Account) string {
	if account == nil {
		return ""
	}
	if ua := account.GetOpenAIUserAgent(); ua != "" {
		return ua
	}
	if codexAccountPersonaEnabled.Load() {
		return codexAccountPersonaUA(account)
	}
	return ""
}
