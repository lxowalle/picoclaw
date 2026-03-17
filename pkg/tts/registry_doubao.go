//go:build tts_doubao || tts_all

package tts

import (
	"github.com/sipeed/picoclaw/pkg/tts/doubao"
)

func init() {
	// Register Doubao TTS provider with default configuration
	Register("doubao", doubao.New(doubao.DefaultConfig()))
}
