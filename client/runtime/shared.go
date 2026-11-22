package clientruntime

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

// shared-copy.json is a copy of web-app/src/lib/shared-copy.json (single
// source for the restore command and support lines; TestSharedCopyInSync).
//
//go:generate cp ../../web-app/src/lib/shared-copy.json shared-copy.json
//go:embed shared-copy.json
var sharedCopyJSON []byte

var shared struct {
	RestoreCommand map[string]string `json:"restoreCommand"`
	DebtDeduct     string            `json:"debtDeduct"`     // D33 switch
	DebtPrompt     string            `json:"debtPrompt"`     // D35 red prompt tail (CS on)
	DebtPromptOff  string            `json:"debtPromptOff"`  // D16 CS-off tail
	TryFirst       string            `json:"tryFirstPrompt"` // C40 / UX-WALK #14
	Support        struct {
		QQ    string   `json:"qq"`
		Email string   `json:"email"`
		Lines []string `json:"lines"`
	} `json:"support"`
}

func init() {
	if err := json.Unmarshal(sharedCopyJSON, &shared); err != nil {
		panic(err)
	}
}

// r6-gap U2: runtime support email from GET /api/status (relay_support_email).
// Until a successful fetch sets it (or when the field is missing), hasSupport
// and supportEmail fall back to build-time shared-copy support.email.
var (
	runtimeSupportMu    sync.RWMutex
	runtimeSupportEmail string
	runtimeSupportSet   bool
)

func setRuntimeSupportEmail(email string) {
	runtimeSupportMu.Lock()
	defer runtimeSupportMu.Unlock()
	runtimeSupportEmail = strings.TrimSpace(email)
	runtimeSupportSet = true
}

func resetRuntimeSupportEmail() {
	runtimeSupportMu.Lock()
	defer runtimeSupportMu.Unlock()
	runtimeSupportEmail = ""
	runtimeSupportSet = false
}

// supportEmail returns the admin-configured runtime email when fetched,
// otherwise the build-time shared-copy value.
func supportEmail() string {
	runtimeSupportMu.RLock()
	defer runtimeSupportMu.RUnlock()
	if runtimeSupportSet {
		return runtimeSupportEmail
	}
	return shared.Support.Email
}

// restoreCommand is the same generator as web-app/src/lib/shared-copy.ts.
func restoreCommand(goos, origin string) string {
	os := "mac"
	if goos == "windows" {
		os = "win"
	}
	return strings.ReplaceAll(shared.RestoreCommand[os], "{origin}", strings.TrimRight(origin, "/"))
}

// supportQQ is the 客服 QQ number (the one support channel for now).
func supportQQ() string { return strings.TrimSpace(shared.Support.QQ) }

// hasSupport: D27 — QQ or email present; both empty hides every 联系客服
// button. Email prefers the runtime /api/status value (U2).
func hasSupport() bool { return supportQQ() != "" || strings.TrimSpace(supportEmail()) != "" }

// supportButton / supportAction: 「联系客服」 when contact info exists, else the fallback (D30).
func supportButton(fallback string) string {
	if hasSupport() {
		return "联系客服"
	}
	return fallback
}

func supportAction(fallback string) string {
	if hasSupport() {
		return "support"
	}
	return fallback
}

// supportLine1: without real contact info the 加 QQ wording would point at nothing.
// D16: CS-off must not say 「联系客服」.
func supportLine1() string {
	if hasSupport() {
		return shared.Support.Lines[0]
	}
	return "余额用完了或遇到问题，开通续费后再用。"
}

// debtPromptTail picks CS-on / CS-off owed-balance prompt tail (D16).
func debtPromptTail() string {
	if hasSupport() {
		return shared.DebtPrompt
	}
	if shared.DebtPromptOff != "" {
		return shared.DebtPromptOff
	}
	return "续上后先扣"
}
