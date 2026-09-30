package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
)

const titleTailBytes = 1 << 20

// LastTitle returns the last ai-title recorded in a transcript, reading only
// the final 1 MB (transcripts reach tens of MB). Empty if none found.
func LastTitle(transcriptPath string) string {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > titleTailBytes {
		f.Seek(st.Size()-titleTailBytes, io.SeekStart)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	title := ""
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.Contains(line, []byte(`"ai-title"`)) {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			AITitle string `json:"aiTitle"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Type == "ai-title" && rec.AITitle != "" {
			title = rec.AITitle
		}
	}
	return title
}
