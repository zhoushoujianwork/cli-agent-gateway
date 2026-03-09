package main

import (
	"fmt"

	"cli-agent-gateway/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd(repoRoot string) *cobra.Command {
	var global bool
	var gatewayAddr string

	cmd := &cobra.Command{
		Use:          "config [workdir]",
		Short:        "Manage control-plane config under ~/.cag",
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if global {
				if len(args) > 0 {
					return cliExitError{code: 2}
				}
				path, err := config.WriteUserEnv(gatewayAddr)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "write ~/.cag/.env failed: %v\n", err)
					return cliExitError{code: 1}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "configured user env file: %s\n", path)
				return nil
			}

			workdir := resolveWorkdir(repoRoot, args)
			path, err := config.WriteDefaultEnv(repoRoot, workdir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "write ~/.cag/.env failed: %v\n", err)
				return cliExitError{code: 1}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "configured env file: %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "write user-level config to ~/.cag/.env")
	cmd.Flags().StringVar(&gatewayAddr, "gatewayd-addr", "", "gatewayd address for ~/.cag/.env (used with --global)")

	cmd.AddCommand(
		newConfigShowCmd(repoRoot),
		newConfigListCmd(repoRoot),
		newConfigGetCmd(repoRoot),
		newConfigSetCmd(repoRoot),
		newConfigUnsetCmd(repoRoot),
	)

	return cmd
}

func newConfigShowCmd(repoRoot string) *cobra.Command {
	return &cobra.Command{
		Use:          "show [key]",
		Short:        "Show effective config entries",
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return printConfigEntries(repoRoot, "show", cmd)
			}
			entry, err := config.Get(repoRoot, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "show config failed: %v\n", err)
				return cliExitError{code: 1}
			}
			printConfigEntry(cmd, entry)
			return nil
		},
	}
}

func newConfigListCmd(repoRoot string) *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List effective config entries",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return printConfigEntries(repoRoot, "list", cmd)
		},
	}
}

func newConfigGetCmd(repoRoot string) *cobra.Command {
	return &cobra.Command{
		Use:          "get <key>",
		Short:        "Show one effective config value",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, err := config.Get(repoRoot, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "get config failed: %v\n", err)
				return cliExitError{code: 1}
			}
			printConfigEntry(cmd, entry)
			return nil
		},
	}
}

func newConfigSetCmd(repoRoot string) *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:          "set <key> <value>",
		Short:        "Persist a config override",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, path, err := config.Set(repoRoot, args[0], args[1], global)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "set config failed: %v\n", err)
				return cliExitError{code: 1}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "configured %s in %s: %s=%s\n", entry.Scope, path, entry.Key, entry.Value)
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "write user-level config to ~/.cag/.env")
	return cmd
}

func newConfigUnsetCmd(repoRoot string) *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:          "unset <key>",
		Short:        "Remove a persisted config override",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, path, err := config.Unset(repoRoot, args[0], global)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "unset config failed: %v\n", err)
				return cliExitError{code: 1}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed override from %s: %s=%s (source=%s)\n", path, entry.Key, entry.Value, entry.Source)
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "write user-level config to ~/.cag/.env")
	return cmd
}

func printConfigEntries(repoRoot, action string, cmd *cobra.Command) error {
	entries, err := config.List(repoRoot)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s config failed: %v\n", action, err)
		return cliExitError{code: 1}
	}
	for _, entry := range entries {
		printConfigEntry(cmd, entry)
	}
	return nil
}

func printConfigEntry(cmd *cobra.Command, entry config.Entry) {
	fmt.Fprintf(cmd.OutOrStdout(), "%s=%s\t(scope=%s source=%s)\n", entry.Key, entry.Value, entry.Scope, entry.Source)
}
