package envfile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadDotEnvSetDefault reads KEY=VALUE lines and only sets keys missing in process env.
func LoadDotEnvSetDefault(path string) error {
	values, err := Parse(path)
	if err != nil {
		return err
	}
	for key, value := range values {
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
	return nil
}

// Parse reads dotenv file and returns key/value pairs.
func Parse(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := parseValue(strings.TrimSpace(line[eq+1:]))
		if key != "" {
			out[key] = value
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func Quote(value string) string {
	if value == "" {
		return `""`
	}
	needsQuote := false
	for _, ch := range value {
		if ch == '#' || ch == '"' || ch == '\'' || ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return value
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func Write(path string, values map[string]string, orderedKeys []string, headerLines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var lines []string
	for _, h := range headerLines {
		lines = append(lines, h)
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}

	seen := map[string]struct{}{}
	for _, k := range orderedKeys {
		v, ok := values[k]
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s=%s", k, Quote(v)))
		seen[k] = struct{}{}
	}

	var remaining []string
	for k := range values {
		if _, ok := seen[k]; !ok {
			remaining = append(remaining, k)
		}
	}
	sort.Strings(remaining)
	for _, k := range remaining {
		lines = append(lines, fmt.Sprintf("%s=%s", k, Quote(values[k])))
	}
	lines = append(lines, "")
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

func parseValue(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
