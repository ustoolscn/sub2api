package service

import (
	"strings"
	"testing"
	"time"
)

func TestRewriteCodexEnvironmentTimezone(t *testing.T) {
	body := []byte(`{"input":[{"content":[{"type":"input_text","text":"<environment_context>\n<cwd>/x</cwd>\n<timezone>Asia/Shanghai</timezone>\n<current_date>2026-09-14</current_date>\n</environment_context>"}]}]}`)

	out := rewriteCodexEnvironmentTimezone(body, "America/New_York")
	s := string(out)
	if !strings.Contains(s, "<timezone>America/New_York</timezone>") {
		t.Errorf("timezone not rewritten: %s", s)
	}
	wantDate := time.Now().In(mustLoadTZ(t, "America/New_York")).Format("2006-01-02")
	if !strings.Contains(s, "<current_date>"+wantDate+"</current_date>") {
		t.Errorf("current_date not rewritten to %s: %s", wantDate, s)
	}
}

func TestRewriteCodexEnvironmentTimezoneNoop(t *testing.T) {
	body := []byte(`{"x":"<timezone>Asia/Shanghai</timezone>"}`)
	if got := rewriteCodexEnvironmentTimezone(body, ""); string(got) != string(body) {
		t.Error("empty tz must be a no-op")
	}
	if got := rewriteCodexEnvironmentTimezone(body, "Not/AZone"); string(got) != string(body) {
		t.Error("invalid tz must be a no-op")
	}
	no := []byte(`{"x":"y"}`)
	if got := rewriteCodexEnvironmentTimezone(no, "America/New_York"); string(got) != string(no) {
		t.Error("body without tags must be unchanged")
	}
}

func mustLoadTZ(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}
