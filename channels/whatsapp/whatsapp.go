package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels"
	"github.com/mosaxiv/clawlet/config"
)

const (
	defaultWhatsAppBaseURL       = "https://graph.facebook.com"
	defaultWhatsAppAPIVersion    = "v24.0"
	defaultWhatsAppWebhookPath   = "/whatsapp/webhook"
	defaultWhatsAppWebhookListen = "127.0.0.1:18791"
)

type Channel struct {
	cfg   config.WhatsAppConfig
	bus   *bus.Bus
	allow channels.AllowList

	running atomic.Bool

	hc *http.Client

	mu     sync.Mutex
	cancel context.CancelFunc
	srv    *http.Server
}

func New(cfg config.WhatsAppConfig, b *bus.Bus) *Channel {
	return &Channel{
		cfg:   cfg,
		bus:   b,
		allow: channels.AllowList{AllowFrom: cfg.AllowFrom},
		hc: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *Channel) Name() string    { return "whatsapp" }
func (c *Channel) IsRunning() bool { return c.running.Load() }

func (c *Channel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.AccessToken) == "" {
		return fmt.Errorf("whatsapp accessToken is empty")
	}
	if strings.TrimSpace(c.cfg.PhoneNumberID) == "" {
		return fmt.Errorf("whatsapp phoneNumberId is empty")
	}
	if strings.TrimSpace(c.cfg.VerifyToken) == "" {
		return fmt.Errorf("whatsapp verifyToken is empty")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.cancel = nil
		c.mu.Unlock()
	}()

	mux := http.NewServeMux()
	mux.HandleFunc(normalizeWebhookPath(c.cfg.WebhookPath), c.handleWebhook)

	srv := &http.Server{
		Addr:              webhookListen(c.cfg.WebhookListen),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	c.mu.Lock()
	c.srv = srv
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.srv == srv {
			c.srv = nil
		}
		c.mu.Unlock()
	}()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	c.running.Store(true)
	defer c.running.Store(false)

	select {
	case <-runCtx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
		return runCtx.Err()
	case err := <-errCh:
		return err
	}
}

func (c *Channel) Stop() error {
	c.mu.Lock()
	cancel := c.cancel
	srv := c.srv
	c.cancel = nil
	c.srv = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if srv != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	to := strings.TrimSpace(msg.ChatID)
	if to == "" {
		return fmt.Errorf("chat_id is empty")
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return nil
	}

	req := whatsappSendRequest{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               to,
		Type:             "text",
		Text: &whatsappText{
			Body:       content,
			PreviewURL: false,
		},
	}
	if replyID := resolveWhatsAppReplyTarget(msg); replyID != "" {
		req.Context = &whatsappContext{MessageID: replyID}
	}
	return c.sendMessage(ctx, req)
}

