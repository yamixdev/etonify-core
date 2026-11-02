package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/sagernet/sing-box/experimental/rootmode"
)

var coreCommit = "unknown"
var moduleVersion = "0.1.1"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		abi := runtime.GOARCH
		if abi == "arm64" {
			abi = "arm64-v8a"
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"protocol": rootmode.Protocol, "moduleVersion": moduleVersion, "coreCommit": coreCommit, "abi": abi})
		return
	}
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		defer stderr.Close()
		os.Stderr = stderr
	}
	if err = execute(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		reason := "runtime_failed"
		if value, ok := err.(rootmode.Reason); ok {
			reason = string(value)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"ready": false, "protocol": rootmode.Protocol, "reason": reason})
		os.Exit(1)
	}
}

func execute(args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 {
		return rootmode.Reason("arguments_denied")
	}
	command := args[0]
	if command != "run" && command != "cleanup" {
		return rootmode.Reason("arguments_denied")
	}
	parser := flag.NewFlagSet(command, flag.ContinueOnError)
	parser.SetOutput(io.Discard)
	options := rootmode.RunOptions{}
	parser.IntVar(&options.UID, "uid", 0, "")
	parser.StringVar(&options.Socket, "socket", "", "")
	parser.StringVar(&options.WorkDir, "work-dir", rootmode.WorkDirectory, "")
	if parser.Parse(args[1:]) != nil || parser.NArg() != 0 {
		return rootmode.Reason("arguments_denied")
	}
	if command == "cleanup" {
		return rootmode.Cleanup(options)
	}
	_, identityErr := hex.DecodeString(coreCommit)
	if len(coreCommit) != 40 || identityErr != nil || strings.ToLower(coreCommit) != coreCommit {
		return rootmode.Reason("build_identity_missing")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return rootmode.Run(ctx, options, input, output)
}
