package main

import (
	"os"
	"strings"

	"cli-agent-gateway/internal/config"
)

func managedChildEnv(extra ...string) []string {
	scrubbed := make(map[string]struct{}, len(config.KnownKeys()))
	for _, key := range config.KnownKeys() {
		scrubbed[key] = struct{}{}
	}
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, item := range os.Environ() {
		key := item
		if idx := strings.IndexByte(item, '='); idx >= 0 {
			key = item[:idx]
		}
		if _, blocked := scrubbed[key]; blocked {
			continue
		}
		env = append(env, item)
	}
	env = append(env, extra...)
	return env
}
