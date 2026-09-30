package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinistoisr/timewarp/internal/db"
)

type transcriptLine struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	AITitle   string `json:"aiTitle"`
	IsMeta    bool   `json:"isMeta"`
	Message   *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Input json.RawMessage `json:"input"`
}

func blocks(raw json.RawMessage) (text string, list []contentBlock) {
	if len(raw) == 0 {
		return "", nil
	}
	if raw[0] == '"' {
		json.Unmarshal(raw, &text)
		return text, nil
	}
	json.Unmarshal(raw, &list)
	var parts []string
	for _, b := range list {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), list
}

// EventsFromTranscript rebuilds the events the hook would have written from a
// Claude Code transcript: prompts, tool calls, and a stop at the last
// timestamped line of each turn. Titles are the latest ai-title seen, and the
// session's final title fills events that came before the first one.
func EventsFromTranscript(r io.Reader, host, user string) ([]db.ClaudeEvent, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var evs []db.ClaudeEvent
	title := ""
	var lastTS time.Time
	lastSession := ""
	inTurn := false

	stop := func() {
		if inTurn && !lastTS.IsZero() {
			evs = append(evs, eventsFor(nil, db.ClaudeEvent{TS: lastTS, Hostname: host, Username: user,
				SessionID: lastSession, SessionTitle: title, Event: "stop"})...)
		}
		inTurn = false
	}

	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var l transcriptLine
			if json.Unmarshal(line, &l) == nil {
				if l.Type == "ai-title" && l.AITitle != "" {
					title = l.AITitle
				} else if ts, perr := time.Parse(time.RFC3339Nano, l.Timestamp); perr == nil && l.SessionID != "" {
					base := db.ClaudeEvent{TS: ts.UTC(), Hostname: host, Username: user,
						SessionID: l.SessionID, SessionTitle: title}
					var content json.RawMessage
					if l.Message != nil {
						content = l.Message.Content
					}
					text, list := blocks(content)
					switch l.Type {
					case "user":
						if text != "" && !l.IsMeta {
							stop()
							base.Event = "prompt"
							evs = append(evs, eventsFor(ExtractRefs(text, nil, l.Cwd), base)...)
							inTurn = true
						}
					case "assistant":
						for _, b := range list {
							if b.Type == "tool_use" {
								base.Event = "tool"
								evs = append(evs, eventsFor(ExtractRefs("", b.Input, l.Cwd), base)...)
							}
						}
					}
					lastTS, lastSession = ts.UTC(), l.SessionID
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
	}
	stop()
	for i := range evs {
		if evs[i].SessionTitle == "" {
			evs[i].SessionTitle = title
		}
	}
	return evs, nil
}

// Backfill imports every *.jsonl transcript under root into the DB in dbpath.
func Backfill(root, dbpath string) (files, inserted int, err error) {
	t, err := db.Open(dbpath)
	if err != nil {
		return 0, 0, err
	}
	defer t.Close()
	host, _ := os.Hostname()
	user := os.Getenv("USERNAME")
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		evs, err := EventsFromTranscript(f, host, user)
		f.Close()
		if err != nil {
			return nil
		}
		n, err := t.InsertClaudeEvents(evs)
		if err != nil {
			return err
		}
		files++
		inserted += n
		return nil
	})
	return files, inserted, walkErr
}
