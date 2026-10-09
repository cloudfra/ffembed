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
	"archive/tar"
	"io"

	"github.com/mholt/archives"
	"github.com/ulikunitz/xz"
)

const (
	// TarXzExtension is the file extension of the archives written by TarXz.
	TarXzExtension = ".tar.xz"
	// xzDictCap is the size of the xz dictionary, which is how far back the
	// compressor can look for repeated data. 64 MiB is what xz -9 uses and is
	// the largest dictionary the archives xz reader accepts.
	xzDictCap = 64 << 20
)

// MaxXz is the archives xz format tuned for the smallest output rather than
// the library defaults.
type MaxXz struct {
	archives.Xz
}

// OpenWriter returns an xz writer that uses the largest supported dictionary.
// The default hash table matcher is kept because the binary tree matcher of
// the xz package produces larger output.
func (MaxXz) OpenWriter(w io.Writer) (io.WriteCloser, error) {
	return xz.WriterConfig{DictCap: xzDictCap}.NewWriter(w)
}

// TarXz returns the format of a tar archive compressed with MaxXz.
func TarXz() archives.CompressedArchive {
	return archives.CompressedArchive{
		Compression: MaxXz{},
		Archival: archives.Tar{
			Format:        tar.FormatGNU,
			NumericUIDGID: true,
		},
		Extraction: archives.Tar{},
	}
}
