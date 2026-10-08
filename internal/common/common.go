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

// Package common holds helpers shared by the ffembed commands.
package common

import "strings"

// CSVToList splits a comma separated string into its unique values. Whitespace
// around each value is trimmed and empty values are dropped. The order of the
// returned values is not guaranteed.
func CSVToList(val string) []string {
	vals := strings.Split(val, ",")
	m := map[string]any{}
	for _, v := range vals {
		k := strings.TrimSpace(v)
		if k != "" {
			m[k] = nil
		}
	}
	result := make([]string, len(m))
	i := 0
	for k := range m {
		result[i] = k
		i++
	}
	return result
}
