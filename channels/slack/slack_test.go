package slack

import (
	"testing"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestStripBotMention(t *testing.T) {
	c := &Channel{botUserID: "U123"}

	tests := []struct {
		in   string
		want string
	}{
		{in: "<@U123> hello", want: "hello"},
		{in: "<@U123>: hello", want: "hello"},
		{in: "<@U123>, hello", want: "hello"},
		{in: "hello <@U123>", want: "hello <@U123>"},
		{in: "<@U999> hello", want: "<@U999> hello"},
	}

	for _, tt := range tests {
		if got := c.stripBotMention(tt.in); got != tt.want {
			t.Fatalf("stripBotMention(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAllowedByPolicy_DMAlwaysAllowed(t *testing.T) {
	c := &Channel{}
	if !c.allowedByPolicy("message", "D123", "im", "hi") {
		t.Fatal("expected dm to be allowed")
	}
	if !c.allowedByPolicy("message", "G123", "mpim", "hi") {
		t.Fatal("expected mpim to be allowed")
	}
}

func TestAllowedByPolicy_GroupOpen(t *testing.T) {
	c := &Channel{}
	c.cfg.GroupPolicy = "open"
	if !c.allowedByPolicy("message", "C123", "channel", "hi") {
		t.Fatal("expected group open to allow message")
	}
	if !c.allowedByPolicy("app_mention", "C123", "channel", "<@U1> hi") {
		t.Fatal("expected group open to allow app_mention")
	}
}

func TestAllowedByPolicy_GroupAllowlist(t *testing.T) {
	c := &Channel{}
	c.cfg.GroupPolicy = "allowlist"
	c.cfg.GroupAllowFrom = []string{"C123"}

	if !c.allowedByPolicy("message", "C123", "channel", "hi") {
		t.Fatal("expected allowlisted channel to be allowed")
	}
	if c.allowedByPolicy("message", "C999", "channel", "hi") {
		t.Fatal("expected non-allowlisted channel to be denied")
	}
}

func TestAllowedByPolicy_GroupMention(t *testing.T) {
	c := &Channel{}
	c.cfg.GroupPolicy = "mention"

	if !c.allowedByPolicy("app_mention", "C123", "channel", "<@U1> hi") {
		t.Fatal("expected app_mention to be allowed in mention policy")
	}
	if c.allowedByPolicy("message", "C123", "channel", "<@U1> hi") {
		t.Fatal("expected message to be denied in mention policy (dedup via app_mention)")
	}
}

func TestSlackThreadMeta(t *testing.T) {
	t.Run("from_delivery", func(t *testing.T) {
		threadTS, direct := slackThreadMeta(bus.OutboundMessage{
			Delivery: bus.Delivery{
				ThreadID: "1740000000.100",
			},
		})
		if threadTS != "1740000000.100" || direct {
			t.Fatalf("unexpected thread meta: thread_ts=%q direct=%v", threadTS, direct)
		}
	})

	t.Run("fallback_reply_to", func(t *testing.T) {
		threadTS, direct := slackThreadMeta(bus.OutboundMessage{
			ReplyTo: "1740000000.200",
		})
		if threadTS != "1740000000.200" || direct {
			t.Fatalf("unexpected thread meta: thread_ts=%q direct=%v", threadTS, direct)
		}
	})
}

func TestBuildSlackDelivery(t *testing.T) {
	t.Run("thread_fallback_to_ts", func(t *testing.T) {
		d := buildSlackDelivery("1740000000.300", "", "channel")
		if d.MessageID != "1740000000.300" || d.ThreadID != "1740000000.300" || d.IsDirect {
			t.Fatalf("unexpected delivery: %+v", d)
		}
	})

	t.Run("direct_chat", func(t *testing.T) {
		d := buildSlackDelivery("1740000000.400", "1740000000.401", "im")
		if !d.IsDirect || d.ThreadID != "1740000000.401" {
			t.Fatalf("unexpected delivery: %+v", d)
		}
	})
}

func TestSlackInboundAttachments(t *testing.T) {
	ev := &slackevents.MessageEvent{
		Message: &slack.Msg{
			Files: []slack.File{
				{
					ID:                 "F1",
					Name:               "photo.png",
					Mimetype:           "image/png",
					Size:               1234,
					URLPrivateDownload: "https://files.slack.com/files-pri/T/F/photo.png",
				},
				{
					ID:         "F2",
					Name:       "voice.mp3",
					Mimetype:   "audio/mpeg",
					Size:       99,
					URLPrivate: "https://files.slack.com/files-pri/T/F/voice.mp3",
				},
			},
		},
	}
	got := slackInboundAttachments(ev, "xoxb-test")
	if len(got) != 2 {
		t.Fatalf("attachments=%d", len(got))
	}
	if got[0].Kind != "image" || got[1].Kind != "audio" {
		t.Fatalf("unexpected kinds: %+v", got)
	}
	if got[0].Headers["Authorization"] == "" {
		t.Fatalf("missing auth header")
	}
	if got[1].URL == "" {
		t.Fatalf("missing url")
	}
}
