package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type cliExitError struct {
	code int
}

func (e cliExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}

func executeRoot(repoRoot string, args []string) int {
	root := newRootCmd(repoRoot)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var exitErr cliExitError
		if errors.As(err, &exitErr) {
			return exitErr.code
		}
		message := strings.TrimSpace(err.Error())
		if message != "" {
			fmt.Fprintln(os.Stderr, message)
		}
		return 1
	}
	return 0
}

func newRootCmd(repoRoot string) *cobra.Command {
	root := &cobra.Command{
		Use:           "cag",
		Short:         "CLI Agent Gateway",
		SilenceErrors: true,
		SilenceUsage:  true,
		Long: strings.TrimSpace(`
Session-first local gateway CLI.

Primary product commands:
  cag config
  cag session ...
  cag channel ...
  cag binding ...
  cag runtime ...
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return cliExitError{code: 2}
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(
		newConfigCmd(repoRoot),
		newSessionCmd(repoRoot),
		newChannelCmd(repoRoot),
		newBindingCmd(repoRoot),
		newRuntimeCmd(repoRoot),
		newRawRootCmd("run", "Run the gateway runtime in the foreground", true, false, func(args []string) int {
			return runGoMain(repoRoot, args)
		}),
		newRawRootCmd("start", "Start the gateway runtime in the background", true, false, func(args []string) int {
			return runStart(repoRoot, args)
		}),
		newRawRootCmd("stop", "Stop the managed gateway runtime", true, false, func(args []string) int {
			return runStop(repoRoot, args)
		}),
		newRawRootCmd("restart", "Restart the managed gateway runtime", true, false, func(args []string) int {
			return runRestart(repoRoot, args)
		}),
		newRawRootCmd("status", "Show legacy runtime status", true, false, func(args []string) int {
			return runStatus(repoRoot, args)
		}),
		newRawRootCmd("health", "Run internal gateway health checks", true, false, func(args []string) int {
			return runHealth(repoRoot, args)
		}),
		newRawRootCmd("doctor", "Run internal gateway doctor checks", true, false, func(args []string) int {
			return runDoctor(repoRoot, args)
		}),
		newRawRootCmd("gatewayd", "Run the gateway control-plane server", true, false, func(args []string) int {
			return runGatewayd(repoRoot, args)
		}),
		newRawRootCmd("gatewayd-up", "Ensure the managed gateway control-plane is up", true, false, func(args []string) int {
			return runGatewaydUp(repoRoot, args)
		}),
		newRawRootCmd("gatewayd-down", "Stop the managed gateway control-plane", true, false, func(args []string) int {
			return runGatewaydDown(repoRoot, args)
		}),
		newRawRootCmd("users", "List recorded external users", true, false, func(args []string) int {
			return runUsers(repoRoot, args)
		}),
		newRawRootCmd("user-allow", "Mark an external user as allowed", true, false, func(args []string) int {
			return runUserAllow(repoRoot, args)
		}),
		newRawRootCmd("user-block", "Mark an external user as blocked", true, false, func(args []string) int {
			return runUserBlock(repoRoot, args)
		}),
		newRawRootCmd("send", "Legacy send command", true, false, func(args []string) int {
			return runSend(repoRoot, args)
		}),
		newLegacyShimCmd("sessions", func(args []string) int {
			return runDeprecatedCLI("sessions", "session list", hasFlag(args, "--json"))
		}),
		newLegacyShimCmd("messages", func(args []string) int {
			return runDeprecatedCLI("messages", "session messages --key <session_key>", hasFlag(args, "--json"))
		}),
		newLegacyShimCmd("session-clear", func(args []string) int {
			return runDeprecatedCLI("session-clear", "session clear --key <session_key>", hasFlag(args, "--json"))
		}),
		newLegacyShimCmd("session-new", func(args []string) int {
			return runDeprecatedCLI("session-new", "session create --key <session_key> --workdir <path>", hasFlag(args, "--json"))
		}),
		newLegacyShimCmd("session-delete", func(args []string) int {
			return runDeprecatedCLI("session-delete", "session delete --key <session_key>", hasFlag(args, "--json"))
		}),
		newLegacyShimCmd("sessions-delete-all", func(args []string) int {
			return runDeprecatedCLI("sessions-delete-all", "session delete --key <session_key> (repeat as needed)", hasFlag(args, "--json"))
		}),
		newActionsCmd(),
	)

	return root
}

func newGroupCmd(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:          use,
		Short:        short,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return cliExitError{code: 2}
		},
	}
}

func newRawRootCmd(use, short string, hidden bool, deprecated bool, run func(args []string) int) *cobra.Command {
	cmd := &cobra.Command{
		Use:                use,
		Short:              short,
		Hidden:             hidden,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wantsHelp(args) {
				_ = cmd.Help()
				return nil
			}
			return exitCodeToError(run(args))
		},
	}
	if deprecated {
		cmd.Deprecated = "legacy migration command"
	}
	return cmd
}

func newLegacyShimCmd(use string, run func(args []string) int) *cobra.Command {
	return newRawRootCmd(use, "Legacy compatibility command", true, false, run)
}

func newActionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "actions",
		Short:        "Print supported action names",
		Hidden:       true,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			names := make([]string, 0, len(cmd.Root().Commands()))
			for _, child := range cmd.Root().Commands() {
				if child.Name() == "completion" || child.Name() == "help" {
					continue
				}
				names = append(names, child.Name())
			}
			sort.Strings(names)
			for _, name := range names {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	}
}

func exitCodeToError(code int) error {
	if code == 0 {
		return nil
	}
	return cliExitError{code: code}
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		switch strings.TrimSpace(arg) {
		case "-h", "--help", "help":
			return true
		}
	}
	return false
}
