package main

import (
	"fmt"
	"path"

	"github.com/webduvet/fintechlab-simple/internal/activity"
)

// What the console's file-exchange panel says about this acquirer.

// recordSFTP turns one SFTP request into one line.
//
// The verbs are the client's, not this server's: "downloaded
// download/…csv.pgp" is what the platform did, and that is what an
// operator is checking. A refusal keeps the server's own error, because
// "permission denied" and "no such file" send you to different places.
func recordSFTP(l *activity.Log) func(user, remote, op, p string, err error) {
	return func(user, remote, op, p string, err error) {
		ev := activity.Event{Op: "sftp." + op, Peer: remote, Status: activity.StatusOK}
		detail := map[string]string{}
		if user != "" {
			detail["user"] = user
		}
		if p != "" {
			detail["path"] = p
		}
		switch op {
		case "session":
			if err != nil {
				// A client with the wrong key and a client that never
				// connected look identical from the settlement side.
				ev.Summary = "connection refused: " + err.Error()
				ev.Status = activity.StatusWarn
				break
			}
			ev.Summary = fmt.Sprintf("%s connected over SFTP", displayUser(user))
		case "download":
			ev.Summary = fmt.Sprintf("%s collected %s", displayUser(user), path.Base(p))
		case "upload":
			ev.Summary = fmt.Sprintf("%s delivered %s", displayUser(user), path.Base(p))
		case "list":
			ev.Summary = fmt.Sprintf("%s listed %s", displayUser(user), p)
		default:
			ev.Summary = fmt.Sprintf("%s %s %s", displayUser(user), op, p)
		}
		if err != nil && op != "session" {
			ev.Summary += " — refused: " + err.Error()
			ev.Status = activity.StatusWarn
			detail["error"] = err.Error()
		}
		if len(detail) > 0 {
			ev.Detail = detail
		}
		l.Record(ev)
	}
}

func displayUser(user string) string {
	if user == "" {
		return "a client"
	}
	return user
}

// summarizeFileRead covers the HTTP mirror of the same exchange. It is the
// same conversation from the acquirer's point of view — someone came and
// took a settlement file — so it lands in the same panel rather than a
// second one nobody would think to open.
func summarizeFileRead(c *activity.Call) (string, map[string]string) {
	r := c.Request
	file := r.PathValue("filename")
	return fmt.Sprintf("collected %s over HTTP", file), map[string]string{
		"merchant": r.PathValue("merchant_id"),
		"category": r.PathValue("category"),
		"file":     file,
		"bytes":    fmt.Sprintf("%d", len(c.RespBody)),
	}
}
