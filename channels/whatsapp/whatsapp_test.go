package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/mosaxiv/clawlet/bus"
)

func TestResolveWhatsAppReplyTarget(t *testing.T) {
	t.Run("prefer delivery reply id", func(t *testing.T) {
		got := resolveWhatsAppReplyTarget(bus.OutboundMessage{
			ReplyTo: "legacy",
			Delivery: bus.Delivery{
				ReplyToID: "typed",
			},
		})
		if got != "typed" {
			t.Fatalf("expected typed, got %q", got)
		}
	})

	t.Run("fallback legacy reply_to", func(t *testing.T) {
		got := resolveWhatsAppReplyTarget(bus.OutboundMessage{
			ReplyTo: "legacy",
		})
		if got != "legacy" {
			t.Fatalf("expected legacy, got %q", got)
		}
	})
}

func TestVerifyWhatsAppSignature(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	secret := "topsecret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sum := hex.EncodeToString(mac.Sum(nil))
	header := "sha256=" + sum

	if !verifyWhatsAppSignature(secret, body, header) {
		t.Fatal("expected valid signature")
	}
	if verifyWhatsAppSignature(secret, body, "sha256=deadbeef") {
		t.Fatal("expected invalid signature")
	}
	if !verifyWhatsAppSignature("", body, "") {
		t.Fatal("expected unsigned webhook to be accepted when appSecret is empty")
	}
}

func TestExtractWhatsAppInboundMessages(t *testing.T) {
	payload := whatsappWebhookPayload{
		Object: "whatsapp_business_account",
		Entry: []whatsappWebhookEntry{
			{
				Changes: []whatsappWebhookChange{
					{
						Field: "messages",
						Value: whatsappChangeValue{
							Messages: []whatsappInboundMessage{
								{
									From: "15551234567",
									ID:   "wamid.1",
									Type: "text",
									Text: whatsappInboundText{Body: "hello"},
								},
								{
									From: "15557654321",
									ID:   "wamid.2",
									Type: "interactive",
									Interactive: whatsappInteractive{
										Type: "button_reply",
										ButtonReply: whatsappInteractiveReply{
											Title: "confirm",
										},
									},
									Context: whatsappInboundContext{
										ID: "wamid.prev",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	got := extractWhatsAppInboundMessages(payload)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Content != "hello" || got[0].ChatID != "15551234567" {
		t.Fatalf("unexpected first message: %+v", got[0])
	}
	if got[1].Content != "confirm" || got[1].Delivery.ReplyToID != "wamid.prev" {
		t.Fatalf("unexpected second message: %+v", got[1])
	}
}

func TestShouldRetryWhatsAppSend(t *testing.T) {
	t.Run("retry on 429", func(t *testing.T) {
		retry, wait := shouldRetryWhatsAppSend(&whatsappHTTPError{
			StatusCode: 429,
		}, 1)
		if !retry || wait <= 0 {
			t.Fatalf("expected retry on 429, got retry=%v wait=%v", retry, wait)
		}
	})

	t.Run("no retry on 4xx", func(t *testing.T) {
		retry, wait := shouldRetryWhatsAppSend(&whatsappHTTPError{
			StatusCode: 400,
		}, 1)
		if retry || wait != 0 {
			t.Fatalf("expected no retry on 400, got retry=%v wait=%v", retry, wait)
		}
	})

	t.Run("no retry on context canceled", func(t *testing.T) {
		retry, wait := shouldRetryWhatsAppSend(context.Canceled, 1)
		if retry || wait != 0 {
			t.Fatalf("expected no retry, got retry=%v wait=%v", retry, wait)
		}
	})
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("2"); got != 2*time.Second {
		t.Fatalf("expected 2s, got %v", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Fatalf("expected 0, got %v", got)
	}
}
