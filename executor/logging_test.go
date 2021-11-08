// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package executor

import (
	"bytes"
	"log"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBindLoggingPipe_ErrorsWithLargeToken(t *testing.T) {
	input := `Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.`

	reader := strings.NewReader(input)

	logs := bytes.Buffer{}

	log.SetOutput(&logs)
	defer func() {
		log.SetOutput(os.Stderr)
	}()

	out := bytes.Buffer{}

	maxBufferBytes := 32
	addPrefix := false
	bindLoggingPipe("TestFunc", reader, &out, addPrefix, maxBufferBytes)

	// give the pipe time to actually parse the logs
	time.Sleep(500 * time.Millisecond)

	got := out.String()
	want := ""
	if want != got {
		t.Fatalf("expected empty string due to error, but got %q", got)
	}

	wantSt := `bufio.Scanner: token too long`
	if !strings.Contains(logs.String(), wantSt) {
		t.Fatalf("want text: %q, but not found in: %q", wantSt, logs.String())
	}
}

func TestBindLoggingPipe_ReadsValidSize(t *testing.T) {
	input := `Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.
`
	validSize := len(input)
	reader := strings.NewReader(input)

	logs := bytes.Buffer{}

	log.SetOutput(&logs)
	defer func() {
		log.SetOutput(os.Stderr)
	}()

	out := bytes.Buffer{}

	maxBufferBytes := validSize
	addPrefix := false
	bindLoggingPipe("TestFunc", reader, &out, addPrefix, maxBufferBytes)

	// give the pipe time to actually parse the logs
	time.Sleep(500 * time.Millisecond)

	got := out.String()
	want := input
	if want != got {
		t.Fatalf("want output %q, but got %q", want, got)
	}

	wantSt := `bufio.Scanner: token too long`
	if strings.Contains(logs.String(), wantSt) {
		t.Fatalf("Found error %s in output: %q", wantSt, logs.String())
	}
}

func TestBindLoggingPipe_ReadsValidSizedLines(t *testing.T) {
	input1 := `Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.
`
	input2 := `Sed ut perspiciatis unde omnis iste natus error sit voluptatem accusantium doloremque laudantium, totam rem aperiam, eaque ipsa quae ab illo inventore veritatis et quasi architecto beatae vitae dicta sunt explicabo.
`
	validSize := int(math.Max(float64(len(input1)), float64(len(input2))))

	reader := strings.NewReader(input1 + input2)

	logs := bytes.Buffer{}

	log.SetOutput(&logs)
	defer func() {
		log.SetOutput(os.Stderr)
	}()

	out := bytes.Buffer{}

	maxBufferBytes := validSize
	addPrefix := false
	bindLoggingPipe("TestFunc", reader, &out, addPrefix, maxBufferBytes)

	// give the pipe time to actually parse the logs
	time.Sleep(500 * time.Millisecond)

	got := out.String()
	want := input1 + input2
	if want != got {
		t.Fatalf("want output %q, but got %q", want, got)
	}

	t.Logf(out.String())
	wantSt := `bufio.Scanner: token too long`
	if strings.Contains(logs.String(), wantSt) {
		t.Fatalf("Found error %s in output: %q", wantSt, logs.String())
	}
}
