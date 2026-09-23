package bankingcircle

import (
	"time"
	// The runtime image is plain alpine, with no zoneinfo: embed Go's own
	// copy so Europe/Paris always resolves.
	_ "time/tzdata"
)

// Banking Circle dates a booking by its own business day, not by the UTC
// calendar. Its docs put the day's end at 19:00 CET ("Payments after 19:00
// CET will appear in the next business day's report"; "The last available
// report for transaction date = current calendar date will be at 19.00,
// following our EOD cycle"). So a booking at or after 19:00 Central
// European time belongs to the next business day, and one made on a
// weekend to the Monday after.
//
// Central European time is read as Europe/Paris, which keeps CET and CEST
// in step with the bank. Weekends are the only non-business days here: the
// docs do not list the bank's holidays, and the lab does not guess them.
var bankLocation = mustLoadLocation("Europe/Paris")

// bankCutoffHour is the end of Banking Circle's business day, local time.
const bankCutoffHour = 19

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// BusinessDate is the Banking Circle business day (YYYY-MM-DD) a booking
// at ts falls on. A value that is already a bare date is taken as that
// day; anything unparseable yields "".
func BusinessDate(ts string) string {
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		if len(ts) == len("2006-01-02") {
			if _, err := time.Parse("2006-01-02", ts); err == nil {
				return ts
			}
		}
		return ""
	}
	local := at.In(bankLocation)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, bankLocation)
	if local.Hour() >= bankCutoffHour {
		day = day.AddDate(0, 0, 1)
	}
	for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		day = day.AddDate(0, 0, 1)
	}
	return day.Format("2006-01-02")
}
