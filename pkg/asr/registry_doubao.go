//go:build asr_doubao || asr_all

package asr

import (
	"github.com/sipeed/picoclaw/pkg/asr/doubao"
)

func init() {
	// Register Doubao ASR provider with default configuration
	Register("doubao", doubao.New(doubao.DefaultConfig()))
}
