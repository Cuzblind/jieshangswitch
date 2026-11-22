//go:build darwin

package main

import (
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// macOS shell without cgo: AppKit via the Objective-C runtime (purego).
// jieshangai:// arrives as a kAEGetURL Apple event (Info.plist CFBundleURLTypes),
// the same mechanism Tauri/Wails/cc-switch rely on. The page opens in the
// default browser; clicking the Dock icon again reopens it.

func init() { runtime.LockOSThread() } // AppKit must own the main thread

func osLabel() string {
	out, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return "macOS"
	}
	return "macOS " + strings.TrimSpace(string(out))
}

func platformPrepare() {} // URL scheme comes from Info.plist; LaunchServices registers it

const (
	kInternetEventClass  = 0x4755524c // 'GURL'
	kAEGetURL            = 0x4755524c // 'GURL'
	kCoreEventClass      = 0x61657674 // 'aevt'
	kAEReopenApplication = 0x72617070 // 'rapp'
	keyDirectObject      = 0x2d2d2d2d // '----'
)

func goString(p uintptr) string {
	if p == 0 {
		return ""
	}
	var b []byte
	for i := uintptr(0); ; i++ {
		c := *(*byte)(unsafe.Pointer(p + i))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

func platformRun(app *App, uiURL string) {
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
		openTarget(uiURL)
		select {}
	}
	open := func() { _ = openTarget(uiURL) }
	app.SetOpener(open)

	sel := objc.RegisterName
	cls, err := objc.RegisterClass("JSAEventHandler", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: sel("handleURL:withReply:"), Fn: func(self objc.ID, _ objc.SEL, ev objc.ID, reply objc.ID) {
			desc := ev.Send(sel("paramDescriptorForKeyword:"), uint32(keyDirectObject))
			str := desc.Send(sel("stringValue"))
			u := goString(objc.Send[uintptr](str, sel("UTF8String")))
			go func() { app.HandleURL(u); open() }()
		}},
		{Cmd: sel("handleReopen:withReply:"), Fn: func(self objc.ID, _ objc.SEL, ev objc.ID, reply objc.ID) {
			go open()
		}},
	})
	if err != nil {
		open()
		select {}
	}
	handler := objc.ID(cls).Send(sel("new"))
	nsapp := objc.ID(objc.GetClass("NSApplication")).Send(sel("sharedApplication"))
	nsapp.Send(sel("setActivationPolicy:"), int64(0)) // regular: Dock icon, Quit from Dock
	mgr := objc.ID(objc.GetClass("NSAppleEventManager")).Send(sel("sharedAppleEventManager"))
	mgr.Send(sel("setEventHandler:andSelector:forEventClass:andEventID:"), handler, sel("handleURL:withReply:"), uint32(kInternetEventClass), uint32(kAEGetURL))
	mgr.Send(sel("setEventHandler:andSelector:forEventClass:andEventID:"), handler, sel("handleReopen:withReply:"), uint32(kCoreEventClass), uint32(kAEReopenApplication))

	// Plain launch (no link within a moment): just show the page.
	go func() {
		time.Sleep(1200 * time.Millisecond)
		if app.CurrentView().Screen == "home" {
			open()
		}
	}()
	nsapp.Send(sel("run"))
}
