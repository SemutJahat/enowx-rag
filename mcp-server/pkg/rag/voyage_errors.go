package rag

import (
	"encoding/json"
	"fmt"
	"strings"
)

// voyageError turns a non-200 Voyage API response into an error that says
// what went wrong and what to do about it, keeping the API's own detail.
func voyageError(api, model string, status int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	var parsed struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Detail != "" {
		detail = parsed.Detail
	}
	if len(detail) > 300 {
		detail = detail[:300] + "…"
	}
	var hint string
	switch status {
	case 401, 403:
		hint = "the Voyage API key is invalid, revoked or lacks access — set a valid key in Settings → API keys (keys start with \"pa-\")"
	case 400, 422:
		lower := strings.ToLower(detail)
		switch {
		case strings.Contains(lower, "dimension"):
			hint = fmt.Sprintf("%q does not support this output dimension — choose one of the sizes listed in Settings → Models", model)
		case strings.Contains(lower, "model"):
			hint = fmt.Sprintf("model %q is not available to this key — pick a model from Settings → Models", model)
		default:
			hint = "the request was rejected — check the model and dimension in Settings → Models"
		}
	case 429:
		hint = "rate limited by Voyage — accounts without a payment method have very low limits; add a payment method in the Voyage dashboard (free tokens still apply) or retry later"
	case 500, 502, 503, 504:
		hint = "Voyage is having trouble — this is retried on the next sync or search"
	}
	if hint == "" {
		return fmt.Errorf("voyage %s (%s) returned HTTP %d: %s", api, model, status, detail)
	}
	return fmt.Errorf("voyage %s (%s) returned HTTP %d: %s — %s", api, model, status, detail, hint)
}
