package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Print resolved configuration",
	Long:  "Load the configuration (defaults merged with ~/.config/filemaid/config.json) and print it as JSON.",
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	},
}
