package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
)

func runBindingCommand(repoRoot string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "binding requires a subcommand")
		return 2
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "create":
		fs := flag.NewFlagSet("binding create", flag.ContinueOnError)
		channel := fs.String("channel", "", "channel")
		conversationID := fs.String("conversation-id", "", "conversation id")
		threadID := fs.String("thread-id", "", "thread id")
		sessionKey := fs.String("session-key", "", "session key")
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "binding.create", *jsonOut, &gatewayv1.ActionRequest{
			Channel:        *channel,
			ConversationId: *conversationID,
			ThreadId:       *threadID,
			SessionKey:     *sessionKey,
		})
	case "delete", "show":
		fs := flag.NewFlagSet("binding "+args[0], flag.ContinueOnError)
		channel := fs.String("channel", "", "channel")
		conversationID := fs.String("conversation-id", "", "conversation id")
		threadID := fs.String("thread-id", "", "thread id")
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "binding."+strings.ToLower(strings.TrimSpace(args[0])), *jsonOut, &gatewayv1.ActionRequest{
			Channel:        *channel,
			ConversationId: *conversationID,
			ThreadId:       *threadID,
		})
	case "list":
		fs := flag.NewFlagSet("binding list", flag.ContinueOnError)
		channel := fs.String("channel", "", "channel filter")
		sessionKey := fs.String("session-key", "", "session key filter")
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "binding.list", *jsonOut, &gatewayv1.ActionRequest{
			Channel:    *channel,
			SessionKey: *sessionKey,
		})
	default:
		fmt.Fprintf(os.Stderr, "unknown binding subcommand: %s\n", args[0])
		return 2
	}
}
