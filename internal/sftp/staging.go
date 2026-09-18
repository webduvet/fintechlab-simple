package sftp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/webduvet/fintechlab-simple/internal/settlement"
)

// Stager writes settlement report files to a structured directory tree
// under OutDir ({OutDir}/{merchant_id}/{filename}), and separately exposes
// a StagingDir for files that would be "in progress" in a real SFT
// integration. Nothing in this package currently writes into StagingDir --
// Stage writes directly to OutDir, matching
// ARCHITECTURE-sftp-staging.md section 4's flow -- so ListStaging honestly
// reflects an idle configured-but-unused directory rather than fabricating
// a pending-file lifecycle the docs never asked for.
type Stager struct {
	OutDir     string // base directory for staged (finalized) files
	StagingDir string // base directory for in-progress files (currently unused by Stage)
}

// Stage writes a report file for a given merchant and format ("csv", "xml",
// or "json") and returns its path relative to OutDir. Rejects
// report.MerchantID if it is not safe to use as a single directory
// segment (see ValidatePathSegment) -- this is the point merchant_id
// enters the filesystem-writing side of this package.
func (s *Stager) Stage(report *settlement.Report, format string) (string, error) {
	if err := ValidatePathSegment(report.MerchantID); err != nil {
		return "", err
	}
	var data []byte
	var err error
	switch format {
	case "csv":
		data, err = settlement.ToCSV(report)
	case "xml":
		data, err = settlement.ToXML(report)
	case "json":
		data, err = json.MarshalIndent(report, "", "  ")
	default:
		return "", fmt.Errorf("sftp: unsupported format %q", format)
	}
	if err != nil {
		return "", err
	}

	filename := fmt.Sprintf("%s_%s_%s_WX.%s", report.MerchantID, report.TransactionsDate, report.Currency, format)
	rel := filepath.Join(report.MerchantID, filename)
	full := filepath.Join(s.OutDir, rel)
	// 0o777, not 0o755: this directory is shared cross-container (settlement
	// writes it, worldline reads it) and cross-UID with the host (a
	// developer running `rm -rf sftp/` locally) — same fake-and-obvious-lab
	// tradeoff as ca/generate.sh's cert permissions.
	if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// ListOut returns every file under OutDir, sorted by modified time
// descending (newest first).
func (s *Stager) ListOut() ([]os.FileInfo, error) {
	return listDir(s.OutDir)
}

// ListOutMerchant returns every file staged directly under
// {OutDir}/{merchantID} — the per-merchant subdirectory Stage (and
// cmd/settlement's stageBambora) both write into. Unlike ListOut, this
// does not depend on the filename matching any particular format: the
// directory itself is the source of truth for which merchant a file
// belongs to, which matters once more than one file format is staged
// here (the WX filename convention embeds a merchant id; the real
// Worldline/Bambora settlement CSV does not, since a real Bambora file
// can span multiple merchants — see
// docs/ARCHITECTURE-vendor-corrections.md section 1).
func (s *Stager) ListOutMerchant(merchantID string) ([]os.FileInfo, error) {
	return listDir(filepath.Join(s.OutDir, merchantID))
}

// ListStaging returns every file under StagingDir, sorted by modified time
// descending.
func (s *Stager) ListStaging() ([]os.FileInfo, error) {
	return listDir(s.StagingDir)
}

func listDir(root string) ([]os.FileInfo, error) {
	var out []os.FileInfo
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == root {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			out = append(out, info)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime().After(out[j].ModTime()) })
	return out, nil
}

// ReadOut reads and returns the contents of a file by its path relative to
// OutDir. relativePath is cleaned and rejected if it would escape OutDir
// (e.g. via "..") -- this is reachable directly from an HTTP path segment
// in cmd/settlement, so it must not become a traversal primitive.
func (s *Stager) ReadOut(relativePath string) ([]byte, error) {
	full, err := safeJoin(s.OutDir, relativePath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}

func safeJoin(base, rel string) (string, error) {
	clean := filepath.Clean("/" + rel)[1:] // strip any leading ".." segments' escape via a rooted clean
	full := filepath.Join(base, clean)
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if fullAbs != baseAbs && !strings.HasPrefix(fullAbs, baseAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("sftp: path %q escapes base directory", rel)
	}
	return full, nil
}
