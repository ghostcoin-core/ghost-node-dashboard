package status

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLog(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, LogFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendLog(t *testing.T, dir, content string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, LogFile), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

const (
	lineMined    = "INFO [10-28|10:00:01.000] 🔨 mined potential block                  number=41 hash=0xabc"
	lineLost     = "INFO [10-28|10:00:02.000] 😱 block lost                             number=41 hash=0xabc"
	lineImported = "INFO [10-28|10:00:03.000] Imported new chain segment               number=42 hash=0xdef blocks=1"
	linePeers    = "INFO [10-28|10:00:04.000] Looking for peers                        peercount=0 tried=3 static=0"
	lineOther    = "DEBUG[10-28|10:00:05.000] Something else entirely"
)

func TestLogPanel(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 250; i++ {
		fmt.Fprintf(&b, "INFO [10-28|09:00:00.000] line %d\n", i)
	}
	b.WriteString(lineMined + "\n" + lineLost + "\n" + lineImported + "\n" + linePeers + "\n\x1b[32m" + lineOther + "\x1b[0m\r\n")
	writeLog(t, dir, b.String())

	p := ReadLog(dir)
	if !p.Available || p.Message != "" {
		t.Fatalf("available %v, message %q", p.Available, p.Message)
	}
	if len(p.Lines) != LogLines {
		t.Fatalf("%d lines, want %d", len(p.Lines), LogLines)
	}
	want := []LogLine{
		{Text: lineOther},
		{Text: linePeers, Meaning: "Your node is looking for other nodes to connect to."},
		{Text: lineImported, Meaning: "Your node received new blocks from the network and added them to its copy of the chain."},
		{Text: lineLost, Meaning: LogMeaning("block lost")},
		{Text: lineMined, Meaning: LogMeaning("mined potential block")},
		{Text: "INFO [10-28|09:00:00.000] line 250"},
	}
	for i, w := range want {
		if p.Lines[i] != w {
			t.Errorf("line %d = %+v, want %+v", i, p.Lines[i], w)
		}
	}
	if !strings.Contains(want[3].Meaning, "same height was accepted first") || !strings.Contains(want[4].Meaning, "found a block") {
		t.Errorf("meanings: %q, %q", want[3].Meaning, want[4].Meaning)
	}
	// Newest first: the oldest line shown is the 200th from the end.
	if last := p.Lines[LogLines-1].Text; last != "INFO [10-28|09:00:00.000] line 56" {
		t.Errorf("oldest line shown %q, want line 56", last)
	}
	if p.Path != filepath.Join(dir, LogFile) {
		t.Errorf("path %q", p.Path)
	}
}

func TestLogPanelNoLog(t *testing.T) {
	p := ReadLog(t.TempDir())
	if p.Available || p.Message == "" || p.Lines == nil || len(p.Lines) != 0 {
		t.Errorf("no log: %+v", p)
	}
}

func TestLogPanelRefusesOutsideNodeDir(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not the node's log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, LogFile)); err != nil {
		t.Skipf("cannot make a symbolic link: %v", err)
	}

	p := ReadLog(dir)
	if p.Available || len(p.Lines) != 0 {
		t.Fatalf("read a log outside the node folder: %+v", p)
	}
	if !strings.Contains(p.Message, ErrOutsideNodeDir.Error()) {
		t.Errorf("message %q, want it to say the log is outside the node folder", p.Message)
	}
	if _, err := tailLog(dir, 10); !errors.Is(err, ErrOutsideNodeDir) {
		t.Errorf("tailLog error %v, want ErrOutsideNodeDir", err)
	}
	var c lostRaces
	if _, err := c.count(dir); !errors.Is(err, ErrOutsideNodeDir) {
		t.Errorf("lost race count error %v, want ErrOutsideNodeDir", err)
	}

	// A link to another file inside the node folder is fine.
	inner := filepath.Join(dir, "logs", "current.log")
	if err := os.MkdirAll(filepath.Dir(inner), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inner, []byte(linePeers+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, LogFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inner, filepath.Join(dir, LogFile)); err != nil {
		t.Fatal(err)
	}
	if p := ReadLog(dir); !p.Available || len(p.Lines) != 1 || p.Lines[0].Text != linePeers {
		t.Errorf("link inside the node folder: %+v", p)
	}

	// A folder in place of the log is refused too.
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, LogFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if p := ReadLog(other); p.Available {
		t.Errorf("read a folder as the log: %+v", p)
	}
}

func TestLostRaceCount(t *testing.T) {
	dir := t.TempDir()
	var c lostRaces
	if _, err := c.count(dir); err == nil {
		t.Error("no error without a log")
	}

	writeLog(t, dir, lineMined+"\n"+lineLost+"\n"+lineImported+"\nWARN BLOCK LOST number=7\n"+lineLost+"\n")
	if n, err := c.count(dir); err != nil || n != 3 {
		t.Fatalf("count = %d, %v; want 3", n, err)
	}

	// New lines are added to the count; half a line waits for its end.
	appendLog(t, dir, linePeers+"\n"+lineLost[:30])
	if n, _ := c.count(dir); n != 3 {
		t.Errorf("count with half a line = %d, want 3", n)
	}
	appendLog(t, dir, lineLost[30:]+"\n")
	if n, _ := c.count(dir); n != 4 {
		t.Errorf("count after the line ends = %d, want 4", n)
	}
	if n, _ := c.count(dir); n != 4 {
		t.Errorf("count again = %d, want 4 (nothing new)", n)
	}

	// A log that starts over is counted from its start.
	writeLog(t, dir, lineLost+"\n")
	if n, _ := c.count(dir); n != 1 {
		t.Errorf("count after the log was cut = %d, want 1", n)
	}
}

func TestRewardsPanelLostRaces(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, lineLost+"\n"+lineLost+"\n"+lineMined+"\n")
	p := NewRewards(RewardsEnv{NodeDir: dir}).Fetch(t.Context(), downNode(t))
	if p.LostRaces == nil || *p.LostRaces != 2 {
		t.Errorf("lost races %v, want 2", p.LostRaces)
	}
	if p.LostRacesNote != LostRaceNote || !strings.Contains(p.LostRacesNote, "earns nothing") {
		t.Errorf("note %q", p.LostRacesNote)
	}
	if p := NewRewards(RewardsEnv{NodeDir: t.TempDir()}).Fetch(t.Context(), downNode(t)); p.LostRaces != nil {
		t.Errorf("lost races %v without a log, want null", *p.LostRaces)
	}
}
