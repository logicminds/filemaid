package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/logicminds/filemaid/internal/actions"
)

func main() {
	dir := "/Users/opselite/projects/filemaid/tmpquerytest"
	path := filepath.Join(dir, "sample.txt")
	_ = os.WriteFile(path, []byte("hello"), 0644)

	fs := actions.NewOSFS()
	fs.SetTags(path, []string{"filemaid", "Documents"})
	fs.FlushMDImport()

	time.Sleep(500 * time.Millisecond)

	cmd := exec.Command("mdfind", "-onlyin", dir, "kMDItemUserTags == \"filemaid\"cd")
	out, err := cmd.CombinedOutput()
	fmt.Printf("query filemaid: err=%v out=%s\n", err, string(out))

	cmd = exec.Command("mdfind", "-onlyin", dir, "kMDItemUserTags == \"Documents\"cd")
	out, err = cmd.CombinedOutput()
	fmt.Printf("query Documents: err=%v out=%s\n", err, string(out))

	cmd = exec.Command("mdls", "-name", "kMDItemUserTags", path)
	out, err = cmd.CombinedOutput()
	fmt.Printf("mdls: err=%v out=%s\n", err, string(out))
}
