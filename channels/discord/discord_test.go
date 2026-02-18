package discord

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/mosaxiv/clawlet/bus"
)

func TestResolveDiscordReplyTarget(t *testing.T) {
	t.Run("prefer delivery reply id", func(t *testing.T) {
		got := resolveDiscordReplyTarget(bus.OutboundMessage{
			ReplyTo: "legacy",
			Delivery: bus.Delivery{
				ReplyToID: "typed",
			},
		})
		if got != "typed" {
			t.Fatalf("expected typed reply id, got %q", got)
		}
	})

	t.Run("fallback legacy reply_to", func(t *testing.T) {
		got := resolveDiscordReplyTarget(bus.OutboundMessage{
			ReplyTo: "legacy",
		})
		if got != "legacy" {
			t.Fatalf("expected legacy reply_to, got %q", got)
		}
	})
}

func TestBuildDiscordDelivery(t *testing.T) {
	t.Run("direct message with message reference", func(t *testing.T) {
		m := &discordgo.MessageCreate{
			Message: &discordgo.Message{
				ID:      "m1",
				GuildID: "",
				MessageReference: &discordgo.MessageReference{
					MessageID: "r1",
				},
			},
		}

		d := buildDiscordDelivery(m)
		if d.MessageID != "m1" || d.ReplyToID != "r1" || !d.IsDirect {
			t.Fatalf("unexpected delivery: %+v", d)
		}
	})

	t.Run("guild message with referenced message fallback", func(t *testing.T) {
		m := &discordgo.MessageCreate{
			Message: &discordgo.Message{
				ID:      "m2",
				GuildID: "g1",
				ReferencedMessage: &discordgo.Message{
					ID: "r2",
				},
			},
		}

		d := buildDiscordDelivery(m)
		if d.MessageID != "m2" || d.ReplyToID != "r2" || d.IsDirect {
			t.Fatalf("unexpected delivery: %+v", d)
		}
	})
}

func TestShouldRetryDiscordSend(t *testing.T) {
	t.Run("rate limit", func(t *testing.T) {
		err := &discordgo.RateLimitError{
			RateLimit: &discordgo.RateLimit{
				TooManyRequests: &discordgo.TooManyRequests{RetryAfter: 2 * time.Second},
				URL:             "/channels/1/messages",
			},
		}
		retry, wait := shouldRetryDiscordSend(err, 1)
		if !retry || wait != 2*time.Second {
			t.Fatalf("expected rate-limit retry, got retry=%v wait=%v", retry, wait)
		}
	})

	t.Run("rest 5xx", func(t *testing.T) {
		err := &discordgo.RESTError{
			Response: &http.Response{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway"},
		}
		retry, wait := shouldRetryDiscordSend(err, 2)
		if !retry || wait <= 0 {
			t.Fatalf("expected 5xx retry, got retry=%v wait=%v", retry, wait)
		}
	})

	t.Run("rest 4xx no retry", func(t *testing.T) {
		err := &discordgo.RESTError{
			Response: &http.Response{StatusCode: http.StatusBadRequest, Status: "400 Bad Request"},
		}
		retry, wait := shouldRetryDiscordSend(err, 1)
		if retry || wait != 0 {
			t.Fatalf("expected no retry, got retry=%v wait=%v", retry, wait)
		}
	})

	t.Run("context canceled no retry", func(t *testing.T) {
		retry, wait := shouldRetryDiscordSend(context.Canceled, 1)
		if retry || wait != 0 {
			t.Fatalf("expected no retry, got retry=%v wait=%v", retry, wait)
		}
	})
}

func TestDiscordInboundAttachments(t *testing.T) {
	msg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "a1",
					Filename:    "pic.jpg",
					ContentType: "image/jpeg",
					Size:        1024,
					URL:         "https://cdn.discordapp.com/attachments/pic.jpg",
				},
				{
					ID:          "a2",
					Filename:    "note.mp3",
					ContentType: "audio/mpeg",
					Size:        2048,
					URL:         "https://cdn.discordapp.com/attachments/note.mp3",
				},
			},
		},
	}
	got := discordInboundAttachments(msg)
	if len(got) != 2 {
		t.Fatalf("attachments=%d", len(got))
	}
	if got[0].Kind != "image" || got[1].Kind != "audio" {
		t.Fatalf("unexpected kinds: %+v", got)
	}
}
