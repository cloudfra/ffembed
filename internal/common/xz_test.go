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

package common

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholt/archives"
)

// compress returns data compressed with MaxXz.
func compress(tb testing.TB, data []byte) []byte {
	tb.Helper()

	var buf bytes.Buffer
	w, err := MaxXz{}.OpenWriter(&buf)
	if err != nil {
		tb.Fatalf("OpenWriter() failed, %s", err)
	}
	if _, err := w.Write(data); err != nil {
		tb.Fatalf("Write() failed, %s", err)
	}
	if err := w.Close(); err != nil {
		tb.Fatalf("Close() failed, %s", err)
	}
	return buf.Bytes()
}

func TestMaxXzRoundTrip(t *testing.T) {
	testCases := []struct {
		name string
		data string
	}{
		{name: "empty", data: ""},
		{name: "short", data: "ffmpeg binary"},
		{name: "repetitive", data: strings.Repeat("ffmpeg binary ", 10000)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := MaxXz{}.OpenReader(bytes.NewReader(compress(t, []byte(tc.data))))
			if err != nil {
				t.Fatalf("OpenReader() failed, %s", err)
			}
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("ReadAll() failed, %s", err)
			}
			if err := r.Close(); err != nil {
				t.Errorf("Close() failed, %s", err)
			}
			if string(got) != tc.data {
				t.Errorf("round trip returned %d bytes, want the %d bytes written", len(got), len(tc.data))
			}
		})
	}
}

func TestMaxXzCompresses(t *testing.T) {
	data := []byte(strings.Repeat("ffmpeg binary ", 10000))

	if got := compress(t, data); len(got) >= len(data)/10 {
		t.Errorf("compressed %d bytes to %d bytes, want less than a tenth", len(data), len(got))
	}
}

func TestMaxXzUsesMaxDictionary(t *testing.T) {
	got := compress(t, []byte("ffmpeg binary"))

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
	if len(got) < filterOffset+3 {
		t.Fatalf("output is %d bytes, too short to hold a block header", len(got))
	}
	if filter := got[filterOffset]; filter != lzma2Filter {
		t.Fatalf("filter = %#x, want LZMA2 (%#x)", filter, lzma2Filter)
	}
	if dict := got[filterOffset+2]; dict != dict64MiB {
		t.Errorf("encoded dictionary size = %d, want %d (64 MiB)", dict, dict64MiB)
	}
}

func TestTarXzExtension(t *testing.T) {
	if got := TarXz().Extension(); got != TarXzExtension {
		t.Errorf("TarXz().Extension() = %q, want %q", got, TarXzExtension)
	}
}

func TestTarXzRoundTrip(t *testing.T) {
	want := map[string]string{
		"ffmpeg":  "ffmpeg binary",
		"ffprobe": "ffprobe binary",
	}

	dir := t.TempDir()
	files := make([]archives.FileInfo, 0, len(want))
	for name, content := range want {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) failed, %s", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) failed, %s", path, err)
		}
		files = append(files, archives.FileInfo{
			FileInfo:      info,
			NameInArchive: name,
			Open: func() (fs.File, error) {
				return os.Open(filepath.Clean(path))
			},
		})
	}

	var buf bytes.Buffer
	if err := TarXz().Archive(t.Context(), &buf, files); err != nil {
		t.Fatalf("Archive() failed, %s", err)
	}

	got := map[string]string{}
	if err := TarXz().Extract(t.Context(), &buf, func(_ context.Context, f archives.FileInfo) error {
		r, err := f.Open()
		if err != nil {
			return err
		}
		content, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		got[f.NameInArchive] = string(content)
		return r.Close()
	}); err != nil {
		t.Fatalf("Extract() failed, %s", err)
	}

	if !maps.Equal(got, want) {
		t.Errorf("extracted %v, want %v", got, want)
	}
}
