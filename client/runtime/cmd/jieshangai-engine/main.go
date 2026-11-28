package main

import (
	"context"
	"fmt"
	rt "jieshangai/client/runtime"
	"os"
)

var version = "0.2.0-beta.13"

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--prepare-offline" {
		if err := rt.PrepareOffline(context.Background(), os.Args[2], func(line string) { fmt.Fprintln(os.Stderr, line) }); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--restore-on-uninstall" {
		if rt.RestoreForUninstall() != nil {
			os.Exit(1)
		}
		return
	}
	env, err := rt.DesktopEnv()
	if err != nil {
		os.Exit(1)
	}
	app := rt.NewDesktop(env, version)
	if app.ServeDesktop(os.Stdin, os.Stdout) != nil {
		os.Exit(1)
	}
}
