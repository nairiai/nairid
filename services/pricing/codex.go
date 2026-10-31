package pricing

import (
	"regexp"
	"strings"
)

// codexModelPricing holds per-million-token rates in USD for an OpenAI/Codex model.
// Cached input tokens are billed at a discount versus fresh input tokens.
type codexModelPricing struct {
	InputUSDPerMillion       float64
	CachedInputUSDPerMillion float64
	OutputUSDPerMillion      float64
}

// Public OpenAI pricing as of 2026-10. Values may drift; treat this as a
// best-effort estimate. Pricing updates are a nairid release.
//
// Sources:
//   - https://openai.com/api/pricing (gpt-5, gpt-5-codex families)
//   - https://platform.openai.com/docs/models
//   - https://models.dev (gpt-6 and gpt-5.6 families)
var codexPricingByModel = map[string]codexModelPricing{
	// GPT-6 family, keyed verbatim: each tier and point release has its own rate.
	"gpt-6-astra":   {InputUSDPerMillion: 10.00, CachedInputUSDPerMillion: 1.00, OutputUSDPerMillion: 50.00},
	"gpt-6-sol":     {InputUSDPerMillion: 2.00, CachedInputUSDPerMillion: 0.20, OutputUSDPerMillion: 10.00},
	"gpt-6-luna":    {InputUSDPerMillion: 0.10, CachedInputUSDPerMillion: 0.01, OutputUSDPerMillion: 0.50},
	"gpt-6.1-sol":   {InputUSDPerMillion: 2.00, CachedInputUSDPerMillion: 0.10, OutputUSDPerMillion: 10.00},
	"gpt-6.1-terra": {InputUSDPerMillion: 1.00, CachedInputUSDPerMillion: 0.05, OutputUSDPerMillion: 5.00},

	// GPT-5.6 family. Without these, "gpt-5.6-sol" would collapse to the gpt-5 rate.
	"gpt-5.6-sol":   {InputUSDPerMillion: 4.00, CachedInputUSDPerMillion: 0.40, OutputUSDPerMillion: 20.00},
	"gpt-5.6-terra": {InputUSDPerMillion: 2.00, CachedInputUSDPerMillion: 0.20, OutputUSDPerMillion: 12.00},
	"gpt-5.6-luna":  {InputUSDPerMillion: 0.20, CachedInputUSDPerMillion: 0.02, OutputUSDPerMillion: 1.20},

	// GPT-5 family
	"gpt-5":      {InputUSDPerMillion: 1.25, CachedInputUSDPerMillion: 0.125, OutputUSDPerMillion: 10.00},
	"gpt-5-mini": {InputUSDPerMillion: 0.25, CachedInputUSDPerMillion: 0.025, OutputUSDPerMillion: 2.00},
	"gpt-5-nano": {InputUSDPerMillion: 0.05, CachedInputUSDPerMillion: 0.005, OutputUSDPerMillion: 0.40},

	// Codex variants of GPT-5 (priced like their base model)
	"gpt-5-codex":      {InputUSDPerMillion: 1.25, CachedInputUSDPerMillion: 0.125, OutputUSDPerMillion: 10.00},
	"gpt-5-mini-codex": {InputUSDPerMillion: 0.25, CachedInputUSDPerMillion: 0.025, OutputUSDPerMillion: 2.00},

	// Legacy o-series fallbacks (in case older sessions still run)
	"o3":      {InputUSDPerMillion: 2.00, CachedInputUSDPerMillion: 0.50, OutputUSDPerMillion: 8.00},
	"o3-mini": {InputUSDPerMillion: 1.10, CachedInputUSDPerMillion: 0.55, OutputUSDPerMillion: 4.40},
	"o4-mini": {InputUSDPerMillion: 1.10, CachedInputUSDPerMillion: 0.275, OutputUSDPerMillion: 4.40},
}

// CodexCostUSD computes the dollar cost of a single Codex turn from its
// reported token usage. cachedInputTokens are subtracted from inputTokens
// to avoid double-billing, then billed at the discounted cache rate.
//
// Returns (cost, true) when the model is recognised. Returns (0, false)
// when the model is unknown so callers can leave cost NULL upstream.
func CodexCostUSD(model string, inputTokens, cachedInputTokens, outputTokens int64) (float64, bool) {
	p, ok := lookupCodexPricing(model)
	if !ok {
		return 0, false
	}

	// Defensive clamp: cached can never exceed reported input.
	freshInput := inputTokens - cachedInputTokens
	if freshInput < 0 {
		freshInput = 0
	}
	if cachedInputTokens < 0 {
		cachedInputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}

	const perMillion = 1_000_000.0
	cost := (float64(freshInput)/perMillion)*p.InputUSDPerMillion +
		(float64(cachedInputTokens)/perMillion)*p.CachedInputUSDPerMillion +
		(float64(outputTokens)/perMillion)*p.OutputUSDPerMillion
	return cost, true
}

// platformMinorVersionRe strips the dotted minor version from platform-flavoured
// Codex model ids ("gpt-5.4-mini" → "gpt-5-mini"). The Nairi platform exposes
// Codex models under names like gpt-5.4-mini / gpt-5.5-pro / gpt-5.3-codex,
// which OpenAI's public pricing pages do not list verbatim. We collapse the
// `.X` segment so the canonical OpenAI keys ("gpt-5-mini", "gpt-5-codex", …)
// keep matching. The capture group preserves the major version and any suffix.
var platformMinorVersionRe = regexp.MustCompile(`^(gpt-\d+)\.\d+(-.+)?$`)

// lookupCodexPricing returns the pricing entry for a model, accepting:
//   - canonical OpenAI model ids ("gpt-5-codex")
//   - snapshot / date-stamped variants ("gpt-5-codex-2026-04-01") via prefix match
//   - platform-flavoured ids with a dotted minor version ("gpt-5.4-mini",
//     "gpt-5.3-codex") by normalising "gpt-N.X[-suffix]" → "gpt-N[-suffix]"
//     before lookup, then falling back to the prefix matcher.
func lookupCodexPricing(model string) (codexModelPricing, bool) {
	if p, ok := codexPricingByModel[model]; ok {
		return p, true
	}
	if p, ok := matchPrefix(model); ok {
		return p, true
	}
	// Platform-flavoured ids: collapse the .X minor segment and retry.
	if m := platformMinorVersionRe.FindStringSubmatch(model); m != nil {
		normalized := m[1] + m[2] // e.g. "gpt-5" + "-mini" = "gpt-5-mini"
		if p, ok := codexPricingByModel[normalized]; ok {
			return p, true
		}
		if p, ok := matchPrefix(normalized); ok {
			return p, true
		}
	}
	return codexModelPricing{}, false
}

// matchPrefix returns the longest pricing entry whose key is a prefix of
// model (matched on full hyphen segments). Used for snapshot-suffixed ids
// and for normalised platform ids that include a suffix we do not list
// verbatim (e.g. "gpt-5-pro" → fall back to "gpt-5").
func matchPrefix(model string) (codexModelPricing, bool) {
	var best string
	for prefix := range codexPricingByModel {
		if !strings.HasPrefix(model, prefix+"-") {
			continue
		}
		if len(prefix) > len(best) {
			best = prefix
		}
	}
	if best == "" {
		return codexModelPricing{}, false
	}
	return codexPricingByModel[best], true
}
