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

// ArgError reports an invalid or missing command argument.
type ArgError struct {
	// Arg is the name of the offending argument.
	Arg string
	// Message describes what is wrong with the argument.
	Message string
}

// Error implements the error interface.
func (e *ArgError) Error() string {
	return "invalid argument: " + e.Arg + ": " + e.Message
}
