package main

import (
	"bufio"
	"fmt"
	"os"
	"time"

	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
	"cli-agent-gateway/internal/utils/sessionctl"
	"github.com/spf13/cobra"
)

func newRuntimeCmd(repoRoot string) *cobra.Command {
	cmd := newGroupCmd("runtime", "Inspect live session runtimes")
	cmd.AddCommand(
		newRuntimeStatusCmd(repoRoot),
		newRuntimePSCmd(repoRoot),
		newRuntimeLogsCmd(repoRoot),
	)
	return cmd
}

func newRuntimeStatusCmd(repoRoot string) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:          "status",
		Short:        "Show global runtime status",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitCodeToError(runAction(repoRoot, "runtime.status", jsonOut, &gatewayv1.ActionRequest{}))
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "json output")
	return cmd
}

func newRuntimePSCmd(repoRoot string) *cobra.Command {
	var jsonOut bool
	var sessionKey string
	var includeDetached bool

	cmd := &cobra.Command{
		Use:          "ps",
		Short:        "List live session runtimes",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitCodeToError(runAction(repoRoot, "runtime.ps", jsonOut, &gatewayv1.ActionRequest{
				SessionKey:      sessionKey,
				IncludeArchived: includeDetached,
			}))
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "json output")
	cmd.Flags().StringVar(&sessionKey, "session-key", "", "session filter")
	cmd.Flags().BoolVar(&includeDetached, "include-detached", false, "include detached entries")
	return cmd
}

func newRuntimeLogsCmd(repoRoot string) *cobra.Command {
	var jsonOut bool
	var follow bool

	cmd := &cobra.Command{
		Use:          "logs",
		Short:        "Show runtime logs",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := tryActionViaGRPC(repoRoot, &gatewayv1.ActionRequest{Action: "runtime.logs"})
			if err != nil {
				if jsonOut {
					printJSONActionError("runtime.logs", "gateway_unreachable", formatGatewayUnavailable(err))
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "runtime logs failed: %s\n", formatGatewayUnavailable(err))
				}
				return cliExitError{code: 1}
			}
			payload, err := decodeActionPayload(resp)
			if err != nil {
				if jsonOut {
					printJSONActionError("runtime.logs", "decode_failed", err.Error())
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "runtime logs failed: %v\n", err)
				}
				return cliExitError{code: 1}
			}
			if jsonOut {
				printJSON(payload)
				return nil
			}
			logFile := sessionctl.CleanString(payload["log_file"])
			if !follow {
				fmt.Fprintln(cmd.OutOrStdout(), logFile)
				return nil
			}
			return exitCodeToError(followFile(logFile))
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "json output")
	cmd.Flags().BoolVar(&follow, "follow", false, "follow logs")
	return cmd
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
