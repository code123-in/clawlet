package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mosaxiv/clawlet/llm"
	"github.com/urfave/cli/v3"
)

func cmdModels() *cli.Command {
	return &cli.Command{
		Name:  "models",
		Usage: "list available models from the configured provider",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}

			client := &llm.Client{
				Provider: cfg.LLM.Provider,
				BaseURL:  cfg.LLM.BaseURL,
				APIKey:   cfg.LLM.APIKey,
				Headers:  cfg.LLM.Headers,
			}

			fmt.Printf("fetching models for provider: %s...\n", client.Provider)
			models, err := client.ListModels(ctx)
			if err != nil {
				return err
			}

			sort.Slice(models, func(i, j int) bool {
				return models[i].ID < models[j].ID
			})

			fmt.Printf("\n%-40s\n", "MODEL ID")
			fmt.Println(strings.Repeat("-", 40))
			for _, m := range models {
				fmt.Printf("%-40s\n", m.ID)
			}
			fmt.Printf("\ntotal: %d models\n", len(models))

			return nil
		},
	}
}
