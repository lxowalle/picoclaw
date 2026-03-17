package pico

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/asr"
	asrService "github.com/sipeed/picoclaw/pkg/asr/service"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/identity"
	"github.com/sipeed/picoclaw/pkg/logger"
	ttsService "github.com/sipeed/picoclaw/pkg/tts/service"
)

// picoConn represents a single WebSocket connection.
type picoConn struct {
	id        string
	conn      *websocket.Conn
	sessionID string
	writeMu   sync.Mutex
	closed    atomic.Bool
}

// writeJSON sends a JSON message to the connection with write locking.
func (pc *picoConn) writeJSON(v any) error {
	if pc.closed.Load() {
		return fmt.Errorf("connection closed")
	}
	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()
	return pc.conn.WriteJSON(v)
}

// close closes the connection.
func (pc *picoConn) close() {
	if pc.closed.CompareAndSwap(false, true) {
		pc.conn.Close()
	}
}

// PicoChannel implements the native Pico Protocol WebSocket channel.
// It serves as the reference implementation for all optional capability interfaces.
type PicoChannel struct {
	*channels.BaseChannel
	config      config.PicoConfig
	appConfig   *config.Config
	upgrader    websocket.Upgrader
	connections sync.Map // connID → *picoConn
	connCount   atomic.Int32
	ctx         context.Context
	cancel      context.CancelFunc
	asrService  *asrService.Service
	ttsService  *ttsService.Service
}

// NewPicoChannel creates a new Pico Protocol channel.
func NewPicoChannel(cfg *config.Config, messageBus *bus.MessageBus) (*PicoChannel, error) {
	picoCfg := cfg.Channels.Pico
	if picoCfg.Token == "" {
		return nil, fmt.Errorf("pico token is required")
	}

	base := channels.NewBaseChannel("pico", picoCfg, messageBus, picoCfg.AllowFrom)

	allowOrigins := picoCfg.AllowOrigins
	checkOrigin := func(r *http.Request) bool {
		if len(allowOrigins) == 0 {
			return true // allow all if not configured
		}
		origin := r.Header.Get("Origin")
		for _, allowed := range allowOrigins {
			if allowed == "*" || allowed == origin {
				return true
			}
		}
		return false
	}

	channel := &PicoChannel{
		BaseChannel: base,
		config:      picoCfg,
		appConfig:   cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin:     checkOrigin,
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}

	return channel, nil
}

// SetASRService implements channels.ASRAware interface.
// It is called by Manager to inject the ASR service.
func (c *PicoChannel) SetASRService(svc *asrService.Service) {
	c.asrService = svc
	logger.InfoCF("pico", "ASR service injected", nil)
}

// SetTTSService implements channels.TTSAware interface.
// It is called by Manager to inject the TTS service.
func (c *PicoChannel) SetTTSService(svc *ttsService.Service) {
	c.ttsService = svc
	logger.InfoCF("pico", "TTS service injected", nil)
}

// Start implements Channel.
func (c *PicoChannel) Start(ctx context.Context) error {
	logger.InfoC("pico", "Starting Pico Protocol channel")
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.SetRunning(true)
	logger.InfoC("pico", "Pico Protocol channel started")
	return nil
}

// Stop implements Channel.
func (c *PicoChannel) Stop(ctx context.Context) error {
	logger.InfoC("pico", "Stopping Pico Protocol channel")
	c.SetRunning(false)

	// Close all connections
	c.connections.Range(func(key, value any) bool {
		if pc, ok := value.(*picoConn); ok {
			pc.close()
		}
		c.connections.Delete(key)
		return true
	})

	if c.cancel != nil {
		c.cancel()
	}

	logger.InfoC("pico", "Pico Protocol channel stopped")
	return nil
}

// WebhookPath implements channels.WebhookHandler.
func (c *PicoChannel) WebhookPath() string { return "/pico/" }

// ServeHTTP implements http.Handler for the shared HTTP server.
func (c *PicoChannel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/pico")

	switch {
	case path == "/ws" || path == "/ws/":
		c.handleWebSocket(w, r)
	default:
		http.NotFound(w, r)
	}
}

