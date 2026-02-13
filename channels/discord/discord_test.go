package discord

import (
	"testing"

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
