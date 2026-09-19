package sshftp

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// jail is a chroot for SFTP sessions. Every client path — absolute or
// relative, with or without ".." — is interpreted as a path *inside* root.
// Host-absolute paths such as /etc/passwd become <root>/etc/passwd.
type jail struct {
	root string // absolute, cleaned
	// obs, when set, is told about each request the session serves. It is
	// how the vendor binary turns "someone fetched a file" into something
	// an operator can see; nil leaves the jail exactly as it was.
	obs Observer
}

// Observer is notified once per SFTP request, after it has been answered.
// A refused request is reported too, with its error — the denials are the
// half worth watching, since a client pulling from the wrong directory
// looks identical to no client at all.
type Observer func(op, path string, err error)

func (j *jail) report(op, path string, err error) {
	if j.obs != nil {
		j.obs(op, path, err)
	}
}

func newJail(root string) (*jail, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("sshftp: jail root: %w", err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("sshftp: jail root: %w", err)
		}
		abs = filepath.Clean(abs)
	}
	return &jail{root: abs}, nil
}

func (j *jail) handlers() sftp.Handlers {
	return sftp.Handlers{FileGet: j, FilePut: j, FileCmd: j, FileList: j}
}

// resolve maps an SFTP path onto a local path under root. Client-absolute
// paths are jail-absolute, never host-absolute.
func (j *jail) resolve(p string) (string, error) {
	if p == "" {
		p = "/"
	}
	posix := path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	rel := strings.TrimPrefix(posix, "/")
	full := j.root
	if rel != "" {
		full = filepath.Join(j.root, filepath.FromSlash(rel))
	}
	full = filepath.Clean(full)
	relToRoot, err := filepath.Rel(j.root, full)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", os.ErrPermission
	}
	return full, nil
}

// confined resolves p and refuses any symlink that would walk out of root.
//
// This is check-then-open: a symlink could be swapped after the walk and
// before os.OpenFile. That TOCTOU window is acceptable in this lab; do not
// treat confined() as a substitute for a kernel-enforced openat.
func (j *jail) confined(p string) (string, error) {
	full, err := j.resolve(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(j.root, full)
	if err != nil {
		return "", os.ErrPermission
	}
	if rel == "." {
		return j.root, nil
	}
	acc := j.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		acc = filepath.Join(acc, part)
		fi, err := os.Lstat(acc)
		if err != nil {
			if os.IsNotExist(err) {
				return full, nil
			}
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := filepath.EvalSymlinks(acc)
		if err != nil {
			return "", err
		}
		trel, err := filepath.Rel(j.root, target)
		if err != nil || trel == ".." || strings.HasPrefix(trel, ".."+string(filepath.Separator)) {
			return "", os.ErrPermission
		}
		acc = target
	}
	return full, nil
}

func (j *jail) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	f, err := j.open(r)
	j.report("download", r.Filepath, err)
	return readerOrNil(f, err)
}

func (j *jail) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	f, err := j.open(r)
	j.report("upload", r.Filepath, err)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// readerOrNil keeps a failed open returning a nil interface rather than a
// non-nil interface holding a nil *os.File, which the sftp server would
// happily call Read on.
func readerOrNil(f *os.File, err error) (io.ReaderAt, error) {
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (j *jail) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	return j.open(r)
}

func (j *jail) open(r *sftp.Request) (*os.File, error) {
	full, err := j.confined(r.Filepath)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(full, osFlags(r.Flags), 0o644)
}

func (j *jail) Filecmd(r *sftp.Request) error {
	err := j.filecmd(r)
	j.report(strings.ToLower(r.Method), r.Filepath, err)
	return err
}

