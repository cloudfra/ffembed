// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ffbundle

import (
	"archive/tar"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ffembed/internal/common"
	"github.com/mholt/archives"
)

// validArgs returns arguments that pass validation.
func validArgs(tb testing.TB, input ...string) Args {
	tb.Helper()

	return Args{
		Architecture:    "amd64",
		OperatingSystem: "linux",
		Input:           input,
		Output:          filepath.Join(tb.TempDir(), "bundle.tar.xz"),
	}
}

// writeFiles creates a directory holding files, keyed by slash separated path.
func writeFiles(tb testing.TB, files map[string]string) string {
	tb.Helper()

	dir := tb.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			tb.Fatalf("MkdirAll(%q) failed, %s", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			tb.Fatalf("WriteFile(%q) failed, %s", path, err)
		}
	}
	return dir
}

// readBundle returns the content of every file in the bundle keyed by name.
func readBundle(tb testing.TB, name string) map[string]string {
	tb.Helper()

	f, err := os.Open(filepath.Clean(name))
	if err != nil {
		tb.Fatalf("Open(%q) failed, %s", name, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			tb.Errorf("Close(%q) failed, %s", name, err)
		}
	}()

	xz, err := archives.Xz{}.OpenReader(f)
	if err != nil {
		tb.Fatalf("opening xz stream of %q failed, %s", name, err)
	}
	defer func() {
		if err := xz.Close(); err != nil {
			tb.Errorf("closing xz stream of %q failed, %s", name, err)
		}
	}()

	got := map[string]string{}
	tr := tar.NewReader(xz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			tb.Fatalf("reading %q failed, %s", name, err)
		}
		if _, ok := got[hdr.Name]; ok {
			tb.Errorf("%q contains %q more than once", name, hdr.Name)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			tb.Fatalf("reading %q from %q failed, %s", hdr.Name, name, err)
		}
		got[hdr.Name] = string(content)
	}
	return got
}

