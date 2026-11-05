package host

import (
	_ "embed"
	json "encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

var rex = host{
	name: "Rex",
	env:  "REX_BLOCK",
	// Rex keys are bare block ids, as cairn has always stored them.
	key:   Key,
	tab:   func() string { return rexWindow(Key(os.Getenv("REX_BLOCK"))) },
	label: func() string { return rexLabel(os.Getenv("REX_SESSION")) },
	title: rexTitle,
	holds: rexHolds,
	dir:   func(block string) string { return rexProcessOf(block).Foreground.Cwd },
	panel: rexPanel,
}

// rexBin is the rex CLI: on PATH, else in $REX_BIN_DIR, which Rex sets in its terminals.
func rexBin() string {
	if p, err := exec.LookPath("rex"); err == nil {
		return p
	}
	if dir := os.Getenv("REX_BIN_DIR"); dir != "" {
		return filepath.Join(dir, "rex")
	}
	return "rex"
}

func rexTitle(block string) string {
	var res struct {
		Title string `json:"title"`
	}
	decode(&res, rexBin(), "block", "call", "-s", os.Getenv("REX_SESSION"), "-b", "block:"+block, "title", "{}")
	return res.Title
}

// rexProcess is what Rex knows of the program in the foreground of a block.
type rexProcess struct {
	Foreground struct {
		PID int    `json:"pid"`
		Cwd string `json:"cwd"`
	} `json:"foreground"`
}

func rexProcessOf(block string) rexProcess {
	var p rexProcess
	decode(&p, rexBin(), "block", "call", "-s", os.Getenv("REX_SESSION"), "-b", "block:"+block, "process", "{}")
	return p
}

// rexHolds reports whether the block's foreground program is cairn or its ancestor, as
// a Codex app-server holding another block's $REX_BLOCK is not.
func rexHolds(block string) bool {
	pid := rexProcessOf(block).Foreground.PID
	return pid != 0 && slices.Contains(lineage(), pid)
}

// rexDefaultSuffix is how much of a window's id Rex puts in the label it gives a window
// nobody renamed, as in Window 450b6607.
const rexDefaultSuffix = 8

// rexWindow is the label of the window holding a block, unless it is the one Rex gave it.
func rexWindow(block string) string {
	if block == "" {
		return ""
	}
	var b struct {
		Block struct {
			Window string `json:"window_id"`
		} `json:"block"`
	}
	decode(&b, rexBin(), "block", "inspect", "block:"+block, "--json")
	id := b.Block.Window
	if len(id) < rexDefaultSuffix {
		return ""
	}
	var w struct {
		Label string `json:"label"`
	}
	decode(&w, rexBin(), "window", "inspect", id, "--json")
	if w.Label == "Window "+id[len(id)-rexDefaultSuffix:] {
		return ""
	}
	return w.Label
}

func rexLabel(session string) string {
	if session == "" {
		return ""
	}
	var res struct {
		Label string `json:"label"`
	}
	decode(&res, rexBin(), "session", "inspect", session, "--json")
	return res.Label
}

// RexPanelLua is a Lua chunk that toggles the panel of the Rex block a key names, for an
// init.lua action to run itself with loadstring. Run that way it needs no rex CLI, which
// the bare environment Rex gives an action's programs can't find.
func RexPanelLua(cairn, key, dir string, close bool) (string, error) {
	if key == "" {
		key = Pane()
	}
	if h, _ := lookup(hosts, key); h.name != rex.name || key == "" {
		return "", fmt.Errorf("--lua needs a Rex block, not %q", key)
	}
	return rexScript(key, watch{cairn, key, dir}.argv(), close)
}

//go:embed rex.lua
var rexPanelLua string

// rexPanel floats the panel over the window holding the block, or closes the floating
// layer that holds it. -b scopes the script to the block's session.
func rexPanel(id string, w watch, close bool) error {
	script, err := rexScript(id, w.argv(), close)
	if err != nil {
		return err
	}
	_, err = cli(script, rexBin(), "do", "-", "-b", "block:"+id)
	return err
}

// rexScript is panel.lua followed by its call for this block. The arguments travel as JSON
// in a long bracket, which a value holding the bracket's close would end early.
func rexScript(id string, watch []string, close bool) (string, error) {
	args, err := json.Marshal(struct {
		Block   string   `json:"block"`
		Command []string `json:"command"`
		Close   bool     `json:"close"`
		Label   string   `json:"label"`
	}{"block:" + id, watch, close, panelTitle})
	if err != nil {
		return "", err
	}
	if strings.Contains(string(args), "]==]") {
		return "", fmt.Errorf("rex: can't pass %s to Lua", args)
	}
	return rexPanelLua + "return toggle(rex.json.decode([==[" + string(args) + "]==]))\n", nil
}
