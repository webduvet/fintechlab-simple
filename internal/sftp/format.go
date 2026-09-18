// Package sftp simulates the SFTP staging directory Worldline delivers
// settlement report files into: a structured directory tree written with
// plain os.MkdirAll/os.WriteFile, no SSH/SFTP protocol involved. See
// docs/ARCHITECTURE-sftp-staging.md.
package sftp

import (
	"fmt"
	"regexp"
	"strings"
)

// filenamePattern matches "{merchant_id}_{transactions_date}_{currency}_WX.{format}".
// merchantID is captured greedily (non-greedy would still work since the
// remaining fields are fixed-shape, but greedy is simplest and correct)
// because merchant IDs elsewhere in this lab embed underscores themselves
// (e.g. "acc_merchant"); transactions_date and currency are anchored to
// their fixed shapes so they can't be swallowed by that capture. The
// capture explicitly excludes "/" and "\\" so a filename that embeds a
// path separator (e.g. from a directory listing of untrusted input) is
// never parsed as a valid merchant ID instead of being rejected.
var filenamePattern = regexp.MustCompile(`^([^/\\]+)_(\d{4}-\d{2}-\d{2})_([A-Za-z]{3})_WX\.(csv|xml|json)$`)

// ValidatePathSegment rejects a string that is unsafe to use as a single
// filesystem path segment: empty, ".", "..", or containing a path
// separator. Stage and cmd/settlement's file-read handler both call this
// on merchant_id (and, for the handler, filename) before it reaches a
// filepath.Join -- a URL path segment that "looks like" one clean
// component can still decode to a string containing "/" or ".." (e.g. a
// %2f-encoded slash), so validate the decoded string itself rather than
// trusting the router's segment boundaries.
func ValidatePathSegment(s string) error {
	if s == "" || s == "." || s == ".." {
		return fmt.Errorf("sftp: invalid path segment %q", s)
	}
	if strings.ContainsAny(s, "/\\") {
		return fmt.Errorf("sftp: path segment %q must not contain a path separator", s)
	}
	return nil
}

// ParseFilename extracts merchantID, date, currency, and format from a
// Worldline-style filename.
func ParseFilename(name string) (merchantID, date, currency, format string, err error) {
	m := filenamePattern.FindStringSubmatch(name)
	if m == nil {
		return "", "", "", "", fmt.Errorf("sftp: invalid filename %q", name)
	}
	return m[1], m[2], m[3], m[4], nil
}

// ValidateFilename checks that a filename matches the expected pattern.
func ValidateFilename(name string) error {
	_, _, _, _, err := ParseFilename(name)
	return err
}
