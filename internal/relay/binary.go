/*
 * Copyright (C) 2017 Genymobile
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package relay

import (
	"fmt"
	"strings"
)

const maxStringPacketSize = 20

// BuildPacketString builds a human-readable string representation of packet data
func BuildPacketString(data []byte) string {
	return BuildPacketStringRange(data, 0, len(data))
}

// BuildPacketStringRange builds a human-readable string representation of packet data with offset and length
func BuildPacketStringRange(data []byte, offset, length int) string {
	limit := min(maxStringPacketSize, length)
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("[%d bytes] ", length))

	for i := 0; i < limit; i++ {
		if i != 0 {
			if i%4 == 0 {
				builder.WriteString("  ")
			} else {
				builder.WriteString(" ")
			}
		}
		builder.WriteString(fmt.Sprintf("%02X", data[offset+i]))
	}

	if limit < length {
		builder.WriteString(fmt.Sprintf("  ... +%d bytes", length-limit))
	}

	return builder.String()
}

// CopyBytes creates a copy of a byte slice
func CopyBytes(data []byte) []byte {
	result := make([]byte, len(data))
	copy(result, data)
	return result
}

// SliceBytes creates a slice view of a byte slice
func SliceBytes(data []byte, offset, length int) []byte {
	return data[offset : offset+length]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
