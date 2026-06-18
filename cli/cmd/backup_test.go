package cmd

import (
	"os"
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestParseBackupFormat(t *testing.T) {
	cases := []struct {
		in      string
		want    client.Format
		wantErr bool
	}{
		{"proto", client.FormatProto, false},
		{"", client.FormatProto, false},
		{"ndjson", client.FormatNDJSON, false},
		{"yaml", 0, true},
	}
	for _, c := range cases {
		got, err := parseBackupFormat(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseBackupFormat(%q): want error, got nil", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseBackupFormat(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseBackupFormat(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestOpenOutput_Stdout(t *testing.T) {
	for _, p := range []string{"-", ""} {
		w, err := openOutput(p)
		if err != nil {
			t.Fatalf("openOutput(%q): %v", p, err)
		}
		if _, ok := w.(nopWriteCloser); !ok {
			t.Errorf("openOutput(%q): want nopWriteCloser (stdout), got %T", p, w)
		}
		if err := w.Close(); err != nil {
			t.Errorf("Close stdout wrapper: %v", err)
		}
	}
}

func TestOpenOutput_File(t *testing.T) {
	path := t.TempDir() + "/dump.lbk"
	w, err := openOutput(path)
	if err != nil {
		t.Fatalf("openOutput(file): %v", err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("dump file not created: %v", err)
	}
}