func TestRun(t *testing.T) {
	testCases := []struct {
		name  string
		input []map[string]string
		want  map[string]string
	}{
		{
			name: "linux package",
			input: []map[string]string{{
				"ffmpeg":     "ffmpeg binary",
				"ffprobe":    "ffprobe binary",
				"LICENSE":    "license text",
				"readme.txt": "readme",
				"ffplay":     "ffplay binary",
			}},
			want: map[string]string{
				"ffmpeg":  "ffmpeg binary",
				"ffprobe": "ffprobe binary",
				"license": "license text",
			},
		},
		{
			name: "windows package",
			input: []map[string]string{{
				"ffmpeg.exe":  "ffmpeg binary",
				"ffprobe.exe": "ffprobe binary",
				"LICENSE.txt": "license text",
				"ffplay.exe":  "ffplay binary",
			}},
			want: map[string]string{
				"ffmpeg.exe":  "ffmpeg binary",
				"ffprobe.exe": "ffprobe binary",
				"license.txt": "license text",
			},
		},
		{
			name: "nested files are flattened",
			input: []map[string]string{{
				"ffmpeg-7.1/bin/ffmpeg":      "ffmpeg binary",
				"ffmpeg-7.1/bin/ffprobe":     "ffprobe binary",
				"ffmpeg-7.1/LICENSE.md":      "license text",
				"ffmpeg-7.1/doc/ffmpeg.html": "docs",
			}},
			want: map[string]string{
				"ffmpeg":     "ffmpeg binary",
				"ffprobe":    "ffprobe binary",
				"license.md": "license text",
			},
		},
		{
			name: "names match ignoring case",
			input: []map[string]string{{
				"FFmpeg.EXE":  "ffmpeg binary",
				"FFPROBE.exe": "ffprobe binary",
			}},
			want: map[string]string{
				"ffmpeg.exe":  "ffmpeg binary",
				"ffprobe.exe": "ffprobe binary",
			},
		},
		{
			name: "multiple inputs are merged",
			input: []map[string]string{
				{"ffmpeg": "ffmpeg binary", "notes": "notes"},
				{"ffprobe": "ffprobe binary"},
				{"docs/LICENSE": "license text"},
			},
			want: map[string]string{
				"ffmpeg":  "ffmpeg binary",
				"ffprobe": "ffprobe binary",
				"license": "license text",
			},
		},
		{
			name:  "no matching files",
			input: []map[string]string{{"readme.txt": "readme"}},
			want:  map[string]string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			inputs := make([]string, 0, len(tc.input))
			for _, files := range tc.input {
				inputs = append(inputs, writeFiles(t, files))
			}
			args := validArgs(t, inputs...)

			if err := Run(args); err != nil {
				t.Fatalf("Run() failed, %s", err)
			}

			if got := readBundle(t, args.Output); !maps.Equal(got, tc.want) {
				t.Errorf("bundle = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunArchiveInput(t *testing.T) {
	// Bundle a directory, then use the resulting archive as the input.
	first := validArgs(t, writeFiles(t, map[string]string{
		"bin/ffmpeg":  "ffmpeg binary",
		"bin/ffprobe": "ffprobe binary",
		"readme.txt":  "readme",
	}))
	if err := Run(first); err != nil {
		t.Fatalf("Run() failed, %s", err)
	}

	second := validArgs(t, first.Output)
	if err := Run(second); err != nil {
		t.Fatalf("Run() with archive input failed, %s", err)
	}

	want := map[string]string{
		"ffmpeg":  "ffmpeg binary",
		"ffprobe": "ffprobe binary",
	}
	if got := readBundle(t, second.Output); !maps.Equal(got, want) {
		t.Errorf("bundle = %v, want %v", got, want)
	}
}

func TestRunInvalidArgs(t *testing.T) {
	err := Run(Args{})

	var argErr *common.ArgError
	if !errors.As(err, &argErr) {
		t.Fatalf("Run() = %v, want *common.ArgError", err)
	}
}

func TestRunInvalidArgsDoesNotCreateOutput(t *testing.T) {
	args := validArgs(t)

	if err := Run(args); err == nil {
		t.Fatal("Run() succeeded, want error")
	}
	if _, err := os.Stat(args.Output); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat(%q) = %v, want %v", args.Output, err, os.ErrNotExist)
	}
}

// assertOnlyFile fails the test unless dir holds exactly one entry called name.
func assertOnlyFile(tb testing.TB, dir string, name string) {
	tb.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		tb.Fatalf("ReadDir(%q) failed, %s", dir, err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		tb.Errorf("ReadDir(%q) = %v, want only %q", dir, entries, name)
	}
}

func TestRunWritesOnlyOutput(t *testing.T) {
	args := validArgs(t, writeFiles(t, map[string]string{"ffmpeg": "ffmpeg binary"}))

	if err := Run(args); err != nil {
		t.Fatalf("Run() failed, %s", err)
	}

	assertOnlyFile(t, filepath.Dir(args.Output), filepath.Base(args.Output))
}

func TestRunUsesMaxDictionary(t *testing.T) {
	args := validArgs(t, writeFiles(t, map[string]string{"ffmpeg": "ffmpeg binary"}))

	if err := Run(args); err != nil {
		t.Fatalf("Run() failed, %s", err)
	}

	bundle, err := os.ReadFile(args.Output)
	if err != nil {
		t.Fatalf("ReadFile(%q) failed, %s", args.Output, err)
	}

	// The first block header follows the 12 byte stream header and holds the
	// block header size, block flags, and then the LZMA2 filter: its ID, the
	// size of its properties, and the encoded dictionary size.
	// See https://tukaani.org/xz/xz-file-format.txt sections 3.1 and 5.3.1.
	const (
		filterOffset = 14
		lzma2Filter  = 0x21
		// dict64MiB is the encoding of the 64 MiB dictionary used by xz -9.
		dict64MiB = 28
	)
	if len(bundle) < filterOffset+3 {
		t.Fatalf("bundle is %d bytes, too short to hold a block header", len(bundle))
	}
	if got := bundle[filterOffset]; got != lzma2Filter {
		t.Fatalf("filter = %#x, want LZMA2 (%#x)", got, lzma2Filter)
	}
	if got := bundle[filterOffset+2]; got != dict64MiB {
		t.Errorf("encoded dictionary size = %d, want %d (64 MiB)", got, dict64MiB)
	}
}

func TestRunMissingInput(t *testing.T) {
	args := validArgs(t, filepath.Join(t.TempDir(), "does-not-exist"))

	if err := Run(args); err == nil {
		t.Error("Run() succeeded, want error")
	}
}

func TestRunFailureLeavesNoOutput(t *testing.T) {
	args := validArgs(t,
		writeFiles(t, map[string]string{"ffmpeg": "ffmpeg binary"}),
		filepath.Join(t.TempDir(), "does-not-exist"),
	)

	if err := Run(args); err == nil {
		t.Fatal("Run() succeeded, want error")
	}

	dir := filepath.Dir(args.Output)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) failed, %s", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(%q) = %v, want no files", dir, entries)
	}
}

