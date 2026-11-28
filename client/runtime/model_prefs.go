package clientruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type modelPrefs struct {
	ClaudeMode  string `json:"claude_mode,omitempty"`  // "" | "native" | "cn"
	ClaudeModel string `json:"claude_model,omitempty"` // explicit selected model ID
	CodexModel  string `json:"codex_model,omitempty"`  // explicit selected Codex model ID
}

func modelPrefsPath() string {
	return filepath.Join(paths().Dir(), "model-prefs.json")
}

func loadModelPrefs() modelPrefs {
	raw, err := os.ReadFile(modelPrefsPath())
	if err != nil || len(raw) == 0 {
		return modelPrefs{}
	}
	var p modelPrefs
	if json.Unmarshal(raw, &p) != nil {
		return modelPrefs{}
	}
	return p
}

func saveModelPrefs(p modelPrefs) error {
	path := modelPrefsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(b, '\n'))
}

func clearModelPrefs() {
	_ = os.Remove(modelPrefsPath())
}

func catalogContainsModel(cat *DesktopCatalogPayload, id string) bool {
	if cat == nil || strings.TrimSpace(id) == "" {
		return false
	}
	for _, m := range cat.Models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func catalogModelIsCN(cat *DesktopCatalogPayload, id string) bool {
	if cat == nil || id == "" {
		return false
	}
	for _, m := range cat.CnAlternatives {
		if m.ID == id {
			return true
		}
	}
	for _, m := range cat.Models {
		if m.ID == id && m.IsCnAlternative {
			return true
		}
	}
	return false
}

type resolvedClaudeSelection struct {
	mode        string // "native" | "cn"
	activeModel string
	sonnet      string
	opus        string
	haiku       string
	fellBack    bool
	prevModel   string
}

func resolveClaudeSelection(cat *DesktopCatalogPayload, prefs modelPrefs) resolvedClaudeSelection {
	primary := ""
	sonnet, opus, haiku := "", "", ""
	if cat != nil {
		if cat.ClaudeSlots != nil {
			sonnet = strings.TrimSpace(cat.ClaudeSlots.Sonnet)
			opus = strings.TrimSpace(cat.ClaudeSlots.Opus)
			haiku = strings.TrimSpace(cat.ClaudeSlots.Haiku)
		}
		// Respect the server's explicit default_model (e.g. admin-published Opus)
		// before falling back to computed claude_slots.primary.
		if dm := strings.TrimSpace(cat.DefaultModel); dm != "" && catalogContainsModel(cat, dm) {
			primary = dm
		} else if cat.ClaudeSlots != nil {
			primary = strings.TrimSpace(cat.ClaudeSlots.Primary)
		}
		if primary == "" && len(cat.Models) > 0 {
			primary = strings.TrimSpace(cat.Models[0].ID)
		}
		if sonnet == "" {
			sonnet = primary
		}
		if opus == "" {
			opus = primary
		}
		if haiku == "" {
			haiku = primary
		}
	}

	chosen := strings.TrimSpace(prefs.ClaudeModel)
	if prefs.ClaudeMode == "cn" && chosen == "" && cat != nil && len(cat.CnAlternatives) > 0 {
		chosen = strings.TrimSpace(cat.CnAlternatives[0].ID)
	}
	if chosen != "" {
		if catalogContainsModel(cat, chosen) {
			isCN := prefs.ClaudeMode == "cn" || catalogModelIsCN(cat, chosen) || !strings.HasPrefix(strings.ToLower(chosen), "claude")
			if isCN {
				// Map all 4 Claude Code slots to the chosen CN/non-Claude model so
				// background Haiku/Sonnet/Opus sub-agents never fail with 403/404.
				return resolvedClaudeSelection{
					mode:        "cn",
					activeModel: chosen,
					sonnet:      chosen,
					opus:        chosen,
					haiku:       chosen,
				}
			}
			return resolvedClaudeSelection{
				mode:        "native",
				activeModel: chosen,
				sonnet:      sonnet,
				opus:        opus,
				haiku:       haiku,
			}
		}
		// Previously selected model is no longer live in this tier -> auto-fallback to server default!
		if primary != "" && !strings.HasPrefix(strings.ToLower(primary), "claude") {
			return resolvedClaudeSelection{
				mode:        "cn",
				activeModel: primary,
				sonnet:      primary,
				opus:        primary,
				haiku:       primary,
				fellBack:    true,
				prevModel:   chosen,
			}
		}
		return resolvedClaudeSelection{
			mode:        "native",
			activeModel: primary,
			sonnet:      sonnet,
			opus:        opus,
			haiku:       haiku,
			fellBack:    true,
			prevModel:   chosen,
		}
	}
	if prefs.ClaudeMode == "cn" {
		// Wanted CN mode but this tier has no Anthropic-compatible CN alternatives.
		return resolvedClaudeSelection{
			mode:        "native",
			activeModel: primary,
			sonnet:      sonnet,
			opus:        opus,
			haiku:       haiku,
			fellBack:    true,
			prevModel:   "国产平替",
		}
	}
	if primary != "" && !strings.HasPrefix(strings.ToLower(primary), "claude") {
		return resolvedClaudeSelection{
			mode:        "cn",
			activeModel: primary,
			sonnet:      primary,
			opus:        primary,
			haiku:       primary,
		}
	}
	return resolvedClaudeSelection{
		mode:        "native",
		activeModel: primary,
		sonnet:      sonnet,
		opus:        opus,
		haiku:       haiku,
	}
}

func decorateCatalogWithPrefs(agent string, cat *DesktopCatalogPayload) {
	if cat == nil {
		return
	}
	prefs := loadModelPrefs()
	if agent == "claude" {
		sel := resolveClaudeSelection(cat, prefs)
		cat.ClaudeMode = sel.mode
		cat.ActiveModel = sel.activeModel
		return
	}
	if agent == "codex" {
		if prefs.CodexModel != "" && catalogContainsModel(cat, prefs.CodexModel) {
			cat.ActiveModel = prefs.CodexModel
			return
		}
		if cur := configuredCodexModel(); cur != "" && catalogContainsModel(cat, cur) {
			cat.ActiveModel = cur
			return
		}
		cat.ActiveModel = cat.DefaultModel
	}
}
