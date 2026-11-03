package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// icon parses one 24x24 stroke icon, drawn in the text color of its
// element.
func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var closeIcon = icon(`<path d="M18 6 6 18M6 6l12 12"/>`)

// stateOptions are the state filter's choices; the first means no filter.
var stateOptions = []string{"All states", "active", "failed", "inactive"}

// unitTypes orders the sidebar's type filter; types absent from the
// loaded units are skipped.
var unitTypes = []struct {
	typ, label string
}{
	{"service", "Services"},
	{"timer", "Timers"},
	{"socket", "Sockets"},
	{"target", "Targets"},
	{"path", "Paths"},
	{"mount", "Mounts"},
	{"automount", "Automounts"},
	{"swap", "Swaps"},
	{"slice", "Slices"},
	{"scope", "Scopes"},
	{"device", "Devices"},
}

// unitColumns heads the units table. The state dot comes first, as in
// systemctl's own listing.
var unitColumns = []ui.TableColumn{
	{Title: "", ID: "state", Width: 30, Fixed: true},
	{Title: "Unit", ID: "unit", Sortable: true, MinWidth: 180},
	{Title: "Load", ID: "load", Width: 76, Sortable: true},
	{Title: "Active", ID: "active", Width: 76, Sortable: true},
	{Title: "Sub", ID: "sub", Width: 100, Sortable: true},
	{Title: "Description", ID: "description", Sortable: true, MinWidth: 200},
}

// stateColor gives a unit the color systemctl would print its dot in.
func stateColor(t *ui.Theme, u Unit) ui.Color {
	switch {
	case u.Active == "failed" || u.Load == "error" || u.Load == "not-found":
		return t.Danger
	case u.Active == "active":
		return t.Success
	case u.Active == "activating" || u.Active == "deactivating" || u.Active == "reloading":
		return t.Warning
	default:
		return t.TextMuted
	}
}

// visibleUnits applies the search and the type and state filters.
func visibleUnits(units []Unit, search, typeFilter, stateFilter string) []Unit {
	search = strings.ToLower(strings.TrimSpace(search))
	out := make([]Unit, 0, len(units))
	for _, u := range units {
		if typeFilter != "" && u.Type() != typeFilter {
			continue
		}
		if stateFilter != "" && stateFilter != stateOptions[0] && u.Active != stateFilter {
			continue
		}
		if search != "" &&
			!strings.Contains(strings.ToLower(u.Name), search) &&
			!strings.Contains(strings.ToLower(u.Description), search) {
			continue
		}
		out = append(out, u)
	}
	return out
}

// sortVisible orders rows by the column the table's header last chose.
func sortVisible(rows []Unit, s ui.SortOrder) {
	key := func(u Unit) string {
		switch s.Column {
		case "load":
			return u.Load
		case "active":
			return u.Active
		case "sub":
			return u.Sub
		case "description":
			return strings.ToLower(u.Description)
		default:
			return u.Name
		}
	}
	slices.SortStableFunc(rows, func(x, y Unit) int {
		c := strings.Compare(key(x), key(y))
		if s.Descending {
			return -c
		}
		return c
	})
}

func (a *app) view(c *ui.Context) {
	for _, n := range a.notices {
		c.Toast(n)
	}
	a.notices = a.notices[:0]

	// Esc closes the detail pane, unless the search field owns it.
	if a.selName != "" && !a.searchFocus.Focused(c) && c.Shortcut(0, ui.KeyEscape) {
		a.selName = ""
	}
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.toolbar(c)
			rows := visibleUnits(a.units, a.search, a.typeFilter, a.stateFilter)
			sortVisible(rows, a.sort)
			// Reconcile the two selection keepers: the table edits the
			// row index, the rest of the app keeps the unit's name. The
			// name wins unless the index moved during this build, which
			// is the user choosing a row; syncing the name back
			// unconditionally would resurrect a selection Close just
			// cleared.
			a.selRow = slices.IndexFunc(rows, func(u Unit) bool { return u.Name == a.selName })
			synced := a.selRow
			if a.selName == "" {
				a.unitsTable(c, rows).Grow(1).MinHeight(0)
			} else {
				ui.SplitVerticalPercent(c, &a.split, func() {
					a.unitsTable(c, rows).Fill()
				}, func() {
					a.detail(c)
				}).Grow(1).MinHeight(0)
			}
			if a.selRow != synced {
				if a.selRow >= 0 && a.selRow < len(rows) {
					a.selName = rows[a.selRow].Name
				} else {
					a.selName = ""
				}
			}
			if a.selName == "" {
				a.detailFor = ""
			} else if a.selName != a.detailFor {
				a.loadDetails()
			}
			a.statusbar(c)
		})
	})
	a.sudoDialog(c)
}

