package clientruntime

// Remote reads bypass the action queue. Bind every result to its connection,
// wallet generation and request order before admitting it into shared state.
type desktopRead struct {
	agent, secret   string
	epoch, sequence uint64
	usage           bool
}

// Reserve in admission order without waiting for configuration or network I/O.
func (a *App) reserveDesktopRead(agent string, usage bool) desktopRead {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := desktopRead{agent: agent, epoch: a.walletEpoch, usage: usage}
	if usage {
		r.agent = a.walletAgent
		a.usageRequest++
		r.sequence = a.usageRequest
	} else {
		if a.catalogRequests == nil {
			a.catalogRequests = make(map[string]uint64)
		}
		a.catalogRequests[agent]++
		r.sequence = a.catalogRequests[agent]
	}
	return r
}

func (a *App) reserveDesktopRequest(r DesktopRequest) DesktopRequest {
	if r.read.sequence != 0 {
		return r
	}
	if r.Action == "usage-load" {
		r.read = a.reserveDesktopRead("", true)
	} else if r.Action == "catalog-load" && validAgent(r.Agent) {
		r.read = a.reserveDesktopRead(r.Agent, false)
	}
	return r
}

func (a *App) beginDesktopRead(r desktopRead, expectedSecret string) desktopRead {
	a.connectionMu.Lock()
	defer a.connectionMu.Unlock()
	a.mu.Lock()
	current := r.epoch == a.walletEpoch
	a.mu.Unlock()
	if !current {
		return r // this request predates a connection change; never bind its new key
	}
	// Before the first balance refresh there is no selected wallet yet.
	if r.usage && r.agent == "" {
		for _, candidate := range []string{"claude", "codex"} {
			if secret, ok := a.env.Configured(candidate, a.origin); ok && secret != "" {
				r.agent, r.secret = candidate, secret
				break
			}
		}
	} else if secret, ok := a.env.Configured(r.agent, a.origin); ok {
		r.secret = secret
	}
	if expectedSecret != "" && expectedSecret != r.secret {
		r.secret = ""
	}
	return r
}

// Caller holds connectionMu, preventing a config write/restore between this
// check and the cache/native-menu write. a.mu is never held over HTTP or I/O.
func (a *App) applyDesktopRead(r desktopRead, native bool, apply func(latest bool)) bool {
	secret, _ := a.env.Configured(r.agent, a.origin)
	a.mu.Lock()
	defer a.mu.Unlock()
	sequence := a.catalogRequests[r.agent]
	if r.usage {
		sequence = a.usageRequest
	}
	latest := r.sequence == sequence
	if r.secret != secret || r.epoch != a.walletEpoch || (!native && !latest) {
		return false
	}
	apply(latest)
	return true
}

// Called with connectionMu held, even after a partial failed write/restore:
// data from before the attempt must not survive a changed local connection.
func (a *App) invalidateConnectionData() {
	a.mu.Lock()
	a.walletEpoch++
	a.usage = nil
	a.catalogs = nil
	a.skillCatalog = nil
	a.mu.Unlock()
}

func (a *App) writeConnection(agent, secret string) error {
	a.connectionMu.Lock()
	defer a.connectionMu.Unlock()
	a.invalidateConnectionData()
	if err := a.env.Write(agent, a.origin, secret); err != nil {
		return err
	}
	if agent == "claude" {
		_ = ensureClaudeOnboarding()
	}
	return nil
}

func (a *App) loadDesktopCatalog(agent, expectedSecret string, native bool) error {
	_, err := a.loadReservedDesktopCatalogWithNotice(a.reserveDesktopRead(agent, false), expectedSecret, native)
	return err
}

func (a *App) loadReservedDesktopCatalog(r desktopRead, expectedSecret string, native bool) error {
	_, err := a.loadReservedDesktopCatalogWithNotice(r, expectedSecret, native)
	return err
}

func (a *App) loadReservedDesktopCatalogWithNotice(r desktopRead, expectedSecret string, native bool) (string, error) {
	r = a.beginDesktopRead(r, expectedSecret)
	agent := r.agent
	var cat *DesktopCatalogPayload
	var err error
	if r.secret != "" {
		cat, err = a.fetchDesktopCatalog(r.secret)
		if err == nil && cat != nil {
			decorateCatalogWithPrefs(agent, cat)
		}
	}
	a.connectionMu.Lock()
	defer a.connectionMu.Unlock()
	// Check whether this read is still valid before writing native configs,
	// and clone before mutating so a previously published snapshot is never
	// written in place while DesktopState() reads a.catalogs.
	var writeCat *DesktopCatalogPayload
	latestRead := false
	current := a.applyDesktopRead(r, native, func(latest bool) {
		latestRead = latest
		if err != nil {
			return
		}
		if !latest {
			// A Models-page read may overtake this native-menu sync. It must
			// not suppress menu setup; use a clone of its newer cached catalog if ready.
			if newer := a.catalogs[agent]; newer != nil {
				writeCat = cloneDesktopCatalog(newer)
			} else {
				writeCat = cloneDesktopCatalog(cat)
			}
			return
		}
		writeCat = cloneDesktopCatalog(cat)
	})
	if !current {
		return "", nil // stale failures must not replace the current connection's UI
	}
	fallbackMsg := ""
	if err == nil && native && writeCat != nil {
		fallbackMsg, _ = a.writeDesktopCatalog(agent, r.secret, writeCat)
	}
	if err == nil && (latestRead || (native && writeCat != nil)) {
		a.mu.Lock()
		if a.catalogs == nil {
			a.catalogs = make(map[string]*DesktopCatalogPayload)
		}
		if writeCat == nil {
			delete(a.catalogs, agent)
		} else {
			a.catalogs[agent] = writeCat
		}
		a.mu.Unlock()
	}
	return fallbackMsg, err
}
