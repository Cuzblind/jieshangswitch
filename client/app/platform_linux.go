//go:build linux

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Linux is the dev / e2e host: deep links arrive in argv, UI opens in a browser.

func osLabel() string { return "Linux" }

func platformPrepare() {}

func platformRun(app *App, uiURL string) {
	app.SetOpener(func() { _ = openTarget(uiURL) })
	if os.Getenv("JIESHANGAI_PRINT_UI") == "1" {
		fmt.Println(uiURL)
	} else {
		app.OpenWindow()
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
}