// sudoDialog asks for the sudo password when the system manager refuses
// a verb for want of privilege. It shows only while pwOpen holds.
func (a *app) sudoDialog(c *ui.Context) {
	t := c.Theme()
	ui.DialogBase(c, &a.pwOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.4))
		panel.Width(360).Padding(20).Gap(12).Radius(t.Space(2.5)).Background(t.Background).
			Label("Administrator access required").
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Children(func() {
			ui.Text(c, "Administrator access required").Bold().FontSize(15)
			ui.Textf(c, "sudo systemctl %s %s", a.pwVerb, a.pwUnit).
				Font("monospace").FontSize(12).TextColor(t.TextMuted)
			pw := ui.TextInput(c, &a.pwValue).Password().AutoFocus().
				Placeholder("sudo password").Label("sudo password")
			if pw.Submitted() {
				a.runPrivileged()
			}
			if a.pwErr != "" {
				ui.Text(c, a.pwErr).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				if a.pwBusy {
					ui.Text(c, "Authenticating…").FontSize(12).TextColor(t.TextMuted)
				}
				if ui.Button(c, "Cancel").Clicked() {
					a.pwOpen, a.pwValue = false, ""
				}
				if ui.PrimaryButton(c, "Authenticate").
					Disabled(a.pwBusy || a.pwValue == "").Clicked() {
					a.runPrivileged()
				}
			})
		})
	})
}

// sidebar holds the scope switch and the type filter with its counts.
func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(220).Shrink(0).Background(t.Surface).Children(func() {
		ui.Row(c).Padding(14, 12, 12, 12).Children(func() {
			if ui.Tabs(c, &a.scopeTab, "System", "User").Grow(1).Label("Manager scope").Changed() {
				a.switchScope(Scope(a.scopeTab))
			}
		})
		counts := map[string]int{}
		for _, u := range a.units {
			counts[u.Type()]++
		}
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(1).Padding(0, 8, 8, 8).Children(func() {
				a.filterItem(c, "All units", "", len(a.units))
				for _, ut := range unitTypes {
					if counts[ut.typ] > 0 {
						a.filterItem(c, ut.label, ut.typ, counts[ut.typ])
					}
				}
			})
		})
	})
}

// filterItem is one clickable sidebar row choosing the type filter.
func (a *app) filterItem(c *ui.Context, label, value string, count int) {
	t := c.Theme()
	r := ui.Row(c).Padding(6, 10).AlignItems(ui.Center).Radius(t.Radius).
		Focusable().Label(label)
	switch {
	case a.typeFilter == value:
		r.Background(t.Accent.Alpha(0.16))
	case r.Hovered():
		r.Background(t.SurfaceHover)
	}
	r.Children(func() {
		ui.Text(c, label).Grow(1).FontSize(13)
		ui.Textf(c, "%d", count).FontSize(12).TextColor(t.TextMuted)
	})
	if r.Clicked() {
		a.typeFilter = value
	}
}

// toolbar holds the filters, refresh, and the verbs for the selected unit.
func (a *app) toolbar(c *ui.Context) {
	u := a.selectedUnit()
	busy := u != nil && a.busyWith(u.Name)
	ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c, &a.search).Placeholder("Filter units").Width(220).
			Label("Filter units").Bind(&a.searchFocus)
		ui.Select(c, &a.stateFilter, stateOptions).Width(118).Label("State filter")
		if ui.Button(c, "Refresh").Tooltip("Reload the unit list (Ctrl+R)").Disabled(a.loading).Clicked() {
			a.refresh()
		}
		ui.Spacer(c)
		if ui.Button(c, "Start").Disabled(u == nil || u.Active == "active" || busy).Clicked() {
			a.action("start")
		}
		if ui.Button(c, "Stop").Disabled(u == nil || (u.Active != "active" && u.Active != "activating") || busy).Clicked() {
			a.action("stop")
		}
		if ui.Button(c, "Restart").Disabled(u == nil || u.Active != "active" || busy).Clicked() {
			a.action("restart")
		}
		if ui.Button(c, "Reload").Disabled(u == nil || u.Active != "active" || busy).Clicked() {
			a.action("reload")
		}
	})
}

