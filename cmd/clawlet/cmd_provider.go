package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mosaxiv/clawlet/llm"
	"github.com/urfave/cli/v3"
)

func cmdProvider() *cli.Command {
	return &cli.Command{
		Name:  "provider",
		Usage: "provider authentication utilities",
		Commands: []*cli.Command{
			{
				Name:      "login",
				Usage:     "authenticate an OAuth provider",
				ArgsUsage: "<provider>",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "device-code",
						Usage: "use OAuth device code flow (for headless environments)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() < 1 {
						return cli.Exit("usage: clawlet provider login <provider>", 2)
					}
					key := normalizeOAuthProvider(cmd.Args().Get(0))
					switch key {
					case "openai-codex":
						return loginOpenAICodex(ctx, cmd.Bool("device-code"))
					default:
						return cli.Exit(fmt.Sprintf("unsupported oauth provider: %s (supported: openai-codex)", cmd.Args().Get(0)), 1)
					}
				},
			},
		},
	}
}

func normalizeOAuthProvider(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "openai_codex", "codex":
		return "openai-codex"
	default:
		return v
	}
}

func loginOpenAICodex(ctx context.Context, useDeviceCode bool) error {
	if tok, err := llm.LoadCodexOAuthToken(); err == nil && tok.Valid() {
		fmt.Printf("already authenticated with OpenAI Codex (%s)\n", tok.AccountID)
		return nil
	}
	fmt.Println("starting OpenAI Codex OAuth login...")
	var err error
	if useDeviceCode {
		err = llm.LoginCodexOAuthDeviceCode(ctx)
	} else {
		err = llm.LoginCodexOAuthInteractive(ctx)
	}
	if err != nil {
		return err
	}
	tok, err := llm.LoadCodexOAuthToken()
	if err != nil {
		return err
	}
	fmt.Printf("authenticated with OpenAI Codex (%s)\n", tok.AccountID)
	return nil
}
