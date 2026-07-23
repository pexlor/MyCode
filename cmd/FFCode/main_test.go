package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelpAndVersionDoNotStartREPL(t *testing.T) {
	for _, arguments := range [][]string{{"--help"}, {"-h"}, {"--version"}, {"-v"}} {
		var stdout, stderr bytes.Buffer
		started := false
		if code := run(arguments, &stdout, &stderr, func() { started = true }); code != 0 || started || stderr.Len() != 0 {
			t.Fatalf("run(%v) = code %d, started %t, stderr %q", arguments, code, started, stderr.String())
		}
		if stdout.Len() == 0 {
			t.Fatalf("run(%v) did not produce output", arguments)
		}
	}
}

func TestRunStartsREPLWithoutArguments(t *testing.T) {
	started := false
	if code := run(nil, &bytes.Buffer{}, &bytes.Buffer{}, func() { started = true }); code != 0 || !started {
		t.Fatalf("run() = code %d, started %t", code, started)
	}
}

func TestRunRejectsUnknownArguments(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"--unknown"}, &bytes.Buffer{}, &stderr, func() {}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown option") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