// Send implements Channel — sends a message to the appropriate WebSocket connection.
func (c *PicoChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}

	// Send text message
	outMsg := newMessage(TypeMessageCreate, map[string]any{
		"content": msg.Content,
	})
	if err := c.broadcastToSession(msg.ChatID, outMsg); err != nil {
		return err
	}

	// If TTS is enabled, synthesize and send audio
	if c.ttsService != nil && c.config.Audio.Enabled {
		go c.sendAudioResponse(msg.ChatID, msg.Content)
	}

	return nil
}

// sendAudioResponse synthesizes text to audio and sends it to the client.
func (c *PicoChannel) sendAudioResponse(chatID, text string) {
	if c.ttsService == nil {
		return
	}

	// Use provider's output format
	outputFormat := c.ttsService.OutputFormat()

	audioData, err := c.ttsService.Synthesize(c.ctx, text, "", outputFormat)
	if err != nil {
		logger.ErrorCF("pico", "TTS synthesis failed", map[string]any{
			"error": err.Error(),
		})
		return
	}

	// Encode audio data to base64
	audioBase64 := base64.StdEncoding.EncodeToString(audioData)

	// Send audio message
	audioMsg := newMessage(TypeAudioData, map[string]any{
		"data":   audioBase64,
		"format": string(outputFormat),
	})

	if err := c.broadcastToSession(chatID, audioMsg); err != nil {
		logger.ErrorCF("pico", "Failed to send audio", map[string]any{
			"error": err.Error(),
		})
	}
}

// EditMessage implements channels.MessageEditor.
func (c *PicoChannel) EditMessage(ctx context.Context, chatID string, messageID string, content string) error {
	outMsg := newMessage(TypeMessageUpdate, map[string]any{
		"message_id": messageID,
		"content":    content,
	})
	return c.broadcastToSession(chatID, outMsg)
}

// StartTyping implements channels.TypingCapable.
func (c *PicoChannel) StartTyping(ctx context.Context, chatID string) (func(), error) {
	startMsg := newMessage(TypeTypingStart, nil)
	if err := c.broadcastToSession(chatID, startMsg); err != nil {
		return func() {}, err
	}
	return func() {
		stopMsg := newMessage(TypeTypingStop, nil)
		c.broadcastToSession(chatID, stopMsg)
	}, nil
}

// SendPlaceholder implements channels.PlaceholderCapable.
// It sends a placeholder message via the Pico Protocol that will later be
// edited to the actual response via EditMessage (channels.MessageEditor).
func (c *PicoChannel) SendPlaceholder(ctx context.Context, chatID string) (string, error) {
	if !c.config.Placeholder.Enabled {
		return "", nil
	}

	text := c.config.Placeholder.Text
	if text == "" {
		text = "Thinking... 💭"
	}

	msgID := uuid.New().String()
	outMsg := newMessage(TypeMessageCreate, map[string]any{
		"content":    text,
		"message_id": msgID,
	})

	if err := c.broadcastToSession(chatID, outMsg); err != nil {
		return "", err
	}

	return msgID, nil
}

// broadcastToSession sends a message to all connections with a matching session.
func (c *PicoChannel) broadcastToSession(chatID string, msg PicoMessage) error {
	// chatID format: "pico:<sessionID>"
	sessionID := strings.TrimPrefix(chatID, "pico:")
	msg.SessionID = sessionID

	var sent bool
	c.connections.Range(func(key, value any) bool {
		pc, ok := value.(*picoConn)
		if !ok {
			return true
		}
		if pc.sessionID == sessionID {
			if err := pc.writeJSON(msg); err != nil {
				logger.DebugCF("pico", "Write to connection failed", map[string]any{
					"conn_id": pc.id,
					"error":   err.Error(),
				})
			} else {
				sent = true
			}
		}
		return true
	})

	if !sent {
		return fmt.Errorf("no active connections for session %s: %w", sessionID, channels.ErrSendFailed)
	}
	return nil
}

