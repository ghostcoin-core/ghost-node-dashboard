package status

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// LogFile is the node's log inside the node directory.
const LogFile = "node.log"

// LogLines is how many of the last log lines the log panel shows.
const LogLines = 200

// tailBytes is how much of the end of the log is read for the panel.
const tailBytes = 1 << 20

// maxLogLine is the longest line the panel shows; longer ones are cut.
const maxLogLine = 2000

// ErrOutsideNodeDir is returned when node.log leads out of the node folder,
// for example through a symbolic link.
var ErrOutsideNodeDir = errors.New("node.log is outside the node folder")

// LogPanel is what /api/log returns.
type LogPanel struct {
	// Available is false when there is no log to read; Message says why.
	Available bool   `json:"available"`
	Message   string `json:"message,omitempty"`
	Path      string `json:"path"`
	// Lines are the last LogLines lines, newest first.
	Lines []LogLine `json:"lines"`
}

// LogLine is one line of the log with its plain meaning, when it is a
// common one.
type LogLine struct {
	Text    string `json:"text"`
	Meaning string `json:"meaning,omitempty"`
}

var logMeanings = []struct{ match, meaning string }{
	{"block lost", "Another node's block at the same height was accepted first. This is normal and earns nothing."},
	{"mined potential block", "Your node found a block and offered it to the network. It earns a reward if the network accepts it."},
	{"successfully sealed new block", "Your node finished a block of its own."},
	{"block reached canonical chain", "A block your node found is now part of the chain."},
	{"imported new chain segment", "Your node received new blocks from the network and added them to its copy of the chain."},
	{"looking for peers", "Your node is looking for other nodes to connect to."},
	{"commit new sealing work", "Your node started working on the next block."},
}

// LogMeaning is the plain meaning of a common log line, or "".
func LogMeaning(line string) string {
	lower := strings.ToLower(line)
	for _, m := range logMeanings {
		if strings.Contains(lower, m.match) {
			return m.meaning
		}
	}
	return ""
}

var colour = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// openNodeLog opens <nodeDir>/node.log for reading. It refuses a log that
// leads outside the node folder and anything that is not a plain file.
func openNodeLog(nodeDir string) (*os.File, fs.FileInfo, error) {
	if nodeDir == "" {
		return nil, nil, errors.New("no node folder")
	}
	dir, err := filepath.EvalSymlinks(nodeDir)
	if err != nil {
		return nil, nil, err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(dir, LogFile))
	if err != nil {
		return nil, nil, err
	}
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, nil, ErrOutsideNodeDir
	}

	// Open the resolved file through the folder itself too, so a link
	// swapped in after the check above cannot lead out of it either.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	fi, err := root.Stat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
		return nil, nil, ErrOutsideNodeDir
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is not a plain file", LogFile)
	}
	f, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
		return nil, nil, ErrOutsideNodeDir
	}
	if fi, err = f.Stat(); err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%s is not a plain file", LogFile)
	}
	return f, fi, nil
}

// ReadLog reads the log panel from <nodeDir>/node.log.
func ReadLog(nodeDir string) LogPanel {
	p := LogPanel{Path: filepath.Join(nodeDir, LogFile), Lines: []LogLine{}}
	lines, err := tailLog(nodeDir, LogLines)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		p.Message = "The node has not written a log yet."
		return p
	case err != nil:
		p.Message = "Cannot read the node log: " + err.Error()
		return p
	}
	p.Available = true
	for i := len(lines) - 1; i >= 0; i-- {
		p.Lines = append(p.Lines, LogLine{Text: lines[i], Meaning: LogMeaning(lines[i])})
	}
	return p
}

// tailLog returns the last n lines of the node log, oldest first.
func tailLog(nodeDir string, n int) ([]string, error) {
	f, fi, err := openNodeLog(nodeDir)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	start := max(fi.Size()-tailBytes, 0)
	buf := make([]byte, fi.Size()-start)
	got, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:got]
	if start > 0 {
		// The first line is cut by where we started reading.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil
		}
	}

	raw := strings.Split(strings.TrimRight(string(buf), "\r\n"), "\n")
	if len(raw) == 1 && raw[0] == "" {
		return nil, nil
	}
	if len(raw) > n {
		raw = raw[len(raw)-n:]
	}
	lines := make([]string, len(raw))
	for i, line := range raw {
		line = colour.ReplaceAllString(strings.TrimRight(line, "\r"), "")
		if len(line) > maxLogLine {
			line = line[:maxLogLine] + "…"
		}
		lines[i] = strings.ToValidUTF8(line, "�")
	}
	return lines, nil
}

// lostRaces counts the "block lost" lines in the node log. It remembers how
// far it read, so each count only reads what the node wrote since; a log
// that was replaced or cut shorter is counted again from the start.
type lostRaces struct {
	mu     sync.Mutex
	file   fs.FileInfo
	offset int64
	n      int
}

var lostMarker = []byte("block lost")

func (c *lostRaces) count(nodeDir string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	f, fi, err := openNodeLog(nodeDir)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if c.file == nil || !os.SameFile(c.file, fi) || fi.Size() < c.offset {
		c.offset, c.n = 0, 0
	}
	c.file = fi
	if _, err := f.Seek(c.offset, io.SeekStart); err != nil {
		return 0, err
	}

	r := bufio.NewReaderSize(f, 64<<10)
	var lineLen int64
	hit := false
	for {
		chunk, err := r.ReadSlice('\n')
		lineLen += int64(len(chunk))
		if bytes.Contains(bytes.ToLower(chunk), lostMarker) {
			hit = true
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			// A line without its newline yet is counted once it is whole.
			if errors.Is(err, io.EOF) {
				return c.n, nil
			}
			return 0, err
		}
		if hit {
			c.n++
		}
		c.offset += lineLen
		lineLen, hit = 0, false
	}
}
