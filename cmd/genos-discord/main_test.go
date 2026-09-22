package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AnthonyPoschen/genos-discord/internal/config"
)

func TestMissingEnvOutputNamesAndGap(t *testing.T) {
	var buf bytes.Buffer
	code := reportMissing(func(string) string { return "" }, &buf)
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	text := buf.String()
	for _, name := range config.RequiredEnv {
		if !strings.Contains(text, name) {
			t.Fatalf("output missing %s:\n%s", name, text)
		}
	}
	if !strings.Contains(text, config.HumanGap) {
		t.Fatalf("output missing gap:\n%s", text)
	}
}

func TestConfiguredEnvIsSilent(t *testing.T) {
	var buf bytes.Buffer
	code := reportMissing(func(name string) string { return "set-" + name }, &buf)
	if code != 0 || buf.Len() != 0 {
		t.Fatalf("code %d out %q", code, buf.String())
	}
}

func TestPartialEnvPrintsOnlyMissing(t *testing.T) {
	var buf bytes.Buffer
	code := reportMissing(func(name string) string {
		if name == "DISCORD_BOT_TOKEN" {
			return "token"
		}
		return ""
	}, &buf)
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if lines[0] == "DISCORD_BOT_TOKEN" {
		t.Fatalf("present variable was reported:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "DISCORD_APPLICATION_ID") || !strings.Contains(buf.String(), "GENOS_DISCORD_BOT_SECRET") {
		t.Fatalf("output = %s", buf.String())
	}
}
