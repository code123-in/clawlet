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
		Usage: "list or probe available models from the configured provider",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "probe",
				Aliases: []string{"p"},
				Usage:   "probe a specific model ID to see if it works",
			},
		},
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

			probeID := cmd.String("probe")
			if probeID != "" {
				fmt.Printf("probing model '%s' for provider: %s...\n", probeID, client.Provider)
				info, err := client.ProbeModel(ctx, probeID)
				
				statusStr := "Unknown"
				switch info.Status {
				case "ok":
					statusStr = "OK (Found)"
				case "not_found":
					statusStr = "Not Found (404)"
				case "error":
					statusStr = fmt.Sprintf("Error: %v", err)
				}

				fmt.Printf("\n%-40s %-20s\n", "MODEL ID", "STATUS")
				fmt.Println(strings.Repeat("-", 61))
				fmt.Printf("%-40s %-20s\n", info.ID, statusStr)
				return nil
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
			fmt.Println("\nTip: use --probe <model_id> to test if a specific name works.")

			return nil
		},
	}
}
