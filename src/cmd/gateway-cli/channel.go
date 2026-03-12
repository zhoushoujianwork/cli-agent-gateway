package main

import (
	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
	"github.com/spf13/cobra"
)

func newChannelCmd(repoRoot string) *cobra.Command {
	cmd := newGroupCmd("channel", "Inspect channel conversations and inbox")
	cmd.AddCommand(
		newChannelListCmd(repoRoot),
		newChannelToggleCmd(repoRoot, "enable", "Enable a configured channel"),
		newChannelToggleCmd(repoRoot, "disable", "Disable a configured channel"),
		newChannelInboxCmd(repoRoot),
		newChannelShowCmd(repoRoot),
	)
	return cmd
}

func newChannelListCmd(repoRoot string) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List supported channel entrypoints",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitCodeToError(runAction(repoRoot, "channel.list", jsonOut, &gatewayv1.ActionRequest{}))
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "json output")
	return cmd
}

func newChannelToggleCmd(repoRoot, name, short string) *cobra.Command {
	var channel string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:          name,
		Short:        short,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitCodeToError(runAction(repoRoot, "channel."+name, jsonOut, &gatewayv1.ActionRequest{
				Channel: channel,
			}))
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "channel")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "json output")
	_ = cmd.MarkFlagRequired("channel")
	return cmd
}

func newChannelInboxCmd(repoRoot string) *cobra.Command {
	var channel string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:          "inbox",
		Short:        "List unassigned channel conversations",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitCodeToError(runAction(repoRoot, "channel.inbox", jsonOut, &gatewayv1.ActionRequest{
				Channel: channel,
			}))
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "channel filter")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "json output")
	return cmd
}

func newChannelShowCmd(repoRoot string) *cobra.Command {
	var channel string
	var conversationID string
	var threadID string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:          "show",
		Short:        "Show one channel conversation",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitCodeToError(runAction(repoRoot, "channel.show", jsonOut, &gatewayv1.ActionRequest{
				Channel:        channel,
				ConversationId: conversationID,
				ThreadId:       threadID,
			}))
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "channel")
	cmd.Flags().StringVar(&conversationID, "conversation-id", "", "conversation id")
	cmd.Flags().StringVar(&threadID, "thread-id", "", "thread id")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "json output")
	_ = cmd.MarkFlagRequired("channel")
	_ = cmd.MarkFlagRequired("conversation-id")
	return cmd
}
