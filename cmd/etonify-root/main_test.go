package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLIRejectsUnknownCommandsAndArgumentsWithoutOutput(t *testing.T) {
	for _, args := range [][]string{nil, {"shell"}, {"run", "--unknown"}, {"cleanup", "positional"}} {
		var output bytes.Buffer
		if err := execute(args, strings.NewReader("{}\n"), &output); err == nil || err.Error() != "arguments_denied" || output.Len() != 0 {
			t.Fatalf("args %v accepted or leaked output: %v %s", args, err, &output)
		}
	}
}

func TestCLIRunRequiresPinnedHexBuildIdentity(t *testing.T) {
	previous := coreCommit
	defer func() { coreCommit = previous }()
	for _, invalid := range []string{"unknown", strings.Repeat("z", 40), strings.Repeat("A", 40), strings.Repeat("0", 39)} {
		coreCommit = invalid
		var output bytes.Buffer
		if err := execute([]string{"run"}, strings.NewReader("{}\n"), &output); err == nil || err.Error() != "build_identity_missing" {
			t.Fatalf("invalid build identity %q accepted: %v", invalid, err)
		}
	}
}
