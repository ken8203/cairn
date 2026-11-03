package skills

import (
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	for _, topic := range Topics {
		short, err := Get(topic.Name, false)
		if err != nil {
			t.Fatal(err)
		}
		full, err := Get(topic.Name, true)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(short+full, fullMarker) {
			t.Errorf("%s: marker shown", topic.Name)
		}
		if !strings.HasSuffix(short, "Run `cairn skills get "+topic.Name+" --full` for the rest.\n") {
			t.Errorf("%s: short guide doesn't point at --full:\n%s", topic.Name, short)
		}
		body, _, _ := strings.Cut(short, "Run `cairn skills get")
		if !strings.HasPrefix(full, body) || len(full) <= len(body) {
			t.Errorf("%s: full guide doesn't extend the short one", topic.Name)
		}
	}
	if _, err := Get("nope", false); err == nil || err.Error() != `no guide for "nope"; see `+"`cairn skills list`" {
		t.Errorf("err = %v", err)
	}
	if _, err := Get("../skills", false); err == nil {
		t.Error("read outside guides")
	}
}
