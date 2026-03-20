package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/yusefmosiah/fase/internal/service"
)

func newProjectCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Project-scoped operations",
	}

	var hydrateMode string
	var hydrateFormat string

	hydrateCmd := &cobra.Command{
		Use:   "hydrate",
		Short: "Compile a project-scoped briefing for cold-starting any session",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := service.Open(context.Background(), root.configPath)
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()
			result, err := svc.ProjectHydrate(context.Background(), service.ProjectHydrateRequest{
				Mode:   hydrateMode,
				Format: hydrateFormat,
			})
			if err != nil {
				return mapServiceError(err)
			}
			if hydrateFormat == "json" {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), service.RenderProjectHydrateMarkdown(result))
			return err
		},
	}
	hydrateCmd.Flags().StringVar(&hydrateMode, "mode", "standard", "hydration mode: thin, standard, or deep")
	hydrateCmd.Flags().StringVar(&hydrateFormat, "format", "markdown", "output format: markdown or json")

	cmd.AddCommand(hydrateCmd)
	return cmd
}
