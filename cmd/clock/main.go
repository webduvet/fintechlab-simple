// Clock is the lab's business clock: one offset from the real time that
// every vendor in the lab follows, and that a platform under test can
// follow too (docs/plugins.md, "The clock").
//
// Settlement only runs on a business day, so "what happens on a Sunday" is
// a real test, and so is "…and then on Monday, with the same money". This
// service is what makes those two requests rather than a restart of every
// process with a different environment.
//
//	GET    /clock                 the clock as it stands; offset_ms is what a follower reads
//	POST   /clock                 {"at": "<RFC 3339>"}, {"advance": "1d"}, {"mode": "real"|"auto-business-day"}
//	POST   /clock/holds           {"holder", "reason", "ttl_seconds"} — refuse moves while a run is in flight
//	DELETE /clock/holds/{holder}  release it
//	GET    /sim/activity          every move and hold
//
// State is in memory: a restarted clock is on the real time, like a
// restarted lab.
package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // the image has no zoneinfo, and the calendars need two zones

	"github.com/webduvet/fintechlab-simple/internal/httputilx"
	"github.com/webduvet/fintechlab-simple/internal/labclock"
)

func main() {
	addr := env("LISTEN", ":8096")

	cals, err := calendars(env("CLOCK_CALENDARS", "GB=Europe/London,SE=Europe/Stockholm"))
	if err != nil {
		log.Fatalf("clock: %v", err)
	}
	s := labclock.NewServer(cals, nil)

	if start := strings.TrimSpace(os.Getenv("CLOCK_START")); start != "" {
		req := labclock.Request{Reason: "CLOCK_START=" + start}
		if start == string(labclock.ModeAutoBusinessDay) || start == string(labclock.ModeReal) {
			req.Mode = labclock.Mode(start)
		} else {
			req.At = start
		}
		if _, err := s.Move(req); err != nil {
			log.Fatalf("clock: CLOCK_START: %v", err)
		}
	}
	st := s.State()
	log.Printf("clock: %s UTC (%s, %gh from real), business day: %v", st.Now.Format(time.RFC3339), st.Mode, st.OffsetHours, st.BusinessDay)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httputilx.WriteJSON(w, 200, map[string]string{"status": "ok", "service": "clock"})
	})
	s.Routes(mux)

	log.Printf("clock listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// calendars reads "GB=Europe/London,SE=Europe/Stockholm", with each code's
// bank holidays from CLOCK_HOLIDAYS_<CODE> as YYYY-MM-DD, comma-separated.
func calendars(spec string) ([]labclock.Calendar, error) {
	var out []labclock.Calendar
	for _, part := range strings.Split(spec, ",") {
		code, zone, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		code = strings.ToUpper(strings.TrimSpace(code))
		c, err := labclock.NewCalendar(code, strings.TrimSpace(zone),
			strings.FieldsFunc(os.Getenv("CLOCK_HOLIDAYS_"+code), func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }))
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
