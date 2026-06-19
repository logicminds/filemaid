package cli

import (
	"bufio"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var logsTail int

func init() {
	logsCmd.Flags().IntVar(&logsTail, "tail", 20, "number of lines to show")
	rootCmd.AddCommand(logsCmd)
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Tail the log file",
	Long:  "Print the last N lines of the filemaid log file.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return tailLogs(cfg.LogPath, logsTail)
	},
}

// tailLogs prints the last n lines of path. It mirrors the Python tail_logs
// behaviour, printing "log file not found" when the file does not exist.
func tailLogs(path string, n int) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("log file not found")
			return nil
		}
		return err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	start := 0
	if len(lines) > n {
		start = len(lines) - n
	}
	for _, line := range lines[start:] {
		fmt.Println(line)
	}
	return nil
}
