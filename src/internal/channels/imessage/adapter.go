package imessage

import (
	"fmt"

	"cli-agent-gateway/internal/core"
)

type Adapter struct{}

func NewAdapter() *Adapter { return &Adapter{} }

func (a *Adapter) Fetch() ([]core.InboundMessage, error) {
	return nil, fmt.Errorf("imessage adapter TODO: not implemented in Go yet")
}

func (a *Adapter) Send(text, to, messageID, reportFile string) error {
	_ = text
	_ = to
	_ = messageID
	_ = reportFile
	return fmt.Errorf("imessage adapter TODO: not implemented in Go yet")
}
