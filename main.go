// Command systemctl-ui is a native window onto systemd: it lists the
// units of the system or the user's manager, shows one unit's properties,
// status and journal, and runs the everyday verbs — start, stop, restart,
// reload, enable, disable, mask — through systemctl, which asks polkit
// when more privilege is needed.
package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// app is the state the window shows. Views build from it on the main
// thread; workers publish their results through update, so every write
// below happens on the main thread.
type app struct {
	b   Backend
	win *mygo.Window // nil under ui.NewTester, where fetches run inline

	scope    Scope
	scopeTab int // the sidebar's System/User tabs, mirroring scope

	units       []Unit
	loading     bool
	listErr     string
	refreshedAt time.Time
	gen         int // grows per refresh; a stale fetch sees it moved and dies

	search      string
	typeFilter  string // a unit type ("service"), "" for all
	stateFilter string

	sort      ui.SortOrder
	listState ui.ListState
	selRow    int    // the table's chosen row, reconciled with selName
	selName   string // the selected unit's name: selection that survives re-sorts

	tab           int // detail tabs: 0 overview, 1 status, 2 logs
	split         float32
	detailFor     string // the unit the loaded detail belongs to
	detailGen     int
	detailLoading bool
	detailErr     string
	props         []Prop
	statusText    string
	logsText      string

	busy        map[string]bool // "verb name" while its systemctl run is in flight
	notices     []string        // toasts queued by workers, drained by the view
	autoRefresh bool

	// The sudo password dialog: opened when the system manager refuses a
	// verb for want of privilege. The password lives in pwValue only
	// while the dialog shows and is cleared after each attempt.
	pwOpen  bool
	pwValue string
	pwErr   string
	pwBusy  bool
	pwScope Scope
	pwVerb  string
	pwUnit  string

	searchFocus ui.Handle
}

func newApp(b Backend) *app {
	a := &app{
		b:           b,
		stateFilter: stateOptions[0],
		sort:        ui.SortOrder{Column: "unit"},
		split:       60, // SplitVerticalPercent takes 0–100, not a fraction
		selRow:      -1,
		busy:        map[string]bool{},
		autoRefresh: true,
	}
	a.listState.Selected = &a.selRow
	a.listState.Sort = &a.sort
	return a
}

// update runs fn on the main thread and redraws. Without a window (tests)
// it runs fn inline.
func (a *app) update(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	fn()
}

// dispatch runs work off the UI thread when there is a window, inline in
// tests.
func (a *app) dispatch(work func()) {
	if a.win != nil {
		go work()
		return
	}
	work()
}

// refresh reloads the unit list for the current scope.
func (a *app) refresh() {
	a.gen++
	gen, scope := a.gen, a.scope
	a.loading = true
	a.dispatch(func() {
		units, err := a.b.ListUnits(scope)
		a.update(func() {
			if gen != a.gen {
				return
			}
			a.loading = false
			a.refreshedAt = time.Now()
			if err != nil {
				a.listErr = err.Error()
				return
			}
			a.listErr = ""
			a.units = units
		})
	})
}

// loadDetails fetches the selected unit's properties, status and journal.
// The view triggers it whenever selName and detailFor part ways.
func (a *app) loadDetails() {
	name := a.selName
	a.detailGen++
	gen, scope := a.detailGen, a.scope
	a.detailFor = name
	a.detailLoading = true
	a.dispatch(func() {
		props, err := a.b.Show(scope, name)
		status := a.b.Status(scope, name)
		logs := a.b.Logs(scope, name, 400)
		a.update(func() {
			if gen != a.detailGen {
				return
			}
			a.detailLoading = false
			if a.detailFor != name {
				return
			}
			a.detailErr = ""
			if err != nil {
				a.detailErr = err.Error()
			}
			a.props = props
			a.statusText = status
			a.logsText = logs
		})
	})
}

// pastTense words a finished verb as a toast.
var pastTense = map[string]string{
	"start": "started", "stop": "stopped", "restart": "restarted",
	"reload": "reloaded", "enable": "enabled", "disable": "disabled",
	"mask": "masked", "unmask": "unmasked",
}