// unitsTable is the sortable list of units; it is the app's hero.
func (a *app) unitsTable(c *ui.Context, rows []Unit) ui.Element {
	t := c.Theme()
	if len(rows) == 0 {
		return ui.Column(c).Fill().Center().Gap(8).Children(func() {
			switch {
			case a.loading && a.units == nil:
				ui.Text(c, "Loading units…").TextColor(t.TextMuted)
			case a.listErr != "" && a.units == nil:
				ui.Text(c, "Could not list units").Bold()
				ui.Text(c, a.listErr).FontSize(12).TextColor(t.Danger)
			case len(a.units) == 0:
				ui.Text(c, "No units loaded").TextColor(t.TextMuted)
			default:
				ui.Text(c, "No units match the filter").TextColor(t.TextMuted)
			}
		})
	}
	return ui.Table(c, &a.listState, unitColumns, len(rows), func(row, col int) {
		u := rows[row]
		// A chosen row paints itself in the accent color; per-cell
		// colors must follow it onto that background, except the
		// state dot, which keeps its meaning.
		onAccent := u.Name == a.selName
		muted, state := t.TextMuted, stateColor(t, u)
		if onAccent {
			muted = t.AccentText
		}
		switch col {
		case 0:
			ui.Text(c, "●").FontSize(9).TextColor(state).
				Tooltip(fmt.Sprintf("%s (%s)", u.Active, u.Sub))
		case 1:
			ui.Text(c, u.Name).Font("monospace").SingleLine()
		case 2:
			ui.Text(c, u.Load).SingleLine().TextColor(muted)
		case 3:
			if onAccent {
				state = t.AccentText
			}
			ui.Text(c, u.Active).SingleLine().TextColor(state)
		case 4:
			ui.Text(c, u.Sub).SingleLine().TextColor(muted)
		case 5:
			ui.Text(c, u.Description).SingleLine().TextColor(muted)
		}
	}).Label("Units")
}

// detail is the pane for the selected unit: its facts, its status, its
// journal.
func (a *app) detail(c *ui.Context) {
	t := c.Theme()
	name := a.selName
	ui.Column(c).Fill().MinHeight(0).Children(func() {
		ui.Row(c).Padding(4, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Tabs(c, &a.tab, "Overview", "Status", "Logs").Label("Detail tabs")
			ui.Spacer(c)
			if a.detailLoading {
				ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
			}
			if a.tab > 0 {
				if ui.Button(c, "Reload").Disabled(a.detailLoading).
					Tooltip("Fetch the text again").Clicked() {
					a.detailFor = "" // the view reloads it next frame
				}
			}
			close := ui.ButtonBase(c).Padding(7).Radius(t.Radius).
				Background(t.Surface).Border(1, t.Border).
				Label("Close detail view").Tooltip("Close detail view (Esc)")
			if close.Hovered() {
				close.Background(t.SurfaceHover)
			}
			close.Children(func() {
				ui.Icon(c, closeIcon).FontSize(15)
			})
			if close.Clicked() {
				a.selName = ""
			}
		})
		ui.Box(c).Height(1).Shrink(0).Background(t.Border)
		switch {
		case a.detailFor != name, a.detailLoading && a.props == nil:
			ui.Column(c).Fill().Center().Children(func() {
				ui.Text(c, "Loading…").TextColor(t.TextMuted)
			})
		case a.tab == 0:
			a.overview(c)
		case a.tab == 1:
			a.textTab(c, a.statusText, "No status text")
		default:
			a.textTab(c, a.logsText, "No journal lines for this unit")
		}
	})
}