func (c *Channel) sendMessage(ctx context.Context, payload whatsappSendRequest) error {
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := c.sendMessageOnce(ctx, payload)
		if err == nil {
			return nil
		}
		retry, wait := shouldRetryWhatsAppSend(err, attempt)
		if !retry || attempt == maxAttempts {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func (c *Channel) sendMessageOnce(ctx context.Context, payload whatsappSendRequest) error {
	baseURL := strings.TrimRight(strings.TrimSpace(c.cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultWhatsAppBaseURL
	}
	version := strings.TrimSpace(c.cfg.APIVersion)
	if version == "" {
		version = defaultWhatsAppAPIVersion
	}
	endpoint := baseURL + "/" + version + "/" + strings.TrimSpace(c.cfg.PhoneNumberID) + "/messages"

	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.cfg.AccessToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &whatsappHTTPError{
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			Body:       strings.TrimSpace(string(raw)),
		}
	}
	return nil
}

func (c *Channel) handleWebhook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c.handleVerify(w, r)
	case http.MethodPost:
		c.handleInbound(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (c *Channel) handleVerify(w http.ResponseWriter, r *http.Request) {
	verifyToken := strings.TrimSpace(c.cfg.VerifyToken)
	mode := strings.TrimSpace(r.URL.Query().Get("hub.mode"))
	token := strings.TrimSpace(r.URL.Query().Get("hub.verify_token"))
	challenge := r.URL.Query().Get("hub.challenge")

	if mode != "subscribe" || token == "" || token != verifyToken {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, challenge)
}

func (c *Channel) handleInbound(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !verifyWhatsAppSignature(strings.TrimSpace(c.cfg.AppSecret), body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var payload whatsappWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	events := extractWhatsAppInboundMessages(payload)
	for _, evt := range events {
		if !c.allow.Allowed(evt.SenderID) {
			continue
		}
		publishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.bus.PublishInbound(publishCtx, bus.InboundMessage{
			Channel:    "whatsapp",
			SenderID:   evt.SenderID,
			ChatID:     evt.ChatID,
			Content:    evt.Content,
			SessionKey: "whatsapp:" + evt.ChatID,
			Delivery:   evt.Delivery,
		})
		cancel()
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func webhookListen(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return defaultWhatsAppWebhookListen
	}
	return v
}

func normalizeWebhookPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return defaultWhatsAppWebhookPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func resolveWhatsAppReplyTarget(msg bus.OutboundMessage) string {
	candidates := []string{
		strings.TrimSpace(msg.Delivery.ReplyToID),
		strings.TrimSpace(msg.ReplyTo),
	}
	for _, v := range candidates {
		if v != "" {
			return v
		}
	}
	return ""
}

func verifyWhatsAppSignature(appSecret string, body []byte, header string) bool {
	appSecret = strings.TrimSpace(appSecret)
	if appSecret == "" {
		// Allow unsigned webhooks when appSecret is not configured.
		return true
	}
	const prefix = "sha256="
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(strings.ToLower(header), prefix) {
		return false
	}
	givenHex := strings.TrimSpace(header[len(prefix):])
	given, err := hex.DecodeString(givenHex)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	want := mac.Sum(nil)
	if len(given) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(given, want) == 1
}

func shouldRetryWhatsAppSend(err error, attempt int) (bool, time.Duration) {
	if err == nil {
		return false, 0
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, 0
	}

	var httpErr *whatsappHTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == http.StatusTooManyRequests {
			if httpErr.RetryAfter > 0 {
				return true, httpErr.RetryAfter
			}
			return true, whatsappSendBackoff(attempt)
		}
		if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
			return true, whatsappSendBackoff(attempt)
		}
		return false, 0
	}

	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true, whatsappSendBackoff(attempt)
	}

	return false, 0
}

func whatsappSendBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 4)
	return 300 * time.Millisecond * time.Duration(1<<shift)
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	at, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	d := time.Until(at)
	if d < 0 {
		return 0
	}
	return d
}

func extractWhatsAppInboundMessages(payload whatsappWebhookPayload) []whatsappInboundEvent {
	out := make([]whatsappInboundEvent, 0, 8)
	if payload.Object != "whatsapp_business_account" {
		return out
	}

	for _, entry := range payload.Entry {
		for _, ch := range entry.Changes {
			if strings.TrimSpace(ch.Field) != "messages" {
				continue
			}
			for _, msg := range ch.Value.Messages {
				content := whatsappInboundContent(msg)
				if content == "" {
					continue
				}

				sender := strings.TrimSpace(msg.From)
				if sender == "" {
					continue
				}
				out = append(out, whatsappInboundEvent{
					SenderID: sender,
					ChatID:   sender,
					Content:  content,
					Delivery: bus.Delivery{
						MessageID: strings.TrimSpace(msg.ID),
						ReplyToID: strings.TrimSpace(msg.Context.ID),
						IsDirect:  true,
					},
				})
			}
		}
	}

	return out
}

