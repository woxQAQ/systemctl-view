package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Scope is the systemd manager we talk to: the system manager, or the
// calling user's own.
type Scope int

const (
	ScopeSystem Scope = iota
	ScopeUser
)

// Unit is a row of systemctl list-units: one loaded unit and its state.
type Unit struct {
	Name        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
}

// Type is the unit's kind, the suffix of its name: service, timer, ...
func (u Unit) Type() string {
	if i := strings.LastIndexByte(u.Name, '.'); i >= 0 {
		return u.Name[i+1:]
	}
	return ""
}

// Prop is one property of systemctl show, kept in systemd's order; keys
// repeat (Environment, ExecStart, ...).
type Prop struct{ Key, Value string }

// Backend is everything the window asks of systemd. The real
// implementation shells out to systemctl and journalctl; tests use a
// fake.
type Backend interface {
	ListUnits(scope Scope) ([]Unit, error)
	Show(scope Scope, name string) ([]Prop, error)
	// Status and Logs return their text even when the command exits
	// nonzero, as systemctl status does for a dead unit.
	Status(scope Scope, name string) string
	Logs(scope Scope, name string, lines int) string
	Run(scope Scope, verb, name string) error
	// RunPrivileged runs the verb through sudo, feeding password on
	// sudo's stdin; with sudo's credentials cached the password is
	// unread and may be empty.
	RunPrivileged(scope Scope, verb, name, password string) error
	// SudoCached reports whether sudo would run without a password.
	SudoCached() bool
}

// needsAuth reports whether err is systemd refusing for want of
// privilege, which a sudo retry can answer.
func needsAuth(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"access denied", "authentication", "not authorized", "permission denied", "privilege"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// wrongPassword reports whether err is sudo rejecting the password.
func wrongPassword(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "incorrect password") || strings.Contains(msg, "sorry, try again")
}

// cliBackend runs systemctl and journalctl. Privileged actions on system
// units go through polkit, as systemctl's own do: the user authorizes
// them in the desktop's dialog.
type cliBackend struct{}

func (cliBackend) systemctl(scope Scope, args ...string) (string, []string) {
	if scope == ScopeUser {
		args = append([]string{"--user"}, args...)
	}
	return "systemctl", args
}

// output runs a command with colors and pagers off, and returns its
// stdout. An exit failure is an error carrying stderr's first line, as
// that is where systemctl puts its reason.
func output(name string, args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Env = append(os.Environ(), "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=", "PAGER=")
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}

// outputLax is output for commands whose exit code carries meaning we
// show anyway: it returns stdout and stderr joined, without failing.
func outputLax(name string, args ...string) string {
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Env = append(os.Environ(), "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=", "PAGER=")
	_ = cmd.Run()
	return strings.TrimSpace(out.String())
}

func (cliBackend) ListUnits(scope Scope) ([]Unit, error) {
	name, args := cliBackend{}.systemctl(scope,
		"list-units", "--all", "--output=json", "--no-legend", "--no-pager")
	out, err := output(name, args...)
	if err != nil {
		return nil, err
	}
	var units []Unit
	if err := json.Unmarshal([]byte(out), &units); err != nil {
		return nil, fmt.Errorf("reading systemctl's output: %w", err)
	}
	return units, nil
}

func (cliBackend) Show(scope Scope, unit string) ([]Prop, error) {
	name, args := cliBackend{}.systemctl(scope, "show", unit, "--no-pager")
	out, err := output(name, args...)
	if err != nil {
		return nil, err
	}
	var props []Prop
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\n")
		k, v, ok := strings.Cut(line, "=")
		if ok && k != "" {
			props = append(props, Prop{k, v})
		}
	}
	return props, nil
}

func (cliBackend) Status(scope Scope, unit string) string {
	name, args := cliBackend{}.systemctl(scope, "status", unit, "--no-pager", "-l")
	return outputLax(name, args...)
}

func (cliBackend) Logs(scope Scope, unit string, lines int) string {
	unitArg := "--unit=" + unit
	if scope == ScopeUser {
		unitArg = "--user-unit=" + unit
	}
	return outputLax("journalctl", unitArg, "-n", fmt.Sprint(lines), "--no-pager", "-o", "short-iso")
}

func (cliBackend) Run(scope Scope, verb, unit string) error {
	name, args := cliBackend{}.systemctl(scope, verb, unit)
	_, err := output(name, args...)
	return err
}

func (cliBackend) RunPrivileged(scope Scope, verb, unit, password string) error {
	name, args := cliBackend{}.systemctl(scope, verb, unit)
	var errb bytes.Buffer
	cmd := exec.Command("sudo", append([]string{"-S", "-p", "", "--", name}, args...)...)
	cmd.Stdin = strings.NewReader(password + "\n")
	cmd.Stderr = &errb
	cmd.Env = append(os.Environ(), "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=", "PAGER=")
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			// sudo's last line is the verdict; the ones above are its prompts.
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

func (cliBackend) SudoCached() bool {
	return exec.Command("sudo", "-n", "true").Run() == nil
}
