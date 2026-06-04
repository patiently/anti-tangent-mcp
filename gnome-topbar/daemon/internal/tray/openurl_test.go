package tray

import "testing"

func TestOpenLocalRejectsNonLoopback(t *testing.T) {
	if err := OpenLocal("https://example.com"); err == nil {
		t.Error("expected refusal for non-loopback URL")
	}
	if err := OpenLocal("file:///etc/passwd"); err == nil {
		t.Error("expected refusal for file URL")
	}
}

func TestOpenLocalRejectsLookalikeHosts(t *testing.T) {
	for _, u := range []string{
		"http://localhost.evil.com/steal",
		"http://127.0.0.1.evil.com/steal",
		"http://evil.com/?x=http://127.0.0.1",
	} {
		if err := OpenLocal(u); err == nil {
			t.Errorf("expected refusal for lookalike host: %q", u)
		}
	}
}
