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
	"strconv"
	"strings"
	"sync"
	"time"
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

// ListedLostRaces is how many of the latest lost races the rewards panel
// lists.
const ListedLostRaces = 100

// keptLostRaces is how many of the latest lost races are kept in memory, for
// the list and the block wall.
const keptLostRaces = 1000

// lostLinePrefix is how much of a "block lost" line is kept to read its
// block number and time.
const lostLinePrefix = 4 << 10

// LostRace is one "block lost" line of the node log.
type LostRace struct {
	// Number is the height the node lost.
	Number uint64 `json:"number"`
	// Time is the unix time of the line's stamp; null when it cannot be
	// read.
	Time *int64 `json:"time"`
}

// lostRaces counts the "block lost" lines in the node log and keeps the
// latest ones that name their block. It remembers how far it read, so each
// count only reads what the node wrote since; a log that was replaced or cut
// shorter is counted again from the start. The zero value reads stamps in
// the computer's local time.
type lostRaces struct {
	mu     sync.Mutex
	file   fs.FileInfo
	offset int64
	n      int
	// races are the lost races read, oldest first, at most keptLostRaces.
	races []LostRace

	// now and location date the log stamps; nil means time.Now and
	// time.Local.
	now      func() time.Time
	location *time.Location
}

var lostMarker = []byte("block lost")

func (c *lostRaces) count(nodeDir string) (int, error) {
	n, _, err := c.read(nodeDir)
	return n, err
}

// heights is the set of block numbers with a lost race. It reads what the
// node wrote since the last read; with no log to read it is empty.
func (c *lostRaces) heights(nodeDir string) map[uint64]bool {
	_, races, err := c.read(nodeDir)
	if err != nil {
		return nil
	}
	set := make(map[uint64]bool, len(races))
	for _, r := range races {
		set[r.Number] = true
	}
	return set
}

// read brings the count up to date and returns it with a copy of the kept
// lost races, oldest first.
func (c *lostRaces) read(nodeDir string) (int, []LostRace, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	f, fi, err := openNodeLog(nodeDir)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	if c.file == nil || !os.SameFile(c.file, fi) || fi.Size() < c.offset {
		c.offset, c.n, c.races = 0, 0, nil
	}
	c.file = fi
	if _, err := f.Seek(c.offset, io.SeekStart); err != nil {
		return 0, nil, err
	}

	r := bufio.NewReaderSize(f, 64<<10)
	var lineLen int64
	var line []byte
	hit := false
	for {
		chunk, err := r.ReadSlice('\n')
		lineLen += int64(len(chunk))
		if room := lostLinePrefix - len(line); room > 0 {
			line = append(line, chunk[:min(room, len(chunk))]...)
		}
		if bytes.Contains(bytes.ToLower(chunk), lostMarker) {
			hit = true
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			// A line without its newline yet is counted once it is whole.
			if errors.Is(err, io.EOF) {
				return c.n, append([]LostRace(nil), c.races...), nil
			}
			return 0, nil, err
		}
		if hit {
			c.n++
			if race, ok := c.parse(line); ok {
				c.races = append(c.races, race)
				if extra := len(c.races) - keptLostRaces; extra > 0 {
					c.races = append([]LostRace(nil), c.races[extra:]...)
				}
			}
		}
		c.offset += lineLen
		lineLen, hit, line = 0, false, line[:0]
	}
}

var (
	lostNumber = regexp.MustCompile(`(?:^|\s)number=([0-9][0-9,]*)(?:\s|$)`)
	logStamp   = regexp.MustCompile(`\[(\d\d)-(\d\d)\|(\d\d):(\d\d):(\d\d)(?:\.(\d{1,9}))?\]`)
)

// parse reads the block number and the time of a "block lost" line. It is
// false when the line names no block number. The node writes big numbers
// with thousands separators (number=1,234,567).
func (c *lostRaces) parse(raw []byte) (LostRace, bool) {
	line := colour.ReplaceAllString(strings.TrimRight(string(raw), "\r\n"), "")
	m := lostNumber.FindStringSubmatch(line)
	if m == nil {
		return LostRace{}, false
	}
	n, err := strconv.ParseUint(strings.ReplaceAll(m[1], ",", ""), 10, 64)
	if err != nil {
		return LostRace{}, false
	}
	race := LostRace{Number: n}
	if t, ok := c.stamp(line); ok {
		unix := t.Unix()
		race.Time = &unix
	}
	return race, true
}

// stamp reads a log stamp like [10-28|10:00:02.000]. It has no year: it is
// the current year, or the previous one when that would put the stamp more
// than a day in the future.
func (c *lostRaces) stamp(line string) (time.Time, bool) {
	m := logStamp.FindStringSubmatch(line)
	if m == nil {
		return time.Time{}, false
	}
	var v [5]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	month, day, hour, minute, second := v[0], v[1], v[2], v[3], v[4]
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false
	}
	nanos := 0
	if m[6] != "" {
		nanos, _ = strconv.Atoi(m[6] + strings.Repeat("0", 9-len(m[6])))
	}
	now, loc := time.Now, c.location
	if c.now != nil {
		now = c.now
	}
	if loc == nil {
		loc = time.Local
	}
	at := now().In(loc)
	build := func(year int) (time.Time, bool) {
		t := time.Date(year, time.Month(month), day, hour, minute, second, nanos, loc)
		// time.Date moves 31 April to 1 May: such a stamp is not a date.
		return t, t.Month() == time.Month(month) && t.Day() == day
	}
	t, ok := build(at.Year())
	if t.After(at.Add(24 * time.Hour)) {
		t, ok = build(at.Year() - 1)
	}
	return t, ok
}
