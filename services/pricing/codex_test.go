package pricing

import (
	"math"
	"testing"
)

func TestCodexCostUSD_KnownModels(t *testing.T) {
	tests := []struct {
		name              string
		model             string
		inputTokens       int64
		cachedInputTokens int64
		outputTokens      int64
		want              float64
	}{
		{
			name:              "gpt-5 single turn no cache",
			model:             "gpt-5",
			inputTokens:       1_000_000,
			cachedInputTokens: 0,
			outputTokens:      100_000,
			// 1M input * $1.25 + 0.1M output * $10.00 = 1.25 + 1.00 = 2.25
			want: 2.25,
		},
		{
			name:              "gpt-5-codex with cached inputs",
			model:             "gpt-5-codex",
			inputTokens:       1_000_000,
			cachedInputTokens: 800_000, // fresh = 200_000
			outputTokens:      50_000,
			// fresh 0.2M * $1.25 + cached 0.8M * $0.125 + output 0.05M * $10.00
			// = 0.25 + 0.10 + 0.50 = 0.85
			want: 0.85,
		},
		{
			name:              "gpt-5-mini small turn",
			model:             "gpt-5-mini",
			inputTokens:       10_000,
			cachedInputTokens: 0,
			outputTokens:      1_000,
			// 10k * 0.25/1M + 1k * 2.00/1M = 0.0025 + 0.002 = 0.0045
			want: 0.0045,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CodexCostUSD(tt.model, tt.inputTokens, tt.cachedInputTokens, tt.outputTokens)
			if !ok {
				t.Fatalf("expected ok=true for known model %q", tt.model)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("CodexCostUSD(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestCodexCostUSD_SnapshotModelMatches(t *testing.T) {
	// Snapshot-suffixed id should fall back to its base model pricing.
	got, ok := CodexCostUSD("gpt-5-codex-2026-04-01", 1_000_000, 0, 0)
	if !ok {
		t.Fatalf("expected snapshot model to match gpt-5-codex base pricing")
	}
	const want = 1.25 // 1M input tokens * $1.25/M
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("snapshot match cost = %v, want %v", got, want)
	}
}

func TestCodexCostUSD_PrefersLongerPrefix(t *testing.T) {
	// "gpt-5-mini-codex" should match its own entry, not the shorter "gpt-5" prefix.
	got, ok := CodexCostUSD("gpt-5-mini-codex-2026-05-01", 1_000_000, 0, 0)
	if !ok {
		t.Fatalf("expected match for snapshot of gpt-5-mini-codex")
	}
	const want = 0.25 // gpt-5-mini-codex input price per 1M
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("longer-prefix match cost = %v, want %v", got, want)
	}
}

func TestCodexCostUSD_UnknownModel(t *testing.T) {
	_, ok := CodexCostUSD("some-unknown-model", 100, 0, 100)
	if ok {
		t.Fatalf("expected ok=false for unknown model")
	}
}

func TestCodexCostUSD_DefensiveNegatives(t *testing.T) {
	// Negative inputs are clamped to zero, not propagated.
	got, ok := CodexCostUSD("gpt-5", -5, -5, -5)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if got != 0 {
		t.Fatalf("expected zero cost for all-negative inputs, got %v", got)
	}
}

// Platform-flavoured model ids ("gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.5", "gpt-5.5-pro")
// are what the Nairi platform actually exposes and what the Codex CLI reports back at
// the end of a turn. The pricing table is keyed on OpenAI's canonical ids, so the
// lookup must normalise the dotted-minor segment ("gpt-N.X" → "gpt-N") before falling
// through to the prefix matcher for unknown suffixes like "-pro" / "-base".
func TestCodexCostUSD_PlatformDottedMinorVersionIds(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		inputTokens int64
		want        float64
	}{
		{
			name:        "gpt-5.4-mini normalises to gpt-5-mini",
			model:       "gpt-5.4-mini",
			inputTokens: 1_000_000,
			want:        0.25, // gpt-5-mini input rate
		},
		{
			name:        "gpt-5.3-codex normalises to gpt-5-codex",
			model:       "gpt-5.3-codex",
			inputTokens: 1_000_000,
			want:        1.25, // gpt-5-codex input rate
		},
		{
			name:        "gpt-5.5 normalises to gpt-5",
			model:       "gpt-5.5",
			inputTokens: 1_000_000,
			want:        1.25, // gpt-5 input rate
		},
		{
			name:        "gpt-5.5-pro: -pro suffix unknown → prefix-matches gpt-5",
			model:       "gpt-5.5-pro",
			inputTokens: 1_000_000,
			want:        1.25,
		},
		{
			name:        "gpt-5.2-base: -base suffix unknown → prefix-matches gpt-5",
			model:       "gpt-5.2-base",
			inputTokens: 1_000_000,
			want:        1.25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CodexCostUSD(tt.model, tt.inputTokens, 0, 0)
			if !ok {
				t.Fatalf("expected ok=true for platform model %q", tt.model)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("CodexCostUSD(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

// The GPT-6 and GPT-5.6 tiers are keyed verbatim, so they must not fall through to
// the dotted-minor normalisation (which would price gpt-5.6-sol at the gpt-5 rate).
func TestCodexCostUSD_TieredModelsUseOwnRates(t *testing.T) {
	tests := map[string]float64{
		"gpt-6.1-sol":   2.00,
		"gpt-6.1-terra": 1.00,
		"gpt-6-astra":   10.00,
		"gpt-6-sol":     2.00,
		"gpt-6-luna":    0.10,
		"gpt-5.6-sol":   4.00,
		"gpt-5.6-terra": 2.00,
		"gpt-5.6-luna":  0.20,
	}
	for model, wantInput := range tests {
		got, ok := CodexCostUSD(model, 1_000_000, 0, 0)
		if !ok {
			t.Fatalf("expected ok=true for %q", model)
		}
		if math.Abs(got-wantInput) > 1e-9 {
			t.Fatalf("CodexCostUSD(%q) = %v, want %v", model, got, wantInput)
		}
	}
}

// Negative case: a model that does not match either canonical, snapshot, or
// platform-dotted naming is still rejected so callers leave cost NULL.
func TestCodexCostUSD_PlatformDottedMinorVersionStillRejectsGarbage(t *testing.T) {
	if _, ok := CodexCostUSD("gpt-5.4.1-mini", 1_000_000, 0, 0); ok {
		t.Fatalf("expected ok=false: 'gpt-5.4.1-mini' does not match the gpt-N.X[-suffix] pattern")
	}
	if _, ok := CodexCostUSD("gpt-99.0-mini", 1_000_000, 0, 0); ok {
		t.Fatalf("expected ok=false: 'gpt-99.0-mini' normalises to 'gpt-99-mini' which is not in the pricing table")
	}
}

func TestCodexCostUSD_CachedExceedsInputClamps(t *testing.T) {
	// If cached > input the fresh count is clamped to zero so we never bill negative
	// fresh tokens. cached itself is still billed (the reported value is the source of truth).
	got, ok := CodexCostUSD("gpt-5", 100, 1_000, 0)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	// fresh = 0 (clamped), cached = 1000 * 0.125/1M = 0.000125
	const want = 0.000125
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("cached-clamp cost = %v, want %v", got, want)
	}
}
