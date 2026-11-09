package sandbox

import (
	"context"
	"errors"
	"fmt"
	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBufferedSDKResponsesAreBounded(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%v", chunked), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/endpoints/") {
					fmt.Fprint(w, `{"endpoint":"ignored"}`)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/logs") {
					t.Errorf("unexpected path %s", r.URL.Path)
					return
				}
				if chunked {
					w.(http.Flusher).Flush()
				} else {
					w.Header().Set("Content-Length", "1048577")
				}
				block := strings.Repeat("x", 16<<10)
				for i := 0; i < 65; i++ {
					if _, err := io.WriteString(w, block); err != nil {
						return
					}
				}
			}))
			defer up.Close()
			c := New(up.URL, "")
			ex, err := c.Exec(context.Background(), "sandbox")
			if err != nil {
				t.Fatal(err)
			}
			_, err = ex.GetCommandLogs(context.Background(), "command", nil)
			if !errors.Is(err, ErrResponseTooLarge) {
				t.Fatalf("unbounded SDK logs: %v", err)
			}
		})
	}
}

func TestCommandLogCursorAndExactLimit(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/endpoints/") {
			fmt.Fprint(w, `{"endpoint":"ignored"}`)
			return
		}
		if r.URL.Query().Get("cursor") != "25" {
			t.Error("log cursor not forwarded")
		}
		w.Header().Set("EXECD-COMMANDS-TAIL-CURSOR", "1048601")
		io.WriteString(w, strings.Repeat("x", 1<<20))
	}))
	defer up.Close()
	c := New(up.URL, "")
	ex, err := c.Exec(context.Background(), "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	cursor := int64(25)
	logs, err := ex.GetCommandLogs(context.Background(), "command", &cursor)
	if err != nil || logs == nil || len(logs.Output) != 1<<20 || logs.Cursor != 1048601 {
		t.Fatalf("exact bound/incremental logs: %v %v", logs, err)
	}
}

func TestSSESingleUnterminatedLineIsBoundedBeforeCallback(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/endpoints/") {
			fmt.Fprint(w, `{"endpoint":"ignored"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		io.WriteString(w, "data: ")
		block := strings.Repeat("x", 16<<10)
		for i := 0; i < 600; i++ {
			if _, err := io.WriteString(w, block); err != nil {
				return
			}
		}
	}))
	defer up.Close()
	c := New(up.URL, "")
	ex, err := c.Exec(context.Background(), "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	err = ex.RunCommand(context.Background(), osb.RunCommandRequest{Command: "large-single-line"}, func(osb.StreamEvent) error { return nil })
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("SSE parser read beyond platform limit: %v", err)
	}
}
