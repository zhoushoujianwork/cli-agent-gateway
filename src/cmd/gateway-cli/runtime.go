package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
	"cli-agent-gateway/internal/utils/sessionctl"
)

func runRuntimeCommand(repoRoot string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "runtime requires a subcommand")
		return 2
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "status":
		fs := flag.NewFlagSet("runtime status", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "runtime.status", *jsonOut, &gatewayv1.ActionRequest{})
	case "ps":
		fs := flag.NewFlagSet("runtime ps", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "json output")
		sessionKey := fs.String("session-key", "", "session filter")
		includeDetached := fs.Bool("include-detached", false, "include detached entries")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "runtime.ps", *jsonOut, &gatewayv1.ActionRequest{
			SessionKey:      *sessionKey,
			IncludeArchived: *includeDetached,
		})
	case "logs":
		fs := flag.NewFlagSet("runtime logs", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "json output")
		follow := fs.Bool("follow", false, "follow logs")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		resp, err := tryActionViaGRPC(repoRoot, &gatewayv1.ActionRequest{Action: "runtime.logs"})
		if err != nil {
			if *jsonOut {
				printJSONActionError("runtime.logs", "gateway_unreachable", formatGatewayUnavailable(err))
			} else {
				fmt.Fprintf(os.Stderr, "runtime logs failed: %s\n", formatGatewayUnavailable(err))
			}
			return 1
		}
		payload, err := decodeActionPayload(resp)
		if err != nil {
			if *jsonOut {
				printJSONActionError("runtime.logs", "decode_failed", err.Error())
			} else {
				fmt.Fprintf(os.Stderr, "runtime logs failed: %v\n", err)
			}
			return 1
		}
		if *jsonOut {
			printJSON(payload)
			return 0
		}
		logFile := sessionctl.CleanString(payload["log_file"])
		if !*follow {
			fmt.Println(logFile)
			return 0
		}
		return followFile(logFile)
	default:
		fmt.Fprintf(os.Stderr, "unknown runtime subcommand: %s\n", args[0])
		return 2
	}
}

func followFile(path string) int {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open log failed: %v\n", err)
		return 1
	}
	defer f.Close()
	if _, err := f.Seek(0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "seek log failed: %v\n", err)
		return 1
	}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			fmt.Print(line)
		}
		if err == nil {
			continue
		}
		time.Sleep(400 * time.Millisecond)
	}
}