// overview shows the selected unit's headline facts, its unit-file verbs,
// and every property systemctl show reports.
func (a *app) overview(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().MinHeight(0).Children(func() {
		ui.Column(c).Padding(10, 14).Gap(6).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				u := a.selectedUnit()
				dot := ui.Color(t.TextMuted)
				if u != nil {
					dot = stateColor(t, *u)
				}
				ui.Text(c, "●").FontSize(12).TextColor(dot)
				ui.Text(c, a.selName).Font("monospace").FontSize(15).Bold().
					Grow(1).MinWidth(0).SingleLine()
				a.unitFileButtons(c)
			})
			if u := a.selectedUnit(); u != nil && u.Description != "" {
				ui.Text(c, u.Description).TextColor(t.TextMuted)
			}
			if ufs := a.prop("UnitFileState"); ufs != "" {
				ui.Textf(c, "unit file: %s", ufs).FontSize(12).TextColor(t.TextMuted)
			}
		})
		ui.Box(c).Height(1).Shrink(0).Background(t.Border)
		if len(a.props) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				if a.detailErr != "" {
					ui.Text(c, a.detailErr).FontSize(12).TextColor(t.Danger)
				} else {
					ui.Text(c, "No properties").TextColor(t.TextMuted)
				}
			})
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(8, 14).Gap(3).Selectable().Children(func() {
				for _, p := range a.props {
					ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
						ui.Text(c, p.Key).Font("monospace").FontSize(12).
							TextColor(t.TextMuted).Width(230).Shrink(0)
						ui.Text(c, p.Value).Font("monospace").FontSize(12).
							Grow(1).MinWidth(0)
					})
				}
			})
		})
	})
}

// unitFileButtons are the verbs about the unit file, not its process:
// enable, disable, mask, unmask.
func (a *app) unitFileButtons(c *ui.Context) {
	name := a.selName
	busy := a.busyWith(name)
	switch a.prop("UnitFileState") {
	case "enabled":
		if ui.Button(c, "Disable").Disabled(busy).
			Tooltip("Take the unit out of the boot sequence").Clicked() {
			a.action("disable")
		}
	case "static", "indirect":
		ui.Button(c, "Enable").Disabled(true).
			Tooltip("This unit has no [Install] section")
	default:
		if ui.Button(c, "Enable").Disabled(busy).
			Tooltip("Start the unit at boot").Clicked() {
			a.action("enable")
		}
	}
	if a.prop("UnitFileState") == "masked" {
		if ui.Button(c, "Unmask").Disabled(busy).Clicked() {
			a.action("unmask")
		}
	} else if ui.Button(c, "Mask").Disabled(busy).
		Tooltip("Link the unit to /dev/null so nothing can start it").Clicked() &&
		a.confirm("Mask "+name+"?",
			"A masked unit cannot be started, by hand or as a dependency, until it is unmasked.",
			"Mask", "Cancel") {
		a.action("mask")
	}
}

// textTab shows one fetched text — a status or a journal — in monospace.
func (a *app) textTab(c *ui.Context, text, emptyHint string) {
	t := c.Theme()
	if strings.TrimSpace(text) == "" {
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, emptyHint).TextColor(t.TextMuted)
		})
		return
	}
	ui.Scroll(c).Fill().Children(func() {
		ui.Text(c, text).Font("monospace").FontSize(12).Padding(10, 14).Selectable()
	})
}

// statusbar is the bottom line: the counts, the failures loud, the
// refresh state quiet.
func (a *app) statusbar(c *ui.Context) {
	t := c.Theme()
	var active, failed int
	for _, u := range a.units {
		switch u.Active {
		case "active":
			active++
		case "failed":
			failed++
		}
	}
	ui.Row(c).Padding(4, 12).Gap(12).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		ui.Textf(c, "%d units", len(a.units)).FontSize(12).TextColor(t.TextMuted)
		ui.Textf(c, "%d active", active).FontSize(12).TextColor(t.TextMuted)
		f := ui.Textf(c, "%d failed", failed).FontSize(12)
		if failed > 0 {
			f.TextColor(t.Danger).Bold()
		} else {
			f.TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		if a.listErr != "" {
			ui.Text(c, a.listErr).FontSize(12).TextColor(t.Danger).SingleLine()
		}
		ui.Toggle(c, &a.autoRefresh, "Auto-refresh").Label("Reload the list every 5 seconds")
		stamp := "—"
		if !a.refreshedAt.IsZero() {
			stamp = a.refreshedAt.Format("15:04:05")
		}
		ui.Textf(c, "Updated %s", stamp).FontSize(12).TextColor(t.TextMuted)
	})
}
