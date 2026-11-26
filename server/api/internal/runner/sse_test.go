package runner

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerSentEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		f := w.(http.Flusher)
		fmt.Fprint(w, ": comment, ignored\nretry: 1500\n\n")
		fmt.Fprint(w, "id: 1\ndata: {\"delta\":\"Hel\"}\n\n")
		f.Flush()
		fmt.Fprint(w, "event: token\ndata: line one\r\ndata: line two\r\n\r\n")
		f.Flush()
		if r.URL.Query().Get("open") == "" {
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		<-r.Context().Done() // never closes on its own
	}))
	defer srv.Close()

	res := send(t, Spec{Method: "GET", URL: srv.URL}, nil)
	if res.Error != "" || res.StreamEnded != "closed" || len(res.Events) != 3 {
		t.Fatalf("err %q ended %q events %+v", res.Error, res.StreamEnded, res.Events)
	}
	e := res.Events
	if e[0].ID != "1" || e[0].Data != `{"delta":"Hel"}` || e[1].Event != "token" || e[1].Data != "line one\nline two" || e[1].ID != "1" || e[2].Data != "[DONE]" {
		t.Fatalf("events %+v", e)
	}

	// A stream that stays open ends at the timeout, with its events and no error.
	start := time.Now()
	res = send(t, Spec{Method: "GET", URL: srv.URL + "?open=1", Settings: Settings{TimeoutMs: 400}}, nil)
	if res.Error != "" || res.StreamEnded != "timeout" || len(res.Events) != 2 || res.Status != 200 {
		t.Fatalf("err %q ended %q events %d status %d", res.Error, res.StreamEnded, len(res.Events), res.Status)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("the timeout should end the stream")
	}
}
