// Package clientruntime shares the 接上AI installation, authorization and
// reversible configuration engine between the legacy shell and Tauri desktop.
// Desktop uses private JSON Lines pipes; HTTP routes below serve only the
// legacy adapter. All writers retain the /i and /u configuration contract.
package clientruntime

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	version       = "0.1.0-beta"             // -X main.version
	defaultOrigin = "https://jieshangai.com" // -X main.defaultOrigin
)

const (
	lockPort = "47391"
)

//go:embed ui
var uiFS embed.FS

// View is what the page renders. Only user-facing copy lives here: never
// secrets, origins, paths or raw errors.
type View struct {
	Screen      string            `json:"screen"`
	Agent       string            `json:"agent,omitempty"`
	Name        string            `json:"name,omitempty"`
	Line        string            `json:"line,omitempty"`
	Extra       string            `json:"extra,omitempty"`
	Button      string            `json:"button,omitempty"`
	Action      string            `json:"action,omitempty"`
	Second      string            `json:"second,omitempty"` // grey secondary text button
	Second2     string            `json:"secondAction,omitempty"`
	Note        string            `json:"note,omitempty"`
	Busy        bool              `json:"busy,omitempty"`
	Cancellable bool              `json:"cancellable,omitempty"`
	Notice      string            `json:"notice,omitempty"` // desktop: success line shown as a toast on home
	Data        map[string]string `json:"data,omitempty"`
}

type TierOption struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Recommended bool   `json:"recommended,omitempty"`
}

type AgentStatus struct {
	Installed  bool         `json:"installed"`
	Configured bool         `json:"configured"`
	Running    bool         `json:"running"`
	Agent      string       `json:"agent"`
	Name       string       `json:"name"`
	Badge      string       `json:"badge"` // 能用了 / 不能用 / 暂时用不了 / 还没接 / 还没安装 (see badgeNoCLI, badgeUnconnected)
	State      string       `json:"state"` // ok / broken / none / nocli / stopped / disabled
	Button     string       `json:"button"`
	Action     string       `json:"action"`
	Note       string       `json:"note,omitempty"`
	Tier       string       `json:"tier,omitempty"`
	Tiers      []TierOption `json:"tiers,omitempty"`
}

type Balance struct {
	Text string `json:"text"`
	Low  bool   `json:"low"`
	// C41: prompt under the balance ("" = none); Red = the prompt is red (the balance line stays gray).
	Hint string `json:"hint,omitempty"`
	Red  bool   `json:"red,omitempty"`
}

type WorkspaceInfo struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	TeamID int    `json:"teamId,omitempty"`
}

