package cleaners

import (
	"os/exec"
	"time"
)

// commandRunner abstracts subprocess execution so tests can inject behavior.
type commandRunner interface {
	Run(name string, arg ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(name string, arg ...string) ([]byte, error) {
	return exec.Command(name, arg...).CombinedOutput()
}

// clock abstracts time so tests can inject a fixed instant.
type clock interface {
	Now() int64
}

type systemClock struct{}

func (systemClock) Now() int64 { return time.Now().Unix() }

var (
	runner   commandRunner = execRunner{}
	sysClock clock         = systemClock{}
	lookPath               = exec.LookPath
)
