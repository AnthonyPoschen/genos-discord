package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReportsMissingNamesInOrder(t *testing.T) {
	_, missing := Load(func(string) string { return "" })
	if strings.Join(missing, ",") != strings.Join(RequiredEnv, ",") {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestLoadTreatsWhitespaceAsMissing(t *testing.T) {
	_, missing := Load(func(name string) string {
		if name == "DISCORD_BOT_TOKEN" {
			return "  "
		}
		return "value"
	})
	if len(missing) != 1 || missing[0] != "DISCORD_BOT_TOKEN" {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestLoadSkipsPresentValues(t *testing.T) {
	cfg, missing := Load(func(name string) string {
		if name == "GENOS_API_ORIGIN" {
			return ""
		}
		return "set-" + name
	})
	if len(missing) != 1 || missing[0] != "GENOS_API_ORIGIN" {
		t.Fatalf("missing = %#v", missing)
	}
	if cfg.DiscordBotToken != "set-DISCORD_BOT_TOKEN" || cfg.GenosAPIOrigin != "" {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestGapsDocumentListsTheSameGap(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "GAPS.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, HumanGap) {
		t.Fatalf("GAPS.md missing gap sentence:\n%s", text)
	}
	for _, name := range RequiredEnv {
		if !strings.Contains(text, name) {
			t.Fatalf("GAPS.md missing %s", name)
		}
	}
}

func TestDeployManifestHasNoSecretValues(t *testing.T) {
	root := filepath.Join("..", "..")
	deployment, err := os.ReadFile(filepath.Join(root, "deploy", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(deployment)
	for _, name := range RequiredEnv {
		if !strings.Contains(text, "key: "+name) {
			t.Fatalf("deployment missing secret key %s", name)
		}
	}
	for _, want := range []string{
		"name: genos-discord",
		"image: ghcr.io/anthonyposchen/genos-discord:latest",
		"automountServiceAccountToken: false",
		"runAsNonRoot: true",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("deployment missing %s", want)
		}
	}
	if strings.Contains(text, "genos_discord_") || strings.Contains(text, "kubeconfig") {
		t.Fatal("deployment contains a credential")
	}
	kustomize, err := os.ReadFile(filepath.Join(root, "deploy", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kustomize), "deployment.yaml") || !strings.Contains(string(kustomize), "namespace.yaml") {
		t.Fatalf("kustomization = %s", kustomize)
	}
	docker, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(docker), "USER 65532:65532") || !strings.Contains(string(docker), "CGO_ENABLED=0") {
		t.Fatal("dockerfile is not a non-root static build")
	}
}
