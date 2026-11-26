package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// SSEEvent is one Server-Sent Event (text/event-stream).
type SSEEvent struct {
	ID    string  `json:"id,omitempty"`
	Event string  `json:"event,omitempty"` // empty means "message"
	Data  string  `json:"data"`
	Retry int     `json:"retry,omitempty"`
	AtMs  float64 `json:"atMs"` // since the request started
}

const maxEvents = 5000

func isEventStream(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

// readEvents reads an event stream until the server closes it, the send's
// timeout ends it, or the size or event limit is reached. It returns the raw
// stream, the events and why it stopped ("closed", "timeout", "limit") —
// a stream ending at the timeout isn't an error.
func readEvents(ctx context.Context, body io.Reader, start time.Time) (raw []byte, events []SSEEvent, ended string, err error) {
	var buf bytes.Buffer
	r := bufio.NewReaderSize(io.TeeReader(io.LimitReader(body, maxBody+1), &buf), 64<<10)
	var cur SSEEvent
	var data []string
	hasData := false
	dispatch := func() {
		if hasData {
			cur.Data = strings.Join(data, "\n")
			cur.AtMs = float64(time.Since(start).Microseconds()) / 1000
			events = append(events, cur)
		}
		cur, data, hasData = SSEEvent{ID: cur.ID}, nil, false // the last ID carries over
	}
	for {
		line, rerr := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == "" && rerr == nil {
			dispatch()
		} else if line != "" && !strings.HasPrefix(line, ":") {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "data":
				data, hasData = append(data, value), true
			case "event":
				cur.Event = value
			case "id":
				if !strings.Contains(value, "\x00") {
					cur.ID = value
				}
			case "retry":
				if n, err := strconv.Atoi(value); err == nil {
					cur.Retry = n
				}
			}
		}
		if len(events) >= maxEvents || buf.Len() > maxBody {
			return trim(buf.Bytes()), events, "limit", nil
		}
		if rerr != nil {
			dispatch()
			switch {
			case errors.Is(rerr, io.EOF):
				return trim(buf.Bytes()), events, "closed", nil
			case ctx.Err() != nil:
				return trim(buf.Bytes()), events, "timeout", nil
			}
			return trim(buf.Bytes()), events, "closed", rerr
		}
	}
}

func trim(b []byte) []byte {
	if len(b) > maxBody {
		return b[:maxBody]
	}
	return b
}
