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

//go:build linux && amd64

package ffembed

import (
	"embed"
)

// embedded holds the bundle when one was placed in the directory before the
// build. The directory is embedded rather than the bundle so that the build
// works without one.
//
//go:embed all:bin/linux_amd64
var embedded embed.FS

const embeddedPath = "bin/linux_amd64/" + bundleName
