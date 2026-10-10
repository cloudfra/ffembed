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
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// archiveFile returns an ArchiveFile called name backed by a new file holding content.
func archiveFile(tb testing.TB, name string, content string, mode fs.FileMode) ArchiveFile {
	tb.Helper()

	path := filepath.Join(tb.TempDir(), "source")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatalf("WriteFile(%q) failed, %s", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		tb.Fatalf("Chmod(%q) failed, %s", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		tb.Fatalf("Stat(%q) failed, %s", path, err)
	}
	return ArchiveFile{
		Name: name,
		Info: info,
		Open: func() (fs.File, error) {
			return os.Open(filepath.Clean(path))
		},
	}
}

// writeZip returns the zip archive WriteZip produces for files.
func writeZip(tb testing.TB, files []ArchiveFile) *zip.Reader {
	tb.Helper()

	var buf bytes.Buffer
	if err := WriteZip(&buf, files); err != nil {
		tb.Fatalf("WriteZip() failed, %s", err)
	}
	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		tb.Fatalf("zip.NewReader() failed, %s", err)
	}
	return r
}

func TestWriteZipRoundTrip(t *testing.T) {
	want := map[string]string{
		"ffmpeg":      "ffmpeg binary",
		"ffprobe":     "ffprobe binary",
		"license.txt": "",
	}
	files := make([]ArchiveFile, 0, len(want))
	for name, content := range want {
		files = append(files, archiveFile(t, name, content, 0o600))
	}

	got := map[string]string{}
	for _, f := range writeZip(t, files).File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("Open(%q) failed, %s", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("reading %q failed, %s", f.Name, err)
		}
		if err := rc.Close(); err != nil {
			t.Errorf("Close(%q) failed, %s", f.Name, err)
		}
		got[f.Name] = string(content)
	}

	if !maps.Equal(got, want) {
		t.Errorf("archive holds %v, want %v", got, want)
	}
}

func TestWriteZipEmpty(t *testing.T) {
	if got := len(writeZip(t, nil).File); got != 0 {
		t.Errorf("archive holds %d files, want 0", got)
	}
}

func TestWriteZipKeepsFileMode(t *testing.T) {
	r := writeZip(t, []ArchiveFile{
		archiveFile(t, "ffmpeg", "ffmpeg binary", 0o755),
		archiveFile(t, "license", "license text", 0o644),
	})

	want := map[string]fs.FileMode{"ffmpeg": 0o755, "license": 0o644}
	for _, f := range r.File {
		if got := f.Mode().Perm(); got != want[f.Name] {
			t.Errorf("mode of %q = %v, want %v", f.Name, got, want[f.Name])
		}
	}
}

func TestWriteZipCompresses(t *testing.T) {
	content := strings.Repeat("ffmpeg binary ", 10000)
	r := writeZip(t, []ArchiveFile{archiveFile(t, "ffmpeg", content, 0o755)})

	f := r.File[0]
	if f.Method != zip.Deflate {
		t.Errorf("method = %d, want deflate (%d)", f.Method, zip.Deflate)
	}
	if f.CompressedSize64 >= f.UncompressedSize64/10 {
		t.Errorf("compressed %d bytes to %d bytes, want less than a tenth", f.UncompressedSize64, f.CompressedSize64)
	}
}

func TestWriteZipOpenError(t *testing.T) {
	file := archiveFile(t, "ffmpeg", "ffmpeg binary", 0o755)
	wantErr := errors.New("open failed")
	file.Open = func() (fs.File, error) {
		return nil, wantErr
	}

	if err := WriteZip(io.Discard, []ArchiveFile{file}); !errors.Is(err, wantErr) {
		t.Errorf("WriteZip() = %v, want %v", err, wantErr)
	}
}
