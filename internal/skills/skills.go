// Package skills holds the guides an agent reads with `cairn skills get`, built into the
// binary so they always match it.
package skills

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed guides/*.md
var guides embed.FS

// Topics are the guides, in the order `cairn skills list` shows them.
var Topics = []struct{ Name, Summary string }{
	{"cairn", "how an agent uses cairn: trails, steps, notes, and what the hook records"},
	{"setup", "installing cairn, `cairn setup`, `cairn doctor`, and troubleshooting"},
}

// fullMarker ends the part of a guide shown without --full.
const fullMarker = "<!-- full -->\n"

// Get is a topic's guide, the whole of it with full.
func Get(topic string, full bool) (string, error) {
	b, err := guides.ReadFile("guides/" + topic + ".md")
	if err != nil {
		return "", fmt.Errorf("no guide for %q; see `cairn skills list`", topic)
	}
	short, rest, found := strings.Cut(string(b), fullMarker)
	if full {
		return short + rest, nil
	}
	if found {
		short += "Run `cairn skills get " + topic + " --full` for the rest.\n"
	}
	return short, nil
}