// handleWebSocket upgrades the HTTP connection and manages the WebSocket lifecycle.
func (c *PicoChannel) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !c.IsRunning() {
		http.Error(w, "channel not running", http.StatusServiceUnavailable)
		return
	}

	// Authenticate
	if !c.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Check connection limit
	maxConns := c.config.MaxConnections
	if maxConns <= 0 {
		maxConns = 100
	}
	if int(c.connCount.Load()) >= maxConns {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	// Echo the matched subprotocol back so the browser accepts the upgrade.
	var responseHeader http.Header
	if proto := c.matchedSubprotocol(r); proto != "" {
		responseHeader = http.Header{"Sec-WebSocket-Protocol": {proto}}
	}

	conn, err := c.upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		logger.ErrorCF("pico", "WebSocket upgrade failed", map[string]any{
			"error": err.Error(),
		})
		return
	}

	// Determine session ID from query param or generate one
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	pc := &picoConn{
		id:        uuid.New().String(),
		conn:      conn,
		sessionID: sessionID,
	}

	c.connections.Store(pc.id, pc)
	c.connCount.Add(1)

	logger.InfoCF("pico", "WebSocket client connected", map[string]any{
		"conn_id":    pc.id,
		"session_id": sessionID,
	})

	go c.readLoop(pc)
}

// authenticate checks the request for a valid token:
//  1. Authorization: Bearer <token> header
//  2. Sec-WebSocket-Protocol "token.<value>" (for browsers that can't set headers)
//  3. Query parameter "token" (only when AllowTokenQuery is on)
func (c *PicoChannel) authenticate(r *http.Request) bool {
	token := c.config.Token
	if token == "" {
		return false
	}

	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
		if after == token {
			return true
		}
	}

	// Check Sec-WebSocket-Protocol subprotocol ("token.<value>")
	if c.matchedSubprotocol(r) != "" {
		return true
	}

	// Check query parameter only when explicitly allowed
	if c.config.AllowTokenQuery {
		if r.URL.Query().Get("token") == token {
			return true
		}
	}

	return false
}

// matchedSubprotocol returns the "token.<value>" subprotocol that matches
// the configured token, or "" if none do.
func (c *PicoChannel) matchedSubprotocol(r *http.Request) string {
	token := c.config.Token
	for _, proto := range websocket.Subprotocols(r) {
		if after, ok := strings.CutPrefix(proto, "token."); ok && after == token {
			return proto
		}
	}
	return ""
}

// readLoop reads messages from a WebSocket connection.
func (c *PicoChannel) readLoop(pc *picoConn) {
	defer func() {
		pc.close()
		c.connections.Delete(pc.id)
		c.connCount.Add(-1)
		logger.InfoCF("pico", "WebSocket client disconnected", map[string]any{
			"conn_id":    pc.id,
			"session_id": pc.sessionID,
		})
	}()

	readTimeout := time.Duration(c.config.ReadTimeout) * time.Second
	if readTimeout <= 0 {
		readTimeout = 60 * time.Second
	}

	_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
	pc.conn.SetPongHandler(func(appData string) error {
		_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
		return nil
	})

	// Start ping ticker
	pingInterval := time.Duration(c.config.PingInterval) * time.Second
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	go c.pingLoop(pc, pingInterval)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		_, rawMsg, err := pc.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				logger.DebugCF("pico", "WebSocket read error", map[string]any{
					"conn_id": pc.id,
					"error":   err.Error(),
				})
			}
			return
		}

		_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))

		var msg PicoMessage
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			errMsg := newError("invalid_message", "failed to parse message")
			pc.writeJSON(errMsg)
			continue
		}

		c.handleMessage(pc, msg)
	}
}

