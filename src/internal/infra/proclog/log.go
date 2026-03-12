package proclog

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
)

func Configure() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
}

func Info(component string, fields map[string]any) {
	Log("INFO", component, fields)
}

func Warn(component string, fields map[string]any) {
	Log("WARN", component, fields)
}

func Error(component string, fields map[string]any) {
	Log("ERROR", component, fields)
}

func Log(level, component string, fields map[string]any) {
	node := map[string]any{}
	for key, value := range fields {
		node[strings.TrimSpace(key)] = value
	}
	if strings.TrimSpace(component) != "" {
		node["component"] = strings.TrimSpace(component)
	}
	keys := make([]string, 0, len(node))
	for key := range node {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+formatValue(node[key]))
	}
	msg := strings.TrimSpace(level)
	if len(parts) > 0 {
		msg += " " + strings.Join(parts, " ")
	}
	log.Print(msg)
}

func formatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return `""`
	case string:
		return strconv.Quote(strings.TrimSpace(x))
	case fmt.Stringer:
		return strconv.Quote(strings.TrimSpace(x.String()))
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return strconv.Quote(strings.TrimSpace(fmt.Sprint(v)))
	}
}
