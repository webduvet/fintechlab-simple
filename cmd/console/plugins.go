package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/console"
	"github.com/webduvet/fintechlab-simple/internal/httputilx"
)

// Plugins: the platform under test registers its own card (docs/plugins.md).
//
// Everything here is generic. The console forwards a button to the path the
// plugin declared for it and to nothing else, so a plugin is one POST
// away from a card with working buttons, and this file never learns a
// platform's name.

// renewEvery is what a plugin is told to renew at: a third of the TTL, so a
// single lost renewal is not a lapse.
const renewEvery = console.PluginTTL / 3

func (a *app) registerPlugin(w http.ResponseWriter, r *http.Request) {
	var p console.Plugin
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
		httputilx.Error(w, 400, "descriptor: "+err.Error())
		return
	}
	info, err := a.cat.Register(p)
	if err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	a.applyPluginStandIns(p)
	// Probe at once rather than on the next tick, so the card a developer
	// is watching turns green when the platform says it is there.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.mon.Probe(ctx, p.ID)
	}()
	httputilx.WriteJSON(w, 200, map[string]any{
		"id":            p.ID,
		"state":         info.State,
		"ttl_seconds":   info.TTLSeconds,
		"renew_seconds": int(renewEvery / time.Second),
		"clock":         info.Clock,
	})
}

func (a *app) unregisterPlugin(w http.ResponseWriter, r *http.Request) {
	if !a.cat.Unregister(r.PathValue("id")) {
		httputilx.Error(w, 404, "no plugin "+r.PathValue("id"))
		return
	}
	w.WriteHeader(204)
}

func (a *app) listPlugins(w http.ResponseWriter, r *http.Request) {
	httputilx.WriteJSON(w, 200, map[string]any{"plugins": a.cat.Plugins()})
}

// livePlugin finds a plugin a button may be forwarded to. A stopped one
// has said it is not there, so the answer is that, not a connection error.
func (a *app) livePlugin(w http.ResponseWriter, id string) (console.Plugin, bool) {
	p, info, ok := a.cat.Plugin(id)
	if !ok {
		httputilx.Error(w, 404, "no plugin "+id)
		return p, false
	}
	if info.State == console.PluginStopped {
		httputilx.Error(w, 409, p.Name+" has stopped; start it again first")
		return p, false
	}
	return p, true
}

// pluginAction runs one declared button: the method and path the plugin
// registered, with the request's body as it came.
func (a *app) pluginAction(w http.ResponseWriter, r *http.Request) {
	p, ok := a.livePlugin(w, r.PathValue("id"))
	if !ok {
		return
	}
	for _, act := range p.Actions {
		if act.ID == r.PathValue("action") {
			a.forward(w, r, act.Method, p.BaseURL+act.Path)
			return
		}
	}
	httputilx.Error(w, 404, p.Name+" declares no action "+r.PathValue("action"))
}

func (a *app) pluginFiles(w http.ResponseWriter, r *http.Request) {
	p, ok := a.livePlugin(w, r.PathValue("id"))
	if !ok {
		return
	}
	if p.Settlement == nil {
		httputilx.Error(w, 404, p.Name+" runs no settlement files")
		return
	}
	ctx, cancel := reqContext(r)
	defer cancel()
	var out any
	if err := a.getJSON(ctx, a.client, p.BaseURL+p.Settlement.FilesPath, &out); err != nil {
		httputilx.Error(w, 502, err.Error())
		return
	}
	httputilx.WriteJSON(w, 200, out)
}

// pluginRun starts settlement runs — {"files": […]}, forwarded verbatim.
// Several files are still one call: the platform starts them together,
// which is the point of running them together.
func (a *app) pluginRun(w http.ResponseWriter, r *http.Request) {
	p, ok := a.livePlugin(w, r.PathValue("id"))
	if !ok {
		return
	}
	if p.Settlement == nil {
		httputilx.Error(w, 404, p.Name+" runs no settlement files")
		return
	}
	a.forward(w, r, http.MethodPost, p.BaseURL+p.Settlement.RunPath)
}

// forward sends the request's JSON body to url with method and answers with
// whatever url answered, status and all — the peer's refusal in its own
// words.
func (a *app) forward(w http.ResponseWriter, r *http.Request, method, url string) {
	ctx, cancel := reqContext(r)
	defer cancel()
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	if err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		httputilx.Error(w, 500, err.Error())
		return
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		httputilx.Error(w, 502, err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out any
	if len(bytes.TrimSpace(raw)) == 0 {
		out = map[string]any{}
	} else if err := json.Unmarshal(raw, &out); err != nil {
		out = map[string]any{"output": string(raw)}
	}
	httputilx.WriteJSON(w, resp.StatusCode, out)
}

// clockLock says why the lab clock must not move, or "": a live platform
// that cannot follow it would be left on a different day from the vendors
// it talks to.
func (a *app) clockLock() string {
	if name, ok := a.cat.WallClockPlugin(); ok {
		return name + " is registered on the wall clock and cannot follow the lab's; moving the lab would put its vendors on a different day from the platform they serve"
	}
	return ""
}