func (j *jail) filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Setstat":
		full, err := j.confined(r.Filepath)
		if err != nil {
			return err
		}
		if r.AttrFlags().Size {
			return os.Truncate(full, int64(r.Attributes().Size))
		}
		return nil
	case "Rename":
		from, err := j.confined(r.Filepath)
		if err != nil {
			return err
		}
		to, err := j.confined(r.Target)
		if err != nil {
			return err
		}
		return os.Rename(from, to)
	case "Rmdir":
		full, err := j.confined(r.Filepath)
		if err != nil {
			return err
		}
		return os.Remove(full)
	case "Remove":
		full, err := j.confined(r.Filepath)
		if err != nil {
			return err
		}
		return os.Remove(full)
	case "Mkdir":
		full, err := j.confined(r.Filepath)
		if err != nil {
			return err
		}
		return os.Mkdir(full, 0o755)
	case "Symlink":
		// Refuse: a symlink is how a jailed tree grows a hole.
		return os.ErrPermission
	case "Link":
		from, err := j.confined(r.Filepath)
		if err != nil {
			return err
		}
		to, err := j.confined(r.Target)
		if err != nil {
			return err
		}
		return os.Link(from, to)
	default:
		return fmt.Errorf("sshftp: unsupported command %q", r.Method)
	}
}

func (j *jail) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	l, err := j.filelist(r)
	// Stat is the noisiest thing an SFTP client does — every client stats
	// a path before touching it — so only a directory listing is reported.
	// A panel that logged four stats per download would hide the download.
	if r.Method == "List" {
		j.report("list", r.Filepath, err)
	}
	return l, err
}

func (j *jail) filelist(r *sftp.Request) (sftp.ListerAt, error) {
	full, err := j.confined(r.Filepath)
	if err != nil {
		return nil, err
	}
	switch r.Method {
	case "List":
		entries, err := os.ReadDir(full)
		if err != nil {
			return nil, err
		}
		infos := make([]os.FileInfo, 0, len(entries))
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			infos = append(infos, info)
		}
		return listerAt(infos), nil
	case "Stat", "Lstat":
		var info os.FileInfo
		if r.Method == "Lstat" {
			info, err = os.Lstat(full)
		} else {
			info, err = os.Stat(full)
		}
		if err != nil {
			return nil, err
		}
		return listerAt{info}, nil
	default:
		return nil, fmt.Errorf("sshftp: unsupported list %q", r.Method)
	}
}

func (j *jail) RealPath(p string) (string, error) {
	full, err := j.confined(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(j.root, full)
	if err != nil {
		return "", os.ErrPermission
	}
	if rel == "." {
		return "/", nil
	}
	return path.Join("/", filepath.ToSlash(rel)), nil
}

type listerAt []os.FileInfo

func (l listerAt) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(ls, l[offset:])
	if int(offset)+n >= len(l) {
		return n, io.EOF
	}
	return n, nil
}

// ssh FXF flags from draft-ietf-secsh-filexfer-02.
const (
	fxfRead   = 0x00000001
	fxfWrite  = 0x00000002
	fxfAppend = 0x00000004
	fxfCreat  = 0x00000008
	fxfTrunc  = 0x00000010
	fxfExcl   = 0x00000020
)

func osFlags(flags uint32) int {
	read := flags&fxfRead != 0
	write := flags&fxfWrite != 0
	var osf int
	switch {
	case read && write:
		osf = os.O_RDWR
	case write:
		osf = os.O_WRONLY
	default:
		osf = os.O_RDONLY
	}
	// Do not set O_APPEND: SFTP Write packets carry an offset, and
	// O_APPEND conflicts with WriteAt.
	if flags&fxfCreat != 0 {
		osf |= os.O_CREATE
	}
	if flags&fxfTrunc != 0 {
		osf |= os.O_TRUNC
	}
	if flags&fxfExcl != 0 {
		osf |= os.O_EXCL
	}
	return osf
}

// ServeSFTP serves one SFTP subsystem session jailed to root.
func ServeSFTP(channel ssh.Channel, root string, logf Logf) {
	ServeSFTPObserved(channel, root, logf, nil)
}

// ServeSFTPObserved is ServeSFTP with a hook on every request. Separate
// rather than a changed signature so the jail's own tests, and any caller
// that does not want the events, stay exactly as they were.
func ServeSFTPObserved(channel ssh.Channel, root string, logf Logf, obs Observer) {
	defer channel.Close()
	if logf == nil {
		logf = func(string, ...any) {}
	}
	j, err := newJail(root)
	if err != nil {
		logf("sshftp: jail: %v", err)
		return
	}
	j.obs = obs
	server := sftp.NewRequestServer(channel, j.handlers())
	if err := server.Serve(); err != nil && err != io.EOF {
		logf("sshftp: sftp session error: %v", err)
	}
	_ = server.Close()
}
