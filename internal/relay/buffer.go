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
	"io"
)

// StreamBuffer is a circular buffer to store a stream. Read/write boundaries are not preserved.
type StreamBuffer struct {
	data []byte
	head int
	tail int
}

// NewStreamBuffer creates a new StreamBuffer with the given capacity
func NewStreamBuffer(capacity int) *StreamBuffer {
	return &StreamBuffer{
		data: make([]byte, capacity+1),
		head: 0,
		tail: 0,
	}
}

// IsEmpty returns true if the buffer is empty
func (sb *StreamBuffer) IsEmpty() bool {
	return sb.head == sb.tail
}

// IsFull returns true if the buffer is full
func (sb *StreamBuffer) IsFull() bool {
	return (sb.head+1)%len(sb.data) == sb.tail
}

// Size returns the number of bytes stored in the buffer
func (sb *StreamBuffer) Size() int {
	if sb.head < sb.tail {
		return sb.head + len(sb.data) - sb.tail
	}
	return sb.head - sb.tail
}

// Capacity returns the maximum capacity of the buffer
func (sb *StreamBuffer) Capacity() int {
	return len(sb.data) - 1
}

// Remaining returns the number of bytes that can still be written
func (sb *StreamBuffer) Remaining() int {
	return sb.Capacity() - sb.Size()
}

// WriteTo writes the buffer content to a writer
func (sb *StreamBuffer) WriteTo(w io.Writer) (int64, error) {
	if sb.head > sb.tail {
		n, err := w.Write(sb.data[sb.tail:sb.head])
		sb.tail += n
		sb.optimize()
		return int64(n), err
	}

	if sb.head < sb.tail {
		n, err := w.Write(sb.data[sb.tail:])
		sb.tail = (sb.tail + n) % len(sb.data)
		sb.optimize()
		return int64(n), err
	}

	// head == tail means empty buffer
	return 0, nil
}

// ReadFrom reads data from a byte slice into the buffer
func (sb *StreamBuffer) ReadFrom(data []byte) int {
	requested := min(len(data), sb.Remaining())
	if requested <= len(sb.data)-sb.head {
		copy(sb.data[sb.head:], data[:requested])
	} else {
		firstPart := len(sb.data) - sb.head
		copy(sb.data[sb.head:], data[:firstPart])
		copy(sb.data[0:], data[firstPart:requested])
	}
	sb.head = (sb.head + requested) % len(sb.data)
	return requested
}

// optimize resets head and tail to 0 when the buffer is empty to avoid reaching the end of the circular buffer
func (sb *StreamBuffer) optimize() {
	if sb.IsEmpty() {
		sb.head = 0
		sb.tail = 0
	}
}

// DatagramBuffer is a circular buffer to store datagrams (preserving their boundaries)
type DatagramBuffer struct {
	data                 []byte
	head                 int
	tail                 int
	circularBufferLength int
}

const (
	headerLength      = 2
	maxDatagramLength = 1 << 16
	maxBlockLength    = headerLength + maxDatagramLength
)

// NewDatagramBuffer creates a new DatagramBuffer with the given capacity
func NewDatagramBuffer(capacity int) *DatagramBuffer {
	return &DatagramBuffer{
		data:                 make([]byte, capacity+maxBlockLength),
		head:                 0,
		tail:                 0,
		circularBufferLength: capacity + 1,
	}
}

// IsEmpty returns true if the buffer is empty
func (db *DatagramBuffer) IsEmpty() bool {
	return db.head == db.tail
}

// HasEnoughSpaceFor checks if there's enough space for a datagram of the given length
func (db *DatagramBuffer) HasEnoughSpaceFor(datagramLength int) bool {
	if db.head >= db.tail {
		// there is at least the extra space for storing 1 packet
		return true
	}
	remaining := db.tail - db.head - 1 // 1 extra byte to distinguish empty vs full
	return headerLength+datagramLength <= remaining
}

// Capacity returns the capacity of the buffer
func (db *DatagramBuffer) Capacity() int {
	return db.circularBufferLength - 1
}

// WriteTo writes the next datagram to a writer
func (db *DatagramBuffer) WriteTo(w io.Writer) (bool, error) {
	length := db.readLength()
	data := db.data[db.tail : db.tail+length]
	db.tail += length
	if db.tail >= db.circularBufferLength {
		db.tail = 0
	}

	n, err := w.Write(data)
	if err != nil {
		return false, err
	}
	if n != length {
		LogeSimple("DatagramBuffer", "Cannot write the whole datagram to the channel")
		return false, nil
	}
	return true, nil
}

// ReadFrom reads a datagram from a byte slice
func (db *DatagramBuffer) ReadFrom(data []byte) bool {
	length := len(data)
	if length > maxDatagramLength {
		LogeSimple("DatagramBuffer", "Datagram length may not be greater than maxDatagramLength")
		return false
	}
	if !db.HasEnoughSpaceFor(length) {
		return false
	}

	db.writeLength(length)
	copy(db.data[db.head:], data)
	db.head += length
	if db.head >= db.circularBufferLength {
		db.head = 0
	}
	return true
}

func (db *DatagramBuffer) writeLength(length int) {
	db.data[db.head] = byte((length >> 8) & 0xff)
	db.head++
	db.data[db.head] = byte(length & 0xff)
	db.head++
}

func (db *DatagramBuffer) readLength() int {
	length := (int(db.data[db.tail]) << 8) | int(db.data[db.tail+1])
	db.tail += 2
	return length
}
