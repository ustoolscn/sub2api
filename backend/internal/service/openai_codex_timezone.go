package service

import (
	"regexp"
	"strings"
	"time"
)

var (
	codexEnvTimezoneRe  = regexp.MustCompile(`(<timezone>)(.*?)(</timezone>)`)
	codexEnvCurrentDate = regexp.MustCompile(`(<current_date>)(.*?)(</current_date>)`)
)

// NormalizeCodexTimezone trims and validates an IANA timezone name. Empty or
// invalid input returns "" (meaning: do not rewrite, pass the client value
// through). Validation uses time.LoadLocation so only zones present in the
// runtime tzdata are accepted.
func NormalizeCodexTimezone(tz string) string {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return ""
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return ""
	}
	return tz
}

// rewriteCodexEnvironmentTimezone rewrites the <timezone> and <current_date>
// tags inside the Codex environment_context so a shared upstream account does not
// leak each downstream user's real timezone (and so the reported timezone matches
// the egress region). Returns body unchanged when tz is empty or invalid, or when
// the tags are absent. Operates on raw bytes to stay cheap on the passthrough hot
// path.
func rewriteCodexEnvironmentTimezone(body []byte, tz string) []byte {
	tz = strings.TrimSpace(tz)
	if len(body) == 0 || tz == "" {
		return body
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return body
	}
	if !codexEnvTimezoneRe.Match(body) {
		return body
	}
	out := codexEnvTimezoneRe.ReplaceAll(body, []byte("${1}"+tz+"${3}"))
	date := time.Now().In(loc).Format("2006-01-02")
	out = codexEnvCurrentDate.ReplaceAll(out, []byte("${1}"+date+"${3}"))
	return out
}
