package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
)

func runChannelCommand(repoRoot string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "channel requires a subcommand")
		return 2
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list":
		fs := flag.NewFlagSet("channel list", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "channel.list", *jsonOut, &gatewayv1.ActionRequest{})
	case "inbox":
		fs := flag.NewFlagSet("channel inbox", flag.ContinueOnError)
		channel := fs.String("channel", "", "channel filter")
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "channel.inbox", *jsonOut, &gatewayv1.ActionRequest{
			Channel: *channel,
		})
	case "show":
		fs := flag.NewFlagSet("channel show", flag.ContinueOnError)
		channel := fs.String("channel", "", "channel")
		conversationID := fs.String("conversation-id", "", "conversation id")
		threadID := fs.String("thread-id", "", "thread id")
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "channel.show", *jsonOut, &gatewayv1.ActionRequest{
			Channel:        *channel,
			ConversationId: *conversationID,
			ThreadId:       *threadID,
		})
	default:
		fmt.Fprintf(os.Stderr, "unknown channel subcommand: %s\n", args[0])
		return 2
	}
}
