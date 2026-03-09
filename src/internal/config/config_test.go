package config

import (
	"os"
	"path/filepath"
	"testing"

	"cli-agent-gateway/internal/infra/envfile"
)

func TestLoadPrefersUserEnvWhenProcessEnvIsBlank(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	repoEnv := filepath.Join(repo, ".env")
	if err := envfile.Write(repoEnv, map[string]string{
		"CHANNEL_TYPE": "dingtalk",
	}, []string{"CHANNEL_TYPE"}, nil); err != nil {
		t.Fatalf("write repo .env failed: %v", err)
	}

	userEnv := filepath.Join(home, ".cag", ".env")
	if err := envfile.Write(userEnv, map[string]string{
		"DINGTALK_APP_KEY":    "app-key-1",
		"DINGTALK_APP_SECRET": "app-secret-1",
		"DINGTALK_AGENT_ID":   "agent-1",
		"DINGTALK_SEND_MODE":  "api",
	}, []string{
		"DINGTALK_APP_KEY",
		"DINGTALK_APP_SECRET",
		"DINGTALK_AGENT_ID",
		"DINGTALK_SEND_MODE",
	}, nil); err != nil {
		t.Fatalf("write user .env failed: %v", err)
	}

	origHome, hadHome := os.LookupEnv("HOME")
	origKey, hadKey := os.LookupEnv("DINGTALK_APP_KEY")
	origSecret, hadSecret := os.LookupEnv("DINGTALK_APP_SECRET")
	origAgent, hadAgent := os.LookupEnv("DINGTALK_AGENT_ID")
	t.Cleanup(func() {
		if hadHome {
			_ = os.Setenv("HOME", origHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadKey {
			_ = os.Setenv("DINGTALK_APP_KEY", origKey)
		} else {
			_ = os.Unsetenv("DINGTALK_APP_KEY")
		}
		if hadSecret {
			_ = os.Setenv("DINGTALK_APP_SECRET", origSecret)
		} else {
			_ = os.Unsetenv("DINGTALK_APP_SECRET")
		}
		if hadAgent {
			_ = os.Setenv("DINGTALK_AGENT_ID", origAgent)
		} else {
			_ = os.Unsetenv("DINGTALK_AGENT_ID")
		}
	})

	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME failed: %v", err)
	}
	if err := os.Setenv("DINGTALK_APP_KEY", ""); err != nil {
		t.Fatalf("set blank app key failed: %v", err)
	}
	if err := os.Setenv("DINGTALK_APP_SECRET", ""); err != nil {
		t.Fatalf("set blank app secret failed: %v", err)
	}
	if err := os.Setenv("DINGTALK_AGENT_ID", ""); err != nil {
		t.Fatalf("set blank agent id failed: %v", err)
	}

	cfg, err := Load(repo, "")
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if cfg.DingTalkAppKey != "app-key-1" {
		t.Fatalf("expected app key from ~/.cag/.env, got=%q", cfg.DingTalkAppKey)
	}
	if cfg.DingTalkAppSecret != "app-secret-1" {
		t.Fatalf("expected app secret from ~/.cag/.env, got=%q", cfg.DingTalkAppSecret)
	}
	if cfg.DingTalkAgentID != "agent-1" {
		t.Fatalf("expected agent id from ~/.cag/.env, got=%q", cfg.DingTalkAgentID)
	}
}

func TestDefaultRuntimePathsUseFixedCAGHome(t *testing.T) {
	home := t.TempDir()
	origHome, hadHome := os.LookupEnv("HOME")
	t.Cleanup(func() {
		if hadHome {
			_ = os.Setenv("HOME", origHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
	})
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME failed: %v", err)
	}

	pathsA := DefaultRuntimePaths(filepath.Join(t.TempDir(), "repo-a"))
	pathsB := DefaultRuntimePaths(filepath.Join(t.TempDir(), "repo-b"))
	wantBase := filepath.Join(home, ".cag", "runtime")
	if pathsA.BaseDir != wantBase {
		t.Fatalf("expected fixed runtime base=%s got=%s", wantBase, pathsA.BaseDir)
	}
	if pathsB.BaseDir != wantBase {
		t.Fatalf("expected fixed runtime base=%s got=%s", wantBase, pathsB.BaseDir)
	}
	if pathsA.LockFile != filepath.Join(wantBase, "gateway.lock") {
		t.Fatalf("unexpected lock file path: %s", pathsA.LockFile)
	}
}