// pingLoop sends periodic ping frames to keep the connection alive.
func (c *PicoChannel) pingLoop(pc *picoConn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if pc.closed.Load() {
				return
			}
			pc.writeMu.Lock()
			err := pc.conn.WriteMessage(websocket.PingMessage, nil)
			pc.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// handleMessage processes an inbound Pico Protocol message.
func (c *PicoChannel) handleMessage(pc *picoConn, msg PicoMessage) {
	switch msg.Type {
	case TypePing:
		pong := newMessage(TypePong, nil)
		pong.ID = msg.ID
		pc.writeJSON(pong)

	case TypeMessageSend:
		c.handleMessageSend(pc, msg)

	case TypeAudioChunk:
		c.handleAudioChunk(pc, msg)

	case TypeAudioStart:
		// Acknowledge audio stream start
		ack := newMessage(TypeAudioData, map[string]any{"status": "ready"})
		pc.writeJSON(ack)

	case TypeAudioStop:
		// Acknowledge audio stream end
		ack := newMessage(TypeAudioData, map[string]any{"status": "complete"})
		pc.writeJSON(ack)

	default:
		errMsg := newError("unknown_type", fmt.Sprintf("unknown message type: %s", msg.Type))
		pc.writeJSON(errMsg)
	}
}

// handleMessageSend processes an inbound message.send from a client.
func (c *PicoChannel) handleMessageSend(pc *picoConn, msg PicoMessage) {
	content, _ := msg.Payload["content"].(string)
	if strings.TrimSpace(content) == "" {
		errMsg := newError("empty_content", "message content is empty")
		pc.writeJSON(errMsg)
		return
	}

	sessionID := msg.SessionID
	if sessionID == "" {
		sessionID = pc.sessionID
	}

	chatID := "pico:" + sessionID
	senderID := "pico-user"

	peer := bus.Peer{Kind: "direct", ID: "pico:" + sessionID}

	metadata := map[string]string{
		"platform":   "pico",
		"session_id": sessionID,
		"conn_id":    pc.id,
	}

	logger.DebugCF("pico", "Received message", map[string]any{
		"session_id": sessionID,
		"preview":    truncate(content, 50),
	})

	sender := bus.SenderInfo{
		Platform:    "pico",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("pico", senderID),
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	c.HandleMessage(c.ctx, peer, msg.ID, senderID, chatID, content, nil, metadata, sender)
}

// handleAudioChunk processes an inbound audio chunk from a client.
func (c *PicoChannel) handleAudioChunk(pc *picoConn, msg PicoMessage) {
	if c.asrService == nil {
		errMsg := newError("asr_not_configured", "ASR service is not configured")
		pc.writeJSON(errMsg)
		return
	}

	// Extract audio data from payload
	audioData, ok := msg.Payload["data"].(string)
	if !ok {
		errMsg := newError("invalid_audio_data", "audio data is missing or invalid")
		pc.writeJSON(errMsg)
		return
	}

	// Decode base64 audio data
	audioBytes, err := base64.StdEncoding.DecodeString(audioData)
	if err != nil {
		errMsg := newError("audio_decode_error", fmt.Sprintf("failed to decode audio data: %v", err))
		pc.writeJSON(errMsg)
		return
	}

	// Get audio format from payload (default to opus)
	audioFormat := "opus"
	if format, ok := msg.Payload["format"].(string); ok && format != "" {
		audioFormat = format
	}

	// Perform speech recognition
	text, err := c.asrService.Recognize(c.ctx, audioBytes, asr.Format(audioFormat))
	if err != nil {
		errMsg := newError("asr_error", fmt.Sprintf("failed to recognize audio: %v", err))
		pc.writeJSON(errMsg)
		return
	}

	// If recognition produced text, treat it as a message
	if strings.TrimSpace(text) == "" {
		return
	}

	sessionID := msg.SessionID
	if sessionID == "" {
		sessionID = pc.sessionID
	}

	chatID := "pico:" + sessionID
	senderID := "pico-user"

	peer := bus.Peer{Kind: "direct", ID: "pico:" + sessionID}

	metadata := map[string]string{
		"platform":     "pico",
		"session_id":   sessionID,
		"conn_id":      pc.id,
		"audio_format": audioFormat,
	}

	logger.DebugCF("pico", "Received audio message", map[string]any{
		"session_id": sessionID,
		"text":       truncate(text, 50),
	})

	sender := bus.SenderInfo{
		Platform:    "pico",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("pico", senderID),
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	c.HandleMessage(c.ctx, peer, msg.ID, senderID, chatID, text, nil, metadata, sender)
}

// truncate truncates a string to maxLen runes.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