// action runs verb on the selected unit, then reloads list and detail.
// A verb the system manager refuses for want of privilege retries
// through sudo: silently when sudo's credentials are cached, else the
// password dialog asks.
func (a *app) action(verb string) {
	name := a.selName
	key := verb + " " + name
	if name == "" || a.busy[key] {
		return
	}
	scope := a.scope
	a.busy[key] = true
	a.dispatch(func() {
		err := a.b.Run(scope, verb, name)
		askPassword := false
		if err != nil && scope == ScopeSystem && needsAuth(err) {
			if a.b.SudoCached() {
				err = a.b.RunPrivileged(scope, verb, name, "")
			} else {
				askPassword = true
			}
		}
		a.update(func() {
			delete(a.busy, key)
			switch {
			case err == nil:
				a.notices = append(a.notices, name+" "+pastTense[verb])
				a.detailFor = "" // stale after the verb; the view reloads it
				a.refresh()
			case askPassword:
				a.pwScope, a.pwVerb, a.pwUnit = scope, verb, name
				a.pwValue, a.pwErr = "", ""
				a.pwOpen = true
			default:
				a.notices = append(a.notices, fmt.Sprintf("Could not %s %s: %s", verb, name, err))
			}
		})
	})
}

// runPrivileged runs the dialog's pending verb through sudo with the
// typed password.
func (a *app) runPrivileged() {
	verb, unit, scope, pw := a.pwVerb, a.pwUnit, a.pwScope, a.pwValue
	key := verb + " " + unit
	if a.pwBusy || pw == "" || a.busy[key] {
		return
	}
	a.pwBusy = true
	a.busy[key] = true
	a.dispatch(func() {
		err := a.b.RunPrivileged(scope, verb, unit, pw)
		a.update(func() {
			a.pwBusy = false
			delete(a.busy, key)
			a.pwValue = "" // the password is sudo's now, not ours
			switch {
			case err == nil:
				a.pwOpen = false
				a.notices = append(a.notices, unit+" "+pastTense[verb])
				a.detailFor = ""
				a.refresh()
			case wrongPassword(err):
				a.pwErr = "Wrong password, try again"
			default:
				a.pwOpen = false
				a.notices = append(a.notices, fmt.Sprintf("Could not %s %s: %s", verb, unit, err))
			}
		})
	})
}

// busyWith reports whether any verb is running on the unit.
func (a *app) busyWith(name string) bool {
	for k := range a.busy {
		if strings.HasSuffix(k, " "+name) {
			return true
		}
	}
	return false
}

// prop looks up one of the selected unit's loaded properties.
func (a *app) prop(key string) string {
	for _, p := range a.props {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// selectedUnit returns the selected unit, or nil when it is filtered out
// or gone from the list.
func (a *app) selectedUnit() *Unit {
	for i := range a.units {
		if a.units[i].Name == a.selName {
			return &a.units[i]
		}
	}
	return nil
}

// switchScope moves between the system and the user manager.
func (a *app) switchScope(s Scope) {
	if a.scope == s {
		return
	}
	a.scope = s
	a.scopeTab = int(s)
	a.selName, a.detailFor = "", ""
	a.props, a.statusText, a.logsText = nil, "", ""
	a.units = nil
	a.refresh()
}

// confirm asks a yes/no question in the window's native dialog. Without a
// window (tests) it says yes.
func (a *app) confirm(message, detail string, buttons ...string) bool {
	if a.win == nil {
		return true
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:  a.win,
		Type:    mygo.MessageQuestion,
		Message: message,
		Detail:  detail,
		Buttons: buttons,
	})
	return err == nil && res.Button == 0
}

func main() {
	a := newApp(cliBackend{})
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "systemctl-ui",
			Width:     1120,
			Height:    720,
			MinWidth:  780,
			MinHeight: 480,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Label: "Units", Submenu: []*mygo.MenuItem{
				{Label: "Refresh", Accelerator: "CmdOrCtrl+R", Click: func(*mygo.MenuItem, *mygo.Window) { a.refresh() }},
			}},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		go func() {
			for range time.Tick(5 * time.Second) {
				a.update(func() {
					if a.autoRefresh && !a.loading {
						a.refresh()
					}
				})
			}
		}()
		a.refresh()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