func whatsappInboundContent(msg whatsappInboundMessage) string {
	msgType := strings.TrimSpace(msg.Type)
	switch msgType {
	case "text":
		return strings.TrimSpace(msg.Text.Body)
	case "button":
		if txt := strings.TrimSpace(msg.Button.Text); txt != "" {
			return txt
		}
		return strings.TrimSpace(msg.Button.Payload)
	case "interactive":
		switch strings.TrimSpace(msg.Interactive.Type) {
		case "button_reply":
			if t := strings.TrimSpace(msg.Interactive.ButtonReply.Title); t != "" {
				return t
			}
			return strings.TrimSpace(msg.Interactive.ButtonReply.ID)
		case "list_reply":
			if t := strings.TrimSpace(msg.Interactive.ListReply.Title); t != "" {
				return t
			}
			return strings.TrimSpace(msg.Interactive.ListReply.ID)
		}
	case "image":
		caption := strings.TrimSpace(msg.Image.Caption)
		if caption != "" {
			return "[Image] " + caption
		}
		return "[Image]"
	case "video":
		caption := strings.TrimSpace(msg.Video.Caption)
		if caption != "" {
			return "[Video] " + caption
		}
		return "[Video]"
	case "document":
		caption := strings.TrimSpace(msg.Document.Caption)
		if caption != "" {
			return "[Document] " + caption
		}
		name := strings.TrimSpace(msg.Document.Filename)
		if name != "" {
			return "[Document] " + name
		}
		return "[Document]"
	case "audio":
		return "[Voice Message]"
	case "reaction":
		if emoji := strings.TrimSpace(msg.Reaction.Emoji); emoji != "" {
			return "[Reaction] " + emoji
		}
		return "[Reaction]"
	}
	return ""
}

type whatsappSendRequest struct {
	MessagingProduct string           `json:"messaging_product"`
	RecipientType    string           `json:"recipient_type,omitempty"`
	To               string           `json:"to"`
	Type             string           `json:"type"`
	Text             *whatsappText    `json:"text,omitempty"`
	Context          *whatsappContext `json:"context,omitempty"`
}

type whatsappText struct {
	Body       string `json:"body"`
	PreviewURL bool   `json:"preview_url,omitempty"`
}

type whatsappContext struct {
	MessageID string `json:"message_id"`
}

type whatsappWebhookPayload struct {
	Object string                 `json:"object"`
	Entry  []whatsappWebhookEntry `json:"entry"`
}

type whatsappWebhookEntry struct {
	Changes []whatsappWebhookChange `json:"changes"`
}

type whatsappWebhookChange struct {
	Field string              `json:"field"`
	Value whatsappChangeValue `json:"value"`
}

type whatsappChangeValue struct {
	Messages []whatsappInboundMessage `json:"messages"`
}

type whatsappInboundMessage struct {
	From        string                 `json:"from"`
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Text        whatsappInboundText    `json:"text"`
	Button      whatsappInboundButton  `json:"button"`
	Interactive whatsappInteractive    `json:"interactive"`
	Image       whatsappMediaPayload   `json:"image"`
	Video       whatsappMediaPayload   `json:"video"`
	Document    whatsappDocument       `json:"document"`
	Reaction    whatsappInboundReact   `json:"reaction"`
	Context     whatsappInboundContext `json:"context"`
}

type whatsappInboundText struct {
	Body string `json:"body"`
}

type whatsappInboundButton struct {
	Text    string `json:"text"`
	Payload string `json:"payload"`
}

type whatsappInteractive struct {
	Type        string                   `json:"type"`
	ButtonReply whatsappInteractiveReply `json:"button_reply"`
	ListReply   whatsappInteractiveReply `json:"list_reply"`
}

type whatsappInteractiveReply struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type whatsappMediaPayload struct {
	Caption string `json:"caption"`
}

type whatsappDocument struct {
	Caption  string `json:"caption"`
	Filename string `json:"filename"`
}

type whatsappInboundReact struct {
	Emoji string `json:"emoji"`
}

type whatsappInboundContext struct {
	ID string `json:"id"`
}

type whatsappInboundEvent struct {
	SenderID string
	ChatID   string
	Content  string
	Delivery bus.Delivery
}

type whatsappHTTPError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *whatsappHTTPError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("whatsapp send status %d: %s", e.StatusCode, e.Body)
}
