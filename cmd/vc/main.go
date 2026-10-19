package main

import (
	"fmt"
	"os"

	"videocompress/internal/cli"
)

func main() {
	deps, err := cli.DefaultDeps()
	if err != nil {
		fmt.Fprintf(os.Stderr, "초기화 실패: %v\n", err)
		os.Exit(cli.ExitUsage)
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, deps))
}
