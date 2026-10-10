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
	"compress/flate"
	"errors"
	"io"
	"io/fs"
)

// ZipExtension is the file extension of the archives written by WriteZip.
const ZipExtension = ".zip"

// ArchiveFile is a file to store in an archive.
type ArchiveFile struct {
	// Name is the slash separated path of the file inside the archive.
	Name string
	// Info supplies the size, permissions, and modification time of the file.
	Info fs.FileInfo
	// Open returns the content of the file.
	Open func() (fs.File, error)
}

// WriteZip writes files to w as a zip archive compressed with deflate at its
// highest compression level.
func WriteZip(w io.Writer, files []ArchiveFile) error {
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})

	for _, file := range files {
		if err := writeZipFile(zw, file); err != nil {
			return err
		}
	}
	return zw.Close()
}

// writeZipFile adds file to zw.
func writeZipFile(zw *zip.Writer, file ArchiveFile) (err error) {
	header, err := zip.FileInfoHeader(file.Info)
	if err != nil {
		return err
	}
	header.Name = file.Name
	header.Method = zip.Deflate

	dst, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	src, err := file.Open()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, src.Close())
	}()

	_, err = io.Copy(dst, src)
	return err
}
