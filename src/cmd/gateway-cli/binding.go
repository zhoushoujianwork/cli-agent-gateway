package main

import (
	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
	"github.com/spf13/cobra"
)

func newBindingCmd(repoRoot string) *cobra.Command {
	cmd := newGroupCmd("binding", "Manage explicit conversation-to-session bindings")
	cmd.AddCommand(
		newBindingCreateCmd(repoRoot),
		newBindingTargetCmd(repoRoot, "delete", "Delete a channel-to-session binding"),
		newBindingListCmd(repoRoot),
		newBindingTargetCmd(repoRoot, "show", "Show one binding"),
	)
	return cmd
}

func newBindingCreateCmd(repoRoot string) *cobra.Command {
	var channel string
	var conversationID string
	var threadID string
	var sessionKey string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a channel-to-session binding",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitCodeToError(runAction(repoRoot, "binding.create", jsonOut, &gatewayv1.ActionRequest{
				Channel:        channel,
				ConversationId: conversationID,
				ThreadId:       threadID,
				SessionKey:     sessionKey,
			}))
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "channel")
	cmd.Flags().StringVar(&conversationID, "conversation-id", "", "conversation id")
	cmd.Flags().StringVar(&threadID, "thread-id", "", "thread id")
	cmd.Flags().StringVar(&sessionKey, "session-key", "", "session key")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "json output")
	_ = cmd.MarkFlagRequired("channel")
	_ = cmd.MarkFlagRequired("conversation-id")
	_ = cmd.MarkFlagRequired("session-key")
	return cmd
}

func newBindingTargetCmd(repoRoot, name, short string) *cobra.Command {
	var channel string
	var conversationID string
	var threadID string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:          name,
		Short:        short,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitCodeToError(runAction(repoRoot, "binding."+name, jsonOut, &gatewayv1.ActionRequest{
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

func newBindingListCmd(repoRoot string) *cobra.Command {
	var channel string
	var sessionKey string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List channel-to-session bindings",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitCodeToError(runAction(repoRoot, "binding.list", jsonOut, &gatewayv1.ActionRequest{
				Channel:    channel,
				SessionKey: sessionKey,
			}))
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "channel filter")
	cmd.Flags().StringVar(&sessionKey, "session-key", "", "session key filter")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "json output")
	return cmd
}
