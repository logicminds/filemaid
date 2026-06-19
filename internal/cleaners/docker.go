package cleaners

import (
	"strings"

	"github.com/logicminds/filemaid/internal/config"
)

var dockerCleaner = Cleaner{
	Name:   "docker",
	CanRun: dockerCanRun,
	Run:    dockerRun,
}

func dockerCanRun() bool {
	_, err := lookPath("docker")
	return err == nil
}

func dockerRun(dryRun bool, cfg *config.Config) CleanupResult {
	mode := "safe"
	if c, ok := cfg.DevCleanup["docker"]; ok && c.Mode != "" {
		mode = c.Mode
	}

	var cmd []string
	if mode == "aggressive" {
		cmd = []string{"docker", "system", "prune", "-af", "--volumes"}
	} else {
		cmd = []string{"docker", "image", "prune", "-f"}
	}
	command := strings.Join(cmd, " ")

	if dryRun {
		return CleanupResult{
			Name:    "docker",
			Status:  "dry-run",
			Detail:  "would run " + command,
			Command: command,
		}
	}

	out, err := runner.Run(cmd[0], cmd[1:]...)
	if err != nil {
		return CleanupResult{
			Name:    "docker",
			Status:  "failed",
			Detail:  err.Error(),
			Command: command,
		}
	}

	output := strings.TrimSpace(string(out))
	reclaimed := "0 B"
	var saved int64
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Total reclaimed space:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				reclaimed = strings.TrimSpace(parts[1])
				if s := ParseSize(reclaimed); s != nil {
					saved = *s
				}
			}
			break
		}
	}

	detail := output
	if detail == "" {
		detail = "ran " + command
	}

	return CleanupResult{
		Name:       "docker",
		Status:     "ok",
		Saved:      &saved,
		SavedHuman: reclaimed,
		Detail:     detail,
		Command:    command,
	}
}
