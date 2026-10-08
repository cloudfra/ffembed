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

// Command ffbundle is the starter CLI entry point new services should replace.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/cloudfra/ffembed/internal/ffbundle"
)

var fileFlag = flag.String("file", "", "Input File")

func main() {
	flag.Parse()
	if err := ffbundle.Run(ffbundle.Args{
		File: *fileFlag,
	}); err != nil {
		slog.Error("ERROR", "error", err)
		os.Exit(1)
	}
}
