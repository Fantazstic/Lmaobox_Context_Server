package main

import (
	"testing"
)

func TestFormatSearchResultsEdgeCases(t *testing.T) {
	results := []SmartSearchResult{
		{
			Symbol:      "engine.drawText",
			Kind:        "function",
			Section:     "library",
			Score:       1.0,
			Description: "Draws text\nwith newline and | pipe and `backtick` inside",
			Signature:   "drawText(x, y, text string, font string) // very long signature that will be truncated at 60 chars to test correctness",
		},
	}

	snippetResults := []SmartSearchResult{
		{
			Symbol:      "draw.simple",
			Kind:        "snippet",
			Section:     "snippet",
			Score:       0.5,
			Description: "Snippet description | with pipe",
			Signature:   "prefix body `code` with backticks and long content that should be limited to 70 chars",
		},
	}

	out := formatSearchResultsMarkdown("draw text", results, snippetResults, nil, 10)
	t.Log("\n" + out)
}
