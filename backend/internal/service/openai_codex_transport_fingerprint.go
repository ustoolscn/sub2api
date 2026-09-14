package service

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codexfp"
)

// codexOutboundHeaderOrder is the HTTP/2 header order the official Codex CLI
// (hyper HeaderMap insertion order) emits on the /responses turn request.
// Headers not present are skipped; any extra header keeps its position after
// these. Captured from codex-cli 0.154.0.
var codexOutboundHeaderOrder = []string{
	"x-codex-beta-features",
	"x-codex-window-id",
	"x-codex-turn-metadata",
	"x-client-request-id",
	"session_id",
	"session-id",
	"thread-id",
	"conversation_id",
	"chatgpt-account-id",
	"x-codex-routing-hint",
	"openai-beta",
	"accept",
	"content-type",
	"authorization",
	"originator",
	"version",
	"user-agent",
}

// shouldUseCodexFingerprint reports whether an outbound request to targetURL for
// account must leave with the Codex CLI network fingerprint.
func (s *OpenAIGatewayService) shouldUseCodexFingerprint(account *Account, targetURL string) bool {
	// 总开关来源:进程级 flag(DB 设置驱动,启动 warm + 保存热同步),HTTP/WS 出站共用。
	if s == nil || !codexfp.Enabled() {
		return false
	}
	if account == nil || !account.UsesOpenAICodexProtocol() {
		return false
	}
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return false
	}
	return codexfp.IsFingerprintHost(strings.ToLower(strings.TrimSpace(parsed.Hostname())))
}

// applyCodexFingerprintTransport marks req for the Codex network fingerprint and
// stamps the Codex header/pseudo-header ordering, returning the request to use.
// Must be called after all header mutations so the final header set is ordered.
// The ordering magic keys are only added on the fingerprint path; the generic Go
// transport would otherwise forward them to the upstream as literal headers.
func (s *OpenAIGatewayService) applyCodexFingerprintTransport(req *http.Request, account *Account) *http.Request {
	if req == nil || req.URL == nil || !s.shouldUseCodexFingerprint(account, req.URL.String()) {
		return req
	}
	req = req.WithContext(WithHTTPUpstreamCodexFingerprint(req.Context()))
	codexfp.ApplyHeaderOrder(req.Header, codexOutboundHeaderOrder)
	return req
}