type TodayUsageSummary struct {
	CostCNY  string `json:"costCny"`
	Requests int    `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

type UsageHeatmapDay struct {
	Date     string `json:"date"`
	Requests int    `json:"requests"`
	Tokens   int64  `json:"tokens"`
	CostCNY  string `json:"costCny"`
}

type UsageLogRow struct {
	ID               int    `json:"id"`
	CreatedAt        int64  `json:"createdAt"`
	Model            string `json:"model"`
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	CostCNY          string `json:"costCny"`
	UseTimeMs        int    `json:"useTimeMs"`
}

type UsageSnapshot struct {
	Range            string            `json:"range"`
	CostCNY          string            `json:"costCny"`
	Requests         int               `json:"requests"`
	Tokens           int64             `json:"tokens"`
	PromptTokens     int64             `json:"promptTokens"`
	CompletionTokens int64             `json:"completionTokens"`
	Heatmap          []UsageHeatmapDay `json:"heatmap"`
	Logs             []UsageLogRow     `json:"logs"`
}

type App struct {
	appVersion    string
	current       session
	desktop       bool
	desktopStates map[string]AgentStatus
	seenTickets   map[string]bool
	installMu     sync.Mutex
	installations map[string]*desktopInstallation
	updateMu      sync.Mutex
	toolUpdates   UpdateCheckState

	mu               sync.Mutex
	secret           string
	origin           string
	view             View
	statuses         []AgentStatus
	balance          *Balance
	walletAgent      string
	walletKey        string
	walletEpoch      uint64
	accountLabel     string
	workspace        *WorkspaceInfo
	today            *TodayUsageSummary
	usage            *UsageSnapshot
	catalogs         map[string]*DesktopCatalogPayload
	last             struct{ check, reason string }
	retries          map[string]int
	ticket           string // pending ticket for retry (never shown)
	clientAuthVal    string
	clientAuthCached bool
	env              Env
	opener           func() // bring UI to front
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newApp(env Env) *App {
	a := &App{appVersion: version, desktopStates: make(map[string]AgentStatus), seenTickets: make(map[string]bool), secret: randHex(16), origin: env.Origin, env: env, retries: map[string]int{}}
	a.view = View{Screen: "home"}
	return a
}

func agentName(agent string) string {
	if agent == "codex" {
		return "Codex"
	}
	return "Claude Code"
}

func validAgent(a string) bool { return a == "claude" || a == "codex" }

// ---------- HTTP ----------

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || r.URL.Query().Get("s") != a.secret {
			http.NotFound(w, r)
			return
		}
		b, _ := uiFS.ReadFile("ui/index.html")
		page := strings.Replace(string(b), "__SECRET__", a.secret, 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' data:; connect-src 'self'")
		_, _ = w.Write([]byte(page))
	})
	// VISUAL-SPEC §4: Figtree (Latin, OFL) embedded so the shell does not fall back.
	mux.HandleFunc("/figtree.woff2", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("ui/figtree.woff2")
		w.Header().Set("Content-Type", "font/woff2")
		_, _ = w.Write(b)
	})
	// Presence probe for the web download page (no data, no secret): lets the
	// page re-fire jieshangai://install by itself once the app is running.
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); a.webOriginOK(o) {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/state", a.guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.snapshot())
	}))
	mux.HandleFunc("/api/act", a.guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Action, Agent string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		a.act(req.Action, req.Agent)
		writeJSON(w, a.snapshot())
	}))
	// second-instance hand-off; guarded by the lock-file secret
	mux.HandleFunc("/api/forward", a.guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ URL string }
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req)
		if req.URL != "" {
			a.HandleURL(req.URL)
		}
		if a.opener != nil {
			go a.opener()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	return mux
}

func (a *App) webOriginOK(o string) bool {
	if o == "" {
		return false
	}
	if o == a.web() || o == a.origin || o == defaultOrigin || o == "https://www.jieshangai.com" {
		return true
	}
	// local web-app during beta / e2e
	return strings.HasPrefix(o, "http://127.0.0.1:") || strings.HasPrefix(o, "http://localhost:")
}

func (a *App) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "bad", http.StatusMethodNotAllowed)
			return
		}
		host := r.Host
		if host != "127.0.0.1:"+lockPort && host != "localhost:"+lockPort {
			http.Error(w, "bad", http.StatusForbidden)
			return
		}
		if r.Header.Get("X-S") != a.secret {
			http.Error(w, "bad", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type snapshot struct {
	View     View          `json:"view"`
	Statuses []AgentStatus `json:"statuses"`
	Balance  *Balance      `json:"balance,omitempty"`
	Version  string        `json:"version"`
	OS       string        `json:"os"`
	Diag     []string      `json:"diag"`
	Staff    staffInfo     `json:"staff"`
	// D27: false until a real support QQ or email exists → hide every 联系客服 button.
	Support bool `json:"support"`
	// Authorized is true when a desktop connection authorization is stored.
	// The authorization itself is never included.
	Authorized bool `json:"authorized,omitempty"`
}

type staffInfo struct {
	Install map[string]string `json:"install"`
	Restore string            `json:"restore"`
}

func (a *App) snapshot() snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return snapshot{
		View: a.view, Statuses: append([]AgentStatus(nil), a.statuses...), Balance: a.balance, Support: hasSupport(),
		Version: a.appVersion, OS: a.env.OSLabel, Diag: a.diagLocked(),
		Authorized: a.clientAuthLocked() != "",
		Staff: staffInfo{
			Install: map[string]string{"claude": installCommand(a.env.GOOS, a.origin, "claude"), "codex": installCommand(a.env.GOOS, a.origin, "codex")},
			Restore: restoreCommand(a.env.GOOS, a.origin),
		},
	}
}

func (a *App) setView(v View) {
	a.mu.Lock()
	a.view = v
	a.mu.Unlock()
}

func (a *App) getView() View {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.view
}

// ---------- deep link (M1) ----------

// ParseDeepLink accepts jieshangai://install?ticket=…&agent=claude|codex.
func ParseDeepLink(raw string) (ticket, agent string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "jieshangai") {
		return "", "", false
	}
	what := u.Host
	if what == "" {
		what = strings.Trim(u.Opaque+u.Path, "/")
	}
	if what != "install" {
		return "", "", false
	}
	q := u.Query()
	ticket, agent = strings.TrimSpace(q.Get("ticket")), strings.TrimSpace(q.Get("agent"))
	if !validAgent(agent) || !reTicket.MatchString(ticket) {
		return "", agent, false
	}
	return ticket, agent, true
}

// HandleURL is the single entry for argv / Apple event / forwarded links.
func (a *App) HandleURL(raw string) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "jieshangai:") {
		return
	}
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && (strings.EqualFold(u.Host, "open") || strings.Trim(u.Opaque+u.Path, "/") == "open") {
		// D32: 「已经装好了？打开接上AI」 just brings the app up on its home screen.
		if v := a.getView(); !v.Busy {
			a.setView(View{Screen: "home"})
			go a.refreshStatus(true)
		}
		return
	}
	ticket, agent, ok := ParseDeepLink(raw)
	if !ok {
		a.setView(errorView("noticket", agent))
		return
	}
	if v := a.getView(); v.Busy {
		return // one at a time (F5: serial)
	}
	a.mu.Lock()
	if ticket == a.ticket { // the page may fire the same one-time link twice
		a.mu.Unlock()
		return
	}
	a.ticket = ticket
	a.retries = map[string]int{}
	a.mu.Unlock()
	go a.connect(agent, ticket)
}

func (a *App) refreshLoop() {
	a.refreshStatus(true) // once on open; afterwards only the manual refresh icon
	_ = time.Second
}
