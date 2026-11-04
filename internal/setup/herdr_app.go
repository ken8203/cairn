package setup

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"embed"
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed cairn-mod/*
var herdrMod embed.FS

// HerdrAppHome is the app's public configuration directory.
func HerdrAppHome() string {
	home, _ := os.UserHomeDir()
	return cmp.Or(os.Getenv("HERDR_APP_HOME"), filepath.Join(cmp.Or(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config")), "herdr-app"))
}

func modFiles(self string) map[string][]byte {
	main, _ := herdrMod.ReadFile("cairn-mod/main.js")
	manifest, _ := herdrMod.ReadFile("cairn-mod/mod.json")
	path, _ := json.Marshal(self)
	return map[string][]byte{"main.js": main, "mod.json": manifest, "binary.js": []byte("export const binary = " + string(path) + ";\n")}
}

const modOwnership = ".cairn-owned.json"

func modHash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// modChanges preflights every file before writing any, preserving edited assets.
func modChanges(dir string, files map[string][]byte) (bool, error) {
	var owned map[string]string
	b, err := os.ReadFile(filepath.Join(dir, modOwnership))
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &owned); err != nil {
			return false, fmt.Errorf("invalid ownership file: %w", err)
		}
	}
	stale := false
	for name, want := range files {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			stale = true
			continue
		}
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("manual conflict: %s is not a regular file", path)
		}
		old, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		if bytes.Equal(old, want) {
			continue
		}
		if owned[name] == "" || owned[name] != modHash(old) {
			return false, fmt.Errorf("manual conflict: preserve edited %s; move it aside before setup", path)
		}
		stale = true
	}
	return stale, nil
}

func (s steps) herdrApp() {
	if !onPath("herdr-app") {
		return
	}
	self, err := os.Executable()
	if err != nil {
		s.line(Manual, "herdr-mod", err.Error())
		return
	}
	dir := filepath.Join(HerdrAppHome(), "mods", "cairn")
	files := modFiles(self)
	stale, err := modChanges(dir, files)
	switch {
	case err != nil:
		s.line(Manual, "herdr-mod", err.Error())
		return
	case !stale:
		s.line(OK, "herdr-mod", dir)
	case s.dryRun:
		s.line(Skip, "herdr-mod", "dry run; would write "+dir)
	default:
		if err := writeMod(dir, files); err != nil {
			s.line(Manual, "herdr-mod", err.Error())
			return
		}
		s.line(Done, "herdr-mod", "wrote "+dir)
	}
	s.line(Manual, "herdr-app-keys", "to toggle the floating panel, bind mod.open:cairn in keys.json, or run `cairn panel` in a Herdr.app terminal; works with shipped layouts")
}

func writeMod(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	owned := make(map[string]string)
	for name, b := range files {
		if err := replaceModFile(filepath.Join(dir, name), b); err != nil {
			return err
		}
		owned[name] = modHash(b)
	}
	b, err := json.Marshal(owned)
	if err != nil {
		return err
	}
	return replaceModFile(filepath.Join(dir, modOwnership), b)
}

func replaceModFile(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".cairn-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func herdrAppCheck() Check {
	c := Check{Check: "herdr-mod", Status: Warn, Fix: "run `cairn setup`; resolve any manual conflicts"}
	self, err := os.Executable()
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	dir := filepath.Join(HerdrAppHome(), "mods", "cairn")
	stale, err := modChanges(dir, modFiles(self))
	switch {
	case err != nil:
		c.Detail = err.Error()
	case stale:
		c.Detail = "missing or stale assets in " + dir
	default:
		c.Status, c.Detail, c.Fix = OK, "assets current in "+dir+"; open with `cairn panel`", ""
	}
	return c
}
