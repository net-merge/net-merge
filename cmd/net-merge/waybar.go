package main

import (
	"encoding/json"
	"html"
	"strings"

	"net-merge/internal/state"
	"net-merge/internal/tray"
)

// waybarJSON is one line for a waybar custom module ("return-type": "json"): the total speed as the
// text, each adapter in the tooltip, and the class "on" or "off". Waybar reads text as markup.
func waybarJSON(st *state.Status, on bool) string {
	out := struct {
		Text    string `json:"text"`
		Tooltip string `json:"tooltip"`
		Class   string `json:"class"`
	}{"off", "net-merge is off", "off"}
	if on {
		var down, up float64
		lines := []string{"net-merge is on (Mbps)"}
		for _, n := range st.NICs {
			if !n.Present {
				continue
			}
			down, up = down+n.LinkIn, up+n.LinkOut
			lines = append(lines, html.EscapeString(n.Name)+"  ↓"+tray.Mbps(n.LinkIn)+" ↑"+tray.Mbps(n.LinkOut))
		}
		out.Text, out.Tooltip, out.Class = "↓"+tray.Mbps(down)+" ↑"+tray.Mbps(up), strings.Join(lines, "\n"), "on"
	}
	b, _ := json.Marshal(out)
	return string(b)
}