func TestRunFailureKeepsExistingOutput(t *testing.T) {
	args := validArgs(t, filepath.Join(t.TempDir(), "does-not-exist"))
	if err := os.WriteFile(args.Output, []byte("previous bundle"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) failed, %s", args.Output, err)
	}

	if err := Run(args); err == nil {
		t.Fatal("Run() succeeded, want error")
	}

	got, err := os.ReadFile(args.Output)
	if err != nil {
		t.Fatalf("ReadFile(%q) failed, %s", args.Output, err)
	}
	if string(got) != "previous bundle" {
		t.Errorf("output = %q, want %q", got, "previous bundle")
	}
	assertOnlyFile(t, filepath.Dir(args.Output), filepath.Base(args.Output))
}

func TestRunReplacesExistingOutput(t *testing.T) {
	args := validArgs(t, writeFiles(t, map[string]string{"ffmpeg": "ffmpeg binary"}))
	if err := os.WriteFile(args.Output, []byte("previous bundle"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) failed, %s", args.Output, err)
	}

	if err := Run(args); err != nil {
		t.Fatalf("Run() failed, %s", err)
	}

	want := map[string]string{"ffmpeg": "ffmpeg binary"}
	if got := readBundle(t, args.Output); !maps.Equal(got, want) {
		t.Errorf("bundle = %v, want %v", got, want)
	}
}

func TestRunMalformedInput(t *testing.T) {
	args := validArgs(t, "http://[::1")

	if err := Run(args); err == nil {
		t.Error("Run() succeeded, want error")
	}
}

func TestRunOutputNotWritable(t *testing.T) {
	args := validArgs(t, writeFiles(t, map[string]string{"ffmpeg": "ffmpeg binary"}))
	args.Output = filepath.Join(t.TempDir(), "missing", "bundle.tar.xz")

	if err := Run(args); err == nil {
		t.Error("Run() succeeded, want error")
	}
}

func TestValidateArgs(t *testing.T) {
	valid := Args{
		Architecture:    "amd64",
		OperatingSystem: "linux",
		Input:           []string{"ffmpeg.tar.xz"},
		Output:          "bundle.tar.xz",
	}

	testCases := []struct {
		name string
		edit func(*Args)
		// wantArg is the argument reported as invalid, empty when args are valid.
		wantArg string
	}{
		{name: "valid", edit: func(*Args) {}},
		{name: "hash is optional", edit: func(a *Args) { a.Hash = "sha256:abc123" }},
		{name: "missing architecture", edit: func(a *Args) { a.Architecture = "" }, wantArg: "arch"},
		{name: "missing operating system", edit: func(a *Args) { a.OperatingSystem = "" }, wantArg: "os"},
		{name: "nil input", edit: func(a *Args) { a.Input = nil }, wantArg: "url"},
		{name: "empty input", edit: func(a *Args) { a.Input = []string{} }, wantArg: "url"},
		{name: "missing output", edit: func(a *Args) { a.Output = "" }, wantArg: "output"},
		{name: "output in directory", edit: func(a *Args) { a.Output = "out/dir/bundle.tar.xz" }},
		{name: "output extension ignores case", edit: func(a *Args) { a.Output = "BUNDLE.TAR.XZ" }},
		{name: "output is tar.gz", edit: func(a *Args) { a.Output = "bundle.tar.gz" }, wantArg: "output"},
		{name: "output is xz without tar", edit: func(a *Args) { a.Output = "bundle.xz" }, wantArg: "output"},
		{name: "output is tar without xz", edit: func(a *Args) { a.Output = "bundle.tar" }, wantArg: "output"},
		{name: "output has no extension", edit: func(a *Args) { a.Output = "bundle" }, wantArg: "output"},
		{name: "output is only the extension", edit: func(a *Args) { a.Output = "out/.tar.xz" }, wantArg: "output"},
		{name: "output extension is not last", edit: func(a *Args) { a.Output = "bundle.tar.xz.bak" }, wantArg: "output"},
		{name: "reports first missing argument", edit: func(a *Args) { *a = Args{} }, wantArg: "arch"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			args := valid
			tc.edit(&args)

			err := validateArgs(args)
			if tc.wantArg == "" {
				if err != nil {
					t.Errorf("validateArgs() failed, %s", err)
				}
				return
			}

			var argErr *common.ArgError
			if !errors.As(err, &argErr) {
				t.Fatalf("validateArgs() = %v, want *common.ArgError", err)
			}
			if argErr.Arg != tc.wantArg {
				t.Errorf("validateArgs() reported %q, want %q", argErr.Arg, tc.wantArg)
			}
		})
	}
}

func BenchmarkRun(b *testing.B) {
	args := validArgs(b, writeFiles(b, map[string]string{
		"ffmpeg":  "ffmpeg binary",
		"ffprobe": "ffprobe binary",
		"LICENSE": "license text",
	}))

	for b.Loop() {
		if err := Run(args); err != nil {
			b.Errorf("Run() failed, %s", err)
		}
	}
}
