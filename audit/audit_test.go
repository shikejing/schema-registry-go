package audit

import (
	"path/filepath"
	"testing"
)

func TestFileLoggerAppendAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := NewFileLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Log(NewEvent("a1", "REGISTER", "s", 1, ResultAllowed, "ok")); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := NewFileLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if err := l2.Log(NewEvent("a2", "DELETE", "s", 1, ResultDenied, "no")); err != nil {
		t.Fatal(err)
	}
	events, err := l2.Query()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events across reopen, got %d: %+v", len(events), events)
	}
	if events[0].Actor != "a1" || events[1].Actor != "a2" {
		t.Fatalf("event order/content wrong: %+v", events)
	}
}
